package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Dissent is a muhalefet serhi: a dissenting owner's recorded objection.
type Dissent struct {
	PersonID    uuid.UUID `json:"person_id"`
	DisplayName *string   `json:"display_name"`
	Text        string    `json:"text"`
}

// DissentInput is a dissent as the client sends it.
type DissentInput struct {
	PersonID uuid.UUID `json:"person_id"`
	Text     string    `json:"text"`
}

// Effect is the change a decision created (G5).
type Effect struct {
	Kind       string    `json:"kind"`
	RecordType string    `json:"record_type"`
	RecordID   uuid.UUID `json:"record_id"`
	AppliedAt  time.Time `json:"applied_at"`
	AppliedBy  uuid.UUID `json:"applied_by"`
}

// Decision is a numbered decision of a meeting.
type Decision struct {
	ID                uuid.UUID  `json:"id"`
	OrgID             uuid.UUID  `json:"org_id"`
	SiteID            uuid.UUID  `json:"site_id"`
	MeetingID         uuid.UUID  `json:"meeting_id"`
	AgendaItemID      *uuid.UUID `json:"agenda_item_id"`
	SequenceNo        int        `json:"sequence_no"`
	Year              int        `json:"year"`
	Text              string     `json:"text"`
	Outcome           string     `json:"outcome"`
	// OutcomeOverrideReason is set when the outcome was recorded against, or
	// without, the threshold evaluation of the counts (an explicit override).
	OutcomeOverrideReason *string    `json:"outcome_override_reason"`
	VoteCount             *VoteCount `json:"vote_count"`
	Dissents          []Dissent  `json:"dissents"`
	ConcernsResidents bool       `json:"concerns_residents"`
	NotifiedAt        *time.Time `json:"notified_at"`
	Effect            *Effect    `json:"effect"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	Version           int        `json:"version"`

	inputHash string
}

const decisionColumns = `id, org_id, site_id, meeting_id, agenda_item_id, sequence_no, year, text, outcome, outcome_override_reason, vote_count, dissents, concerns_residents, notified_at, effect,
	COALESCE(effect->>'input_hash', ''), created_at, updated_at, version`

func scanDecision(row pgx.Row) (Decision, error) {
	var d Decision
	var vc, ds, ef []byte
	err := row.Scan(&d.ID, &d.OrgID, &d.SiteID, &d.MeetingID, &d.AgendaItemID, &d.SequenceNo, &d.Year, &d.Text, &d.Outcome, &d.OutcomeOverrideReason, &vc, &ds, &d.ConcernsResidents, &d.NotifiedAt, &ef,
		&d.inputHash, &d.CreatedAt, &d.UpdatedAt, &d.Version)
	if err != nil {
		return d, err
	}
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	if d.NotifiedAt != nil {
		t := d.NotifiedAt.UTC()
		d.NotifiedAt = &t
	}
	d.Dissents = []Dissent{}
	if len(ds) > 0 {
		_ = json.Unmarshal(ds, &d.Dissents)
	}
	if len(vc) > 0 && string(vc) != "null" {
		var v VoteCount
		if json.Unmarshal(vc, &v) == nil {
			d.VoteCount = &v
		}
	}
	if len(ef) > 0 && string(ef) != "null" {
		var e Effect
		if json.Unmarshal(ef, &e) == nil {
			d.Effect = &e
		}
	}
	return d, nil
}

func loadDecision(ctx context.Context, tx pgx.Tx, orgID, id uuid.UUID, forUpdate bool) (Decision, error) {
	q := `SELECT ` + decisionColumns + ` FROM decision WHERE org_id = $1 AND id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	d, err := scanDecision(tx.QueryRow(ctx, q, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return d, problem.NotFound("no such decision")
	}
	return d, err
}

func (d Decision) auditView() map[string]any {
	return map[string]any{"meeting_id": d.MeetingID, "agenda_item_id": d.AgendaItemID, "sequence_no": d.SequenceNo, "year": d.Year, "text": d.Text,
		"outcome": d.Outcome, "outcome_override_reason": d.OutcomeOverrideReason, "dissents": d.Dissents, "concerns_residents": d.ConcernsResidents, "effect": d.Effect}
}

// decisionAt loads a decision and checks perm at its site.
func (s *Service) decisionAt(ctx context.Context, tx pgx.Tx, perm authz.Permission, orgID, id uuid.UUID, forUpdate bool) (Decision, authz.Scope, error) {
	d, err := loadDecision(ctx, tx, orgID, id, forUpdate)
	if err != nil {
		return d, authz.Scope{}, err
	}
	sc, err := requireSite(ctx, tx, perm, d.SiteID)
	return d, sc, err
}

// visible reports whether the viewer may see the decision: staff and owners
// see all; tenants and household only decisions that concern residents.
func (d Decision) visible(v viewer) bool { return v.staff || v.owner || d.ConcernsResidents }

// redact hides what only staff (and, where the site allows it, owners) see:
// the dissents with the dissenters' names and text, and the vote counts.
// Tenants never see them.
func (d Decision) redact(v viewer, ownersMay bool) Decision {
	if v.staff || (v.owner && ownersMay) {
		return d
	}
	d.Dissents = []Dissent{}
	d.VoteCount = nil
	return d
}

// DecisionFilter narrows ListDecisions.
type DecisionFilter struct {
	MeetingID         *uuid.UUID
	Year              *int64
	Outcome           string
	ConcernsResidents *bool
	Q                 string
}

// ListDecisions is the searchable decision history of a site, with sequence
// numbers for the karar defteri. It needs meetings.read; residents who are not
// owners only see decisions marked as concerning them.
func (s *Service) ListDecisions(ctx context.Context, orgID, siteID uuid.UUID, f DecisionFilter, p tenancy.PageParams) ([]Decision, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Decision
	var ownersMay bool
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := requireSite(ctx, tx, PermMeetingsRead, siteID)
		if err != nil {
			return err
		}
		v := viewerOf(ctx, sc)
		set, err := loadSettings(ctx, tx, orgID, siteID, false)
		if err != nil {
			return err
		}
		ownersMay = set.Settings.DecisionDetailsVisibleToOwners
		rows, err := tx.Query(ctx, `SELECT `+decisionColumns+` FROM decision WHERE org_id = $1 AND site_id = $2 AND id > $3
			AND ($4::uuid IS NULL OR meeting_id = $4) AND ($5::int IS NULL OR year = $5) AND ($6 = '' OR outcome = $6)
			AND ($7::boolean IS NULL OR concerns_residents = $7) AND ($8 = '' OR text ILIKE '%' || $8 || '%')
			AND ($9 OR concerns_residents) ORDER BY id LIMIT $10`,
			orgID, siteID, after, f.MeetingID, f.Year, f.Outcome, f.ConcernsResidents, f.Q, v.staff || v.owner, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDecision(rows)
			if err != nil {
				return err
			}
			out = append(out, d.redact(v, ownersMay))
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbErr(err, "list decisions")
	}
	items, next := tenancy.Page(out, limit, func(d Decision) uuid.UUID { return d.ID })
	return items, next, nil
}

// GetDecision returns a decision. It needs meetings.read.
func (s *Service) GetDecision(ctx context.Context, orgID, id uuid.UUID) (Decision, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return Decision{}, err
	}
	var out Decision
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		d, sc, err := s.decisionAt(ctx, tx, PermMeetingsRead, orgID, id, false)
		if err != nil {
			return err
		}
		v := viewerOf(ctx, sc)
		if !d.visible(v) {
			return problem.NotFound("no such decision")
		}
		set, err := loadSettings(ctx, tx, orgID, d.SiteID, false)
		if err != nil {
			return err
		}
		out = d.redact(v, set.Settings.DecisionDetailsVisibleToOwners)
		return nil
	})
	return out, dbErr(err, "decision")
}

