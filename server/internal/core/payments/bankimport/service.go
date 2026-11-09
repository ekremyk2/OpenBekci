package bankimport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// RawRetention is how long the parsed source row of a line is kept.
const RawRetention = 90 * 24 * time.Hour

// Service is the bank import module.
type Service struct {
	db    ledger.TxRunner
	pay   *payments.Service
	packs PackImporters
	Now   func() time.Time
}

// NewService returns a Service; now nil means time.Now.
func NewService(db ledger.TxRunner, pay *payments.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, pay: pay, Now: now}
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

// Importer describes a statement importer.
type Importer struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Formats        []string `json:"formats"`
	DefaultMapping *Mapping `json:"default_mapping"`
}

// PackImporters is the pack side of the importers: the statement importers
// of the packs enabled for an organization. The wiring adapts the pack runtime
// to it (the core never imports the runtime).
type PackImporters interface {
	// Importers lists the importers of the organization's enabled packs.
	Importers(ctx context.Context, orgID uuid.UUID) ([]Importer, error)
	// Parse parses a statement with a pack importer; ok is false when no
	// enabled pack provides the importer. opening and closing are the balances
	// the file states, when it does.
	Parse(ctx context.Context, orgID uuid.UUID, importerID, filename string, data []byte) (lines []Line, rowErrs []RowError, opening, closing *int64, ok bool, err error)
}

// SetPacks sets the source of pack importers (nil: only the generic CSV importer).
func (s *Service) SetPacks(p PackImporters) { s.packs = p }

// Importers lists the generic importer and the importers of the packs enabled for the organization.
func (s *Service) Importers(ctx context.Context, orgID uuid.UUID) []Importer {
	out := []Importer{{ID: CoreCSV, Name: "Generic CSV / XLSX statement", Formats: []string{"csv", "xlsx"}, DefaultMapping: &Mapping{Columns: DefaultColumns}}}
	if s.packs != nil {
		if more, err := s.packs.Importers(ctx, orgID); err == nil {
			out = append(out, more...)
		}
	}
	return out
}

