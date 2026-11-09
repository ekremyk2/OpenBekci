// Package sdktest is the test harness of the pack SDK (packs plan 3.11). It
// boots the core against the local PostgreSQL test database (the same
// throwaway databases the core's own integration tests use), runs the core and
// the pack's migrations, creates an organization and a site from the money
// golden fixture (a site with units, people and a chart), enables the pack for
// the organization and drives it through the real registry:
//
//	h := sdktest.New(t, example.New(), sdktest.WithSettings(map[string]any{...}))
//	unit := h.Unit("A-1")
//	h.PostCharge(unit, 10000)
//	h.RecordPayment(unit, 10000)
//	rec := h.CallRoute("POST", "/callback", body, headers)
//	h.RunJob("example.cleanup", nil)
//	h.Emit(sdk.Event{Type: sdk.EventPaymentRecorded, IDs: ...})
//
// It lives inside the server module (it imports the core), so only packs built
// in this repository, or in a fork that adds itself to the Official slice, can
// use it; a mode that drives a built binary is a 1.0 item. The conformance
// suites ConformPaymentProvider and ConformStatementImporter build on it.
package sdktest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/billing"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/payments"
	"github.com/openbekci/openbekci/server/internal/core/payments/bankimport"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/moneytest"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

// Harness is a booted core with one pack enabled for one organization.
type Harness struct {
	T *testing.T
	// Env is the money golden fixture: database, organization, site, clock and
	// a manager context holding every permission organization-wide.
	Env *moneytest.Env
	// Host is the pack runtime under test.
	Host *packs.Host
	// Pack is the pack under test and PackID its id.
	Pack   sdk.Pack
	PackID string
	// Org and Site are the organization and the site of the fixture.
	Org  sdk.ID
	Site sdk.ID
	// Payments, Billing and BankImport are the core services the helpers use.
	Payments   *payments.Service
	Billing    *billing.Service
	BankImport *bankimport.Service
	Tenancy    *tenancy.Service
	// Router serves the pack routes and webhooks exactly as the server mounts them.
	Router http.Handler
	// Jobs is an insert-only River client: events become real jobs, which Emit then runs.
	Jobs *jobs.Client

	settings map[string]any
	skipEnab bool
}

// Option configures New.
type Option func(*Harness)

// WithSettings sets the organization-scope settings the pack is enabled with.
func WithSettings(values map[string]any) Option { return func(h *Harness) { h.settings = values } }

// WithoutEnable leaves the pack disabled; call Enable yourself.
func WithoutEnable() Option { return func(h *Harness) { h.skipEnab = true } }

var testKey = []byte("sdktest-encryption-key-0123456789abcdef")

