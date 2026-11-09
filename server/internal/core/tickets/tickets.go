package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/opsx"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Visibility of a ticket (O3).
const (
	VisPrivate = "private"
	VisCommon  = "common"
)

// Assignee is a staff person or a vendor label (O2).
type Assignee struct {
	Kind        string     `json:"kind"`
	PersonID    *uuid.UUID `json:"person_id"`
	VendorID    *uuid.UUID `json:"vendor_id"`
	DisplayName string     `json:"display_name"`
}

// Attachment is a photo or file on a ticket or one of its comments.
type Attachment struct {
	ID        uuid.UUID  `json:"id"`
	OrgID     uuid.UUID  `json:"org_id"`
	TicketID  uuid.UUID  `json:"ticket_id"`
	CommentID *uuid.UUID `json:"comment_id"`
	FileID    uuid.UUID  `json:"file_id"`
	Mime      string     `json:"mime"`
	Size      int64      `json:"size"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Ticket is a request of a resident or of management.
type Ticket struct {
	ID               uuid.UUID    `json:"id"`
	OrgID            uuid.UUID    `json:"org_id"`
	SiteID           uuid.UUID    `json:"site_id"`
	UnitID           *uuid.UUID   `json:"unit_id"`
	UnitDoorNumber   *string      `json:"unit_door_number"`
	ReporterPersonID uuid.UUID    `json:"reporter_person_id"`
	ReporterName     string       `json:"reporter_display_name"`
	CategoryID       uuid.UUID    `json:"category_id"`
	Title            string       `json:"title"`
	Description      string       `json:"description"`
	StatusID         uuid.UUID    `json:"status_id"`
	StatusName       string       `json:"status_name"`
	StatusSemantic   string       `json:"status_semantic"`
	Visibility       string       `json:"visibility"`
	Assignee         *Assignee    `json:"assignee"`
	VoteCount        int          `json:"vote_count"`
	VotedByMe        bool         `json:"voted_by_me"`
	MergedIntoID     *uuid.UUID   `json:"merged_into_id"`
	CommentCount     int          `json:"comment_count"`
	Attachments      []Attachment `json:"attachments"`
	ClosedAt         *time.Time   `json:"closed_at"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	Version          int          `json:"version"`
}

// caps is what the caller may do with the tickets of one site.
type caps struct {
	scope        authz.Scope
	site         uuid.UUID
	person       uuid.UUID // nil UUID: the caller has no person here
	manage       bool
	read         bool
	readAssigned bool
	work         bool
	open         bool
	units        []uuid.UUID
}

func (c caps) all() bool { return c.manage || c.read }

// canInternal: internal notes are shown to management and auditors, and to
// staff with tickets.work on a ticket assigned to them.
func (c caps) internalAll() bool { return c.all() }

func (s *Service) capsOf(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (caps, error) {
	sc, err := opsx.SiteScope(ctx, tx, siteID)
	if err != nil {
		return caps{}, err
	}
	held, err := opsx.RequireAny(ctx, sc, AnyView)
	if err != nil {
		return caps{}, err
	}
	c := caps{scope: sc, site: siteID,
		manage: authz.Has(ctx, PermManage, sc), read: opsx.Contains(held, PermRead), readAssigned: opsx.Contains(held, PermReadAssigned),
		work: authz.Has(ctx, PermWork, sc), open: opsx.Contains(held, PermOpen), units: opsx.UnitsOf(ctx, siteID)}
	if p, ok, err := opsx.PersonOf(ctx, tx, orgID); err != nil {
		return caps{}, err
	} else if ok {
		c.person = p
	}
	return c, nil
}

const (
	pOrg      = "$1"
	pMe       = "$2"
	pIntAll   = "$3"
	pWork     = "$4"
	pAll      = "$5"
	pCommon   = "$6"
	pAssigned = "$7"
	pUnits    = "$8"
	// extra parameters of a query start at $9
)

func (c caps) base(orgID uuid.UUID) []any {
	return []any{orgID, c.person, c.internalAll(), c.work, c.all(), c.all() || c.open || c.readAssigned, c.readAssigned || c.work, c.units}
}

const visiblePredicate = `(` + pAll + ` OR t.reporter_person_id = ` + pMe + ` OR (t.visibility = 'common' AND ` + pCommon + `)
	OR (t.assignee_person_id = ` + pMe + ` AND ` + pAssigned + `) OR (t.unit_id = ANY(` + pUnits + `) AND ` + inOwnPeriod + `))`

// inOwnPeriod bounds the private tickets of a unit to the caller's own
// occupancy: a ticket is theirs to see when it was opened within the period
// [valid_from, valid_to) of one of their occupancies of that unit (dates in
// the site's time zone). The next tenant never sees the previous tenant's.
const inOwnPeriod = `EXISTS (SELECT 1 FROM occupancy oc JOIN site st ON st.org_id = oc.org_id AND st.id = oc.site_id
		WHERE oc.org_id = t.org_id AND oc.unit_id = t.unit_id AND oc.person_id = ` + pMe + `
		AND t.created_at >= (oc.valid_from::timestamp AT TIME ZONE st.timezone)
		AND (oc.valid_to IS NULL OR t.created_at < (oc.valid_to::timestamp AT TIME ZONE st.timezone)))`

const ticketSelect = `SELECT t.id, t.org_id, t.site_id, t.unit_id, u.door_number, t.reporter_person_id, rp.display_name, t.category_id, t.title, t.description,
	t.status_id, st.name, st.semantic, t.visibility, t.assignee_person_id, t.assignee_vendor_id, coalesce(ap.display_name, av.name, ''),
	(SELECT count(*) FROM ticket_vote v WHERE v.org_id = t.org_id AND v.ticket_id = t.id),
	EXISTS (SELECT 1 FROM ticket_vote v WHERE v.org_id = t.org_id AND v.ticket_id = t.id AND v.person_id = ` + pMe + `),
	t.merged_into_id,
	(SELECT count(*) FROM ticket_comment c WHERE c.org_id = t.org_id AND c.ticket_id = t.id AND (c.visibility = 'public' OR ` + pIntAll + ` OR (` + pWork + ` AND t.assignee_person_id = ` + pMe + `))),
	t.closed_at, t.created_at, t.updated_at, t.version
	FROM ticket t
	LEFT JOIN unit u ON u.org_id = t.org_id AND u.id = t.unit_id
	JOIN person rp ON rp.org_id = t.org_id AND rp.id = t.reporter_person_id
	JOIN ticket_status st ON st.org_id = t.org_id AND st.id = t.status_id
	LEFT JOIN person ap ON ap.org_id = t.org_id AND ap.id = t.assignee_person_id
	LEFT JOIN vendor av ON av.org_id = t.org_id AND av.id = t.assignee_vendor_id`

func scanTicket(row pgx.Row) (Ticket, error) {
	var t Ticket
	var ap, av *uuid.UUID
	var an string
	err := row.Scan(&t.ID, &t.OrgID, &t.SiteID, &t.UnitID, &t.UnitDoorNumber, &t.ReporterPersonID, &t.ReporterName, &t.CategoryID, &t.Title, &t.Description,
		&t.StatusID, &t.StatusName, &t.StatusSemantic, &t.Visibility, &ap, &av, &an, &t.VoteCount, &t.VotedByMe, &t.MergedIntoID, &t.CommentCount,
		&t.ClosedAt, &t.CreatedAt, &t.UpdatedAt, &t.Version)
	if err != nil {
		return t, err
	}
	switch {
	case ap != nil:
		t.Assignee = &Assignee{Kind: "person", PersonID: ap, DisplayName: an}
	case av != nil:
		t.Assignee = &Assignee{Kind: "vendor", VendorID: av, DisplayName: an}
	}
	t.Attachments = []Attachment{}
	t.CreatedAt, t.UpdatedAt = t.CreatedAt.UTC(), t.UpdatedAt.UTC()
	if t.ClosedAt != nil {
		c := t.ClosedAt.UTC()
		t.ClosedAt = &c
	}
	return t, nil
}

func (s *Service) siteOfTicket(ctx context.Context, tx pgx.Tx, orgID, ticketID uuid.UUID) (uuid.UUID, error) {
	var site uuid.UUID
	err := tx.QueryRow(ctx, `SELECT site_id FROM ticket WHERE org_id = $1 AND id = $2`, orgID, ticketID).Scan(&site)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, problem.NotFound("no such ticket")
	}
	return site, err
}

