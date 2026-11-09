package sdk

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// PaymentProvider is a card or online payment provider (plan section 3.8),
// defined around how providers settle: the resident pays gross, the provider
// keeps a fee, the net is settled to the bank later. The core posts gross to
// the resident's receivable, the fee to an expense account and the net through
// a per-provider clearing account until the settlement arrives.
//
// Core call sites (the payments module, never the pack):
//
//   - CreateCheckout: the core checkout endpoint (POST /v1/orgs/{org}/checkouts)
//     asks the provider for a hosted checkout and returns checkout_url.
//   - Status: the checkout status poll of the client (GET /v1/orgs/{org}/checkouts/{id}).
//   - HandleWebhook: the core route POST /v1/orgs/{org}/packs/{id}/payments/{provider}/webhook
//     reads the body, calls HandleWebhook and records the returned events
//     through the payments module, idempotently per (org, provider,
//     provider_txn_id): a replay is acknowledged and ignored.
//   - Refund: the refund operation; the core posts the reversal.
//   - Settlements: the settlement import; the core posts bank against clearing.
//
// Implementations read their credentials with Services.Settings (secret
// fields included) and must verify webhook signatures themselves. Whose
// provider account a call uses follows the core setting
// payments.merchant_of_record: CheckoutRequest.MerchantOfRecord and
// SettlementRequest.MerchantOfRecord say which, and AccountScope turns it into
// the settings scope (the site's own, or the organization's) to read the
// credentials from. A webhook event whose transaction belongs to a checkout is
// recorded for the debtor and the charges the checkout was created for, never
// from what the event says about them.
type PaymentProvider interface {
	// ID is the provider id ("<packid>" or "<packid>.<name>"); the payment's
	// method is "card:<id>".
	ID() string
	// CreateCheckout starts a hosted checkout for the gross amount.
	CreateCheckout(ctx context.Context, req CheckoutRequest) (Checkout, error)
	// Status asks the provider for the state of a transaction.
	Status(ctx context.Context, provider ProviderRef) (PaymentStatus, error)
	// HandleWebhook verifies and parses one webhook delivery. It records
	// nothing; it returns the events the core must record and the response to
	// send to the provider. A bad signature returns ErrInvalidSignature.
	HandleWebhook(ctx context.Context, req WebhookRequest) (WebhookResult, error)
	// Refund refunds a payment at the provider. RefundRequest.RefundRef is
	// the idempotency key: the same ref must not refund twice.
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
	// Settlements lists the settlements the provider paid out in a period,
	// each with gross, fee and net.
	Settlements(ctx context.Context, req SettlementRequest) ([]Settlement, error)
}

// PaymentTarget says what a payment is for. The core gives it to
// CreateCheckout and the provider echoes it back in the webhook event (as
// provider metadata); the webhook signature protects it.
type PaymentTarget struct {
	SiteID ID
	UnitID ID
	// DebtorKind is "owner" or "tenant": which side of the unit pays.
	DebtorKind string
	// ChargeIDs applies the payment to these charges only; empty means oldest first.
	ChargeIDs []ID
}

// CheckoutRequest asks for a hosted checkout.
type CheckoutRequest struct {
	// Reference is an opaque core-generated idempotency reference.
	Reference string
	Gross     Money
	Target    PaymentTarget
	// ReturnURL is where the provider sends the resident back to.
	ReturnURL string
	// Locale is the resident's locale ("tr", "en").
	Locale string
	Payer  Payer
	// MerchantOfRecord is MerchantSite or MerchantOrganization (core setting
	// payments.merchant_of_record): whose provider account the checkout is
	// created on. Read the account with AccountScope(MerchantOfRecord, Target.SiteID).
	MerchantOfRecord string
}

// Merchant of record values (core setting payments.merchant_of_record, decision
// M5): whose provider account collects the money.
const (
	// MerchantSite: the site collects into its own provider account (the default).
	// The provider reads its credentials at the site scope.
	MerchantSite = "site"
	// MerchantOrganization: the management company collects centrally into the
	// organization's provider account; the net is client money owed to the site.
	// The provider reads its credentials at the organization scope.
	MerchantOrganization = "organization"
)

