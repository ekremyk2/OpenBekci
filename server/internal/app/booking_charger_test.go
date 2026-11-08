package app

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

type chargeRow struct {
	typ, side, status string
	amount            int64
	unit              uuid.UUID
	person            *uuid.UUID
	desc              string
}

func TestBookingChargerChargesAndRefunds(t *testing.T) {
	w := seedNotifyWorld(t)
	authz.SetScopeLoader(tenancy.ScopeLoader{}) // as the server does at start
	t.Cleanup(func() { authz.SetScopeLoader(nil) })
	ch := bookingCharger{bill: billing.NewService(w.d, tenancy.NewService(w.d), nil)}
	ctx := context.Background()
	load := func(tx pgx.Tx, cid uuid.UUID) chargeRow {
		var r chargeRow
		if err := tx.QueryRow(ctx, `SELECT charge_type, debtor_kind, status, amount, unit_id, debtor_person_id, description FROM charge WHERE org_id = $1 AND id = $2`,
			w.org, cid).Scan(&r.typ, &r.side, &r.status, &r.amount, &r.unit, &r.person, &r.desc); err != nil {
			t.Fatalf("load charge: %v", err)
		}
		return r
	}
	charge := func(tx pgx.Tx, side string, minor int64) (uuid.UUID, error) {
		return ch.ChargeBooking(ctx, tx, community.BookingCharge{
			OrgID: w.org, SiteID: w.site, UnitID: w.unit, BookingID: id.New(), Side: side,
			Amount: community.Money{Amount: minor, Currency: "TRY"}, Description: "Tennis court, A-1",
		})
	}

	t.Run("the fee is a booking_fee charge on the booking side and is cancelled on refund", func(t *testing.T) {
		err := w.d.InTenantTx(ctx, w.org, func(tx pgx.Tx) error {
			cid, err := charge(tx, "tenant", 15000)
			if err != nil {
				return err
			}
			if cid == uuid.Nil {
				t.Fatal("no charge id")
			}
			got := load(tx, cid)
			if got.typ != "booking_fee" || got.side != "tenant" || got.amount != 15000 || got.unit != w.unit || got.status == "cancelled" || got.desc != "Tennis court, A-1" {
				t.Fatalf("charge = %+v", got)
			}
			if got.person == nil || *got.person != w.tenant {
				t.Fatalf("debtor person = %v, want the tenant %s", got.person, w.tenant)
			}
			if err := ch.RefundBooking(ctx, tx, community.BookingRefund{OrgID: w.org, SiteID: w.site, BookingID: id.New(), ChargeID: cid,
				Amount: community.Money{Amount: 15000, Currency: "TRY"}, Reason: "cancelled by the resident"}); err != nil {
				return err
			}
			if got := load(tx, cid); got.status != "cancelled" {
				t.Fatalf("after the refund the charge is %q, want cancelled", got.status)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a zero fee creates no charge", func(t *testing.T) {
		err := w.d.InTenantTx(ctx, w.org, func(tx pgx.Tx) error {
			cid, err := charge(tx, "owner", 0)
			if err != nil {
				return err
			}
			if cid != uuid.Nil {
				t.Fatalf("charge id %s for a zero fee", cid)
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM charge WHERE org_id = $1 AND charge_type = 'booking_fee' AND debtor_kind = 'owner'`, w.org).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Fatalf("%d owner booking_fee charges, want 0", n)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a fee in another currency than the site's is refused", func(t *testing.T) {
		err := w.d.InTenantTx(ctx, w.org, func(tx pgx.Tx) error {
			_, err := ch.ChargeBooking(ctx, tx, community.BookingCharge{OrgID: w.org, SiteID: w.site, UnitID: w.unit, BookingID: id.New(), Side: "owner",
				Amount: community.Money{Amount: 100, Currency: "EUR"}})
			return err
		})
		if err == nil {
			t.Fatal("want an error for a foreign currency")
		}
	})
}
