package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/permissions"
)

type pb struct {
	t   *testing.T
	e   *idEnv
	w   *authztest.World
	org authztest.Org
	adm string // org_admin bearer token of OrgA
}

func newPB(t *testing.T) *pb {
	t.Helper()
	e := newIDEnv(t, true, true)
	w := authztest.NewWorld(t, e.db, e.svc)
	return &pb{t: t, e: e, w: w, org: w.OrgA, adm: w.Token(t, w.Actor("org_admin"))}
}

type resp struct {
	Code int
	Body map[string]any
	Raw  string
}

func (p *pb) call(method, path, body, token string, headers ...string) resp {
	p.t.Helper()

	rd := strings.NewReader(body)
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "203.0.113.77:4000"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	p.e.h.ServeHTTP(rec, r)
	out := resp{Code: rec.Code, Raw: rec.Body.String()}
	_ = json.Unmarshal(rec.Body.Bytes(), &out.Body)
	return out
}

func (p *pb) must(r resp, code int) resp {
	p.t.Helper()
	if r.Code != code {
		p.t.Fatalf("status %d, want %d: %s", r.Code, code, r.Raw)
	}
	return r
}

func (p *pb) upload(path, token string, fields map[string]string, filename string, content []byte) resp {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = mw.Close()
	return p.call("POST", path, b.String(), token, "Content-Type", mw.FormDataContentType())
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func items(r resp) []map[string]any {
	var out []map[string]any
	for _, v := range r.Body["items"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}
func num(m map[string]any, k string) int { f, _ := m[k].(float64); return int(f) }

func (p *pb) base() string { return "/v1/orgs/" + p.org.ID.String() }

// grantsFor resolves the grants of a user in the organization, as the pipeline does.
func (p *pb) grantsFor(userID uuid.UUID) context.Context {
	p.t.Helper()
	reg, err := permissions.NewRegistry()
	if err != nil {
		p.t.Fatal(err)
	}
	pr := &authz.Principal{ID: userID, UserID: userID}
	g, _, err := authz.NewResolver(p.e.db, reg, nil).ResolveOrg(context.Background(), pr, p.org.ID)
	if err != nil {
		p.t.Fatal(err)
	}
	return authz.WithGrants(authz.WithPrincipal(context.Background(), pr), g)
}

func tokenOf(t *testing.T, e *idEnv, userID uuid.UUID) string {
	t.Helper()
	tp, err := e.svc.IssueTokens(context.Background(), userID, identity.Meta{IP: "198.51.100.1"})
	if err != nil {
		t.Fatal(err)
	}
	return tp.AccessToken
}

// The whole of phase B over HTTP: site, import (dry run, real, again), the
// invitation of a resident by SMS, household money access, a unit claim, a
// move-out with former-occupant access, the site hand-over to another
// organization and the offboarding.
func TestIntegrationPhaseBFlow(t *testing.T) {
	p := newPB(t)
	csvData, err := os.ReadFile("testdata/golden_site.csv")
	if err != nil {
		t.Fatal(err)
	}
	site := p.must(p.call("POST", p.base()+"/sites", `{"name":"Gul Sitesi","blocks":["A","B"]}`, p.adm), 201).Body
	siteID := str(site, "id")

	// allocation keys: the Law 634 defaults exist
	keys := items(p.must(p.call("GET", p.base()+"/sites/"+siteID+"/allocation-keys", "", p.adm), 200))
	if len(keys) != 2 || str(keys[0], "code") != "arsa_payi" {
		t.Fatalf("default keys = %v", keys)
	}

	// dry run: a full report, nothing written
	dry := p.must(p.upload(p.base()+"/sites/"+siteID+"/import", p.adm, map[string]string{"dry_run": "true"}, "golden.csv", csvData), 200).Body
	if dry["applied"] != false || dry["status"] != "dry_run" || len(dry["rows"].([]any)) != 25 {
		t.Fatalf("dry run: %v", dry)
	}
	if n := len(items(p.must(p.call("GET", p.base()+"/sites/"+siteID+"/units?limit=200", "", p.adm), 200))); n != 0 {
		t.Fatalf("a dry run wrote %d units", n)
	}
	// real import
	real := p.must(p.upload(p.base()+"/sites/"+siteID+"/import", p.adm, nil, "golden.csv", csvData), 200).Body
	if real["applied"] != true {
		t.Fatalf("import: %v", real)
	}
	units := items(p.must(p.call("GET", p.base()+"/sites/"+siteID+"/units?limit=200", "", p.adm), 200))
	if len(units) != 24 {
		t.Fatalf("units = %d, want 24", len(units))
	}
	vacant := items(p.must(p.call("GET", p.base()+"/sites/"+siteID+"/units?vacant=true", "", p.adm), 200))
	if len(vacant) != 1 || str(vacant[0], "door_number") != "S-2" {
		t.Fatalf("vacant units = %v", vacant)
	}
	// idempotent: the same file again changes nothing
	again := p.must(p.upload(p.base()+"/sites/"+siteID+"/import", p.adm, nil, "golden.csv", csvData), 200).Body
	sum := again["summary"].(map[string]any)
	if sum["units_created"] != nil || sum["persons_created"] != nil || num(sum, "rows_unchanged") != num(sum, "rows_total") {
		t.Fatalf("re-import is not idempotent: %v", sum)
	}
	// co-owners of A-1 and the plate CSV
	var a1 string
	for _, u := range units {
		if str(u, "door_number") == "A-1" {
			a1 = str(u, "id")
		}
	}
	occ := items(p.must(p.call("GET", p.base()+"/occupancies?unit_id="+a1, "", p.adm), 200))
	if len(occ) != 2 {
		t.Fatalf("A-1 occupancies = %d, want 2 co-owners", len(occ))
	}
	csv := p.must(p.call("GET", p.base()+"/sites/"+siteID+"/vehicles/export", "", p.adm), 200).Raw
	if !strings.Contains(csv, "34 ABC 123,A-1") {
		t.Fatalf("plate export: %s", csv)
	}
	// a third co-owner share beyond 100 is refused
	person := p.must(p.call("POST", p.base()+"/persons", `{"display_name":"Third Owner","phone":"05320009999"}`, p.adm), 201).Body
	p.must(p.call("POST", p.base()+"/units/"+a1+"/occupancies", fmt.Sprintf(`{"person_id":%q,"kind":"owner","share_pct":10}`, str(person, "id")), p.adm), 422)

	// invite a resident by SMS, accept, see the occupancy in /v1/me
	var a2 string
	for _, u := range units {
		if str(u, "door_number") == "A-2" {
			a2 = str(u, "id")
		}
	}
	// A-2 already has an owner: invite a tenant instead
	inv := p.must(p.call("POST", p.base()+"/invitations", fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"display_name":"Cansu Tenant","phone":"05327770001","occupancy_kind":"tenant","channel":"sms"}`, siteID, a2), p.adm), 201).Body
	if inv["delivery"] != "sent" {
		t.Fatalf("delivery = %v", inv["delivery"])
	}
	msg, ok := p.e.sms.Last()
	if !ok || !strings.Contains(msg.Text, "/invite/") || msg.To != "+905327770001" {
		t.Fatalf("sms = %+v", msg)
	}
	token := str(inv, "token")
	prev := p.must(p.call("GET", "/v1/invitations/"+token, "", ""), 200).Body
	if str(prev, "door_number") == "" || str(prev, "invited_name") != "Cansu Tenant" {
		t.Fatalf("preview: %v", prev)
	}
	acc := p.must(p.call("POST", "/v1/invitations/"+token+"/accept", `{}`, ""), 200).Body
	if acc["user_created"] != true || acc["occupancy_id"] == nil {
		t.Fatalf("accept: %v", acc)
	}
	p.must(p.call("POST", "/v1/invitations/"+token+"/accept", `{}`, ""), 404) // single use
	tenantUser := uuid.MustParse(str(acc, "user_id"))
	me := p.must(p.call("GET", "/v1/me", "", tokenOf(t, p.e, tenantUser)), 200).Body
	occs := me["occupancies"].([]any)
	if len(occs) != 1 || occs[0].(map[string]any)["kind"] != "tenant" || occs[0].(map[string]any)["unit_label"] != "A-2" {
		t.Fatalf("me.occupancies = %v", occs)
	}
	orgsMe := me["organizations"].([]any)[0].(map[string]any)
	if len(orgsMe["sites"].([]any)) != 1 {
		t.Fatalf("me.sites = %v", orgsMe["sites"])
	}
	// the tenant has portal rights on the tenant side only
	ctx := p.grantsFor(tenantUser)
	a2id := uuid.MustParse(a2)
	if authz.CanAccessUnit(ctx, a2id, authz.SideTenant) != nil || authz.CanAccessUnit(ctx, a2id, authz.SideOwner) == nil {
		t.Fatal("a tenant reaches the tenant side of their unit and not the owner side")
	}

	// the tenant invites a household member: no money access by default, then granted
	tenantTok := tokenOf(t, p.e, tenantUser)
	hi := p.must(p.call("POST", p.base()+"/units/"+a2+"/household-invitations", `{"display_name":"Kid Tenant","phone":"05327770002","channel":"sms"}`, tenantTok), 201).Body
	hacc := p.must(p.call("POST", "/v1/invitations/"+str(hi, "token")+"/accept", `{}`, ""), 200).Body
	hUser := uuid.MustParse(str(hacc, "user_id"))
	hctx := p.grantsFor(hUser)
	if authz.CanActOnUnit(hctx, a2id) != nil || authz.CanAccessUnit(hctx, a2id, authz.SideTenant) == nil {
		t.Fatal("a household member acts on the unit but sees no money by default")
	}
	hocc := str(hacc, "occupancy_id")
	p.must(p.call("PUT", p.base()+"/occupancies/"+hocc+"/money-access", `{"may_see_money":true}`, p.adm), 422) // If-Match is required
	p.must(p.call("PUT", p.base()+"/occupancies/"+hocc+"/money-access", `{"may_see_money":true}`, tenantTok, "If-Match", `"1"`), 200)
	if authz.CanAccessUnit(p.grantsFor(hUser), a2id, authz.SideTenant) != nil {
		t.Fatal("money access was granted by the tenant")
	}
	// a household member cannot grant money access to themselves
	p.must(p.call("PUT", p.base()+"/occupancies/"+hocc+"/money-access", `{"may_see_money":false}`, tokenOf(t, p.e, hUser), "If-Match", `"2"`), 403)
	// the tenant ends the household member's stay
	p.must(p.call("DELETE", p.base()+"/units/"+a2+"/household/"+hocc, "", tenantTok), 204)

	// site QR: a signed-in stranger claims a unit; the manager approves
	jc := p.must(p.call("POST", p.base()+"/sites/"+siteID+"/join-code", "", p.adm), 201).Body
	code := str(jc, "code")
	if !strings.HasSuffix(str(jc, "url"), "/join/"+code) {
		t.Fatalf("join url: %v", jc)
	}
	stranger := p.w.Actor("no_membership")
	strTok := p.w.Token(t, stranger)
	if pre := p.must(p.call("GET", "/v1/join/"+code, "", ""), 200).Body; len(pre["units"].([]any)) != 0 {
		t.Fatal("an anonymous visitor must not see the unit list")
	}
	if pre := p.must(p.call("GET", "/v1/join/"+code, "", strTok), 200).Body; len(pre["units"].([]any)) == 0 {
		t.Fatal("a signed-in visitor sees the units")
	}
	var b1 string
	for _, u := range units {
		if str(u, "door_number") == "B-1" {
			b1 = str(u, "id")
		}
	}
	claim := p.must(p.call("POST", "/v1/join/"+code+"/claims", fmt.Sprintf(`{"unit_id":%q,"kind":"household","display_name":"Stranger"}`, b1), strTok), 201).Body
	p.must(p.call("POST", "/v1/join/"+code+"/claims", fmt.Sprintf(`{"unit_id":%q,"kind":"household"}`, b1), strTok), 409)
	if me := p.must(p.call("GET", "/v1/me", "", strTok), 200).Body; len(me["occupancies"].([]any)) != 0 {
		t.Fatal("a pending claim gives no rights")
	}
	p.must(p.call("GET", p.base()+"/occupancies", "", strTok), 404) // not a member yet
	pending := items(p.must(p.call("GET", p.base()+"/unit-claims", "", p.adm), 200))
	if len(pending) != 1 {
		t.Fatalf("pending claims = %d", len(pending))
	}
	p.must(p.call("POST", p.base()+"/unit-claims/"+str(claim, "id")+"/approve", `{}`, p.adm), 200)
	if me := p.must(p.call("GET", "/v1/me", "", strTok), 200).Body; len(me["occupancies"].([]any)) != 1 {
		t.Fatal("an approved claim creates the occupancy")
	}

	// move-out: a former owner keeps read access to their own period only
	a3 := ""
	for _, u := range units {
		if str(u, "door_number") == "A-3" {
			a3 = str(u, "id")
		}
	}
	former := p.must(p.call("POST", p.base()+"/persons", `{"display_name":"Former Owner"}`, p.adm), 201).Body
	fo := p.must(p.call("POST", p.base()+"/units/"+a3+"/occupancies", fmt.Sprintf(`{"person_id":%q,"kind":"owner","valid_from":"2020-01-01","valid_to":"2020-06-01"}`, str(former, "id")), p.adm), 201).Body
	fu, err := p.e.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: "former@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	p.w.Exec(t, `UPDATE person SET user_id = $2 WHERE id = $1`, str(former, "id"), fu.ID)
	fctx := p.grantsFor(fu.ID)
	a3id := uuid.MustParse(a3)
	if authz.CanAccessUnit(fctx, a3id, authz.SideOwner) == nil {
		t.Fatal("a former owner has no current access")
	}
	if authz.CanReadUnitAt(fctx, a3id, authz.SideOwner, time.Date(2020, 3, 1, 0, 0, 0, 0, time.UTC)) != nil {
		t.Fatal("a former owner reads their own period")
	}
	if authz.CanReadUnitAt(fctx, a3id, authz.SideOwner, time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)) == nil {
		t.Fatal("a former owner sees nothing after leaving")
	}
	_ = fo
	// the formal move-out with a successor (sale)
	o2 := p.must(p.call("POST", p.base()+"/units/"+a3+"/occupancies", fmt.Sprintf(`{"person_id":%q,"kind":"owner","valid_from":"2020-06-01"}`, str(person, "id")), p.adm), 201).Body
	mo := p.must(p.call("POST", p.base()+"/occupancies/"+str(o2, "id")+"/move-out", fmt.Sprintf(`{"as_of":"2025-01-01","successor_person_id":%q}`, str(former, "id")), p.adm), 201).Body
	if mo["open_charges"] != "carry_to_person" || mo["closing_statement_status"] != "pending" || mo["successor_occupancy_id"] == nil {
		t.Fatalf("move-out: %v", mo)
	}

	// site hand-over: export from OrgA, import into OrgB with the same identifiers
	exp := p.must(p.call("POST", p.base()+"/sites/"+siteID+"/export", "", p.adm), 201).Body
	arch := p.must(p.call("GET", p.base()+"/site-exports/"+str(exp, "id")+"/archive", "", p.adm), 200).Raw
	bAdm := p.w.Token(t, p.w.BAdmin)
	imp := p.upload("/v1/orgs/"+p.w.OrgB.ID.String()+"/site-imports", bAdm, nil, "archive.json", []byte(arch))
	p.must(imp, 201)
	unitsB := items(p.must(p.call("GET", "/v1/orgs/"+p.w.OrgB.ID.String()+"/sites/"+siteID+"/units?limit=200", "", bAdm), 200))
	if len(unitsB) != 24 {
		t.Fatalf("imported units = %d", len(unitsB))
	}
	ids := map[string]bool{}
	for _, u := range units {
		ids[str(u, "id")] = true
	}
	for _, u := range unitsB {
		if !ids[str(u, "id")] {
			t.Fatalf("unit %s was not preserved", str(u, "id"))
		}
	}
	p.must(p.upload("/v1/orgs/"+p.w.OrgB.ID.String()+"/site-imports", bAdm, nil, "archive.json", []byte(arch)), 409) // already there
	// the resident's login survives: the tenant still has their account, now with a person in OrgB too
	if me := p.must(p.call("GET", "/v1/me", "", tenantTok), 200).Body; len(me["organizations"].([]any)) != 2 {
		t.Fatalf("the tenant's account should reach both organizations: %v", me["organizations"])
	}

	// offboarding needs re-authentication, then closes the source
	p.must(p.call("POST", p.base()+"/sites/"+siteID+"/offboard", "", p.adm), 403)
	p.w.Exec(t, `UPDATE session SET reauth_until = now() + interval '10 minutes' WHERE user_id = $1`, p.w.Actor("org_admin").UserID)
	p.must(p.call("POST", p.base()+"/sites/"+siteID+"/offboard", "", p.adm), 200)
	r := p.call("GET", p.base()+"/sites/"+siteID+"/units", "", p.adm)
	if r.Code != http.StatusForbidden || r.Body["code"] != "site_offboarded" {
		t.Fatalf("units of an offboarded site: %d %s", r.Code, r.Raw)
	}
	if authz.CanAccessUnit(p.grantsFor(tenantUser), a2id, authz.SideTenant) == nil {
		t.Fatal("offboarding ends the residents' derived access in the source organization")
	}
}

func TestIntegrationSiteOwnedMembershipNeedsReauth(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	person := p.w.NewPerson(t, p.org)
	m := p.must(p.call("POST", p.base()+"/memberships", fmt.Sprintf(`{"person_id":%q,"role":"board_member","scope_type":"site","scope_id":%q}`, person, f.Site), p.adm), 201).Body
	if m["site_owned"] != true {
		t.Fatalf("board membership must be site-owned: %v", m)
	}
	p.must(p.call("POST", p.base()+"/memberships", fmt.Sprintf(`{"person_id":%q,"role":"auditor"}`, person), p.adm), 422) // site-owned roles belong to a site
	p.must(p.call("DELETE", p.base()+"/memberships/"+str(m, "id"), "", p.adm), 403)
	p.w.Exec(t, `UPDATE session SET reauth_until = now() + interval '10 minutes' WHERE user_id = $1`, p.w.Actor("org_admin").UserID)
	p.must(p.call("DELETE", p.base()+"/memberships/"+str(m, "id"), "", p.adm), 204)
	// the audit trail records it as an override
	ev := items(p.must(p.call("GET", p.base()+"/audit?action=membership.end_site_owned", "", p.adm), 200))
	if len(ev) != 1 {
		t.Fatalf("audit events = %d", len(ev))
	}
}

func TestIntegrationNationalIDMaskedAndNeverAudited(t *testing.T) {
	p := newPB(t)
	r := p.must(p.call("POST", p.base()+"/persons", `{"display_name":"Ayse","national_id":"10000000146"}`, p.adm), 201).Body
	if str(r, "national_id") != "*******0146" || str(r, "national_id_country") != "TR" {
		t.Fatalf("masked national id: %v", r)
	}
	p.must(p.call("POST", p.base()+"/persons", `{"display_name":"Bad","national_id":"12345678901"}`, p.adm), 422)
	got := p.must(p.call("GET", p.base()+"/persons/"+str(r, "id"), "", p.adm), 200).Body
	if str(got, "national_id") != "*******0146" {
		t.Fatalf("read: %v", got)
	}
	ev := p.must(p.call("GET", p.base()+"/audit?action=person.create", "", p.adm), 200).Raw
	if strings.Contains(ev, "10000000146") || strings.Contains(ev, "0146") {
		t.Fatalf("the identity number leaked into the audit trail: %s", ev)
	}
	var sealed []byte
	err := p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT national_id_enc FROM person WHERE id = $1`, str(r, "id")).Scan(&sealed)
	})
	if err != nil || len(sealed) == 0 || bytes.Contains(sealed, []byte("10000000146")) {
		t.Fatalf("stored value must be sealed: %v %x", err, sealed)
	}
}