// DecisionCreate is the input of CreateDecision.
type DecisionCreate struct {
	SiteID            uuid.UUID      `json:"site_id"`
	MeetingID         uuid.UUID      `json:"meeting_id"`
	AgendaItemID      *uuid.UUID     `json:"agenda_item_id"`
	Text              string         `json:"text"`
	Outcome           string         `json:"outcome"`
	Dissents          []DissentInput `json:"dissents"`
	ConcernsResidents bool           `json:"concerns_residents"`
	// OutcomeOverride records an accepted or rejected outcome that the counts
	// do not support (no counts, undetermined shares, or a contradicting
	// threshold). The reason is mandatory and audited.
	OutcomeOverride *OutcomeOverride `json:"outcome_override"`
}

// OutcomeOverride is the explicit, reasoned override of the threshold evaluation.
type OutcomeOverride struct {
	Reason string `json:"reason"`
}

var outcomes = []string{"accepted", "rejected", "no_vote", "noted"}

// resolveDissents names the dissenters; each must be an owner called to the meeting.
func resolveDissents(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, snap Snapshot, in []DissentInput) ([]Dissent, error) {
	out := []Dissent{}
	for i, d := range in {
		if strings.TrimSpace(d.Text) == "" {
			return nil, problem.Validation(problem.Field{Field: "dissents[" + strconv.Itoa(i) + "].text", Code: "required"})
		}
		owner, ok := snap.Owner(d.PersonID)
		if !ok {
			return nil, problem.Validation(problem.Field{Field: "dissents[" + strconv.Itoa(i) + "].person_id", Code: "invalid_value", Params: map[string]any{"reason": "not_an_owner_called_to_the_meeting"}})
		}
		out = append(out, Dissent{PersonID: d.PersonID, DisplayName: ptr(owner.DisplayName), Text: d.Text})
	}
	return out, nil
}

