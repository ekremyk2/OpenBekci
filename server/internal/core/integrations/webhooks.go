package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// maxEndpointsPerOrg bounds the endpoints of one organization.
const maxEndpointsPerOrg = 20

// WebhookEndpoint is an outbound webhook endpoint as the API shows it. The
// signing secret is never part of it.
type WebhookEndpoint struct {
	ID                  uuid.UUID  `json:"id"`
	OrgID               uuid.UUID  `json:"org_id"`
	URL                 string     `json:"url"`
	Events              []string   `json:"events"`
	Active              bool       `json:"active"`
	Description         *string    `json:"description"`
	Failing             bool       `json:"failing"`
	FailingSince        *time.Time `json:"failing_since"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	CreatedBy           *uuid.UUID `json:"created_by"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	Version             int        `json:"version"`
}

// WebhookEndpointCreated is the answer to creating an endpoint: the signing
// secret is returned exactly once.
type WebhookEndpointCreated struct {
	Endpoint WebhookEndpoint `json:"endpoint"`
	Secret   string          `json:"secret"`
}

// WebhookDelivery is one delivery of an event to an endpoint, as logged.
type WebhookDelivery struct {
	ID           uuid.UUID  `json:"id"`
	EndpointID   uuid.UUID  `json:"endpoint_id"`
	EventID      uuid.UUID  `json:"event_id"`
	EventType    string     `json:"event_type"`
	Status       string     `json:"status"` // pending, succeeded or failed (open enum)
	Attempts     int        `json:"attempts"`
	ResponseCode *int       `json:"response_code"`
	LastError    *string    `json:"last_error"`
	NextRetryAt  *time.Time `json:"next_retry_at"`
	CreatedAt    time.Time  `json:"created_at"`
	DeliveredAt  *time.Time `json:"delivered_at"`
}

// EndpointInput is the request to create an endpoint.
type EndpointInput struct {
	URL         string
	Events      []string
	Description *string
	Active      *bool
}

const endpointColumns = `id, org_id, url, events, active, description, failing_since, consecutive_failures, created_by, created_at, updated_at, version`

func scanEndpoint(row pgx.Row) (WebhookEndpoint, error) {
	var e WebhookEndpoint
	err := row.Scan(&e.ID, &e.OrgID, &e.URL, &e.Events, &e.Active, &e.Description, &e.FailingSince, &e.ConsecutiveFailures, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt, &e.Version)
	if err != nil {
		return e, err
	}
	if e.Events == nil {
		e.Events = []string{}
	}
	e.Failing = e.FailingSince != nil
	e.CreatedAt, e.UpdatedAt = e.CreatedAt.UTC(), e.UpdatedAt.UTC()
	if e.FailingSince != nil {
		u := e.FailingSince.UTC()
		e.FailingSince = &u
	}
	return e, nil
}

const deliveryColumns = `id, endpoint_id, event_id, event_type, status, attempts, response_code, last_error, next_retry_at, created_at, delivered_at`

func scanDelivery(row pgx.Row) (WebhookDelivery, error) {
	var d WebhookDelivery
	err := row.Scan(&d.ID, &d.EndpointID, &d.EventID, &d.EventType, &d.Status, &d.Attempts, &d.ResponseCode, &d.LastError, &d.NextRetryAt, &d.CreatedAt, &d.DeliveredAt)
	if err != nil {
		return d, err
	}
	d.CreatedAt = d.CreatedAt.UTC()
	for _, t := range []**time.Time{&d.NextRetryAt, &d.DeliveredAt} {
		if *t != nil {
			u := (**t).UTC()
			*t = &u
		}
	}
	return d, nil
}

func secretAAD(orgID, endpointID uuid.UUID) []byte {
	return []byte(orgID.String() + "/" + endpointID.String())
}

func (s *Service) requireWebhooks(ctx context.Context, perm authz.Permission, orgID uuid.UUID) error {
	return authz.Require(ctx, perm, authz.Scope{OrgID: orgID})
}

