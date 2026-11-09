// Package payments is the payments module (backend plan 4.6): cash and bank
// payments with gap-free receipt numbers, declared transfers with confirmation,
// allocation to open charges and credit (prepayment).
package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/paymentalloc"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Notification types emitted as River jobs for the notifications module.
const (
	NotifyPaymentConfirmed = "payment.confirmed"
	NotifyPaymentRejected  = "payment.rejected"
)

// Service is the payments module.
type Service struct {
	db       ledger.TxRunner
	Now      func() time.Time
	Notifier ledger.Notifier
	// OnRecorded and OnRefunded, when set, are called inside the transaction
	// after a payment was recorded (manually or through a provider) or refunded
	// through a provider; the packs module uses them to emit payment events.
	OnRecorded func(ctx context.Context, tx pgx.Tx, p Payment) error
	OnRefunded func(ctx context.Context, tx pgx.Tx, p Payment) error
}

// NewService returns a Service; now nil means time.Now, notifier nil drops notifications.
func NewService(db ledger.TxRunner, now func() time.Time, n ledger.Notifier) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, Now: now, Notifier: n}
}

func (s *Service) run(ctx context.Context, orgID uuid.UUID, what string, fn func(tx pgx.Tx) error) error {
	err := s.db.InTenantTx(ctx, orgID, fn)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("no such " + what)
	}
	return ledger.DBProblem(err, what)
}

func bad(field, code string) error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

// Allocation is the part of a payment applied to a charge (nil charge: credit).
type Allocation struct {
	ChargeID *uuid.UUID  `json:"charge_id"`
	Amount   money.Money `json:"amount"`
}

// Payment is a payment as the API shows it.
type Payment struct {
	ID             uuid.UUID    `json:"id"`
	OrgID          uuid.UUID    `json:"org_id"`
	SiteID         uuid.UUID    `json:"site_id"`
	PayerPersonID  *uuid.UUID   `json:"payer_person_id"`
	UnitID         *uuid.UUID   `json:"unit_id"`
	DebtorKind     *string      `json:"debtor_kind"`
	PersonID       *uuid.UUID   `json:"person_id"`
	Method         string       `json:"method"`
	Gross          money.Money  `json:"gross"`
	Fee            money.Money  `json:"fee"`
	Net            money.Money  `json:"net"`
	ReceivedOn     string       `json:"received_on"`
	RecordedBy     *uuid.UUID   `json:"recorded_by"`
	AssetAccountID uuid.UUID    `json:"asset_account_id"`
	Reference      *string      `json:"reference"`
	Status         string       `json:"status"`
	ReceiptNo      *string      `json:"receipt_no"`
	JournalEntryID *uuid.UUID   `json:"journal_entry_id"`
	Allocated      money.Money  `json:"allocated"`
	Unallocated    money.Money  `json:"unallocated"`
	Allocations    []Allocation `json:"allocations"`
	CreatedAt      time.Time    `json:"created_at"`
	// Version moves whenever the payment changes status; it is the If-Match value of a refund.
	Version int `json:"version"`
	// ProviderTxnID is the provider's transaction id of a card payment.
	ProviderTxnID *string `json:"provider_txn_id"`
}

const paySel = `SELECT p.id, p.org_id, p.site_id, p.payer_person_id, p.unit_id, p.debtor_kind, p.debtor_person_id, p.method, p.gross, p.fee, p.net, p.received_on::text, p.recorded_by,
	p.asset_account_id, p.reference, p.status, p.receipt_no, p.journal_entry_id, s.currency, p.created_at, p.version, p.provider_txn_id
	FROM payment p JOIN site s ON s.org_id = p.org_id AND s.id = p.site_id`

func scanPayment(r pgx.Row) (p Payment, err error) {
	var gross, fee, net int64
	var cur string
	err = r.Scan(&p.ID, &p.OrgID, &p.SiteID, &p.PayerPersonID, &p.UnitID, &p.DebtorKind, &p.PersonID, &p.Method, &gross, &fee, &net, &p.ReceivedOn, &p.RecordedBy,
		&p.AssetAccountID, &p.Reference, &p.Status, &p.ReceiptNo, &p.JournalEntryID, &cur, &p.CreatedAt, &p.Version, &p.ProviderTxnID)
	c := money.Code(cur)
	p.Gross, p.Fee, p.Net = money.New(gross, c), money.New(fee, c), money.New(net, c)
	p.Allocated, p.Unallocated = money.Zero(c), money.New(net, c)
	p.CreatedAt = p.CreatedAt.UTC()
	p.Allocations = []Allocation{}
	return
}

