package ledger

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FixedTx is a TxRunner that runs every function inside one existing
// transaction. Services built on it (Service.WithTx of billing and payments)
// join a caller's transaction, so a bank line match and its payment commit together.
type FixedTx struct{ Tx pgx.Tx }

// InTenantTx runs fn in the fixed transaction (the caller owns tenant scoping and commit).
func (f FixedTx) InTenantTx(_ context.Context, _ uuid.UUID, fn func(pgx.Tx) error) error {
	return fn(f.Tx)
}
