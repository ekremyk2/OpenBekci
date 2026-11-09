// Package checkout is the provider-agnostic card checkout (release 0.4): a
// resident or a manager starts a hosted checkout for a debtor side, the payment
// provider of an enabled pack returns the checkout_url, and the provider's
// webhook (or the client's status poll) completes it through the payments
// module. The provider is reached through the Providers interface (the packs
// host); this package knows nothing of any provider.
//
// State machine of a checkout:
//
//	pending -> paid       the provider confirmed the money (webhook, or the status poll found it paid)
//	pending -> failed     the provider reported a failure, or could not create the checkout
//	pending -> expired    expires_at passed (the checkout.expire job, or lazily by the status poll)
//	expired, failed -> paid   a late confirmation still records the payment: the money moved
//	paid -> refunded      a manager refund, or a refund the provider reported
package checkout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Permission lets a payer (the owner, tenant or a household member the owner or tenant allowed to see money) and
// the managers of a site start a card checkout. Declared with the money permissions.
const PermCheckout = ledger.PermPaymentsCheckout

// Checkout statuses.
const (
	StatusPending  = "pending"
	StatusPaid     = "paid"
	StatusFailed   = "failed"
	StatusExpired  = "expired"
	StatusRefunded = "refunded"
)

// Clients choose the return URL.
const (
	ClientWeb    = "web"
	ClientMobile = "mobile"
)

// DefaultTTL is how long a checkout stays open when the provider names no expiry.
const DefaultTTL = time.Hour

// maxTTL caps the expiry a provider names.
const maxTTL = 24 * time.Hour

// pollEvery limits how often a status poll asks the provider.
const pollEvery = 5 * time.Second

// ProviderInfo names an enabled payment provider.
type ProviderInfo struct{ Pack, Provider string }

// Providers is the payment provider side of the packs host.
type Providers interface {
	// Providers lists the payment providers of the packs enabled for the organization, by pack and provider id.
	Providers(ctx context.Context, org uuid.UUID) ([]ProviderInfo, error)
	CreateCheckout(ctx context.Context, org uuid.UUID, pack, provider string, req sdk.CheckoutRequest) (sdk.Checkout, error)
	PaymentStatus(ctx context.Context, org uuid.UUID, pack, provider, txn string) (sdk.PaymentStatus, error)
	RefundPayment(ctx context.Context, org uuid.UUID, pack, provider string, req sdk.RefundRequest) (sdk.RefundOutcome, error)
	SyncSettlements(ctx context.Context, org uuid.UUID, pack, provider string, site sdk.ID, req sdk.SettlementRequest) (int, error)
}

// ReceiptFiler files the receipt PDF of a payment (moneydocs).
type ReceiptFiler interface {
	FileReceipt(ctx context.Context, org, payment uuid.UUID) error
}

// PlatformTx runs a function across organizations (the periodic jobs).
type PlatformTx interface {
	InPlatformTx(ctx context.Context, fn func(pgx.Tx) error) error
}

// Config configures Service.
type Config struct {
	DB         ledger.TxRunner
	Platform   PlatformTx
	Payments   *payments.Service
	Providers  Providers
	Receipts   ReceiptFiler
	BaseURL    string
	Log        *slog.Logger
	Now        func() time.Time
	DefaultTTL time.Duration
}

// Service is the checkout module.
type Service struct {
	cfg Config
}

// NewService returns a Service.
func NewService(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.DefaultTTL <= 0 {
		cfg.DefaultTTL = DefaultTTL
	}
	return &Service{cfg: cfg}
}

// Checkout is a checkout as the API shows it.
type Checkout struct {
	ID               uuid.UUID   `json:"id"`
	OrgID            uuid.UUID   `json:"org_id"`
	SiteID           uuid.UUID   `json:"site_id"`
	UnitID           uuid.UUID   `json:"unit_id"`
	DebtorKind       string      `json:"debtor_kind"`
	ChargeIDs        []uuid.UUID `json:"charge_ids"`
	Provider         string      `json:"provider"`
	MerchantOfRecord string      `json:"merchant_of_record"`
	Amount           money.Money `json:"amount"`
	Status           string      `json:"status"`
	CheckoutURL      *string     `json:"checkout_url"`
	ReturnURL        string      `json:"return_url"`
	ExpiresAt        time.Time   `json:"expires_at"`
	PaymentID        *uuid.UUID  `json:"payment_id"`
	FailureCode      *string     `json:"failure_code"`
	CreatedBy        *uuid.UUID  `json:"created_by"`
	CreatedAt        time.Time   `json:"created_at"`
	CompletedAt      *time.Time  `json:"completed_at"`
	// AmountMismatch is set when the provider confirmed another gross than the checkout asked for; the payment
	// holds ConfirmedAmount (what the provider confirmed), not Amount.
	AmountMismatch  bool         `json:"amount_mismatch"`
	ConfirmedAmount *money.Money `json:"confirmed_amount"`

	providerTxn *string
	lastPolled  *time.Time
}

