package httpapi

import (
	"bytes"
	"context"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/reports"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func unsupportedFormat() error {
	return problem.Validation(problem.Field{Field: "format", Code: "unsupported"})
}

// statementMap turns a statement into the API's Statement.
func statementMap(st reports.Statement, debtor map[string]any, kind any) map[string]any {
	cur := st.Currency
	zero := money.Zero(cur)
	lines := []map[string]any{}
	for _, row := range st.Rows {
		debit, credit := zero, zero
		if row.Amount.Amount >= 0 {
			debit = row.Amount
		} else {
			credit = money.New(-row.Amount.Amount, cur)
		}
		principal, fee := zero, zero
		if row.Part == "fee" {
			fee = row.Amount
		} else {
			principal = row.Amount
		}
		lines = append(lines, map[string]any{"date": row.Date, "kind": row.Kind, "description": row.Ref, "debit": debit, "credit": credit, "principal": principal, "late_fee": fee, "balance": row.Balance.Total,
			"debtor_kind": kind, "debtor_display_name": nil, "ref_type": nil, "ref_id": nil})
	}
	from, to := st.AsOf, st.AsOf
	if st.From != nil {
		from = *st.From
	} else if len(st.Rows) > 0 {
		from = st.Rows[0].Date
	}
	if st.To != nil {
		to = *st.To
	}
	return map[string]any{"debtor": debtor, "from": from, "to": to, "opening_balance": st.Opening.Total, "closing_balance": st.Closing.Total,
		"accrued_late_fee": st.AccruedFee, "credit": st.Credit, "total_due": st.Closing.Total, "lines": lines}
}

func (s *apiServer) MoneyGetPersonStatement(ctx context.Context, req gen.MoneyGetPersonStatementRequestObject) (gen.MoneyGetPersonStatementResponseObject, error) {
	r, err := s.rp()
	if err != nil {
		return nil, err
	}
	st, err := r.PersonStatement(ctx, req.OrgId, req.PersonId, dateStr(req.Params.From), dateStr(req.Params.To))
	if err != nil {
		return nil, err
	}
	if f := req.Params.Format; f != nil {
		switch *f {
		case gen.MoneyGetPersonStatementParamsFormatCsv:
			b, err := st.CSV()
			if err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetPersonStatement200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetPersonStatementParamsFormatPdf:
			b := r.StatementPDF(ctx, req.OrgId, st, "statement")
			return gen.MoneyGetPersonStatement200ApplicationpdfResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetPersonStatementParamsFormatJson:
		default:
			return nil, unsupportedFormat()
		}
	}
	out, err := conv[gen.Statement](statementMap(st, map[string]any{"mode": "person", "person_id": req.PersonId}, nil))
	return gen.MoneyGetPersonStatement200JSONResponse(out), err
}

func (s *apiServer) MoneyGetTrialBalanceReport(ctx context.Context, req gen.MoneyGetTrialBalanceReportRequestObject) (gen.MoneyGetTrialBalanceReportResponseObject, error) {
	r, err := s.rp()
	if err != nil {
		return nil, err
	}
	q := req.Params
	rep, err := r.TrialBalanceFor(ctx, req.OrgId, siteOf(q.SiteId), dateStr(q.AsOf), q.FundId)
	if err != nil {
		return nil, err
	}
	if q.Format != nil {
		switch *q.Format {
		case gen.MoneyGetTrialBalanceReportParamsFormatCsv:
			b, err := rep.CSV()
			if err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetTrialBalanceReport200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetTrialBalanceReportParamsFormatJson:
		default:
			return nil, unsupportedFormat()
		}
	}
	out, err := conv[gen.TrialBalanceReport](rep)
	return gen.MoneyGetTrialBalanceReport200JSONResponse(out), err
}

func (s *apiServer) MoneyGetReceivablesAgingReport(ctx context.Context, req gen.MoneyGetReceivablesAgingReportRequestObject) (gen.MoneyGetReceivablesAgingReportResponseObject, error) {
	r, err := s.rp()
	if err != nil {
		return nil, err
	}
	q := req.Params
	rep, err := r.AgingByUnit(ctx, req.OrgId, siteOf(q.SiteId), dateStr(q.AsOf), q.BlockId)
	if err != nil {
		return nil, err
	}
	if q.Format != nil {
		switch *q.Format {
		case gen.MoneyGetReceivablesAgingReportParamsFormatCsv:
			b, err := rep.CSV()
			if err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetReceivablesAgingReport200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetReceivablesAgingReportParamsFormatJson:
		default:
			return nil, unsupportedFormat()
		}
	}
	out, err := conv[gen.ReceivablesAgingReport](rep)
	return gen.MoneyGetReceivablesAgingReport200JSONResponse(out), err
}

func (s *apiServer) MoneyGetFundBalancesReport(ctx context.Context, req gen.MoneyGetFundBalancesReportRequestObject) (gen.MoneyGetFundBalancesReportResponseObject, error) {
	r, err := s.rp()
	if err != nil {
		return nil, err
	}
	q := req.Params
	rep, err := r.FundBalancesFor(ctx, req.OrgId, siteOf(q.SiteId), dateStr(q.AsOf))
	if err != nil {
		return nil, err
	}
	if q.Format != nil {
		switch *q.Format {
		case gen.MoneyGetFundBalancesReportParamsFormatCsv:
			b, err := rep.CSV()
			if err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetFundBalancesReport200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetFundBalancesReportParamsFormatJson:
		default:
			return nil, unsupportedFormat()
		}
	}
	out, err := conv[gen.FundBalancesReport](rep)
	return gen.MoneyGetFundBalancesReport200JSONResponse(out), err
}

func (s *apiServer) MoneyGetIncomeExpenseReport(ctx context.Context, req gen.MoneyGetIncomeExpenseReportRequestObject) (gen.MoneyGetIncomeExpenseReportResponseObject, error) {
	r, err := s.rp()
	if err != nil {
		return nil, err
	}
	q := req.Params
	rep, err := r.IncomeExpenseByAccount(ctx, req.OrgId, siteOf(q.SiteId), dateStr(&q.From), dateStr(&q.To), q.FundId)
	if err != nil {
		return nil, err
	}
	if q.Format != nil {
		switch *q.Format {
		case gen.MoneyGetIncomeExpenseReportParamsFormatCsv:
			b, err := rep.CSV()
			if err != nil {
				return nil, problem.Internal(err)
			}
			return gen.MoneyGetIncomeExpenseReport200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
		case gen.MoneyGetIncomeExpenseReportParamsFormatJson:
		default:
			return nil, unsupportedFormat()
		}
	}
	out, err := conv[gen.IncomeExpenseReport](rep)
	return gen.MoneyGetIncomeExpenseReport200JSONResponse(out), err
}

func (s *apiServer) MoneyGetMoneyOverview(ctx context.Context, req gen.MoneyGetMoneyOverviewRequestObject) (gen.MoneyGetMoneyOverviewResponseObject, error) {
	r, err := s.rp()
	if err != nil {
		return nil, err
	}
	ov, err := r.Overview(ctx, req.OrgId, siteOf(req.Params.SiteId))
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MoneyOverview](ov)
	return gen.MoneyGetMoneyOverview200JSONResponse(out), err
}

var _ = uuid.Nil
