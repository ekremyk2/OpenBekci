package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Lockout of repeated credential failures.
//
// Failures are counted per account ("uid:<id>" once the account is found, so
// the email and the phone number share one counter; the normalised identifier
// for unknown accounts, which are locked out like known ones) and since the
// last success. Two keys apply, and the longer lock wins:
//
//   - (account, client address): LockoutThreshold failures (default 5) lock
//     that address out of that account, 30 s doubling per further failure up to
//     30 minutes;
//   - account alone: LockoutGlobalThreshold failures (default 25) from any
//     addresses lock everybody out of that account, with the same growth.
//
// The trade-off. A single address that keeps guessing is stopped after 5
// failures, and, because the account-wide key needs 25 failures, an attacker
// cannot lock the owner out from one address: it takes failures from at least
// five different addresses (each address is also rate limited). The price is
// that a guesser who rotates addresses gets up to 25 guesses per account
// before the account-wide lock, instead of 5. With argon2id behind every guess,
// the address rate limit and passwords of at least 10 characters that seems the
// better side of the trade for a residential community instance, where
// locking the building's administrator out is the likelier harm. A success
// from any address resets both counters.

// lockoutKey names the failure counter of an account.
func lockoutKey(u User, found bool, norm string) (string, *uuid.UUID) {
	if found {
		return "uid:" + u.ID.String(), &u.ID
	}
	return norm, nil
}

// beginAttempt is the lockout gate of a credential check. Under a per-key
// Postgres advisory lock it refuses the attempt while the account is locked
// and otherwise records it as a failure BEFORE the credential is verified, so
// that parallel guesses cannot all pass a check-then-act race: at most
// LockoutThreshold attempts per address (LockoutGlobalThreshold per account)
// are ever evaluated between two successes. A correct credential then calls
// recordSuccess, which resets the counters. It returns the id of the recorded
// failure, for refundAttempt.
func (s *Service) beginAttempt(ctx context.Context, key, method, ip string, userID *uuid.UUID) (uuid.UUID, error) {
	var wait time.Duration
	attemptID := id.New()
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return err
		}
		var err error
		if wait, err = s.lockedFor(ctx, tx, key, ip); err != nil || wait > 0 {
			return err
		}
		return s.insertAttempt(ctx, tx, attemptID, key, method, ip, false, userID)
	})
	if err != nil {
		return uuid.Nil, problem.Internal(fmt.Errorf("check lockout: %w", err))
	}
	if wait > 0 {
		return uuid.Nil, problem.RateLimited(wait)
	}
	return attemptID, nil
}

// refundAttempt removes a pre-recorded failure whose credential was never
// checked (the server was too busy to hash the password), so that overload
// does not count against the account.
func (s *Service) refundAttempt(ctx context.Context, attemptID uuid.UUID) {
	err := s.db.InAppTx(context.WithoutCancel(ctx), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM login_attempt WHERE id = $1`, attemptID)
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "refund login attempt", "error", err)
	}
}

func (s *Service) insertAttempt(ctx context.Context, tx pgx.Tx, attemptID uuid.UUID, key, method, ip string, success bool, userID *uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO login_attempt (id, identifier, method, ip, success, user_id, at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		attemptID, key, method, clip(ip, 64), success, userID, s.now())
	return err
}

// recordSuccess notes a verified credential, which resets the failure counts of key.
func (s *Service) recordSuccess(ctx context.Context, key, method, ip string, userID *uuid.UUID) {
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		return s.insertAttempt(ctx, tx, id.New(), key, method, ip, true, userID)
	})
	if err != nil {
		s.log.ErrorContext(ctx, "record login attempt", "error", err)
	}
}

// lockedFor returns how much longer ip is locked out of the account key: the
// longer of the (account, address) lock and the account-wide lock.
func (s *Service) lockedFor(ctx context.Context, tx pgx.Tx, key, ip string) (time.Duration, error) {
	now := s.now()
	ip = clip(ip, 64)
	var perIP, global int
	var lastIP, lastGlobal *time.Time
	err := tx.QueryRow(ctx, `
		WITH f AS (
			SELECT ip, at FROM login_attempt
			WHERE identifier = $1 AND NOT success AND at > $2
			  AND at > COALESCE((SELECT max(at) FROM login_attempt WHERE identifier = $1 AND success), '-infinity')
		)
		SELECT count(*) FILTER (WHERE ip = $3::text), max(at) FILTER (WHERE ip = $3::text),
		       count(*), max(at)
		FROM f`, key, now.Add(-lockoutWindow), ip).Scan(&perIP, &lastIP, &global, &lastGlobal)
	if err != nil {
		return 0, err
	}
	wait := s.lockRemaining(perIP, lastIP, s.cfg.LockoutThreshold, now)
	return max(wait, s.lockRemaining(global, lastGlobal, s.cfg.LockoutGlobalThreshold, now)), nil
}

// lockRemaining is how much longer a lock lasts after n failures, the last at
// last, when it starts at threshold failures: 30 s, doubling with every
// further failure, up to 30 minutes.
func (s *Service) lockRemaining(n int, last *time.Time, threshold int, now time.Time) time.Duration {
	if n < threshold || last == nil {
		return 0
	}
	d := lockoutBase << min(n-threshold, maxLockoutSteps)
	d = min(d, lockoutMax)
	if remaining := last.Add(d).Sub(now); remaining > 0 {
		return remaining
	}
	return 0
}
