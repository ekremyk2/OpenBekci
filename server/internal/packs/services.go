package packs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// services is the sdk.Services of one pack. Everything is bound to the
// organization in the call's context.
type services struct {
	h    *Host
	pack string
}

var _ sdk.Services = (*services)(nil)

func (s *services) Units() sdk.UnitReader         { return unitReader{s.h} }
func (s *services) People() sdk.PeopleReader      { return peopleReader{s.h} }
func (s *services) Ledger() sdk.LedgerReader      { return ledgerReader{s.h} }
func (s *services) Poster() sdk.LedgerPoster      { return poster{s.h, s.pack} }
func (s *services) Payments() sdk.PaymentRecorder { return recorder{s.h, s.pack} }
func (s *services) Notifier() sdk.Notifier        { return notifier{s.h, s.pack} }
func (s *services) Files() sdk.FileStore          { return fileStore{s.h} }
func (s *services) Settings() sdk.Settings        { return settingsReader{s.h, s.pack} }
func (s *services) Jobs() sdk.JobQueue            { return jobQueue{s.h, s.pack} }
func (s *services) DB() sdk.DB                    { return tenantDB{s.h} }
func (s *services) Log() *slog.Logger             { return s.h.log.With("pack", s.pack) }

// tenantTx runs fn in a tenant transaction of the context's organization.
func (h *Host) tenantTx(ctx context.Context, fn func(org uuid.UUID, tx pgx.Tx) error) error {
	org, err := orgFromCtx(ctx)
	if err != nil {
		return err
	}
	if h.cfg.DB == nil {
		return errors.New("packs: no database configured")
	}
	err = h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error { return fn(org, tx) })
	if errors.Is(err, pgx.ErrNoRows) {
		return problem.NotFound("not found")
	}
	return err
}

// ---- DB

type tenantDB struct{ h *Host }

func (d tenantDB) InTx(ctx context.Context, fn func(sdk.Tx) error) error {
	return d.h.tenantTx(ctx, func(_ uuid.UUID, tx pgx.Tx) error { return fn(txAdapter{tx}) })
}

type txAdapter struct{ tx pgx.Tx }

// errForbiddenSQL is returned for statements that would step out of the
// tenant transaction the host opened.
var errForbiddenSQL = errors.New("packs: this statement is not allowed in a pack transaction")

// guardSQL refuses statements that change the transaction's tenant context or
// session state (set_config, SET, RESET, the app.org_id setting, transaction
// control). Row-level security is what isolates tenants, and it keys on the
// app.org_id setting the host sets: a pack must not be able to rewrite it by
// accident. This is a guard against mistakes, not a sandbox: packs are trusted
// code compiled into the server.
func guardSQL(sql string) error {
	l := strings.ToLower(sql)
	if strings.Contains(l, "app.org_id") || strings.Contains(l, "set_config") || strings.Contains(l, "current_setting('app.") {
		return errForbiddenSQL
	}
	first, _, _ := strings.Cut(strings.TrimLeft(l, " \t\r\n;("), " ")
	switch strings.TrimRight(first, ";") {
	case "set", "reset", "begin", "commit", "rollback", "savepoint", "release", "start", "end", "abort", "prepare", "discard", "listen", "unlisten", "load":
		return errForbiddenSQL
	}
	return nil
}

func (t txAdapter) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	if err := guardSQL(sql); err != nil {
		return 0, err
	}
	tag, err := t.tx.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}

func (t txAdapter) Query(ctx context.Context, sql string, args ...any) (sdk.Rows, error) {
	if err := guardSQL(sql); err != nil {
		return nil, err
	}
	rows, err := t.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return rowsAdapter{rows}, nil
}

func (t txAdapter) QueryRow(ctx context.Context, sql string, args ...any) sdk.Row {
	if err := guardSQL(sql); err != nil {
		return errRow{err}
	}
	return rowAdapter{t.tx.QueryRow(ctx, sql, args...)}
}

// rowsAdapter exposes only the sdk.Rows methods: a pgx.Rows would let a pack
// reach the connection with a type assertion (Rows.Conn).
type rowsAdapter struct{ r pgx.Rows }

func (r rowsAdapter) Next() bool             { return r.r.Next() }
func (r rowsAdapter) Scan(dest ...any) error { return r.r.Scan(dest...) }
func (r rowsAdapter) Err() error             { return r.r.Err() }
func (r rowsAdapter) Close()                 { r.r.Close() }

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

type rowAdapter struct{ r pgx.Row }

