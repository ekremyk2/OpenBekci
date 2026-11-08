package httpapi

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func (p *pb) occ(unit, person uuid.UUID, body string) resp {
	return p.call("POST", p.base()+"/occupancies", fmt.Sprintf(`{"person_id":%q,"unit_id":%q,%s}`, person, unit, body), p.adm)
}

func (p *pb) patchOcc(o resp, body string) resp {
	return p.call("PATCH", p.base()+"/occupancies/"+str(o.Body, "id"), body, p.adm, "If-Match", fmt.Sprintf(`"%d"`, num(o.Body, "version")))
}

// H1: UpdateOccupancy re-checks overlap, shares and correspondents when dates change.
func TestIntegrationUpdateOccupancyKeepsUnitRules(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)

	// probe 1: shares of overlapping owners must stay within 100
	u1 := f.Unit
	a := p.must(p.occ(u1, p.w.NewPerson(t, p.org), `"kind":"owner","share_pct":60,"valid_from":"2020-01-01","valid_to":"2021-01-01"`), 201)
	p.must(p.occ(u1, p.w.NewPerson(t, p.org), `"kind":"owner","share_pct":60,"valid_from":"2021-01-01"`), 201)
	p.must(p.patchOcc(a, `{"valid_to":null}`), 422) // 120%

	// probe 2: a bounded correspondent plus an open one are never both current
	u2 := p.w.NewUnit(t, f)
	c1 := p.must(p.occ(u2, p.w.NewPerson(t, p.org), `"kind":"owner","is_correspondent":true,"valid_from":"2020-01-01","valid_to":"2021-01-01"`), 201)
	p.must(p.occ(u2, p.w.NewPerson(t, p.org), `"kind":"owner","is_correspondent":true,"valid_from":"2021-01-01"`), 201)
	p.must(p.patchOcc(c1, `{"valid_to":null}`), 409)
	if n := p.qInt(`SELECT count(*) FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND is_correspondent AND valid_to IS NULL`, p.org.ID, u2); n != 1 {
		t.Fatalf("current correspondents = %d", n)
	}

	// probe 3: a future-dated correspondent must not demote today's and leave a gap
	u3 := p.w.NewUnit(t, f)
	p.must(p.occ(u3, p.w.NewPerson(t, p.org), `"kind":"owner","is_correspondent":true,"valid_from":"2020-01-01"`), 201)
	p.must(p.occ(u3, p.w.NewPerson(t, p.org), `"kind":"owner","is_correspondent":true,"valid_from":"2099-01-01"`), 409)
	if n := p.qInt(`SELECT count(*) FROM occupancy WHERE org_id = $1 AND unit_id = $2 AND is_correspondent`, p.org.ID, u3); n != 1 {
		t.Fatalf("correspondents = %d", n)
	}

	// extending a person's own period over their other occupancy of the same kind is refused
	u4 := p.w.NewUnit(t, f)
	pp := p.w.NewPerson(t, p.org)
	x := p.must(p.occ(u4, pp, `"kind":"tenant","valid_from":"2020-01-01","valid_to":"2021-01-01"`), 201)
	p.must(p.occ(u4, pp, `"kind":"tenant","valid_from":"2021-06-01"`), 201)
	p.must(p.patchOcc(x, `{"valid_to":"2022-01-01"}`), 409)
}