const cols = `id, org_id, site_id, unit_id, debtor_kind, charge_ids, provider, merchant_of_record, amount, currency, status, checkout_url, return_url,
	expires_at, payment_id, failure_code, created_by, created_at, completed_at, provider_txn_id, last_polled_at, amount_mismatch, confirmed_amount`

func scan(r pgx.Row) (Checkout, error) {
	var c Checkout
	var amount int64
	var cur string
	var confirmed *int64
	err := r.Scan(&c.ID, &c.OrgID, &c.SiteID, &c.UnitID, &c.DebtorKind, &c.ChargeIDs, &c.Provider, &c.MerchantOfRecord, &amount, &cur, &c.Status, &c.CheckoutURL, &c.ReturnURL,
		&c.ExpiresAt, &c.PaymentID, &c.FailureCode, &c.CreatedBy, &c.CreatedAt, &c.CompletedAt, &c.providerTxn, &c.lastPolled, &c.AmountMismatch, &confirmed)
	if err != nil {
		return c, err
	}
	c.Amount = money.New(amount, money.Code(cur))
	if confirmed != nil {
		m := money.New(*confirmed, money.Code(cur))
		c.ConfirmedAmount = &m
	}
	c.CreatedAt, c.ExpiresAt = c.CreatedAt.UTC(), c.ExpiresAt.UTC()
	if c.CompletedAt != nil {
		t := c.CompletedAt.UTC()
		c.CompletedAt = &t
	}
	if c.ChargeIDs == nil {
		c.ChargeIDs = []uuid.UUID{}
	}
	return c, nil
}

func bad(field, code string) error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

func (s *Service) run(ctx context.Context, org uuid.UUID, what string, fn func(tx pgx.Tx) error) error {
	err := s.cfg.DB.InTenantTx(ctx, org, fn)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return problem.NotFound("no such " + what)
	}
	return ledger.DBProblem(err, what)
}

// ReturnURL is where the provider sends the payer back to: the web app's pay return page, or the mobile app's
// openbekci://pay/return, chosen by client like the link formats of the emailed links.
func (s *Service) ReturnURL(client string, org, checkout uuid.UUID) string {
	if client == ClientMobile {
		return "openbekci://pay/return?org_id=" + org.String() + "&checkout_id=" + checkout.String()
	}
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/orgs/" + org.String() + "/pay/return?checkout_id=" + url.QueryEscape(checkout.String())
}

// CreateInput starts a checkout.
type CreateInput struct {
	UnitID     uuid.UUID
	DebtorKind string
	// ChargeIDs are the charges to pay; empty means oldest first.
	ChargeIDs []uuid.UUID
	// Amount is the gross amount to pay. With charges it defaults to what is open on them; without, to everything
	// open on the unit side.
	Amount *money.Money
	// Client is ClientWeb (default) or ClientMobile.
	Client string
	// IdempotencyKey is the Idempotency-Key of the request. A retry with the same key and the same request returns
	// the same checkout (and never asks the provider for a second hosted checkout), even when the first attempt
	// failed at the provider; the same key with another request is 422 idempotency_mismatch.
	IdempotencyKey string
}

// requestHash identifies the request of a checkout create for the idempotency check: what the caller sent, not
// what the server derived from it.
func (in CreateInput) requestHash() string {
	ids := make([]string, 0, len(in.ChargeIDs))
	for _, c := range in.ChargeIDs {
		ids = append(ids, c.String())
	}
	sort.Strings(ids)
	amount := "-"
	if in.Amount != nil {
		amount = fmt.Sprintf("%d %s", in.Amount.Amount, in.Amount.Currency)
	}
	client := in.Client
	if client == "" {
		client = ClientWeb
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{in.UnitID.String(), in.DebtorKind, strings.Join(ids, ","), amount, client}, "|")))
	return hex.EncodeToString(sum[:])
}

type plan struct {
	site      tenancy.Site
	amount    money.Money
	mor       string
	preferred string
	locale    string
	payer     sdk.Payer
}

// settingsOf reads the organization's settings document and its default locale.
func orgSettings(ctx context.Context, tx pgx.Tx, org uuid.UUID) (map[string]any, string, error) {
	var raw []byte
	var locale string
	if err := tx.QueryRow(ctx, `SELECT settings, default_locale FROM organization WHERE id = $1`, org).Scan(&raw, &locale); err != nil {
		return nil, "", err
	}
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, "", err
		}
	}
	return m, locale, nil
}