func (r rowAdapter) Scan(dest ...any) error {
	err := r.r.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return sdk.ErrNoRows
	}
	return err
}

// ---- readers

type unitReader struct{ h *Host }

const unitCols = `id, site_id, door_number, type, area_m2::float8`

func scanUnit(r pgx.Row) (sdk.Unit, error) {
	var u sdk.Unit
	var id, site uuid.UUID
	if err := r.Scan(&id, &site, &u.DoorNumber, &u.Type, &u.AreaM2); err != nil {
		return u, err
	}
	u.ID, u.SiteID = sdk.ID(id.String()), sdk.ID(site.String())
	return u, nil
}

func (r unitReader) Get(ctx context.Context, unit sdk.ID) (sdk.Unit, error) {
	uu, err := uid(unit)
	if err != nil {
		return sdk.Unit{}, err
	}
	var out sdk.Unit
	err = r.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		out, err = scanUnit(tx.QueryRow(ctx, `SELECT `+unitCols+` FROM unit WHERE org_id = $1 AND id = $2`, org, uu))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fmt.Errorf("unit %s: %w", unit, sdk.ErrNotFound)
	}
	return out, err
}

func (r unitReader) List(ctx context.Context, site sdk.ID) ([]sdk.Unit, error) {
	su, err := uid(site)
	if err != nil {
		return nil, err
	}
	var out []sdk.Unit
	err = r.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+unitCols+` FROM unit WHERE org_id = $1 AND site_id = $2 ORDER BY door_number, id`, org, su)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			u, err := scanUnit(rows)
			if err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, err
}

type peopleReader struct{ h *Host }

const personCols = `p.id, p.display_name, COALESCE(p.contact->>'email', ''), COALESCE(p.contact->>'phone', '')`

func scanPerson(r pgx.Row, extra ...any) (sdk.Person, error) {
	var p sdk.Person
	var id uuid.UUID
	if err := r.Scan(append([]any{&id, &p.DisplayName, &p.Email, &p.Phone}, extra...)...); err != nil {
		return p, err
	}
	p.ID = sdk.ID(id.String())
	return p, nil
}

func (r peopleReader) Get(ctx context.Context, person sdk.ID) (sdk.Person, error) {
	pu, err := uid(person)
	if err != nil {
		return sdk.Person{}, err
	}
	var out sdk.Person
	err = r.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		out, err = scanPerson(tx.QueryRow(ctx, `SELECT `+personCols+` FROM person p WHERE p.org_id = $1 AND p.id = $2`, org, pu))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fmt.Errorf("person %s: %w", person, sdk.ErrNotFound)
	}
	return out, err
}

func (r peopleReader) Occupants(ctx context.Context, unit sdk.ID) ([]sdk.Occupant, error) {
	uu, err := uid(unit)
	if err != nil {
		return nil, err
	}
	var out []sdk.Occupant
	err = r.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+personCols+`, o.kind FROM occupancy o JOIN person p ON p.org_id = o.org_id AND p.id = o.person_id
			JOIN site s ON s.org_id = o.org_id AND s.id = o.site_id
			WHERE o.org_id = $1 AND o.unit_id = $2 AND o.valid_from <= (now() AT TIME ZONE s.timezone)::date
			  AND (o.valid_to IS NULL OR o.valid_to >= (now() AT TIME ZONE s.timezone)::date)
			ORDER BY o.kind, p.display_name, p.id`, org, uu)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			p, err := scanPerson(rows, &kind)
			if err != nil {
				return err
			}
			out = append(out, sdk.Occupant{Person: p, Kind: kind})
		}
		return rows.Err()
	})
	return out, err
}

type ledgerReader struct{ h *Host }

func (r ledgerReader) Accounts(ctx context.Context, site sdk.ID) ([]sdk.Account, error) {
	su, err := uid(site)
	if err != nil {
		return nil, err
	}
	var out []sdk.Account
	err = r.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		if _, err := ledger.EnsureChart(ctx, tx, org, su); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, code, name, type, COALESCE(subtype, '') FROM account WHERE org_id = $1 AND site_id = $2 AND NOT archived ORDER BY code`, org, su)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a sdk.Account
			var id uuid.UUID
			if err := rows.Scan(&id, &a.Code, &a.Name, &a.Type, &a.Subtype); err != nil {
				return err
			}
			a.ID = sdk.ID(id.String())
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (r ledgerReader) Balance(ctx context.Context, site sdk.ID, code string) (debit, credit int64, err error) {
	su, err := uid(site)
	if err != nil {
		return 0, 0, err
	}
	err = r.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		var acc uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM account WHERE org_id = $1 AND site_id = $2 AND code = $3`, org, su, code).Scan(&acc); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("account %q: %w", code, sdk.ErrNotFound)
			}
			return err
		}
		debit, credit, err = ledger.Balance(ctx, tx, org, su, acc, ledger.BalanceFilter{})
		return err
	})
	return debit, credit, err
}

