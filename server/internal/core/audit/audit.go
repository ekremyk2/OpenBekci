// Package audit records who did what (backend plan sections 4.13 and 16).
//
// audit_event is append-only: the application roles hold INSERT and SELECT
// only. Services call Record inside the business transaction, so an event
// exists if and only if the change committed. Diffs are PII-safe: fields
// registered in package pii appear as hashes (see Diff).
//
// Events that belong to no organization (password changes, session
// revocation, platform-administrator promotion) go to platform_audit_event,
// which only the platform role can read; platform administrators read it
// through GET /v1/platform/audit (Service.ListPlatform). That table has no
// org_id and is not touched by privacy erasure: its diffs hold ids and keyed
// hashes, never personal values, and the IP address is stored truncated to
// its /24 (IPv4) or /48 (IPv6) network.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// ActorKind says who caused an event.
type ActorKind string

// Actor kinds.
const (
	ActorUser   ActorKind = "user"
	ActorSystem ActorKind = "system"
	ActorJob    ActorKind = "job"
)

// Event is one audit record.
type Event struct {
	// OrgID is the organization; uuid.Nil records an instance-level event in
	// platform_audit_event.
	OrgID  uuid.UUID
	SiteID *uuid.UUID
	// ActorKind and ActorUserID default from the principal in ctx: a user
	// event with the principal's user id, else a system event.
	ActorKind   ActorKind
	ActorUserID *uuid.UUID
	// Action is "<area>.<verb>", for example org.update.
	Action     string
	TargetType string
	TargetID   uuid.UUID
	// Diff is the PII-safe change (see Diff); nil stores {}.
	Diff Changes
	// IP and RequestID default from the request metadata in ctx.
	IP        string
	RequestID string
}

// RequestMeta is what the HTTP layer knows about the request, carried in the
// context so that services record it without importing the transport.
type RequestMeta struct {
	IP        string
	RequestID string
}

type metaKey struct{}

// WithRequestMeta returns ctx carrying the request metadata.
func WithRequestMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// RequestMetaFrom returns the request metadata carried in ctx (zero when none).
func RequestMetaFrom(ctx context.Context) RequestMeta {
	m, _ := ctx.Value(metaKey{}).(RequestMeta)
	return m
}

// Record appends e inside tx. Call it from the service, in the same
// transaction as the change it describes.
func Record(ctx context.Context, tx pgx.Tx, e Event) error {
	if e.Action == "" || e.TargetType == "" {
		return fmt.Errorf("audit: event needs an action and a target type")
	}
	if e.ActorKind == "" {
		if p := authz.PrincipalFromContext(ctx); p != nil {
			uid := p.UserID
			e.ActorKind, e.ActorUserID = ActorUser, &uid
		} else {
			e.ActorKind = ActorSystem
		}
	}
	m, _ := ctx.Value(metaKey{}).(RequestMeta)
	if e.IP == "" {
		e.IP = m.IP
	}
	if e.RequestID == "" {
		e.RequestID = m.RequestID
	}
	diff := e.Diff
	if diff == nil {
		diff = Changes{}
	}
	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return fmt.Errorf("audit: encode diff: %w", err)
	}
	ip := nullIfEmpty(e.IP)
	if ip != nil {
		addr, err := netip.ParseAddr(*ip)
		switch {
		case err != nil:
			ip = nil // not an address (behind a proxy without TRUSTED_PROXIES): store nothing
		case e.OrgID == uuid.Nil:
			// The instance-level trail is not reached by per-organization
			// erasure, so it keeps a network, not a household: /24 or /48.
			ip = truncatedIP(addr)
		}
	}
	if e.OrgID == uuid.Nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO platform_audit_event (id, actor_user_id, actor_kind, action, target_type, target_id, diff, ip, request_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			id.New(), e.ActorUserID, e.ActorKind, e.Action, e.TargetType, e.TargetID, diffJSON, ip, nullIfEmpty(e.RequestID))
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_event (id, org_id, site_id, actor_user_id, actor_kind, action, target_type, target_id, diff, ip, request_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			id.New(), e.OrgID, e.SiteID, e.ActorUserID, e.ActorKind, e.Action, e.TargetType, e.TargetID, diffJSON, ip, nullIfEmpty(e.RequestID))
	}
	if err != nil {
		return fmt.Errorf("audit: record %s: %w", e.Action, err)
	}
	return nil
}

// truncatedIP is the /24 (IPv4) or /48 (IPv6) network of addr, as CIDR text.
func truncatedIP(addr netip.Addr) *string {
	addr = addr.Unmap()
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	s := netip.PrefixFrom(addr, bits).Masked().String()
	return &s
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