// New boots the harness: database, core and pack migrations, the fixture
// organization (its country set to the pack's first country, if any) and
// site, the pack host started, and the pack enabled.
func New(t *testing.T, pack sdk.Pack, opts ...Option) *Harness {
	t.Helper()
	env := moneytest.New(t)
	h := &Harness{T: t, Env: env, Pack: pack, PackID: pack.Manifest().ID, Org: sdk.ID(env.OrgID.String()), Site: sdk.ID(env.SiteID.String())}
	for _, o := range opts {
		o(h)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if c := pack.Manifest().Countries; len(c) > 0 {
		env.Exec(`UPDATE organization SET country_code = $2 WHERE id = $1`, env.OrgID, c[0])
	}
	signer := storage.NewSigner(testKey)
	fs := files.NewService(env.DB, storage.NewLocal(t.TempDir(), signer), files.Config{Signer: signer, Log: log})
	h.Payments = payments.NewService(env.DB, env.Clock.Now, nil)
	h.Billing = billing.NewService(env.DB, tenancy.NewService(env.DB), nil)
	h.BankImport = bankimport.NewService(env.DB, h.Payments, env.Clock.Now)

	host, err := packs.New(packs.Config{DB: env.DB, Log: log, EncryptionKey: testKey, Payments: h.Payments, Files: fs, Notifier: jobs.DiscardNotifier{}, Now: env.Clock.Now}, []sdk.Pack{pack})
	if err != nil {
		t.Fatalf("sdktest: packs.New: %v", err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("sdktest: the pack failed to start: %v", err)
	}
	if err := host.MigrateUp(context.Background()); err != nil {
		t.Fatalf("sdktest: the pack's migrations failed: %v", err)
	}
	host.AttachPayments(h.Payments)
	h.BankImport.SetPacks(host.BankImporters())
	h.Host = host
	h.Tenancy = tenancy.NewService(env.DB).WithSiteHook(host)
	if h.Jobs, err = jobs.New(env.DB.WorkerPool(), log, jobs.Options{Register: host.RegisterWorkers}); err != nil {
		t.Fatalf("sdktest: river client: %v", err)
	}
	host.SetJobs(h.Jobs)
	r := chi.NewRouter()
	host.Mount(r)
	h.Router = r
	if !h.skipEnab {
		h.Enable(h.settings)
	}
	return h
}

// Ctx returns a context for core calls: a manager holding every permission.
func (h *Harness) Ctx() context.Context { return h.Env.Ctx }

// PackCtx returns a context as the host gives it to pack code: carrying the organization.
func (h *Harness) PackCtx() context.Context { return sdk.WithOrg(context.Background(), h.Org) }

// Enable enables the pack with the given organization-scope settings and fails the test on error.
func (h *Harness) Enable(values map[string]any) packs.State {
	h.T.Helper()
	st, err := h.TryEnable(values)
	if err != nil {
		h.T.Fatalf("sdktest: enable %s: %v", h.PackID, err)
	}
	return st
}

// TryEnable enables the pack and returns the error, for tests of settings validation.
func (h *Harness) TryEnable(values map[string]any) (packs.State, error) {
	return h.Host.Enable(h.Ctx(), h.Env.OrgID, h.PackID, values)
}

// Disable disables the pack.
func (h *Harness) Disable() {
	h.T.Helper()
	if _, err := h.Host.Disable(h.Ctx(), h.Env.OrgID, h.PackID); err != nil {
		h.T.Fatalf("sdktest: disable %s: %v", h.PackID, err)
	}
}

// Unit adds a unit to the fixture site (arsa payi 0) and returns its id.
func (h *Harness) Unit(door string) sdk.ID { return sdk.ID(h.Env.Unit(door, 0).String()) }

// Person adds a person to the fixture.
func (h *Harness) Person(name string) sdk.ID { return sdk.ID(h.Env.Person(name).String()) }

// PostCharge posts a one-off charge of amount (minor units) to the owner side
// of a unit through the billing module, due on the fixture's current day.
func (h *Harness) PostCharge(unit sdk.ID, amount int64) sdk.ID {
	h.T.Helper()
	u, err := uuid.Parse(string(unit))
	if err != nil {
		h.T.Fatal(err)
	}
	ch, err := h.Billing.CreateCharge(ledger.SystemContext(h.Ctx(), h.Env.OrgID), h.Env.OrgID, billing.ChargeInput{
		SiteID: h.Env.SiteID, UnitID: u, ChargeType: "special_assessment", DebtorKind: "owner",
		Amount: money.New(amount, "TRY"), DueDate: h.Env.Clock.Now().Format("2006-01-02"), Description: "sdktest charge"})
	if err != nil {
		h.T.Fatalf("sdktest: PostCharge: %v", err)
	}
	return sdk.ID(ch.ID.String())
}

// RecordPayment records a manual bank transfer payment for the owner side of a
// unit through the payments module and returns its id.
func (h *Harness) RecordPayment(unit sdk.ID, amount int64) sdk.ID {
	h.T.Helper()
	u, err := uuid.Parse(string(unit))
	if err != nil {
		h.T.Fatal(err)
	}
	p, err := h.Payments.Record(h.Ctx(), h.Env.OrgID, payments.Input{
		SiteID: h.Env.SiteID, Debtor: billing.Debtor{UnitID: &u, Kind: "owner"}, Method: "bank_transfer",
		Amount: money.New(amount, "TRY"), ReceivedOn: h.Env.Clock.Now().Format("2006-01-02")})
	if err != nil {
		h.T.Fatalf("sdktest: RecordPayment: %v", err)
	}
	return sdk.ID(p.ID.String())
}

// RunJob runs a job of the pack for the fixture organization on this goroutine,
// as the worker would, and returns its error.
func (h *Harness) RunJob(kind string, payload []byte) error {
	return h.Host.RunJob(h.Ctx(), h.Env.OrgID, h.PackID, kind, payload, 1)
}

// CallRoute calls a pack route (a path relative to /v1/orgs/{org}/packs/{id}):
// the request goes through the same router the server mounts, without a
// session. Headers are name, value pairs.
func (h *Harness) CallRoute(method, path string, body []byte, headers ...string) *httptest.ResponseRecorder {
	h.T.Helper()
	return h.do(method, "/v1/orgs/"+h.Env.OrgID.String()+"/packs/"+h.PackID+path, body, headers)
}

// Webhook posts a delivery to the core's payment webhook route of a provider
// (POST .../payments/{provider}/webhook): the pack verifies it, the core
// records the events idempotently.
func (h *Harness) Webhook(provider string, body []byte, headers http.Header) *httptest.ResponseRecorder {
	h.T.Helper()
	var pairs []string
	for k, vs := range headers {
		for _, v := range vs {
			pairs = append(pairs, k, v)
		}
	}
	return h.CallRoute("POST", "/payments/"+provider+"/webhook", body, pairs...)
}

func (h *Harness) do(method, path string, body []byte, headers []string) *httptest.ResponseRecorder {
	h.T.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.7:4000"
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.Router.ServeHTTP(rec, req)
	return rec
}

// Emit delivers a core event as the core does: it is inserted as one job per
// subscriber of the enabled pack in a transaction (so River's dedupe applies),
// and the jobs are then run. Delivering the same event twice runs the handler
// once.
func (h *Harness) Emit(ev sdk.Event) {
	h.T.Helper()
	err := h.Env.DB.InTenantTx(context.Background(), h.Env.OrgID, func(tx pgx.Tx) error { return h.Host.Emit(context.Background(), tx, h.Env.OrgID, ev) })
	if err != nil {
		h.T.Fatalf("sdktest: Emit: %v", err)
	}
	h.RunPendingEvents()
}

// RunPendingEvents runs the pack event jobs queued so far.
func (h *Harness) RunPendingEvents() {
	h.T.Helper()
	var args []packs.EventArgs
	var ids []int64
	err := h.Env.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT id, args FROM river_job WHERE kind = 'pack.event' AND state IN ('available', 'scheduled', 'retryable') ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var jid int64
			var raw []byte
			if err := rows.Scan(&jid, &raw); err != nil {
				return err
			}
			var a packs.EventArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return err
			}
			ids, args = append(ids, jid), append(args, a)
		}
		return rows.Err()
	})
	if err != nil {
		h.T.Fatalf("sdktest: read event jobs: %v", err)
	}
	for _, a := range args {
		org, _ := uuid.Parse(a.Org)
		if err := h.Host.Deliver(context.Background(), org, a); err != nil {
			h.T.Fatalf("sdktest: event %s to %s: %v", a.Type, a.Sub, err)
		}
	}
	// the jobs ran here: complete them so a later call does not run them again
	if len(ids) > 0 {
		err = h.Env.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `UPDATE river_job SET state = 'completed', finalized_at = now() WHERE id = ANY($1)`, ids)
			return err
		})
		if err != nil {
			h.T.Fatalf("sdktest: complete event jobs: %v", err)
		}
	}
}

