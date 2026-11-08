package httpapi

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/collections"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (s *apiServer) bi() (*bankimport.Service, error) {
	if s.mny.BankImport == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.BankImport, nil
}

func (s *apiServer) co() (*collections.Service, error) {
	if s.mny.Collections == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.Collections, nil
}

// ------------------------------------------------------------ bank import

func (s *apiServer) MoneyListBankImporters(ctx context.Context, req gen.MoneyListBankImportersRequestObject) (gen.MoneyListBankImportersResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.BankImporterList](b.Importers(), "")
	return gen.MoneyListBankImporters200JSONResponse(out), err
}

// bankMapping reads the multipart `mapping` field: a ColumnMapping or just its columns object.
func bankMapping(raw string) (bankimport.Mapping, error) {
	var m bankimport.Mapping
	invalid := problem.Validation(problem.Field{Field: "mapping", Code: "invalid_format"})
	var generic map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return m, invalid
	}
	if _, ok := generic["columns"]; ok {
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return m, invalid
		}
		return m, nil
	}
	cols := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &cols); err != nil {
		return m, invalid
	}
	m.Columns = cols
	return m, nil
}

func balanceField(v, field string) (*int64, error) {
	if v == "" {
		return nil, nil
	}
	n, err := bankimport.ParseAmount(v)
	if err != nil {
		return nil, problem.Validation(problem.Field{Field: field, Code: "invalid_format"})
	}
	return &n, nil
}

func (s *apiServer) MoneyCreateBankStatementImport(ctx context.Context, req gen.MoneyCreateBankStatementImportRequestObject) (gen.MoneyCreateBankStatementImportResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	up, err := readUpload(req.Body)
	if err != nil {
		return nil, err
	}
	in := bankimport.CreateInput{Importer: up.fields["importer"], Filename: up.filename, Data: up.data}
	if in.SiteID, err = uuid.Parse(up.fields["site_id"]); err != nil {
		return nil, problem.Validation(problem.Field{Field: "site_id", Code: "invalid_format"})
	}
	if in.AssetAccountID, err = uuid.Parse(up.fields["asset_account_id"]); err != nil {
		return nil, problem.Validation(problem.Field{Field: "asset_account_id", Code: "invalid_format"})
	}
	if in.Importer == "" {
		return nil, problem.Validation(problem.Field{Field: "importer", Code: "required"})
	}
	if m := up.fields["mapping"]; m != "" {
		if in.Mapping, err = bankMapping(m); err != nil {
			return nil, err
		}
	}
	if in.OpeningBalance, err = balanceField(up.fields["opening_balance"], "opening_balance"); err != nil {
		return nil, err
	}
	if in.ClosingBalance, err = balanceField(up.fields["closing_balance"], "closing_balance"); err != nil {
		return nil, err
	}
	v, err := b.Create(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BankStatementImport](v)
	return gen.MoneyCreateBankStatementImport201JSONResponse(out), err
}

func (s *apiServer) MoneyListBankStatementImports(ctx context.Context, req gen.MoneyListBankStatementImportsRequestObject) (gen.MoneyListBankStatementImportsResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := b.ListImports(ctx, req.OrgId, siteOf(p.SiteId), deref(p.Status), p.AssetAccountId, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.BankStatementImportList](items, next)
	return gen.MoneyListBankStatementImports200JSONResponse(out), err
}

func (s *apiServer) MoneyGetBankStatementImport(ctx context.Context, req gen.MoneyGetBankStatementImportRequestObject) (gen.MoneyGetBankStatementImportResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	v, err := b.GetImport(ctx, req.OrgId, req.ImportId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BankStatementImport](v)
	return gen.MoneyGetBankStatementImport200JSONResponse(out), err
}

func (s *apiServer) MoneyDeleteBankStatementImport(ctx context.Context, req gen.MoneyDeleteBankStatementImportRequestObject) (gen.MoneyDeleteBankStatementImportResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	if err := b.DeleteImport(ctx, req.OrgId, req.ImportId); err != nil {
		return nil, err
	}
	return gen.MoneyDeleteBankStatementImport204Response{}, nil
}

func (s *apiServer) MoneyGetImportReconciliation(ctx context.Context, req gen.MoneyGetImportReconciliationRequestObject) (gen.MoneyGetImportReconciliationResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	v, err := b.Reconciliation(ctx, req.OrgId, req.ImportId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ImportReconciliation](v)
	return gen.MoneyGetImportReconciliation200JSONResponse(out), err
}

