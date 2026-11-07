package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// qInt runs a counting query through the platform pool.
func (p *pb) qInt(sql string, args ...any) int {
	p.t.Helper()
	var n int
	err := p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), sql, args...).Scan(&n)
	})
	if err != nil {
		p.t.Fatalf("query: %v\n%s", err, sql)
	}
	return n
}

// archiveOf exports a freshly seeded site of OrgA with a resident, a board
// member and an outgoing company's site manager (linked to a real account).
func (p *pb) archiveOf(t *testing.T) (siteID uuid.UUID, data []byte, resident uuid.UUID) {
	t.Helper()
	f := p.w.NewSite(t, p.org)
	resident = p.w.NewPerson(t, p.org)
	p.w.NewOccupancy(t, f, f.Unit, resident, "owner")
	board := p.w.NewPerson(t, p.org)
	p.w.Exec(t, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, site_owned, valid_from) VALUES ($1, $2, $3, 'board_member', 'site', $4, true, now() - interval '1 day')`, id.New(), p.org.ID, board, f.Site)
	mgr := p.w.NewPerson(t, p.org)
	mu, err := p.e.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: id.New().String() + "@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	p.w.Exec(t, `UPDATE person SET user_id = $2 WHERE id = $1`, mgr, mu.ID)
	p.w.Exec(t, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from) VALUES ($1, $2, $3, 'site_manager', 'site', $4, now() - interval '1 day')`, id.New(), p.org.ID, mgr, f.Site)
	_, data, err = p.e.tenSvc.SystemExportSite(context.Background(), p.org.ID, f.Site)
	if err != nil {
		t.Fatal(err)
	}
	return f.Site, data, resident
}

func TestIntegrationSiteImportCarriesNoCompanyRolesAndNoAccountLinks(t *testing.T) {
	p := newPB(t)
	siteID, data, _ := p.archiveOf(t)
	orgB := p.w.OrgB.ID
	linked0 := p.qInt(`SELECT count(*) FROM person WHERE org_id = $1 AND user_id IS NOT NULL`, orgB)
	people0 := p.qInt(`SELECT count(*) FROM person WHERE org_id = $1`, orgB)
	rec, err := p.e.tenSvc.SystemImportSite(context.Background(), orgB, data)
	if err != nil {
		t.Fatalf("import of an archive signed by this instance: %v", err)
	}
	if rec.Summary["untrusted"] != false {
		t.Fatalf("summary: %v", rec.Summary)
	}
	// the legitimate-company leak: no site_manager (nor any role membership) comes along
	if n := p.qInt(`SELECT count(*) FROM membership WHERE org_id = $1 AND role = 'site_manager'`, orgB); n != 0 {
		t.Fatalf("the outgoing company's site_manager membership was imported (%d)", n)
	}
	if n := p.qInt(`SELECT count(*) FROM membership WHERE org_id = $1 AND role = 'board_member' AND site_owned AND scope_id = $2`, orgB, siteID); n != 1 {
		t.Fatalf("the board membership belongs to the site and must come along, got %d", n)
	}
	// the archive never links an account
	if n := p.qInt(`SELECT count(*) FROM person WHERE org_id = $1 AND user_id IS NOT NULL`, orgB); n != linked0 {
		t.Fatalf("%d imported people are linked to an account", n)
	}
	if n := p.qInt(`SELECT count(*) FROM person WHERE org_id = $1`, orgB); n != people0+2 { // resident and board member
		t.Fatalf("people imported = %d", n)
	}
}

func TestIntegrationSiteImportRefusesUnsignedAndTamperedArchives(t *testing.T) {
	p := newPB(t)
	_, data, resident := p.archiveOf(t)
	orgB := p.w.OrgB.ID
	var a map[string]any
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	org0 := p.qInt(`SELECT count(*) FROM membership WHERE org_id = $1 AND scope_type = 'org'`, orgB)
	linked0 := p.qInt(`SELECT count(*) FROM person WHERE org_id = $1 AND user_id IS NOT NULL`, orgB)
	tables := a["tables"].(map[string]any)
	// the attacker scenario: a planted person linked to an account, with an org-wide org_admin membership
	attacker := id.New().String()
	tables["person"] = append(tables["person"].([]any), map[string]any{
		"id": attacker, "org_id": p.org.ID.String(), "display_name": "Mallory", "user_id": p.w.Actor("org_admin").UserID.String(),
		"contact": map[string]any{},
	})
	_ = resident
	tables["membership"] = append(tables["membership"].([]any), map[string]any{
		"id": id.New().String(), "org_id": p.org.ID.String(), "person_id": attacker, "role": "org_admin", "scope_type": "org",
		"scope_id": nil, "valid_from": "2020-01-01T00:00:00Z", "valid_to": nil, "site_owned": false, "decision_id": nil,
		"created_at": "2020-01-01T00:00:00Z", "updated_at": "2020-01-01T00:00:00Z", "version": 1,
	})
	tampered, _ := json.Marshal(a)

	// the signature no longer matches
	_, err := p.e.tenSvc.SystemImportSite(context.Background(), orgB, tampered)
	if err == nil || p.qInt(`SELECT count(*) FROM site WHERE org_id = $1`, orgB) != 0 {
		t.Fatalf("a tampered archive was accepted: %v", err)
	}
	delete(a, "signature")
	unsigned, _ := json.Marshal(a)
	if _, err := p.e.tenSvc.SystemImportSite(context.Background(), orgB, unsigned); err == nil {
		t.Fatal("an unsigned archive was accepted without the untrusted option")
	}

	// even with the explicit option, the planted rows grant nothing
	if _, err := p.e.tenSvc.SystemImportSiteWithOptions(context.Background(), orgB, unsigned, tenancy.ImportOptions{AllowUntrusted: true}); err != nil {
		t.Fatalf("untrusted import: %v", err)
	}
	if n := p.qInt(`SELECT count(*) FROM membership WHERE org_id = $1 AND scope_type = 'org'`, orgB); n != org0 {
		t.Fatalf("an organization-wide membership was planted (%d)", n)
	}
	if n := p.qInt(`SELECT count(*) FROM person WHERE org_id = $1 AND (id = $2 OR user_id IS NOT NULL)`, orgB, attacker); n != linked0 {
		t.Fatalf("the planted person was imported (%d)", n)
	}
}

func TestIntegrationSiteImportRejectsRowsOfAnotherSite(t *testing.T) {
	p := newPB(t)
	_, data, _ := p.archiveOf(t)
	var a map[string]any
	_ = json.Unmarshal(data, &a)
	delete(a, "signature")
	tables := a["tables"].(map[string]any)
	tables["vehicle"] = append(tables["vehicle"].([]any), map[string]any{"id": id.New().String(), "org_id": p.org.ID.String(), "site_id": id.New().String(), "plate": "34ZZ34"})
	bad, _ := json.Marshal(a)
	if _, err := p.e.tenSvc.SystemImportSiteWithOptions(context.Background(), p.w.OrgB.ID, bad, tenancy.ImportOptions{AllowUntrusted: true}); err == nil {
		t.Fatal("a row of another site was accepted")
	}
	tables["vehicle"] = []any{}
	tables["mystery_table"] = []any{}
	bad, _ = json.Marshal(a)
	if _, err := p.e.tenSvc.SystemImportSiteWithOptions(context.Background(), p.w.OrgB.ID, bad, tenancy.ImportOptions{AllowUntrusted: true}); err == nil {
		t.Fatal("an unknown table was accepted")
	}
}