// Import is an uploaded statement as the API shows it.
type Import struct {
	ID               uuid.UUID    `json:"id"`
	OrgID            uuid.UUID    `json:"org_id"`
	SiteID           uuid.UUID    `json:"site_id"`
	Importer         string       `json:"importer"`
	Filename         string       `json:"filename"`
	FileID           *uuid.UUID   `json:"file_id"`
	AssetAccountID   uuid.UUID    `json:"asset_account_id"`
	Status           string       `json:"status"`
	OpeningBalance   *money.Money `json:"opening_balance"`
	ClosingBalance   *money.Money `json:"closing_balance"`
	PeriodFrom       *string      `json:"period_from"`
	PeriodTo         *string      `json:"period_to"`
	TransactionCount int64        `json:"transaction_count"`
	UnmatchedCount   int64        `json:"unmatched_count"`
	Error            *string      `json:"error"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	currency         money.Code
}

const importSel = `SELECT i.id, i.org_id, i.site_id, i.importer, i.filename, i.asset_account_id, i.status, i.opening_balance, i.closing_balance, i.period_from::text, i.period_to::text, i.error, i.created_at, i.updated_at, s.currency,
	(SELECT count(*) FROM bank_transaction t WHERE t.org_id = i.org_id AND t.import_id = i.id),
	(SELECT count(*) FROM bank_transaction t WHERE t.org_id = i.org_id AND t.import_id = i.id AND t.match_status IN ('unmatched', 'suggested'))
	FROM bank_statement_import i JOIN site s ON s.org_id = i.org_id AND s.id = i.site_id`

func scanImport(r pgx.Row) (Import, error) {
	var im Import
	var op, cl *int64
	var cur string
	err := r.Scan(&im.ID, &im.OrgID, &im.SiteID, &im.Importer, &im.Filename, &im.AssetAccountID, &im.Status, &op, &cl, &im.PeriodFrom, &im.PeriodTo, &im.Error, &im.CreatedAt, &im.UpdatedAt, &cur, &im.TransactionCount, &im.UnmatchedCount)
	c := money.Code(cur)
	im.currency = c
	if op != nil {
		m := money.New(*op, c)
		im.OpeningBalance = &m
	}
	if cl != nil {
		m := money.New(*cl, c)
		im.ClosingBalance = &m
	}
	im.CreatedAt, im.UpdatedAt = im.CreatedAt.UTC(), im.UpdatedAt.UTC()
	return im, err
}

// Transaction is a statement line as the API shows it.
type Transaction struct {
	ID           uuid.UUID    `json:"id"`
	OrgID        uuid.UUID    `json:"org_id"`
	SiteID       uuid.UUID    `json:"site_id"`
	ImportID     uuid.UUID    `json:"import_id"`
	BookedOn     string       `json:"booked_on"`
	Amount       money.Money  `json:"amount"`
	Counterparty *string      `json:"counterparty"`
	Description  *string      `json:"description"`
	Reference    *string      `json:"reference"`
	MatchStatus  string       `json:"match_status"`
	PaymentID    *uuid.UUID   `json:"payment_id"`
	ExpenseID    *uuid.UUID   `json:"expense_id"`
	Suggestions  []Suggestion `json:"suggestions"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

const txSel = `SELECT t.id, t.org_id, t.site_id, t.import_id, t.booked_on::text, t.amount, t.counterparty, t.description, t.reference, t.match_status, t.payment_id, t.expense_id, t.suggestions, t.created_at, t.updated_at, s.currency
	FROM bank_transaction t JOIN site s ON s.org_id = t.org_id AND s.id = t.site_id`

func scanTx(r pgx.Row) (Transaction, error) {
	var t Transaction
	var amt int64
	var cur string
	var sug []byte
	err := r.Scan(&t.ID, &t.OrgID, &t.SiteID, &t.ImportID, &t.BookedOn, &amt, &t.Counterparty, &t.Description, &t.Reference, &t.MatchStatus, &t.PaymentID, &t.ExpenseID, &sug, &t.CreatedAt, &t.UpdatedAt, &cur)
	t.Amount = money.New(amt, money.Code(cur))
	t.Suggestions = []Suggestion{}
	if len(sug) > 0 {
		_ = json.Unmarshal(sug, &t.Suggestions)
	}
	if t.Suggestions == nil {
		t.Suggestions = []Suggestion{}
	}
	t.CreatedAt, t.UpdatedAt = t.CreatedAt.UTC(), t.UpdatedAt.UTC()
	return t, err
}

// CreateInput is an upload.
type CreateInput struct {
	SiteID         uuid.UUID
	Importer       string
	AssetAccountID uuid.UUID
	Filename       string
	Data           []byte
	Mapping        Mapping
	// OpeningBalance and ClosingBalance are optional statement balances in minor units.
	OpeningBalance, ClosingBalance *int64
	// FileID is reserved for the files module.
	FileID *uuid.UUID
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Create stores an uploaded statement, parses it with the importer and
// suggests a unit side per incoming line (money.post). Parsing is synchronous
// today; a failed parse is a 422 and nothing is stored, rows that fail are
// listed in the import's error and the status becomes partial.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, in CreateInput) (Import, error) {
	if len(in.Data) > MaxFileBytes {
		return Import{}, problem.New(413, problem.CodePayloadTooLarge, "the statement is larger than 10 MiB")
	}
	var lines []Line
	var rowErrs []RowError
	if in.Importer == CoreCSV {
		var perr error
		lines, rowErrs, perr = Parse(in.Filename, in.Data, in.Mapping)
		if perr != nil {
			return Import{}, problem.Validation(problem.Field{Field: "file", Code: "invalid_format"})
		}
	} else {
		// an importer of a pack enabled for the organization
		if s.packs == nil {
			return Import{}, bad("importer", "invalid_value")
		}
		var opening, closing *int64
		var ok bool
		var perr error
		lines, rowErrs, opening, closing, ok, perr = s.packs.Parse(ctx, orgID, in.Importer, in.Filename, in.Data)
		switch {
		case perr != nil:
			if errors.As(perr, new(*problem.Error)) {
				return Import{}, perr
			}
			return Import{}, problem.Validation(problem.Field{Field: "file", Code: "invalid_format"})
		case !ok:
			return Import{}, bad("importer", "invalid_value")
		}
		if in.OpeningBalance == nil && in.ClosingBalance == nil {
			in.OpeningBalance, in.ClosingBalance = opening, closing
		}
	}
	if len(lines) > MaxLines {
		return Import{}, problem.Validation(problem.Field{Field: "file", Code: "too_many_rows", Params: map[string]any{"max_rows": MaxLines}})
	}
	if len(lines) == 0 {
		return Import{}, problem.Validation(problem.Field{Field: "file", Code: "required"})
	}
	var out Import
	err := s.run(ctx, orgID, "bank statement import", func(tx pgx.Tx) error {
		site, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyPost, orgID, in.SiteID)
		if err != nil {
			return err
		}
		var typ, sub string
		if err := tx.QueryRow(ctx, `SELECT type, COALESCE(subtype,'') FROM account WHERE org_id=$1 AND site_id=$2 AND id=$3`, orgID, in.SiteID, in.AssetAccountID).Scan(&typ, &sub); err != nil || typ != "asset" || sub != ledger.SubBank {
			return bad("asset_account_id", "invalid_value")
		}
		cands, err := loadCandidates(ctx, tx, orgID, in.SiteID, ledger.Today(site.Timezone, s.Now()))
		if err != nil {
			return err
		}
		opening, closing := in.OpeningBalance, in.ClosingBalance
		from, to := lines[0].BookedOn, lines[0].BookedOn
		for _, l := range lines {
			from, to = min(from, l.BookedOn), max(to, l.BookedOn)
		}
		if opening == nil && closing == nil {
			opening, closing = balancesOf(lines)
		}
		status, errText := "ready", (*string)(nil)
		if len(rowErrs) > 0 {
			status = "partial"
			parts := make([]string, 0, 5)
			for i, e := range rowErrs {
				if i == 5 {
					parts = append(parts, fmt.Sprintf("and %d more", len(rowErrs)-5))
					break
				}
				parts = append(parts, e.Error())
			}
			errText = nilIfEmpty(strings.Join(parts, "; "))
		}
		mapping, _ := json.Marshal(in.Mapping)
		sum := sha256.Sum256(in.Data)
		iid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO bank_statement_import (id, org_id, site_id, importer, asset_account_id, filename, content, content_sha256, mapping, status, opening_balance, closing_balance, period_from, period_to, error, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::date,$14::date,$15,$16)`,
			iid, orgID, in.SiteID, in.Importer, in.AssetAccountID, in.Filename, in.Data, sum[:], mapping, status, opening, closing, from, to, errText, ledger.Actor(ctx)); err != nil {
			if tenancy.IsUnique(err, "bank_statement_import_file") {
				return problem.Conflict("this statement was already imported into this account")
			}
			return err
		}
		for _, l := range lines {
			var sug []Suggestion
			if l.Amount > 0 {
				sug = Rank(l.Counterparty, l.Description, l.Reference, l.Amount, cands)
			}
			if sug == nil {
				sug = []Suggestion{}
			}
			sb, _ := json.Marshal(sug)
			raw, _ := json.Marshal(l.Raw)
			ms := "unmatched"
			if len(sug) > 0 {
				ms = "suggested"
			}
			if _, err := tx.Exec(ctx, `INSERT INTO bank_transaction (id, org_id, site_id, import_id, line_no, booked_on, amount, counterparty, description, reference, raw, match_status, suggestions)
				VALUES ($1,$2,$3,$4,$5,$6::date,$7,$8,$9,$10,$11,$12,$13)`,
				id.New(), orgID, in.SiteID, iid, l.LineNo, l.BookedOn, l.Amount, nilIfEmpty(l.Counterparty), nilIfEmpty(l.Description), nilIfEmpty(l.Reference), raw, ms, sb); err != nil {
				return err
			}
		}
		if err := ledger.Audit(ctx, tx, orgID, in.SiteID, "bank_import.create", "bank_statement_import", iid, nil, map[string]any{"importer": in.Importer, "lines": len(lines), "status": status}); err != nil {
			return err
		}
		out, err = scanImport(tx.QueryRow(ctx, importSel+` WHERE i.org_id=$1 AND i.id=$2`, orgID, iid))
		return err
	})
	return out, err
}

// balancesOf derives the statement balances from a balance column: the file
// may list lines oldest first or newest first.
func balancesOf(lines []Line) (opening, closing *int64) {
	first, last := lines[0], lines[len(lines)-1]
	if first.BookedOn > last.BookedOn {
		first, last = last, first
	}
	if first.Balance == nil || last.Balance == nil {
		return nil, nil
	}
	o := *first.Balance - first.Amount
	c := *last.Balance
	return &o, &c
}

// loadCandidates builds the unit sides a line could belong to.
func loadCandidates(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, today string) ([]Candidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT u.id, u.door_number, sd.side, o.person_id, COALESCE(p.display_name, '')
		FROM unit u CROSS JOIN (VALUES ('owner'), ('tenant')) AS sd(side)
		LEFT JOIN LATERAL (SELECT person_id FROM occupancy oc WHERE oc.org_id = u.org_id AND oc.unit_id = u.id AND oc.kind = sd.side
			AND oc.valid_from <= $3::date AND (oc.valid_to IS NULL OR oc.valid_to > $3::date) ORDER BY oc.is_correspondent DESC, oc.valid_from LIMIT 1) o ON true
		LEFT JOIN person p ON p.org_id = u.org_id AND p.id = o.person_id
		WHERE u.org_id = $1 AND u.site_id = $2 AND u.status = 'active'
		ORDER BY u.door_number, sd.side`, orgID, siteID, today)
	if err != nil {
		return nil, err
	}
	var cands []Candidate
	idx := map[string]int{}
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.UnitID, &c.Door, &c.Side, &c.PersonID, &c.PersonName); err != nil {
			rows.Close()
			return nil, err
		}
		idx[c.UnitID.String()+c.Side] = len(cands)
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	crows, err := tx.Query(ctx, `
		SELECT c.unit_id, c.debtor_kind, c.amount - COALESCE((SELECT SUM(a.amount) FROM payment_allocation a WHERE a.org_id = c.org_id AND a.charge_id = c.id AND NOT a.voided), 0)::bigint, c.period
		FROM charge c WHERE c.org_id = $1 AND c.site_id = $2 AND c.debtor_kind IN ('owner', 'tenant') AND c.cancel_entry_id IS NULL AND c.status IN ('open', 'partially_paid') AND c.amount > 0`, orgID, siteID)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var u uuid.UUID
		var side, period string
		var rem int64
		if err := crows.Scan(&u, &side, &rem, &period); err != nil {
			return nil, err
		}
		if i, ok := idx[u.String()+side]; ok && rem > 0 {
			cands[i].OpenTotal += rem
			cands[i].OpenCharges = append(cands[i].OpenCharges, rem)
			if period != "" {
				cands[i].Periods = append(cands[i].Periods, period)
			}
		}
	}
	return cands, crows.Err()
}

