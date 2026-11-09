// Package announcements is the one-way notice board (backend plan 4.10,
// decisions O4 and O5): management publishes, residents read, the manager sees
// who has read. The sender chooses the channels and sees the recipient and SMS
// counts before publishing; delivery goes through the notification dispatcher.
package announcements

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
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/core/opsx"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Permissions of the module.
const (
	PermRead    authz.Permission = "announcements.read"
	PermPublish authz.Permission = "announcements.publish"
)

// AnyView is the permission set of the read operations.
var AnyView = authz.AnyOf(PermRead, PermPublish)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	all := []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite, authz.ScopeBlock}
	return []authz.PermissionDef{
		{Name: PermRead, Description: "Read announcements addressed to the caller and mark them read",
			Roles: authz.BuiltinRoles(), Scopes: all, Occupants: []authz.OccupancyKind{authz.OccOwner, authz.OccTenant, authz.OccHousehold}},
		{Name: PermPublish, Description: "Create, schedule and publish announcements; see read receipts",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleSiteManager, authz.RoleBoardMember}, Scopes: []authz.ScopeType{authz.ScopeOrg, authz.ScopeSite}},
	}
}

// DB is the database access the module needs, satisfied by *db.DB. The
// platform transaction is for the scheduled publisher, which looks across
// organizations; everything it writes goes through tenant transactions.
type DB interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// Config configures the Service.
type Config struct {
	// SMS and Mail say whether the channels are configured; publishing with an
	// unavailable channel answers 503.
	SMS, Mail bool
}

// Service is the announcements module.
type Service struct {
	db     DB
	files  *files.Service
	notify jobs.Notifier
	cfg    Config
	now    func() time.Time
}

// MaxFiles is the number of attachments of one announcement.
const MaxFiles = 10

// NewService returns a Service and registers the "announcement" visibility
// resolver with the file module.
func NewService(db DB, fs *files.Service, notify jobs.Notifier, cfg Config) *Service {
	if notify == nil {
		notify = jobs.DiscardNotifier{}
	}
	s := &Service{db: db, files: fs, notify: notify, cfg: cfg, now: time.Now}
	if fs != nil {
		fs.RegisterResolver("announcement", resolver{s}, files.WithMaxFiles(MaxFiles))
	}
	return s
}

// Statuses.
const (
	StatusDraft     = "draft"
	StatusScheduled = "scheduled"
	StatusPublished = "published"
	StatusArchived  = "archived"
)

// Audience selects who an announcement is for.
type Audience struct {
	Kind    string      `json:"kind"`
	BlockID *uuid.UUID  `json:"block_id,omitempty"`
	UnitIDs []uuid.UUID `json:"unit_ids,omitempty"`
}

// Attachment is a file on an announcement.
type Attachment struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Mime string    `json:"mime"`
	Size int64     `json:"size"`
}

