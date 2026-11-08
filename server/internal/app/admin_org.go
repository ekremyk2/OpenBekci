package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OrgID resolves an organization given by id or slug (admin commands).
func (a *Admin) OrgID(ctx context.Context, slugOrID string) (uuid.UUID, error) {
	var id uuid.UUID
	err := a.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		if u, perr := uuid.Parse(slugOrID); perr == nil {
			return tx.QueryRow(ctx, `SELECT id FROM organization WHERE id = $1`, u).Scan(&id)
		}
		return tx.QueryRow(ctx, `SELECT id FROM organization WHERE slug = $1`, slugOrID).Scan(&id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("no organization %q", slugOrID)
	}
	return id, err
}