// checkOutcome ties accepted and rejected to a voted item: the counts must
// exist and the site's threshold for the topic must agree with the outcome.
func (s *Service) checkOutcome(ctx context.Context, tx pgx.Tx, m Meeting, item *AgendaItem, outcome string, override *OutcomeOverride) (*VoteCount, error) {
	counts := func() *VoteCount {
		if item != nil && item.WillBeVoted {
			if v, err := s.voteCountOf(ctx, tx, m, *item); err == nil && v.RecordedAt != nil {
				return &v
			}
		}
		return nil
	}
	if outcome != "accepted" && outcome != "rejected" {
		return counts(), nil
	}
	if override != nil {
		if strings.TrimSpace(override.Reason) == "" {
			return nil, problem.Validation(problem.Field{Field: "outcome_override.reason", Code: "required"})
		}
		return counts(), nil
	}
	bad := func(reason string, extra map[string]any) error {
		p := map[string]any{"reason": reason}
		for k, v := range extra {
			p[k] = v
		}
		return problem.Validation(problem.Field{Field: "outcome", Code: "invalid_value", Params: p})
	}
	if item == nil || !item.WillBeVoted {
		return nil, bad("needs_a_voted_agenda_item", nil)
	}
	v, err := s.voteCountOf(ctx, tx, m, *item)
	if err != nil {
		return nil, err
	}
	if v.RecordedAt == nil || v.YesCount+v.NoCount+v.AbstainCount == 0 {
		return nil, bad("vote_counts_required", nil)
	}
	if v.OutcomeHint == HintUndetermined {
		return nil, bad("threshold_undetermined", map[string]any{"hint": "record the shares, or use outcome_override with a reason"})
	}
	if v.OutcomeHint != outcome {
		return nil, bad("contradicts_the_threshold", map[string]any{"threshold_says": v.OutcomeHint})
	}
	return &v, nil
}