// Announcement is a one-way notice.
type Announcement struct {
	ID             uuid.UUID    `json:"id"`
	OrgID          uuid.UUID    `json:"org_id"`
	SiteID         uuid.UUID    `json:"site_id"`
	Title          string       `json:"title"`
	BodyMD         string       `json:"body_md"`
	Status         string       `json:"status"`
	Audience       Audience     `json:"audience"`
	PublishAt      *time.Time   `json:"publish_at"`
	ExpiresAt      *time.Time   `json:"expires_at"`
	Pinned         bool         `json:"pinned"`
	Channels       []string     `json:"channels"`
	RecipientCount *int         `json:"recipient_count"`
	SMSCount       *int         `json:"sms_count"`
	SMSSegments    *int         `json:"sms_segments"`
	ReadCount      int          `json:"read_count"`
	ReadAt         *time.Time   `json:"read_at"`
	Attachments    []Attachment `json:"attachments"`
	PublishedAt    *time.Time   `json:"published_at"`
	CreatedBy      *uuid.UUID   `json:"created_by"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	Version        int          `json:"version"`
}

// $1 org, $2 me (person, may be the nil uuid), $3 shows read counts
const selectSQL = `SELECT a.id, a.org_id, a.site_id, a.title, a.body_md, a.status, a.audience, a.publish_at, a.expires_at, a.pinned, a.channels,
	a.recipient_count, a.sms_count, a.sms_segments,
	CASE WHEN $3 THEN (SELECT count(*) FROM announcement_read r WHERE r.org_id = a.org_id AND r.announcement_id = a.id) ELSE 0 END,
	(SELECT r.read_at FROM announcement_read r WHERE r.org_id = a.org_id AND r.announcement_id = a.id AND r.person_id = $2),
	a.published_at, a.created_by, a.created_at, a.updated_at, a.version
	FROM announcement a`

func scanAnn(row pgx.Row) (Announcement, error) {
	var a Announcement
	var aud []byte
	err := row.Scan(&a.ID, &a.OrgID, &a.SiteID, &a.Title, &a.BodyMD, &a.Status, &aud, &a.PublishAt, &a.ExpiresAt, &a.Pinned, &a.Channels,
		&a.RecipientCount, &a.SMSCount, &a.SMSSegments, &a.ReadCount, &a.ReadAt, &a.PublishedAt, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt, &a.Version)
	if err != nil {
		return a, err
	}
	_ = json.Unmarshal(aud, &a.Audience)
	if a.Channels == nil {
		a.Channels = []string{}
	}
	a.Attachments = []Attachment{}
	utc := func(p **time.Time) {
		if *p != nil {
			t := (*p).UTC()
			*p = &t
		}
	}
	utc(&a.PublishAt)
	utc(&a.ExpiresAt)
	utc(&a.ReadAt)
	utc(&a.PublishedAt)
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return a, nil
}

type viewer struct {
	scope   authz.Scope
	person  uuid.UUID
	publish bool
}

func (s *Service) viewerOf(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (viewer, error) {
	sc, err := opsx.SiteScope(ctx, tx, siteID)
	if err != nil {
		return viewer{}, err
	}
	held, err := opsx.RequireAny(ctx, sc, AnyView)
	if err != nil {
		return viewer{}, err
	}
	v := viewer{scope: sc, publish: opsx.Contains(held, PermPublish)}
	if p, ok, err := opsx.PersonOf(ctx, tx, orgID); err != nil {
		return v, err
	} else if ok {
		v.person = p
	}
	return v, nil
}

// visibleSQL restricts non-publishers to published, unexpired announcements:
// the ones addressed to them when it was published, and pinned ones whose
// audience they belong to today (a resident who moves in later still sees the
// house rules). The frozen recipient list is also the delivery list; it does
// not limit what a pinned notice shows to.
const visibleSQL = `($4 OR (a.status = 'published' AND (a.expires_at IS NULL OR a.expires_at > now())
	AND (EXISTS (SELECT 1 FROM announcement_recipient rc WHERE rc.org_id = a.org_id AND rc.announcement_id = a.id AND rc.person_id = $2)
	  OR (a.pinned AND EXISTS (SELECT 1 FROM occupancy o JOIN unit un ON un.org_id = o.org_id AND un.id = o.unit_id JOIN site st ON st.org_id = o.org_id AND st.id = o.site_id
	      WHERE o.org_id = a.org_id AND o.site_id = a.site_id AND o.person_id = $2
	        AND o.valid_from <= (now() AT TIME ZONE st.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE st.timezone)::date)
	        AND (CASE a.audience->>'kind' WHEN 'site' THEN true WHEN 'owners' THEN o.kind = 'owner' WHEN 'tenants' THEN o.kind = 'tenant'
	             WHEN 'block' THEN un.block_id = (a.audience->>'block_id')::uuid
	             WHEN 'units' THEN o.unit_id::text IN (SELECT jsonb_array_elements_text(a.audience->'unit_ids')) ELSE false END))))))`

func (s *Service) loadOne(ctx context.Context, tx pgx.Tx, orgID, annID uuid.UUID, forUpdate bool) (Announcement, viewer, error) {
	var site uuid.UUID
	err := tx.QueryRow(ctx, `SELECT site_id FROM announcement WHERE org_id = $1 AND id = $2`, orgID, annID).Scan(&site)
	if errors.Is(err, pgx.ErrNoRows) {
		return Announcement{}, viewer{}, problem.NotFound("no such announcement")
	}
	if err != nil {
		return Announcement{}, viewer{}, err
	}
	v, err := s.viewerOf(ctx, tx, orgID, site)
	if err != nil {
		return Announcement{}, v, err
	}
	q := selectSQL + ` WHERE a.org_id = $1 AND a.id = $5 AND ` + visibleSQL
	if forUpdate {
		q += ` FOR UPDATE OF a`
	}
	a, err := scanAnn(tx.QueryRow(ctx, q, orgID, v.person, v.publish, v.publish, annID))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, v, problem.NotFound("no such announcement")
	}
	if err != nil {
		return a, v, err
	}
	return a, v, s.fillFiles(ctx, tx, orgID, []*Announcement{&a})
}

func (s *Service) fillFiles(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, as []*Announcement) error {
	if len(as) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(as))
	by := map[uuid.UUID]*Announcement{}
	for i, a := range as {
		ids[i], by[a.ID] = a.ID, a
	}
	rows, err := tx.Query(ctx, `SELECT af.announcement_id, f.id, f.name, f.mime, f.size FROM announcement_file af
		JOIN file f ON f.org_id = af.org_id AND f.id = af.file_id WHERE af.org_id = $1 AND af.announcement_id = ANY($2) ORDER BY af.position, f.id`, orgID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var aid uuid.UUID
		var at Attachment
		if err := rows.Scan(&aid, &at.ID, &at.Name, &at.Mime, &at.Size); err != nil {
			return err
		}
		by[aid].Attachments = append(by[aid].Attachments, at)
	}
	return rows.Err()
}

// Get returns an announcement (publishers any, others the ones addressed to them).
func (s *Service) Get(ctx context.Context, orgID, annID uuid.UUID) (Announcement, error) {
	var a Announcement
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		a, _, err = s.loadOne(ctx, tx, orgID, annID, false)
		return err
	})
	return a, tenancy.DBProblem(err, "announcement")
}

// Filter narrows List.
type Filter struct {
	SiteID   uuid.UUID
	Status   string
	Pinned   *bool
	Unread   bool
	From, To *time.Time
}

// List lists a site's announcements, pinned first, then newest first. The
// cursor is opaque.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, f Filter, p tenancy.PageParams) ([]Announcement, string, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = tenancy.DefaultLimit
	}
	if limit > 200 {
		limit = 200
	}
	cursorPinned, cursorID := true, uuid.Nil
	if p.Cursor != "" {
		pin, i, ok := strings.Cut(p.Cursor, "|")
		u, err := uuid.Parse(i)
		if !ok || err != nil || (pin != "0" && pin != "1") {
			return nil, "", problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
		}
		cursorPinned, cursorID = pin == "1", u
	}
	if f.Status != "" && !slices.Contains([]string{StatusDraft, StatusScheduled, StatusPublished, StatusArchived}, f.Status) {
		return nil, "", problem.Validation(problem.Field{Field: "status", Code: "invalid_value"})
	}
	var out []Announcement
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		v, err := s.viewerOf(ctx, tx, orgID, f.SiteID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, selectSQL+` WHERE a.org_id = $1 AND a.site_id = $5 AND `+visibleSQL+`
			AND ($6 = '' OR a.status = $6) AND ($7::boolean IS NULL OR a.pinned = $7)
			AND (NOT $8 OR (a.status = 'published' AND NOT EXISTS (SELECT 1 FROM announcement_read r WHERE r.org_id = a.org_id AND r.announcement_id = a.id AND r.person_id = $2)))
			AND ($9::timestamptz IS NULL OR coalesce(a.published_at, a.created_at) >= $9) AND ($10::timestamptz IS NULL OR coalesce(a.published_at, a.created_at) < $10)
			AND ($11 = '00000000-0000-0000-0000-000000000000'::uuid OR (a.pinned, a.id) < ($12, $11))
			ORDER BY a.pinned DESC, a.id DESC LIMIT $13`,
			orgID, v.person, v.publish, v.publish, f.SiteID, f.Status, f.Pinned, f.Unread, f.From, f.To, cursorID, cursorPinned, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAnn(rows)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		ptrs := make([]*Announcement, len(out))
		for i := range out {
			ptrs[i] = &out[i]
		}
		return s.fillFiles(ctx, tx, orgID, ptrs)
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "announcements")
	}
	if out == nil {
		out = []Announcement{}
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		pin := "0"
		if last.Pinned {
			pin = "1"
		}
		next = pin + "|" + last.ID.String()
	}
	return out, next, nil
}

type input struct {
	title, body *string
	audience    *Audience
	publishAt   *time.Time
	expiresAt   *time.Time
	pinned      *bool
	channels    []string
	fileIDs     []uuid.UUID
	has         map[string]bool
}

func parseInput(d tenancy.Doc, create bool) (input, error) {
	in := input{has: map[string]bool{}}
	allowed := []string{"title", "body_md", "audience", "publish_at", "expires_at", "pinned", "channels", "file_ids"}
	if create {
		allowed = append(allowed, "site_id")
	}
	bad := d.Unknown(allowed...)
	for k := range d {
		in.has[k] = true
	}
	var err error
	if in.title, err = d.String("title"); err != nil {
		return in, err
	}
	if in.body, err = d.String("body_md"); err != nil {
		return in, err
	}
	if in.title != nil {
		t := strings.TrimSpace(*in.title)
		in.title = &t
		if l := len([]rune(t)); l < 1 || l > 200 {
			bad = append(bad, problem.Field{Field: "title", Code: "invalid_length"})
		}
	} else if create || d.Has("title") {
		bad = append(bad, problem.Field{Field: "title", Code: "required"})
	}
	if in.body != nil {
		if l := len([]rune(*in.body)); l < 1 || l > 20000 {
			bad = append(bad, problem.Field{Field: "body_md", Code: "invalid_length"})
		}
	} else if create || d.Has("body_md") {
		bad = append(bad, problem.Field{Field: "body_md", Code: "required"})
	}
	if d.Has("audience") {
		var a Audience
		if err := d.Get("audience", &a); err != nil {
			return in, err
		}
		in.audience = &a
	} else if create {
		bad = append(bad, problem.Field{Field: "audience", Code: "required"})
	}
	for _, kv := range []struct {
		k string
		p **time.Time
	}{{"publish_at", &in.publishAt}, {"expires_at", &in.expiresAt}} {
		sp, err := d.String(kv.k)
		if err != nil {
			return in, err
		}
		if sp != nil {
			t, perr := time.Parse(time.RFC3339, *sp)
			if perr != nil {
				bad = append(bad, problem.Field{Field: kv.k, Code: "invalid_format"})
				continue
			}
			*kv.p = &t
		}
	}
	if d.Has("pinned") {
		var p bool
		if err := d.Get("pinned", &p); err != nil {
			return in, err
		}
		in.pinned = &p
	}
	if d.Has("channels") {
		if err := d.Get("channels", &in.channels); err != nil {
			return in, err
		}
		if bc := checkChannels(in.channels, "channels"); len(bc) > 0 {
			bad = append(bad, bc...)
		}
	}
	if d.Has("file_ids") {
		if err := d.Get("file_ids", &in.fileIDs); err != nil {
			return in, err
		}
		if len(in.fileIDs) > MaxFiles {
			bad = append(bad, problem.Field{Field: "file_ids", Code: "too_many"})
		}
	}
	if len(bad) > 0 {
		return in, problem.Validation(bad...)
	}
	return in, nil
}

func checkChannels(ch []string, field string) []problem.Field {
	var bad []problem.Field
	if len(ch) == 0 {
		return []problem.Field{{Field: field, Code: "required"}}
	}
	for i, c := range ch {
		switch c {
		case jobs.ChannelInApp, jobs.ChannelEmail, jobs.ChannelSMS, jobs.ChannelPush, jobs.ChannelChat:
		default:
			bad = append(bad, problem.Field{Field: fmt.Sprintf("%s[%d]", field, i), Code: "invalid_value"})
		}
	}
	return bad
}

func normChannels(ch []string) []string {
	var out []string
	for _, c := range []string{jobs.ChannelInApp, jobs.ChannelEmail, jobs.ChannelSMS, jobs.ChannelPush, jobs.ChannelChat} {
		if slices.Contains(ch, c) {
			out = append(out, c)
		}
	}
	return out
}

func (s *Service) checkAudience(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, a Audience) error {
	switch a.Kind {
	case "site", "owners", "tenants":
		return nil
	case "block":
		var ok bool
		if a.BlockID == nil || tx.QueryRow(ctx, `SELECT true FROM block WHERE org_id = $1 AND site_id = $2 AND id = $3`, orgID, siteID, *a.BlockID).Scan(&ok) != nil {
			return problem.Validation(problem.Field{Field: "audience.block_id", Code: "invalid_value"})
		}
	case "units":
		if len(a.UnitIDs) == 0 {
			return problem.Validation(problem.Field{Field: "audience.unit_ids", Code: "required"})
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM unit WHERE org_id = $1 AND site_id = $2 AND id = ANY($3)`, orgID, siteID, a.UnitIDs).Scan(&n); err != nil {
			return err
		}
		uniq := map[uuid.UUID]bool{}
		for _, u := range a.UnitIDs {
			uniq[u] = true
		}
		if n != len(uniq) {
			return problem.Validation(problem.Field{Field: "audience.unit_ids", Code: "invalid_value"})
		}
	default:
		return problem.Validation(problem.Field{Field: "audience.kind", Code: "invalid_value"})
	}
	return nil
}

