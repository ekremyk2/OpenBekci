package reports

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/packdocs"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
)

// SetTemplates gives the service the resolver of pack document templates.
func (s *Service) SetTemplates(r doctpl.Resolver) { s.templates = r }

// StatementPDF renders a statement as a PDF: with the template of kind
// ("statement", or "closing_statement" for a move-out) of a pack enabled for
// the organization when there is one for its locale, else the generic PDF.
func (s *Service) StatementPDF(ctx context.Context, org uuid.UUID, st Statement, kind string) []byte {
	if s.templates == nil {
		return st.PDF()
	}
	var locale string
	_ = s.db.InTenantTx(ctx, org, func(tx pgx.Tx) error { locale = packdocs.OrgLocale(ctx, tx, org); return nil })
	if b, ok := packdocs.Render(ctx, s.templates, nil, org, kind, locale, st.packData()); ok {
		return b
	}
	return st.PDF()
}

func tr(m money.Money) string {
	s, err := m.Format("tr-TR")
	if err != nil {
		return m.String()
	}
	return s
}

// packData is the data of the statement templates: lines with a kind code
// (opening, charge, fee, waiver, payment), debit and credit columns and the running balance.
func (st Statement) packData() map[string]any {
	lines := make([]any, 0, len(st.Rows)+1)
	zero := money.Zero(st.Currency)
	lines = append(lines, map[string]any{"kind": "opening", "debit": "", "credit": "", "balance": tr(st.Opening.Total)})
	for _, r := range st.Rows {
		debit, credit := "", ""
		if r.Amount.Amount >= 0 {
			debit = tr(r.Amount)
		} else if neg, err := zero.Sub(r.Amount); err == nil {
			credit = tr(neg)
		}
		lines = append(lines, map[string]any{"date": r.Date, "kind": r.Kind, "part": r.Part, "ref": r.Ref, "debit": debit, "credit": credit, "balance": tr(r.Balance.Total)})
	}
	from := ""
	if st.From != nil {
		from = *st.From
	}
	return map[string]any{"site_name": st.SiteName, "debtor_name": st.Label, "period_from": from, "as_of": st.AsOf, "lines": lines,
		"principal": tr(st.Closing.Principal), "late_fee": tr(st.Closing.Fee), "balance": tr(st.Closing.Total), "move_out_date": st.AsOf, "person": st.Label}
}