// loadVisible loads a ticket the caller may see (404 otherwise) with its
// capabilities. forUpdate locks the row.
func (s *Service) loadVisible(ctx context.Context, tx pgx.Tx, orgID, ticketID uuid.UUID, forUpdate bool) (Ticket, caps, error) {
	site, err := s.siteOfTicket(ctx, tx, orgID, ticketID)
	if err != nil {
		return Ticket{}, caps{}, err
	}
	c, err := s.capsOf(ctx, tx, orgID, site)
	if err != nil {
		return Ticket{}, caps{}, err
	}
	q := ticketSelect + ` WHERE t.org_id = ` + pOrg + ` AND t.id = $9 AND ` + visiblePredicate
	if forUpdate {
		q += ` FOR UPDATE OF t`
	}
	t, err := scanTicket(tx.QueryRow(ctx, q, append(c.base(orgID), ticketID)...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Ticket{}, c, problem.NotFound("no such ticket")
	}
	if err != nil {
		return Ticket{}, c, err
	}
	if err := s.fillAttachments(ctx, tx, orgID, c, []*Ticket{&t}); err != nil {
		return t, c, err
	}
	return t, c, nil
}

// errMerged answers every change to a ticket that was merged into another: a
// merged ticket is terminal (its votes and discussion live on the target).
func errMerged(t Ticket) error {
	if t.MergedIntoID != nil {
		return problem.Conflict("the ticket was merged into another ticket; work on the target")
	}
	return nil
}

func (c caps) canWork(t Ticket) bool {
	return c.manage || (c.work && t.Assignee != nil && t.Assignee.PersonID != nil && *t.Assignee.PersonID == c.person)
}

func (s *Service) fillAttachments(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, _ caps, ts []*Ticket) error {
	if len(ts) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(ts))
	byID := map[uuid.UUID]*Ticket{}
	for i, t := range ts {
		ids[i] = t.ID
		byID[t.ID] = t
	}
	rows, err := tx.Query(ctx, `SELECT id, org_id, ticket_id, comment_id, file_id, mime, size, created_at, updated_at
		FROM ticket_attachment WHERE org_id = $1 AND ticket_id = ANY($2) AND comment_id IS NULL ORDER BY id`, orgID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return err
		}
		if t := byID[a.TicketID]; t != nil {
			t.Attachments = append(t.Attachments, a)
		}
	}
	return rows.Err()
}

func scanAttachment(row pgx.Row) (Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.OrgID, &a.TicketID, &a.CommentID, &a.FileID, &a.Mime, &a.Size, &a.CreatedAt, &a.UpdatedAt)
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return a, err
}

// Filter narrows ListTickets.
type Filter struct {
	SiteID           uuid.UUID
	CategoryID       *uuid.UUID
	StatusID         *uuid.UUID
	Semantic         string
	AssigneePersonID *uuid.UUID
	AssigneeVendorID *uuid.UUID
	UnitID           *uuid.UUID
	Visibility       string
	ReporterPersonID *uuid.UUID
	Mine             bool
	Queue            bool
	Q                string
}