// Create starts a checkout for a unit side: it validates that the caller may pay that side (a manager of the
// site may pay for any; a resident only through a current occupancy that reaches the side's money), picks the
// enabled provider (409 payment_provider_unavailable when none), asks it for the hosted checkout and returns the
// pending checkout with its checkout_url.
func (s *Service) Create(ctx context.Context, org uuid.UUID, in CreateInput) (Checkout, error) {
	if in.UnitID == uuid.Nil {
		return Checkout{}, bad("unit_id", "required")
	}
	if in.DebtorKind != "owner" && in.DebtorKind != "tenant" {
		return Checkout{}, bad("debtor_kind", "invalid_value")
	}
	if in.Client != "" && in.Client != ClientWeb && in.Client != ClientMobile {
		return Checkout{}, bad("client", "invalid_value")
	}
	if in.Amount != nil && in.Amount.Amount <= 0 {
		return Checkout{}, bad("amount", "invalid_value")
	}
	if len(in.ChargeIDs) > 200 {
		return Checkout{}, bad("charge_ids", "too_many")
	}
	var p plan
	err := s.run(ctx, org, "unit", func(tx pgx.Tx) error {
		var siteID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT site_id FROM unit WHERE org_id = $1 AND id = $2`, org, in.UnitID).Scan(&siteID); err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsCheckout, siteID); err != nil {
			return err
		}
		if !ledger.HasSite(ctx, tx, ledger.PermMoneyManage, siteID) {
			if err := authz.CanAccessUnit(ctx, in.UnitID, authz.UnitSide(in.DebtorKind)); err != nil {
				return err
			}
		}
		site, err := tenancy.LoadSite(ctx, tx, org, siteID, false, false)
		if err != nil {
			return err
		}
		p.site = site
		cur := ledger.Currency(site)
		d := billing.Debtor{UnitID: &in.UnitID, Kind: in.DebtorKind}
		charges, err := billing.LoadCharges(ctx, tx, org, siteID, d)
		if err != nil {
			return err
		}
		items, err := billing.OpenItems(charges, cur, ledger.Location(site.Timezone))
		if err != nil {
			return err
		}
		open := map[uuid.UUID]int64{}
		var total int64
		for _, it := range items {
			open[it.ChargeID] = it.Remaining.Amount
			total += it.Remaining.Amount
		}
		var selected int64
		seen := map[uuid.UUID]bool{}
		for _, c := range in.ChargeIDs {
			rem, ok := open[c]
			if !ok || seen[c] {
				return bad("charge_ids", "not_open")
			}
			seen[c] = true
			selected += rem
		}
		switch {
		case in.Amount != nil:
			if in.Amount.Currency != cur {
				return bad("amount", "invalid_value")
			}
			p.amount = *in.Amount
		case len(in.ChargeIDs) > 0:
			p.amount = money.New(selected, cur)
		default:
			p.amount = money.New(total, cur)
		}
		if p.amount.Amount <= 0 {
			return bad("amount", "required")
		}
		orgS, locale, err := orgSettings(ctx, tx, org)
		if err != nil {
			return err
		}
		p.locale = locale
		p.mor = tenancy.EffectiveMerchantOfRecord(orgS, site.Settings)
		p.preferred = tenancy.EffectivePaymentProvider(orgS, site.Settings)
		if u := ledger.Actor(ctx); u != nil {
			var name string
			var email, phone *string
			err := tx.QueryRow(ctx, `SELECT display_name, contact->>'email', contact->>'phone' FROM person WHERE org_id = $1 AND user_id = $2`, org, *u).Scan(&name, &email, &phone)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			p.payer = sdk.Payer{Name: name, Email: deref(email), Phone: deref(phone)}
		}
		return nil
	})
	if err != nil {
		return Checkout{}, err
	}
	info, err := s.pick(ctx, org, p.preferred)
	if err != nil {
		return Checkout{}, err
	}
	if s.cfg.Providers == nil {
		return Checkout{}, unavailable()
	}
	now := s.cfg.Now().UTC()
	cid := id.New()
	client := in.Client
	if client == "" {
		client = ClientWeb
	}
	expires := now.Add(s.cfg.DefaultTTL)
	chargeIDs := in.ChargeIDs
	if chargeIDs == nil {
		chargeIDs = []uuid.UUID{}
	}
	actor := ledger.Actor(ctx)
	hash := in.requestHash()
	var replay *Checkout
	err = s.run(ctx, org, "checkout", func(tx pgx.Tx) error {
		replay = nil
		keyed := in.IdempotencyKey != "" && actor != nil
		if keyed {
			// serialize the attempts of one key, then look for an earlier one
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 12))`, fmt.Sprintf("checkout|%s|%s|%s", org, *actor, in.IdempotencyKey)); err != nil {
				return err
			}
			prev, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM checkout WHERE org_id = $1 AND created_by = $2 AND idempotency_key = $3`, org, *actor, in.IdempotencyKey))
			switch {
			case err == nil:
				var storedHash *string
				if err := tx.QueryRow(ctx, `SELECT request_hash FROM checkout WHERE org_id = $1 AND id = $2`, org, prev.ID).Scan(&storedHash); err != nil {
					return err
				}
				if storedHash == nil || *storedHash != hash {
					return problem.New(422, problem.CodeIdempotencyMismatch, "this Idempotency-Key was already used with a different request")
				}
				if prev.providerTxn != nil || (prev.Status != StatusPending && prev.Status != StatusFailed) {
					replay = &prev
					return nil
				}
				// the provider never answered: ask again under the same reference, so that a checkout the provider
				// did create is the one we get back
				cid = prev.ID
				_, err = tx.Exec(ctx, `UPDATE checkout SET status = 'pending', failure_code = NULL, completed_at = NULL, expires_at = $3 WHERE org_id = $1 AND id = $2`, org, cid, expires)
				return err
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			}
		}
		var key, h *string
		if keyed {
			key, h = &in.IdempotencyKey, &hash
		}
		_, err := tx.Exec(ctx, `INSERT INTO checkout (id, org_id, site_id, unit_id, debtor_kind, charge_ids, provider, merchant_of_record, amount, currency, return_url, created_by, created_at, expires_at, idempotency_key, request_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			cid, org, p.site.ID, in.UnitID, in.DebtorKind, chargeIDs, info.Provider, p.mor, p.amount.Amount, string(p.amount.Currency), s.ReturnURL(client, org, cid), actor, now, expires, key, h)
		return err
	})
	if err != nil {
		return Checkout{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	returnURL := s.ReturnURL(client, org, cid)
	target := sdk.PaymentTarget{SiteID: sdk.ID(p.site.ID.String()), UnitID: sdk.ID(in.UnitID.String()), DebtorKind: in.DebtorKind}
	for _, c := range in.ChargeIDs {
		target.ChargeIDs = append(target.ChargeIDs, sdk.ID(c.String()))
	}
	res, perr := s.cfg.Providers.CreateCheckout(ctx, org, info.Pack, info.Provider, sdk.CheckoutRequest{
		Reference: cid.String(), Gross: sdk.Money{Amount: p.amount.Amount, Currency: string(p.amount.Currency)}, Target: target,
		ReturnURL: returnURL, Locale: p.locale, Payer: p.payer, MerchantOfRecord: p.mor,
	})
	if perr == nil && (res.ProviderTxnID == "" || res.CheckoutURL == "") {
		perr = problem.Wrap(502, problem.CodePackError, "the payment provider returned no checkout", errors.New("empty checkout"))
	}
	if perr != nil {
		_ = s.run(ctx, org, "checkout", func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE checkout SET status = 'failed', failure_code = 'provider_error', completed_at = now() WHERE org_id = $1 AND id = $2 AND status = 'pending'`, org, cid)
			return err
		})
		return Checkout{}, providerProblem(perr)
	}
	if res.ExpiresAt != nil && res.ExpiresAt.After(now) {
		expires = res.ExpiresAt.UTC()
		if expires.After(now.Add(maxTTL)) {
			expires = now.Add(maxTTL)
		}
	}
	var out Checkout
	err = s.run(ctx, org, "checkout", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE checkout SET provider_txn_id = $3, checkout_url = $4, expires_at = $5 WHERE org_id = $1 AND id = $2`, org, cid, res.ProviderTxnID, res.CheckoutURL, expires); err != nil {
			return err
		}
		out, err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM checkout WHERE org_id = $1 AND id = $2`, org, cid))
		if err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, org, p.site.ID, "checkout.create", "checkout", cid, nil, map[string]any{
			"provider": info.Provider, "amount": p.amount.Amount, "unit_id": in.UnitID.String(), "debtor_kind": in.DebtorKind, "merchant_of_record": p.mor})
	})
	return out, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// providerProblem turns an error of a provider call into a problem with a stable code and a fixed message. What
// the provider (or the pack) said is kept as the cause for the log and never reaches the client: a provider's
// words can name its account, its credentials or its internals. Problems the core raised itself (a reversal that
// the ledger refused) pass through.
func providerProblem(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return problem.Wrap(504, problem.CodeTimeout, "the payment provider took too long", err)
	case errors.Is(err, sdk.ErrUnavailable):
		return problem.Wrap(503, problem.CodePackError, "the payment provider is temporarily unavailable", err)
	case errors.Is(err, sdk.ErrCredentials):
		return problem.Wrap(409, problem.CodePaymentProviderUnavailable, "card payment is not available for this site", err)
	case errors.Is(err, sdk.ErrRejected), errors.Is(err, sdk.ErrUnsupported):
		return problem.Wrap(422, problem.CodeValidationFailed, "the payment provider did not accept the request", err)
	}
	var pe *problem.Error
	if errors.As(err, &pe) && pe.Status < 500 && !isPackError(pe) {
		return err
	}
	return problem.Wrap(502, problem.CodePackError, "the payment provider failed", err)
}

func isPackError(pe *problem.Error) bool { return pe.Code == problem.CodePackError }

func unavailable() error {
	return problem.New(409, problem.CodePaymentProviderUnavailable, "no card payment provider is enabled for this site")
}

// pick chooses the provider checkout uses: the one the settings name when it is enabled, else the first enabled.
func (s *Service) pick(ctx context.Context, org uuid.UUID, preferred string) (ProviderInfo, error) {
	if s.cfg.Providers == nil {
		return ProviderInfo{}, unavailable()
	}
	list, err := s.cfg.Providers.Providers(ctx, org)
	if err != nil {
		return ProviderInfo{}, err
	}
	for _, p := range list {
		if preferred == "" || p.Provider == preferred {
			return p, nil
		}
	}
	return ProviderInfo{}, unavailable()
}

// SiteView is what a site's settings response says about card payments.
type SiteView struct {
	// CardAvailable says whether "Pay by card" exists for the site; Provider is the provider checkout would use.
	CardAvailable bool
	Provider      string
	// MerchantOfRecord is the effective merchant of record; Override is the site's own setting ("" when it follows
	// the organization).
	MerchantOfRecord string
	Override         string
}

// SiteViewOf answers, for the settings of a site, whether card checkout is available (a payment provider of an
// enabled pack exists for it) and who the merchant of record is. The caller must already be allowed to read the site.
func (s *Service) SiteViewOf(ctx context.Context, org, site uuid.UUID) (SiteView, error) {
	var v SiteView
	var preferred string
	err := s.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT settings FROM site WHERE org_id = $1 AND id = $2`, org, site).Scan(&raw); err != nil {
			return err
		}
		siteS := map[string]any{}
		_ = json.Unmarshal(raw, &siteS)
		orgS, _, err := orgSettings(ctx, tx, org)
		if err != nil {
			return err
		}
		preferred = tenancy.EffectivePaymentProvider(orgS, siteS)
		v.MerchantOfRecord = tenancy.EffectiveMerchantOfRecord(orgS, siteS)
		if o, ok := siteS[tenancy.SettingMerchantOfRecord].(string); ok && tenancy.ValidMerchantOfRecord(o) {
			v.Override = o
		}
		return nil
	})
	if err != nil {
		return v, err
	}
	info, err := s.pick(ctx, org, preferred)
	if err != nil {
		var pe *problem.Error
		if errors.As(err, &pe) && pe.Code == problem.CodePaymentProviderUnavailable {
			return v, nil
		}
		return v, err
	}
	v.CardAvailable, v.Provider = true, info.Provider
	return v, nil
}

