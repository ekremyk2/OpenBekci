package sdk

import (
	"context"
	"io"
	"log/slog"
)

// Services is what the core gives a pack. Packs read through the readers and
// write through the poster and recorder; the core's invariants (balanced
// entries, lock date, idempotency, audit) apply to pack writes exactly as to
// its own. Nothing here takes an organization argument: every call acts for
// the organization in its context ([OrgFrom]), which the host sets before
// calling into the pack.
type Services interface {
	Units() UnitReader
	People() PeopleReader
	Ledger() LedgerReader
	Poster() LedgerPoster
	Payments() PaymentRecorder
	Notifier() Notifier
	Files() FileStore
	Settings() Settings
	Jobs() JobQueue
	// DB gives tenant transactions only: row-level security applies to the
	// pack's pack_<id>_* tables. There is no raw pool.
	DB() DB
	Log() *slog.Logger
}

// DB runs transactions with the tenant context set to the organization in ctx.
type DB interface {
	// InTx runs fn in a transaction; it commits if fn returns nil and rolls back otherwise.
	InTx(ctx context.Context, fn func(Tx) error) error
}

// Tx is a tenant transaction. Query parameters use PostgreSQL's $1 style.
// Packs touch their own pack_<id>_* tables only; core tables are read through
// the readers.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (rowsAffected int64, err error)
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

// Rows iterates a result set; Close must be called.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// Row is one row; Scan returns ErrNoRows when there was none.
type Row interface {
	Scan(dest ...any) error
}

// UnitReader reads units (dwellings, shops, parking spaces, ...).
type UnitReader interface {
	Get(ctx context.Context, unit ID) (Unit, error)
	// List returns the units of a site ordered by door number.
	List(ctx context.Context, site ID) ([]Unit, error)
}

// Unit is a unit as a pack sees it.
type Unit struct {
	ID         ID
	SiteID     ID
	DoorNumber string
	Type       string
	AreaM2     *float64
}

// PeopleReader reads people. National ids and other sealed fields are never exposed.
type PeopleReader interface {
	Get(ctx context.Context, person ID) (Person, error)
	// Occupants lists the people currently occupying a unit with the kind of their occupancy.
	Occupants(ctx context.Context, unit ID) ([]Occupant, error)
}

// Person is a person as a pack sees it.
type Person struct {
	ID          ID
	DisplayName string
	Email       string
	Phone       string
}

// Occupant is a person living in or owning a unit.
type Occupant struct {
	Person Person
	// Kind is "owner", "tenant" or "household".
	Kind string
}

// LedgerReader reads the double-entry ledger.
type LedgerReader interface {
	Accounts(ctx context.Context, site ID) ([]Account, error)
	// Balance returns the debit and credit totals of an account by code.
	Balance(ctx context.Context, site ID, accountCode string) (debit, credit int64, err error)
}

// Account is a ledger account.
type Account struct {
	ID      ID
	Code    string
	Name    string
	Type    string // asset, liability, equity, income, expense
	Subtype string
}

// LedgerPoster appends entries to the ledger. The entry must balance, and
// within each fund; the date must be after the site's lock date.
type LedgerPoster interface {
	Post(ctx context.Context, e Entry) (ID, error)
}

// Entry is a journal entry to post.
type Entry struct {
	Site      ID
	EntryDate string // civil date
	Memo      string
	Lines     []EntryLine
}

// EntryLine is one line; exactly one of Debit and Credit is positive.
type EntryLine struct {
	AccountCode string
	// Fund is the fund code ("operating", "reserve"); empty means operating.
	Fund   string
	Debit  int64
	Credit int64
	// Unit and DebtorKind attach a receivable line to a unit side.
	Unit       ID
	DebtorKind string
}

// PaymentRecorder records provider payments, refunds and settlements through
// the payments module: allocation to charges, receipt numbers, audit and the
// ledger postings are the core's. Everything is idempotent per
// (organization, provider, provider_txn_id) or the given reference, so replays
// are safe.
type PaymentRecorder interface {
	// RecordProviderPayment records a paid transaction: gross to the debtor's
	// receivable, the fee to the fee expense account and the net to the
	// provider's clearing account. A replay returns the first result with Duplicate set.
	RecordProviderPayment(ctx context.Context, p ProviderPayment) (RecordedPayment, error)
	// RefundProviderPayment reverses a recorded provider payment in full.
	RefundProviderPayment(ctx context.Context, r ProviderRefund) (RefundOutcome, error)
	// RecordSettlement posts a provider payout: bank against the clearing account.
	RecordSettlement(ctx context.Context, s ProviderSettlement) (SettlementOutcome, error)
}

// ProviderPayment is a paid provider transaction.
type ProviderPayment struct {
	Provider      string
	ProviderTxnID string
	Target        PaymentTarget
	Gross, Fee    Money
	// ReceivedOn is the civil date the money was received; empty means today at the site.
	ReceivedOn string
	Reference  string
}

// RecordedPayment is the result of RecordProviderPayment.
type RecordedPayment struct {
	PaymentID ID
	// Duplicate is true when the transaction was recorded before.
	Duplicate bool
}

// ProviderRefund is a refund of a recorded provider payment.
type ProviderRefund struct {
	Provider      string
	ProviderTxnID string
	// RefundRef is the idempotency key (the provider's refund id).
	RefundRef string
	Reason    string
}

// RefundOutcome is the result of RefundProviderPayment.
type RefundOutcome struct {
	PaymentID ID
	// Duplicate is true when the payment was already refunded.
	Duplicate bool
}

// ProviderSettlement is a payout.
type ProviderSettlement struct {
	Provider string
	Site     ID
	Ref      string
	// BankAccountCode is the bank account the net arrived in; empty means the site's default bank account.
	BankAccountCode string
	SettledOn       string
	Net             Money
	// ProviderTxnIDs are the transactions the payout covers. When the
	// organization is the merchant of record one payout covers the payments of
	// several sites; the core splits the net by these transactions' sites (the
	// remainder, if any, goes to Site). Optional otherwise.
	ProviderTxnIDs []string
}

// SettlementOutcome is the result of RecordSettlement.
type SettlementOutcome struct {
	EntryID   ID
	Duplicate bool
}

// Notifier sends notifications to people through the core notification
// module (in-app, email, SMS according to their channel settings).
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Notification is a message to people.
type Notification struct {
	// Type selects the template; use a type of the core vocabulary or
	// "<packid>.<name>" with a catalog entry.
	Type    string
	Site    ID
	People  []ID
	Payload map[string]any
	// Link is the app path the notification opens.
	Link string
}

// FileStore stores and reads files through the core file module.
type FileStore interface {
	// Put stores a generated file (a receipt, an export) and returns its id.
	Put(ctx context.Context, f NewFile) (ID, error)
	// Open reads a file.
	Open(ctx context.Context, file ID) (io.ReadCloser, FileInfo, error)
}

// NewFile is a file to store.
type NewFile struct {
	Site    ID // optional
	Name    string
	Content io.Reader
}

// FileInfo describes a stored file.
type FileInfo struct {
	Name string
	MIME string
	Size int64
}

// JobQueue enqueues the pack's own background jobs.
type JobQueue interface {
	// Enqueue queues a job of the pack (kind "<packid>.<name>") for the
	// organization in ctx. The payload must be small.
	Enqueue(ctx context.Context, kind string, payload []byte) error
}