// ListTickets lists the tickets of a site the caller may see, newest first.
// Management and auditors see all; staff the tickets assigned to them and the
// common ones; residents their own, their unit's and the common ones.
func (s *Service) ListTickets(ctx context.Context, orgID uuid.UUID, f Filter, p tenancy.PageParams) ([]Ticket, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	if f.Visibility != "" && f.Visibility != VisPrivate && f.Visibility != VisCommon {
		return nil, "", problem.Validation(problem.Field{Field: "visibility", Code: "invalid_value"})
	}
	if f.Semantic != "" && f.Semantic != OpenLike && f.Semantic != ClosedLike {
		return nil, "", problem.Validation(problem.Field{Field: "semantic", Code: "invalid_value"})
	}
	var out []Ticket
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		c, err := s.capsOf(ctx, tx, orgID, f.SiteID)
		if err != nil {
			return err
		}
		if err := ensureWorkflow(ctx, tx, orgID, f.SiteID); err != nil {
			return err
		}
		q := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(f.Q), `\`, `\\`), `%`, `\%`)
		args := append(c.base(orgID), f.SiteID, after, f.CategoryID, f.StatusID, f.Semantic, f.AssigneePersonID, f.AssigneeVendorID, f.UnitID, f.Visibility,
			f.ReporterPersonID, f.Mine, f.Queue, "%"+strings.ReplaceAll(q, "_", `\_`)+"%", q != "", limit+1)
		rows, err := tx.Query(ctx, ticketSelect+` WHERE t.org_id = $1 AND t.site_id = $9 AND `+visiblePredicate+`
			AND ($10 = '00000000-0000-0000-0000-000000000000'::uuid OR t.id < $10)
			AND ($11::uuid IS NULL OR t.category_id = $11) AND ($12::uuid IS NULL OR t.status_id = $12) AND ($13 = '' OR st.semantic = $13)
			AND ($14::uuid IS NULL OR t.assignee_person_id = $14) AND ($15::uuid IS NULL OR t.assignee_vendor_id = $15) AND ($16::uuid IS NULL OR t.unit_id = $16)
			AND ($17 = '' OR t.visibility = $17) AND ($18::uuid IS NULL OR t.reporter_person_id = $18)
			AND (NOT $19 OR t.reporter_person_id = $2) AND (NOT $20 OR t.assignee_person_id = $2)
			AND (NOT $22 OR t.title ILIKE $21 OR t.description ILIKE $21)
			ORDER BY t.id DESC LIMIT $23`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTicket(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		ptrs := make([]*Ticket, len(out))
		for i := range out {
			ptrs[i] = &out[i]
		}
		return s.fillAttachments(ctx, tx, orgID, c, ptrs)
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "tickets")
	}
	items, next := tenancy.Page(out, limit, func(t Ticket) uuid.UUID { return t.ID })
	return items, next, nil
}

// GetTicket returns a ticket the caller may see (404 otherwise).
func (s *Service) GetTicket(ctx context.Context, orgID, ticketID uuid.UUID) (Ticket, error) {
	var t Ticket
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		t, _, err = s.loadVisible(ctx, tx, orgID, ticketID, false)
		return err
	})
	return t, tenancy.DBProblem(err, "ticket")
}

type ticketAudit struct {
	CategoryID  uuid.UUID  `json:"category_id"`
	UnitID      *uuid.UUID `json:"unit_id"`
	StatusID    uuid.UUID  `json:"status_id"`
	Visibility  string     `json:"visibility"`
	AssigneeP   *uuid.UUID `json:"assignee_person_id"`
	AssigneeV   *uuid.UUID `json:"assignee_vendor_id"`
	MergedIntoI *uuid.UUID `json:"merged_into_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
}

func auditView(t Ticket) ticketAudit {
	a := ticketAudit{CategoryID: t.CategoryID, UnitID: t.UnitID, StatusID: t.StatusID, Visibility: t.Visibility, MergedIntoI: t.MergedIntoID, Title: t.Title, Description: t.Description}
	if t.Assignee != nil {
		a.AssigneeP, a.AssigneeV = t.Assignee.PersonID, t.Assignee.VendorID
	}
	return a
}

func (s *Service) record(ctx context.Context, tx pgx.Tx, action string, before *Ticket, after Ticket) error {
	var b any
	if before != nil {
		b = auditView(*before)
	}
	diff, _ := audit.Diff("ticket", b, auditView(after))
	site := after.SiteID
	return audit.Record(ctx, tx, audit.Event{OrgID: after.OrgID, SiteID: &site, Action: action, TargetType: "ticket", TargetID: after.ID, Diff: diff})
}