// ListImports lists a site's imports (money.read).
func (s *Service) ListImports(ctx context.Context, orgID, siteID uuid.UUID, status string, account *uuid.UUID, p ledger.PageParams) ([]Import, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Import
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, importSel+` WHERE i.org_id=$1 AND i.site_id=$2 AND i.id > $3 AND ($4 = '' OR i.status = $4) AND ($5::uuid IS NULL OR i.asset_account_id = $5) ORDER BY i.id LIMIT $6`,
			orgID, siteID, after, status, account, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			im, err := scanImport(rows)
			if err != nil {
				return err
			}
			out = append(out, im)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(i Import) uuid.UUID { return i.ID })
	return items, next, nil
}

func (s *Service) loadImport(ctx context.Context, tx pgx.Tx, orgID, importID uuid.UUID, perm authz.Permission) (Import, error) {
	im, err := scanImport(tx.QueryRow(ctx, importSel+` WHERE i.org_id=$1 AND i.id=$2`, orgID, importID))
	if err != nil {
		return im, err
	}
	return im, ledger.RequireSite(ctx, tx, perm, im.SiteID)
}

// GetImport returns an import (money.read).
func (s *Service) GetImport(ctx context.Context, orgID, importID uuid.UUID) (Import, error) {
	var im Import
	err := s.run(ctx, orgID, "bank statement import", func(tx pgx.Tx) (err error) {
		im, err = s.loadImport(ctx, tx, orgID, importID, ledger.PermMoneyRead)
		return err
	})
	return im, err
}