// ImportStatement uploads a statement through the core bank import module
// with an importer of the enabled pack, into the fixture site's bank account,
// and returns how many lines the core stored.
func (h *Harness) ImportStatement(importerID, filename string, data []byte) (int64, error) {
	h.T.Helper()
	var acc uuid.UUID
	err := h.Env.DB.InTenantTx(context.Background(), h.Env.OrgID, func(tx pgx.Tx) error {
		_, err := ledger.EnsureChart(context.Background(), tx, h.Env.OrgID, h.Env.SiteID)
		return err
	})
	if err != nil {
		return 0, err
	}
	h.Env.Query([]any{&acc}, `SELECT id FROM account WHERE org_id = $1 AND site_id = $2 AND subtype = 'bank'`, h.Env.OrgID, h.Env.SiteID)
	im, err := h.BankImport.Create(h.Ctx(), h.Env.OrgID, bankimport.CreateInput{SiteID: h.Env.SiteID, Importer: importerID, AssetAccountID: acc, Filename: filename, Data: data})
	if err != nil {
		return 0, err
	}
	return im.TransactionCount, nil
}

// Balance returns the debit and credit totals of an account of the fixture site by code.
func (h *Harness) Balance(code string) (debit, credit int64) {
	h.T.Helper()
	d, c, err := h.Host.Services(h.PackID).Ledger().Balance(h.PackCtx(), h.Site, code)
	if err != nil {
		h.T.Fatalf("sdktest: balance of %s: %v", code, err)
	}
	return d, c
}