// CreateTicket opens a ticket. Residents open tickets for their own unit (or
// common ones); management may open on behalf of a person (reporter_person_id)
// and choose the visibility, everybody else gets the category's default.
func (s *Service) CreateTicket(ctx context.Context, orgID uuid.UUID, d tenancy.Doc) (Ticket, error) {
	if bad := d.Unknown("site_id", "unit_id", "category_id", "title", "description", "visibility", "reporter_person_id", "attachment_file_ids"); len(bad) > 0 {
		return Ticket{}, problem.Validation(bad...)
	}
	var in struct {
		SiteID, CategoryID uuid.UUID
		UnitID, Reporter   *uuid.UUID
		Title, Desc, Vis   string
		FileIDs            []uuid.UUID
	}
	var bad []problem.Field
	getUUID := func(k string, dst *uuid.UUID, required bool) error {
		sp, err := d.String(k)
		if err != nil {
			return err
		}
		if sp == nil {
			if required {
				bad = append(bad, problem.Field{Field: k, Code: "required"})
			}
			return nil
		}
		u, perr := uuid.Parse(*sp)
		if perr != nil {
			bad = append(bad, problem.Field{Field: k, Code: "invalid_format"})
			return nil
		}
		*dst = u
		return nil
	}
	optUUID := func(k string) (*uuid.UUID, error) {
		var u uuid.UUID
		if err := getUUID(k, &u, false); err != nil || u == uuid.Nil {
			return nil, err
		}
		return &u, nil
	}
	if err := getUUID("site_id", &in.SiteID, true); err != nil {
		return Ticket{}, err
	}
	if err := getUUID("category_id", &in.CategoryID, true); err != nil {
		return Ticket{}, err
	}
	var err error
	if in.UnitID, err = optUUID("unit_id"); err != nil {
		return Ticket{}, err
	}
	if in.Reporter, err = optUUID("reporter_person_id"); err != nil {
		return Ticket{}, err
	}
	for _, kv := range []struct {
		k string
		p *string
	}{{"title", &in.Title}, {"description", &in.Desc}, {"visibility", &in.Vis}} {
		sp, err := d.String(kv.k)
		if err != nil {
			return Ticket{}, err
		}
		if sp != nil {
			*kv.p = *sp
		}
	}
	in.Title = strings.TrimSpace(in.Title)
	if l := len([]rune(in.Title)); l < 1 || l > 200 {
		bad = append(bad, problem.Field{Field: "title", Code: "invalid_length"})
	}
	if len(in.Desc) > 10000 {
		bad = append(bad, problem.Field{Field: "description", Code: "invalid_length"})
	}
	if in.Vis != "" && in.Vis != VisPrivate && in.Vis != VisCommon {
		bad = append(bad, problem.Field{Field: "visibility", Code: "invalid_value"})
	}
	if d.Has("attachment_file_ids") {
		if err := d.Get("attachment_file_ids", &in.FileIDs); err != nil {
			return Ticket{}, err
		}
	}
	if len(in.FileIDs) > MaxPhotos {
		bad = append(bad, problem.Field{Field: "attachment_file_ids", Code: "too_many"})
	}
	if len(bad) > 0 {
		return Ticket{}, problem.Validation(bad...)
	}

	var out Ticket
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		c, err := s.capsOf(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if !c.open {
			return problem.Forbidden("you may not open tickets here")
		}
		if err := ensureWorkflow(ctx, tx, orgID, in.SiteID); err != nil {
			return err
		}
		reporter := c.person
		if in.Reporter != nil && *in.Reporter != c.person {
			if !c.manage {
				return problem.Forbidden("only management may open a ticket on behalf of somebody")
			}
			reporter = *in.Reporter
		}
		if reporter == uuid.Nil {
			return problem.Forbidden("this account has no person in the organization")
		}
		var catVis string
		var catRole *string
		var archived bool
		err = tx.QueryRow(ctx, `SELECT default_visibility, default_assignee_role, archived FROM ticket_category WHERE org_id = $1 AND site_id = $2 AND id = $3`,
			orgID, in.SiteID, in.CategoryID).Scan(&catVis, &catRole, &archived)
		if errors.Is(err, pgx.ErrNoRows) || archived {
			return problem.Validation(problem.Field{Field: "category_id", Code: "invalid_value"})
		}
		if err != nil {
			return err
		}
		// the creator chooses (O3): a private ticket about their own unit or a common one
		// about the shared parts; without a choice the category's default applies. A
		// private ticket needs a unit the opener occupies (the checks below), so a resident
		// cannot open one for somebody else's flat.
		vis := catVis
		if in.Vis != "" {
			vis = in.Vis
		}
		unit := in.UnitID
		if unit != nil {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT true FROM unit WHERE org_id = $1 AND site_id = $2 AND id = $3`, orgID, in.SiteID, *unit).Scan(&ok); err != nil {
				return problem.Validation(problem.Field{Field: "unit_id", Code: "invalid_value"})
			}
			if !c.manage && !slices.Contains(c.units, *unit) {
				return problem.Forbidden("you may open tickets for your own unit only")
			}
		} else if vis == VisPrivate {
			// the unit the ticket is about is the reporter's, not the opener's: a
			// manager opening on a resident's behalf must name the unit, unless the
			// resident has exactly one
			cand := c.units
			if reporter != c.person {
				cand = nil
				rows, err := tx.Query(ctx, `SELECT DISTINCT o.unit_id FROM occupancy o JOIN site st ON st.org_id = o.org_id AND st.id = o.site_id
					WHERE o.org_id = $1 AND o.site_id = $2 AND o.person_id = $3
					AND o.valid_from <= (now() AT TIME ZONE st.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE st.timezone)::date)`, orgID, in.SiteID, reporter)
				if err != nil {
					return err
				}
				for rows.Next() {
					var u uuid.UUID
					if err := rows.Scan(&u); err != nil {
						rows.Close()
						return err
					}
					cand = append(cand, u)
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return err
				}
			}
			if len(cand) != 1 {
				return problem.Validation(problem.Field{Field: "unit_id", Code: "required"})
			}
			unit = &cand[0]
		}
		var statusID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM ticket_status WHERE org_id = $1 AND site_id = $2 AND NOT archived AND semantic = 'open_like' ORDER BY position LIMIT 1`, orgID, in.SiteID).Scan(&statusID); err != nil {
			return err
		}
		var assignee *uuid.UUID
		if catRole != nil {
			var p uuid.UUID
			err := tx.QueryRow(ctx, `SELECT person_id FROM membership WHERE org_id = $1 AND role = $2 AND (scope_type = 'org' OR (scope_type = 'site' AND scope_id = $3))
				AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now()) ORDER BY created_at, id LIMIT 1`, orgID, *catRole, in.SiteID).Scan(&p)
			if err == nil {
				assignee = &p
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		tid := id.New()
		now := s.now()
		if _, err := tx.Exec(ctx, `INSERT INTO ticket (id, org_id, site_id, unit_id, reporter_person_id, category_id, title, description, status_id, visibility, assignee_person_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)`,
			tid, orgID, in.SiteID, unit, reporter, in.CategoryID, in.Title, in.Desc, statusID, vis, assignee, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_status_history (id, org_id, site_id, ticket_id, from_status_id, to_status_id, changed_by, changed_by_name, at)
			VALUES ($1, $2, $3, $4, NULL, $5, $6, NULLIF($7, ''), $8)`, id.New(), orgID, in.SiteID, tid, statusID, actorUser(ctx), opsx.ActorName(ctx, tx, orgID), now); err != nil {
			return err
		}
		for _, fid := range in.FileIDs {
			if err := s.attach(ctx, tx, orgID, in.SiteID, tid, nil, fid); err != nil {
				return err
			}
		}
		out, _, err = s.loadVisible(ctx, tx, orgID, tid, false)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, "tickets.create", nil, out)
	})
	return out, tenancy.DBProblem(err, "ticket")
}

// attach attaches an uploaded file to a ticket (or to one of its comments).
func (s *Service) attach(ctx context.Context, tx pgx.Tx, orgID, siteID, ticketID uuid.UUID, commentID *uuid.UUID, fileID uuid.UUID) error {
	if s.files == nil {
		return problem.Internal(errors.New("file module not configured"))
	}
	ref := ticketID
	if commentID != nil {
		ref = *commentID
	}
	f, err := s.files.Attach(ctx, tx, orgID, fileID, files.ScopeRef{Type: "ticket", ID: ref})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ticket_attachment (id, org_id, site_id, ticket_id, comment_id, file_id, mime, size) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id.New(), orgID, siteID, ticketID, commentID, fileID, f.Mime, f.Size)
	return err
}

// UpdateTicket edits a ticket (tickets.manage). Only the fields sent change;
// assignee_person_id and assignee_vendor_id are alternatives: setting one
// clears the other, null clears that assignee, absent leaves it (sending both
// non-null is 422).
func (s *Service) UpdateTicket(ctx context.Context, orgID, ticketID uuid.UUID, expected int, d tenancy.Doc) (Ticket, error) {
	if bad := d.Unknown("unit_id", "category_id", "title", "description", "visibility", "assignee_person_id", "assignee_vendor_id"); len(bad) > 0 {
		return Ticket{}, problem.Validation(bad...)
	}
	var out Ticket
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, c, err := s.loadVisible(ctx, tx, orgID, ticketID, true)
		if err != nil {
			return err
		}
		if !c.manage {
			return problem.Forbidden("you need tickets.manage to change a ticket")
		}
		if err := errMerged(before); err != nil {
			return err
		}
		if before.Version != expected {
			return tenancy.ErrVersion
		}
		next := before
		var bad []problem.Field
		uuidField := func(k string) (*uuid.UUID, bool, error) {
			if !d.Has(k) {
				return nil, false, nil
			}
			sp, err := d.String(k)
			if err != nil {
				return nil, true, err
			}
			if sp == nil {
				return nil, true, nil
			}
			u, perr := uuid.Parse(*sp)
			if perr != nil {
				bad = append(bad, problem.Field{Field: k, Code: "invalid_format"})
				return nil, false, nil
			}
			return &u, true, nil
		}
		if v, ok, err := uuidField("unit_id"); err != nil {
			return err
		} else if ok {
			if v != nil {
				var x bool
				if err := tx.QueryRow(ctx, `SELECT true FROM unit WHERE org_id = $1 AND site_id = $2 AND id = $3`, orgID, before.SiteID, *v).Scan(&x); err != nil {
					bad = append(bad, problem.Field{Field: "unit_id", Code: "invalid_value"})
				}
			}
			next.UnitID = v
		}
		if v, ok, err := uuidField("category_id"); err != nil {
			return err
		} else if ok && v != nil {
			var arch bool
			if err := tx.QueryRow(ctx, `SELECT archived FROM ticket_category WHERE org_id = $1 AND site_id = $2 AND id = $3`, orgID, before.SiteID, *v).Scan(&arch); err != nil || (arch && *v != before.CategoryID) {
				bad = append(bad, problem.Field{Field: "category_id", Code: "invalid_value"})
			}
			next.CategoryID = *v
		}
		for _, kv := range []struct {
			k string
			p *string
		}{{"title", &next.Title}, {"description", &next.Description}, {"visibility", &next.Visibility}} {
			if d.Has(kv.k) {
				sp, err := d.String(kv.k)
				if err != nil {
					return err
				}
				if sp == nil {
					bad = append(bad, problem.Field{Field: kv.k, Code: "required"})
					continue
				}
				*kv.p = *sp
			}
		}
		next.Title = strings.TrimSpace(next.Title)
		if l := len([]rune(next.Title)); l < 1 || l > 200 {
			bad = append(bad, problem.Field{Field: "title", Code: "invalid_length"})
		}
		if next.Visibility != VisPrivate && next.Visibility != VisCommon {
			bad = append(bad, problem.Field{Field: "visibility", Code: "invalid_value"})
		}
		if next.Visibility == VisPrivate && next.UnitID == nil {
			bad = append(bad, problem.Field{Field: "unit_id", Code: "required"})
		}
		var ap, av *uuid.UUID
		if before.Assignee != nil {
			ap, av = before.Assignee.PersonID, before.Assignee.VendorID
		}
		pv, pOK, err := uuidField("assignee_person_id")
		if err != nil {
			return err
		}
		vv, vOK, err := uuidField("assignee_vendor_id")
		if err != nil {
			return err
		}
		if pOK && vOK && pv != nil && vv != nil {
			bad = append(bad, problem.Field{Field: "assignee_vendor_id", Code: "conflicts_with_assignee_person_id"})
		}
		if pOK {
			ap = pv
			if pv != nil && !vOK {
				av = nil
			}
		}
		if vOK {
			av = vv
			if vv != nil && !pOK {
				ap = nil
			}
		}
		if ap != nil {
			var x bool
			err := tx.QueryRow(ctx, `SELECT true FROM membership WHERE org_id = $1 AND person_id = $2 AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now())
				AND (scope_type = 'org' OR (scope_type = 'site' AND scope_id = $3)) LIMIT 1`, orgID, *ap, before.SiteID).Scan(&x)
			if err != nil {
				bad = append(bad, problem.Field{Field: "assignee_person_id", Code: "invalid_value"})
			}
		}
		if av != nil {
			var x bool
			if err := tx.QueryRow(ctx, `SELECT true FROM vendor WHERE org_id = $1 AND id = $2 AND (site_id IS NULL OR site_id = $3)`, orgID, *av, before.SiteID).Scan(&x); err != nil {
				bad = append(bad, problem.Field{Field: "assignee_vendor_id", Code: "invalid_value"})
			}
		}
		if len(bad) > 0 {
			return problem.Validation(bad...)
		}
		if _, err := tx.Exec(ctx, `UPDATE ticket SET unit_id = $3, category_id = $4, title = $5, description = $6, visibility = $7, assignee_person_id = $8, assignee_vendor_id = $9,
			updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, ticketID, next.UnitID, next.CategoryID, next.Title, next.Description, next.Visibility, ap, av); err != nil {
			return err
		}
		out, _, err = s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, "tickets.update", &before, out)
	})
	return out, tenancy.DBProblem(err, "ticket")
}