func (s *apiServer) MoneyListBankTransactions(ctx context.Context, req gen.MoneyListBankTransactionsRequestObject) (gen.MoneyListBankTransactionsResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := bankimport.TxFilter{ImportID: p.ImportId, MatchStatus: deref(p.MatchStatus), From: dateStr(p.From), To: dateStr(p.To), Q: deref(p.Q)}
	items, next, err := b.ListTransactions(ctx, req.OrgId, siteOf(p.SiteId), f, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.BankTransactionList](items, next)
	return gen.MoneyListBankTransactions200JSONResponse(out), err
}

func (s *apiServer) MoneyGetBankTransaction(ctx context.Context, req gen.MoneyGetBankTransactionRequestObject) (gen.MoneyGetBankTransactionResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	v, err := b.GetTransaction(ctx, req.OrgId, req.TransactionId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BankTransaction](v)
	return gen.MoneyGetBankTransaction200JSONResponse(out), err
}

func (s *apiServer) MoneyMatchBankTransaction(ctx context.Context, req gen.MoneyMatchBankTransactionRequestObject) (gen.MoneyMatchBankTransactionResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[bankimport.MatchInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := b.Match(ctx, req.OrgId, req.TransactionId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BankTransaction](v)
	return gen.MoneyMatchBankTransaction200JSONResponse(out), err
}

func (s *apiServer) MoneyUnmatchBankTransaction(ctx context.Context, req gen.MoneyUnmatchBankTransactionRequestObject) (gen.MoneyUnmatchBankTransactionResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	v, err := b.Unmatch(ctx, req.OrgId, req.TransactionId, deref(req.Body.Reason))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BankTransaction](v)
	return gen.MoneyUnmatchBankTransaction200JSONResponse(out), err
}

func (s *apiServer) MoneyIgnoreBankTransaction(ctx context.Context, req gen.MoneyIgnoreBankTransactionRequestObject) (gen.MoneyIgnoreBankTransactionResponseObject, error) {
	b, err := s.bi()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	v, err := b.Ignore(ctx, req.OrgId, req.TransactionId, deref(req.Body.Reason))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BankTransaction](v)
	return gen.MoneyIgnoreBankTransaction200JSONResponse(out), err
}

// ------------------------------------------------------------ dunning stages

func (s *apiServer) MoneyListDunningStages(ctx context.Context, req gen.MoneyListDunningStagesRequestObject) (gen.MoneyListDunningStagesResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	items, next, err := c.ListStages(ctx, req.OrgId, siteOf(req.Params.SiteId), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.DunningStageList](items, next)
	return gen.MoneyListDunningStages200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateDunningStage(ctx context.Context, req gen.MoneyCreateDunningStageRequestObject) (gen.MoneyCreateDunningStageResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[collections.StageInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := c.CreateStage(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DunningStage](v)
	return gen.MoneyCreateDunningStage201JSONResponse(out), err
}

func (s *apiServer) MoneyGetDunningStage(ctx context.Context, req gen.MoneyGetDunningStageRequestObject) (gen.MoneyGetDunningStageResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	v, err := c.GetStage(ctx, req.OrgId, req.StageId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DunningStage](v)
	return gen.MoneyGetDunningStage200JSONResponse(out), err
}

// nullableString reads a PATCH field: absent leaves it (nil), null clears it (pointer to nil).
func nullableString(m map[string]json.RawMessage, key string) (**string, error) {
	raw, ok := m[key]
	if !ok {
		return nil, nil
	}
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, problem.Validation(problem.Field{Field: key, Code: "invalid_type"})
	}
	return &v, nil
}

func (s *apiServer) MoneyUpdateDunningStage(ctx context.Context, req gen.MoneyUpdateDunningStageRequestObject) (gen.MoneyUpdateDunningStageResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	version, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	m, err := conv[map[string]json.RawMessage](req.Body)
	if err != nil {
		return nil, err
	}
	var u collections.StageUpdate
	if err := decodeFields(m, map[string]any{"name": &u.Name, "kind": &u.Kind, "days_after_due": &u.DaysAfterDue, "channels": &u.Channels, "position": &u.Position, "enabled": &u.Enabled}); err != nil {
		return nil, err
	}
	if u.TemplateKey, err = nullableString(m, "template_key"); err != nil {
		return nil, err
	}
	v, err := c.UpdateStage(ctx, req.OrgId, req.StageId, version, u)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DunningStage](v)
	return gen.MoneyUpdateDunningStage200JSONResponse(out), err
}

