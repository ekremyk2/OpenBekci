package bankimport_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func TestIntegrationUploadRefusesTooManyLines(t *testing.T) {
	w := newWorld(t)
	data := "booked_on,amount,counterparty,description,reference\n" + strings.Repeat("10.03.2026,\"1,00\",x,y,\n", bankimport.MaxLines+1)
	_, err := w.bank.Create(w.e.Ctx, w.e.OrgID, bankimport.CreateInput{SiteID: w.e.SiteID, Importer: bankimport.CoreCSV, AssetAccountID: w.acct, Filename: "s.csv", Data: []byte(data)})
	var pe *problem.Error
	if !errors.As(err, &pe) || pe.Status != 422 || len(pe.Fields) != 1 || pe.Fields[0].Code != "too_many_rows" {
		t.Fatalf("got %v", err)
	}
}