func (s *Service) statusOf(ctx context.Context, tx pgx.Tx, orgID, siteID, statusID uuid.UUID) (name, semantic string, err error) {
	err = tx.QueryRow(ctx, `SELECT name, semantic FROM ticket_status WHERE org_id = $1 AND site_id = $2 AND id = $3 AND NOT archived`, orgID, siteID, statusID).Scan(&name, &semantic)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", problem.Validation(problem.Field{Field: "status_id", Code: "invalid_value"})
	}
	return
}

// TransitionInput is the body of a status change.
type TransitionInput struct {
	StatusID          uuid.UUID
	Comment           string
	CommentVisibility string
}

// Transition moves a ticket to another status of its site's workflow
// (management, or staff with tickets.work on a ticket assigned to them),
// appends to the history and tells the reporter.
func (s *Service) Transition(ctx context.Context, orgID, ticketID uuid.UUID, in TransitionInput) (Ticket, error) {
	vis := in.CommentVisibility
	if vis == "" {
		vis = "public"
	}
	if vis != "public" && vis != "internal" {
		return Ticket{}, problem.Validation(problem.Field{Field: "comment_visibility", Code: "invalid_value"})
	}
	if len(in.Comment) > 10000 {
		return Ticket{}, problem.Validation(problem.Field{Field: "comment", Code: "invalid_length"})
	}
	var out Ticket
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, c, err := s.loadVisible(ctx, tx, orgID, ticketID, true)
		if err != nil {
			return err
		}
		if !c.canWork(before) {
			return problem.Forbidden("you may not change the status of this ticket")
		}
		if err := errMerged(before); err != nil {
			return err
		}
		name, semantic, err := s.statusOf(ctx, tx, orgID, before.SiteID, in.StatusID)
		if err != nil {
			return err
		}
		if in.StatusID == before.StatusID {
			return problem.Conflict("the ticket already has this status")
		}
		now := s.now()
		var closed *time.Time
		if semantic == ClosedLike {
			closed = &now
			if before.ClosedAt != nil && before.StatusSemantic == ClosedLike {
				closed = before.ClosedAt
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE ticket SET status_id = $3, closed_at = $4, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, ticketID, in.StatusID, closed); err != nil {
			return err
		}
		comment := strings.TrimSpace(in.Comment)
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_status_history (id, org_id, site_id, ticket_id, from_status_id, to_status_id, changed_by, changed_by_name, comment, comment_internal, at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11)`,
			id.New(), orgID, before.SiteID, ticketID, before.StatusID, in.StatusID, actorUser(ctx), opsx.ActorName(ctx, tx, orgID), comment, vis == "internal", now); err != nil {
			return err
		}
		if comment != "" {
			if vis == "internal" && (!c.manage && !c.work) {
				return problem.Forbidden("internal notes need tickets.manage or tickets.work")
			}
			author := c.person
			if author == uuid.Nil {
				author = before.ReporterPersonID
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ticket_comment (id, org_id, site_id, ticket_id, author_person_id, body, visibility) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				id.New(), orgID, before.SiteID, ticketID, author, comment, vis); err != nil {
				return err
			}
		}
		out, _, err = s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		if err := s.record(ctx, tx, "tickets.transition", &before, out); err != nil {
			return err
		}
		if before.ReporterPersonID != c.person {
			return s.notify.Notify(ctx, tx, jobs.NotifyArgs{OrgID: orgID, SiteID: &before.SiteID, Type: "ticket.updated",
				Recipients: []jobs.Recipient{{PersonID: &before.ReporterPersonID}},
				Payload:    map[string]any{"ticket_id": ticketID, "title": before.Title, "status": name}, Link: "/tickets/" + ticketID.String()})
		}
		return nil
	})
	return out, tenancy.DBProblem(err, "ticket")
}

// Merge folds a duplicate into another ticket of the site: votes carry over to
// the target, the duplicate is closed and points to it (tickets.manage).
func (s *Service) Merge(ctx context.Context, orgID, ticketID, intoID uuid.UUID) (Ticket, error) {
	if ticketID == intoID {
		return Ticket{}, problem.Validation(problem.Field{Field: "into_ticket_id", Code: "invalid_value"})
	}
	var out Ticket
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		// Lock both rows in id order, so A merged into B and B into A at the same
		// time cannot deadlock.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM ticket WHERE org_id = $1 AND id = ANY($2) ORDER BY id FOR UPDATE`, orgID, []uuid.UUID{ticketID, intoID}); err != nil {
			return err
		}
		before, c, err := s.loadVisible(ctx, tx, orgID, ticketID, true)
		if err != nil {
			return err
		}
		if !c.manage {
			return problem.Forbidden("you need tickets.manage to merge tickets")
		}
		target, _, err := s.loadVisible(ctx, tx, orgID, intoID, true)
		if err != nil {
			return err // 404 for a ticket the caller cannot see, 403 without rights: not a validation problem
		}
		if target.SiteID != before.SiteID {
			return problem.Validation(problem.Field{Field: "into_ticket_id", Code: "invalid_value"})
		}
		if before.MergedIntoID != nil {
			return problem.Conflict("the ticket is already merged")
		}
		if target.MergedIntoID != nil {
			return problem.Conflict("the target ticket is itself merged into another")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_vote (org_id, ticket_id, person_id) SELECT org_id, $3, person_id FROM ticket_vote WHERE org_id = $1 AND ticket_id = $2
			ON CONFLICT DO NOTHING`, orgID, ticketID, intoID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM ticket_vote WHERE org_id = $1 AND ticket_id = $2`, orgID, ticketID); err != nil {
			return err
		}
		var doneID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM ticket_status WHERE org_id = $1 AND site_id = $2 AND NOT archived AND semantic = 'closed_like' ORDER BY position LIMIT 1`, orgID, before.SiteID).Scan(&doneID); err != nil {
			return err
		}
		now := s.now()
		if _, err := tx.Exec(ctx, `UPDATE ticket SET merged_into_id = $3, status_id = $4, closed_at = coalesce(closed_at, $5), updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, ticketID, intoID, doneID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_status_history (id, org_id, site_id, ticket_id, from_status_id, to_status_id, changed_by, changed_by_name, comment, at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), 'merged into another ticket', $9)`,
			id.New(), orgID, before.SiteID, ticketID, before.StatusID, doneID, actorUser(ctx), opsx.ActorName(ctx, tx, orgID), now); err != nil {
			return err
		}
		out, _, err = s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, "tickets.merge", &before, out)
	})
	return out, tenancy.DBProblem(err, "ticket")
}

// Vote is the result of a +1 or its withdrawal.
type Vote struct {
	TicketID  uuid.UUID `json:"ticket_id"`
	VoteCount int       `json:"vote_count"`
	Voted     bool      `json:"voted"`
}

// SetVote adds or withdraws the caller's "+1" on a common ticket; both are idempotent.
func (s *Service) SetVote(ctx context.Context, orgID, ticketID uuid.UUID, voted bool) (Vote, error) {
	v := Vote{TicketID: ticketID, Voted: voted}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		t, c, err := s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		if !c.open || c.person == uuid.Nil {
			return problem.Forbidden("you may not vote here")
		}
		if err := errMerged(t); err != nil {
			return err
		}
		if t.Visibility != VisCommon {
			return problem.Conflict("only common tickets can be voted")
		}
		if voted {
			_, err = tx.Exec(ctx, `INSERT INTO ticket_vote (org_id, ticket_id, person_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, orgID, ticketID, c.person)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM ticket_vote WHERE org_id = $1 AND ticket_id = $2 AND person_id = $3`, orgID, ticketID, c.person)
		}
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM ticket_vote WHERE org_id = $1 AND ticket_id = $2`, orgID, ticketID).Scan(&v.VoteCount)
	})
	return v, tenancy.DBProblem(err, "vote")
}

