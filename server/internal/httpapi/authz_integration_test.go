package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/permissions"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

type authzEnv struct {
	*idEnv
	w *authztest.World
}

func newAuthzEnv(t *testing.T) *authzEnv {
	t.Helper()
	e := newIDEnv(t, false, false)
	return &authzEnv{idEnv: e, w: authztest.NewWorld(t, e.db, e.svc)}
}

// as sends a request as the actor with a fresh bearer token.
func (e *authzEnv) as(a authztest.Actor, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	h := append([]string{"Authorization", "Bearer " + e.w.Token(e.t, a)}, headers...)
	return e.do(method, path, body, false, h...)
}

func (e *authzEnv) orgPath(o authztest.Org, suffix string) string {
	return "/v1/orgs/" + o.ID.String() + suffix
}

func ifMatch(v int) string { return `"` + strconv.Itoa(v) + `"` }

// TestPermissionsSpecInSync fails when the registry and the committed spec
// files drift: task gen rewrites them.
func TestPermissionsSpecInSync(t *testing.T) {
	reg, err := permissions.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "src", "components", "permissions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != string(reg.SpecYAML()) {
		t.Error("api/src/components/permissions.yaml differs from the permission registry; run tools/task.ps1 gen")
	}
	spec := loadSpec(t)
	enum := spec.Components.Schemas["Permission"].Value.Enum
	var inSpec []string
	for _, v := range enum {
		inSpec = append(inSpec, v.(string))
	}
	var inCode []string
	for _, p := range reg.Permissions() {
		inCode = append(inCode, string(p))
	}
	if !slices.Equal(inSpec, inCode) {
		t.Errorf("components.schemas.Permission = %v, registry = %v; run tools/task.ps1 gen", inSpec, inCode)
	}
	var matrix map[string][]string
	if !extJSON(t, spec.Extensions, "x-openbekci-roles", &matrix) {
		t.Fatal("no root x-openbekci-roles in api/openapi.yaml")
	}
	for _, role := range append(authz.BuiltinRoles(), authz.RolePlatformAdmin) {
		var want []string
		for _, p := range reg.RolePermissions(role) {
			want = append(want, string(p))
		}
		if !slices.Equal(matrix[string(role)], want) {
			t.Errorf("x-openbekci-roles[%s] = %v, registry says %v", role, matrix[string(role)], want)
		}
	}
}

