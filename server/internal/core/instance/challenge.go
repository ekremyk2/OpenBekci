package instance

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ChallengeSource returns the pending push relay enrollment challenge.
type ChallengeSource interface {
	Challenge(ctx context.Context) (challenge string, pending bool, err error)
}

// WithChallenges serves the enrollment challenge in the discovery document
// (outside the cache: it is ephemeral).
func (s *Service) WithChallenges(c ChallengeSource) *Service {
	s.challenges = c
	return s
}

// PGChallenges reads and writes the relay_enrollment row.
type PGChallenges struct{ DB TxRunner }

// Challenge implements ChallengeSource: the challenge while it has not expired.
func (p PGChallenges) Challenge(ctx context.Context) (string, bool, error) {
	var c string
	err := p.DB.InAppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT challenge FROM relay_enrollment WHERE expires_at > now()`).Scan(&c)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read relay challenge: %w", err)
	}
	return c, true, nil
}

// Set stores a challenge valid for ttlSeconds, replacing any earlier one.
func (p PGChallenges) Set(ctx context.Context, challenge string, ttlSeconds int) error {
	return p.DB.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO relay_enrollment (id, challenge, expires_at) VALUES (true, $1, now() + make_interval(secs => $2))
			ON CONFLICT (id) DO UPDATE SET challenge = EXCLUDED.challenge, expires_at = EXCLUDED.expires_at`, challenge, ttlSeconds)
		return err
	})
}

// Clear removes the challenge.
func (p PGChallenges) Clear(ctx context.Context) error {
	return p.DB.InAppTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM relay_enrollment`)
		return err
	})
}
