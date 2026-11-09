package packs

import (
	"context"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
)

// BankImporters adapts the host to the bank import module: it returns the
// bankimport.PackImporters that offers the statement importers of the packs
// enabled for an organization. Wire it with bankimport.Service.SetPacks.
func (h *Host) BankImporters() bankimport.PackImporters { return bankImporters{h} }

type bankImporters struct{ h *Host }

func (p bankImporters) Importers(ctx context.Context, orgID uuid.UUID) ([]bankimport.Importer, error) {
	list, err := p.h.Importers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]bankimport.Importer, 0, len(list))
	for _, im := range list {
		out = append(out, bankimport.Importer{ID: im.ID, Name: im.ID, Formats: im.Formats})
	}
	return out, nil
}

func (p bankImporters) Parse(ctx context.Context, orgID uuid.UUID, importerID, filename string, data []byte) ([]bankimport.Line, []bankimport.RowError, *int64, *int64, bool, error) {
	st, ok, err := p.h.ParseStatement(ctx, orgID, importerID, filename, data)
	if err != nil || !ok {
		return nil, nil, nil, nil, ok, err
	}
	lines := make([]bankimport.Line, 0, len(st.Lines))
	for _, l := range st.Lines {
		lines = append(lines, bankimport.Line{LineNo: l.LineNo, BookedOn: l.BookedOn, Amount: l.Amount, Counterparty: l.Counterparty,
			Description: l.Description, Reference: l.Reference, Balance: l.Balance, Raw: l.Raw})
	}
	var errs []bankimport.RowError
	for _, e := range st.Errors {
		errs = append(errs, bankimport.RowError{Row: e.Row, Field: e.Field, Reason: e.Reason})
	}
	return lines, errs, st.OpeningBalance, st.ClosingBalance, true, nil
}
