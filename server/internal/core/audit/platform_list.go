package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// PermPlatformRead guards the instance-level trail. It is platform.manage, the
// operator permission (declared by package platformadmin; spelled here so that
// audit does not import it).
const PermPlatformRead authz.Permission = "platform.manage"

// PlatformRunner is the cross-organization database access the platform trail
// needs, satisfied by *db.DB.
type PlatformRunner interface {
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// PlatformFilter narrows ListPlatform; zero fields do not filter.
type PlatformFilter struct {
	Action   string
	Actor    *uuid.UUID
	From, To *time.Time
}

// ListPlatform returns one page of platform_audit_event, newest first, with
// the same cursor as List. It needs platform.manage (instance scope), which
// only platform administrators hold. The events carry ids and hashes, never
// personal values, and the IP address is stored truncated (/24, /48), so the
// table is out of reach of privacy erasure by design.
func (s *Service) ListPlatform(ctx context.Context, f PlatformFilter, cursorStr string, limit int) ([]Stored, string, error) {
	if err := authz.Require(ctx, PermPlatformRead, authz.Scope{}); err != nil {
		return nil, "", err
	}
	runner, ok := s.db.(PlatformRunner)
	if !ok {
		return nil, "", problem.Internal(fmt.Errorf("audit: the database runner has no platform access"))
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		return nil, "", problem.Validation(problem.Field{Field: "limit", Code: "too_large", Params: map[string]any{"max": MaxLimit}})
	}
	var atBound *time.Time
	var idBound *uuid.UUID
	if cursorStr != "" {
		c, err := decodeCursor(cursorStr)
		if err != nil {
			return nil, "", err
		}
		t := time.UnixMicro(c.AtMicro).UTC()
		atBound, idBound = &t, &c.ID
	}
	var out []Stored
	err := runner.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, at, actor_user_id, actor_kind, action, target_type, target_id, diff, host(ip), request_id
			FROM platform_audit_event
			WHERE ($1::text IS NULL OR action = $1)
			  AND ($2::uuid IS NULL OR actor_user_id = $2)
			  AND ($3::timestamptz IS NULL OR at >= $3)
			  AND ($4::timestamptz IS NULL OR at < $4)
			  AND ($5::timestamptz IS NULL OR (at, id) < ($5, $6::uuid))
			ORDER BY at DESC, id DESC
			LIMIT $7`,
			nullStr(f.Action), f.Actor, f.From, f.To, atBound, idBound, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Stored
			if err := rows.Scan(&e.ID, &e.At, &e.ActorUserID, &e.ActorKind, &e.Action, &e.TargetType, &e.TargetID, &e.Diff, &e.IP, &e.RequestID); err != nil {
				return err
			}
			e.At = e.At.UTC()
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", problem.Internal(fmt.Errorf("list platform audit events: %w", err))
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = EncodeCursor(last.At, last.ID)
	}
	return out, next, nil
}
