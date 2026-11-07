package httpapi

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"testing"
)

// H3: re-importing a file never resurrects a former occupant, never attaches
// people from outside the site and reports a different named owner.
func TestIntegrationReimportKeepsMoveOutsAndSiteBoundaries(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	imp := func(csv string, mapping string) resp {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		_ = mw.WriteField("site_id", f.Site.String())
		if mapping != "" {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", `form-data; name="mapping"`)
			h.Set("Content-Type", "application/json")
			w, _ := mw.CreatePart(h)
			_, _ = w.Write([]byte(mapping))
		}
		fw, _ := mw.CreateFormFile("file", "f.csv")
		_, _ = fw.Write([]byte(csv))
		_ = mw.Close()
		return p.call("POST", p.base()+"/units:import", b.String(), p.adm, "Content-Type", mw.FormDataContentType())
	}
	owners := func(door string) int {
		return p.qInt(`SELECT count(*) FROM occupancy o JOIN unit u ON u.org_id = o.org_id AND u.id = o.unit_id WHERE o.org_id = $1 AND u.site_id = $2 AND u.door_number = $3 AND o.kind = 'owner'`, p.org.ID, f.Site, door)
	}
	current := func(door string) int {
		return p.qInt(`SELECT count(*) FROM occupancy o JOIN unit u ON u.org_id = o.org_id AND u.id = o.unit_id WHERE o.org_id = $1 AND u.site_id = $2 AND u.door_number = $3 AND o.kind = 'owner' AND o.valid_to IS NULL`, p.org.ID, f.Site, door)
	}
	const file = "door_number,owner_name,owner_phone\nD1,Ayse Kaya,05321110000\n"
	p.must(imp(file, ""), 202)
	if owners("D1") != 1 {
		t.Fatal("first import")
	}
	// the owner moves out; the same file again must not bring them back
	p.w.Exec(t, `UPDATE occupancy SET valid_from = '2020-01-01', valid_to = '2021-01-01' WHERE org_id = $1 AND unit_id = (SELECT id FROM unit WHERE org_id = $1 AND site_id = $2 AND door_number = 'D1')`, p.org.ID, f.Site)
	r := p.must(imp(file, ""), 202)
	if current("D1") != 0 || owners("D1") != 1 {
		t.Fatalf("a former owner was resurrected: %d current, %d total", current("D1"), owners("D1"))
	}
	if rows, _ := r.Body["rows"].([]any); len(rows) == 0 || fmt.Sprint(rows[0]) == "" {
		t.Fatalf("report: %s", r.Raw)
	}
	// a different owner for the now-empty unit is fine
	p.must(imp("door_number,owner_name,owner_phone\nD1,Mehmet Demir,05322220000\n", ""), 202)
	if current("D1") != 1 {
		t.Fatal("the new owner was not added")
	}
	// yet another name: a warning, nothing changes, unless replace_owner is set
	p.must(imp("door_number,owner_name,owner_phone\nD1,Zeynep Aydin,05323330000\n", ""), 202)
	if current("D1") != 1 {
		t.Fatalf("a different owner was added silently (%d current)", current("D1"))
	}
	p.must(imp("door_number,owner_name,owner_phone\nD1,Zeynep Aydin,05323330000\n", `{"columns":{"replace_owner":"true"},"date_format":null,"delimiter":null,"sheet":null,"remember":false}`), 202)
	if current("D1") != 2 {
		t.Fatalf("replace_owner did not add the owner (%d current)", current("D1"))
	}
	// a person of another site of the organization is not matched by phone
	other := p.w.NewSite(t, p.org)
	op := p.w.NewPerson(t, p.org)
	p.w.NewOccupancy(t, other, other.Unit, op, "owner")
	p.w.Exec(t, `UPDATE person SET contact = '{"phone":"+905324440000"}' WHERE id = $1`, op)
	p.must(imp("door_number,owner_name,owner_phone\nD2,Someone Else,05324440000\n", ""), 202)
	if n := p.qInt(`SELECT count(*) FROM occupancy WHERE org_id = $1 AND person_id = $2`, p.org.ID, op); n != 1 {
		t.Fatalf("a person of another site was attached to this site's unit (%d occupancies)", n)
	}
}
