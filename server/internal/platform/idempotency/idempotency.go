// Package idempotency stores Idempotency-Key requests and their responses
// (backend plan, section 8): insert first with an in-progress row, replay the
// stored response for the same key and request, 7-day retention.
//
// Keys belong to an authenticated principal (and the organization in the
// request path, when there is one); anonymous callers have no stored keys, so
// one caller can never replay or probe another's. Every row carries the token
// of the request that owns it, so a request that outlives its takeover cannot
// overwrite the new owner's response.
package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// Retention is how long a completed key is remembered.
	Retention = 7 * 24 * time.Hour
	// StaleAfter is the age after which an in-progress row is assumed to belong
	// to a crashed request and may be taken over.
	StaleAfter = 5 * time.Minute
)

// ErrNotOwner is returned by Complete when the key is no longer owned by the
// caller: the row was taken over (it went stale) or released.
var ErrNotOwner = errors.New("idempotency: key is not owned by this request")

// Scope is what a key belongs to: the authenticated principal and, for
// organization-scoped operations, the organization. OrgID is nil for
// instance-wide operations; PrincipalID is always set.
type Scope struct {
	OrgID       *uuid.UUID
	PrincipalID uuid.UUID
}

// Response is a stored response. Headers holds the allow-listed headers other
// than Content-Type (for example Location).
type Response struct {
	Status      int
	ContentType string
	Headers     map[string][]string
	Body        []byte
	// Digest is the SHA-256 (hex) of the content of a streamed request body, or
	// empty when the request body is part of the request hash. A replay of a
	// streamed request must present the same digest.
	Digest string
}

// State is the outcome of Begin.
type State int

// Begin outcomes.
const (
	// Acquired: the caller owns the key and must run the request, then call
	// Complete (or Abort) with Result.Owner.
	Acquired State = iota
	// InProgress: another request with this key is still running.
	InProgress
	// Replay: the same request already completed; Result.Response is its response.
	Replay
	// Mismatch: the key was used with a different request.
	Mismatch
)

// Result is what Begin found.
type Result struct {
	State    State
	Owner    uuid.UUID // set when State is Acquired
	Response Response  // set when State is Replay
}

// Store is the idempotency store.
type Store interface {
	// Begin inserts an in-progress row, or reports why it could not.
	Begin(ctx context.Context, s Scope, key, requestHash string) (Result, error)
	// Complete records the response and releases the key for replay. It
	// returns ErrNotOwner when owner no longer holds the key.
	Complete(ctx context.Context, s Scope, key string, owner uuid.UUID, resp Response) error
	// Abort removes owner's in-progress row so the client can retry (used when
	// the request failed with a server error).
	Abort(ctx context.Context, s Scope, key string, owner uuid.UUID) error
}

// TxRunner runs a transaction on a non-tenant connection (*db.DB.InAppTx).
type TxRunner interface {
	InAppTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// PG is the PostgreSQL store.
type PG struct{ db TxRunner }

// NewPG returns a store on db.
func NewPG(db TxRunner) *PG { return &PG{db: db} }

const scopeMatch = `org_id IS NOT DISTINCT FROM $1::uuid AND principal_id = $2::uuid AND key = $3`

// Begin implements Store.
func (s *PG) Begin(ctx context.Context, sc Scope, key, hash string) (Result, error) {
	for range 3 {
		var res Result
		retry := false
		owner := uuid.New()
		err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `
				INSERT INTO idempotency_key (org_id, principal_id, key, request_hash, status, owner, expires_at)
				VALUES ($1, $2, $3, $4, 'in_progress', $5, now() + make_interval(secs => $6))
				ON CONFLICT DO NOTHING`,
				sc.OrgID, sc.PrincipalID, key, hash, owner, Retention.Seconds())
			if err != nil {
				return fmt.Errorf("insert idempotency key: %w", err)
			}
			if tag.RowsAffected() == 1 {
				res.State, res.Owner = Acquired, owner
				return nil
			}

			var (
				storedHash, status string
				respStatus         *int
				respType           *string
				respHeaders        []byte
				respDigest         *string
				respBody           []byte
				expired, stale     bool
			)
			err = tx.QueryRow(ctx, `
				SELECT request_hash, status, response_status, response_content_type, response_headers, response_body, content_digest,
				       expires_at < now(), created_at < now() - make_interval(secs => $4)
				FROM idempotency_key WHERE `+scopeMatch,
				sc.OrgID, sc.PrincipalID, key, StaleAfter.Seconds()).
				Scan(&storedHash, &status, &respStatus, &respType, &respHeaders, &respBody, &respDigest, &expired, &stale)
			if errors.Is(err, pgx.ErrNoRows) {
				retry = true // deleted between insert and select
				return nil
			}
			if err != nil {
				return fmt.Errorf("read idempotency key: %w", err)
			}
			if expired || (status == "in_progress" && stale) {
				if _, err := tx.Exec(ctx, `DELETE FROM idempotency_key WHERE `+scopeMatch, sc.OrgID, sc.PrincipalID, key); err != nil {
					return fmt.Errorf("delete stale idempotency key: %w", err)
				}
				retry = true
				return nil
			}
			switch {
			case storedHash != hash:
				res.State = Mismatch
			case status == "in_progress":
				res.State = InProgress
			default:
				res.State = Replay
				res.Response.Body = respBody
				if respDigest != nil {
					res.Response.Digest = *respDigest
				}
				if respStatus != nil {
					res.Response.Status = *respStatus
				}
				if respType != nil {
					res.Response.ContentType = *respType
				}
				if len(respHeaders) > 0 {
					if err := json.Unmarshal(respHeaders, &res.Response.Headers); err != nil {
						return fmt.Errorf("decode stored response headers: %w", err)
					}
				}
			}
			return nil
		})
		if err != nil {
			return Result{}, err
		}
		if !retry {
			return res, nil
		}
	}
	return Result{}, errors.New("idempotency: key contended, giving up")
}

// Complete implements Store.
func (s *PG) Complete(ctx context.Context, sc Scope, key string, owner uuid.UUID, resp Response) error {
	headers, err := json.Marshal(resp.Headers)
	if err != nil {
		return fmt.Errorf("encode response headers: %w", err)
	}
	if resp.Headers == nil {
		headers = []byte("{}")
	}
	return s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE idempotency_key
			SET status = 'done', response_status = $4, response_content_type = $5, response_headers = $6,
			    response_body = $7, expires_at = now() + make_interval(secs => $8), content_digest = NULLIF($10::text, '')
			WHERE `+scopeMatch+` AND owner = $9 AND status = 'in_progress'`,
			sc.OrgID, sc.PrincipalID, key, resp.Status, resp.ContentType, headers, resp.Body, Retention.Seconds(), owner, resp.Digest)
		if err != nil {
			return fmt.Errorf("complete idempotency key: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotOwner
		}
		return nil
	})
}

// Abort implements Store.
func (s *PG) Abort(ctx context.Context, sc Scope, key string, owner uuid.UUID) error {
	return s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM idempotency_key WHERE `+scopeMatch+` AND owner = $4 AND status = 'in_progress'`,
			sc.OrgID, sc.PrincipalID, key, owner)
		if err != nil {
			return fmt.Errorf("abort idempotency key: %w", err)
		}
		return nil
	})
}

// Purge deletes expired keys and returns how many it removed.
func (s *PG) Purge(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM idempotency_key WHERE expires_at < now()`)
		if err != nil {
			return fmt.Errorf("purge idempotency keys: %w", err)
		}
		n = tag.RowsAffected()
		return nil
	})
	return n, err
}