// Comment is a message on a ticket; internal ones are hidden from residents.
type Comment struct {
	ID          uuid.UUID    `json:"id"`
	OrgID       uuid.UUID    `json:"org_id"`
	TicketID    uuid.UUID    `json:"ticket_id"`
	AuthorID    uuid.UUID    `json:"author_person_id"`
	AuthorName  string       `json:"author_display_name"`
	Body        string       `json:"body"`
	Visibility  string       `json:"visibility"`
	Attachments []Attachment `json:"attachments"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

func (s *Service) canSeeInternal(c caps, t Ticket) bool { return c.internalAll() || c.canWork(t) }

// ListComments lists a ticket's comments oldest first; internal notes only for
// those who may read them.
func (s *Service) ListComments(ctx context.Context, orgID, ticketID uuid.UUID, p tenancy.PageParams) ([]Comment, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Comment
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		t, c, err := s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT c.id, c.org_id, c.ticket_id, c.author_person_id, p.display_name, c.body, c.visibility, c.created_at, c.updated_at
			FROM ticket_comment c JOIN person p ON p.org_id = c.org_id AND p.id = c.author_person_id
			WHERE c.org_id = $1 AND c.ticket_id = $2 AND c.id > $3 AND (c.visibility = 'public' OR $4) ORDER BY c.id LIMIT $5`,
			orgID, ticketID, after, s.canSeeInternal(c, t), limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cm Comment
			if err := rows.Scan(&cm.ID, &cm.OrgID, &cm.TicketID, &cm.AuthorID, &cm.AuthorName, &cm.Body, &cm.Visibility, &cm.CreatedAt, &cm.UpdatedAt); err != nil {
				return err
			}
			cm.Attachments = []Attachment{}
			cm.CreatedAt, cm.UpdatedAt = cm.CreatedAt.UTC(), cm.UpdatedAt.UTC()
			out = append(out, cm)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return s.fillCommentAttachments(ctx, tx, orgID, out)
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "comments")
	}
	items, next := tenancy.Page(out, limit, func(c Comment) uuid.UUID { return c.ID })
	return items, next, nil
}

