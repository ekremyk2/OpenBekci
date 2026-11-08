package people

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MoveOutEvent is what the money module is told inside the move-out transaction.
type MoveOutEvent struct {
	OrgID, SiteID, MoveOutID, UnitID, PersonID uuid.UUID
	Side                                       string // owner or tenant: the side the person leaves
	AsOf                                       string // civil date, first day the person is gone
	OpenCharges                                string // carry_to_person, keep_on_unit or write_off
}

// MoveOutMoney is the money side's answer: what was carried (minor units).
type MoveOutMoney struct {
	CarriedMinor int64
	Currency     string
}

// MoneyHooks is implemented by the money module and registered in the wiring
// (SetMoneyHooks); people never imports money.
type MoneyHooks interface {
	OnMoveOut(ctx context.Context, tx pgx.Tx, e MoveOutEvent) (MoveOutMoney, error)
}

// SetMoneyHooks registers the money module's hooks (nil leaves open charges untouched).
func (s *Service) SetMoneyHooks(h MoneyHooks) { s.hooks = h }