// DeleteImport discards an import while no line is matched (money.post).
func (s *Service) DeleteImport(ctx context.Context, orgID, importID uuid.UUID) error {
	return s.run(ctx, orgID, "bank statement import", func(tx pgx.Tx) error {
		im, err := s.loadImport(ctx, tx, orgID, importID, ledger.PermMoneyPost)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM bank_transaction WHERE org_id=$1 AND import_id=$2 AND match_status = 'matched'`, orgID, importID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problem.Conflict("the import has matched lines; unmatch them first")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM bank_transaction WHERE org_id=$1 AND import_id=$2`, orgID, importID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM bank_statement_import WHERE org_id=$1 AND id=$2`, orgID, importID); err != nil {
			return err
		}
		return ledger.Audit(ctx, tx, orgID, im.SiteID, "bank_import.delete", "bank_statement_import", importID, nil, nil)
	})
}

// TxFilter filters ListTransactions.
type TxFilter struct {
	ImportID    *uuid.UUID
	MatchStatus string
	From, To    string
	Q           string
}

// ListTransactions lists statement lines (money.read).
func (s *Service) ListTransactions(ctx context.Context, orgID, siteID uuid.UUID, f TxFilter, p ledger.PageParams) ([]Transaction, string, error) {
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	var out []Transaction
	err = s.run(ctx, orgID, "site", func(tx pgx.Tx) error {
		if _, err := ledger.SiteFor(ctx, tx, ledger.PermMoneyRead, orgID, siteID); err != nil {
			return err
		}
		q := ""
		if f.Q != "" {
			q = "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(f.Q) + "%"
		}
		rows, err := tx.Query(ctx, txSel+` WHERE t.org_id=$1 AND t.site_id=$2 AND t.id > $3 AND ($4::uuid IS NULL OR t.import_id = $4) AND ($5 = '' OR t.match_status = $5)
			AND ($6 = '' OR t.booked_on >= $6::date) AND ($7 = '' OR t.booked_on <= $7::date)
			AND ($8 = '' OR t.counterparty ILIKE $8 OR t.description ILIKE $8 OR t.reference ILIKE $8) ORDER BY t.id LIMIT $9`,
			orgID, siteID, after, f.ImportID, f.MatchStatus, f.From, f.To, q, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTx(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	items, next := tenancy.Page(out, limit, func(t Transaction) uuid.UUID { return t.ID })
	return items, next, nil
}

// GetTransaction returns one line (money.read).
func (s *Service) GetTransaction(ctx context.Context, orgID, txnID uuid.UUID) (Transaction, error) {
	var t Transaction
	err := s.run(ctx, orgID, "bank transaction", func(tx pgx.Tx) (err error) {
		if t, err = scanTx(tx.QueryRow(ctx, txSel+` WHERE t.org_id=$1 AND t.id=$2`, orgID, txnID)); err != nil {
			return err
		}
		return ledger.RequireSite(ctx, tx, ledger.PermMoneyRead, t.SiteID)
	})
	return t, err
}

// MatchInput matches a line.
type MatchInput struct {
	Debtor        *billing.Debtor `json:"debtor"`
	PaymentID     *uuid.UUID      `json:"payment_id"`
	PayerPersonID *uuid.UUID      `json:"payer_person_id"`
	// Allocations may name charges to pay (in order); amounts are advisory, the line amount is allocated in that order.
	Allocations []struct {
		ChargeID uuid.UUID `json:"charge_id"`
	} `json:"allocations"`
	ExpenseID *uuid.UUID `json:"expense_id"`
}

type txRow struct {
	site       uuid.UUID
	importID   uuid.UUID
	asset      uuid.UUID
	status     string
	amount     int64
	bookedOn   string
	reference  *string
	desc       *string
	paymentID  *uuid.UUID
	suggestion []byte
}

func lockTx(ctx context.Context, tx pgx.Tx, orgID, txnID uuid.UUID) (txRow, error) {
	var r txRow
	err := tx.QueryRow(ctx, `SELECT t.site_id, t.import_id, i.asset_account_id, t.match_status, t.amount, t.booked_on::text, t.reference, t.description, t.payment_id, t.suggestions
		FROM bank_transaction t JOIN bank_statement_import i ON i.org_id = t.org_id AND i.id = t.import_id WHERE t.org_id=$1 AND t.id=$2 FOR UPDATE OF t`, orgID, txnID).
		Scan(&r.site, &r.importID, &r.asset, &r.status, &r.amount, &r.bookedOn, &r.reference, &r.desc, &r.paymentID, &r.suggestion)
	return r, err
}

// Match settles an incoming line (payments.record): it creates and confirms a
// bank-transfer payment for the debtor, allocated as usual, or confirms the
// declared payment payment_id with the line. Everything commits together.
func (s *Service) Match(ctx context.Context, orgID, txnID uuid.UUID, in MatchInput) (Transaction, error) {
	var out Transaction
	err := s.run(ctx, orgID, "bank transaction", func(tx pgx.Tx) error {
		r, err := lockTx(ctx, tx, orgID, txnID)
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsRecord, r.site); err != nil {
			return err
		}
		if r.status == "matched" || r.status == "ignored" {
			return problem.Conflict("the line is already " + r.status)
		}
		if in.ExpenseID != nil {
			// an outgoing line settles an expense that was posted without the bank: the link is
			// the match (no payment is created; the expense already posted its entry)
			if r.amount >= 0 {
				return bad("expense_id", "invalid_value")
			}
			var amt int64
			var st string
			var xsite uuid.UUID
			err := tx.QueryRow(ctx, `SELECT amount, status, site_id FROM expense WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, *in.ExpenseID).Scan(&amt, &st, &xsite)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && xsite != r.site) {
				return bad("expense_id", "invalid_value")
			}
			if err != nil {
				return err
			}
			if st != "posted" {
				return problem.Conflict("only a posted expense can be linked to a bank line")
			}
			if amt != -r.amount {
				return problem.Validation(problem.Field{Field: "expense_id", Code: "amount_mismatch"})
			}
			if _, err := tx.Exec(ctx, `UPDATE bank_transaction SET match_status='matched', expense_id=$3, payment_id=NULL, matched_by=$4, matched_at=now(), updated_at=now() WHERE org_id=$1 AND id=$2`,
				orgID, txnID, *in.ExpenseID, ledger.Actor(ctx)); err != nil {
				if pe := new(pgconn.PgError); errors.As(err, &pe) && pe.Code == "23505" {
					return problem.Conflict("the expense is already linked to another bank line")
				}
				return err
			}
			if err := ledger.Audit(ctx, tx, orgID, r.site, "bank_transaction.match", "bank_transaction", txnID, map[string]any{"status": r.status}, map[string]any{"status": "matched", "expense_id": *in.ExpenseID}); err != nil {
				return err
			}
			out, err = scanTx(tx.QueryRow(ctx, txSel+` WHERE t.org_id=$1 AND t.id=$2`, orgID, txnID))
			return err
		}
		if r.amount <= 0 {
			return bad("amount", "invalid_value")
		}
		site, err := tenancy.LoadSite(ctx, tx, orgID, r.site, false, false)
		if err != nil {
			return err
		}
		pay := s.pay.WithTx(tx)
		var chargeIDs []uuid.UUID
		for _, a := range in.Allocations {
			chargeIDs = append(chargeIDs, a.ChargeID)
		}
		var pid uuid.UUID
		switch {
		case in.PaymentID != nil:
			dp, err := pay.Get(ctx, orgID, *in.PaymentID)
			if err != nil {
				return err
			}
			if dp.SiteID != r.site || dp.Status != "pending_declared" {
				return problem.Conflict("only a declared payment of this site can be confirmed with a line")
			}
			if dp.Net.Amount != r.amount {
				return problem.Validation(problem.Field{Field: "payment_id", Code: "amount_mismatch"})
			}
			if _, err := tx.Exec(ctx, `UPDATE payment SET asset_account_id=$3 WHERE org_id=$1 AND id=$2`, orgID, dp.ID, r.asset); err != nil {
				return err
			}
			cp, err := pay.Confirm(ctx, orgID, dp.ID, r.asset, r.bookedOn, chargeIDs)
			if err != nil {
				return err
			}
			pid = cp.ID
		case in.Debtor != nil:
			ref := r.reference
			if ref == nil {
				ref = r.desc
			}
			cp, err := pay.Record(ctx, orgID, payments.Input{SiteID: r.site, Debtor: *in.Debtor, PayerPersonID: in.PayerPersonID, Method: "bank_transfer",
				Amount: money.New(r.amount, ledger.Currency(site)), ReceivedOn: r.bookedOn, AssetAccountID: &r.asset, Reference: ref, ChargeIDs: chargeIDs})
			if err != nil {
				return err
			}
			pid = cp.ID
		default:
			return bad("debtor", "required")
		}
		if _, err := tx.Exec(ctx, `UPDATE bank_transaction SET match_status='matched', payment_id=$3, matched_by=$4, matched_at=now(), updated_at=now() WHERE org_id=$1 AND id=$2`, orgID, txnID, pid, ledger.Actor(ctx)); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, r.site, "bank_transaction.match", "bank_transaction", txnID, map[string]any{"status": r.status}, map[string]any{"status": "matched", "payment_id": pid}); err != nil {
			return err
		}
		out, err = scanTx(tx.QueryRow(ctx, txSel+` WHERE t.org_id=$1 AND t.id=$2`, orgID, txnID))
		return err
	})
	return out, err
}