func TestIntegrationOrgUpdateIsAuditedWithDiff(t *testing.T) {
	e := newAuthzEnv(t)
	admin, auditor, manager := e.w.Actor("org_admin"), e.w.Actor("auditor"), e.w.Actor("site_manager")
	org := e.w.OrgA

	var got struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	}
	rec := e.as(admin, "GET", e.orgPath(org, ""), "")
	e.mustJSON(rec, 200, &got)

	// a stale version is a conflict, nothing changes
	e.problem(e.as(admin, "PATCH", e.orgPath(org, ""), `{"name":"Stale"}`, "If-Match", ifMatch(got.Version+5)), 409, "version_conflict")
	// validation
	e.problem(e.as(admin, "PATCH", e.orgPath(org, ""), `{"timezone":"Mars/Olympus"}`, "If-Match", ifMatch(got.Version)), 422, "validation_failed")
	// If-Match is required, but authentication is checked first
	e.problem(e.as(admin, "PATCH", e.orgPath(org, ""), `{"name":"No version"}`), 422, "validation_failed")
	e.problem(e.do("PATCH", e.orgPath(org, ""), `{"name":"No version"}`, false), 401, "unauthenticated")
	// a manager can read but not change
	e.problem(e.as(manager, "PATCH", e.orgPath(org, ""), `{"name":"Nope"}`, "If-Match", ifMatch(got.Version)), 403, "insufficient_permission")

	e.mustJSON(e.as(admin, "PATCH", e.orgPath(org, ""), `{"name":"Gul Sitesi","timezone":"Europe/Berlin"}`, "If-Match", ifMatch(got.Version)), 200, &got)
	if got.Name != "Gul Sitesi" || got.Version != 2 {
		t.Fatalf("after update: %+v", got)
	}
	// an update that changes nothing writes nothing (no version bump, no event)
	e.mustJSON(e.as(admin, "PATCH", e.orgPath(org, ""), `{"name":"Gul Sitesi"}`, "If-Match", ifMatch(2)), 200, &got)
	if got.Version != 2 {
		t.Errorf("no-op update bumped the version to %d", got.Version)
	}

	// the auditor reads the trail; the manager may not
	e.problem(e.as(manager, "GET", e.orgPath(org, "/audit"), ""), 403, "insufficient_permission")
	var list struct {
		Items []struct {
			Action      string  `json:"action"`
			ActorKind   string  `json:"actor_kind"`
			ActorUserID *string `json:"actor_user_id"`
			TargetType  string  `json:"target_type"`
			TargetID    string  `json:"target_id"`
			IP          *string `json:"ip"`
			RequestID   *string `json:"request_id"`
			Diff        map[string]struct {
				Before any `json:"before"`
				After  any `json:"after"`
			} `json:"diff"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	e.mustJSON(e.as(auditor, "GET", e.orgPath(org, "/audit"), ""), 200, &list)
	if len(list.Items) != 1 {
		t.Fatalf("want exactly the one org.update event, got %+v", list.Items)
	}
	ev := list.Items[0]
	if ev.Action != "org.update" || ev.ActorKind != "user" || ev.TargetType != "organization" || ev.TargetID != org.ID.String() {
		t.Errorf("event: %+v", ev)
	}
	if ev.ActorUserID == nil || *ev.ActorUserID != admin.UserID.String() {
		t.Errorf("actor = %v, want %s", ev.ActorUserID, admin.UserID)
	}
	if ev.IP == nil || *ev.IP != "203.0.113.9" || ev.RequestID == nil || *ev.RequestID == "" {
		t.Errorf("ip/request id: %v %v", ev.IP, ev.RequestID)
	}
	if d := ev.Diff["name"]; d.Before != "Organization A" || d.After != "Gul Sitesi" {
		t.Errorf("name diff: %+v", d)
	}
	if d := ev.Diff["timezone"]; d.Before != "Europe/Istanbul" || d.After != "Europe/Berlin" {
		t.Errorf("timezone diff: %+v", d)
	}
	if len(ev.Diff) != 2 {
		t.Errorf("only changed fields belong in the diff: %+v", ev.Diff)
	}
	if list.NextCursor != nil {
		t.Errorf("next_cursor = %v on the only page", *list.NextCursor)
	}
	// filters
	var filtered struct{ Items []json.RawMessage }
	e.mustJSON(e.as(auditor, "GET", e.orgPath(org, "/audit?action=org.update&target_type=organization&actor="+admin.UserID.String()), ""), 200, &filtered)
	if len(filtered.Items) != 1 {
		t.Errorf("filter matched %d events", len(filtered.Items))
	}
	e.mustJSON(e.as(auditor, "GET", e.orgPath(org, "/audit?action=other.thing"), ""), 200, &filtered)
	if len(filtered.Items) != 0 {
		t.Errorf("filter on another action matched %d events", len(filtered.Items))
	}
	e.mustJSON(e.as(auditor, "GET", e.orgPath(org, "/audit?to="+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)), ""), 200, &filtered)
	if len(filtered.Items) != 0 {
		t.Errorf("to=an hour ago matched %d events", len(filtered.Items))
	}
}

func TestIntegrationCrossOrganizationIsNotFound(t *testing.T) {
	e := newAuthzEnv(t)
	bAdmin, aAdmin := e.w.BAdmin, e.w.Actor("org_admin")
	// B's administrator sees B and nothing of A, not even that A exists
	e.mustJSON(e.as(bAdmin, "GET", e.orgPath(e.w.OrgB, ""), ""), 200, nil)
	e.problem(e.as(bAdmin, "GET", e.orgPath(e.w.OrgA, ""), ""), 404, "not_found")
	e.problem(e.as(bAdmin, "GET", e.orgPath(e.w.OrgA, "/audit"), ""), 404, "not_found")
	e.problem(e.as(aAdmin, "PATCH", e.orgPath(e.w.OrgB, ""), `{"name":"x"}`, "If-Match", `"1"`), 404, "not_found")
	// an unknown organization id looks the same; a malformed one fails path validation
	e.problem(e.as(aAdmin, "GET", "/v1/orgs/"+uuid.NewString(), ""), 404, "not_found")
	e.problem(e.as(aAdmin, "GET", "/v1/orgs/not-a-uuid", ""), 422, "validation_failed")
	// the platform administrator has no organization permissions without a membership
	e.problem(e.as(e.w.Actor("platform_admin"), "GET", e.orgPath(e.w.OrgA, ""), ""), 404, "not_found")
	// an event of A never shows up in B's trail
	e.mustJSON(e.as(aAdmin, "PATCH", e.orgPath(e.w.OrgA, ""), `{"name":"A renamed"}`, "If-Match", ifMatch(1)), 200, nil)
	var list struct{ Items []json.RawMessage }
	e.mustJSON(e.as(bAdmin, "GET", e.orgPath(e.w.OrgB, "/audit"), ""), 200, &list)
	if len(list.Items) != 0 {
		t.Errorf("organization B sees %d events of organization A", len(list.Items))
	}
}

func TestIntegrationMembershipValidityWindows(t *testing.T) {
	e := newAuthzEnv(t)
	reg, err := permissions.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	u := e.w.Person(t, e.w.OrgA, e.w.Actor("no_membership"))
	org := e.w.OrgA
	add := func(role authz.Role, from, to time.Time) {
		e.w.AddMembership(t, org, u, role, authz.ScopeOrg, nil, from, to)
	}
	add(authz.RoleStaff, now.Add(-2*time.Hour), now.Add(-time.Hour)) // expired
	add(authz.RoleAuditor, now.Add(time.Hour), time.Time{})          // not yet valid
	add(authz.RoleAccountant, now.Add(-time.Hour), now)              // valid_to == now: already over (half-open window)
	pr := &authz.Principal{UserID: u.UserID, ID: u.UserID}
	at := func(clock time.Time) authz.Grants {
		r := authz.NewResolver(e.db, reg, func() time.Time { return clock })
		g, member, err := r.ResolveOrg(context.Background(), pr, org.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !member && len(g.Items) != 0 {
			t.Fatalf("grants without membership: %+v", g)
		}
		return g
	}
	r := authz.NewResolver(e.db, reg, func() time.Time { return now })
	if _, member, err := r.ResolveOrg(context.Background(), pr, org.ID); err != nil || member {
		t.Fatalf("only expired and future memberships: member=%v err=%v, want not a member", member, err)
	}
	// over HTTP the same caller is a stranger: 404
	e.problem(e.as(u, "GET", e.orgPath(org, ""), ""), 404, "not_found")

	scope := authz.Scope{OrgID: org.ID}
	if g := at(now.Add(-90 * time.Minute)); !g.Has("org.read", scope) { // inside the staff window
		t.Error("staff membership must be active inside its window")
	}
	if g := at(now.Add(-30 * time.Minute)); !g.Has("org.read", scope) { // accountant window
		t.Error("accountant membership must be active inside its window")
	}
	if g := at(now.Add(2 * time.Hour)); !g.Has("audit.read", scope) { // auditor window, later
		t.Error("auditor membership must be active after valid_from")
	}
	if g := at(now.Add(-90 * time.Minute)); g.Has("audit.read", scope) {
		t.Error("the not-yet-valid auditor membership must not grant audit.read")
	}
}

func TestIntegrationMePermissions(t *testing.T) {
	e := newAuthzEnv(t)
	type perm struct {
		Permission string    `json:"permission"`
		SiteIDs    *[]string `json:"site_ids"`
		BlockIDs   *[]string `json:"block_ids"`
	}
	var me struct {
		Organizations []struct {
			ID          string `json:"id"`
			Permissions []perm `json:"permissions"`
		} `json:"organizations"`
	}
	find := func(ps []perm, name string) *perm {
		for i := range ps {
			if ps[i].Permission == name {
				return &ps[i]
			}
		}
		return nil
	}

	e.mustJSON(e.as(e.w.Actor("org_admin"), "GET", "/v1/me", ""), 200, &me)
	if len(me.Organizations) != 1 {
		t.Fatalf("organizations: %+v", me.Organizations)
	}
	ps := me.Organizations[0].Permissions
	for _, want := range []string{"org.manage", "org.read", "audit.read", "me.read"} {
		p := find(ps, want)
		if p == nil || p.SiteIDs != nil || p.BlockIDs != nil {
			t.Errorf("org_admin %s = %+v, want org-wide", want, p)
		}
	}
	if find(ps, "platform.manage") != nil || find(ps, "audit.report") != nil {
		t.Errorf("org_admin must not hold platform.manage or audit.report: %+v", ps)
	}

	// the guard holds almost nothing; the platform administrator no organization permission at all
	e.mustJSON(e.as(e.w.Actor("guard"), "GET", "/v1/me", ""), 200, &me)
	if find(me.Organizations[0].Permissions, "org.read") != nil {
		t.Error("guard must not hold org.read")
	}
	e.mustJSON(e.as(e.w.Actor("platform_admin"), "GET", "/v1/me", ""), 200, &me)
	if len(me.Organizations) != 0 {
		t.Errorf("platform administrator without membership: %+v", me.Organizations)
	}

	// site- and block-scoped memberships list their ids, an org-wide one swallows them
	u := e.w.Person(t, e.w.OrgA, e.w.Actor("no_membership"))
	site, site2, block := id.New(), id.New(), id.New()
	now := time.Now()
	e.w.AddMembership(t, e.w.OrgA, u, authz.RoleSiteManager, authz.ScopeSite, &site, now.Add(-time.Hour), time.Time{})
	e.w.AddMembership(t, e.w.OrgA, u, authz.RoleSiteManager, authz.ScopeSite, &site2, now.Add(-time.Hour), time.Time{})
	e.w.AddMembership(t, e.w.OrgA, u, authz.RoleStaff, authz.ScopeBlock, &block, now.Add(-time.Hour), time.Time{})
	e.mustJSON(e.as(u, "GET", "/v1/me", ""), 200, &me)
	p := find(me.Organizations[0].Permissions, "me.read")
	if p == nil || p.SiteIDs == nil || len(*p.SiteIDs) != 2 || p.BlockIDs == nil || len(*p.BlockIDs) != 1 {
		t.Fatalf("me.read = %+v, want 2 sites and 1 block", p)
	}
	// a site-limited member reads the organization (org.read is grantable at site scope) but
	// cannot change it (org.manage is org-only); audit.read is not part of site_manager/staff
	e.mustJSON(e.as(u, "GET", e.orgPath(e.w.OrgA, ""), ""), 200, nil)
	e.problem(e.as(u, "PATCH", e.orgPath(e.w.OrgA, ""), `{"name":"x"}`, "If-Match", `"1"`), 403, "insufficient_permission")
	e.problem(e.as(u, "GET", e.orgPath(e.w.OrgA, "/audit"), ""), 403, "insufficient_permission")
	e.w.AddMembership(t, e.w.OrgA, u, authz.RoleAuditor, authz.ScopeOrg, nil, now.Add(-time.Hour), time.Time{})
	me.Organizations = nil // decoding into reused elements would keep stale pointers
	e.mustJSON(e.as(u, "GET", "/v1/me", ""), 200, &me)
	if p := find(me.Organizations[0].Permissions, "me.read"); p == nil || p.SiteIDs != nil || p.BlockIDs != nil {
		t.Errorf("an org-wide grant must make me.read org-wide: %+v", p)
	}
	e.mustJSON(e.as(u, "GET", e.orgPath(e.w.OrgA, ""), ""), 200, nil)
}

func TestIntegrationAuditIsAppendOnly(t *testing.T) {
	e := newAuthzEnv(t)
	ctx := context.Background()
	org := e.w.OrgA
	target := id.New()
	err := e.db.InTenantTx(ctx, org.ID, func(tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{OrgID: org.ID, Action: "test.created", TargetType: "thing", TargetID: target})
	})
	if err != nil {
		t.Fatal(err)
	}
	denied := func(what string, err error) {
		t.Helper()
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "42501" {
			t.Errorf("%s: err = %v, want SQLSTATE 42501 (insufficient_privilege)", what, err)
		}
	}
	for _, stmt := range []string{
		`UPDATE audit_event SET action = 'tampered'`,
		`DELETE FROM audit_event`,
		`TRUNCATE audit_event`,
	} {
		denied("app role: "+stmt, e.db.InTenantTx(ctx, org.ID, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, stmt); return err }))
		denied("platform role: "+stmt, e.db.InPlatformTx(ctx, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, stmt); return err }))
	}
	for _, stmt := range []string{
		`UPDATE platform_audit_event SET action = 'tampered'`,
		`DELETE FROM platform_audit_event`,
		`TRUNCATE platform_audit_event`,
	} {
		denied("app role: "+stmt, e.db.InAppTx(ctx, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, stmt); return err }))
		denied("platform role: "+stmt, e.db.InPlatformTx(ctx, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, stmt); return err }))
	}
	// the app role appends to the instance-level trail but cannot read it
	denied("app role reading platform_audit_event", e.db.InAppTx(ctx, func(tx pgx.Tx) error {
		var n int
		return tx.QueryRow(ctx, `SELECT count(*) FROM platform_audit_event`).Scan(&n)
	}))
	// the row is intact and visible only to its own organization (RLS)
	var n int
	if err := e.db.InTenantTx(ctx, org.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE action = 'test.created'`).Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("own org sees %d events, err %v", n, err)
	}
	if err := e.db.InTenantTx(ctx, e.w.OrgB.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("other org sees %d events, err %v", n, err)
	}
	// an event cannot be written into another organization's trail
	err = e.db.InTenantTx(ctx, e.w.OrgB.ID, func(tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{OrgID: org.ID, Action: "test.cross", TargetType: "thing", TargetID: target})
	})
	if err == nil {
		t.Error("recording into another organization's trail must fail under RLS")
	}
}

