package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/tenancy"
)

// M4: offboarding needs an export newer than the last change; archives expire.
func TestIntegrationOffboardNeedsFreshExportAndArchivesExpire(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	reauth := func() {
		p.w.Exec(t, `UPDATE session SET reauth_until = now() + interval '10 minutes' WHERE user_id = $1`, p.w.Actor("org_admin").UserID)
	}
	reauth()
	body := `{"confirm_site_name":"` + f.SiteName + `"}`
	p.must(p.call("POST", p.base()+"/sites/"+f.Site.String()+":offboard", body, p.adm), 409) // never exported
	exp := p.must(p.call("POST", p.base()+"/sites/"+f.Site.String()+":export", `{}`, p.adm), 202).Body
	// a change after the export makes it stale
	p.w.Exec(t, `UPDATE unit SET updated_at = now() WHERE id = $1`, f.Unit)
	r := p.call("POST", p.base()+"/sites/"+f.Site.String()+":offboard", body, p.adm)
	if r.Code != 409 || !strings.Contains(r.Raw, "stale_export") {
		t.Fatalf("stale export: %d %s", r.Code, r.Raw)
	}
	p.must(p.call("POST", p.base()+"/sites/"+f.Site.String()+":export", `{}`, p.adm), 202) // exporting again clears it
	p.must(p.call("POST", p.base()+"/sites/"+f.Site.String()+":offboard", body, p.adm), 200)

	// retention: the archive of an export older than 30 days is deleted by the job; the record stays
	id := str(exp, "id")
	if n := p.qInt(`SELECT count(*) FROM site_export WHERE id::text = $1 AND archive IS NOT NULL`, id); n != 1 {
		t.Fatal("the archive should be stored")
	}
	purger := tenancy.ArchivePurger{DB: p.e.db}
	if n, err := purger.Purge(context.Background()); err != nil || n != 0 {
		t.Fatalf("a fresh archive was purged: %d %v", n, err)
	}
	p.w.Exec(t, `UPDATE site_export SET at = now() - interval '31 days' WHERE id::text = $1`, id)
	if n, err := purger.Purge(context.Background()); err != nil || n < 1 {
		t.Fatalf("expired archive not purged: %d %v", n, err)
	}
	if n := p.qInt(`SELECT count(*) FROM site_export WHERE id::text = $1 AND archive IS NULL`, id); n != 1 {
		t.Fatal("the export record must stay without its archive")
	}
}