func loadAllocations(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, p *Payment) error {
	rows, err := tx.Query(ctx, `SELECT charge_id, amount FROM payment_allocation WHERE org_id=$1 AND payment_id=$2 AND NOT voided ORDER BY created_at, id`, orgID, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var sum int64
	for rows.Next() {
		var cid uuid.UUID
		var amt int64
		if err := rows.Scan(&cid, &amt); err != nil {
			return err
		}
		c := cid
		p.Allocations = append(p.Allocations, Allocation{ChargeID: &c, Amount: money.New(amt, p.Net.Currency)})
		sum += amt
	}
	if p.Status == "confirmed" {
		p.Allocated = money.New(sum, p.Gross.Currency)
		p.Unallocated = money.New(p.Gross.Amount-sum, p.Gross.Currency)
	}
	return rows.Err()
}

// Input records a payment.
type Input struct {
	SiteID         uuid.UUID      `json:"site_id"`
	Debtor         billing.Debtor `json:"debtor"`
	PayerPersonID  *uuid.UUID     `json:"payer_person_id"`
	Method         string         `json:"method"`
	Amount         money.Money    `json:"amount"`
	ReceivedOn     string         `json:"received_on"`
	AssetAccountID *uuid.UUID     `json:"asset_account_id"`
	Reference      *string        `json:"reference"`
	// ChargeIDs applies the payment only to these charges, in order (manual override).
	ChargeIDs []uuid.UUID `json:"charge_ids"`
}

func (in Input) validate(site tenancy.Site) error {
	if err := in.Debtor.Validate(); err != nil {
		return err
	}
	if in.Method != "cash" && in.Method != "bank_transfer" {
		return bad("method", "invalid_value")
	}
	if in.Amount.Amount <= 0 || in.Amount.Currency != ledger.Currency(site) {
		return bad("amount", "invalid_value")
	}
	if _, err := ledger.ParseDate(in.ReceivedOn); err != nil {
		return bad("received_on", "invalid_format")
	}
	return nil
}

// settle allocates a payment to the debtor's open items (or the listed
// charges), posts its entry and records the allocations and the receipt number.
func (s *Service) settle(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, site tenancy.Site, ch ledger.Chart, p *Payment, d billing.Debtor, manual []uuid.UUID, feeAccount uuid.UUID) ([]paymentalloc.Allocation, error) {
	tz := ledger.Location(site.Timezone)
	cur := ledger.Currency(site)
	// one posting at a time per debtor, and credit the debtor already holds goes against the open charges first
	if err := billing.LockOpenCharges(ctx, tx, orgID, site.ID, d); err != nil {
		return nil, err
	}
	if _, err := billing.ApplyCredit(ctx, tx, orgID, site, ch, d, ledger.Today(site.Timezone, s.Now())); err != nil {
		return nil, err
	}
	charges, err := billing.LoadCharges(ctx, tx, orgID, site.ID, d)
	if err != nil {
		return nil, err
	}
	items, err := billing.OpenItems(charges, cur, tz)
	if err != nil {
		return nil, err
	}
	order := paymentalloc.OldestFirstPrincipalBeforeFee
	if len(manual) > 0 {
		order = paymentalloc.Manual(manual)
	}
	res, err := paymentalloc.Allocate(p.Gross, items, order, tz) // the resident pays gross; a provider fee is booked below
	if err != nil {
		if errors.Is(err, paymentalloc.ErrUnknownCharge) {
			return nil, bad("charge_ids", "not_open")
		}
		return nil, problem.Wrap(422, problem.CodeValidationFailed, "the payment cannot be allocated", err)
	}
	fundOf := map[uuid.UUID]uuid.UUID{}
	var ids []uuid.UUID
	for _, a := range res.Allocations {
		ids = append(ids, a.ChargeID)
	}
	if len(ids) > 0 {
		rows, err := tx.Query(ctx, `SELECT id, fund_id FROM charge WHERE org_id=$1 AND id = ANY($2)`, orgID, ids)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var cid, f uuid.UUID
			if err := rows.Scan(&cid, &f); err != nil {
				rows.Close()
				return nil, err
			}
			fundOf[cid] = f
		}
		rows.Close()
	}
	recv := map[uuid.UUID]int64{}
	var order2 []uuid.UUID
	for _, a := range res.Allocations {
		f := fundOf[a.ChargeID]
		if _, ok := recv[f]; !ok {
			order2 = append(order2, f)
		}
		recv[f] += a.Amount.Amount
	}
	var lines []ledger.Line
	for _, f := range order2 {
		lines = append(lines,
			ledger.Line{AccountID: p.AssetAccountID, FundID: f, Debit: recv[f]},
			d.Line(ledger.Line{AccountID: ch.Account(d.ReceivableSubtype()), FundID: f, Credit: recv[f]}, nil))
	}
	if res.Credit.Amount > 0 {
		lines = append(lines,
			ledger.Line{AccountID: p.AssetAccountID, FundID: ch.OperatingFund, Debit: res.Credit.Amount},
			d.Line(ledger.Line{AccountID: ch.Account(ledger.SubPrepayment), FundID: ch.OperatingFund, Credit: res.Credit.Amount}, nil))
	}
	if p.Fee.Amount > 0 {
		// a provider keeps its fee: expense against the clearing account, so the clearing
		// account is left holding the net that the settlement will bring to the bank
		if feeAccount == uuid.Nil {
			return nil, bad("fee", "invalid_value")
		}
		lines = append(lines,
			ledger.Line{AccountID: feeAccount, FundID: ch.OperatingFund, Debit: p.Fee.Amount},
			ledger.Line{AccountID: p.AssetAccountID, FundID: ch.OperatingFund, Credit: p.Fee.Amount})
	}
	eid, err := ledger.PostEntry(ctx, tx, ledger.Entry{OrgID: orgID, SiteID: site.ID, EntryDate: p.ReceivedOn, Memo: "Payment", SourceType: "payment", SourceID: &p.ID, Lines: lines})
	if err != nil {
		return nil, err
	}
	year, _ := ledger.ParseDate(p.ReceivedOn)
	n, err := ledger.NextNumber(ctx, tx, orgID, site.ID, "receipt", year.Year())
	if err != nil {
		return nil, err
	}
	rno := fmt.Sprintf("%d-%06d", year.Year(), n)
	p.ReceiptNo, p.JournalEntryID, p.Status = &rno, &eid, "confirmed"
	return res.Allocations, nil
}

