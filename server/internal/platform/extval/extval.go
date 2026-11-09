// Package extval is the seam between the core's validation points (vendors,
// vehicles, bank details, imports) and the validators packs contribute. The
// core validates a value with its own generic checks first; then, for an
// organization with a pack enabled, with the pack's validators of the kind for
// the organization's country. The core never imports a pack: the wiring hands
// the modules a Validators backed by the pack host.
package extval

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// Validators runs the validators of the packs enabled for an organization.
type Validators interface {
	// ValidateValue checks value with the validators of kind for country. ok is
	// false when no enabled pack has one (nothing was checked); err is the
	// first failure.
	ValidateValue(ctx context.Context, org uuid.UUID, kind, country, value string) (ok bool, err error)
}

// Kinds the core asks for.
const (
	KindTaxID = "tax_id"
	KindIBAN  = "iban"
	KindPlate = "plate"
)

type coder interface{ Code() string }

// FieldProblem turns a validator's error into a field problem: code
// invalid_format, the validator's own code (a catalog key such as
// "tr.validator.tax_id.checksum", which clients label through the codes
// endpoint) in the params.
func FieldProblem(field string, err error) problem.Field {
	f := problem.Field{Field: field, Code: "invalid_format"}
	if c, ok := err.(coder); ok {
		f.Params = map[string]any{"validator": c.Code()}
	}
	return f
}

// Check validates value of a kind for the organization of the transaction with
// the enabled packs' validators. It returns nil when there is no Validators,
// no validator applies, or the value is valid.
func Check(ctx context.Context, v Validators, tx pgx.Tx, org uuid.UUID, kind, field, value string) *problem.Field {
	if v == nil || value == "" {
		return nil
	}
	var country string
	if err := tx.QueryRow(ctx, `SELECT country_code FROM organization WHERE id = $1`, org).Scan(&country); err != nil {
		return nil
	}
	if _, err := v.ValidateValue(ctx, org, kind, country, value); err != nil {
		f := FieldProblem(field, err)
		return &f
	}
	return nil
}