// access reports whether the caller may see the checkout: its creator, a manager who reads the site's money, or a
// resident who reaches the unit side.
func (s *Service) access(ctx context.Context, tx pgx.Tx, c Checkout) error {
	if u := ledger.Actor(ctx); u != nil && c.CreatedBy != nil && *u == *c.CreatedBy {
		return nil
	}
	if ledger.HasSite(ctx, tx, ledger.PermMoneyRead, c.SiteID) {
		return nil
	}
	if authz.CanAccessUnit(ctx, c.UnitID, authz.UnitSide(c.DebtorKind)) == nil {
		return nil
	}
	return problem.NotFound("no such checkout")
}

// Get is the status poll: it returns the checkout (to its creator, to a manager of the site, to a resident of the
// unit side). A pending checkout is reconciled with the provider (at most every few seconds), so a payment whose
// webhook was lost is still found; a pending checkout past its expiry is expired.
func (s *Service) Get(ctx context.Context, org, checkoutID uuid.UUID) (Checkout, error) {
	var c Checkout
	err := s.run(ctx, org, "checkout", func(tx pgx.Tx) (err error) {
		if err = authz.RequireInOrg(ctx, PermCheckout, org); err != nil {
			return err
		}
		c, err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM checkout WHERE org_id = $1 AND id = $2`, org, checkoutID))
		if err != nil {
			return err
		}
		return s.access(ctx, tx, c)
	})
	if err != nil {
		return Checkout{}, err
	}
	if c.Status != StatusPending {
		return c, nil
	}
	return s.reconcile(ctx, org, c)
}

// reconcile asks the provider about a pending checkout.
func (s *Service) reconcile(ctx context.Context, org uuid.UUID, c Checkout) (Checkout, error) {
	now := s.cfg.Now()
	// The throttle is a claim in the database, taken with one conditional UPDATE: of any number of concurrent
	// polls (of this process or another) only the one that moves last_polled_at asks the provider, so a client
	// that polls in a loop cannot turn the poll into a flood of provider calls.
	claimed := false
	if c.providerTxn != nil && s.cfg.Providers != nil {
		err := s.run(ctx, org, "checkout", func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE checkout SET last_polled_at = $3 WHERE org_id = $1 AND id = $2 AND status = 'pending'
				AND (last_polled_at IS NULL OR last_polled_at <= $3::timestamptz - make_interval(secs => $4))`, org, c.ID, now.UTC(), pollEvery.Seconds())
			claimed = tag.RowsAffected() == 1
			return err
		})
		if err != nil {
			s.cfg.Log.WarnContext(ctx, "checkout status poll: claiming the poll", "checkout_id", c.ID, "error", err)
			claimed = false
		}
	}
	if claimed {
		pack := s.packOf(ctx, org, c.Provider)
		st, err := s.cfg.Providers.PaymentStatus(ctx, org, pack, c.Provider, *c.providerTxn)
		switch {
		case err != nil:
			s.cfg.Log.WarnContext(ctx, "checkout status poll: the provider did not answer", "checkout_id", c.ID, "provider", c.Provider, "error", err)
		case st.State == sdk.PaymentPaid:
			ev := sdk.PaymentEvent{Kind: sdk.EventKindPaid, ProviderTxnID: *c.providerTxn, Gross: st.Gross, Fee: st.Fee, Net: st.Net}
			if _, err := s.OnPaid(ctx, org, c.Provider, ev); err != nil {
				s.cfg.Log.WarnContext(ctx, "checkout status poll: recording the payment failed", "checkout_id", c.ID, "error", err)
			}
		case st.State == sdk.PaymentFailed:
			if err := s.OnFailed(ctx, org, c.Provider, *c.providerTxn, "provider_declined"); err != nil {
				s.cfg.Log.WarnContext(ctx, "checkout status poll: marking the checkout failed", "checkout_id", c.ID, "error", err)
			}
		}
	}
	var out Checkout
	err := s.run(ctx, org, "checkout", func(tx pgx.Tx) (err error) {
		// a pending checkout past its expiry is expired on the spot
		if _, err = tx.Exec(ctx, `UPDATE checkout SET status = 'expired', completed_at = $3 WHERE org_id = $1 AND id = $2 AND status = 'pending' AND expires_at < $3`, org, c.ID, now.UTC()); err != nil {
			return err
		}
		out, err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM checkout WHERE org_id = $1 AND id = $2`, org, c.ID))
		return err
	})
	return out, err
}

// packOf finds the pack of a provider id among the enabled ones.
func (s *Service) packOf(ctx context.Context, org uuid.UUID, provider string) string {
	list, err := s.cfg.Providers.Providers(ctx, org)
	if err != nil {
		return ""
	}
	for _, p := range list {
		if p.Provider == provider {
			return p.Pack
		}
	}
	// a provider id is "<packid>" or "<packid>.<name>"
	pack, _, _ := strings.Cut(provider, ".")
	return pack
}

// ListMine lists the checkouts the caller started, oldest first.
func (s *Service) ListMine(ctx context.Context, org uuid.UUID, p tenancy.PageParams) ([]Checkout, string, error) {
	u := ledger.Actor(ctx)
	if u == nil {
		return nil, "", problem.Unauthenticated("a signed-in account is needed")
	}
	if err := authz.RequireInOrg(ctx, PermCheckout, org); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Checkout
	err = s.run(ctx, org, "checkout", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+cols+` FROM checkout WHERE org_id = $1 AND created_by = $2 AND id > $3 ORDER BY id LIMIT $4`, org, *u, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(c Checkout) uuid.UUID { return c.ID })
	return items, next, nil
}

// ListSite lists the checkouts of a site for its managers (money.read), oldest first; status "" lists all.
func (s *Service) ListSite(ctx context.Context, org, site uuid.UUID, status string, p tenancy.PageParams) ([]Checkout, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Checkout
	err = s.run(ctx, org, "site", func(tx pgx.Tx) error {
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, site); err != nil {
			return err
		}
		if _, err := tenancy.LoadSite(ctx, tx, org, site, false, true); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+cols+` FROM checkout WHERE org_id = $1 AND site_id = $2 AND id > $3 AND ($4 = '' OR status = $4) ORDER BY id LIMIT $5`, org, site, after, status, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(c Checkout) uuid.UUID { return c.ID })
	return items, next, nil
}

// ---- completion (webhook, status poll)

func (s *Service) byTxn(ctx context.Context, org uuid.UUID, provider, txn string) (Checkout, bool, error) {
	var c Checkout
	found := true
	err := s.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) (err error) {
		c, err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM checkout WHERE org_id = $1 AND provider = $2 AND provider_txn_id = $3`, org, provider, txn))
		if errors.Is(err, pgx.ErrNoRows) {
			found, err = false, nil
		}
		return err
	})
	return c, found, err
}