// AssertLedgerInvariants fails the test unless the books of the fixture
// organization are sound: every journal entry balances (debits equal
// credits), every reversal mirrors the entry it reverses, and receipt numbers
// are gap-free. The conformance suites call it after every case, so a pack
// that finds a way to post an unbalanced or half-reversed entry cannot pass.
func (h *Harness) AssertLedgerInvariants() {
	h.T.Helper()
	if n := h.Int(`SELECT count(*) FROM (SELECT entry_id FROM journal_line WHERE org_id = $1 GROUP BY entry_id HAVING sum(debit) <> sum(credit)) x`, h.Env.OrgID); n != 0 {
		h.T.Errorf("sdktest: %d journal entries are unbalanced (debits differ from credits)", n)
	}
	if n := h.Int(`SELECT count(*) FROM journal_entry e WHERE e.org_id = $1 AND e.reverses_entry_id IS NOT NULL AND
		(SELECT COALESCE(sum(debit), 0) FROM journal_line WHERE org_id = e.org_id AND entry_id = e.id) <>
		(SELECT COALESCE(sum(credit), 0) FROM journal_line WHERE org_id = e.org_id AND entry_id = e.reverses_entry_id)`, h.Env.OrgID); n != 0 {
		h.T.Errorf("sdktest: %d reversals do not mirror the entry they reverse", n)
	}
	if n := h.Int(`SELECT count(*) FROM (SELECT site_id, left(receipt_no, 4) AS yr, count(*) AS c, max(substr(receipt_no, 6)::int) AS m FROM payment
		WHERE org_id = $1 AND receipt_no IS NOT NULL GROUP BY 1, 2) x WHERE c <> m`, h.Env.OrgID); n != 0 {
		h.T.Errorf("sdktest: receipt numbers of %d site-years have gaps or repeats", n)
	}
	if n := h.Int(`SELECT count(*) FROM payment p WHERE p.org_id = $1 AND p.status = 'confirmed' AND p.provider IS NOT NULL AND
		p.net <> p.gross - p.fee`, h.Env.OrgID); n != 0 {
		h.T.Errorf("sdktest: %d provider payments whose net is not gross minus fee", n)
	}
}

// Int runs a query returning one integer as the platform role (it bypasses row-level security).
func (h *Harness) Int(sql string, args ...any) int64 { return h.Env.Int(sql, args...) }

// Exec runs SQL as the platform role.
func (h *Harness) Exec(sql string, args ...any) { h.Env.Exec(sql, args...) }

// NewID returns a fresh id.
func NewID() sdk.ID { return sdk.ID(id.New().String()) }

// HasHeader is a small helper for fixtures: a header set with one entry.
func Header(kv ...string) http.Header {
	hd := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		hd.Set(kv[i], kv[i+1])
	}
	return hd
}

// Contains reports whether a recorder's body contains s.
func Contains(rec *httptest.ResponseRecorder, s string) bool {
	return strings.Contains(rec.Body.String(), s)
}