// CreateDecision records a decision and gives it the next number of the site
// and year (gap-free: the counter row is locked in this transaction). Accepted
// and rejected need a voted agenda item with counts that agree. It needs decisions.manage.
func (s *Service) CreateDecision(ctx context.Context, orgID uuid.UUID, in DecisionCreate) (Decision, error) {
	if err := authz.RequireAnywhere(ctx, PermDecisionsManage); err != nil {
		return Decision{}, err
	}
	var fs []problem.Field
	if strings.TrimSpace(in.Text) == "" {
		fs = append(fs, problem.Field{Field: "text", Code: "required"})
	}
	if !slices.Contains(outcomes, in.Outcome) {
		fs = append(fs, problem.Field{Field: "outcome", Code: "invalid_value"})
	}
	if len(fs) > 0 {
		return Decision{}, problem.Validation(fs...)
	}
	var out Decision
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := requireSite(ctx, tx, PermDecisionsManage, in.SiteID)
		if err != nil {
			return err
		}
		m, err := loadMeeting(ctx, tx, orgID, in.MeetingID, true)
		if err != nil {
			return err
		}
		if m.SiteID != in.SiteID {
			return problem.Validation(problem.Field{Field: "meeting_id", Code: "invalid_value"})
		}
		if m.Status != StatusInSession && m.Status != StatusClosed {
			return problem.Conflict("decisions are recorded in the session or after it")
		}
		snap, err := loadSnapshot(m.EligibilitySnapshot)
		if err != nil {
			return err
		}
		var item *AgendaItem
		if in.AgendaItemID != nil {
			a, err := loadAgendaItem(ctx, tx, orgID, *in.AgendaItemID, true)
			if err != nil || a.MeetingID != m.ID {
				if err == nil || isNotFound(err) {
					return problem.Validation(problem.Field{Field: "agenda_item_id", Code: "invalid_value"})
				}
				return err
			}
			if a.DecisionID != nil {
				return problem.Conflict("this agenda item already has a decision")
			}
			item = &a
		}
		vc, err := s.checkOutcome(ctx, tx, m, item, in.Outcome, in.OutcomeOverride)
		if err != nil {
			return err
		}
		var overrideReason *string
		if in.OutcomeOverride != nil && (in.Outcome == "accepted" || in.Outcome == "rejected") {
			overrideReason = ptr(strings.TrimSpace(in.OutcomeOverride.Reason))
		}
		dissents, err := resolveDissents(ctx, tx, orgID, snap, in.Dissents)
		if err != nil {
			return err
		}
		si, err := loadSiteInfo(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		year := m.ScheduledAt.In(si.Loc).Year()
		var seq int
		if err := tx.QueryRow(ctx, `INSERT INTO decision_counter (org_id, site_id, year, last_no) VALUES ($1, $2, $3, 1)
			ON CONFLICT (org_id, site_id, year) DO UPDATE SET last_no = decision_counter.last_no + 1 RETURNING last_no`, orgID, in.SiteID, year).Scan(&seq); err != nil {
			return err
		}
		did := newID()
		if _, err := tx.Exec(ctx, `INSERT INTO decision (id, org_id, site_id, meeting_id, agenda_item_id, sequence_no, year, text, outcome, vote_count, dissents, concerns_residents, created_by, outcome_override_reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			did, orgID, in.SiteID, in.MeetingID, in.AgendaItemID, seq, year, in.Text, in.Outcome, jsonOrNil(vc), mustJSON(dissents), in.ConcernsResidents, principalID(ctx), overrideReason); err != nil {
			return err
		}
		if item != nil {
			if _, err := tx.Exec(ctx, `UPDATE agenda_item SET decision_id = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, item.ID, did); err != nil {
				return err
			}
		}
		if out, err = loadDecision(ctx, tx, orgID, did, false); err != nil {
			return err
		}
		diff, err := audit.Diff("decision", nil, out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "decision.create", TargetType: "decision", TargetID: did, Diff: diff})
	})
	if tenancy.IsUnique(err, "decision_one_per_item") {
		return Decision{}, problem.Conflict("this agenda item already has a decision")
	}
	return out, dbErr(err, "decision")
}

func jsonOrNil(v *VoteCount) any {
	if v == nil {
		return nil
	}
	return mustJSON(v)
}