// OnPaid completes the checkout a paid provider event belongs to: the payment is recorded through the payments
// module (gross to the debtor's receivable, allocated to the selected charges or oldest first, the fee and the
// clearing account, the receipt numbered, payment.confirmed notified and emitted), the checkout becomes paid and
// the receipt is filed. handled is false when the transaction belongs to no checkout (the caller records it from
// the event's own target). Replays are idempotent per (organization, provider, provider_txn_id); a late payment
// of an expired or failed checkout is recorded all the same.
func (s *Service) OnPaid(ctx context.Context, org uuid.UUID, provider string, ev sdk.PaymentEvent) (handled bool, err error) {
	c, ok, err := s.byTxn(ctx, org, provider, ev.ProviderTxnID)
	if err != nil || !ok {
		return false, err
	}
	cur := string(c.Amount.Currency)
	// the payment holds what the provider confirmed: an event that confirms no amount is not recorded (the
	// checkout's own amount is what was asked for, not what was paid)
	if ev.Gross.Amount <= 0 || (ev.Gross.Currency != "" && ev.Gross.Currency != cur) {
		return true, problem.Validation(problem.Field{Field: "gross", Code: "invalid_value"})
	}
	gross := money.New(ev.Gross.Amount, money.Code(cur))
	fee := money.Zero(money.Code(cur))
	if ev.Fee.Amount > 0 {
		fee = money.New(ev.Fee.Amount, money.Code(cur))
	}
	in := payments.ProviderInput{Provider: provider, ProviderTxnID: ev.ProviderTxnID, SiteID: c.SiteID, Debtor: billing.Debtor{UnitID: &c.UnitID, Kind: c.DebtorKind},
		Gross: gross, Fee: fee, ChargeIDs: c.ChargeIDs}
	ref := "checkout:" + c.ID.String()
	in.Reference = &ref
	if !ev.OccurredAt.IsZero() {
		in.ReceivedOn = ev.OccurredAt.UTC().Format(ledger.DateLayout)
	}
	sys := ledger.SystemContext(ctx, org)
	res, err := s.cfg.Payments.RecordProvider(sys, org, in)
	if s.chargesNoLongerOpen(err) {
		// the charges this checkout was for were paid by something else while the payer was at the provider;
		// the money has moved all the same, so it is recorded oldest first (any surplus becomes credit) instead
		// of bouncing the webhook for the provider to retry for ever
		s.cfg.Log.WarnContext(ctx, "checkout paid after its charges were settled; recording the payment oldest first", "checkout_id", c.ID)
		in.ChargeIDs = nil
		res, err = s.cfg.Payments.RecordProvider(sys, org, in)
	}
	if err != nil {
		return true, err
	}
	confirmed := res.Payment.Gross.Amount
	err = s.run(ctx, org, "checkout", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE checkout SET status = 'paid', payment_id = $3, failure_code = NULL, completed_at = now()
			WHERE org_id = $1 AND id = $2 AND status IN ('pending', 'expired', 'failed')`, org, c.ID, res.Payment.ID); err != nil {
			return err
		}
		if confirmed == c.Amount.Amount {
			return nil
		}
		// the provider confirmed another amount than the checkout asked for: the payment holds the confirmed one,
		// the checkout says so, once, and the audit trail names both amounts
		tag, err := tx.Exec(ctx, `UPDATE checkout SET amount_mismatch = true, confirmed_amount = $3 WHERE org_id = $1 AND id = $2 AND NOT amount_mismatch`, org, c.ID, confirmed)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return ledger.Audit(ctx, tx, org, c.SiteID, "checkout.amount_mismatch", "checkout", c.ID, nil, map[string]any{
			"expected": c.Amount.Amount, "confirmed": confirmed, "payment_id": res.Payment.ID.String(), "provider_txn_id": ev.ProviderTxnID})
	})
	if err != nil {
		return true, err
	}
	if !res.Duplicate && s.cfg.Receipts != nil {
		if err := s.cfg.Receipts.FileReceipt(sys, org, res.Payment.ID); err != nil {
			// the payment stands; the receipt can be generated again on demand
			s.cfg.Log.WarnContext(ctx, "filing the receipt of a checkout payment", "payment_id", res.Payment.ID, "error", err)
		}
	}
	return true, nil
}

// chargesNoLongerOpen reports whether err is the payments module refusing the selected charges because they are
// not open any more.
func (s *Service) chargesNoLongerOpen(err error) bool {
	var pe *problem.Error
	if !errors.As(err, &pe) {
		return false
	}
	for _, f := range pe.Fields {
		if f.Field == "charge_ids" && f.Code == "not_open" {
			return true
		}
	}
	return false
}

// OnFailed marks the pending checkout of a provider transaction failed.
func (s *Service) OnFailed(ctx context.Context, org uuid.UUID, provider, txn, code string) error {
	return s.run(ctx, org, "checkout", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE checkout SET status = 'failed', failure_code = $4, completed_at = now() WHERE org_id = $1 AND provider = $2 AND provider_txn_id = $3 AND status = 'pending'`,
			org, provider, txn, code)
		return err
	})
}

