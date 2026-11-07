package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Line is one line of a journal entry: exactly one of Debit and Credit is positive.
type Line struct {
	AccountID  uuid.UUID
	FundID     uuid.UUID
	Debit      int64
	Credit     int64
	UnitID     *uuid.UUID
	DebtorKind string
	PersonID   *uuid.UUID
	BlockID    *uuid.UUID
	VendorID   *uuid.UUID
}

// Entry is a journal entry to post.
type Entry struct {
	OrgID, SiteID   uuid.UUID
	EntryDate       string // civil date; the lock date applies to it
	DocumentDate    string // defaults to EntryDate
	Memo            string
	SourceType      string
	SourceID        *uuid.UUID
	ReversesEntryID *uuid.UUID
	Lines           []Line
}

// PostEntry validates and appends an entry inside tx and returns its id. The
// entry must balance overall and within every fund; the date must be after the
// site's lock date (409 locked_period). It is the only way money is written.
func PostEntry(ctx context.Context, tx pgx.Tx, e Entry) (uuid.UUID, error) {
	if len(e.Lines) < 2 {
		return uuid.Nil, problem.Validation(problem.Field{Field: "lines", Code: "too_few"})
	}
	if e.DocumentDate == "" {
		e.DocumentDate = e.EntryDate
	}
	perFund := map[uuid.UUID]int64{}
	var d, c int64
	for i, l := range e.Lines {
		if l.Debit < 0 || l.Credit < 0 || (l.Debit == 0) == (l.Credit == 0) {
			return uuid.Nil, problem.Validation(problem.Field{Field: fmt.Sprintf("lines[%d]", i), Code: "one_sided_amount_required"})
		}
		if l.AccountID == uuid.Nil || l.FundID == uuid.Nil {
			return uuid.Nil, problem.Validation(problem.Field{Field: fmt.Sprintf("lines[%d]", i), Code: "account_and_fund_required"})
		}
		d, c = d+l.Debit, c+l.Credit
		perFund[l.FundID] += l.Debit - l.Credit
	}
	if d != c {
		return uuid.Nil, problem.Validation(problem.Field{Field: "lines", Code: "unbalanced", Params: map[string]any{"debit": d, "credit": c}})
	}
	for _, v := range perFund {
		if v != 0 {
			return uuid.Nil, problem.Validation(problem.Field{Field: "lines", Code: "unbalanced_within_fund"})
		}
	}
	var lock *string
	if err := tx.QueryRow(ctx, `SELECT lock_date::text FROM site WHERE org_id = $1 AND id = $2 FOR SHARE`, e.OrgID, e.SiteID).Scan(&lock); err != nil {
		return uuid.Nil, err
	}
	if lock != nil && e.EntryDate <= *lock {
		return uuid.Nil, Locked(e.EntryDate, *lock)
	}
	fy, err := ensureFiscalYear(ctx, tx, e.OrgID, e.SiteID, e.EntryDate)
	if err != nil {
		return uuid.Nil, err
	}
	entryID := id.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO journal_entry (id, org_id, site_id, entry_date, document_date, fiscal_year_id, memo, source_type, source_id, posted_by, reverses_entry_id)
		VALUES ($1,$2,$3,$4::date,$5::date,$6,$7,$8,$9,$10,$11)`,
		entryID, e.OrgID, e.SiteID, e.EntryDate, e.DocumentDate, fy, e.Memo, e.SourceType, e.SourceID, Actor(ctx), e.ReversesEntryID); err != nil {
		return uuid.Nil, DBProblem(err, "journal entry")
	}
	batch := &pgx.Batch{}
	for _, l := range e.Lines {
		var kind *string
		if l.DebtorKind != "" {
			kind = &l.DebtorKind
		}
		batch.Queue(`INSERT INTO journal_line (id, org_id, site_id, entry_id, entry_date, account_id, fund_id, debit, credit, unit_id, debtor_kind, person_id, block_id, vendor_id)
			VALUES ($1,$2,$3,$4,$5::date,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			id.New(), e.OrgID, e.SiteID, entryID, e.EntryDate, l.AccountID, l.FundID, l.Debit, l.Credit, l.UnitID, kind, l.PersonID, l.BlockID, l.VendorID)
	}
	res := tx.SendBatch(ctx, batch)
	for range e.Lines {
		if _, err := res.Exec(); err != nil {
			_ = res.Close()
			return uuid.Nil, DBProblem(err, "journal line")
		}
	}
	if err := res.Close(); err != nil {
		return uuid.Nil, DBProblem(err, "journal line")
	}
	return entryID, nil
}