func (s *Service) fillCommentAttachments(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, cs []Comment) error {
	if len(cs) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(cs))
	idx := map[uuid.UUID]int{}
	for i, c := range cs {
		ids[i] = c.ID
		idx[c.ID] = i
	}
	rows, err := tx.Query(ctx, `SELECT id, org_id, ticket_id, comment_id, file_id, mime, size, created_at, updated_at FROM ticket_attachment WHERE org_id = $1 AND comment_id = ANY($2) ORDER BY id`, orgID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return err
		}
		if a.CommentID != nil {
			i := idx[*a.CommentID]
			cs[i].Attachments = append(cs[i].Attachments, a)
		}
	}
	return rows.Err()
}

// CreateComment adds a comment. visibility internal needs tickets.manage, or
// tickets.work on a ticket assigned to the caller. The reporter is told about
// public comments of other people.
func (s *Service) CreateComment(ctx context.Context, orgID, ticketID uuid.UUID, body, visibility string, fileIDs []uuid.UUID) (Comment, error) {
	body = strings.TrimSpace(body)
	var bad []problem.Field
	if l := len([]rune(body)); l < 1 || l > 10000 {
		bad = append(bad, problem.Field{Field: "body", Code: "invalid_length"})
	}
	if visibility == "" {
		visibility = "public"
	}
	if visibility != "public" && visibility != "internal" {
		bad = append(bad, problem.Field{Field: "visibility", Code: "invalid_value"})
	}
	if len(fileIDs) > MaxPhotos {
		bad = append(bad, problem.Field{Field: "attachment_file_ids", Code: "too_many"})
	}
	if len(bad) > 0 {
		return Comment{}, problem.Validation(bad...)
	}
	var out Comment
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		t, c, err := s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		if c.person == uuid.Nil {
			return problem.Forbidden("this account has no person in the organization")
		}
		// reading is not enough (auditors read): commenting needs tickets.open on
		// a ticket the caller can see, or tickets.manage / tickets.work
		if !c.open && !c.manage && !c.work {
			return problem.Forbidden("you need tickets.open to comment")
		}
		if err := errMerged(t); err != nil {
			return err
		}
		if visibility == "internal" && (!c.manage && !c.canWork(t)) {
			return problem.Forbidden("internal notes need tickets.manage or tickets.work")
		}
		cid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_comment (id, org_id, site_id, ticket_id, author_person_id, body, visibility) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			cid, orgID, t.SiteID, ticketID, c.person, body, visibility); err != nil {
			return err
		}
		for _, fid := range fileIDs {
			if err := s.attach(ctx, tx, orgID, t.SiteID, ticketID, &cid, fid); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE ticket SET updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, ticketID); err != nil {
			return err
		}
		out = Comment{ID: cid, OrgID: orgID, TicketID: ticketID, AuthorID: c.person, Body: body, Visibility: visibility, Attachments: []Attachment{}}
		if err := tx.QueryRow(ctx, `SELECT p.display_name, c.created_at, c.updated_at FROM ticket_comment c JOIN person p ON p.org_id = c.org_id AND p.id = c.author_person_id WHERE c.org_id = $1 AND c.id = $2`,
			orgID, cid).Scan(&out.AuthorName, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
		cs := []Comment{out}
		if err := s.fillCommentAttachments(ctx, tx, orgID, cs); err != nil {
			return err
		}
		out = cs[0]
		diff, _ := audit.Diff("ticket_comment", nil, map[string]any{"visibility": visibility})
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &t.SiteID, Action: "tickets.comment", TargetType: "ticket_comment", TargetID: cid, Diff: diff}); err != nil {
			return err
		}
		if visibility != "public" {
			return nil
		}
		// the reporter hears about comments of others; a comment of a resident
		// (not management) goes to the assignee, or to the managers when nobody is assigned
		var to []uuid.UUID
		if t.ReporterPersonID != c.person {
			to = append(to, t.ReporterPersonID)
		}
		if !c.manage {
			if t.Assignee != nil && t.Assignee.PersonID != nil {
				to = append(to, *t.Assignee.PersonID)
			} else if t.Assignee == nil {
				mgrs, err := s.managersOf(ctx, tx, orgID, t.SiteID)
				if err != nil {
					return err
				}
				to = append(to, mgrs...)
			}
		}
		seen := map[uuid.UUID]bool{c.person: true}
		var rcpt []jobs.Recipient
		for _, p := range to {
			if !seen[p] {
				seen[p] = true
				pid := p
				rcpt = append(rcpt, jobs.Recipient{PersonID: &pid})
			}
		}
		if len(rcpt) == 0 {
			return nil
		}
		return s.notify.Notify(ctx, tx, jobs.NotifyArgs{OrgID: orgID, SiteID: &t.SiteID, Type: "ticket.commented", Recipients: rcpt,
			Key:     "ticket.commented:" + cid.String(),
			Payload: map[string]any{"ticket_id": ticketID, "title": t.Title, "author": out.AuthorName}, Link: "/tickets/" + ticketID.String()})
	})
	return out, tenancy.DBProblem(err, "comment")
}

