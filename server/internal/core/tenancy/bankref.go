package tenancy

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// BankReferenceHint is the seam through which a pack suggests the text a payer
// writes on a bank transfer (the packs module implements it with the packs
// enabled for the organization). ok is false when no enabled pack has a scheme.
type BankReferenceHint interface {
	BankReference(ctx context.Context, org uuid.UUID, door, period string) (reference string, ok bool, err error)
}

// WithBankReferenceHint sets the hint source and returns s.
func (s *Service) WithBankReferenceHint(h BankReferenceHint) *Service {
	s.refHint = h
	return s
}

// SuggestedReference returns the reference text to write on a transfer for a
// unit of the site and the current month at the site; nil when no pack in the
// organization offers a scheme. The caller has already passed the bank details
// permission check.
func (s *Service) SuggestedReference(ctx context.Context, orgID, siteID, unitID uuid.UUID) (*string, error) {
	if s.refHint == nil {
		return nil, nil
	}
	var door, tz string
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT u.door_number, s.timezone FROM unit u JOIN site s ON s.org_id = u.org_id AND s.id = u.site_id
			WHERE u.org_id = $1 AND u.site_id = $2 AND u.id = $3`, orgID, siteID, unitID).Scan(&door, &tz)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such unit")
		}
		return err
	})
	if err != nil {
		return nil, DBProblem(err, "suggested reference")
	}
	now := time.Now()
	if loc, err := time.LoadLocation(tz); err == nil {
		now = now.In(loc)
	}
	ref, ok, err := s.refHint.BankReference(ctx, orgID, door, now.Format("2006-01"))
	if err != nil || !ok {
		return nil, err
	}
	return &ref, nil
}
