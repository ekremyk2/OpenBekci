package privacy_test

import (
	"context"
	"io"
	"strings"

	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/privacy"
	"github.com/openbekci/openbekci/server/internal/moneytest"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

// readAs reads the export file as the user holding files.read; the resolver decides.
func readAs(fs *files.Service, e *moneytest.Env, user, file uuid.UUID) (string, error) {
	ctx := authz.WithGrants(authz.WithPrincipal(context.Background(), &authz.Principal{ID: user, UserID: user}),
		authz.Grants{OrgID: e.OrgID, Items: []authz.Grant{{Permission: files.PermRead, ScopeType: authz.ScopeOrg}}})
	d, err := fs.Open(ctx, e.OrgID, file, "")
	if err != nil {
		return "", err
	}
	defer d.Body.Close()
	b, err := io.ReadAll(d.Body)
	return string(b), err
}

type fakeNotifier struct{ got []jobs.NotifyArgs }

func (n *fakeNotifier) Notify(_ context.Context, _ pgx.Tx, a jobs.NotifyArgs) error {
	n.got = append(n.got, a)
	return nil
}

// account seeds a sign-in account with a session and links a new person to it.
func account(e *moneytest.Env, name string) (person, user uuid.UUID) {
	person, user = e.Person(name), id.New()
	e.Exec(`INSERT INTO "user" (id, email) VALUES ($1, $2)`, user, name+"@privacy.example.test")
	e.Exec(`INSERT INTO session (id, user_id, kind, token_hash, family_id, expires_at) VALUES ($1, $2, 'web_cookie', $3, $1, now() + interval '1 day')`, id.New(), user, []byte(id.New().String()))
	e.Exec(`UPDATE person SET user_id = $2, contact = '{"phone":"+905550000001"}', notes = 'allergic to cats', national_id_enc = '\x01', national_id_country = 'TR' WHERE id = $1`, person, user)
	return person, user
}

// openCharge gives the person's unit an open charge, so the person owes money.
func openCharge(e *moneytest.Env, person uuid.UUID) {
	unit := e.Unit("A-1", 0)
	e.Occupy(unit, person, "owner", "2020-01-01", "")
	fund, entry := id.New(), id.New()
	e.Exec(`INSERT INTO fund (id, org_id, site_id, code, name) VALUES ($1, $2, $3, 'operating', 'Operating')`, fund, e.OrgID, e.SiteID)
	e.Exec(`INSERT INTO journal_entry (id, org_id, site_id, entry_date, document_date, source_type) VALUES ($1, $2, $3, '2026-01-01', '2026-01-01', 'raw')`, entry, e.OrgID, e.SiteID)
	e.Exec(`INSERT INTO charge (id, org_id, site_id, unit_id, charge_type, debtor_kind, amount, due_date, fund_id, journal_entry_id)
		VALUES ($1, $2, $3, $4, 'dues', 'owner', 10000, '2026-01-10', $5, $6)`, id.New(), e.OrgID, e.SiteID, unit, fund, entry)
}

func TestIntegrationConsentDecisions(t *testing.T) {
	e := moneytest.New(t)
	_, user := account(e, "ayse")
	s := privacy.NewService(e.DB, privacy.Config{})
	ctx := context.Background()
	if err := s.Decide(ctx, e.OrgID, user, "core.privacy_notice", "1", privacy.Accepted); err != nil {
		t.Fatal(err)
	}
	acc, err := s.Accepted(ctx, user)
	if err != nil || acc[e.OrgID]["core.privacy_notice"] != "1" {
		t.Fatalf("accepted = %v, %v", acc, err)
	}
	if err := s.Decide(ctx, e.OrgID, user, "core.privacy_notice", "1", privacy.Declined); err != nil {
		t.Fatal(err)
	}
	st, _ := s.States(ctx, e.OrgID, user)
	if st["core.privacy_notice"].Decision != privacy.Declined {
		t.Fatalf("states = %v", st)
	}
	if acc, _ := s.Accepted(ctx, user); len(acc) != 0 {
		t.Fatalf("a declined text still counts as accepted: %v", acc)
	}
	if n := e.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action LIKE 'privacy.consent_%'`, e.OrgID); n != 2 {
		t.Fatalf("%d consent audit events, want 2", n)
	}
}

func TestIntegrationErase(t *testing.T) {
	e := moneytest.New(t)
	person, user := account(e, "mehmet")
	openCharge(e, person)
	s := privacy.NewService(e.DB, privacy.Config{})
	if err := s.Decide(context.Background(), e.OrgID, user, "core.privacy_notice", "1", privacy.Accepted); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Erase(moneytest.WithoutReauth(e.Ctx), e.OrgID, person, "request", true); err == nil || !strings.Contains(err.Error(), "reauth") {
		t.Fatalf("erase without re-authentication: %v", err)
	}
	if _, err := s.Erase(e.Ctx, e.OrgID, person, "request", false); err == nil || !strings.Contains(err.Error(), "open charges") {
		t.Fatalf("erase with an open charge: %v", err)
	}
	res, err := s.Erase(e.Ctx, e.OrgID, person, "Data subject request 2026/17", true)
	if err != nil || res.OpenItems != 1 {
		t.Fatalf("forced erase: %+v, %v", res, err)
	}
	var name, notes string
	var contact, sealed any
	var uid *uuid.UUID
	e.Query([]any{&name, &contact, &notes, &sealed, &uid}, `SELECT display_name, contact, COALESCE(notes, ''), national_id_enc, user_id FROM person WHERE id = $1`, person)
	if !strings.HasPrefix(name, "Former person ") || notes != "" || sealed != nil || uid != nil || len(contact.(map[string]any)) != 0 {
		t.Fatalf("person kept data: %q %v %q %v %v", name, contact, notes, sealed, uid)
	}
	if n := e.Int(`SELECT (SELECT count(*) FROM session WHERE user_id = $1) + (SELECT count(*) FROM consent WHERE user_id = $1)`, user); n != 0 {
		t.Fatalf("%d sessions and consents left", n)
	}
	var email, status string
	e.Query([]any{&email, &status}, `SELECT email, status FROM "user" WHERE id = $1`, user)
	if status != "disabled" || strings.Contains(email, "mehmet") {
		t.Fatalf("account kept: %s %s", email, status)
	}
	if n := e.Int(`SELECT count(*) FROM charge WHERE debtor_kind = 'owner' AND org_id = $1`, e.OrgID); n != 1 {
		t.Fatal("the ledger row went")
	}
	// audited as forced, and the free-text reason is not in the trail
	if n := e.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'privacy.erase' AND target_id = $2 AND diff->'forced'->>'after' = 'true' AND diff::text NOT LIKE '%2026/17%'`, e.OrgID, person); n != 1 {
		t.Fatalf("erase audit event missing or leaking the reason (%d)", n)
	}
	if _, err := s.EraseAsOperator(context.Background(), e.OrgID, person, "again", true); err == nil || !strings.Contains(err.Error(), "already erased") {
		t.Fatalf("second erase: %v", err)
	}
}

