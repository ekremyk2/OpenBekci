package people

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/crypto"
)

// PlatformTx runs a function in a platform (cross-organization) transaction.
type PlatformTx interface {
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// ResealNationalIDs rewrites every sealed national id that is in the old
// format (no key id) under the current key id, for the admin command. A value
// that does not open with key is left alone and counted as failed. It returns
// how many values were rewritten and how many failed.
func ResealNationalIDs(ctx context.Context, d PlatformTx, key []byte) (done, failed int, err error) {
	c, err := crypto.NewFieldCipher(key, "person.national_id")
	if err != nil {
		return 0, 0, err
	}
	err = d.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT org_id, id, national_id_enc FROM person WHERE national_id_enc IS NOT NULL`)
		if err != nil {
			return err
		}
		type item struct {
			org, id uuid.UUID
			sealed  []byte
		}
		var todo []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.org, &it.id, &it.sealed); err != nil {
				rows.Close()
				return err
			}
			if crypto.NeedsReseal(it.sealed) {
				todo = append(todo, it)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, it := range todo {
			pt, err := c.Open(it.sealed, it.id[:])
			if err != nil {
				failed++
				continue
			}
			sealed, err := c.Seal(pt, it.id[:])
			if err != nil {
				return fmt.Errorf("reseal: %w", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE person SET national_id_enc = $3 WHERE org_id = $1 AND id = $2`, it.org, it.id, sealed); err != nil {
				return err
			}
			done++
		}
		return nil
	})
	return done, failed, err
}