// UpdateDecision corrects a decision (text, outcome, dissents, whether it
// concerns residents) if it is still at expectedVersion. The number never
// changes. Text and outcome are locked once an effect was applied. It needs decisions.manage.
func (s *Service) UpdateDecision(ctx context.Context, orgID, id uuid.UUID, expectedVersion int, d tenancy.Doc) (Decision, error) {
	if err := authz.RequireAnywhere(ctx, PermDecisionsManage); err != nil {
		return Decision{}, err
	}
	fs := d.Unknown("text", "outcome", "dissents", "concerns_residents")
	var out Decision
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		cur, sc, err := s.decisionAt(ctx, tx, PermDecisionsManage, orgID, id, true)
		if err != nil {
			return err
		}
		if cur.Version != expectedVersion {
			return errVersion
		}
		next := cur
		if err := d.Get("text", &next.Text); err != nil {
			return err
		}
		if err := d.Get("outcome", &next.Outcome); err != nil {
			return err
		}
		if err := d.Get("concerns_residents", &next.ConcernsResidents); err != nil {
			return err
		}
		if strings.TrimSpace(next.Text) == "" {
			fs = append(fs, problem.Field{Field: "text", Code: "required"})
		}
		if !slices.Contains(outcomes, next.Outcome) {
			fs = append(fs, problem.Field{Field: "outcome", Code: "invalid_value"})
		}
		if len(fs) > 0 {
			return problem.Validation(fs...)
		}
		if cur.Effect != nil && (next.Text != cur.Text || next.Outcome != cur.Outcome) {
			return problem.Conflict("the decision already created a change; its text and outcome are locked")
		}
		m, err := loadMeeting(ctx, tx, orgID, cur.MeetingID, false)
		if err != nil {
			return err
		}
		snap, err := loadSnapshot(m.EligibilitySnapshot)
		if err != nil {
			return err
		}
		final := m.Status == StatusClosed
		if final && (next.Text != cur.Text || next.Outcome != cur.Outcome || next.ConcernsResidents != cur.ConcernsResidents) {
			return problem.Conflict("the meeting is closed: its decisions are final, only dissents can still be added within the window")
		}
		if d.Has("dissents") {
			var in []DissentInput
			if err := d.Get("dissents", &in); err != nil {
				return err
			}
			if next.Dissents, err = resolveDissents(ctx, tx, orgID, snap, in); err != nil {
				return err
			}
			if final {
				if err := s.checkDissentAddition(ctx, tx, m, cur.Dissents, next.Dissents); err != nil {
					return err
				}
			}
		}
		var item *AgendaItem
		if cur.AgendaItemID != nil {
			a, err := loadAgendaItem(ctx, tx, orgID, *cur.AgendaItemID, false)
			if err != nil {
				return err
			}
			item = &a
		}
		vc := cur.VoteCount
		if next.Outcome != cur.Outcome {
			if vc, err = s.checkOutcome(ctx, tx, m, item, next.Outcome, nil); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE decision SET text = $3, outcome = $4, dissents = $5, concerns_residents = $6, vote_count = $7, updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, id, next.Text, next.Outcome, mustJSON(next.Dissents), next.ConcernsResidents, jsonOrNil(vc)); err != nil {
			return err
		}
		if out, err = loadDecision(ctx, tx, orgID, id, false); err != nil {
			return err
		}
		diff, err := audit.Diff("decision", cur.auditView(), out.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "decision.update", TargetType: "decision", TargetID: id, Diff: diff})
	})
	return out, dbErr(err, "decision")
}