// Create saves an announcement as a draft; with publish_at it is scheduled.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, d tenancy.Doc) (Announcement, error) {
	in, err := parseInput(d, true)
	if err != nil {
		return Announcement{}, err
	}
	sp, err := d.String("site_id")
	if err != nil {
		return Announcement{}, err
	}
	siteID, perr := uuid.Parse(deref(sp))
	if perr != nil {
		return Announcement{}, problem.Validation(problem.Field{Field: "site_id", Code: "required"})
	}
	var out Announcement
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := opsx.SiteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermPublish, sc); err != nil {
			return err
		}
		if err := s.checkAudience(ctx, tx, orgID, siteID, *in.audience); err != nil {
			return err
		}
		ch := normChannels(in.channels)
		if len(ch) == 0 {
			ch = []string{jobs.ChannelInApp}
		}
		status := StatusDraft
		if in.publishAt != nil {
			status = StatusScheduled
		}
		aud, _ := json.Marshal(in.audience)
		aid := id.New()
		pinned := in.pinned != nil && *in.pinned
		if _, err := tx.Exec(ctx, `INSERT INTO announcement (id, org_id, site_id, title, body_md, status, audience, publish_at, expires_at, pinned, channels, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			aid, orgID, siteID, *in.title, *in.body, status, aud, in.publishAt, in.expiresAt, pinned, ch, actorUser(ctx)); err != nil {
			return err
		}
		if err := s.setFiles(ctx, tx, orgID, aid, in.fileIDs); err != nil {
			return err
		}
		out, _, err = s.loadOne(ctx, tx, orgID, aid, false)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, "announcements.create", nil, out)
	})
	return out, tenancy.DBProblem(err, "announcement")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func actorUser(ctx context.Context) *uuid.UUID {
	if p := authz.PrincipalFromContext(ctx); p != nil {
		u := p.UserID
		return &u
	}
	return nil
}

type annAudit struct {
	Title    string     `json:"title"`
	Body     string     `json:"body_md"`
	Status   string     `json:"status"`
	Audience Audience   `json:"audience"`
	Pinned   bool       `json:"pinned"`
	Channels []string   `json:"channels"`
	Publish  *time.Time `json:"publish_at"`
	Expires  *time.Time `json:"expires_at"`
}

func (s *Service) record(ctx context.Context, tx pgx.Tx, action string, before *Announcement, after Announcement) error {
	view := func(a Announcement) annAudit {
		return annAudit{a.Title, a.BodyMD, a.Status, a.Audience, a.Pinned, a.Channels, a.PublishAt, a.ExpiresAt}
	}
	var b any
	if before != nil {
		b = view(*before)
	}
	diff, _ := audit.Diff("announcement", b, view(after))
	site := after.SiteID
	return audit.Record(ctx, tx, audit.Event{OrgID: after.OrgID, SiteID: &site, Action: action, TargetType: "announcement", TargetID: after.ID, Diff: diff})
}

// setFiles replaces the announcement's attachments by fileIDs.
func (s *Service) setFiles(ctx context.Context, tx pgx.Tx, orgID, annID uuid.UUID, fileIDs []uuid.UUID) error {
	if fileIDs == nil {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT file_id FROM announcement_file WHERE org_id = $1 AND announcement_id = $2`, orgID, annID)
	if err != nil {
		return err
	}
	var have []uuid.UUID
	for rows.Next() {
		var f uuid.UUID
		if err := rows.Scan(&f); err != nil {
			rows.Close()
			return err
		}
		have = append(have, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, f := range have {
		if !slices.Contains(fileIDs, f) {
			if _, err := tx.Exec(ctx, `DELETE FROM announcement_file WHERE org_id = $1 AND announcement_id = $2 AND file_id = $3`, orgID, annID, f); err != nil {
				return err
			}
			if err := s.files.Detach(ctx, tx, orgID, f); err != nil {
				return err
			}
		}
	}
	for i, f := range fileIDs {
		if !slices.Contains(have, f) {
			if _, err := s.files.Attach(ctx, tx, orgID, f, files.ScopeRef{Type: "announcement", ID: annID}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO announcement_file (org_id, announcement_id, file_id, position) VALUES ($1, $2, $3, $4)`, orgID, annID, f, i); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE announcement_file SET position = $4 WHERE org_id = $1 AND announcement_id = $2 AND file_id = $3`, orgID, annID, f, i); err != nil {
			return err
		}
	}
	return nil
}

// Update edits an announcement (announcements.publish). A published one keeps
// its content: only pinned and expires_at change (409 otherwise); an archived
// one does not change.
func (s *Service) Update(ctx context.Context, orgID, annID uuid.UUID, expected int, d tenancy.Doc) (Announcement, error) {
	in, err := parseInput(d, false)
	if err != nil {
		return Announcement{}, err
	}
	var out Announcement
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, v, err := s.loadOne(ctx, tx, orgID, annID, true)
		if err != nil {
			return err
		}
		if !v.publish {
			return problem.Forbidden("you need announcements.publish")
		}
		if before.Version != expected {
			return tenancy.ErrVersion
		}
		content := in.title != nil || in.body != nil || in.audience != nil || in.has["publish_at"] || in.channels != nil || in.fileIDs != nil
		if before.Status == StatusArchived || (before.Status == StatusPublished && content) {
			return problem.Conflict("a published or archived announcement keeps its content; only pin and expiry can change")
		}
		next := before
		if in.title != nil {
			next.Title = *in.title
		}
		if in.body != nil {
			next.BodyMD = *in.body
		}
		if in.audience != nil {
			if err := s.checkAudience(ctx, tx, orgID, before.SiteID, *in.audience); err != nil {
				return err
			}
			next.Audience = *in.audience
		}
		if in.has["publish_at"] {
			next.PublishAt = in.publishAt
			switch {
			case before.Status == StatusPublished:
			case in.publishAt != nil:
				next.Status = StatusScheduled
			default:
				next.Status = StatusDraft
			}
		}
		if in.has["expires_at"] {
			next.ExpiresAt = in.expiresAt
		}
		if in.pinned != nil {
			next.Pinned = *in.pinned
		}
		if in.channels != nil {
			next.Channels = normChannels(in.channels)
		}
		aud, _ := json.Marshal(next.Audience)
		if _, err := tx.Exec(ctx, `UPDATE announcement SET title = $3, body_md = $4, status = $5, audience = $6, publish_at = $7, expires_at = $8, pinned = $9, channels = $10,
			updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`,
			orgID, annID, next.Title, next.BodyMD, next.Status, aud, next.PublishAt, next.ExpiresAt, next.Pinned, next.Channels); err != nil {
			return err
		}
		if err := s.setFiles(ctx, tx, orgID, annID, in.fileIDs); err != nil {
			return err
		}
		out, _, err = s.loadOne(ctx, tx, orgID, annID, false)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, "announcements.update", &before, out)
	})
	return out, tenancy.DBProblem(err, "announcement")
}