// Unmatch reverses a match (payments.record): the payment the match created or
// confirmed is voided (its entry reversed, allocations released) and the line is open again.
func (s *Service) Unmatch(ctx context.Context, orgID, txnID uuid.UUID, reason string) (Transaction, error) {
	var out Transaction
	err := s.run(ctx, orgID, "bank transaction", func(tx pgx.Tx) error {
		r, err := lockTx(ctx, tx, orgID, txnID)
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsRecord, r.site); err != nil {
			return err
		}
		if r.status != "matched" && r.status != "ignored" {
			return problem.Conflict("the line is not matched")
		}
		if r.paymentID != nil {
			why := reason
			if why == "" {
				why = "bank line unmatched"
			}
			if _, err := s.pay.WithTx(tx).Void(ctx, orgID, *r.paymentID, why); err != nil {
				return err
			}
		}
		next := "unmatched"
		var sug []Suggestion
		_ = json.Unmarshal(r.suggestion, &sug)
		if len(sug) > 0 {
			next = "suggested"
		}
		if _, err := tx.Exec(ctx, `UPDATE bank_transaction SET match_status=$3, payment_id=NULL, expense_id=NULL, note=$4, matched_by=NULL, matched_at=NULL, updated_at=now() WHERE org_id=$1 AND id=$2`, orgID, txnID, next, nilIfEmpty(reason)); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, r.site, "bank_transaction.unmatch", "bank_transaction", txnID, map[string]any{"status": r.status, "payment_id": r.paymentID}, map[string]any{"status": next, "reason": reason}); err != nil {
			return err
		}
		out, err = scanTx(tx.QueryRow(ctx, txSel+` WHERE t.org_id=$1 AND t.id=$2`, orgID, txnID))
		return err
	})
	return out, err
}

