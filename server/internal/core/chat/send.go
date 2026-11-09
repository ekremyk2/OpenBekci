package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/chat"
)

// Reach is what the dispatcher knows about a user's chat before it sends.
type Reach int

// Reach values.
const (
	// ReachNone: no verified link.
	ReachNone Reach = iota
	// ReachOpen: a verified link whose provider accepts a message now.
	ReachOpen
	// ReachWindowClosed: only links whose provider's session window is over.
	ReachWindowClosed
)

// Reaches classifies users on the dispatcher's transaction. The windows of
// the core providers are known up front; a pack provider's window is only
// found out when it refuses the message (shortcut: no fallback planned for
// it, the delivery is skipped as window_closed).
func (s *Service) Reaches(ctx context.Context, tx pgx.Tx, org uuid.UUID, users []uuid.UUID) (map[uuid.UUID]Reach, error) {
	out := map[uuid.UUID]Reach{}
	if len(users) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT user_id, provider, last_inbound_at FROM chat_link WHERE org_id = $1 AND user_id = ANY($2) AND verified_at IS NOT NULL`, org, users)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := s.cfg.Now()
	for rows.Next() {
		var u uuid.UUID
		var provider string
		var last *time.Time
		if err := rows.Scan(&u, &provider, &last); err != nil {
			return nil, err
		}
		r := ReachOpen
		if b, ok := chat.BuiltinByID(provider); ok && b.Window > 0 && (last == nil || now.Sub(*last) >= b.Window) {
			r = ReachWindowClosed
		}
		if r == ReachOpen || out[u] == ReachNone {
			out[u] = r
		}
	}
	return out, rows.Err()
}

// Send delivers m to every verified chat link of the user and returns nil
// when at least one accepted it. With none accepting it returns ErrNoLink
// (no link), chat.ErrWindowClosed or chat.ErrDead (every link refused for
// that reason; a dead link is marked unverified), or the first retryable
// error, which the job policy retries.
func (s *Service) Send(ctx context.Context, org, user uuid.UUID, m chat.Message) error {
	type link struct {
		id       uuid.UUID
		provider string
		sealed   []byte
		last     *time.Time
	}
	var links []link
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, provider, external_id_sealed, last_inbound_at FROM chat_link
			WHERE org_id = $1 AND user_id = $2 AND verified_at IS NOT NULL ORDER BY created_at, id`, org, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l link
			if err := rows.Scan(&l.id, &l.provider, &l.sealed, &l.last); err != nil {
				return err
			}
			links = append(links, l)
		}
		return rows.Err()
	})
	if err != nil {
		return fmt.Errorf("load chat links: %w", err)
	}
	if len(links) == 0 {
		return ErrNoLink
	}
	var sent bool
	var retry, final error
	for _, l := range links {
		p, err := s.provider(ctx, org, l.provider)
		if err != nil {
			retry = errors.Join(retry, fmt.Errorf("chat provider %s: %w", l.provider, err))
			continue
		}
		if w := p.Window(); w > 0 && (l.last == nil || s.cfg.Now().Sub(*l.last) >= w) {
			final = chat.ErrWindowClosed
			continue
		}
		to, err := s.cipher.Open(l.sealed, s.aad(org, l.provider, "external"))
		if err != nil {
			final = chat.ErrDead
			s.markDead(ctx, org, l.id)
			continue
		}
		switch err := p.Send(ctx, string(to), m); {
		case err == nil:
			sent = true
		case errors.Is(err, chat.ErrWindowClosed):
			final = chat.ErrWindowClosed
		case errors.Is(err, chat.ErrDead):
			final = chat.ErrDead
			s.markDead(ctx, org, l.id)
		default:
			retry = errors.Join(retry, err)
		}
	}
	switch {
	case sent:
		return nil
	case retry != nil:
		return retry
	}
	return final
}

// markDead turns a link unverified: the provider says the chat is gone. The
// person sees it in the profile and links again; nothing is sent meanwhile.
func (s *Service) markDead(ctx context.Context, org, linkID uuid.UUID) {
	err := s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE chat_link SET verified_at = NULL, external_hash = NULL WHERE org_id = $1 AND id = $2`, org, linkID)
		return err
	})
	if err != nil {
		s.cfg.Log.ErrorContext(ctx, "mark chat link unverified", "error", err)
	}
}
