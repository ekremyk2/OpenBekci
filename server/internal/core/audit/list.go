package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Permissions of the audit module.
const (
	PermRead   authz.Permission = "audit.read"
	PermReport authz.Permission = "audit.report"
)

// Permissions declares the module's permissions; the wiring registers them.
func Permissions() []authz.PermissionDef {
	return []authz.PermissionDef{
		{Name: PermRead, Description: "Read the organization's audit trail",
			Roles: []authz.Role{authz.RoleOrgAdmin, authz.RoleAuditor}, Scopes: []authz.ScopeType{authz.ScopeOrg}},
		{Name: PermReport, Description: "Write audit notes and reports (denetci)",
			Roles: []authz.Role{authz.RoleAuditor}, Scopes: []authz.ScopeType{authz.ScopeOrg}},
	}
}

// TxRunner is the database access the module needs, satisfied by *db.DB.
type TxRunner interface {
	InTenantTx(ctx context.Context, orgID uuid.UUID, fn func(pgx.Tx) error) error
}

// Service lists audit events.
type Service struct{ db TxRunner }

// NewService returns a Service.
func NewService(db TxRunner) *Service { return &Service{db: db} }

// Stored is an audit event as read back.
type Stored struct {
	ID          uuid.UUID
	At          time.Time
	SiteID      *uuid.UUID
	ActorUserID *uuid.UUID
	ActorKind   string
	Action      string
	TargetType  string
	TargetID    uuid.UUID
	Diff        json.RawMessage
	IP          *string
	RequestID   *string
}

// Filter narrows List. Zero fields do not filter; From is inclusive, To exclusive.
type Filter struct {
	Action     string
	TargetType string
	Actor      *uuid.UUID
	From, To   *time.Time
}

// DefaultLimit and MaxLimit follow the list conventions of the API.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

type cursor struct {
	AtMicro int64     `json:"a"`
	ID      uuid.UUID `json:"i"`
}

// EncodeCursor makes the opaque cursor for the position after (at, id).
func EncodeCursor(at time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(cursor{AtMicro: at.UnixMicro(), ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, error) {
	var c cursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(raw, &c)
	}
	if err != nil || c.ID == uuid.Nil {
		return cursor{}, problem.Validation(problem.Field{Field: "cursor", Code: "invalid_value"})
	}
	return c, nil
}

// List returns one page of the organization's events, newest first, ordered
// by (at, id) descending, and the cursor of the next page ("" on the last).
// It needs audit.read.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, f Filter, cursorStr string, limit int) ([]Stored, string, error) {
	if err := authz.Require(ctx, PermRead, authz.Scope{OrgID: orgID}); err != nil {
		return nil, "", err
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		return nil, "", problem.Validation(problem.Field{Field: "limit", Code: "too_large", Params: map[string]any{"max": MaxLimit}})
	}
	var cur *cursor
	if cursorStr != "" {
		c, err := decodeCursor(cursorStr)
		if err != nil {
			return nil, "", err
		}
		cur = &c
	}
	var atBound *time.Time
	var idBound *uuid.UUID
	if cur != nil {
		t := time.UnixMicro(cur.AtMicro).UTC()
		atBound, idBound = &t, &cur.ID
	}
	var out []Stored
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, at, site_id, actor_user_id, actor_kind, action, target_type, target_id, diff, host(ip), request_id
			FROM audit_event
			WHERE org_id = $1
			  AND ($2::text IS NULL OR action = $2)
			  AND ($3::text IS NULL OR target_type = $3)
			  AND ($4::uuid IS NULL OR actor_user_id = $4)
			  AND ($5::timestamptz IS NULL OR at >= $5)
			  AND ($6::timestamptz IS NULL OR at < $6)
			  AND ($7::timestamptz IS NULL OR (at, id) < ($7, $8::uuid))
			ORDER BY at DESC, id DESC
			LIMIT $9`,
			orgID, nullStr(f.Action), nullStr(f.TargetType), f.Actor, f.From, f.To, atBound, idBound, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Stored
			if err := rows.Scan(&e.ID, &e.At, &e.SiteID, &e.ActorUserID, &e.ActorKind, &e.Action, &e.TargetType, &e.TargetID, &e.Diff, &e.IP, &e.RequestID); err != nil {
				return err
			}
			e.At = e.At.UTC()
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", problem.Internal(fmt.Errorf("list audit events: %w", err))
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = EncodeCursor(last.At, last.ID)
	}
	return out, next, nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