func TestIntegrationCodesLabels(t *testing.T) {
	p := newPB(t)
	r := p.must(p.call("GET", "/v1/codes?locale=tr", "", ""), 200).Body
	sections := r["sections"].(map[string]any)
	if sections["unit_types"].(map[string]any)["flat"] != "Daire" || r["locale"] != "tr" {
		t.Fatalf("tr labels: %v", sections["unit_types"])
	}
	en := p.must(p.call("GET", "/v1/codes", "", ""), 200).Body
	if en["sections"].(map[string]any)["occupancy_kinds"].(map[string]any)["owner"] != "Owner" {
		t.Fatal("en labels")
	}
}

func TestIntegrationRLSIsolatesPhaseBTables(t *testing.T) {
	p := newPB(t)
	fa := p.w.NewSite(t, p.w.OrgA)
	// another organization's site is invisible and unreachable
	p.must(p.call("GET", "/v1/orgs/"+p.w.OrgB.ID.String()+"/sites/"+fa.Site.String(), "", p.w.Token(t, p.w.BAdmin)), 404)
	// the composite foreign keys refuse a cross-organization link at write time
	pb := p.w.NewPerson(t, p.w.OrgB)
	err := p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, 'owner', 'owner', '2020-01-01')`, p.w.OrgA.ID, fa.Site, fa.Unit, pb)
		return err
	})
	if err == nil {
		t.Fatal("an occupancy of org A must not reference a person of org B")
	}
	// ... nor a unit of another site
	other := p.w.NewSite(t, p.w.OrgA)
	err = p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, 'owner', 'owner', '2020-01-01')`, p.w.OrgA.ID, fa.Site, other.Unit, p.w.NewPerson(t, p.w.OrgA))
		return err
	})
	if err == nil {
		t.Fatal("an occupancy must not reference a unit of another site")
	}
}