// saveAllocations stores the allocations of a payment (the row exists) and re-derives the charge statuses.
func saveAllocations(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, p Payment, allocs []paymentalloc.Allocation) error {
	var ids []uuid.UUID
	for _, a := range allocs {
		ids = append(ids, a.ChargeID)
		if _, err := tx.Exec(ctx, `INSERT INTO payment_allocation (id, org_id, site_id, payment_id, charge_id, amount, applied_on) VALUES ($1,$2,$3,$4,$5,$6,$7::date)`,
			id.New(), orgID, siteID, p.ID, a.ChargeID, a.Amount.Amount, p.ReceivedOn); err != nil {
			return err
		}
	}
	_, err := billing.RefreshStatus(ctx, tx, orgID, siteID, ids)
	return err
}

// checkAsset validates the asset account of a payment and the collector rule:
// a caller without money.manage (a kapici or board member bound to a cash box)
// may record only into their own cash box (M5).
func (s *Service) checkAsset(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, ch ledger.Chart, in *Input) (uuid.UUID, error) {
	acc := ch.Account(ledger.SubBank)
	if in.AssetAccountID != nil {
		acc = *in.AssetAccountID
	} else if in.Method == "cash" {
		return uuid.Nil, bad("asset_account_id", "required")
	}
	var typ, sub string
	if err := tx.QueryRow(ctx, `SELECT type, COALESCE(subtype,'') FROM account WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, in.SiteID, acc).Scan(&typ, &sub); err != nil {
		return uuid.Nil, bad("asset_account_id", "invalid_value")
	}
	if typ != "asset" || (in.Method == "cash" && sub != ledger.SubCashBox) || (in.Method == "bank_transfer" && sub != ledger.SubBank) {
		return uuid.Nil, bad("asset_account_id", "invalid_value")
	}
	if !ledger.HasSite(ctx, tx, ledger.PermMoneyManage, in.SiteID) {
		user := ledger.Actor(ctx)
		if user == nil {
			return uuid.Nil, problem.Forbidden("a cash collector needs a signed-in account")
		}
		own, ok, err := ledger.CollectorBox(ctx, tx, orgID, in.SiteID, *user)
		if err != nil {
			return uuid.Nil, err
		}
		if !ok || own != acc {
			return uuid.Nil, problem.Forbidden("you may record payments only into your own cash box")
		}
	}
	return acc, nil
}

// Record records and confirms a payment (payments.record): it is allocated
// oldest-first (principal before fee) or to the listed charges, the surplus
// becomes credit, and a gap-free receipt number is taken. Backdating
// (received_on in the past) is allowed down to the lock date.
func (s *Service) Record(ctx context.Context, orgID uuid.UUID, in Input) (Payment, error) {
	var out Payment
	err := s.run(ctx, orgID, "payment", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsRecord, in.SiteID); err != nil {
			return err
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, in.SiteID, true, false)
		if err != nil {
			return err
		}
		if err := in.validate(site); err != nil {
			return err
		}
		if in.ReceivedOn > ledger.Today(site.Timezone, s.Now()) {
			return bad("received_on", "in_the_future")
		}
		ch, err := ledger.EnsureChart(ctx, tx, orgID, in.SiteID)
		if err != nil {
			return err
		}
		asset, err := s.checkAsset(ctx, tx, orgID, ch, &in)
		if err != nil {
			return err
		}
		if err := checkDebtor(ctx, tx, orgID, in.SiteID, in.Debtor); err != nil {
			return err
		}
		pid := id.New()
		p := Payment{ID: pid, OrgID: orgID, SiteID: in.SiteID, PayerPersonID: in.PayerPersonID, Method: in.Method, ReceivedOn: in.ReceivedOn, AssetAccountID: asset, Reference: in.Reference,
			Gross: in.Amount, Fee: money.Zero(in.Amount.Currency), Net: in.Amount, RecordedBy: ledger.Actor(ctx)}
		allocs, err := s.settle(ctx, tx, orgID, site, ch, &p, in.Debtor, in.ChargeIDs, uuid.Nil)
		if err != nil {
			return err
		}
		if err := insertPayment(ctx, tx, p, in.Debtor); err != nil {
			return err
		}
		if err := saveAllocations(ctx, tx, orgID, in.SiteID, p, allocs); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "payment.record", "payment", p.ID, nil, map[string]any{"amount": in.Amount.Amount, "method": in.Method, "receipt_no": *p.ReceiptNo, "received_on": in.ReceivedOn}); err != nil {
			return err
		}
		if err := s.notify(ctx, tx, NotifyPaymentConfirmed, p); err != nil {
			return err
		}
		out, err = loadPayment(ctx, tx, orgID, p.ID)
		if err != nil {
			return err
		}
		if s.OnRecorded != nil {
			return s.OnRecorded(ctx, tx, out)
		}
		return nil
	})
	return out, err
}

func (s *Service) notify(ctx context.Context, tx pgx.Tx, typ string, p Payment) error {
	if s.Notifier == nil {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{"payment_id": p.ID, "unit_id": p.UnitID, "debtor_kind": p.DebtorKind, "person_id": p.PersonID, "amount": p.Net, "receipt_no": p.ReceiptNo})
	return s.Notifier.Enqueue(ctx, tx, ledger.Notification{Type: typ, OrgID: p.OrgID, SiteID: p.SiteID, Payload: payload})
}

func checkDebtor(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, d billing.Debtor) error {
	var n int
	var err error
	if d.IsPerson() {
		err = tx.QueryRow(ctx, `SELECT count(*) FROM person WHERE org_id=$1 AND id=$2`, orgID, *d.PersonID).Scan(&n)
	} else {
		err = tx.QueryRow(ctx, `SELECT count(*) FROM unit WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, siteID, *d.UnitID).Scan(&n)
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return bad("debtor", "invalid_value")
	}
	return nil
}