// AccountScope is the settings scope a provider reads its account (credentials,
// merchant id) from for a call: the organization scope when the organization is
// the merchant of record, else the scope of the site. The core fills
// CheckoutRequest.MerchantOfRecord and SettlementRequest.MerchantOfRecord; a
// provider passes that and the site of the target.
func AccountScope(merchantOfRecord string, site ID) Scope {
	if merchantOfRecord == MerchantOrganization || site == "" {
		return OrgScope()
	}
	return SiteScope(site)
}

// Payer is what the provider may need to know about the payer.
type Payer struct {
	Name, Email, Phone string
}

// Checkout is a created hosted checkout.
type Checkout struct {
	ProviderTxnID string
	CheckoutURL   string
	ExpiresAt     *time.Time
}

// ProviderRef names a transaction at a provider.
type ProviderRef struct {
	ProviderTxnID string
}

// PaymentState is the state of a provider transaction. Open enum: unknown
// states are treated as pending.
type PaymentState string

// Payment states.
const (
	PaymentPending  PaymentState = "pending"
	PaymentPaid     PaymentState = "paid"
	PaymentFailed   PaymentState = "failed"
	PaymentRefunded PaymentState = "refunded"
)

// PaymentStatus is the answer of Status.
type PaymentStatus struct {
	State PaymentState
	// Gross, Fee and Net are set once the transaction is paid; Net = Gross - Fee.
	Gross, Fee, Net Money
}

// WebhookRequest is one webhook delivery as received.
type WebhookRequest struct {
	Method     string
	Path       string
	Header     http.Header
	Query      url.Values
	Body       []byte
	ReceivedAt time.Time
}

// WebhookResult is what HandleWebhook returns.
type WebhookResult struct {
	// Events are the payment events to record, possibly none (a ping).
	Events []PaymentEvent
	// Response is sent back to the provider; nil means 200 with an empty body.
	Response *HTTPResponse
}

// HTTPResponse is the response to send to a webhook caller.
type HTTPResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// PaymentEventKind says what happened at the provider.
type PaymentEventKind string

// Payment event kinds.
const (
	// EventKindPaid: the resident paid. Needs ProviderTxnID, Target, Gross, Fee, Net.
	EventKindPaid PaymentEventKind = "paid"
	// EventKindFailed: the payment failed; nothing is recorded.
	EventKindFailed PaymentEventKind = "failed"
	// EventKindRefunded: the provider refunded the payment (started at the
	// provider's side); the core posts the reversal. Needs ProviderTxnID, RefundID.
	EventKindRefunded PaymentEventKind = "refunded"
	// EventKindSettled: the provider paid out. Needs Settlement.
	EventKindSettled PaymentEventKind = "settled"
)

// PaymentEvent is one thing the webhook reported.
type PaymentEvent struct {
	Kind          PaymentEventKind
	ProviderTxnID string
	// RefundID is the provider's refund id, the idempotency key of a refund event.
	RefundID   string
	Target     PaymentTarget
	Gross      Money
	Fee        Money
	Net        Money
	OccurredAt time.Time
	// Reference is the payment reference shown on the statement.
	Reference  string
	Settlement *Settlement
}

// RefundRequest asks the provider to refund a payment in full. Partial
// refunds return ErrUnsupported in release 0.4 of the core.
type RefundRequest struct {
	ProviderTxnID string
	// RefundRef is the idempotency key.
	RefundRef string
	Amount    Money
	Reason    string
}

// RefundResult is the provider's answer to a refund.
type RefundResult struct {
	ProviderRefundID string
	Amount           Money
}

// SettlementRequest selects a period (civil dates, inclusive).
type SettlementRequest struct {
	From, To string
	// Site is the site whose provider account is asked (MerchantSite); empty
	// when the organization's account is asked (MerchantOrganization).
	Site ID
	// MerchantOfRecord is MerchantSite or MerchantOrganization; read the
	// account with AccountScope(MerchantOfRecord, Site).
	MerchantOfRecord string
}

// Settlement is one payout of the provider: gross, fee and net, and the
// transactions it covers.
type Settlement struct {
	// Ref is the provider's settlement id, the idempotency key of the posting.
	Ref             string
	SettledOn       string // civil date
	Gross, Fee, Net Money
	ProviderTxnIDs  []string
}