// Ignore sets a line aside (payments.record), for example a bank fee or a transfer that is not income.
func (s *Service) Ignore(ctx context.Context, orgID, txnID uuid.UUID, reason string) (Transaction, error) {
	var out Transaction
	err := s.run(ctx, orgID, "bank transaction", func(tx pgx.Tx) error {
		r, err := lockTx(ctx, tx, orgID, txnID)
		if err != nil {
			return err
		}
		if err := ledger.RequireSite(ctx, tx, ledger.PermPaymentsRecord, r.site); err != nil {
			return err
		}
		if r.status == "matched" {
			return problem.Conflict("the line is matched; unmatch it first")
		}
		if _, err := tx.Exec(ctx, `UPDATE bank_transaction SET match_status='ignored', note=$3, updated_at=now() WHERE org_id=$1 AND id=$2`, orgID, txnID, nilIfEmpty(reason)); err != nil {
			return err
		}
		if err := ledger.Audit(ctx, tx, orgID, r.site, "bank_transaction.ignore", "bank_transaction", txnID, map[string]any{"status": r.status}, map[string]any{"status": "ignored", "reason": reason}); err != nil {
			return err
		}
		out, err = scanTx(tx.QueryRow(ctx, txSel+` WHERE t.org_id=$1 AND t.id=$2`, orgID, txnID))
		return err
	})
	return out, err
}

