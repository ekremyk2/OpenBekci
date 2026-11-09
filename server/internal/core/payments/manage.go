package payments

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// DeclareInput is a resident's "I have paid" for a bank transfer.
type DeclareInput struct {
	SiteID    uuid.UUID      `json:"site_id"`
	Debtor    billing.Debtor `json:"debtor"`
	Amount    money.Money    `json:"amount"`
	PaidOn    string         `json:"paid_on"`
	Reference *string        `json:"reference"`
}

// Declare records a declared bank transfer as pending_declared (payments.declare,
// the caller's own unit side only). Nothing is posted until a manager confirms it.
func (s *Service) Declare(ctx context.Context, orgID uuid.UUID, in DeclareInput) (Payment, error) {
	if in.Debtor.UnitID == nil || in.Debtor.IsPerson() || (in.Debtor.Kind != "owner" && in.Debtor.Kind != "tenant") {
		return Payment{}, bad("debtor", "invalid_value")
	}
	var out Payment
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsDeclare, in.SiteID); err != nil {
			return err
		}
		if !ledger.HasSite(ctx, tx, ledger.PermMoneyManage, in.SiteID) {
			if err := authz.CanAccessUnit(ctx, *in.Debtor.UnitID, authz.UnitSide(in.Debtor.Kind)); err != nil {
				return err
			}
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, in.SiteID, false, false)
		if err != nil {
			return err
		}
		if in.Amount.Amount <= 0 || in.Amount.Currency != ledger.Currency(site) {
			return bad("amount", "invalid_value")
		}
		if _, err := ledger.ParseDate(in.PaidOn); err != nil {
			return bad("paid_on", "invalid_format")
		}
		if in.PaidOn > ledger.Today(site.Timezone, s.Now()) {
			return bad("paid_on", "in_the_future")
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		if err := checkDebtor(ctx, tx, orgID, in.SiteID, in.Debtor); err != nil {
			return err
		}
		p := Payment{ID: id.New(), OrgID: orgID, SiteID: in.SiteID, Method: "bank_transfer", ReceivedOn: in.PaidOn, AssetAccountID: ch.Account(ledger.SubBank), Reference: in.Reference,
			Gross: in.Amount, Fee: money.Zero(in.Amount.Currency), Net: in.Amount, Status: "pending_declared", RecordedBy: ledger.Actor(ctx)}
		if err := insertPayment(ctx, tx, p, in.Debtor); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "payment.declare", "payment", p.ID, nil, map[string]any{"amount": in.Amount.Amount, "paid_on": in.PaidOn}); err != nil {
			return err
		}
		out, err = loadPayment(ctx, tx, orgID, p.ID)
		return err
	})
	return out, err
}

func debtorOf(p Payment) billing.Debtor {
	if p.DebtorKind != nil && *p.DebtorKind == "person" {
		return billing.Debtor{PersonID: p.PersonID}
	}
	k := ""
	if p.DebtorKind != nil {
		k = *p.DebtorKind
	}
	return billing.Debtor{UnitID: p.UnitID, Kind: k}
}

