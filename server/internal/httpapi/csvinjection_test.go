package httpapi

import (
	"strings"
	"testing"
)

// M1: vehicle export escapes formula cells; door numbers cannot start like a formula.
func TestIntegrationVehicleExportEscapesFormulas(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	p.w.Exec(t, `UPDATE unit SET door_number = '=cmd|calc!A0' WHERE id = $1`, f.Unit)
	p.w.Exec(t, `INSERT INTO vehicle (id, org_id, site_id, unit_id, plate, label) VALUES (gen_random_uuid(), $1, $2, $3, '34ABC123', '=HYPERLINK("http://evil","x")')`, p.org.ID, f.Site, f.Unit)
	out := p.must(p.call("GET", p.base()+"/vehicles:export?site_id="+f.Site.String(), "", p.adm), 200).Raw
	if strings.Contains(out, ",=") || strings.Contains(out, ",\"=") {
		t.Fatalf("unescaped formula cell: %s", out)
	}
	if !strings.Contains(out, "'=cmd|calc!A0") {
		t.Fatalf("door number was not escaped: %s", out)
	}
	// the import and the API refuse such door numbers
	r := p.upload(p.base()+"/units:import?dry_run=true", p.adm, map[string]string{"site_id": f.Site.String()}, "f.csv", []byte("door_number\n=1+1\n"))
	if !strings.Contains(r.Raw, "formula_prefix") {
		t.Fatalf("import accepted a formula door number: %s", r.Raw)
	}
	p.must(p.call("POST", p.base()+"/units", `{"site_id":"`+f.Site.String()+`","block_id":"`+f.Block.String()+`","door_number":"@x"}`, p.adm), 422)
}