// Reconciliation compares a statement with the ledger.
type Reconciliation struct {
	ImportID         uuid.UUID    `json:"import_id"`
	StatementOpening *money.Money `json:"statement_opening"`
	StatementClosing *money.Money `json:"statement_closing"`
	LedgerOpening    money.Money  `json:"ledger_opening"`
	LedgerClosing    money.Money  `json:"ledger_closing"`
	Difference       money.Money  `json:"difference"`
	// LinesTotal is the sum of all lines; StatementCheck is opening + lines - closing (nil without both balances; 0 when the statement is consistent).
	LinesTotal      money.Money  `json:"lines_total"`
	StatementCheck  *money.Money `json:"statement_check"`
	MatchedTotal    money.Money  `json:"matched_total"`
	UnmatchedTotal  money.Money  `json:"unmatched_total"`
	IgnoredTotal    money.Money  `json:"ignored_total"`
	MatchedCount    int64        `json:"matched_count"`
	UnmatchedCount  int64        `json:"unmatched_count"`
	IgnoredCount    int64        `json:"ignored_count"`
	LedgerMovement  money.Money  `json:"ledger_movement"`
	StatementMoveBy money.Money  `json:"statement_movement"`
}

// Reconciliation returns the balances of an import (money.read): the statement
// opening and closing, the ledger balance of the bank account at the start and
// end of the statement period, the matched and unmatched totals, and the
// difference between statement movement and ledger movement (it shrinks to
// zero as the lines are matched and nothing else was posted to the account).
func (s *Service) Reconciliation(ctx context.Context, orgID, importID uuid.UUID) (Reconciliation, error) {
	var rc Reconciliation
	err := s.run(ctx, orgID, "bank statement import", func(tx pgx.Tx) error {
		im, err := s.loadImport(ctx, tx, orgID, importID, ledger.PermMoneyRead)
		if err != nil {
			return err
		}
		cur := im.currency
		rc = Reconciliation{ImportID: importID, StatementOpening: im.OpeningBalance, StatementClosing: im.ClosingBalance}
		var total, matched, unmatched, ignored int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0)::bigint,
			COALESCE(SUM(amount) FILTER (WHERE match_status='matched'),0)::bigint, COALESCE(SUM(amount) FILTER (WHERE match_status IN ('unmatched','suggested')),0)::bigint, COALESCE(SUM(amount) FILTER (WHERE match_status='ignored'),0)::bigint,
			count(*) FILTER (WHERE match_status='matched'), count(*) FILTER (WHERE match_status IN ('unmatched','suggested')), count(*) FILTER (WHERE match_status='ignored')
			FROM bank_transaction WHERE org_id=$1 AND import_id=$2`, orgID, importID).Scan(&total, &matched, &unmatched, &ignored, &rc.MatchedCount, &rc.UnmatchedCount, &rc.IgnoredCount); err != nil {
			return err
		}
		mk := func(a int64) money.Money { return money.New(a, cur) }
		rc.LinesTotal, rc.MatchedTotal, rc.UnmatchedTotal, rc.IgnoredTotal = mk(total), mk(matched), mk(unmatched), mk(ignored)
		before, upTo := "", ""
		if im.PeriodFrom != nil {
			d, _ := ledger.ParseDate(*im.PeriodFrom)
			before = ledger.FormatDate(d.AddDate(0, 0, -1))
		}
		if im.PeriodTo != nil {
			upTo = *im.PeriodTo
		}
		bal := func(to string) (int64, error) {
			if to == "" && before != "" { // no period: nothing before the start
				return 0, nil
			}
			d, c, err := ledger.Balance(ctx, tx, orgID, im.SiteID, im.AssetAccountID, ledger.BalanceFilter{To: to})
			return d - c, err
		}
		lo, err := bal(before)
		if err != nil {
			return err
		}
		lc, err := bal(upTo)
		if err != nil {
			return err
		}
		rc.LedgerOpening, rc.LedgerClosing, rc.LedgerMovement = mk(lo), mk(lc), mk(lc-lo)
		stmtMove := total
		if im.OpeningBalance != nil && im.ClosingBalance != nil {
			stmtMove = im.ClosingBalance.Amount - im.OpeningBalance.Amount
			chk := mk(im.OpeningBalance.Amount + total - im.ClosingBalance.Amount)
			rc.StatementCheck = &chk
		}
		rc.StatementMoveBy = mk(stmtMove)
		rc.Difference = mk(stmtMove - (lc - lo))
		return nil
	})
	return rc, err
}

// PruneRaw strips the parsed source row of lines older than RawRetention and
// returns how many were stripped (platform job).
func PruneRaw(ctx context.Context, d interface {
	InPlatformTx(context.Context, func(pgx.Tx) error) error
}, now time.Time) (int64, error) {
	var n int64
	err := d.InPlatformTx(ctx, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE bank_transaction SET raw = NULL WHERE raw IS NOT NULL AND created_at < $1`, now.Add(-RawRetention))
		n = ct.RowsAffected()
		return err
	})
	return n, err
}