// decodeFields decodes the present keys of a PATCH body into pointer-to-pointer targets.
func decodeFields(m map[string]json.RawMessage, targets map[string]any) error {
	for key, dst := range targets {
		raw, ok := m[key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return problem.Validation(problem.Field{Field: key, Code: "invalid_type"})
		}
	}
	return nil
}

func (s *apiServer) MoneyDeleteDunningStage(ctx context.Context, req gen.MoneyDeleteDunningStageRequestObject) (gen.MoneyDeleteDunningStageResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	version, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := c.DeleteStage(ctx, req.OrgId, req.StageId, version); err != nil {
		return nil, err
	}
	return gen.MoneyDeleteDunningStage204Response{}, nil
}

// ------------------------------------------------------------ dunning runs and events

type runBody struct {
	SiteID     uuid.UUID        `json:"site_id"`
	StageID    uuid.UUID        `json:"stage_id"`
	Debtors    []billing.Debtor `json:"debtors"`
	MinBalance *money.Money     `json:"min_balance"`
	Channels   []string         `json:"channels"`
}

func (b runBody) input() collections.RunInput {
	return collections.RunInput{SiteID: b.SiteID, StageID: b.StageID, Debtors: b.Debtors, MinBalance: b.MinBalance, Channels: b.Channels}
}