// checkDissentAddition allows a closed meeting's decision to gain dissents (a
// muhalefet serhi can be filed after the meeting) within the site's window
// after closing, and never to lose or change one.
func (s *Service) checkDissentAddition(ctx context.Context, tx pgx.Tx, m Meeting, before, after []Dissent) error {
	if len(after) < len(before) {
		return problem.Conflict("a dissent that was recorded cannot be removed")
	}
	for i, b := range before {
		if after[i].PersonID != b.PersonID || after[i].Text != b.Text {
			return problem.Conflict("a dissent that was recorded cannot be changed; add a new one")
		}
	}
	var closedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT closed_at FROM meeting WHERE org_id = $1 AND id = $2`, m.OrgID, m.ID).Scan(&closedAt); err != nil {
		return err
	}
	days := m.rules().DissentWindowDays
	if closedAt != nil && s.now().After(closedAt.Add(time.Duration(days)*24*time.Hour)) {
		return problem.Conflict("the window for adding dissents after the meeting closed has passed")
	}
	return nil
}

// ApplyResult is what ApplyDecision returns.
type ApplyResult struct {
	DryRun   bool           `json:"dry_run"`
	Preview  map[string]any `json:"preview"`
	Decision *Decision      `json:"decision"`
}

// Effect kinds of ApplyDecision.
const (
	EffectDuesRate          = "dues_rate"
	EffectMembership        = "membership"
	EffectBudgetFinal       = "budget_final"
	EffectSpecialAssessment = "special_assessment"
)

var electedRoles = []string{string(authz.RoleSiteManager), string(authz.RoleBoardMember), string(authz.RoleAuditor)}

func inputHash(in map[string]any) string {
	b, _ := json.Marshal(in) // map keys are sorted: canonical
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func strField(in map[string]any, k string) string {
	s, _ := in[k].(string)
	return s
}

func uuidField(in map[string]any, k string) (uuid.UUID, bool) {
	u, err := uuid.Parse(strField(in, k))
	return u, err == nil
}

func dateField(in map[string]any, k string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strField(in, k))
	return t, err == nil
}

// ApplyDecision creates the change an accepted decision describes (G5): a
// membership with a term for an election (done here, atomically with the
// decision), or a dues rate, budget finalization or special assessment through
// DecisionEffects. The link is kept on the decision and on the created record
// (decision_id). Applying the same effect again returns the same result; a
// different one is a conflict. dry_run previews without writing. It needs decisions.manage.
func (s *Service) ApplyDecision(ctx context.Context, orgID, id uuid.UUID, in map[string]any, dryRun bool) (ApplyResult, error) {
	if err := authz.RequireAnywhere(ctx, PermDecisionsManage); err != nil {
		return ApplyResult{}, err
	}
	kind := strField(in, "kind")
	if !slices.Contains([]string{EffectDuesRate, EffectMembership, EffectBudgetFinal, EffectSpecialAssessment}, kind) {
		return ApplyResult{}, fieldProblem("kind", "invalid_value")
	}
	res := ApplyResult{DryRun: dryRun}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		d, sc, err := s.decisionAt(ctx, tx, PermDecisionsManage, orgID, id, true)
		if err != nil {
			return err
		}
		hash := inputHash(in)
		if d.Effect != nil {
			if d.Effect.Kind == kind && d.inputHash == hash {
				cp := d
				res.Decision = &cp // same effect again: nothing to do
				return nil
			}
			return problem.Conflict("this decision already created a change (" + d.Effect.Kind + ")")
		}
		if d.Outcome != "accepted" {
			return problem.Conflict("only an accepted decision can create a change")
		}
		ref := DecisionRef{OrgID: orgID, SiteID: d.SiteID, DecisionID: d.ID, Text: d.Text}
		var rec EffectRecord
		var preview map[string]any
		switch kind {
		case EffectMembership:
			rec, preview, err = s.applyMembership(ctx, tx, d, in, dryRun)
		case EffectDuesRate:
			if dryRun {
				preview, err = s.effects.Preview(ctx, tx, ref, kind, in)
			} else {
				rec, err = s.effects.ApplyDuesRate(ctx, tx, ref, in)
			}
		case EffectSpecialAssessment:
			plan, _ := in["plan"].(map[string]any)
			if plan == nil {
				return fieldProblem("plan", "required")
			}
			if dryRun {
				preview, err = s.effects.Preview(ctx, tx, ref, kind, plan)
			} else {
				rec, err = s.effects.ApplySpecialAssessment(ctx, tx, ref, plan)
			}
		case EffectBudgetFinal:
			bid, ok := uuidField(in, "budget_id")
			if !ok {
				return fieldProblem("budget_id", "required")
			}
			if dryRun {
				preview, err = s.effects.Preview(ctx, tx, ref, kind, in)
			} else {
				rec, err = s.effects.FinalizeBudget(ctx, tx, ref, bid)
			}
		}
		if err != nil {
			return err
		}
		if dryRun {
			res.Preview = preview
			cp := d
			res.Decision = &cp
			return nil
		}
		by := uuid.Nil
		if p := principalID(ctx); p != nil {
			by = *p
		}
		eff := map[string]any{"kind": kind, "record_type": rec.RecordType, "record_id": rec.RecordID, "applied_at": s.now().UTC(), "applied_by": by, "input_hash": hash}
		if _, err := tx.Exec(ctx, `UPDATE decision SET effect = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, id, mustJSON(eff)); err != nil {
			return err
		}
		nd, err := loadDecision(ctx, tx, orgID, id, false)
		if err != nil {
			return err
		}
		res.Decision = &nd
		diff, err := audit.Diff("decision", d.auditView(), nd.auditView())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "decision.apply", TargetType: "decision", TargetID: id, Diff: diff})
	})
	return res, dbErr(err, "decision")
}