func (s *Service) validateURL(ctx context.Context, raw string) (string, error) {
	u, code := ValidateTarget(ctx, s.cfg.Resolver, strings.TrimSpace(raw), s.cfg.AllowedCIDRs)
	if code != "" {
		return "", problem.Validation(problem.Field{Field: "url", Code: code})
	}
	return u.String(), nil
}

func cleanDescription(d *string) (*string, error) {
	if d == nil {
		return nil, nil //nolint:nilnil // no description
	}
	t := strings.TrimSpace(*d)
	if len([]rune(t)) > 500 {
		return nil, problem.Validation(problem.Field{Field: "description", Code: "too_long"})
	}
	if t == "" {
		return nil, nil //nolint:nilnil // blank means none
	}
	return &t, nil
}

func auditEndpoint(e WebhookEndpoint, host string) map[string]any {
	return map[string]any{"url_host": host, "events": e.Events, "active": e.Active}
}

func hostOf(rawURL string) string {
	_, rest, _ := strings.Cut(rawURL, "://")
	host, _, _ := strings.Cut(rest, "/")
	host, _, _ = strings.Cut(host, "?")
	return host
}

// CreateEndpoint creates an endpoint and returns its signing secret, shown once.
func (s *Service) CreateEndpoint(ctx context.Context, orgID uuid.UUID, in EndpointInput) (WebhookEndpointCreated, error) {
	if err := s.requireWebhooks(ctx, PermWebhooksManage, orgID); err != nil {
		return WebhookEndpointCreated{}, err
	}
	if s.cipher == nil {
		return WebhookEndpointCreated{}, problem.Internal(errors.New("webhooks: no encryption key configured"))
	}
	target, err := s.validateURL(ctx, in.URL)
	if err != nil {
		return WebhookEndpointCreated{}, err
	}
	if !validEventTypes(in.Events) {
		return WebhookEndpointCreated{}, problem.Validation(problem.Field{Field: "events", Code: "invalid_value", Params: map[string]any{"known": EventTypes()}})
	}
	desc, err := cleanDescription(in.Description)
	if err != nil {
		return WebhookEndpointCreated{}, err
	}
	active := in.Active == nil || *in.Active
	secretBody, _, err := crypto.NewToken()
	if err != nil {
		return WebhookEndpointCreated{}, problem.Internal(err)
	}
	secret := secretPrefix + secretBody
	var by *uuid.UUID
	if p := authz.PrincipalFromContext(ctx); p != nil && p.UserID != uuid.Nil {
		u := p.UserID
		by = &u
	}
	endpointID := id.New()
	sealed, err := s.cipher.Seal([]byte(secret), secretAAD(orgID, endpointID))
	if err != nil {
		return WebhookEndpointCreated{}, problem.Internal(err)
	}
	var out WebhookEndpointCreated
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhook_endpoint WHERE org_id = $1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n >= maxEndpointsPerOrg {
			return problem.Conflict(fmt.Sprintf("an organization can have at most %d webhook endpoints", maxEndpointsPerOrg))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO webhook_endpoint (id, org_id, url, secret_sealed, events, active, description, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, endpointID, orgID, target, sealed, in.Events, active, desc, by); err != nil {
			return err
		}
		e, err := scanEndpoint(tx.QueryRow(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoint WHERE org_id = $1 AND id = $2`, orgID, endpointID))
		if err != nil {
			return err
		}
		diff, err := audit.Diff("webhook_endpoint", nil, auditEndpoint(e, hostOf(target)))
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, Action: "webhook.create", TargetType: "webhook_endpoint", TargetID: endpointID, Diff: diff}); err != nil {
			return err
		}
		out = WebhookEndpointCreated{Endpoint: e, Secret: secret}
		return nil
	})
	if err != nil {
		return WebhookEndpointCreated{}, tenancy.DBProblem(err, "webhook endpoint")
	}
	return out, nil
}

// ListEndpoints lists the organization's endpoints in creation order.
func (s *Service) ListEndpoints(ctx context.Context, orgID uuid.UUID, limit int, cursor string) ([]WebhookEndpoint, string, error) {
	if err := s.requireWebhooks(ctx, PermWebhooksRead, orgID); err != nil {
		return nil, "", err
	}
	lim, after, err := tenancy.PageParams{Limit: limit, Cursor: cursor}.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []WebhookEndpoint
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoint WHERE org_id = $1 AND id > $2 ORDER BY id LIMIT $3`, orgID, after, lim+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEndpoint(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "webhook endpoints")
	}
	items, next := tenancy.Page(out, lim, func(e WebhookEndpoint) uuid.UUID { return e.ID })
	return items, next, nil
}

// GetEndpoint returns one endpoint.
func (s *Service) GetEndpoint(ctx context.Context, orgID, endpointID uuid.UUID) (WebhookEndpoint, error) {
	if err := s.requireWebhooks(ctx, PermWebhooksRead, orgID); err != nil {
		return WebhookEndpoint{}, err
	}
	var e WebhookEndpoint
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		e, err = scanEndpoint(tx.QueryRow(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoint WHERE org_id = $1 AND id = $2`, orgID, endpointID))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookEndpoint{}, problem.NotFound("no such webhook endpoint")
	}
	return e, tenancy.DBProblem(err, "webhook endpoint")
}

// UpdateEndpoint changes an endpoint (If-Match: expectedVersion). Only the
// fields in d change: url, events, description (null clears it) and active.
// Changing the url or re-activating the endpoint clears its failing mark.
func (s *Service) UpdateEndpoint(ctx context.Context, orgID, endpointID uuid.UUID, expectedVersion int, d tenancy.Doc) (WebhookEndpoint, error) {
	if err := s.requireWebhooks(ctx, PermWebhooksManage, orgID); err != nil {
		return WebhookEndpoint{}, err
	}
	if bad := d.Unknown("url", "events", "description", "active"); len(bad) > 0 {
		return WebhookEndpoint{}, problem.Validation(bad...)
	}
	var (
		newURL  *string
		events  []string
		desc    *string
		active  *bool
		hasDesc = d.Has("description")
	)
	if d.Has("url") {
		var raw string
		if d.IsNull("url") {
			return WebhookEndpoint{}, problem.Validation(problem.Field{Field: "url", Code: "required"})
		}
		if err := d.Get("url", &raw); err != nil {
			return WebhookEndpoint{}, err
		}
		t, err := s.validateURL(ctx, raw)
		if err != nil {
			return WebhookEndpoint{}, err
		}
		newURL = &t
	}
	if d.Has("events") {
		if err := d.Get("events", &events); err != nil {
			return WebhookEndpoint{}, err
		}
		if !validEventTypes(events) {
			return WebhookEndpoint{}, problem.Validation(problem.Field{Field: "events", Code: "invalid_value", Params: map[string]any{"known": EventTypes()}})
		}
	}
	if hasDesc && !d.IsNull("description") {
		var raw string
		if err := d.Get("description", &raw); err != nil {
			return WebhookEndpoint{}, err
		}
		c, err := cleanDescription(&raw)
		if err != nil {
			return WebhookEndpoint{}, err
		}
		desc = c
	}
	if d.Has("active") {
		var a bool
		if d.IsNull("active") {
			return WebhookEndpoint{}, problem.Validation(problem.Field{Field: "active", Code: "required"})
		}
		if err := d.Get("active", &a); err != nil {
			return WebhookEndpoint{}, err
		}
		active = &a
	}
	var out WebhookEndpoint
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		before, err := scanEndpoint(tx.QueryRow(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoint WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, endpointID))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such webhook endpoint")
		}
		if err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return tenancy.ErrVersion
		}
		next := before
		if newURL != nil {
			next.URL = *newURL
		}
		if events != nil {
			next.Events = events
		}
		if hasDesc {
			next.Description = desc
		}
		if active != nil {
			next.Active = *active
		}
		clearFailing := next.URL != before.URL || (!before.Active && next.Active)
		if _, err := tx.Exec(ctx, `UPDATE webhook_endpoint SET url = $3, events = $4, description = $5, active = $6, updated_at = now(), version = version + 1,
				failing_since = CASE WHEN $7 THEN NULL ELSE failing_since END, consecutive_failures = CASE WHEN $7 THEN 0 ELSE consecutive_failures END
			WHERE org_id = $1 AND id = $2`, orgID, endpointID, next.URL, next.Events, next.Description, next.Active, clearFailing); err != nil {
			return err
		}
		out, err = scanEndpoint(tx.QueryRow(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoint WHERE org_id = $1 AND id = $2`, orgID, endpointID))
		if err != nil {
			return err
		}
		diff, err := audit.Diff("webhook_endpoint", auditEndpoint(before, hostOf(before.URL)), auditEndpoint(out, hostOf(out.URL)))
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, Action: "webhook.update", TargetType: "webhook_endpoint", TargetID: endpointID, Diff: diff})
	})
	if err != nil {
		return WebhookEndpoint{}, tenancy.DBProblem(err, "webhook endpoint")
	}
	return out, nil
}