// OnRefunded marks the paid checkout of a provider transaction refunded.
func (s *Service) OnRefunded(ctx context.Context, org uuid.UUID, provider, txn string) error {
	return s.run(ctx, org, "checkout", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE checkout SET status = 'refunded' WHERE org_id = $1 AND provider = $2 AND provider_txn_id = $3 AND status = 'paid'`, org, provider, txn)
		return err
	})
}

// ---- refunds

// Refund refunds a provider payment in full: the provider refunds first (the refund reference is derived from the
// payment, so a retry cannot refund twice), then the reversal is posted through the payments module, the checkout
// becomes refunded and the action is audited under the manager's name. It needs money.post at the site and the
// payment's current version (If-Match); a payment already refunded is returned unchanged.
func (s *Service) Refund(ctx context.Context, org, paymentID uuid.UUID, version int, reason string) (payments.Payment, error) {
	if len([]rune(reason)) > 500 {
		return payments.Payment{}, bad("reason", "too_long")
	}
	var pr struct {
		site          uuid.UUID
		provider, txn string
		status        string
		version       int
	}
	err := s.run(ctx, org, "payment", func(tx pgx.Tx) error {
		var prov, txn *string
		if err := tx.QueryRow(ctx, `SELECT site_id, provider, provider_txn_id, status, version FROM payment WHERE org_id = $1 AND id = $2`, org, paymentID).
			Scan(&pr.site, &prov, &txn, &pr.status, &pr.version); err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermMoneyPost, pr.site); err != nil {
			return err
		}
		if prov == nil || txn == nil {
			return problem.Conflict("only a payment made through a payment provider can be refunded this way; void a cash or bank payment instead")
		}
		pr.provider, pr.txn = *prov, *txn
		return nil
	})
	if err != nil {
		return payments.Payment{}, err
	}
	if pr.status == "refunded" {
		return s.cfg.Payments.Get(ctx, org, paymentID)
	}
	if pr.version != version {
		return payments.Payment{}, tenancy.VersionConflict("payment")
	}
	if pr.status != "confirmed" {
		return payments.Payment{}, problem.Conflict("only a confirmed payment can be refunded")
	}
	if s.cfg.Providers == nil {
		return payments.Payment{}, unavailable()
	}
	pack := s.packOf(ctx, org, pr.provider)
	if reason == "" {
		reason = "refunded by a manager"
	}
	if _, err := s.cfg.Providers.RefundPayment(ctx, org, pack, pr.provider, sdk.RefundRequest{ProviderTxnID: pr.txn, RefundRef: "refund-" + paymentID.String(), Reason: reason}); err != nil {
		s.cfg.Log.WarnContext(ctx, "refunding a provider payment", "payment_id", paymentID, "provider", pr.provider, "error", err)
		return payments.Payment{}, providerProblem(err)
	}
	err = s.run(ctx, org, "payment", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE checkout SET status = 'refunded' WHERE org_id = $1 AND payment_id = $2 AND status = 'paid'`, org, paymentID); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, org, pr.site, "payment.refund_issue", "payment", paymentID, nil, map[string]any{"provider": pr.provider, "reason": reason})
	})
	if err != nil {
		return payments.Payment{}, err
	}
	return s.cfg.Payments.Get(ctx, org, paymentID)
}