// Delete removes a draft or scheduled announcement.
func (s *Service) Delete(ctx context.Context, orgID, annID uuid.UUID, expected int) error {
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, v, err := s.loadOne(ctx, tx, orgID, annID, true)
		if err != nil {
			return err
		}
		if !v.publish {
			return problem.Forbidden("you need announcements.publish")
		}
		if before.Version != expected {
			return tenancy.ErrVersion
		}
		if before.Status != StatusDraft && before.Status != StatusScheduled {
			return problem.Conflict("only a draft can be deleted; archive a published announcement")
		}
		for _, f := range before.Attachments {
			if err := s.files.Detach(ctx, tx, orgID, f.ID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM announcement WHERE org_id = $1 AND id = $2`, orgID, annID); err != nil {
			return err
		}
		return s.record(ctx, tx, "announcements.delete", &before, Announcement{ID: annID, OrgID: orgID, SiteID: before.SiteID, Status: "deleted", Channels: before.Channels})
	})
	return tenancy.DBProblem(err, "announcement")
}

// Archive archives a published announcement.
func (s *Service) Archive(ctx context.Context, orgID, annID uuid.UUID) (Announcement, error) {
	var out Announcement
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, v, err := s.loadOne(ctx, tx, orgID, annID, true)
		if err != nil {
			return err
		}
		if !v.publish {
			return problem.Forbidden("you need announcements.publish")
		}
		if before.Status != StatusPublished {
			return problem.Conflict("only a published announcement can be archived")
		}
		if _, err := tx.Exec(ctx, `UPDATE announcement SET status = 'archived', pinned = false, updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, orgID, annID); err != nil {
			return err
		}
		out, _, err = s.loadOne(ctx, tx, orgID, annID, false)
		if err != nil {
			return err
		}
		return s.record(ctx, tx, "announcements.archive", &before, out)
	})
	return out, tenancy.DBProblem(err, "announcement")
}

