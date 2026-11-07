package identity

import (
	"context"
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

// canDerive reports whether rotated tokens can be derived from the server
// secret (needed for the refresh grace window).
func (s *Service) canDerive() bool { return len(s.cfg.SecretKey) >= 16 }

// rotatedTokens are the refresh and access token that replace the refresh
// token row old. They are derived (HMAC of the server secret and the old row
// id) rather than random, so that a retry of the rotation within the grace
// window can be answered with the same pair without storing the tokens.
func (s *Service) rotatedTokens(old uuid.UUID) (refresh, access string, refreshHash, accessHash []byte) {
	refresh, refreshHash = crypto.DeriveToken(s.cfg.SecretKey, "rotated-refresh", old.String())
	access, accessHash = crypto.DeriveToken(s.cfg.SecretKey, "rotated-access", old.String())
	return refresh, access, refreshHash, accessHash
}

func (s *Service) insertPair(ctx context.Context, tx pgx.Tx, userID, family uuid.UUID, rotatedFrom *uuid.UUID, m Meta) (TokenPair, error) {
	now := s.now().Truncate(time.Microsecond) // the database keeps microseconds; a replayed pair must match exactly
	out := TokenPair{SessionID: family, AccessExpiresAt: now.Add(s.cfg.AccessTTL), RefreshExpiresAt: now.Add(s.cfg.RefreshTTL)}
	refreshIn := sessionInsert{userID: userID, family: family, rotatedFrom: rotatedFrom, kind: KindRefreshToken, expires: out.RefreshExpiresAt, m: m}
	accessIn := sessionInsert{userID: userID, family: family, kind: KindAccessToken, expires: out.AccessExpiresAt, m: m}
	if rotatedFrom != nil && s.canDerive() {
		refreshIn.fixedToken, accessIn.fixedToken, _, _ = s.rotatedTokens(*rotatedFrom)
	}
	var err error
	if out.RefreshToken, _, err = s.insertSession(ctx, tx, refreshIn); err != nil {
		return TokenPair{}, err
	}
	out.AccessToken, _, err = s.insertSession(ctx, tx, accessIn)
	return out, err
}

// Refresh exchanges a refresh token for a new pair and retires the old
// refresh token. The new session keeps the device name of the old one.
//
// Presenting a refresh token that was already rotated means it leaked (or the
// client raced itself): the whole family is revoked and the user must sign in
// again. One exception protects against a lost response: if the rotation
// happened less than RefreshGrace ago and its new refresh token has not been
// used yet, the old token is accepted ONCE more and answered with the same new
// pair, so a client that never received the first answer can retry. The
// trade-off: a thief who replays a stolen token inside those 30 seconds gets
// the same pair as the owner; the next use of the old token, or of the pair by
// the other party's rotation, is still caught by the reuse detection.
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
		var revoked, graceUsed *time.Time
		var status string
		var device *string
		err := tx.QueryRow(ctx, `
			SELECT s.id, s.user_id, s.family_id, s.expires_at, s.revoked_at, s.grace_used_at, s.device_name, u.status
			FROM session s JOIN "user" u ON u.id = s.user_id
			WHERE s.token_hash = $1 AND s.kind = $2 FOR UPDATE OF s`, crypto.HashToken(refreshToken), KindRefreshToken).
			Scan(&rowID, &userID, &family, &expires, &revoked, &graceUsed, &device, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if revoked != nil {
			if pair, replayed, err := s.replayRotation(ctx, tx, rowID, family, *revoked, graceUsed, expires, status); err != nil || replayed {
				out, ok, owner = pair, replayed, userID
				return err
			}
			// Reuse detection: burn the family. This commits (fn returns nil).
			s.log.WarnContext(ctx, "refresh token reuse detected; revoking session family", "user_id", userID, "family_id", family)
			_, err = tx.Exec(ctx, `UPDATE session SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, family, now)
			return err
		}
		if !expires.After(now) || status != "active" {
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE session SET revoked_at = $2 WHERE id = $1`, rowID, now); err != nil {
			return err
		}
		if m.DeviceName == "" && device != nil {
			m.DeviceName = *device
		}
		out, err = s.insertPair(ctx, tx, userID, family, &rowID, m)
		ok, owner = err == nil, userID
		return err
	})
	if err != nil {
		return TokenPair{}, uuid.Nil, problem.Internal(fmt.Errorf("refresh: %w", err))
	}
	if !ok {
		return TokenPair{}, uuid.Nil, errInvalidGrant()
	}
	return out, owner, nil
}

// replayRotation answers a retry of a rotation inside the grace window with
// the pair the rotation issued. It reports false when the token is not
// eligible (window over, already replayed once, the new pair already used or
// revoked, the user disabled) and the caller must treat it as reuse.
func (s *Service) replayRotation(ctx context.Context, tx pgx.Tx, oldID, family uuid.UUID, rotatedAt time.Time, graceUsed *time.Time, expires time.Time, status string) (TokenPair, bool, error) {
	now := s.now()
	if !s.canDerive() || graceUsed != nil || status != "active" || !expires.After(now) || now.Sub(rotatedAt) > RefreshGrace || now.Before(rotatedAt) {
		return TokenPair{}, false, nil
	}
	refresh, access, refreshHash, accessHash := s.rotatedTokens(oldID)
	out := TokenPair{SessionID: family, RefreshToken: refresh, AccessToken: access}
	found := 0
	rows, err := tx.Query(ctx, `
		SELECT token_hash, expires_at FROM session
		WHERE family_id = $1 AND token_hash IN ($2, $3) AND revoked_at IS NULL AND expires_at > $4`,
		family, refreshHash, accessHash, now)
	if err != nil {
		return TokenPair{}, false, err
	}
	for rows.Next() {
		var h []byte
		var exp time.Time
		if err := rows.Scan(&h, &exp); err != nil {
			rows.Close()
			return TokenPair{}, false, err
		}
		if crypto.Equal(h, refreshHash) {
			out.RefreshExpiresAt = exp.In(now.Location())
		} else {
			out.AccessExpiresAt = exp.In(now.Location())
		}
		found++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TokenPair{}, false, err
	}
	if found != 2 {
		return TokenPair{}, false, nil // the new pair was rotated or revoked meanwhile
	}
	if _, err := tx.Exec(ctx, `UPDATE session SET grace_used_at = $2 WHERE id = $1`, oldID, now); err != nil {
		return TokenPair{}, false, err
	}
	s.log.InfoContext(ctx, "refresh token replayed inside the grace window", "family_id", family)
	return out, true, nil
}
