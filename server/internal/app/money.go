package app

import (
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/httpapi"
	"github.com/openbekci/openbekci/server/internal/platform/db"
)

// newMoney builds the money services (phase C) served by the HTTP API.
func newMoney(d *db.DB) httpapi.MoneyDeps {
	return httpapi.MoneyDeps{
		Ledger: ledger.NewService(d, nil),
	}
}
