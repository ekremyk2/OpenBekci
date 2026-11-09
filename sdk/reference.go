package sdk

import "context"

// ReferenceHinter is an optional interface of a Pack (found by the host with a
// type assertion): the text a payer writes in the description of a bank
// transfer so that the statement line can be matched to the unit.
//
// Core call site: the bank details response of a site
// (GET /v1/orgs/{org}/sites/{site}/bank-details?unit_id=) returns it as
// suggested_reference for organizations that have the pack enabled. The text
// should be one the pack's own statement importers lift into
// StatementLine.Reference again (the Turkish pack: "<door>-<YYYY-MM>").
type ReferenceHinter interface {
	// SuggestedReference returns the reference text for a unit's door number and
	// a period ("2026-03").
	SuggestedReference(ctx context.Context, door, period string) (string, error)
}