// DeleteEndpoint deletes an endpoint and its delivery log. Deliveries still
// queued for it are dropped when their job finds the endpoint gone.
func (s *Service) DeleteEndpoint(ctx context.Context, orgID, endpointID uuid.UUID) error {
	if err := s.requireWebhooks(ctx, PermWebhooksManage, orgID); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		e, err := scanEndpoint(tx.QueryRow(ctx, `DELETE FROM webhook_endpoint WHERE org_id = $1 AND id = $2 RETURNING `+endpointColumns, orgID, endpointID))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such webhook endpoint")
		}
		if err != nil {
			return err
		}
		diff, err := audit.Diff("webhook_endpoint", auditEndpoint(e, hostOf(e.URL)), nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, Action: "webhook.delete", TargetType: "webhook_endpoint", TargetID: endpointID, Diff: diff})
	})
	return tenancy.DBProblem(err, "webhook endpoint")
}

// TestEndpoint queues a ping event to the endpoint (also when it is inactive)
// and returns the pending delivery; its outcome shows in the delivery log.
func (s *Service) TestEndpoint(ctx context.Context, orgID, endpointID uuid.UUID) (WebhookDelivery, error) {
	if err := s.requireWebhooks(ctx, PermWebhooksManage, orgID); err != nil {
		return WebhookDelivery{}, err
	}
	if s.insert.Load() == nil {
		return WebhookDelivery{}, problem.Internal(errors.New("webhooks: the job queue is not available"))
	}
	var out WebhookDelivery
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM webhook_endpoint WHERE org_id = $1 AND id = $2`, orgID, endpointID).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return problem.NotFound("no such webhook endpoint")
			}
			return err
		}
		ev := Event{ID: id.New(), Type: EventPing, Version: eventVersion, OrgID: orgID, OccurredAt: s.now(),
			IDs: map[string]uuid.UUID{"endpoint_id": endpointID}}
		deliveries, err := s.enqueue(ctx, tx, ev, []uuid.UUID{endpointID})
		if err != nil {
			return err
		}
		if len(deliveries) != 1 {
			return errors.New("webhooks: ping was not queued")
		}
		out, err = scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryColumns+` FROM webhook_delivery WHERE org_id = $1 AND id = $2`, orgID, deliveries[0]))
		if err != nil {
			return err
		}
		diff, err := audit.Diff("webhook_endpoint", nil, map[string]any{"event": EventPing})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, Action: "webhook.test", TargetType: "webhook_endpoint", TargetID: endpointID, Diff: diff})
	})
	if err != nil {
		return WebhookDelivery{}, tenancy.DBProblem(err, "webhook endpoint")
	}
	return out, nil
}

// ListDeliveries lists the endpoint's deliveries, newest first. status filters
// on pending, succeeded or failed ("" lists all).
func (s *Service) ListDeliveries(ctx context.Context, orgID, endpointID uuid.UUID, status string, limit int, cursor string) ([]WebhookDelivery, string, error) {
	if err := s.requireWebhooks(ctx, PermWebhooksRead, orgID); err != nil {
		return nil, "", err
	}
	if status != "" && status != "pending" && status != "succeeded" && status != "failed" {
		return nil, "", problem.Validation(problem.Field{Field: "status", Code: "invalid_value"})
	}
	lim, after, err := tenancy.PageParams{Limit: limit, Cursor: cursor}.Resolve()
	if err != nil {
		return nil, "", err
	}
	var before *uuid.UUID
	if after != uuid.Nil {
		before = &after
	}
	var out []WebhookDelivery
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM webhook_endpoint WHERE org_id = $1 AND id = $2`, orgID, endpointID).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return problem.NotFound("no such webhook endpoint")
			}
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+deliveryColumns+` FROM webhook_delivery WHERE org_id = $1 AND endpoint_id = $2
			AND ($3::uuid IS NULL OR id < $3) AND ($4 = '' OR status = $4) ORDER BY id DESC LIMIT $5`, orgID, endpointID, before, status, lim+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDelivery(rows)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "webhook deliveries")
	}
	items, next := tenancy.Page(out, lim, func(d WebhookDelivery) uuid.UUID { return d.ID })
	return items, next, nil
}