// managersOf returns the people who run the site's tickets: administrators,
// site managers and the board, by a membership in force at the site.
func (s *Service) managersOf(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT person_id FROM membership WHERE org_id = $1 AND role IN ('org_admin', 'site_manager', 'board_member')
		AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now()) AND (scope_type = 'org' OR (scope_type = 'site' AND scope_id = $2))`, orgID, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var p uuid.UUID
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateAttachment attaches an uploaded file to a ticket or to one of its
// comments. The reporter, management and a comment's author may.
func (s *Service) CreateAttachment(ctx context.Context, orgID, ticketID, fileID uuid.UUID, commentID *uuid.UUID) (Attachment, error) {
	var out Attachment
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		t, c, err := s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		if err := s.mayChangeAttachments(ctx, tx, orgID, t, c, commentID); err != nil {
			return err
		}
		if err := s.attach(ctx, tx, orgID, t.SiteID, ticketID, commentID, fileID); err != nil {
			return err
		}
		out, err = scanAttachment(tx.QueryRow(ctx, `SELECT id, org_id, ticket_id, comment_id, file_id, mime, size, created_at, updated_at FROM ticket_attachment WHERE org_id = $1 AND file_id = $2`, orgID, fileID))
		return err
	})
	return out, tenancy.DBProblem(err, "attachment")
}

func (s *Service) mayChangeAttachments(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, t Ticket, c caps, commentID *uuid.UUID) error {
	if c.manage || (t.ReporterPersonID == c.person && c.person != uuid.Nil) {
		if commentID == nil {
			return nil
		}
	}
	if commentID != nil {
		var author uuid.UUID
		err := tx.QueryRow(ctx, `SELECT author_person_id FROM ticket_comment WHERE org_id = $1 AND id = $2 AND ticket_id = $3`, orgID, *commentID, t.ID).Scan(&author)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such comment")
		}
		if err != nil {
			return err
		}
		if author == c.person || c.manage {
			return nil
		}
	}
	return problem.Forbidden("you may not change the attachments of this ticket")
}

// DeleteAttachment removes an attachment; the file itself expires as an unattached draft.
func (s *Service) DeleteAttachment(ctx context.Context, orgID, ticketID, attID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		t, c, err := s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		var fileID uuid.UUID
		var commentID *uuid.UUID
		err = tx.QueryRow(ctx, `SELECT file_id, comment_id FROM ticket_attachment WHERE org_id = $1 AND id = $2 AND ticket_id = $3`, orgID, attID, ticketID).Scan(&fileID, &commentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such attachment")
		}
		if err != nil {
			return err
		}
		if err := s.mayChangeAttachments(ctx, tx, orgID, t, c, commentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM ticket_attachment WHERE org_id = $1 AND id = $2`, orgID, attID); err != nil {
			return err
		}
		return s.files.Detach(ctx, tx, orgID, fileID)
	})
	return tenancy.DBProblem(err, "attachment")
}

// Person is a user reference with a display name.
type Person struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
}

// StatusChange is one entry of the append-only status history.
type StatusChange struct {
	ID             uuid.UUID  `json:"id"`
	OrgID          uuid.UUID  `json:"org_id"`
	TicketID       uuid.UUID  `json:"ticket_id"`
	FromStatusID   *uuid.UUID `json:"from_status_id"`
	FromStatusName *string    `json:"from_status_name"`
	ToStatusID     uuid.UUID  `json:"to_status_id"`
	ToStatusName   string     `json:"to_status_name"`
	ChangedBy      *Person    `json:"changed_by"`
	Comment        *string    `json:"comment"`
	At             time.Time  `json:"at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ListHistory lists a ticket's status history, oldest first. The cursor is
// opaque (time and id); internal comments are blanked for those who may not
// read them.
func (s *Service) ListHistory(ctx context.Context, orgID, ticketID uuid.UUID, p tenancy.PageParams) ([]StatusChange, string, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = tenancy.DefaultLimit
	}
	if limit > 200 {
		limit = 200
	}
	afterAt, afterID := time.Time{}, uuid.Nil
	if p.Cursor != "" {
		a, i, ok := strings.Cut(p.Cursor, "|")
		var err error
		if !ok {
			err = errors.New("bad cursor")
		} else if afterAt, err = time.Parse(time.RFC3339Nano, a); err == nil {
			afterID, err = uuid.Parse(i)
		}
		if err != nil {
			return nil, "", problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
		}
	}
	var out []StatusChange
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		t, c, err := s.loadVisible(ctx, tx, orgID, ticketID, false)
		if err != nil {
			return err
		}
		internal := s.canSeeInternal(c, t)
		rows, err := tx.Query(ctx, `SELECT h.id, h.org_id, h.ticket_id, h.from_status_id, fs.name, h.to_status_id, ts.name, h.changed_by, h.changed_by_name,
			CASE WHEN h.comment_internal AND NOT $5 THEN NULL ELSE h.comment END, h.at
			FROM ticket_status_history h
			JOIN ticket_status ts ON ts.org_id = h.org_id AND ts.id = h.to_status_id
			LEFT JOIN ticket_status fs ON fs.org_id = h.org_id AND fs.id = h.from_status_id
			WHERE h.org_id = $1 AND h.ticket_id = $2 AND ($3::timestamptz IS NULL OR (h.at, h.id) > ($3, $4))
			ORDER BY h.at, h.id LIMIT $6`, orgID, ticketID, nullTime(afterAt), afterID, internal, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h StatusChange
			var by *uuid.UUID
			var byName *string
			if err := rows.Scan(&h.ID, &h.OrgID, &h.TicketID, &h.FromStatusID, &h.FromStatusName, &h.ToStatusID, &h.ToStatusName, &by, &byName, &h.Comment, &h.At); err != nil {
				return err
			}
			if by != nil {
				n := ""
				if byName != nil {
					n = *byName
				}
				h.ChangedBy = &Person{ID: *by, DisplayName: n}
			}
			h.At = h.At.UTC()
			h.CreatedAt, h.UpdatedAt = h.At, h.At
			out = append(out, h)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "history")
	}
	if out == nil {
		out = []StatusChange{}
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = last.At.Format(time.RFC3339Nano) + "|" + last.ID.String()
	}
	return out, next, nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// resolver is the file visibility resolver of the "ticket" scope. The scope id
// is a ticket id (the ticket's own files) or a comment id (a comment's).
type resolver struct{ s *Service }

func (r resolver) lookup(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (t Ticket, c caps, internal bool, found bool, err error) {
	orgID := authz.GrantsFromContext(ctx).OrgID
	ticketID, commentVis := ref.ID, ""
	var cid uuid.UUID
	if e := tx.QueryRow(ctx, `SELECT ticket_id, visibility FROM ticket_comment WHERE org_id = $1 AND id = $2`, orgID, ref.ID).Scan(&cid, &commentVis); e == nil {
		ticketID = cid
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return t, c, false, false, e
	}
	t, c, err = r.s.loadVisible(ctx, tx, orgID, ticketID, false)
	if err != nil {
		var pe *problem.Error
		if errors.As(err, &pe) && (pe.Status == 404 || pe.Status == 403) {
			return t, c, false, false, nil
		}
		return t, c, false, false, err
	}
	return t, c, commentVis != "internal" || r.s.canSeeInternal(c, t), true, nil
}

// CanRead implements files.VisibilityResolver.
func (r resolver) CanRead(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
	_, _, ok, found, err := r.lookup(ctx, tx, ref)
	return found && ok, err
}

// CanAttach implements files.VisibilityResolver.
func (r resolver) CanAttach(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
	t, c, ok, found, err := r.lookup(ctx, tx, ref)
	if err != nil || !found || !ok {
		return false, err
	}
	return c.open && (c.manage || t.ReporterPersonID == c.person), nil
}

var _ = fmt.Sprintf
var _ = json.Marshal
