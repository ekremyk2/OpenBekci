package httpapi

import (
	"context"
	"errors"

	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errTodo = errors.New("not implemented")

func (s *apiServer) MoneyListBills(_ context.Context, _ gen.MoneyListBillsRequestObject) (gen.MoneyListBillsResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyCreateBill(_ context.Context, _ gen.MoneyCreateBillRequestObject) (gen.MoneyCreateBillResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyGetBill(_ context.Context, _ gen.MoneyGetBillRequestObject) (gen.MoneyGetBillResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyCreateBillPayment(_ context.Context, _ gen.MoneyCreateBillPaymentRequestObject) (gen.MoneyCreateBillPaymentResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyListBudgets(_ context.Context, _ gen.MoneyListBudgetsRequestObject) (gen.MoneyListBudgetsResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyCreateBudget(_ context.Context, _ gen.MoneyCreateBudgetRequestObject) (gen.MoneyCreateBudgetResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyDeleteBudget(_ context.Context, _ gen.MoneyDeleteBudgetRequestObject) (gen.MoneyDeleteBudgetResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyGetBudget(_ context.Context, _ gen.MoneyGetBudgetRequestObject) (gen.MoneyGetBudgetResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyUpdateBudget(_ context.Context, _ gen.MoneyUpdateBudgetRequestObject) (gen.MoneyUpdateBudgetResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyGetBudgetLines(_ context.Context, _ gen.MoneyGetBudgetLinesRequestObject) (gen.MoneyGetBudgetLinesResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyPutBudgetLines(_ context.Context, _ gen.MoneyPutBudgetLinesRequestObject) (gen.MoneyPutBudgetLinesResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyFinalizeBudget(_ context.Context, _ gen.MoneyFinalizeBudgetRequestObject) (gen.MoneyFinalizeBudgetResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyListExpenses(_ context.Context, _ gen.MoneyListExpensesRequestObject) (gen.MoneyListExpensesResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyCreateExpense(_ context.Context, _ gen.MoneyCreateExpenseRequestObject) (gen.MoneyCreateExpenseResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyGetExpense(_ context.Context, _ gen.MoneyGetExpenseRequestObject) (gen.MoneyGetExpenseResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyVoidExpense(_ context.Context, _ gen.MoneyVoidExpenseRequestObject) (gen.MoneyVoidExpenseResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyGetPaymentReceipt(_ context.Context, _ gen.MoneyGetPaymentReceiptRequestObject) (gen.MoneyGetPaymentReceiptResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneySendPaymentReceipt(_ context.Context, _ gen.MoneySendPaymentReceiptRequestObject) (gen.MoneySendPaymentReceiptResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyListRefunds(_ context.Context, _ gen.MoneyListRefundsRequestObject) (gen.MoneyListRefundsResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyCreateRefund(_ context.Context, _ gen.MoneyCreateRefundRequestObject) (gen.MoneyCreateRefundResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyGetRefund(_ context.Context, _ gen.MoneyGetRefundRequestObject) (gen.MoneyGetRefundResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyGetBudgetVsActualReport(_ context.Context, _ gen.MoneyGetBudgetVsActualReportRequestObject) (gen.MoneyGetBudgetVsActualReportResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneySendStatement(_ context.Context, _ gen.MoneySendStatementRequestObject) (gen.MoneySendStatementResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyListTransfers(_ context.Context, _ gen.MoneyListTransfersRequestObject) (gen.MoneyListTransfersResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyCreateTransfer(_ context.Context, _ gen.MoneyCreateTransferRequestObject) (gen.MoneyCreateTransferResponseObject, error) {
	return nil, problem.Internal(errTodo)
}

func (s *apiServer) MoneyGetTransfer(_ context.Context, _ gen.MoneyGetTransferRequestObject) (gen.MoneyGetTransferResponseObject, error) {
	return nil, problem.Internal(errTodo)
}
