package community

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
)

// ResolveGuard turns an X-Guard-Session token into the account the guard acts
// as and their display name, for the access module's manual open (it
// implements access.GuardResolver). The same checks as the gate console apply.
// A guard without an account is attributed to the console login.
func (s *Service) ResolveGuard(ctx context.Context, orgID, siteID uuid.UUID, token string) (uuid.UUID, string, error) {
	var (
		user uuid.UUID
		name string
	)
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		g, err := s.guardFor(ctx, tx, orgID, siteID, token)
		if err != nil {
			return err
		}
		name = g.Name
		var uid *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT user_id FROM person WHERE org_id = $1 AND id = $2`, orgID, *g.Person).Scan(&uid); err != nil {
			return err
		}
		switch {
		case uid != nil:
			user = *uid
		case authz.PrincipalFromContext(ctx) != nil:
			user = authz.PrincipalFromContext(ctx).UserID
		}
		return nil
	})
	return user, name, err
}
