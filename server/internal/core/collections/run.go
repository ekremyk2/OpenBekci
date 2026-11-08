package collections

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func debtorKey(d billing.Debtor) string {
	if d.IsPerson() {
		return "p:" + d.PersonID.String()
	}
	return "u:" + d.UnitID.String() + ":" + d.Kind
}

// debtorState is a debtor's position on a day.
type debtorState struct {
	D          billing.Debtor
	Key        string
	Person     *uuid.UUID
	Overdue    int64
	Total      int64
	OldestDue  string
	Days       int
	Frozen     bool
	Period     string
	oldestTime time.Time
}

// debtorStates lists the debtors of a site that have overdue open charges on today, with
// the person who currently holds the side, the days past the oldest due date and whether a
// collection case freezes them.
func debtorStates(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site, today string) ([]debtorState, error) {
	tz := ledger.Location(site.Timezone)
	now, _ := time.ParseInLocation(ledger.DateLayout, today, tz)
	rows, err := tx.Query(ctx, `
		SELECT c.unit_id, c.debtor_kind, c.debtor_person_id, c.due_date::text,
			c.amount - COALESCE((SELECT SUM(a.amount) FROM payment_allocation a WHERE a.org_id = c.org_id AND a.charge_id = c.id AND NOT a.voided), 0)::bigint
		FROM charge c WHERE c.org_id=$1 AND c.site_id=$2 AND c.cancel_entry_id IS NULL AND c.status IN ('open', 'partially_paid') AND c.amount > 0
		ORDER BY c.due_date, c.id`, orgID, site.ID)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*debtorState{}
	var order []string
	for rows.Next() {
		var unit uuid.UUID
		var kind string
		var person *uuid.UUID
		var due string
		var rem int64
		if err := rows.Scan(&unit, &kind, &person, &due, &rem); err != nil {
			rows.Close()
			return nil, err
		}
		if rem <= 0 {
			continue
		}
		var d billing.Debtor
		if kind == "person" {
			d = billing.Debtor{PersonID: person}
		} else {
			u := unit
			d = billing.Debtor{UnitID: &u, Kind: kind}
		}
		k := debtorKey(d)
		st := byKey[k]
		if st == nil {
			st = &debtorState{D: d, Key: k}
			byKey[k] = st
			order = append(order, k)
		}
		st.Total += rem
		if due < today {
			st.Overdue += rem
			if st.OldestDue == "" || due < st.OldestDue {
				st.OldestDue = due
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	frozen := map[string]bool{}
	crows, err := tx.Query(ctx, `SELECT unit_id, debtor_kind, debtor_person_id FROM collection_case WHERE org_id=$1 AND site_id=$2 AND status='open'`, orgID, site.ID)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var unit, person *uuid.UUID
		var kind *string
		if err := crows.Scan(&unit, &kind, &person); err != nil {
			crows.Close()
			return nil, err
		}
		frozen[debtorKey(debtorOf(unit, kind, person))] = true
	}
	crows.Close()
	var out []debtorState
	for _, k := range order {
		st := byKey[k]
		if st.Overdue <= 0 {
			continue
		}
		due, _ := time.ParseInLocation(ledger.DateLayout, st.OldestDue, tz)
		st.Days = int(now.Sub(due).Hours() / 24)
		st.Period = st.OldestDue[:7]
		st.Frozen = frozen[k]
		if st.D.IsPerson() {
			st.Person = st.D.PersonID
		} else if p, _, err := occupantName(ctx, tx, orgID, *st.D.UnitID, st.D.Kind, today); err != nil {
			return nil, err
		} else {
			st.Person = p
		}
		out = append(out, *st)
	}
	return out, nil
}

func loadStage(ctx context.Context, tx pgx.Tx, orgID, stageID uuid.UUID) (Stage, error) {
	return scanStage(tx.QueryRow(ctx, stageSel+` WHERE org_id=$1 AND id=$2`, orgID, stageID))
}

// RunInput selects a manual reminder run.
type RunInput struct {
	SiteID     uuid.UUID
	StageID    uuid.UUID
	Debtors    []billing.Debtor
	MinBalance *money.Money
	Channels   []string
}

// Preview is the dry run of a reminder run.
type Preview struct {
	RecipientCount int `json:"recipient_count"`
	SMSCount       int `json:"sms_count"`
	SkippedFrozen  int `json:"skipped_frozen"`
}

// Run is a created reminder run.
type Run struct {
	ID         uuid.UUID `json:"id"`
	SiteID     uuid.UUID `json:"site_id"`
	StageID    uuid.UUID `json:"stage_id"`
	EventCount int       `json:"event_count"`
	SMSCount   int       `json:"sms_count"`
	CreatedAt  time.Time `json:"created_at"`
}

// selectRecipients applies a manual run's filters: explicit debtors, or every
// debtor whose overdue days reach the stage and whose overdue balance reaches min.
func selectRecipients(states []debtorState, in RunInput, stage Stage) (recipients []debtorState, frozen int) {
	var min int64 = 1
	if in.MinBalance != nil && in.MinBalance.Amount > min {
		min = in.MinBalance.Amount
	}
	only := map[string]bool{}
	for _, d := range in.Debtors {
		only[debtorKey(d)] = true
	}
	for _, st := range states {
		if len(only) > 0 {
			if !only[st.Key] {
				continue
			}
		} else if st.Days < stage.DaysAfterDue {
			continue
		}
		if st.Overdue < min {
			continue
		}
		if st.Frozen {
			frozen++
			continue
		}
		recipients = append(recipients, st)
	}
	return
}

func (s *Service) prepareRun(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, in RunInput) (tenancy.Site, Stage, []debtorState, int, error) {
	site, err := ledger.SiteFor(ctx, tx, ledger.PermCollectionsManage, orgID, in.SiteID)
	if err != nil {
		return site, Stage{}, nil, 0, err
	}
	stage, err := loadStage(ctx, tx, orgID, in.StageID)
	if err != nil || stage.SiteID != in.SiteID {
		return site, stage, nil, 0, bad("stage_id", "invalid_value")
	}
	if err := validChannels(in.Channels, "channels"); err != nil {
		return site, stage, nil, 0, err
	}
	for i, d := range in.Debtors {
		if err := d.Validate(); err != nil {
			return site, stage, nil, 0, problem.Validation(problem.Field{Field: fmt.Sprintf("debtors.%d", i), Code: "invalid_value"})
		}
	}
	if in.MinBalance != nil && in.MinBalance.Currency != ledger.Currency(site) {
		return site, stage, nil, 0, bad("min_balance", "invalid_value")
	}
	states, err := debtorStates(ctx, tx, orgID, site, ledger.Today(site.Timezone, s.Now()))
	if err != nil {
		return site, stage, nil, 0, err
	}
	rec, frozen := selectRecipients(states, in, stage)
	return site, stage, rec, frozen, nil
}

// PreviewRun counts the recipients of a run without sending (collections.manage).
func (s *Service) PreviewRun(ctx context.Context, orgID uuid.UUID, in RunInput) (Preview, error) {
	var out Preview
	err := s.run(ctx, orgID, "dunning stage", func(tx pgx.Tx) error {
		_, _, rec, frozen, err := s.prepareRun(ctx, tx, orgID, in)
		if err != nil {
			return err
		}
		out = Preview{RecipientCount: len(rec), SkippedFrozen: frozen}
		if slices.Contains(in.Channels, "sms") {
			out.SMSCount = len(rec)
		}
		return nil
	})
	return out, err
}

// CreateRun sends a reminder run (collections.manage): per recipient and channel one
// event row, and a dunning.reminder notification job for the message channels or the
// stored letter for the letter channel. Debtors with an open collection case are skipped.
func (s *Service) CreateRun(ctx context.Context, orgID uuid.UUID, in RunInput) (Run, error) {
	var out Run
	err := s.run(ctx, orgID, "dunning stage", func(tx pgx.Tx) error {
		site, stage, rec, _, err := s.prepareRun(ctx, tx, orgID, in)
		if err != nil {
			return err
		}
		out = Run{ID: id.New(), SiteID: in.SiteID, StageID: in.StageID}
		if _, err := tx.Exec(ctx, `INSERT INTO dunning_run (id, org_id, site_id, stage_id, created_by) VALUES ($1,$2,$3,$4,$5)`, out.ID, orgID, in.SiteID, in.StageID, ledger.Actor(ctx)); err != nil {
			return err
		}
		for _, st := range rec {
			for _, ch := range in.Channels {
				if _, err := s.fire(ctx, tx, orgID, site, stage, st, ch, &out.ID, "run"); err != nil {
					return err
				}
				out.EventCount++
				if ch == "sms" {
					out.SMSCount++
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE dunning_run SET event_count=$3, sms_count=$4 WHERE org_id=$1 AND id=$2`, orgID, out.ID, out.EventCount, out.SMSCount); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "dunning_run.create", "dunning_run", out.ID, nil, map[string]any{"stage_id": in.StageID, "events": out.EventCount}); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT created_at FROM dunning_run WHERE org_id=$1 AND id=$2`, orgID, out.ID).Scan(&out.CreatedAt)
	})
	out.CreatedAt = out.CreatedAt.UTC()
	return out, err
}

// fire records one event and emits its notification (or stores the letter). A job
// event that already exists (same debtor, stage, period, channel) is skipped; the
// result reports whether an event was created.
func (s *Service) fire(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site, stage Stage, st debtorState, channel string, runID *uuid.UUID, source string) (bool, error) {
	var letter []byte
	if channel == "letter" {
		var err error
		if letter, err = s.letterTx(ctx, tx, orgID, site, st.D, ledger.Today(site.Timezone, s.Now())); err != nil {
			return false, err
		}
	}
	eid := id.New()
	var unit *uuid.UUID
	var kind *string
	if !st.D.IsPerson() {
		unit, kind = st.D.UnitID, &st.D.Kind
	}
	ct, err := tx.Exec(ctx, `INSERT INTO dunning_event (id, org_id, site_id, run_id, stage_id, source, unit_id, debtor_kind, person_id, debtor_key, period, channel, amount_due, letter)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT DO NOTHING`,
		eid, orgID, site.ID, runID, stage.ID, source, unit, kind, st.Person, st.Key, st.Period, channel, st.Overdue, letter)
	if err != nil {
		return false, err
	}
	if ct.RowsAffected() == 0 {
		return false, nil
	}
	if channel == "letter" || s.Notifier == nil {
		return true, nil
	}
	payload, _ := json.Marshal(map[string]any{"unit_id": unit, "debtor_kind": kind, "person_id": st.Person, "stage_id": stage.ID, "stage": stage.Name, "channel": channel,
		"amount_due": money.New(st.Overdue, ledger.Currency(site)), "event_id": eid})
	return true, s.Notifier.Enqueue(ctx, tx, ledger.Notification{Type: ledger.NotifyDunningReminder, OrgID: orgID, SiteID: site.ID, Payload: payload})
}

// RunStages is the hourly catch-up of a site's dunning stages (collections.manage; the job
// runs with the system context). For every debtor with an overdue balance and no open
// collection case it fires the highest enabled stage whose days after the oldest due date
// have passed, provided that stage escalates past what the debtor already received for that
// delinquency (the period is the month of the oldest overdue charge). Events are unique per
// debtor, stage, period and channel, so a repeated or missed run neither doubles nor skips
// a reminder. It returns the number of events created.
func (s *Service) RunStages(ctx context.Context, orgID, siteID uuid.UUID) (int, error) {
	n := 0
	err := s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		n = 0
		site, err := ledger.SiteFor(ctx, tx, ledger.PermCollectionsManage, orgID, siteID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, stageSel+` WHERE org_id=$1 AND site_id=$2 AND enabled ORDER BY days_after_due DESC, position`, orgID, siteID)
		if err != nil {
			return err
		}
		var stages []Stage
		for rows.Next() {
			st, err := scanStage(rows)
			if err != nil {
				rows.Close()
				return err
			}
			stages = append(stages, st)
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(stages) == 0 {
			return err
		}
		states, err := debtorStates(ctx, tx, orgID, site, ledger.Today(site.Timezone, s.Now()))
		if err != nil {
			return err
		}
		// the highest stage (by days) each debtor and period has received
		sent := map[string]int{}
		erows, err := tx.Query(ctx, `SELECT e.debtor_key, e.period, MAX(s.days_after_due) FROM dunning_event e JOIN dunning_stage s ON s.org_id = e.org_id AND s.id = e.stage_id
			WHERE e.org_id=$1 AND e.site_id=$2 GROUP BY e.debtor_key, e.period`, orgID, siteID)
		if err != nil {
			return err
		}
		for erows.Next() {
			var k, p string
			var days int
			if err := erows.Scan(&k, &p, &days); err != nil {
				erows.Close()
				return err
			}
			sent[k+"|"+p] = days
		}
		erows.Close()
		for _, st := range states {
			if st.Frozen {
				continue
			}
			prev, had := sent[st.Key+"|"+st.Period]
			for _, stage := range stages { // highest days first
				if st.Days < stage.DaysAfterDue || (had && stage.DaysAfterDue <= prev) {
					continue
				}
				for _, ch := range stage.Channels {
					made, err := s.fire(ctx, tx, orgID, site, stage, st, ch, nil, "job")
					if err != nil {
						return err
					}
					if made {
						n++
					}
				}
				break
			}
		}
		return nil
	})
	return n, err
}

// Event is a reminder or notice to a debtor as the API shows it.
type Event struct {
	ID             uuid.UUID   `json:"id"`
	OrgID          uuid.UUID   `json:"org_id"`
	SiteID         uuid.UUID   `json:"site_id"`
	RunID          *uuid.UUID  `json:"run_id"`
	UnitID         *uuid.UUID  `json:"unit_id"`
	DebtorKind     *string     `json:"debtor_kind"`
	PersonID       *uuid.UUID  `json:"person_id"`
	StageID        uuid.UUID   `json:"stage_id"`
	Period         string      `json:"period"`
	SentAt         time.Time   `json:"sent_at"`
	Channel        string      `json:"channel"`
	AmountDue      int64       `json:"-"`
	AmountDueMoney money.Money `json:"amount_due"`
	HasLetter      bool        `json:"has_letter"`
	DocumentFileID *uuid.UUID  `json:"document_file_id"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// EventFilter filters ListEvents.
type EventFilter struct {
	UnitID     *uuid.UUID
	DebtorKind string
	StageID    *uuid.UUID
	From, To   string
}

// ListEvents lists the reminders sent (money.read).
func (s *Service) ListEvents(ctx context.Context, orgID, siteID uuid.UUID, f EventFilter, p ledger.PageParams) ([]Event, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Event
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID)
		if err != nil {
			return err
		}
		cur := ledger.Currency(site)
		rows, err := tx.Query(ctx, `SELECT id, org_id, site_id, run_id, unit_id, debtor_kind, person_id, stage_id, period, sent_at, channel, amount_due, letter IS NOT NULL, document_file_id, created_at, updated_at
			FROM dunning_event WHERE org_id=$1 AND site_id=$2 AND id > $3 AND ($4::uuid IS NULL OR unit_id = $4) AND ($5 = '' OR debtor_kind = $5) AND ($6::uuid IS NULL OR stage_id = $6)
			AND ($7 = '' OR sent_at >= $7::date) AND ($8 = '' OR sent_at < $8::date + 1) ORDER BY id LIMIT $9`, orgID, siteID, after, f.UnitID, f.DebtorKind, f.StageID, f.From, f.To, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.ID, &e.OrgID, &e.SiteID, &e.RunID, &e.UnitID, &e.DebtorKind, &e.PersonID, &e.StageID, &e.Period, &e.SentAt, &e.Channel, &e.AmountDue, &e.HasLetter, &e.DocumentFileID, &e.CreatedAt, &e.UpdatedAt); err != nil {
				return err
			}
			e.AmountDueMoney = money.New(e.AmountDue, cur)
			e.SentAt, e.CreatedAt, e.UpdatedAt = e.SentAt.UTC(), e.CreatedAt.UTC(), e.UpdatedAt.UTC()
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(e Event) uuid.UUID { return e.ID })
	return items, next, nil
}