func TestIntegrationExportFilesArePrivate(t *testing.T) {
	e := moneytest.New(t)
	person, user := account(e, "zeynep")
	e.Occupy(e.Unit("B-2", 0), person, "tenant", "2024-01-01", "")
	signer := storage.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	fs := files.NewService(e.DB, storage.NewLocal(t.TempDir(), signer), files.Config{Signer: signer})
	fs.RegisterResolver(privacy.Scope, privacy.FileResolver())
	notes := &fakeNotifier{}
	s := privacy.NewService(e.DB, privacy.Config{Files: fs, Notifier: notes})
	manager, board, req := id.New(), id.New(), id.New()
	e.Exec(`INSERT INTO data_export (org_id, id, person_id, requested_by) VALUES ($1, $2, $3, $4)`, e.OrgID, req, person, manager)
	if err := s.RunExport(context.Background(), privacy.ExportArgs{OrgID: e.OrgID, PersonID: person, SiteID: e.SiteID, RequestID: req, RequestedBy: manager}); err != nil {
		t.Fatal(err)
	}
	var js, pdf uuid.UUID
	e.Query([]any{&js, &pdf}, `SELECT (SELECT id FROM file WHERE scope_id = $1 AND name LIKE '%.json.txt'), (SELECT id FROM file WHERE scope_id = $1 AND name LIKE '%.pdf')`, req)

	body, err := readAs(fs, e, user, js)
	if err != nil || !strings.Contains(body, "zeynep") || !strings.Contains(body, `"occupancies"`) || strings.Contains(body, "national_id_enc") {
		t.Fatalf("the person reads their bundle: %.200s, %v", body, err)
	}
	if b, err := readAs(fs, e, user, pdf); err != nil || !strings.HasPrefix(b, "%PDF") {
		t.Fatalf("pdf: %.10s, %v", b, err)
	}
	if _, err := readAs(fs, e, manager, js); err != nil {
		t.Fatalf("the requesting manager reads it: %v", err)
	}
	if n := e.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'privacy.export_read' AND target_id = $2`, e.OrgID, person); n != 1 {
		t.Fatalf("%d export_read audit events, want 1 (the person's own read is not one)", n)
	}
	if _, err := readAs(fs, e, board, js); err == nil {
		t.Fatal("a board member read somebody's export")
	}
	if len(notes.got) != 1 || notes.got[0].Type != "privacy.export_ready" || *notes.got[0].Recipients[0].PersonID != person {
		t.Fatalf("notification = %+v", notes.got)
	}
	if n := e.Int(`SELECT count(*) FROM file WHERE scope_id = $1 AND retention_until > now() + interval '6 days' AND retention_until < now() + interval '8 days'`, req); n != 2 {
		t.Fatalf("%d files with a 7 day expiry, want 2", n)
	}
}

func TestIntegrationPruneDeliveries(t *testing.T) {
	e := moneytest.New(t)
	person := e.Person("old")
	for i, st := range []string{"sent", "pending"} {
		n := id.New()
		e.Exec(`INSERT INTO notification (id, org_id, person_id, type) VALUES ($1, $2, $3, 'ticket.updated')`, n, e.OrgID, person)
		e.Exec(`INSERT INTO notification_delivery (id, org_id, notification_id, channel, status, updated_at) VALUES ($1, $2, $3, $4, $5, now() - interval '120 days')`,
			id.New(), e.OrgID, n, []string{"email", "sms"}[i], st)
	}
	s := privacy.NewService(e.DB, privacy.Config{Now: time.Now})
	n, err := s.Prune(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v; want the old sent row only", n, err)
	}
	if left := e.Int(`SELECT count(*) FROM notification_delivery WHERE status = 'pending'`); left != 1 {
		t.Fatal("a pending delivery was pruned")
	}
}