func (s *apiServer) MoneyPreviewDunningRun(ctx context.Context, req gen.MoneyPreviewDunningRunRequestObject) (gen.MoneyPreviewDunningRunResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	b, err := conv[runBody](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := c.PreviewRun(ctx, req.OrgId, b.input())
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DunningRunPreview](v)
	return gen.MoneyPreviewDunningRun200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateDunningRun(ctx context.Context, req gen.MoneyCreateDunningRunRequestObject) (gen.MoneyCreateDunningRunResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	b, err := conv[runBody](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := c.CreateRun(ctx, req.OrgId, b.input())
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DunningRun](v)
	return gen.MoneyCreateDunningRun201JSONResponse(out), err
}

func (s *apiServer) MoneyListDunningEvents(ctx context.Context, req gen.MoneyListDunningEventsRequestObject) (gen.MoneyListDunningEventsResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := collections.EventFilter{UnitID: p.UnitId, DebtorKind: deref(p.DebtorKind), StageID: p.StageId, From: dateStr(p.From), To: dateStr(p.To)}
	items, next, err := c.ListEvents(ctx, req.OrgId, siteOf(p.SiteId), f, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.DunningEventList](items, next)
	return gen.MoneyListDunningEvents200JSONResponse(out), err
}

// debtorParams reads the debtor query parameters (unit side or person); nil when none is given.
func debtorParams(unit *uuid.UUID, kind *string, person *uuid.UUID) (*billing.Debtor, error) {
	if unit == nil && kind == nil && person == nil {
		return nil, nil
	}
	d := billing.Debtor{UnitID: unit, PersonID: person, Kind: deref(kind)}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *apiServer) MoneyGetDunningLetter(ctx context.Context, req gen.MoneyGetDunningLetterRequestObject) (gen.MoneyGetDunningLetterResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	p := req.Params
	var kind *string
	if p.DebtorKind != nil {
		k := string(*p.DebtorKind)
		kind = &k
	}
	in := collections.LetterInput{EventID: p.EventId, AsOf: dateStr(p.AsOf)}
	if p.EventId == nil {
		if p.SiteId == nil {
			return nil, problem.Validation(problem.Field{Field: "event_id", Code: "required"})
		}
		in.SiteID = *p.SiteId
		if in.Debtor, err = debtorParams(p.UnitId, kind, p.PersonId); err != nil {
			return nil, err
		}
	}
	b, err := c.Letter(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	return gen.MoneyGetDunningLetter200ApplicationpdfResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
}

// ------------------------------------------------------------ collection cases

func (s *apiServer) MoneyListCollectionCases(ctx context.Context, req gen.MoneyListCollectionCasesRequestObject) (gen.MoneyListCollectionCasesResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := c.ListCases(ctx, req.OrgId, siteOf(p.SiteId), collections.CaseFilter{UnitID: p.UnitId, Status: deref(p.Status)}, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.CollectionCaseList](items, next)
	return gen.MoneyListCollectionCases200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateCollectionCase(ctx context.Context, req gen.MoneyCreateCollectionCaseRequestObject) (gen.MoneyCreateCollectionCaseResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[collections.CaseInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := c.CreateCase(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.CollectionCase](v)
	return gen.MoneyCreateCollectionCase201JSONResponse(out), err
}

func (s *apiServer) MoneyGetCollectionCase(ctx context.Context, req gen.MoneyGetCollectionCaseRequestObject) (gen.MoneyGetCollectionCaseResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	v, err := c.GetCase(ctx, req.OrgId, req.CaseId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.CollectionCase](v)
	return gen.MoneyGetCollectionCase200JSONResponse(out), err
}

func (s *apiServer) MoneyUpdateCollectionCase(ctx context.Context, req gen.MoneyUpdateCollectionCaseRequestObject) (gen.MoneyUpdateCollectionCaseResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	version, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	m, err := conv[map[string]json.RawMessage](req.Body)
	if err != nil {
		return nil, err
	}
	var u collections.CaseUpdate
	if u.Lawyer, err = nullableString(m, "lawyer"); err != nil {
		return nil, err
	}
	if u.FileNo, err = nullableString(m, "file_no"); err != nil {
		return nil, err
	}
	if u.Notes, err = nullableString(m, "notes"); err != nil {
		return nil, err
	}
	v, err := c.UpdateCase(ctx, req.OrgId, req.CaseId, version, u)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.CollectionCase](v)
	return gen.MoneyUpdateCollectionCase200JSONResponse(out), err
}

func (s *apiServer) MoneyCloseCollectionCase(ctx context.Context, req gen.MoneyCloseCollectionCaseRequestObject) (gen.MoneyCloseCollectionCaseResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	var costs *money.Money
	var note string
	if req.Body != nil {
		b, err := conv[struct {
			Costs *money.Money `json:"costs"`
			Note  *string      `json:"note"`
		}](req.Body)
		if err != nil {
			return nil, err
		}
		costs, note = b.Costs, deref(b.Note)
	}
	v, err := c.CloseCase(ctx, req.OrgId, req.CaseId, costs, note)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.CollectionCase](v)
	return gen.MoneyCloseCollectionCase200JSONResponse(out), err
}

// ------------------------------------------------------------ debt breakdown

func (s *apiServer) MoneyGetDebtBreakdown(ctx context.Context, req gen.MoneyGetDebtBreakdownRequestObject) (gen.MoneyGetDebtBreakdownResponseObject, error) {
	c, err := s.co()
	if err != nil {
		return nil, err
	}
	p := req.Params
	var kind *string
	if p.DebtorKind != nil {
		k := string(*p.DebtorKind)
		kind = &k
	}
	d, err := debtorParams(p.UnitId, kind, p.PersonId)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, problem.Validation(problem.Field{Field: "unit_id", Code: "required"})
	}
	bd, err := c.Breakdown(ctx, req.OrgId, siteOf(p.SiteId), *d, dateStr(p.AsOf))
	if err != nil {
		return nil, err
	}
	if f := p.Format; f != nil {
		var raw []byte
		switch *f {
		case gen.MoneyGetDebtBreakdownParamsFormatCsv:
			if raw, err = bd.CSV(); err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetDebtBreakdown200TextcsvResponse{Body: bytes.NewReader(raw), ContentLength: int64(len(raw))}, nil
		case gen.MoneyGetDebtBreakdownParamsFormatXlsx:
			if raw, err = bd.XLSX(); err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetDebtBreakdown200ApplicationvndOpenxmlformatsOfficedocumentSpreadsheetmlSheetResponse{Body: bytes.NewReader(raw), ContentLength: int64(len(raw))}, nil
		case gen.MoneyGetDebtBreakdownParamsFormatPdf:
			if raw, err = bd.PDF("Borç dökümü"); err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetDebtBreakdown200ApplicationpdfResponse{Body: bytes.NewReader(raw), ContentLength: int64(len(raw))}, nil
		case gen.MoneyGetDebtBreakdownParamsFormatJson:
		default:
			return nil, problem.Validation(problem.Field{Field: "format", Code: "unsupported"})
		}
	}
	out, err := conv[gen.DebtBreakdown](bd)
	return gen.MoneyGetDebtBreakdown200JSONResponse(out), err
}
