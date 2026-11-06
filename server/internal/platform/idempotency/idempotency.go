// Package idempotency stores Idempotency-Key requests and their responses
// (backend plan, section 8): insert first with an in-progress row, replay the
// stored response for the same key and request, 7-day retention.
package idempotency

import (
	"context"
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

// Scope is the (org, principal) a key belongs to. Nil means "none": anonymous
// requests and platform-level endpoints.
type Scope struct {
	OrgID       *uuid.UUID
	PrincipalID *uuid.UUID
}

// Response is a stored response.
type Response struct {
	Status      int
	ContentType string
	Body        []byte
}

// State is the outcome of Begin.
type State int

// Begin outcomes.
const (
	// Acquired: the caller owns the key and must run the request, then call
	// Complete (or Abort).
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
	Response Response
}

// Store is the idempotency store.
type Store interface {
	// Begin inserts an in-progress row, or reports why it could not.
	Begin(ctx context.Context, s Scope, key, requestHash string) (Result, error)
	// Complete records the response and releases the key for replay.
	Complete(ctx context.Context, s Scope, key string, resp Response) error
	// Abort removes an in-progress row so the client can retry (used when the
	// request failed with a server error).
	Abort(ctx context.Context, s Scope, key string) error
}

// TxRunner runs a transaction on a non-tenant connection (*db.DB.InAppTx).
type TxRunner interface {
	InAppTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// PG is the PostgreSQL store.
type PG struct{ db TxRunner }

// NewPG returns a store on db.
func NewPG(db TxRunner) *PG { return &PG{db: db} }

const scopeMatch = `org_id IS NOT DISTINCT FROM $1::uuid AND principal_id IS NOT DISTINCT FROM $2::uuid AND key = $3`

// Begin implements Store.
func (s *PG) Begin(ctx context.Context, sc Scope, key, hash string) (Result, error) {
	for range 3 {
		var res Result
		retry := false
		err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `
				INSERT INTO idempotency_key (org_id, principal_id, key, request_hash, status, expires_at)
				VALUES ($1, $2, $3, $4, 'in_progress', now() + make_interval(secs => $5))
				ON CONFLICT DO NOTHING`,
				sc.OrgID, sc.PrincipalID, key, hash, Retention.Seconds())
			if err != nil {
				return fmt.Errorf("insert idempotency key: %w", err)
			}
			if tag.RowsAffected() == 1 {
				res.State = Acquired
				return nil
			}

			var (
				storedHash, status string
				respStatus         *int
				respType           *string
				respBody           []byte
				expired, stale     bool
			)
			err = tx.QueryRow(ctx, `
				SELECT request_hash, status, response_status, response_content_type, response_body,
				       expires_at < now(), created_at < now() - make_interval(secs => $4)
				FROM idempotency_key WHERE `+scopeMatch,
				sc.OrgID, sc.PrincipalID, key, StaleAfter.Seconds()).
				Scan(&storedHash, &status, &respStatus, &respType, &respBody, &expired, &stale)
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
				if respStatus != nil {
					res.Response.Status = *respStatus
				}
				if respType != nil {
					res.Response.ContentType = *respType
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
func (s *PG) Complete(ctx context.Context, sc Scope, key string, resp Response) error {
	return s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE idempotency_key
			SET status = 'done', response_status = $4, response_content_type = $5, response_body = $6,
			    expires_at = now() + make_interval(secs => $7)
			WHERE `+scopeMatch+` AND status = 'in_progress'`,
			sc.OrgID, sc.PrincipalID, key, resp.Status, resp.ContentType, resp.Body, Retention.Seconds())
		if err != nil {
			return fmt.Errorf("complete idempotency key: %w", err)
		}
		return nil
	})
}

// Abort implements Store.
func (s *PG) Abort(ctx context.Context, sc Scope, key string) error {
	return s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM idempotency_key WHERE `+scopeMatch+` AND status = 'in_progress'`,
			sc.OrgID, sc.PrincipalID, key)
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