// Confirm settles a declared payment (payments.record): it is allocated and
// posted like a recorded payment, optionally with another received date.
func (s *Service) Confirm(ctx context.Context, orgID, paymentID uuid.UUID, assetAccount uuid.UUID, receivedOn string, chargeIDs []uuid.UUID) (Payment, error) {
	var out Payment
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		p, err := scanPayment(tx.QueryRow(ctx, paySel+` WHERE p.org_id=$1 AND p.id=$2 FOR UPDATE OF p`, orgID, paymentID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsRecord, p.SiteID); err != nil {
			return err
		}
		if p.Status != "pending_declared" {
			return problem.Conflict("only a declared payment can be confirmed")
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, p.SiteID, true, false)
		if err != nil {
			return err
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, p.SiteID)
		if err != nil {
			return err
		}
		if receivedOn != "" {
			if _, err := ledger.ParseDate(receivedOn); err != nil {
				return bad("received_on", "invalid_format")
			}
			p.ReceivedOn = receivedOn
		}
		if p.ReceivedOn > ledger.Today(site.Timezone, s.Now()) {
			return bad("received_on", "in_the_future")
		}
		if err := s.requireOwnBox(ctx, tx, orgID, p); err != nil {
			return err
		}
		if assetAccount != uuid.Nil && assetAccount != p.AssetAccountID {
			var typ, sub string
			if err := tx.QueryRow(ctx, `SELECT type, COALESCE(subtype,'') FROM account WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, p.SiteID, assetAccount).Scan(&typ, &sub); err != nil || typ != "asset" || sub != ledger.SubBank {
				return bad("asset_account_id", "invalid_value")
			}
			p.AssetAccountID = assetAccount
		}
		d := debtorOf(p)
		allocs, err := s.settle(ctx, tx, orgID, site, ch, &p, d, chargeIDs, uuid.Nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment SET status='confirmed', received_on=$3::date, receipt_no=$4, journal_entry_id=$5, recorded_by=$6, asset_account_id=$7 WHERE org_id=$1 AND id=$2`,
			orgID, paymentID, p.ReceivedOn, p.ReceiptNo, p.JournalEntryID, ledger.Actor(ctx), p.AssetAccountID); err != nil {
			return err
		}
		if err := saveAllocations(ctx, tx, orgID, p.SiteID, p, allocs); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, p.SiteID, "payment.confirm", "payment", paymentID, map[string]any{"status": "pending_declared"}, map[string]any{"status": "confirmed", "receipt_no": *p.ReceiptNo}); err != nil {
			return err
		}
		if err := s.notify(ctx, tx, NotifyPaymentConfirmed, p); err != nil {
			return err
		}
		out, err = loadPayment(ctx, tx, orgID, paymentID)
		return err
	})
	return out, err
}

// Reject turns a declared payment down with a reason (payments.record); the
// resident is told through a payment.rejected notification.
func (s *Service) Reject(ctx context.Context, orgID, paymentID uuid.UUID, reason string) (Payment, error) {
	if reason == "" {
		return Payment{}, bad("reason", "required")
	}
	var out Payment
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		p, err := scanPayment(tx.QueryRow(ctx, paySel+` WHERE p.org_id=$1 AND p.id=$2 FOR UPDATE OF p`, orgID, paymentID))
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsRecord, p.SiteID); err != nil {
			return err
		}
		if err := s.requireOwnBox(ctx, tx, orgID, p); err != nil {
			return err
		}
		if p.Status != "pending_declared" {
			return problem.Conflict("only a declared payment can be rejected")
		}
		if _, err := tx.Exec(ctx, `UPDATE payment SET status='voided' WHERE org_id=$1 AND id=$2`, orgID, paymentID); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, p.SiteID, "payment.reject", "payment", paymentID, map[string]any{"status": "pending_declared"}, map[string]any{"status": "voided", "reason": reason}); err != nil {
			return err
		}
		if s.Notifier != nil {
			payload, _ := json.Marshal(map[string]any{"payment_id": p.ID, "unit_id": p.UnitID, "debtor_kind": p.DebtorKind, "person_id": p.PersonID, "amount": p.Net, "reason": reason})
			if err := s.Notifier.Enqueue(ctx, tx, ledger.Notification{Type: NotifyPaymentRejected, OrgID: orgID, SiteID: p.SiteID, Payload: payload}); err != nil {
				return err
			}
		}
		out, err = loadPayment(ctx, tx, orgID, paymentID)
		return err
	})
	return out, err
}

// Void reverses a confirmed payment (payments.record; a collector only in their own cash box): its entry is reversed dated
// today, its allocations are voided and the charges re-derive their status.
func (s *Service) Void(ctx context.Context, orgID, paymentID uuid.UUID, reason string) (Payment, error) {
	return s.reverse(ctx, orgID, paymentID, reason, "voided", nil)
}