// ---- poster

type poster struct {
	h    *Host
	pack string
}

func (p poster) Post(ctx context.Context, e sdk.Entry) (sdk.ID, error) {
	su, err := uid(e.Site)
	if err != nil {
		return "", err
	}
	var eid uuid.UUID
	err = p.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		ch, err := ledger.EnsureChart(ctx, tx, org, su)
		if err != nil {
			return err
		}
		lines := make([]ledger.Line, 0, len(e.Lines))
		for i, l := range e.Lines {
			acc := ch.AccountCode(l.AccountCode)
			if acc == uuid.Nil {
				return problem.Validation(problem.Field{Field: fmt.Sprintf("lines[%d].account_code", i), Code: "invalid_value"})
			}
			fund := ch.OperatingFund
			if l.Fund != "" {
				if fund = ch.Funds[l.Fund]; fund == uuid.Nil {
					return problem.Validation(problem.Field{Field: fmt.Sprintf("lines[%d].fund", i), Code: "invalid_value"})
				}
			}
			ll := ledger.Line{AccountID: acc, FundID: fund, Debit: l.Debit, Credit: l.Credit, DebtorKind: l.DebtorKind}
			if l.Unit != "" {
				u, err := uid(l.Unit)
				if err != nil {
					return err
				}
				ll.UnitID = &u
			}
			lines = append(lines, ll)
		}
		eid, err = ledger.PostEntry(ledger.SystemContext(ctx, org), tx, ledger.Entry{OrgID: org, SiteID: su, EntryDate: e.EntryDate, Memo: e.Memo, SourceType: "pack." + p.pack, Lines: lines})
		return err
	})
	return sdk.ID(eid.String()), err
}

// ---- payments

type recorder struct {
	h    *Host
	pack string
}

func (r recorder) svc() (*payments.Service, error) {
	if r.h.cfg.Payments == nil {
		return nil, errors.New("packs: payments are not configured")
	}
	return r.h.cfg.Payments, nil
}

func toMoney(m sdk.Money) money.Money { return money.New(m.Amount, money.Code(m.Currency)) }

func debtorOf(t sdk.PaymentTarget) (billing.Debtor, error) {
	u, err := uid(t.UnitID)
	if err != nil {
		return billing.Debtor{}, err
	}
	return billing.Debtor{UnitID: &u, Kind: t.DebtorKind}, nil
}

func (r recorder) RecordProviderPayment(ctx context.Context, p sdk.ProviderPayment) (sdk.RecordedPayment, error) {
	svc, err := r.svc()
	if err != nil {
		return sdk.RecordedPayment{}, err
	}
	org, err := orgFromCtx(ctx)
	if err != nil {
		return sdk.RecordedPayment{}, err
	}
	site, err := uid(p.Target.SiteID)
	if err != nil {
		return sdk.RecordedPayment{}, err
	}
	d, err := debtorOf(p.Target)
	if err != nil {
		return sdk.RecordedPayment{}, err
	}
	in := payments.ProviderInput{Provider: p.Provider, ProviderTxnID: p.ProviderTxnID, SiteID: site, Debtor: d,
		Gross: toMoney(p.Gross), Fee: toMoney(p.Fee), ReceivedOn: p.ReceivedOn}
	if p.Reference != "" {
		ref := p.Reference
		in.Reference = &ref
	}
	for _, c := range p.Target.ChargeIDs {
		cu, err := uid(c)
		if err != nil {
			return sdk.RecordedPayment{}, err
		}
		in.ChargeIDs = append(in.ChargeIDs, cu)
	}
	if in.Fee.Currency == "" {
		in.Fee = money.Zero(in.Gross.Currency)
	}
	res, err := svc.RecordProvider(ledger.SystemContext(ctx, org), org, in)
	if err != nil {
		return sdk.RecordedPayment{}, err
	}
	return sdk.RecordedPayment{PaymentID: sdk.ID(res.Payment.ID.String()), Duplicate: res.Duplicate}, nil
}