// applyMembership creates the elected role's membership with its term (site
// owned for board and auditor, D2), linked to the decision, and optionally ends
// the previous holders' terms (a new election ends the old terms, G6).
func (s *Service) applyMembership(ctx context.Context, tx pgx.Tx, d Decision, in map[string]any, dryRun bool) (EffectRecord, map[string]any, error) {
	var fs []problem.Field
	person, ok := uuidField(in, "person_id")
	if !ok {
		fs = append(fs, problem.Field{Field: "person_id", Code: "required"})
	}
	role := strField(in, "role")
	if !slices.Contains(electedRoles, role) {
		fs = append(fs, problem.Field{Field: "role", Code: "invalid_value", Params: map[string]any{"allowed": electedRoles}})
	}
	if st := strField(in, "scope_type"); st != "site" {
		fs = append(fs, problem.Field{Field: "scope_type", Code: "invalid_value", Params: map[string]any{"reason": "elected_roles_are_site_scoped"}})
	}
	if sid, has := uuidField(in, "scope_id"); has && sid != d.SiteID {
		fs = append(fs, problem.Field{Field: "scope_id", Code: "invalid_value", Params: map[string]any{"reason": "must_be_the_decisions_site"}})
	} else if !has && in["scope_id"] != nil {
		fs = append(fs, problem.Field{Field: "scope_id", Code: "invalid_value"})
	}
	from, okFrom := dateField(in, "valid_from")
	to, okTo := dateField(in, "valid_to")
	if !okFrom {
		fs = append(fs, problem.Field{Field: "valid_from", Code: "required"})
	}
	if !okTo {
		fs = append(fs, problem.Field{Field: "valid_to", Code: "required"})
	} else if okFrom && !to.After(from) {
		fs = append(fs, problem.Field{Field: "valid_to", Code: "invalid_value", Params: map[string]any{"reason": "before_valid_from"}})
	}
	if len(fs) > 0 {
		return EffectRecord{}, nil, problem.Validation(fs...)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM person WHERE org_id = $1 AND id = $2)`, d.OrgID, person).Scan(&exists); err != nil {
		return EffectRecord{}, nil, err
	}
	if !exists {
		return EffectRecord{}, nil, problem.Validation(problem.Field{Field: "person_id", Code: "invalid_value"})
	}
	endPrev, _ := in["end_previous"].(bool)
	var toEnd []uuid.UUID
	if endPrev {
		rows, err := tx.Query(ctx, `SELECT id FROM membership WHERE org_id = $1 AND role = $2 AND scope_type = 'site' AND scope_id = $3
			AND valid_from < $4 AND (valid_to IS NULL OR valid_to > $4) ORDER BY id`, d.OrgID, role, d.SiteID, from)
		if err != nil {
			return EffectRecord{}, nil, err
		}
		for rows.Next() {
			var mid uuid.UUID
			if err := rows.Scan(&mid); err != nil {
				rows.Close()
				return EffectRecord{}, nil, err
			}
			toEnd = append(toEnd, mid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return EffectRecord{}, nil, err
		}
	}
	siteOwned := role == string(authz.RoleBoardMember) || role == string(authz.RoleAuditor)
	preview := map[string]any{"person_id": person, "role": role, "scope_type": "site", "scope_id": d.SiteID, "valid_from": from.Format("2006-01-02"),
		"valid_to": to.Format("2006-01-02"), "site_owned": siteOwned, "decision_id": d.ID, "would_end": toEnd}
	if dryRun {
		return EffectRecord{}, preview, nil
	}
	for _, mid := range toEnd {
		if _, err := tx.Exec(ctx, `UPDATE membership SET valid_to = $3, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, d.OrgID, mid, from); err != nil {
			return EffectRecord{}, nil, err
		}
		diff, err := audit.Diff("membership", map[string]any{"valid_to": nil}, map[string]any{"valid_to": from, "ended_by_decision": d.ID})
		if err != nil {
			return EffectRecord{}, nil, err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: d.OrgID, SiteID: &d.SiteID, Action: "membership.end", TargetType: "membership", TargetID: mid, Diff: diff}); err != nil {
			return EffectRecord{}, nil, err
		}
	}
	mid := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from, valid_to, site_owned, decision_id)
		VALUES ($1, $2, $3, $4, 'site', $5, $6, $7, $8, $9)`, mid, d.OrgID, person, role, d.SiteID, from, to, siteOwned, d.ID); err != nil {
		return EffectRecord{}, nil, err
	}
	diff, err := audit.Diff("membership", nil, map[string]any{"person_id": person, "role": role, "scope_type": "site", "scope_id": d.SiteID, "valid_from": from, "valid_to": to,
		"site_owned": siteOwned, "decision_id": d.ID})
	if err != nil {
		return EffectRecord{}, nil, err
	}
	if err := audit.Record(ctx, tx, audit.Event{OrgID: d.OrgID, SiteID: &d.SiteID, Action: "membership.create", TargetType: "membership", TargetID: mid, Diff: diff}); err != nil {
		return EffectRecord{}, nil, err
	}
	return EffectRecord{RecordType: "membership", RecordID: mid}, preview, nil
}
