package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoMoney = errors.New("money services not configured")

// MoneyDeps are the money services the HTTP layer serves (wired in internal/app).
type MoneyDeps struct {
	Ledger *ledger.Service
}

func (s *apiServer) lg() (*ledger.Service, error) {
	if s.mny.Ledger == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.Ledger, nil
}

// siteOf reads the site_id query parameter; the services answer 422 for the
// nil id (the spec leaves the parameter optional so authentication comes first).
func siteOf(p *openapi_types.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}

// dateStr renders an optional date parameter as a civil date ("" when absent).
func dateStr(d *openapi_types.Date) string {
	if d == nil {
		return ""
	}
	return d.Format(ledger.DateLayout)
}

func (s *apiServer) MoneyListFunds(ctx context.Context, req gen.MoneyListFundsRequestObject) (gen.MoneyListFundsResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	items, next, err := l.ListFunds(ctx, req.OrgId, siteOf(req.Params.SiteId), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.FundList](items, next)
	return gen.MoneyListFunds200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateFund(ctx context.Context, req gen.MoneyCreateFundRequestObject) (gen.MoneyCreateFundResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[ledger.FundInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := l.CreateFund(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Fund](v)
	return gen.MoneyCreateFund201JSONResponse(out), err
}

func (s *apiServer) MoneyGetFund(ctx context.Context, req gen.MoneyGetFundRequestObject) (gen.MoneyGetFundResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	v, err := l.GetFund(ctx, req.OrgId, req.FundId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Fund](v)
	return gen.MoneyGetFund200JSONResponse(out), err
}

func (s *apiServer) MoneyUpdateFund(ctx context.Context, req gen.MoneyUpdateFundRequestObject) (gen.MoneyUpdateFundResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := l.UpdateFund(ctx, req.OrgId, req.FundId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Fund](v)
	return gen.MoneyUpdateFund200JSONResponse(out), err
}

func (s *apiServer) MoneyListAccounts(ctx context.Context, req gen.MoneyListAccountsRequestObject) (gen.MoneyListAccountsResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	f := ledger.AccountFilter{Archived: req.Params.Archived}
	if req.Params.Type != nil {
		f.Type = *req.Params.Type
	}
	if req.Params.Subtype != nil {
		f.Subtype = *req.Params.Subtype
	}
	items, next, err := l.ListAccounts(ctx, req.OrgId, siteOf(req.Params.SiteId), f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.AccountList](items, next)
	return gen.MoneyListAccounts200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateAccount(ctx context.Context, req gen.MoneyCreateAccountRequestObject) (gen.MoneyCreateAccountResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[ledger.AccountInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := l.CreateAccount(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Account](v)
	return gen.MoneyCreateAccount201JSONResponse(out), err
}

func (s *apiServer) MoneyGetAccount(ctx context.Context, req gen.MoneyGetAccountRequestObject) (gen.MoneyGetAccountResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	v, err := l.GetAccount(ctx, req.OrgId, req.AccountId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Account](v)
	return gen.MoneyGetAccount200JSONResponse(out), err
}

func (s *apiServer) MoneyUpdateAccount(ctx context.Context, req gen.MoneyUpdateAccountRequestObject) (gen.MoneyUpdateAccountResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := l.UpdateAccount(ctx, req.OrgId, req.AccountId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Account](v)
	return gen.MoneyUpdateAccount200JSONResponse(out), err
}

func (s *apiServer) MoneyListCashBoxes(ctx context.Context, req gen.MoneyListCashBoxesRequestObject) (gen.MoneyListCashBoxesResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	items, next, err := l.ListCashBoxes(ctx, req.OrgId, siteOf(req.Params.SiteId), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.CashBoxList](items, next)
	return gen.MoneyListCashBoxes200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateCashBox(ctx context.Context, req gen.MoneyCreateCashBoxRequestObject) (gen.MoneyCreateCashBoxResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[ledger.CashBoxInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := l.CreateCashBox(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.CashBox](v)
	return gen.MoneyCreateCashBox201JSONResponse(out), err
}

func (s *apiServer) MoneyGetCashBox(ctx context.Context, req gen.MoneyGetCashBoxRequestObject) (gen.MoneyGetCashBoxResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	v, err := l.GetCashBox(ctx, req.OrgId, req.CashBoxId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.CashBox](v)
	return gen.MoneyGetCashBox200JSONResponse(out), err
}

func (s *apiServer) MoneyUpdateCashBox(ctx context.Context, req gen.MoneyUpdateCashBoxRequestObject) (gen.MoneyUpdateCashBoxResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := l.UpdateCashBox(ctx, req.OrgId, req.CashBoxId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.CashBox](v)
	return gen.MoneyUpdateCashBox200JSONResponse(out), err
}

func (s *apiServer) MoneyListFiscalYears(ctx context.Context, req gen.MoneyListFiscalYearsRequestObject) (gen.MoneyListFiscalYearsResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	items, err := l.ListFiscalYears(ctx, req.OrgId, siteOf(req.Params.SiteId))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.FiscalYearList](items, "")
	return gen.MoneyListFiscalYears200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateFiscalYear(ctx context.Context, req gen.MoneyCreateFiscalYearRequestObject) (gen.MoneyCreateFiscalYearResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[ledger.FiscalYearInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := l.CreateFiscalYear(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.FiscalYear](v)
	return gen.MoneyCreateFiscalYear201JSONResponse(out), err
}

func (s *apiServer) MoneyGetFiscalYear(ctx context.Context, req gen.MoneyGetFiscalYearRequestObject) (gen.MoneyGetFiscalYearResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	v, err := l.GetFiscalYear(ctx, req.OrgId, req.FiscalYearId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.FiscalYear](v)
	return gen.MoneyGetFiscalYear200JSONResponse(out), err
}

func (s *apiServer) MoneyCloseFiscalYear(ctx context.Context, req gen.MoneyCloseFiscalYearRequestObject) (gen.MoneyCloseFiscalYearResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	var dec *openapi_types.UUID
	if req.Body != nil {
		dec = req.Body.DecisionId
	}
	v, err := l.SetFiscalYearStatus(ctx, req.OrgId, req.FiscalYearId, "closed", dec)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.FiscalYear](v)
	return gen.MoneyCloseFiscalYear200JSONResponse(out), err
}

func (s *apiServer) MoneyApproveFiscalYear(ctx context.Context, req gen.MoneyApproveFiscalYearRequestObject) (gen.MoneyApproveFiscalYearResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	var dec *openapi_types.UUID
	if req.Body != nil {
		dec = req.Body.DecisionId
	}
	v, err := l.SetFiscalYearStatus(ctx, req.OrgId, req.FiscalYearId, "approved", dec)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.FiscalYear](v)
	return gen.MoneyApproveFiscalYear200JSONResponse(out), err
}

func (s *apiServer) MoneyListJournalEntries(ctx context.Context, req gen.MoneyListJournalEntriesRequestObject) (gen.MoneyListJournalEntriesResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	f := ledger.JournalFilter{From: dateStr(req.Params.From), To: dateStr(req.Params.To), AccountID: req.Params.AccountId, SourceID: req.Params.SourceId}
	if req.Params.SourceType != nil {
		f.SourceType = *req.Params.SourceType
	}
	items, next, err := l.ListJournalEntries(ctx, req.OrgId, siteOf(req.Params.SiteId), f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.JournalEntryList](items, next)
	return gen.MoneyListJournalEntries200JSONResponse(out), err
}

func (s *apiServer) MoneyGetJournalEntry(ctx context.Context, req gen.MoneyGetJournalEntryRequestObject) (gen.MoneyGetJournalEntryResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	v, err := l.GetJournalEntry(ctx, req.OrgId, req.EntryId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.JournalEntry](v)
	return gen.MoneyGetJournalEntry200JSONResponse(out), err
}

func (s *apiServer) MoneyReverseJournalEntry(ctx context.Context, req gen.MoneyReverseJournalEntryRequestObject) (gen.MoneyReverseJournalEntryResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	memo := ""
	if req.Body != nil {
		memo = req.Body.Reason
	}
	v, err := l.ReverseJournalEntry(ctx, req.OrgId, req.EntryId, memo)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.JournalEntry](v)
	return gen.MoneyReverseJournalEntry201JSONResponse(out), err
}

func (s *apiServer) MoneyGetLockDate(ctx context.Context, req gen.MoneyGetLockDateRequestObject) (gen.MoneyGetLockDateResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	v, err := l.GetLockDate(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.LockDate](v)
	return gen.MoneyGetLockDate200JSONResponse(out), err
}

func (s *apiServer) MoneySetLockDate(ctx context.Context, req gen.MoneySetLockDateRequestObject) (gen.MoneySetLockDateResponseObject, error) {
	l, err := s.lg()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	var to *string
	if v, err := req.Body.LockDate.Get(); err == nil {
		s := v.Format(ledger.DateLayout)
		to = &s
	}
	v, err := l.SetLockDate(ctx, req.OrgId, req.SiteId, ver, to)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.LockDate](v)
	return gen.MoneySetLockDate200JSONResponse(out), err
}