// ---- jobs

// ExpireStale expires every pending checkout past its expiry, in every organization (the checkout.expire job).
func (s *Service) ExpireStale(ctx context.Context) (int64, error) {
	var n int64
	err := s.cfg.Platform.InPlatformTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE checkout SET status = 'expired', completed_at = now() WHERE status = 'pending' AND expires_at < now()`)
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

type siteRow struct {
	org, site uuid.UUID
	settings  map[string]any
	orgSet    map[string]any
}

// SyncSettlements asks the payment providers of every organization for the payouts of a period (civil dates,
// inclusive) and posts them (the checkout.sync_settlements job). Per provider, each site that collects into its
// own account is asked on its own; the organization's account is asked once for the sites whose merchant of
// record is the organization. A failing provider is logged and does not stop the others.
func (s *Service) SyncSettlements(ctx context.Context, from, to string) (posted, failed int, err error) {
	var sites []siteRow
	err = s.cfg.Platform.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT s.org_id, s.id, s.settings, o.settings FROM site s JOIN organization o ON o.id = s.org_id
			WHERE s.status = 'active' AND o.status = 'active' ORDER BY s.org_id, s.id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r siteRow
			var a, b []byte
			if err := rows.Scan(&r.org, &r.site, &a, &b); err != nil {
				return err
			}
			r.settings, r.orgSet = map[string]any{}, map[string]any{}
			_ = json.Unmarshal(a, &r.settings)
			_ = json.Unmarshal(b, &r.orgSet)
			sites = append(sites, r)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, 0, err
	}
	byOrg := map[uuid.UUID][]siteRow{}
	var orgs []uuid.UUID
	for _, r := range sites {
		if _, ok := byOrg[r.org]; !ok {
			orgs = append(orgs, r.org)
		}
		byOrg[r.org] = append(byOrg[r.org], r)
	}
	for _, org := range orgs {
		provs, err := s.cfg.Providers.Providers(ctx, org)
		if err != nil {
			s.cfg.Log.ErrorContext(ctx, "settlement sync: listing providers", "org_id", org, "error", err)
			failed++
			continue
		}
		for _, pv := range provs {
			var orgModeSite *uuid.UUID
			for _, r := range byOrg[org] {
				if want := tenancy.EffectivePaymentProvider(r.orgSet, r.settings); want != "" && want != pv.Provider {
					continue
				}
				if tenancy.EffectiveMerchantOfRecord(r.orgSet, r.settings) == tenancy.MerchantOrganization {
					if orgModeSite == nil {
						site := r.site
						orgModeSite = &site
					}
					continue
				}
				n, err := s.cfg.Providers.SyncSettlements(ctx, org, pv.Pack, pv.Provider, sdk.ID(r.site.String()),
					sdk.SettlementRequest{From: from, To: to, Site: sdk.ID(r.site.String()), MerchantOfRecord: sdk.MerchantSite})
				posted += n
				if err != nil {
					s.cfg.Log.ErrorContext(ctx, "settlement sync failed", "org_id", org, "site_id", r.site, "provider", pv.Provider, "error", err)
					failed++
				}
			}
			if orgModeSite != nil {
				n, err := s.cfg.Providers.SyncSettlements(ctx, org, pv.Pack, pv.Provider, sdk.ID(orgModeSite.String()),
					sdk.SettlementRequest{From: from, To: to, MerchantOfRecord: sdk.MerchantOrganization})
				posted += n
				if err != nil {
					s.cfg.Log.ErrorContext(ctx, "settlement sync failed", "org_id", org, "provider", pv.Provider, "error", err)
					failed++
				}
			}
		}
	}
	return posted, failed, nil
}