// Preview is what publishing would reach and cost (O5).
type Preview struct {
	RecipientCount int `json:"recipient_count"`
	SMSCount       int `json:"sms_count"`
	// SMSSegments is what the SMS bill counts: a long text or a Turkish glyph
	// (UCS-2, 70 characters a segment) makes several per message.
	SMSSegments int `json:"sms_segments"`
	MailCount   int `json:"email_count"`
	InAppCount  int `json:"in_app_count"`
}

type recipient struct {
	person           uuid.UUID
	phone, mail, app bool
	locale           string
	muted            bool // muted announcement.published: addressed, but nothing is sent
}

// smsTitleMax caps the title in a text message.
const smsTitleMax = 100

func smsTitle(t string) string {
	r := []rune(strings.TrimSpace(t))
	if len(r) > smsTitleMax {
		return string(r[:smsTitleMax-1]) + "…"
	}
	return string(r)
}

// recipients resolves an audience to the people currently in the site's units.
func recipients(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, a Audience) ([]recipient, error) {
	rows, err := tx.Query(ctx, `SELECT p.id, coalesce(p.contact->>'phone', u.phone, '') <> '', coalesce(p.contact->>'email', u.email::text, '') <> '', p.user_id IS NOT NULL,
			coalesce(u.locale, ''), EXISTS (SELECT 1 FROM notification_mute m WHERE m.org_id = p.org_id AND m.person_id = p.id AND m.type = 'announcement.published')
		FROM person p LEFT JOIN "user" u ON u.id = p.user_id
		WHERE p.org_id = $1 AND p.id IN (
			SELECT o.person_id FROM occupancy o
			JOIN unit un ON un.org_id = o.org_id AND un.id = o.unit_id
			JOIN site st ON st.org_id = o.org_id AND st.id = o.site_id
			WHERE o.org_id = $1 AND o.site_id = $2
			  AND o.valid_from <= (now() AT TIME ZONE st.timezone)::date AND (o.valid_to IS NULL OR o.valid_to > (now() AT TIME ZONE st.timezone)::date)
			  AND (CASE $3 WHEN 'site' THEN true WHEN 'owners' THEN o.kind = 'owner' WHEN 'tenants' THEN o.kind = 'tenant'
			       WHEN 'block' THEN un.block_id = $4 WHEN 'units' THEN o.unit_id = ANY($5) ELSE false END))
		ORDER BY p.id`, orgID, siteID, a.Kind, a.BlockID, a.UnitIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipient
	for rows.Next() {
		var r recipient
		if err := rows.Scan(&r.person, &r.phone, &r.mail, &r.app, &r.locale, &r.muted); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// countFor counts what the channels would send. People who muted the type are
// addressed (RecipientCount) but get nothing, so they are left out of the
// channel counts. The SMS segments come from the text rendered in each
// recipient's language, siteName and title being what the template fills in.
func countFor(rs []recipient, channels []string, siteName, title string) Preview {
	p := Preview{RecipientCount: len(rs)}
	segs := map[string]int{}
	for _, r := range rs {
		if r.muted {
			continue
		}
		if slices.Contains(channels, jobs.ChannelSMS) && r.phone {
			p.SMSCount++
			loc := notifications.NormalizeLocale(r.locale)
			n, ok := segs[loc]
			if !ok {
				n = notifications.SMSSegments(notifications.Render("announcement.published", loc, map[string]any{"site": siteName, "title": title, "sms_title": smsTitle(title)}).SMS)
				segs[loc] = n
			}
			p.SMSSegments += n
		}
		if slices.Contains(channels, jobs.ChannelEmail) && r.mail {
			p.MailCount++
		}
		if slices.Contains(channels, jobs.ChannelInApp) && r.app {
			p.InAppCount++
		}
	}
	return p
}

// PreviewAnnouncement counts the recipients of an audience and what the chosen
// channels would send, without saving anything.
func (s *Service) PreviewAnnouncement(ctx context.Context, orgID, siteID uuid.UUID, a Audience, channels []string, title string) (Preview, error) {
	if bad := checkChannels(channels, "channels"); len(bad) > 0 {
		return Preview{}, problem.Validation(bad...)
	}
	var out Preview
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := opsx.SiteScope(ctx, tx, siteID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermPublish, sc); err != nil {
			return err
		}
		if err := s.checkAudience(ctx, tx, orgID, siteID, a); err != nil {
			return err
		}
		rs, err := recipients(ctx, tx, orgID, siteID, a)
		if err != nil {
			return err
		}
		var siteName string
		if err := tx.QueryRow(ctx, `SELECT name FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&siteName); err != nil {
			return err
		}
		if strings.TrimSpace(title) == "" {
			title = strings.Repeat("ş", smsTitleMax) // no title yet: the longest a text takes, in the widest alphabet
		}
		out = countFor(rs, channels, siteName, title)
		return nil
	})
	return out, tenancy.DBProblem(err, "announcement")
}

func snippet(md string) string {
	t := strings.Join(strings.Fields(md), " ")
	r := []rune(t)
	if len(r) > 140 {
		return string(r[:139]) + "…"
	}
	return t
}

// Publish publishes a draft or scheduled announcement now: it fixes the
// recipients and their counts and hands the message to the dispatcher with
// the chosen channels (override replaces the stored ones). The HTTP layer
// requires an Idempotency-Key.
func (s *Service) Publish(ctx context.Context, orgID, annID uuid.UUID, override []string) (Announcement, error) {
	if override != nil {
		if bad := checkChannels(override, "channels"); len(bad) > 0 {
			return Announcement{}, problem.Validation(bad...)
		}
	}
	var out Announcement
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, v, err := s.loadOne(ctx, tx, orgID, annID, true)
		if err != nil {
			return err
		}
		if !v.publish {
			return problem.Forbidden("you need announcements.publish")
		}
		out, err = s.publishLocked(ctx, tx, before, override, true)
		return err
	})
	return out, tenancy.DBProblem(err, "announcement")
}

func (s *Service) publishLocked(ctx context.Context, tx pgx.Tx, before Announcement, override []string, checkGateways bool) (Announcement, error) {
	if before.Status != StatusDraft && before.Status != StatusScheduled {
		return Announcement{}, problem.Conflict("the announcement is already " + before.Status)
	}
	channels := before.Channels
	if override != nil {
		channels = normChannels(override)
	}
	if checkGateways {
		if slices.Contains(channels, jobs.ChannelSMS) && !s.cfg.SMS {
			return Announcement{}, problem.New(503, problem.CodeSMSUnavailable, "no SMS provider is configured")
		}
		if slices.Contains(channels, jobs.ChannelEmail) && !s.cfg.Mail {
			return Announcement{}, problem.New(503, problem.CodeMailUnavailable, "no mail provider is configured")
		}
	}
	rs, err := recipients(ctx, tx, before.OrgID, before.SiteID, before.Audience)
	if err != nil {
		return Announcement{}, err
	}
	var siteName string
	if err := tx.QueryRow(ctx, `SELECT name FROM site WHERE org_id = $1 AND id = $2`, before.OrgID, before.SiteID).Scan(&siteName); err != nil {
		return Announcement{}, err
	}
	pv := countFor(rs, channels, siteName, before.Title)
	if _, err := tx.Exec(ctx, `INSERT INTO announcement_recipient (org_id, announcement_id, person_id) SELECT $1, $2, unnest($3::uuid[]) ON CONFLICT DO NOTHING`,
		before.OrgID, before.ID, personIDs(rs)); err != nil {
		return Announcement{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE announcement SET status = 'published', published_at = now(), channels = $3, recipient_count = $4, sms_count = $5, sms_segments = $6,
		updated_at = now(), version = version + 1 WHERE org_id = $1 AND id = $2`, before.OrgID, before.ID, channels, pv.RecipientCount, pv.SMSCount, pv.SMSSegments); err != nil {
		return Announcement{}, err
	}
	var out Announcement
	rec := make([]jobs.Recipient, len(rs))
	for i, r := range rs {
		p := r.person
		rec[i] = jobs.Recipient{PersonID: &p}
	}
	site := before.SiteID
	if err := s.notify.Notify(ctx, tx, jobs.NotifyArgs{OrgID: before.OrgID, SiteID: &site, Type: "announcement.published", Recipients: rec, Channels: channels,
		Payload: map[string]any{"announcement_id": before.ID, "title": before.Title, "sms_title": smsTitle(before.Title), "snippet": snippet(before.BodyMD)}, Link: "/announcements/" + before.ID.String(),
		Key: "announcement:" + before.ID.String()}); err != nil {
		return out, err
	}
	out, err = scanAnn(tx.QueryRow(ctx, selectSQL+` WHERE a.org_id = $1 AND a.id = $4`, before.OrgID, uuid.Nil, true, before.ID))
	if err != nil {
		return out, err
	}
	if err := s.fillFiles(ctx, tx, before.OrgID, []*Announcement{&out}); err != nil {
		return out, err
	}
	return out, s.record(ctx, tx, "announcements.publish", &before, out)
}

func personIDs(rs []recipient) []uuid.UUID {
	out := make([]uuid.UUID, len(rs))
	for i, r := range rs {
		out[i] = r.person
	}
	return out
}

// PublishDue publishes the scheduled announcements whose time has come. It is
// the catch-up task of the worker: idempotent, one transaction per
// announcement, so one failure does not hold back the others.
func (s *Service) PublishDue(ctx context.Context) error {
	type due struct{ org, id uuid.UUID }
	var list []due
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		// a notice whose end passed while it waited is never published
		if _, err := tx.Exec(ctx, `UPDATE announcement SET status = 'archived', pinned = false, updated_at = now(), version = version + 1
			WHERE status = 'scheduled' AND publish_at <= now() AND expires_at IS NOT NULL AND expires_at <= now()`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT org_id, id FROM announcement WHERE status = 'scheduled' AND publish_at <= now() AND (expires_at IS NULL OR expires_at > now()) ORDER BY publish_at LIMIT 200`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.org, &d.id); err != nil {
				return err
			}
			list = append(list, d)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		err := s.db.InTenantTx(ctx, d.org, func(tx pgx.Tx) error {
			a, err := scanAnn(tx.QueryRow(ctx, selectSQL+` WHERE a.org_id = $1 AND a.id = $4 AND a.status = 'scheduled' FOR UPDATE OF a`, d.org, uuid.Nil, true, d.id))
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			// the same gateway check as a manual publish: a scheduled SMS with no
			// provider stays scheduled (and the error is reported) instead of
			// publishing a count that is never sent
			_, err = s.publishLocked(ctx, tx, a, nil, true)
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("announcement %s: %w", d.id, err))
		}
	}
	return errors.Join(errs...)
}

// Read is one line of the read receipts.
type Read struct {
	PersonID        uuid.UUID  `json:"person_id"`
	DisplayName     string     `json:"display_name"`
	UnitDoorNumbers []string   `json:"unit_door_numbers"`
	ReadAt          *time.Time `json:"read_at"`
}

// Reads is the receipts page with the counts beside it.
type Reads struct {
	Items         []Read
	Next          string
	AudienceCount int
	ReadCount     int
}

// ListReads lists the people an announcement was addressed to and whether they
// read it, sorted by display name then person id (the cursor is opaque);
// read=true or false filters.
func (s *Service) ListReads(ctx context.Context, orgID, annID uuid.UUID, read *bool, p tenancy.PageParams) (Reads, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = tenancy.DefaultLimit
	}
	if limit > 200 {
		limit = 200
	}
	afterName, afterID, hasAfter := "", uuid.Nil, false
	if p.Cursor != "" {
		n, i, ok := strings.Cut(p.Cursor, "\x1f")
		u, err := uuid.Parse(i)
		if !ok || err != nil {
			return Reads{}, problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
		}
		afterName, afterID, hasAfter = n, u, true
	}
	var out Reads
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, v, err := s.loadOne(ctx, tx, orgID, annID, false)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermPublish, v.scope); err != nil {
			return err
		}
		_ = a
		if err := tx.QueryRow(ctx, `SELECT count(*), count(r.person_id) FROM announcement_recipient rc
			LEFT JOIN announcement_read r ON r.org_id = rc.org_id AND r.announcement_id = rc.announcement_id AND r.person_id = rc.person_id
			WHERE rc.org_id = $1 AND rc.announcement_id = $2`, orgID, annID).Scan(&out.AudienceCount, &out.ReadCount); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT p.id, p.display_name, r.read_at,
			coalesce((SELECT array_agg(DISTINCT un.door_number) FROM occupancy o JOIN unit un ON un.org_id = o.org_id AND un.id = o.unit_id
			          WHERE o.org_id = p.org_id AND o.person_id = p.id AND o.site_id = $3 AND o.valid_to IS NULL), '{}')
			FROM announcement_recipient rc
			JOIN person p ON p.org_id = rc.org_id AND p.id = rc.person_id
			LEFT JOIN announcement_read r ON r.org_id = rc.org_id AND r.announcement_id = rc.announcement_id AND r.person_id = rc.person_id
			WHERE rc.org_id = $1 AND rc.announcement_id = $2 AND ($4::boolean IS NULL OR ($4 = (r.person_id IS NOT NULL)))
			  AND (NOT $5 OR (p.display_name, p.id) > ($6, $7))
			ORDER BY p.display_name, p.id LIMIT $8`, orgID, annID, a.SiteID, read, hasAfter, afterName, afterID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Read
			if err := rows.Scan(&r.PersonID, &r.DisplayName, &r.ReadAt, &r.UnitDoorNumbers); err != nil {
				return err
			}
			if r.ReadAt != nil {
				t := r.ReadAt.UTC()
				r.ReadAt = &t
			}
			slices.Sort(r.UnitDoorNumbers)
			out.Items = append(out.Items, r)
		}
		return rows.Err()
	})
	if err != nil {
		return Reads{}, tenancy.DBProblem(err, "read receipts")
	}
	if out.Items == nil {
		out.Items = []Read{}
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		l := out.Items[len(out.Items)-1]
		out.Next = l.DisplayName + "\x1f" + l.PersonID.String()
	}
	return out, nil
}

// MarkRead records that the caller opened the announcement in the web or app.
func (s *Service) MarkRead(ctx context.Context, orgID, annID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, v, err := s.loadOne(ctx, tx, orgID, annID, false)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermRead, v.scope); err != nil {
			return err
		}
		if v.person == uuid.Nil || a.Status != StatusPublished {
			return nil
		}
		// only people it was addressed to count (a manager previewing does not)
		_, err = tx.Exec(ctx, `INSERT INTO announcement_read (org_id, announcement_id, person_id)
			SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM announcement_recipient WHERE org_id = $1 AND announcement_id = $2 AND person_id = $3)
			ON CONFLICT DO NOTHING`, orgID, annID, v.person)
		return err
	})
	return tenancy.DBProblem(err, "announcement")
}

// resolver is the file visibility resolver of the "announcement" scope.
type resolver struct{ s *Service }

func (r resolver) CanRead(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
	orgID := authz.GrantsFromContext(ctx).OrgID
	_, _, err := r.s.loadOne(ctx, tx, orgID, ref.ID, false)
	if err != nil {
		var pe *problem.Error
		if errors.As(err, &pe) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r resolver) CanAttach(ctx context.Context, tx pgx.Tx, ref files.ScopeRef) (bool, error) {
	orgID := authz.GrantsFromContext(ctx).OrgID
	_, v, err := r.s.loadOne(ctx, tx, orgID, ref.ID, false)
	if err != nil {
		var pe *problem.Error
		if errors.As(err, &pe) {
			return false, nil
		}
		return false, err
	}
	return v.publish, nil
}