// reverse is Void and the provider refund: final is the status the payment ends in (voided or refunded).
func (s *Service) reverse(ctx context.Context, orgID, paymentID uuid.UUID, reason, final string, refundRef *string) (Payment, error) {
	var out Payment
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		p, err := scanPayment(tx.QueryRow(ctx, paySel+` WHERE p.org_id=$1 AND p.id=$2 FOR UPDATE OF p`, orgID, paymentID))
		if err != nil {
			return err
		}
		site, err := ledger.SiteFor(ctx, tx, ledger.PermPaymentsRecord, orgID, p.SiteID)
		if err != nil {
			return err
		}
		if !ledger.HasSite(ctx, tx, ledger.PermMoneyManage, p.SiteID) {
			user := ledger.Actor(ctx)
			own := uuid.Nil
			if user != nil {
				own, _, _ = ledger.CollectorBox(ctx, tx, orgID, p.SiteID, *user)
			}
			if own != p.AssetAccountID {
				return problem.Forbidden("you may void payments only in your own cash box")
			}
		}
		if p.Status == final {
			out, err = loadPayment(ctx, tx, orgID, paymentID)
			return err
		}
		if p.Status != "confirmed" || p.JournalEntryID == nil {
			return problem.Conflict("only a confirmed payment can be voided")
		}
		if err := billing.LockOpenCharges(ctx, tx, orgID, p.SiteID, debtorOf(p)); err != nil {
			return err
		}
		today := ledger.Today(site.Timezone, s.Now())
		// allocations a cancelled charge already gave back as credit: the reversal below would
		// debit the receivable for them again, so move that credit back first
		chart, err := ledger.EnsureChart(ctx, tx, orgID, p.SiteID)
		if err != nil {
			return err
		}
		given, err := tx.Query(ctx, `SELECT c.fund_id, SUM(a.amount) FROM payment_allocation a JOIN charge c ON c.org_id = a.org_id AND c.id = a.charge_id
			WHERE a.org_id=$1 AND a.payment_id=$2 AND a.voided GROUP BY c.fund_id`, orgID, paymentID)
		if err != nil {
			return err
		}
		type back struct {
			fund uuid.UUID
			amt  int64
		}
		var backs []back
		for given.Next() {
			var b back
			if err := given.Scan(&b.fund, &b.amt); err != nil {
				given.Close()
				return err
			}
			backs = append(backs, b)
		}
		given.Close()
		for _, b := range backs {
			if err := billing.MoveCreditToReceivable(ctx, tx, orgID, p.SiteID, chart, debtorOf(p), b.fund, b.amt, today, "Payment voided: credit of a cancelled charge returned"); err != nil {
				return err
			}
		}
		rid, err := ledger.Reverse(ctx, tx, orgID, p.SiteID, *p.JournalEntryID, today, "Payment voided: "+reason)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE payment_allocation SET voided = true WHERE org_id=$1 AND payment_id=$2 AND NOT voided RETURNING charge_id`, orgID, paymentID)
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var cid uuid.UUID
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, cid)
		}
		rows.Close()
		if _, err := tx.Exec(ctx, `UPDATE payment SET status=$4, void_entry_id=$3, refund_ref=$5 WHERE org_id=$1 AND id=$2`, orgID, paymentID, rid, final, refundRef); err != nil {
			return err
		}
		if _, err := billing.RefreshStatus(ctx, tx, orgID, p.SiteID, ids); err != nil {
			return err
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, p.SiteID)
		if err != nil {
			return err
		}
		if credit, err := billing.CreditOf(ctx, tx, orgID, p.SiteID, ch, debtorOf(p)); err != nil {
			return err
		} else if credit < 0 {
			return problem.Conflict("the surplus of this payment has already been applied to later charges; cancel those charges first")
		}
		if err := ledger.Audit(ctx, tx, orgID, p.SiteID, "payment."+map[string]string{"voided": "void", "refunded": "refund"}[final], "payment", paymentID, map[string]any{"status": "confirmed"}, map[string]any{"status": final, "reason": reason}); err != nil {
			return err
		}
		out, err = loadPayment(ctx, tx, orgID, paymentID)
		if err != nil {
			return err
		}
		if final == "refunded" && s.OnRefunded != nil {
			return s.OnRefunded(ctx, tx, out)
		}
		return nil
	})
	return out, err
}

// Filter filters List.
type Filter struct {
	UnitID     *uuid.UUID
	DebtorKind string
	PersonID   *uuid.UUID
	Status     string
	Method     string
	ReceiptNo  string
	From, To   string
}

// List lists payments (money.read; a resident gets their own unit side only).
func (s *Service) List(ctx context.Context, orgID, siteID uuid.UUID, f Filter, p ledger.PageParams) ([]Payment, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Payment
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := tenancy.LoadSite(ctx, tx, orgID, siteID, false, true); err != nil {
			return err
		}
		if !ledger.HasSite(ctx, tx, ledger.PermMoneyRead, siteID) {
			if err := billing.OwnSide(ctx, f.UnitID, f.DebtorKind); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, paySel+` WHERE p.org_id=$1 AND p.site_id=$2 AND p.id > $3
			AND ($4::uuid IS NULL OR p.unit_id = $4) AND ($5 = '' OR p.debtor_kind = $5) AND ($6::uuid IS NULL OR p.debtor_person_id = $6)
			AND ($7 = '' OR p.status = $7) AND ($8 = '' OR p.method = $8) AND ($9 = '' OR p.receipt_no = $9)
			AND ($10 = '' OR p.received_on >= $10::date) AND ($11 = '' OR p.received_on <= $11::date) ORDER BY p.id LIMIT $12`,
			orgID, siteID, after, f.UnitID, f.DebtorKind, f.PersonID, f.Status, f.Method, f.ReceiptNo, f.From, f.To, limit+1)
		if err != nil {
			return err
		}
		for rows.Next() {
			pm, err := scanPayment(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out = append(out, pm)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range out {
			if err := loadAllocations(ctx, tx, orgID, &out[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(p Payment) uuid.UUID { return p.ID })
	return items, next, nil
}

// Get returns a payment with its allocations (money.read, or the caller's own unit side).
func (s *Service) Get(ctx context.Context, orgID, paymentID uuid.UUID) (Payment, error) {
	var p Payment
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) (err error) {
		if p, err = loadPayment(ctx, tx, orgID, paymentID); err != nil {
			return err
		}
		if ledger.HasSite(ctx, tx, ledger.PermMoneyRead, p.SiteID) {
			return nil
		}
		if p.UnitID == nil || p.DebtorKind == nil {
			return problem.NotFound("no such payment")
		}
		if authz.CanAccessUnit(ctx, *p.UnitID, authz.UnitSide(*p.DebtorKind)) != nil {
			return problem.NotFound("no such payment")
		}
		return nil
	})
	return p, err
}

// requireOwnBox lets a collector without money.manage settle or turn down only the
// declared transfers into their own cash box (M5); a declared bank transfer
// belongs to the bank account, so it needs money.manage.
func (s *Service) requireOwnBox(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, p Payment) error {
	if ledger.HasSite(ctx, tx, ledger.PermMoneyManage, p.SiteID) {
		return nil
	}
	user := ledger.Actor(ctx)
	if user == nil {
		return problem.Forbidden("a signed-in account is needed")
	}
	own, ok, err := ledger.CollectorBox(ctx, tx, orgID, p.SiteID, *user)
	if err != nil {
		return err
	}
	if !ok || own != p.AssetAccountID {
		return problem.Forbidden("you may settle only payments of your own cash box")
	}
	return nil
}

// GetForReceipt returns a payment to whoever may read its receipt: holders of money.read or payments.record
// at the site (a collector sees the payments of the building), or the resident of the unit side it was paid for.
func (s *Service) GetForReceipt(ctx context.Context, orgID, paymentID uuid.UUID) (Payment, error) {
	var p Payment
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) (err error) {
		if p, err = loadPayment(ctx, tx, orgID, paymentID); err != nil {
			return err
		}
		if ledger.HasSite(ctx, tx, ledger.PermMoneyRead, p.SiteID) || ledger.HasSite(ctx, tx, ledger.PermPaymentsRecord, p.SiteID) {
			return nil
		}
		if p.UnitID == nil || p.DebtorKind == nil || authz.CanAccessUnit(ctx, *p.UnitID, authz.UnitSide(*p.DebtorKind)) != nil {
			return problem.NotFound("no such payment")
		}
		return nil
	})
	return p, err
}
