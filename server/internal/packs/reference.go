package packs

import (
	"context"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/sdk"
)

// BankReference implements tenancy.BankReferenceHint: the first pack enabled for
// the organization that implements sdk.ReferenceHinter names the reference.
func (h *Host) BankReference(ctx context.Context, org uuid.UUID, door, period string) (string, bool, error) {
	active, err := h.Active(ctx, org)
	if err != nil {
		return "", false, err
	}
	for _, p := range active {
		rh, ok := p.impl.(sdk.ReferenceHinter)
		if !ok {
			continue
		}
		var ref string
		err := h.call(ctx, org, p.Manifest.ID, "SuggestedReference", func(ctx context.Context) (err error) {
			ref, err = rh.SuggestedReference(ctx, door, period)
			return err
		})
		if err != nil {
			return "", false, err
		}
		if ref != "" {
			return ref, true, nil
		}
	}
	return "", false, nil
}