func (r recorder) RefundProviderPayment(ctx context.Context, rf sdk.ProviderRefund) (sdk.RefundOutcome, error) {
	svc, err := r.svc()
	if err != nil {
		return sdk.RefundOutcome{}, err
	}
	org, err := orgFromCtx(ctx)
	if err != nil {
		return sdk.RefundOutcome{}, err
	}
	p, dup, err := svc.RefundProvider(ledger.SystemContext(ctx, org), org, payments.ProviderRefundInput{Provider: rf.Provider, ProviderTxnID: rf.ProviderTxnID, RefundRef: rf.RefundRef, Reason: rf.Reason})
	if err != nil {
		return sdk.RefundOutcome{}, err
	}
	return sdk.RefundOutcome{PaymentID: sdk.ID(p.ID.String()), Duplicate: dup}, nil
}

func (r recorder) RecordSettlement(ctx context.Context, s sdk.ProviderSettlement) (sdk.SettlementOutcome, error) {
	svc, err := r.svc()
	if err != nil {
		return sdk.SettlementOutcome{}, err
	}
	org, err := orgFromCtx(ctx)
	if err != nil {
		return sdk.SettlementOutcome{}, err
	}
	site, err := uid(s.Site)
	if err != nil {
		return sdk.SettlementOutcome{}, err
	}
	entry, dup, err := svc.RecordSettlement(ledger.SystemContext(ctx, org), org, payments.SettlementInput{Provider: s.Provider, SiteID: site, Ref: s.Ref,
		BankAccountCode: s.BankAccountCode, SettledOn: s.SettledOn, Net: toMoney(s.Net)})
	if err != nil {
		return sdk.SettlementOutcome{}, err
	}
	return sdk.SettlementOutcome{EntryID: sdk.ID(entry.String()), Duplicate: dup}, nil
}

// ---- notifier

type notifier struct {
	h    *Host
	pack string
}

func (n notifier) Notify(ctx context.Context, nt sdk.Notification) error {
	if n.h.cfg.Notifier == nil {
		return errors.New("packs: notifications are not configured")
	}
	args := jobs.NotifyArgs{Type: nt.Type, Payload: nt.Payload, Link: nt.Link}
	for _, p := range nt.People {
		pu, err := uid(p)
		if err != nil {
			return err
		}
		args.Recipients = append(args.Recipients, jobs.Recipient{PersonID: &pu})
	}
	return n.h.tenantTx(ctx, func(org uuid.UUID, tx pgx.Tx) error {
		args.OrgID = org
		if nt.Site != "" {
			su, err := uid(nt.Site)
			if err != nil {
				return err
			}
			args.SiteID = &su
		}
		return n.h.cfg.Notifier.Notify(ctx, tx, args)
	})
}

// ---- files

type fileStore struct{ h *Host }

func (f fileStore) svc() (*files.Service, error) {
	if f.h.cfg.Files == nil {
		return nil, errors.New("packs: files are not configured")
	}
	return f.h.cfg.Files, nil
}

func (f fileStore) Put(ctx context.Context, nf sdk.NewFile) (sdk.ID, error) {
	svc, err := f.svc()
	if err != nil {
		return "", err
	}
	org, err := orgFromCtx(ctx)
	if err != nil {
		return "", err
	}
	p := files.UploadParams{OrgID: org, Name: nf.Name, Content: nf.Content}
	if nf.Site != "" {
		su, err := uid(nf.Site)
		if err != nil {
			return "", err
		}
		p.SiteID = &su
	}
	out, err := svc.UploadGenerated(ctx, p)
	if err != nil {
		return "", err
	}
	return sdk.ID(out.ID.String()), nil
}

func (f fileStore) Open(ctx context.Context, file sdk.ID) (io.ReadCloser, sdk.FileInfo, error) {
	svc, err := f.svc()
	if err != nil {
		return nil, sdk.FileInfo{}, err
	}
	org, err := orgFromCtx(ctx)
	if err != nil {
		return nil, sdk.FileInfo{}, err
	}
	fu, err := uid(file)
	if err != nil {
		return nil, sdk.FileInfo{}, err
	}
	rctx := authz.WithGrants(authz.WithPrincipal(ctx, &authz.Principal{}), authz.Grants{OrgID: org, Items: []authz.Grant{{Permission: files.PermRead, ScopeType: authz.ScopeOrg}}})
	dl, err := svc.Open(rctx, org, fu, "")
	if err != nil {
		return nil, sdk.FileInfo{}, err
	}
	if dl.Body == nil {
		return nil, sdk.FileInfo{}, errors.New("packs: the file store serves this file by redirect")
	}
	return dl.Body, sdk.FileInfo{Name: dl.Name, MIME: dl.Mime, Size: dl.Size}, nil
}
