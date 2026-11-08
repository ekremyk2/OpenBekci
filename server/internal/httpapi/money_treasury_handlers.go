package httpapi

import (
	"bytes"
	"context"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/treasury"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (s *apiServer) tr() (*treasury.Service, error) {
	if s.mny.Treasury == nil {
		return nil, problem.Internal(errNoMoney)
	}
	return s.mny.Treasury, nil
}

// withOrg adds the organization and the update time (the creation time: these records never change) to a record.
func withOrg(v any, org uuid.UUID) (map[string]any, error) {
	m, err := conv[map[string]any](v)
	if err != nil {
		return nil, err
	}
	m["org_id"] = org
	if _, ok := m["updated_at"]; !ok {
		m["updated_at"] = m["created_at"]
	}
	return m, nil
}

func withOrgList[T any](items []T, org uuid.UUID) ([]map[string]any, error) {
	out := make([]map[string]any, len(items))
	for i, it := range items {
		m, err := withOrg(it, org)
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}

// ------------------------------------------------------------ transfers

func (s *apiServer) MoneyCreateTransfer(ctx context.Context, req gen.MoneyCreateTransferRequestObject) (gen.MoneyCreateTransferResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[treasury.TransferInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.CreateTransfer(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Transfer](m)
	return gen.MoneyCreateTransfer201JSONResponse(out), err
}

func (s *apiServer) MoneyListTransfers(ctx context.Context, req gen.MoneyListTransfersRequestObject) (gen.MoneyListTransfersResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := t.ListTransfers(ctx, req.OrgId, siteOf(q.SiteId), treasury.TransferFilter{AccountID: q.AccountId, From: dateStr(q.From), To: dateStr(q.To), Kind: deref(q.Kind)}, pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	rows, err := withOrgList(items, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.TransferList](rows, next)
	return gen.MoneyListTransfers200JSONResponse(out), err
}

func (s *apiServer) MoneyGetTransfer(ctx context.Context, req gen.MoneyGetTransferRequestObject) (gen.MoneyGetTransferResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	v, err := t.GetTransfer(ctx, req.OrgId, req.TransferId)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Transfer](m)
	return gen.MoneyGetTransfer200JSONResponse(out), err
}

// ------------------------------------------------------------ refunds

func (s *apiServer) MoneyCreateRefund(ctx context.Context, req gen.MoneyCreateRefundRequestObject) (gen.MoneyCreateRefundResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[treasury.RefundInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.CreateRefund(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Refund](m)
	return gen.MoneyCreateRefund201JSONResponse(out), err
}

func (s *apiServer) MoneyListRefunds(ctx context.Context, req gen.MoneyListRefundsRequestObject) (gen.MoneyListRefundsResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := t.ListRefunds(ctx, req.OrgId, siteOf(q.SiteId), treasury.RefundFilter{UnitID: q.UnitId, PersonID: q.PersonId, From: dateStr(q.From), To: dateStr(q.To), Kind: deref(q.Kind)}, pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	rows, err := withOrgList(items, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.RefundList](rows, next)
	return gen.MoneyListRefunds200JSONResponse(out), err
}

func (s *apiServer) MoneyGetRefund(ctx context.Context, req gen.MoneyGetRefundRequestObject) (gen.MoneyGetRefundResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	v, err := t.GetRefund(ctx, req.OrgId, req.RefundId)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Refund](m)
	return gen.MoneyGetRefund200JSONResponse(out), err
}

// ------------------------------------------------------------ expenses

func (s *apiServer) MoneyCreateExpense(ctx context.Context, req gen.MoneyCreateExpenseRequestObject) (gen.MoneyCreateExpenseResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[treasury.ExpenseInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.CreateExpense(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Expense](m)
	return gen.MoneyCreateExpense201JSONResponse(out), err
}

func (s *apiServer) MoneyListExpenses(ctx context.Context, req gen.MoneyListExpensesRequestObject) (gen.MoneyListExpensesResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := t.ListExpenses(ctx, req.OrgId, siteOf(q.SiteId), treasury.ExpenseFilter{Category: q.CategoryAccountId, Fund: q.FundId, Vendor: q.VendorId, Status: deref(q.Status),
		From: dateStr(q.From), To: dateStr(q.To), Q: deref(q.Q)}, pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	rows, err := withOrgList(items, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.ExpenseList](rows, next)
	return gen.MoneyListExpenses200JSONResponse(out), err
}

func (s *apiServer) MoneyGetExpense(ctx context.Context, req gen.MoneyGetExpenseRequestObject) (gen.MoneyGetExpenseResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	v, err := t.GetExpense(ctx, req.OrgId, req.ExpenseId)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Expense](m)
	return gen.MoneyGetExpense200JSONResponse(out), err
}

func (s *apiServer) MoneyVoidExpense(ctx context.Context, req gen.MoneyVoidExpenseRequestObject) (gen.MoneyVoidExpenseResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	v, err := t.VoidExpense(ctx, req.OrgId, req.ExpenseId, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Expense](m)
	return gen.MoneyVoidExpense200JSONResponse(out), err
}

// ------------------------------------------------------------ bills

func billOut(b treasury.Bill, org uuid.UUID) (map[string]any, error) {
	m, err := withOrg(b, org)
	if err != nil {
		return nil, err
	}
	pays := make([]map[string]any, len(b.Payments))
	for i, p := range b.Payments {
		if pays[i], err = withOrg(p, org); err != nil {
			return nil, err
		}
	}
	m["payments"] = pays
	return m, nil
}

func (s *apiServer) MoneyCreateBill(ctx context.Context, req gen.MoneyCreateBillRequestObject) (gen.MoneyCreateBillResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[treasury.BillInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.CreateBill(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	m, err := billOut(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Bill](m)
	return gen.MoneyCreateBill201JSONResponse(out), err
}

func (s *apiServer) MoneyListBills(ctx context.Context, req gen.MoneyListBillsRequestObject) (gen.MoneyListBillsResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := t.ListBills(ctx, req.OrgId, siteOf(q.SiteId), treasury.BillFilter{Vendor: q.VendorId, Status: deref(q.Status), DueFrom: dateStr(q.DueFrom), DueTo: dateStr(q.DueTo)}, pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, len(items))
	for i, b := range items {
		if rows[i], err = billOut(b, req.OrgId); err != nil {
			return nil, err
		}
	}
	out, err := listOf[gen.BillList](rows, next)
	return gen.MoneyListBills200JSONResponse(out), err
}

func (s *apiServer) MoneyGetBill(ctx context.Context, req gen.MoneyGetBillRequestObject) (gen.MoneyGetBillResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	b, err := t.GetBill(ctx, req.OrgId, req.BillId)
	if err != nil {
		return nil, err
	}
	m, err := billOut(b, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Bill](m)
	return gen.MoneyGetBill200JSONResponse(out), err
}

func (s *apiServer) MoneyCreateBillPayment(ctx context.Context, req gen.MoneyCreateBillPaymentRequestObject) (gen.MoneyCreateBillPaymentResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[treasury.BillPaymentInput](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.CreateBillPayment(ctx, req.OrgId, req.BillId, in)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BillPayment](m)
	return gen.MoneyCreateBillPayment201JSONResponse(out), err
}

// ------------------------------------------------------------ budgets

func (s *apiServer) MoneyCreateBudget(ctx context.Context, req gen.MoneyCreateBudgetRequestObject) (gen.MoneyCreateBudgetResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[struct {
		SiteID       uuid.UUID  `json:"site_id"`
		FiscalYearID uuid.UUID  `json:"fiscal_year_id"`
		DecisionID   *uuid.UUID `json:"decision_id"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.CreateBudget(ctx, req.OrgId, in.SiteID, in.FiscalYearID, in.DecisionID)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Budget](m)
	return gen.MoneyCreateBudget201JSONResponse(out), err
}

func (s *apiServer) MoneyListBudgets(ctx context.Context, req gen.MoneyListBudgetsRequestObject) (gen.MoneyListBudgetsResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	q := req.Params
	items, next, err := t.ListBudgets(ctx, req.OrgId, siteOf(q.SiteId), q.FiscalYearId, deref(q.Status), pageOf(q.Limit, q.Cursor))
	if err != nil {
		return nil, err
	}
	rows, err := withOrgList(items, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.BudgetList](rows, next)
	return gen.MoneyListBudgets200JSONResponse(out), err
}

func (s *apiServer) MoneyGetBudget(ctx context.Context, req gen.MoneyGetBudgetRequestObject) (gen.MoneyGetBudgetResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	v, err := t.GetBudget(ctx, req.OrgId, req.BudgetId)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Budget](m)
	return gen.MoneyGetBudget200JSONResponse(out), err
}

func (s *apiServer) MoneyUpdateBudget(ctx context.Context, req gen.MoneyUpdateBudgetRequestObject) (gen.MoneyUpdateBudgetResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(&req.Params.IfMatch)
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
	v, err := t.UpdateBudget(ctx, req.OrgId, req.BudgetId, ver, d)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Budget](m)
	return gen.MoneyUpdateBudget200JSONResponse(out), err
}

func (s *apiServer) MoneyDeleteBudget(ctx context.Context, req gen.MoneyDeleteBudgetRequestObject) (gen.MoneyDeleteBudgetResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := t.DeleteBudget(ctx, req.OrgId, req.BudgetId, ver); err != nil {
		return nil, err
	}
	return gen.MoneyDeleteBudget204Response{}, nil
}

func (s *apiServer) MoneyGetBudgetLines(ctx context.Context, req gen.MoneyGetBudgetLinesRequestObject) (gen.MoneyGetBudgetLinesResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	v, err := t.GetBudgetLines(ctx, req.OrgId, req.BudgetId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BudgetLines](v)
	return gen.MoneyGetBudgetLines200JSONResponse(out), err
}

func (s *apiServer) MoneyPutBudgetLines(ctx context.Context, req gen.MoneyPutBudgetLinesRequestObject) (gen.MoneyPutBudgetLinesResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, hasBody(true)
	}
	in, err := conv[struct {
		Lines []treasury.BudgetLine `json:"lines"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.PutBudgetLines(ctx, req.OrgId, req.BudgetId, ver, in.Lines)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BudgetLines](v)
	return gen.MoneyPutBudgetLines200JSONResponse(out), err
}

func (s *apiServer) MoneyFinalizeBudget(ctx context.Context, req gen.MoneyFinalizeBudgetRequestObject) (gen.MoneyFinalizeBudgetResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	var dec *uuid.UUID
	if req.Body != nil {
		in, err := conv[struct {
			DecisionID *uuid.UUID `json:"decision_id"`
		}](req.Body)
		if err != nil {
			return nil, err
		}
		dec = in.DecisionID
	}
	v, err := t.FinalizeBudgetFor(ctx, req.OrgId, req.BudgetId, dec)
	if err != nil {
		return nil, err
	}
	m, err := withOrg(v, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Budget](m)
	return gen.MoneyFinalizeBudget200JSONResponse(out), err
}

func (s *apiServer) MoneyGetBudgetVsActualReport(ctx context.Context, req gen.MoneyGetBudgetVsActualReportRequestObject) (gen.MoneyGetBudgetVsActualReportResponseObject, error) {
	t, err := s.tr()
	if err != nil {
		return nil, err
	}
	q := req.Params
	rep, err := t.BudgetVsActual(ctx, req.OrgId, siteOf(q.SiteId), q.FiscalYearId)
	if err != nil {
		return nil, err
	}
	if q.Format != nil {
		switch *q.Format {
		case gen.MoneyGetBudgetVsActualReportParamsFormatCsv:
			b, err := rep.CSV()
			if err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetBudgetVsActualReport200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetBudgetVsActualReportParamsFormatJson:
		default:
			return nil, unsupportedFormat()
		}
	}
	out, err := conv[gen.BudgetVsActualReport](rep)
	return gen.MoneyGetBudgetVsActualReport200JSONResponse(out), err
}

var _ = money.Zero
