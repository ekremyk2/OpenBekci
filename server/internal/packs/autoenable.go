package packs

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
)

// AutoEnableForCountry enables every pack that names countries
// (Manifest.Countries) for the organizations of those countries that have no
// enablement row for it yet, so that organizations created before the pack
// existed do not silently lose the legal defaults the core used to carry (the
// Turkish governance presets moved into the tr pack). It is idempotent and
// meant to run at every server start: an organization that has a row, enabled
// or disabled, is never touched, so an administrator's decision to disable the
// pack sticks. The enablement is the ordinary one (presets applied to the
// sites that have not changed the settings, chart names localized, audited as
// a system action); a pack whose enablement needs settings fails for the
// organization and is skipped (logged). It returns how many organizations
// were enabled.
func (h *Host) AutoEnableForCountry(ctx context.Context) (int, error) {
	total := 0
	for _, p := range h.packs {
		if len(p.Manifest.Countries) == 0 {
			continue
		}
		var orgs []uuid.UUID
		err := h.cfg.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT o.id FROM organization o WHERE o.status = 'active' AND o.country_code = ANY($1)
				AND NOT EXISTS (SELECT 1 FROM pack_enablement e WHERE e.org_id = o.id AND e.pack_id = $2) ORDER BY o.created_at, o.id`,
				slices.Clone(p.Manifest.Countries), p.Manifest.ID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					return err
				}
				orgs = append(orgs, id)
			}
			return rows.Err()
		})
		if err != nil {
			return total, err
		}
		for _, org := range orgs {
			sys := authz.WithGrants(authz.WithPrincipal(ctx, &authz.Principal{}), authz.Grants{OrgID: org, Items: []authz.Grant{{Permission: PermManage, ScopeType: authz.ScopeOrg}}})
			if _, err := h.Enable(sys, org, p.Manifest.ID, nil); err != nil {
				h.log.WarnContext(ctx, "could not enable the pack of the organization's country", "pack", p.Manifest.ID, "org", org, "error", err)
				continue
			}
			total++
			h.log.InfoContext(ctx, "enabled the pack of the organization's country", "pack", p.Manifest.ID, "org", org)
		}
	}
	return total, nil
}