func TestIntegrationAuditPaginationCursor(t *testing.T) {
	e := newAuthzEnv(t)
	ctx := context.Background()
	org := e.w.OrgA
	auditor := e.w.Actor("auditor")
	const total = 7
	for i := 0; i < total; i++ {
		// separate transactions: distinct `at` values, plus same-timestamp ties from one transaction below
		err := e.db.InTenantTx(ctx, org.ID, func(tx pgx.Tx) error {
			return audit.Record(ctx, tx, audit.Event{OrgID: org.ID, Action: "test.n", TargetType: "thing", TargetID: id.New()})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// events of one transaction share `at` (now() is the transaction start): the id breaks the tie
	err := e.db.InTenantTx(ctx, org.ID, func(tx pgx.Tx) error {
		for i := 0; i < 4; i++ {
			if err := audit.Record(ctx, tx, audit.Event{OrgID: org.ID, Action: "test.tie", TargetType: "thing", TargetID: id.New()}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	type page struct {
		Items []struct {
			ID string    `json:"id"`
			At time.Time `json:"at"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	var seen []string
	cursor, pages := "", 0
	for {
		path := e.orgPath(org, "/audit?limit=3")
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		var p page
		e.mustJSON(e.as(auditor, "GET", path, ""), 200, &p)
		pages++
		if len(p.Items) > 3 {
			t.Fatalf("page of %d with limit 3", len(p.Items))
		}
		for _, it := range p.Items {
			seen = append(seen, it.ID)
		}
		if p.NextCursor == nil {
			break
		}
		if len(p.Items) != 3 {
			t.Fatalf("a short page (%d) must be the last, but has a cursor", len(p.Items))
		}
		cursor = *p.NextCursor
		if pages > 10 {
			t.Fatal("pagination does not terminate")
		}
	}
	if len(seen) != total+4 || pages != 4 {
		t.Fatalf("saw %d events in %d pages, want %d in 4", len(seen), pages, total+4)
	}
	uniq := map[string]bool{}
	for _, id := range seen {
		if uniq[id] {
			t.Fatalf("event %s appears on two pages", id)
		}
		uniq[id] = true
	}
	// the cursor round-trips over (at, id), newest first
	var all page
	e.mustJSON(e.as(auditor, "GET", e.orgPath(org, "/audit?limit=200"), ""), 200, &all)
	if len(all.Items) != total+4 || all.NextCursor != nil {
		t.Fatalf("one big page: %d items, cursor %v", len(all.Items), all.NextCursor)
	}
	for i, it := range all.Items {
		if it.ID != seen[i] {
			t.Fatalf("paged order differs from the single page at %d", i)
		}
		if i > 0 && it.At.After(all.Items[i-1].At) {
			t.Fatalf("not newest first at %d", i)
		}
	}
	// a bad cursor and an oversized limit are validation problems
	e.problem(e.as(auditor, "GET", e.orgPath(org, "/audit?cursor=%21%21"), ""), 422, "validation_failed")
	e.problem(e.as(auditor, "GET", e.orgPath(org, "/audit?limit=201"), ""), 422, "validation_failed")
	e.problem(e.as(auditor, "GET", e.orgPath(org, "/audit?bogus=1"), ""), 422, "validation_failed")
}

func TestIntegrationIdentityMutationsAreAudited(t *testing.T) {
	e := newAuthzEnv(t)
	ctx := context.Background()
	u := e.w.Actor("staff")
	events := func() map[string]int {
		out := map[string]int{}
		err := e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT action, count(*) FROM platform_audit_event GROUP BY action`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var a string
				var n int
				if err := rows.Scan(&a, &n); err != nil {
					return err
				}
				out[a] = n
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// seeding promoted one platform administrator
	if got := events()["user.promote_platform_admin"]; got != 1 {
		t.Fatalf("promotion events = %d, want 1", got)
	}
	// promoting again changes nothing and records nothing
	if err := e.svc.PromotePlatformAdmin(ctx, e.w.Actor("platform_admin").UserID); err != nil {
		t.Fatal(err)
	}
	if got := events()["user.promote_platform_admin"]; got != 1 {
		t.Errorf("repeated promotion recorded again: %d", got)
	}

	// password change needs re-authentication first; set a password through the admin path, then reauth
	if err := e.svc.SetPasswordAdmin(ctx, u.UserID, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if got := events()["user.password_set_admin"]; got != 1 {
		t.Fatalf("admin password events = %d", got)
	}
	tok := e.w.Token(t, u)
	h := []string{"Authorization", "Bearer " + tok}
	e.mustJSON(e.do("POST", "/v1/auth/reauth", `{"method":"password","password":"correct horse battery"}`, false, h...), 200, nil)
	if rec := e.do("POST", "/v1/auth/password", `{"new_password":"another long passphrase"}`, false, h...); rec.Code != http.StatusNoContent {
		t.Fatalf("change password: %d %s", rec.Code, rec.Body)
	}
	if got := events()["user.password_change"]; got != 1 {
		t.Errorf("password change events = %d, want 1", got)
	}
	if rec := e.do("DELETE", "/v1/me/sessions", "", false, h...); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke all: %d %s", rec.Code, rec.Body)
	}
	if got := events()["session.revoke_all"]; got != 1 {
		t.Errorf("revoke-all events = %d, want 1", got)
	}
	tok = e.w.Token(t, u)
	if rec := e.do("DELETE", "/v1/me/sessions/current", "", false, "Authorization", "Bearer "+tok); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke current: %d %s", rec.Code, rec.Body)
	}
	if got := events()["session.revoke"]; got != 1 {
		t.Errorf("revoke events = %d, want 1", got)
	}
	// the user actor, ip and request id are recorded for a request-driven event
	var actor *uuid.UUID
	var ip, reqID *string
	err := e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT actor_user_id, host(ip), request_id FROM platform_audit_event WHERE action = 'session.revoke'`).Scan(&actor, &ip, &reqID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if actor == nil || *actor != u.UserID || ip == nil || *ip != "203.0.113.0" || reqID == nil || *reqID == "" {
		t.Errorf("session.revoke actor/ip/request id = %v %v %v", actor, ip != nil, reqID != nil)
	}
	// no secret material in any event
	var leaked int
	err = e.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM platform_audit_event WHERE diff::text ~* 'argon|correct horse|passphrase|@authz'`).Scan(&leaked)
	})
	if err != nil || leaked != 0 {
		t.Errorf("secrets or contacts in audit diffs: %d (err %v)", leaked, err)
	}
}

func TestIntegrationOrgTx(t *testing.T) {
	e := newAuthzEnv(t)
	ctx := context.Background()
	if err := OrgTx(ctx, func(pgx.Tx) error { return nil }); err == nil {
		t.Error("OrgTx outside an organization route must fail")
	}
	ctx = context.WithValue(ctx, orgScopeKey{}, orgScope{id: e.w.OrgA.ID, db: e.db})
	var got string
	if err := OrgTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_setting('app.org_id')`).Scan(&got)
	}); err != nil || got != e.w.OrgA.ID.String() {
		t.Fatalf("tenant context = %q, err %v", got, err)
	}
	if o, ok := OrgID(ctx); !ok || o != e.w.OrgA.ID {
		t.Error("OrgID")
	}
}
