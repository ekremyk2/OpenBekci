package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// RefreshGrace is how long after a rotation the previous refresh token is
// still accepted, once, and answered with the same new pair (see Refresh).
const RefreshGrace = 30 * time.Second

func (s *Service) insertPair(ctx context.Context, tx pgx.Tx, userID, family uuid.UUID, rotatedFrom *uuid.UUID, m Meta) (TokenPair, error) {
	now := s.now().Truncate(time.Microsecond) // the database keeps microseconds; a replayed pair must match exactly
	out := TokenPair{SessionID: family, AccessExpiresAt: now.Add(s.cfg.AccessTTL), RefreshExpiresAt: now.Add(s.cfg.RefreshTTL)}
	var err error
	if out.RefreshToken, _, err = s.insertSession(ctx, tx, sessionInsert{userID: userID, family: family, rotatedFrom: rotatedFrom, kind: KindRefreshToken, expires: out.RefreshExpiresAt, m: m}); err != nil {
		return TokenPair{}, err
	}
	out.AccessToken, _, err = s.insertSession(ctx, tx, sessionInsert{userID: userID, family: family, kind: KindAccessToken, expires: out.AccessExpiresAt, m: m})
	return out, err
}

// gracePair is what is sealed for a replay: the successor pair.
type gracePair struct {
	Refresh, Access       string
	RefreshExp, AccessExp time.Time
}

// Refresh exchanges a refresh token for a new pair and retires the old
// refresh token. The new session keeps the device name of the old one.
//
// Presenting a refresh token that was already rotated means it leaked (or the
// client raced itself): the whole family is revoked and the user must sign in
// again. One exception protects against a lost response: at rotation the new
// pair is stored on the old row, encrypted under a key derived (HKDF) from the
// old refresh token itself, for RefreshGrace. A retry with the old token inside
// that window decrypts it and gets the same pair, once; the blob is then
// erased. Only a holder of the old token can open it and the token is never
// stored in clear, so a database dump, even with the server secret, yields no
// live token. The trade-off: a thief who replays a stolen token inside those
// 30 seconds gets the same pair as the owner; any later reuse is still caught.
func (s *Service) Refresh(ctx context.Context, refreshToken string, m Meta) (TokenPair, uuid.UUID, error) {
	if err := s.ipGate(m.IP); err != nil {
		return TokenPair{}, uuid.Nil, err
	}
	now := s.now()
	var out TokenPair
	ok := false
	var owner uuid.UUID
	err := s.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var rowID, userID, family uuid.UUID
		var expires time.Time
		var revoked, graceUntil *time.Time
		var blob []byte
		var status string
		var device *string
		err := tx.QueryRow(ctx, `
			SELECT s.id, s.user_id, s.family_id, s.expires_at, s.revoked_at, s.grace_until, s.grace_blob, s.device_name, u.status
			FROM session s JOIN "user" u ON u.id = s.user_id
			WHERE s.token_hash = $1 AND s.kind = $2 FOR UPDATE OF s`, crypto.HashToken(refreshToken), KindRefreshToken).
			Scan(&rowID, &userID, &family, &expires, &revoked, &graceUntil, &blob, &device, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if revoked != nil {
			if blob != nil {
				// the one allowed replay: the blob is erased whatever the outcome
				if _, err := tx.Exec(ctx, `UPDATE session SET grace_blob = NULL, grace_until = NULL WHERE id = $1`, rowID); err != nil {
					return err
				}
				if graceUntil != nil && now.Before(*graceUntil) && expires.After(now) && status == "active" {
					pair, replayed, err := s.replayRotation(ctx, tx, refreshToken, blob, family, now)
					if err != nil {
						return err
					}
					if replayed {
						out, ok, owner = pair, true, userID
						return nil
					}
				}
			}
			// Reuse detection: burn the family. This commits (fn returns nil).
			s.log.WarnContext(ctx, "refresh token reuse detected; revoking session family", "user_id", userID, "family_id", family)
			_, err = tx.Exec(ctx, `UPDATE session SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, family, now)
			return err
		}
		if !expires.After(now) || status != "active" {
			return nil
		}
		if m.DeviceName == "" && device != nil {
			m.DeviceName = *device
		}
		if out, err = s.insertPair(ctx, tx, userID, family, &rowID, m); err != nil {
			return err
		}
		sealed, err := json.Marshal(gracePair{Refresh: out.RefreshToken, Access: out.AccessToken, RefreshExp: out.RefreshExpiresAt, AccessExp: out.AccessExpiresAt})
		if err != nil {
			return err
		}
		if sealed, err = crypto.SealWithToken(refreshToken, sealed); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE session SET revoked_at = $2, grace_blob = $3, grace_until = $4 WHERE id = $1`,
			rowID, now, sealed, now.Add(RefreshGrace)); err != nil {
			return err
		}
		ok, owner = true, userID
		return nil
	})
	if err != nil {
		return TokenPair{}, uuid.Nil, problem.Internal(fmt.Errorf("refresh: %w", err))
	}
	if !ok {
		return TokenPair{}, uuid.Nil, errInvalidGrant()
	}
	return out, owner, nil
}

// replayRotation opens the sealed successor pair with the presented token. It
// reports false when it cannot (wrong blob, or the new pair was rotated or
// revoked meanwhile); the caller then treats the request as reuse.
func (s *Service) replayRotation(ctx context.Context, tx pgx.Tx, oldToken string, blob []byte, family uuid.UUID, now time.Time) (TokenPair, bool, error) {
	plain, err := crypto.OpenWithToken(oldToken, blob)
	if err != nil {
		return TokenPair{}, false, nil
	}
	var gp gracePair
	if json.Unmarshal(plain, &gp) != nil {
		return TokenPair{}, false, nil
	}
	var alive int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM session
		WHERE family_id = $1 AND token_hash IN ($2, $3) AND revoked_at IS NULL AND expires_at > $4`,
		family, crypto.HashToken(gp.Refresh), crypto.HashToken(gp.Access), now).Scan(&alive); err != nil {
		return TokenPair{}, false, err
	}
	if alive != 2 {
		return TokenPair{}, false, nil // the new pair was rotated or revoked meanwhile
	}
	s.log.InfoContext(ctx, "refresh token replayed inside the grace window", "family_id", family)
	return TokenPair{
		SessionID: family, AccessToken: gp.Access, RefreshToken: gp.Refresh,
		AccessExpiresAt: gp.AccessExp.In(now.Location()), RefreshExpiresAt: gp.RefreshExp.In(now.Location()),
	}, true, nil
}
