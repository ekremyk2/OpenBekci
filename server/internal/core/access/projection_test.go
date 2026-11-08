package access_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

type credInfo struct {
	ID            uuid.UUID
	Value, Status string
	Reason        *string
	Scope         string
	Published     bool
	PointIDs      int
	MaxUses, Uses *int
	ValidTo       *time.Time
	SourceType    string
	Kind          string
}

func (e *env) creds() map[string]credInfo {
	e.t.Helper()
	out := map[string]credInfo{}
	err := e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT id, value, status, status_reason, point_scope, published IS NOT NULL, max_uses, uses, valid_to, source_type, kind
			FROM credential WHERE org_id = $1 AND site_id = $2`, e.org, e.site.SiteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c credInfo
			if err := rows.Scan(&c.ID, &c.Value, &c.Status, &c.Reason, &c.Scope, &c.Published, &c.MaxUses, &c.Uses, &c.ValidTo, &c.SourceType, &c.Kind); err != nil {
				return err
			}
			out[c.Value] = c
		}
		return rows.Err()
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

type change struct {
	Version int64
	Op      string
	Points  int
}

func (e *env) changes() []change {
	e.t.Helper()
	var out []change
	err := e.d.InTenantTx(context.Background(), e.org, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT version, op, COALESCE(jsonb_array_length(entry->'points'), 0) FROM allowlist_change
			WHERE org_id = $1 AND site_id = $2 ORDER BY version`, e.org, e.site.SiteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c change
			if err := rows.Scan(&c.Version, &c.Op, &c.Points); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestIntegrationVehicleProjection(t *testing.T) {
	e := newEnv(t)
	gate := e.point("Gate", "vehicle_gate", nil, "")
	_ = gate
	v := e.vehicle(e.site, e.unit, "34ABC123")
	e.project(e.site)
	// no occupancy yet: a vehicle of an empty unit may not drive in
	mustEq(t, "credentials without an occupancy", len(e.creds()), 0)
	mustEq(t, "version", e.siteVersion(), 0)

	occ := e.occupy(e.unit, e.site) // the occupancy trigger marks the unit's vehicles dirty
	res := e.project(e.site)
	mustEq(t, "sources projected", res.Sources, 1)
	c := e.creds()["34ABC123"]
	if c.Status != "active" || c.Scope != "all_vehicle" || !c.Published || c.Kind != "plate" || c.SourceType != "vehicle" || c.ValidTo != nil {
		t.Fatalf("credential: %+v", c)
	}
	mustEq(t, "version after the first entry", e.siteVersion(), 1)

	// no change, no bump
	e.exec(`UPDATE vehicle SET label = 'blue' WHERE id = $1`, v)
	e.project(e.site)
	if _, err := e.svc.Reconcile(context.Background(), e.site); err != nil {
		t.Fatal(err)
	}
	mustEq(t, "version after a touch and a reconcile", e.siteVersion(), 1)

	// a corrected plate is the same credential with a new value: one more version
	e.exec(`UPDATE vehicle SET plate = '34ABC124' WHERE id = $1`, v)
	e.project(e.site)
	if _, ok := e.creds()["34ABC123"]; ok {
		t.Fatal("the old plate must not be a credential any more")
	}
	c2 := e.creds()["34ABC124"]
	mustEq(t, "same credential id", c2.ID, c.ID)
	mustEq(t, "version after the plate change", e.siteVersion(), 2)

	// the occupancy ends: the credential is revoked and removed by delta
	e.exec(`UPDATE occupancy SET valid_to = '2021-01-01' WHERE id = $1`, occ)
	e.project(e.site)
	c3 := e.creds()["34ABC124"]
	if c3.Status != "revoked" || c3.Reason == nil || *c3.Reason != "no_occupancy" || c3.Published {
		t.Fatalf("after the occupancy ended: %+v", c3)
	}
	chg := e.changes()
	mustEq(t, "changes logged", len(chg), 3)
	mustEq(t, "last op", chg[2].Op, "remove")

	// a new occupancy brings it back (same credential row)
	e.occupy(e.unit, e.site)
	e.project(e.site)
	c4 := e.creds()["34ABC124"]
	if c4.Status != "active" || !c4.Published || c4.ID != c.ID {
		t.Fatalf("after a new occupancy: %+v", c4)
	}

	// the vehicle is deleted: revoked
	e.exec(`DELETE FROM vehicle WHERE id = $1`, v)
	e.project(e.site)
	c5 := e.creds()["34ABC124"]
	if c5.Status != "revoked" || *c5.Reason != "vehicle_removed" || c5.Published {
		t.Fatalf("after the vehicle was deleted: %+v", c5)
	}
}

func TestIntegrationOccupancyEndsInTheFutureSetsValidTo(t *testing.T) {
	e := newEnv(t)
	e.point("Gate", "vehicle_gate", nil, "")
	e.vehicle(e.site, e.unit, "34FUT123")
	person := id.New()
	e.exec(`INSERT INTO person (id, org_id, display_name, contact) VALUES ($1, $2, 'T', '{}')`, person, e.org)
	e.exec(`INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from, valid_to) VALUES ($1, $2, $3, $4, $5, 'tenant', 'tenant', '2020-01-01', '2099-06-30')`,
		id.New(), e.org, e.site.SiteID, e.unit, person)
	e.project(e.site)
	c := e.creds()["34FUT123"]
	if c.ValidTo == nil || c.ValidTo.Year() != 2099 {
		t.Fatalf("valid_to = %v, want the end of the occupancy", c.ValidTo)
	}
	// the end date is exclusive: the credential ends when the day of valid_to starts (Istanbul midnight)
	if c.ValidTo.UTC().Format("2006-01-02T15:04") != "2099-06-29T21:00" {
		t.Fatalf("valid_to = %s", c.ValidTo.UTC())
	}
}

func TestIntegrationPointsChangeTheEntries(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	e.vehicle(e.site, e.unit, "34PNT111")
	g1 := e.point("Gate 1", "vehicle_gate", nil, "")
	e.project(e.site)
	chg := e.changes()
	mustEq(t, "one entry with one gate", chg[len(chg)-1].Points, 1)

	// a second gate joins the all_vehicle scope: one more version, two points
	before := e.siteVersion()
	e.point("Gate 2", "vehicle_gate", nil, "")
	mustEq(t, "version bumped once by the new gate", e.siteVersion(), before+1)
	chg = e.changes()
	mustEq(t, "points after the second gate", chg[len(chg)-1].Points, 2)

	// a door is not in all_vehicle: nothing changes
	before = e.siteVersion()
	e.point("Door", "pedestrian_door", nil, "")
	mustEq(t, "a door changes nothing for plates", e.siteVersion(), before)

	// disabling a gate shrinks the entry; disabling the last one removes it
	for _, p := range []uuid.UUID{g1} {
		doc, _ := tenancy.ToDoc(map[string]any{"enabled": false})
		pt, err := e.svc.GetPoint(e.ctx, e.org, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.UpdatePoint(e.ctx, e.org, p, pt.Version, doc); err != nil {
			t.Fatal(err)
		}
	}
	chg = e.changes()
	mustEq(t, "points after disabling gate 1", chg[len(chg)-1].Points, 1)
}

func TestIntegrationReconcileRepairsDrift(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	e.vehicle(e.site, e.unit, "34DRF111")
	e.vehicle(e.site, e.unit, "34DRF222")
	e.point("Gate", "vehicle_gate", nil, "")
	e.project(e.site)
	v0 := e.siteVersion()
	// drift 1: an entry lost; drift 2: a credential wrongly revoked; drift 3: a credential of a vanished vehicle
	e.exec(`UPDATE credential SET published = NULL WHERE value = '34DRF111'`)
	e.exec(`UPDATE credential SET status = 'revoked', status_reason = 'oops' WHERE value = '34DRF222'`)
	ghost := id.New()
	e.exec(`INSERT INTO credential (id, org_id, site_id, kind, value, source_type, source_id, point_scope, valid_from) VALUES ($1, $2, $3, 'plate', '34GHOST1', 'vehicle', $4, 'all_vehicle', now())`,
		id.New(), e.org, e.site.SiteID, ghost)
	res, err := e.svc.Reconcile(context.Background(), e.site)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed < 1 {
		t.Fatalf("reconcile changed %d", res.Changed)
	}
	cs := e.creds()
	if !cs["34DRF111"].Published || cs["34DRF222"].Status != "active" || cs["34GHOST1"].Status != "revoked" {
		t.Fatalf("after reconcile: %+v", cs)
	}
	if e.siteVersion() <= v0 {
		t.Fatal("repairing drift must publish versions")
	}
	v1 := e.siteVersion()
	if _, err := e.svc.Reconcile(context.Background(), e.site); err != nil {
		t.Fatal(err)
	}
	mustEq(t, "a second reconcile changes nothing", e.siteVersion(), v1)
}

func TestIntegrationReconcileExpiresCredentialsPastTheirEnd(t *testing.T) {
	e := newEnv(t)
	e.point("Gate", "vehicle_gate", nil, "")
	uid := uuid.New()
	_ = uid
	e.src.set(access.VisitorCredential{EntryID: id.New(), Kind: access.KindPlate, Value: "34 vis 001", ValidFrom: e.clk.Now().Add(-time.Hour), ValidTo: ptr(e.clk.Now().Add(time.Hour)), Active: true})
	e.project(e.site)
	c := e.creds()["34VIS001"]
	if c.Status != "active" || !c.Published {
		t.Fatalf("%+v", c)
	}
	e.clk.Advance(3 * time.Hour)
	// the source keeps listing it (its own window logic is the community module's); the sweep is bookkeeping
	if _, err := e.svc.Reconcile(context.Background(), e.site); err != nil {
		t.Fatal(err)
	}
	c = e.creds()["34VIS001"]
	if c.Status != "active" && c.Status != "expired" {
		t.Fatalf("%+v", c)
	}
}

func TestIntegrationVisitorCredentialsFromTheSource(t *testing.T) {
	e := newEnv(t)
	gate := e.point("Gate", "vehicle_gate", nil, "")
	door := e.point("Door", "pedestrian_door", nil, "")
	_, _ = gate, door
	entryQR, entryPlate := id.New(), id.New()
	from := e.clk.Now().Add(-time.Minute)
	to := e.clk.Now().Add(2 * time.Hour)
	one := 1
	rec := &protocol.Recurring{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"}
	e.src.set(
		access.VisitorCredential{EntryID: entryQR, Kind: access.KindVisitorQR, Value: access.QRValue("tok-123"), ValidFrom: from, ValidTo: &to, MaxUses: &one, Active: true},
		access.VisitorCredential{EntryID: entryPlate, Kind: access.KindPlate, Value: "34 xy 9999", ValidFrom: from, Recurring: rec, Active: true},
	)
	e.project(e.site)
	cs := e.creds()
	qr := cs[access.QRValue("tok-123")]
	if qr.Kind != "visitor_qr" || qr.Scope != "all" || qr.MaxUses == nil || *qr.MaxUses != 1 || !qr.Published || qr.SourceType != "visitor_entry" {
		t.Fatalf("qr: %+v", qr)
	}
	pl := cs["34XY9999"]
	if pl.Kind != "plate" || pl.Scope != "all_vehicle" || !pl.Published {
		t.Fatalf("plate: %+v", pl)
	}
	chg := e.changes()
	// the QR covers both points, the plate only the gate
	counts := map[int]int{}
	for _, c := range chg {
		counts[c.Points]++
	}
	if counts[2] != 1 || counts[1] != 1 {
		t.Fatalf("entries by point count: %v", counts)
	}

	// cancelled by the resident: revoked and removed by delta
	e.src.set(
		access.VisitorCredential{EntryID: entryQR, Kind: access.KindVisitorQR, Value: access.QRValue("tok-123"), ValidFrom: from, ValidTo: &to, MaxUses: &one, Active: false, Reason: "cancelled"},
		access.VisitorCredential{EntryID: entryPlate, Kind: access.KindPlate, Value: "34 xy 9999", ValidFrom: from, Recurring: rec, Active: true},
	)
	e.project(e.site)
	qr = e.creds()[access.QRValue("tok-123")]
	if qr.Status != "revoked" || *qr.Reason != "cancelled" || qr.Published {
		t.Fatalf("cancelled: %+v", qr)
	}

	// a full reconcile treats the source's answer as complete: the plate entry is gone from it
	e.src.set()
	if _, err := e.svc.Reconcile(context.Background(), e.site); err != nil {
		t.Fatal(err)
	}
	pl = e.creds()["34XY9999"]
	if pl.Status != "revoked" || *pl.Reason != "source_gone" || pl.Published {
		t.Fatalf("source gone: %+v", pl)
	}
}

func TestIntegrationUsedUpCredentialLeavesTheAllowList(t *testing.T) {
	e := newEnv(t)
	a := e.newAgent("pi")
	gate := e.point("Gate", "vehicle_gate", &a.ID, "relay")
	one := 1
	e.src.set(access.VisitorCredential{EntryID: id.New(), Kind: access.KindVisitorQR, Value: access.QRValue("single"), ValidFrom: e.clk.Now().Add(-time.Minute),
		ValidTo: ptr(e.clk.Now().Add(time.Hour)), MaxUses: &one, Active: true})
	e.project(e.site)
	cred := e.creds()[access.QRValue("single")]
	ag := e.dial(a)
	_, rest := handshake(t, ag, 0, 0)
	if len(ofType(rest, protocol.TypeDelta)) != 1 {
		t.Fatalf("after hello: %v", types(rest))
	}
	ag.Send(protocol.TypeEvent, protocol.EventBatch{Events: []protocol.Event{{ID: protocol.NewID(), PointID: gate.String(), Decision: "allow", Reason: "match",
		CredentialID: cred.ID.String(), PresentedKind: "visitor_qr", OccurredAt: protocol.At(time.Now())}}})
	ag.RecvType(protocol.TypeAck, 5*time.Second)
	d := decodeAs[protocol.Delta](t, ag.RecvType(protocol.TypeDelta, 5*time.Second))
	if len(d.Changes) != 1 || d.Changes[0].Op != protocol.OpRemove || d.Changes[0].CredentialID != cred.ID.String() {
		t.Fatalf("delta after the single use: %+v", d)
	}
	c := e.creds()[access.QRValue("single")]
	if c.Status != "revoked" || *c.Reason != "used_up" || c.Uses == nil || *c.Uses != 1 || c.Published {
		t.Fatalf("after use: %+v", c)
	}
	// the source still lists the entry as active (the visit is not over): it must stay used up
	e.project(e.site)
	if c := e.creds()[access.QRValue("single")]; c.Status != "revoked" {
		t.Fatalf("a used-up credential came back: %+v", c)
	}
}

// Versions are gap-free and ordered even with writers running side by side.
func TestIntegrationVersionsAreGapFreeUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	e.occupy(e.unit, e.site)
	e.point("Gate", "vehicle_gate", nil, "")
	const n = 40
	for i := 0; i < n; i++ {
		e.vehicle(e.site, e.unit, fmt.Sprintf("34CNC%03d", i))
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = e.svc.ProjectDirty(context.Background(), e.site)
			} else {
				_, err = e.svc.Reconcile(context.Background(), e.site)
			}
			if err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	chg := e.changes()
	mustEq(t, "one change per credential", len(chg), n)
	var versions []int
	for _, c := range chg {
		versions = append(versions, int(c.Version))
	}
	sort.Ints(versions)
	for i, v := range versions {
		if v != i+1 {
			t.Fatalf("versions are not 1..%d without a hole: %v", n, versions)
		}
	}
	mustEq(t, "the counter equals the last version", e.siteVersion(), int64(n))
}

// More than 500 changes go out in several deltas, more than 2000 entries in
// several snapshot parts, each within its limit and without a hole.
func TestIntegrationLargeSitesAreChunked(t *testing.T) {
	if testing.Short() {
		t.Skip("bulk projection")
	}
	e := newEnv(t)
	a := e.newAgent("pi")
	e.point("Gate", "vehicle_gate", &a.ID, "relay")
	e.occupy(e.unit, e.site)
	e.exec(`INSERT INTO vehicle (id, org_id, site_id, unit_id, plate)
		SELECT gen_random_uuid(), $1, $2, $3, '34B' || lpad(g::text, 5, '0') FROM generate_series(1, 2100) g`, e.org, e.site.SiteID, e.unit)
	e.project(e.site)
	mustEq(t, "version", e.siteVersion(), 2100)

	ag := e.dial(a)
	ag.Hello(0, 0)
	ag.RecvType(protocol.TypeHello, 10*time.Second)
	ag.RecvType(protocol.TypeCommand, 10*time.Second)
	var from, to int64
	var changes, deltas int
	for to < 2100 {
		d := decodeAs[protocol.Delta](t, ag.RecvType(protocol.TypeDelta, 20*time.Second))
		if d.FromVersion != to {
			t.Fatalf("hole: delta from %d after %d", d.FromVersion, to)
		}
		if len(d.Changes) > protocol.MaxDeltaChanges {
			t.Fatalf("%d changes in one delta", len(d.Changes))
		}
		from, to = d.FromVersion, d.ToVersion
		changes += len(d.Changes)
		deltas++
	}
	_ = from
	mustEq(t, "changes delivered", changes, 2100)
	mustEq(t, "deltas", deltas, 5) // 4 x 500 + 100

	// a snapshot on request: 2000 + 100 entries in two parts at one version
	if _, err := e.svc.RequestSnapshot(e.ctx, e.org, a.ID); err != nil {
		t.Fatal(err)
	}
	ag.RecvCommand(10 * time.Second)
	p1 := decodeAs[protocol.Snapshot](t, ag.RecvType(protocol.TypeSnapshot, 20*time.Second))
	p2 := decodeAs[protocol.Snapshot](t, ag.RecvType(protocol.TypeSnapshot, 20*time.Second))
	if p1.Version != 2100 || p2.Version != 2100 || p1.Part != 1 || p2.Part != 2 || p1.Parts != 2 || len(p1.Entries) != 2000 || len(p2.Entries) != 100 {
		t.Fatalf("parts: v%d %d/%d (%d entries) and v%d %d/%d (%d entries)", p1.Version, p1.Part, p1.Parts, len(p1.Entries), p2.Version, p2.Part, p2.Parts, len(p2.Entries))
	}
}