// EmitFromAudit is the audit.Sink: it turns the audited domain actions that
// have a webhook event into that event, in the transaction of the action.
func (s *Service) EmitFromAudit(ctx context.Context, tx pgx.Tx, e audit.Event) error {
	ev, ok, err := fromAudit(ctx, tx, e)
	if err != nil || !ok {
		return err
	}
	return s.Emit(ctx, tx, ev)
}

// Emit fans a core event out to the organization's active endpoints that
// subscribe to its type: one delivery row and one job per (event, endpoint).
// tx is the transaction of the business change, so an event exists if and only
// if the change commits. Emitting the same event twice queues nothing new.
func (s *Service) Emit(ctx context.Context, tx pgx.Tx, ev Event) error {
	rows, err := tx.Query(ctx, `SELECT id FROM webhook_endpoint WHERE org_id = $1 AND active AND $2 = ANY(events) ORDER BY id`, ev.OrgID, ev.Type)
	if err != nil {
		return err
	}
	var endpoints []uuid.UUID
	for rows.Next() {
		var eid uuid.UUID
		if err := rows.Scan(&eid); err != nil {
			rows.Close()
			return err
		}
		endpoints = append(endpoints, eid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(endpoints) == 0 {
		return nil
	}
	_, err = s.enqueue(ctx, tx, ev, endpoints)
	return err
}

// enqueue writes the delivery rows and inserts the delivery jobs, and returns
// the ids of the deliveries it created (an existing (event, endpoint) pair is
// skipped: that is the dedupe).
func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, ev Event, endpoints []uuid.UUID) ([]uuid.UUID, error) {
	ins := s.insert.Load()
	if ins == nil {
		s.cfg.Log.WarnContext(ctx, "webhook event dropped: the job client is not started yet", "type", ev.Type, "event", ev.ID)
		return nil, nil
	}
	body, err := ev.body()
	if err != nil {
		return nil, err
	}
	var created []uuid.UUID
	for _, eid := range endpoints {
		did := id.New()
		tag, err := tx.Exec(ctx, `INSERT INTO webhook_delivery (id, org_id, endpoint_id, event_id, event_type, body)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (org_id, endpoint_id, event_id) DO NOTHING`, did, ev.OrgID, eid, ev.ID, ev.Type, body)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		args := DeliverArgs{OrgID: ev.OrgID, EndpointID: eid, EventID: ev.ID, DeliveryID: did}
		opts := &river.InsertOpts{MaxAttempts: s.maxTry, UniqueOpts: river.UniqueOpts{ByArgs: true}}
		if err := (*ins)(ctx, tx, args, opts); err != nil {
			return nil, fmt.Errorf("enqueue webhook delivery: %w", err)
		}
		created = append(created, did)
	}
	return created, nil
}