// ensureFiscalYear returns the fiscal year containing day, creating it from
// the site's fiscal_year_start_month when missing.
func ensureFiscalYear(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, day string) (uuid.UUID, error) {
	var fy uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM fiscal_year WHERE org_id = $1 AND site_id = $2 AND $3::date BETWEEN start_on AND end_on`, orgID, siteID, day).Scan(&fy)
	if err == nil {
		return fy, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	var startMonth int
	if err := tx.QueryRow(ctx, `SELECT fiscal_year_start_month FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&startMonth); err != nil {
		return uuid.Nil, err
	}
	d, err := ParseDate(day)
	if err != nil {
		return uuid.Nil, problem.Validation(problem.Field{Field: "entry_date", Code: "invalid_format"})
	}
	y := d.Year()
	if int(d.Month()) < startMonth {
		y--
	}
	start := time.Date(y, time.Month(startMonth), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, -1)
	fy = id.New()
	err = tx.QueryRow(ctx, `INSERT INTO fiscal_year (id, org_id, site_id, start_on, end_on) VALUES ($1,$2,$3,$4::date,$5::date)
		ON CONFLICT (org_id, site_id, start_on) DO UPDATE SET updated_at = fiscal_year.updated_at RETURNING id`,
		fy, orgID, siteID, FormatDate(start), FormatDate(end)).Scan(&fy)
	return fy, err
}

// Reverse posts the mirror image of an entry dated entryDate (today, rule 3),
// keeping the original document date, with reverses_entry_id set. An entry is
// reversed at most once (409 conflict), and a reversal cannot be reversed.
func Reverse(ctx context.Context, tx pgx.Tx, orgID, siteID, entryID uuid.UUID, entryDate, memo string) (uuid.UUID, error) {
	var docDate, srcType string
	var srcID, reverses *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT document_date::text, source_type, source_id, reverses_entry_id FROM journal_entry WHERE org_id = $1 AND site_id = $2 AND id = $3`,
		orgID, siteID, entryID).Scan(&docDate, &srcType, &srcID, &reverses); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, problem.NotFound("no such journal entry")
		}
		return uuid.Nil, err
	}
	if reverses != nil {
		return uuid.Nil, problem.Conflict("a reversal cannot be reversed; post a new entry instead")
	}
	rows, err := tx.Query(ctx, `SELECT account_id, fund_id, debit, credit, unit_id, COALESCE(debtor_kind, ''), person_id, block_id, vendor_id
		FROM journal_line WHERE org_id = $1 AND entry_id = $2 ORDER BY id`, orgID, entryID)
	if err != nil {
		return uuid.Nil, err
	}
	var lines []Line
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.AccountID, &l.FundID, &l.Debit, &l.Credit, &l.UnitID, &l.DebtorKind, &l.PersonID, &l.BlockID, &l.VendorID); err != nil {
			rows.Close()
			return uuid.Nil, err
		}
		l.Debit, l.Credit = l.Credit, l.Debit
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	if memo == "" {
		memo = "Reversal"
	}
	rid, err := PostEntry(ctx, tx, Entry{OrgID: orgID, SiteID: siteID, EntryDate: entryDate, DocumentDate: docDate, Memo: memo,
		SourceType: srcType + ".reversal", SourceID: srcID, ReversesEntryID: &entryID, Lines: lines})
	if err != nil && tenancyUnique(err) {
		return uuid.Nil, problem.Conflict("the entry was already reversed")
	}
	return rid, err
}

func tenancyUnique(err error) bool {
	pe := problem.From(err)
	return pe.Status == 409
}

// NextNumber takes the next gap-free number of kind for the site and year
// under a row lock (receipts, M5): the row is created on first use.
func NextNumber(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, kind string, year int) (int, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO sequence (org_id, site_id, kind, year) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, orgID, siteID, kind, year); err != nil {
		return 0, err
	}
	var n int
	err := tx.QueryRow(ctx, `UPDATE sequence SET last = last + 1 WHERE org_id = $1 AND site_id = $2 AND kind = $3 AND year = $4 RETURNING last`,
		orgID, siteID, kind, year).Scan(&n)
	return n, err
}

// BalanceFilter narrows a balance query.
type BalanceFilter struct {
	UnitID     *uuid.UUID
	DebtorKind string
	PersonID   *uuid.UUID
	FundID     *uuid.UUID
	From, To   string // civil dates, inclusive; empty is open
}

// Balance returns Σ debit and Σ credit of an account over the journal index
// (there is no balance cache). Receivable balances are debit − credit.
func Balance(ctx context.Context, tx pgx.Tx, orgID, siteID, accountID uuid.UUID, f BalanceFilter) (debit, credit int64, err error) {
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(debit), 0)::bigint, COALESCE(SUM(credit), 0)::bigint FROM journal_line
		WHERE org_id = $1 AND site_id = $2 AND account_id = $3
		  AND ($4::uuid IS NULL OR unit_id = $4) AND ($5 = '' OR debtor_kind = $5) AND ($6::uuid IS NULL OR person_id = $6)
		  AND ($7::uuid IS NULL OR fund_id = $7) AND ($8 = '' OR entry_date >= $8::date) AND ($9 = '' OR entry_date <= $9::date)`,
		orgID, siteID, accountID, f.UnitID, f.DebtorKind, f.PersonID, f.FundID, f.From, f.To).Scan(&debit, &credit)
	return
}