func insertPayment(ctx context.Context, tx pgx.Tx, p Payment, d billing.Debtor) error {
	var kind *string
	var unit *uuid.UUID
	if d.IsPerson() {
		k := "person"
		kind = &k
	} else {
		kind, unit = &d.Kind, d.UnitID
	}
	_, err := tx.Exec(ctx, `INSERT INTO payment (id, org_id, site_id, unit_id, debtor_kind, debtor_person_id, payer_person_id, method, gross, fee, net, received_on, recorded_by, asset_account_id, reference, status, receipt_no, journal_entry_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date,$13,$14,$15,$16,$17,$18)`,
		p.ID, p.OrgID, p.SiteID, unit, kind, d.PersonID, p.PayerPersonID, p.Method, p.Gross.Amount, p.Fee.Amount, p.Net.Amount, p.ReceivedOn, p.RecordedBy, p.AssetAccountID, p.Reference, p.Status, p.ReceiptNo, p.JournalEntryID)
	return err
}

func loadPayment(ctx context.Context, tx pgx.Tx, orgID, paymentID uuid.UUID) (Payment, error) {
	p, err := scanPayment(tx.QueryRow(ctx, paySel+` WHERE p.org_id=$1 AND p.id=$2`, orgID, paymentID))
	if err != nil {
		return p, err
	}
	return p, loadAllocations(ctx, tx, orgID, &p)
}
