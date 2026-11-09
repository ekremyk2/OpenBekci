package httpapi

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Matrix rows of the community module (the draft contract's role-map form of
// x-openbekci-roles: holding any one of the listed permissions suffices). Every
// build seeds what it needs afresh through the platform pool.

// wideRules is a facility that opens round the clock, any day, with no quota or fee.
const wideRules = `{"opening_hours":[{"days":[1,2,3,4,5,6,7],"start":"00:00","end":"00:00"}],"max_duration_minutes":480,"max_days_ahead":90,` +
	`"min_notice_minutes":null,"quota_per_unit":null,"quota_period":null,"who_may_book":["owner","tenant","household"],"fee":null,"cancel_refund_cutoff_hours":null}`

func cmFacility(w *authztest.World, t *testing.T, f authztest.SiteFix) uuid.UUID {
	t.Helper()
	fid := id.New()
	w.Exec(t, `INSERT INTO facility (id, org_id, site_id, name, rules) VALUES ($1, $2, $3, 'Hall', $4::jsonb)`, fid, f.Org.ID, f.Site, wideRules)
	return fid
}

func cmBooking(w *authztest.World, t *testing.T, f authztest.SiteFix, fid uuid.UUID) uuid.UUID {
	t.Helper()
	b := id.New()
	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	w.Exec(t, `INSERT INTO booking (id, org_id, site_id, facility_id, unit_id, start_at, end_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		b, f.Org.ID, f.Site, fid, f.Unit, start, start.Add(time.Hour))
	return b
}

func cmEntry(w *authztest.World, t *testing.T, f authztest.SiteFix, status string, from, to time.Time) uuid.UUID {
	t.Helper()
	e := id.New()
	_, h, _ := crypto.NewToken()
	w.Exec(t, `INSERT INTO visitor_entry (id, org_id, site_id, unit_id, name, window_from, window_to, approval_status, source, qr_token_hash)
		VALUES ($1, $2, $3, $4, 'Guest', $5, $6, $7, 'pre_registered', $8)`, e, f.Org.ID, f.Site, f.Unit, from, to, status, h)
	return e
}

func cmRecurring(w *authztest.World, t *testing.T, f authztest.SiteFix) uuid.UUID {
	t.Helper()
	r := id.New()
	_, h, _ := crypto.NewToken()
	w.Exec(t, `INSERT INTO recurring_visitor (id, org_id, site_id, unit_id, name, days, start_time, end_time, valid_from, qr_token_hash)
		VALUES ($1, $2, $3, $4, 'Cleaner', '{1,2,3}', '09:00', '17:00', '2026-01-01', $5)`, r, f.Org.ID, f.Site, f.Unit, h)
	return r
}

func cmDelivery(w *authztest.World, t *testing.T, f authztest.SiteFix) uuid.UUID {
	t.Helper()
	d := id.New()
	w.Exec(t, `INSERT INTO delivery (id, org_id, site_id, unit_id) VALUES ($1, $2, $3, $4)`, d, f.Org.ID, f.Site, f.Unit)
	return d
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func init() {
	ifm := "If-Match"
	idem := func() string { return fmt.Sprintf("matrix-key-%d", bSeq.Add(1)) }
	p := perms
	// the operations accept any one of their permissions: one "a|b" alternatives name, sorted as the spec lists it
	anyP := func(ps ...authz.Permission) []authz.Permission {
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
		return []authz.Permission{authz.AnyOf(ps...)}
	}
	bookRead := anyP(community.PermBookingsRead, community.PermFacilitiesManage)
	siteQ := func(org authztest.Org, f authztest.SiteFix, path string) string {
		return orgPath(org, path+"?site_id="+f.Site.String())
	}
	rows := map[string]mrow{
		"community.listFacilities": {perms: bookRead, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/facilities")}
		}},
		"community.createFacility": {perms: p(community.PermFacilitiesManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/facilities"), fmt.Sprintf(`{"site_id":%q,"name":"Hall","rules":%s}`, f.Site, wideRules))
		}},
		"community.getFacility": {perms: bookRead, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/facilities/"+cmFacility(w, t, w.NewSite(t, org)).String())}
		}},
		"community.updateFacility": {perms: p(community.PermFacilitiesManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/facilities/"+cmFacility(w, t, w.NewSite(t, org)).String()), `{"name":"Renamed hall"}`, ifm, `"1"`)
		}},
		"community.deleteFacility": {perms: p(community.PermFacilitiesManage), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("DELETE", orgPath(org, "/facilities/"+cmFacility(w, t, w.NewSite(t, org)).String()), "", ifm, `"1"`)
		}},
		"community.getFacilityAvailability": {perms: p(community.PermBookingsCreate), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			fid := cmFacility(w, t, w.NewSite(t, org))
			now := time.Now()
			return mreq{method: "GET", path: orgPath(org, "/facilities/"+fid.String()+"/availability?from="+rfc(now)+"&to="+rfc(now.Add(7*24*time.Hour)))}
		}},
		"community.listFacilityBlocks": {perms: bookRead, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/facilities/"+cmFacility(w, t, w.NewSite(t, org)).String()+"/blocks")}
		}},
		"community.createFacilityBlock": {perms: p(community.PermFacilitiesManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			fid := cmFacility(w, t, w.NewSite(t, org))
			s := time.Now().Add(200 * time.Hour)
			return jsonReq("POST", orgPath(org, "/facilities/"+fid.String()+"/blocks"), fmt.Sprintf(`{"start":%q,"end":%q,"reason":"Painting"}`, rfc(s), rfc(s.Add(2*time.Hour))))
		}},
		"community.deleteFacilityBlock": {perms: p(community.PermFacilitiesManage), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			fid := cmFacility(w, t, f)
			b := id.New()
			s := time.Now().Add(200 * time.Hour)
			w.Exec(t, `INSERT INTO facility_block (id, org_id, site_id, facility_id, start_at, end_at) VALUES ($1, $2, $3, $4, $5, $6)`, b, org.ID, f.Site, fid, s, s.Add(time.Hour))
			return mreq{method: "DELETE", path: orgPath(org, "/facilities/"+fid.String()+"/blocks/"+b.String())}
		}},

		"community.listBookings": {perms: p(community.PermBookingsRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/bookings")}
		}},
		"community.createBooking": {perms: p(community.PermBookingsCreate), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			fid := cmFacility(w, t, f)
			start := time.Now().UTC().Add(48 * time.Hour).Truncate(24 * time.Hour).Add(9 * time.Hour)
			return jsonReq("POST", orgPath(org, "/bookings"), fmt.Sprintf(`{"facility_id":%q,"unit_id":%q,"start":%q,"end":%q}`, fid, f.Unit, rfc(start), rfc(start.Add(time.Hour))), "Idempotency-Key", idem())
		}},
		"community.getBooking": {perms: p(community.PermBookingsRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return mreq{method: "GET", path: orgPath(org, "/bookings/"+cmBooking(w, t, f, cmFacility(w, t, f)).String())}
		}},
		"community.cancelBooking": {perms: anyP(community.PermBookingsManage, community.PermBookingsCreate), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/bookings/"+cmBooking(w, t, f, cmFacility(w, t, f)).String()+":cancel"), `{"reason":"Matrix"}`, "Idempotency-Key", idem())
		}},
		"community.previewBooking": {perms: p(community.PermBookingsCreate), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			fid := cmFacility(w, t, f)
			start := time.Now().UTC().Add(48 * time.Hour).Truncate(24 * time.Hour).Add(9 * time.Hour)
			return jsonReq("POST", orgPath(org, "/facilities/"+fid.String()+"/bookings:preview"), fmt.Sprintf(`{"facility_id":%q,"unit_id":%q,"start":%q,"end":%q}`, fid, f.Unit, rfc(start), rfc(start.Add(time.Hour))))
		}},

		"community.listVisitorEntries": {perms: anyP(community.PermVisitorsRead, community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/visitor-entries")}
		}},
		"community.createVisitorEntry": {perms: p(community.PermVisitorsManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			s := time.Now().Add(time.Hour)
			return jsonReq("POST", orgPath(org, "/visitor-entries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Mehmet Kaya","plate":"34 abc 123","window_from":%q,"window_to":%q}`,
				f.Site, f.Unit, rfc(s), rfc(s.Add(2*time.Hour))))
		}},
		"community.getVisitorEntry": {perms: anyP(community.PermVisitorsRead, community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return mreq{method: "GET", path: orgPath(org, "/visitor-entries/"+cmEntry(w, t, f, "pre_approved", time.Now(), time.Now().Add(time.Hour)).String())}
		}},
		"community.updateVisitorEntry": {perms: p(community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			e := cmEntry(w, t, f, "pre_approved", time.Now(), time.Now().Add(time.Hour))
			return jsonReq("PATCH", orgPath(org, "/visitor-entries/"+e.String()), `{"name":"Renamed guest"}`, ifm, `"1"`)
		}},
		"community.cancelVisitorEntry": {perms: p(community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			e := cmEntry(w, t, f, "pre_approved", time.Now(), time.Now().Add(time.Hour))
			return jsonReq("POST", orgPath(org, "/visitor-entries/"+e.String()+":cancel"), "")
		}},
		"community.rotateVisitorEntryQr": {perms: p(community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			e := cmEntry(w, t, f, "pre_approved", time.Now(), time.Now().Add(time.Hour))
			return jsonReq("POST", orgPath(org, "/visitor-entries/"+e.String()+":rotate-qr"), "")
		}},
		"community.rotateRecurringVisitorQr": {perms: p(community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/recurring-visitors/"+cmRecurring(w, t, w.NewSite(t, org)).String()+":rotate-qr"), "")
		}},
		"community.listRecurringVisitors": {perms: anyP(community.PermVisitorsRead, community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/recurring-visitors")}
		}},
		"community.createRecurringVisitor": {perms: p(community.PermVisitorsManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/recurring-visitors"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Cleaner","days":[1,3],"start":"09:00","end":"17:00","valid_from":"2026-01-01"}`, f.Site, f.Unit))
		}},
		"community.getRecurringVisitor": {perms: anyP(community.PermVisitorsRead, community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/recurring-visitors/"+cmRecurring(w, t, w.NewSite(t, org)).String())}
		}},
		"community.updateRecurringVisitor": {perms: p(community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/recurring-visitors/"+cmRecurring(w, t, w.NewSite(t, org)).String()), `{"end":"18:00"}`, ifm, `"1"`)
		}},
		"community.revokeRecurringVisitor": {perms: p(community.PermVisitorsManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/recurring-visitors/"+cmRecurring(w, t, w.NewSite(t, org)).String()+":revoke"), "")
		}},
		// residents only: no role holds visitors.decide, so every role of the matrix is refused before the handler
		"community.listVisitorApprovalRequests": {perms: p(community.PermVisitorsDecide), status: 200, build: mget("/v1/orgs/{org_id}/visitor-approval-requests")},
		"community.getVisitorApprovalRequest": {perms: anyP(community.PermVisitorsRead, community.PermVisitorsDecide), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			e := cmEntry(w, t, f, "requested", time.Now(), time.Now())
			rid := id.New()
			w.Exec(t, `INSERT INTO visitor_approval_request (id, org_id, site_id, entry_id, unit_id, expires_at) VALUES ($1, $2, $3, $4, $5, now() + interval '10 minutes')`, rid, org.ID, f.Site, e, f.Unit)
			return mreq{method: "GET", path: orgPath(org, "/visitor-approval-requests/"+rid.String())}
		}},
		"community.decideVisitorApprovalRequest": {perms: p(community.PermVisitorsDecide), status: 200, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/visitor-approval-requests/"+uuid.NewString()+":decide"), `{"decision":"approve"}`)
		}},
		"community.listDeliveries": {perms: anyP(community.PermDeliveriesRead, community.PermVisitorsRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/deliveries")}
		}},

		"gate.listGuards": {perms: p(community.PermGateOperate), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/gate/guards")}
		}},
		"community.createGuardSession": {perms: p(community.PermGateOperate), status: 401 /* a person without a PIN: invalid_grant */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/gate/sessions"), fmt.Sprintf(`{"site_id":%q,"person_id":%q,"pin":"1234"}`, f.Site, w.NewPerson(t, org)))
		}},
		"community.setGuardPin": {perms: p(community.PermGateManage), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			person := w.NewPerson(t, org)
			w.NewMembership(t, org, person, string(authz.RoleGuard))
			return jsonReq("PUT", orgPath(org, "/persons/"+person.String()+"/gate-pin"), `{"pin":"4321"}`)
		}},
		"community.getGateConsole": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/gate/console")}
		}},
		"community.listGateVisitors": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/gate/visitors")}
		}},
		"community.getGateVisitor": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return mreq{method: "GET", path: orgPath(org, "/gate/visitors/"+cmEntry(w, t, f, "pre_approved", time.Now(), time.Now().Add(time.Hour)).String())}
		}},
		"community.scanGateVisitor": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/gate/visitors:scan"), `{"token":"obk1.0123456789012345678901234567890123456789012"}`)
		}},
		"community.createWalkInVisitor": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			w.NewOccupancy(t, f, f.Unit, w.NewPerson(t, org), "owner")
			return jsonReq("POST", orgPath(org, "/gate/visitors:walk-in"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Courier","plate":"34 xyz 99"}`, f.Site, f.Unit))
		}},
		"community.arriveGateVisitor": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			w.NewOccupancy(t, f, f.Unit, w.NewPerson(t, org), "owner") // a unit nobody lives in admits nobody
			e := cmEntry(w, t, f, "pre_approved", time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
			return jsonReq("POST", orgPath(org, "/gate/visitors/"+e.String()+":arrive"), "")
		}},
		"community.leaveGateVisitor": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			e := cmEntry(w, t, f, "pre_approved", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
			w.Exec(t, `UPDATE visitor_entry SET arrived_at = now() WHERE id = $1`, e)
			return jsonReq("POST", orgPath(org, "/gate/visitors/"+e.String()+":leave"), "")
		}},
		"community.getGateVisitorCallContact": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			person := w.NewPerson(t, org)
			w.Exec(t, `UPDATE person SET gate_contact_phone = '+905550001122' WHERE id = $1`, person)
			w.NewOccupancy(t, f, f.Unit, person, "owner")
			e := cmEntry(w, t, f, "requested", time.Now(), time.Now())
			w.Exec(t, `INSERT INTO visitor_approval_request (id, org_id, site_id, entry_id, unit_id, expires_at) VALUES ($1, $2, $3, $4, $5, now() + interval '10 minutes')`, id.New(), org.ID, f.Site, e, f.Unit)
			return mreq{method: "GET", path: orgPath(org, "/gate/visitors/"+e.String()+"/call-contact")}
		}},
		"community.searchGatePlates": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			w.NewVehicle(t, f)
			return mreq{method: "GET", path: orgPath(org, "/gate/plates?site_id="+f.Site.String()+"&q=34TST")}
		}},
		"gate.searchUnits": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/gate/units?site_id="+w.NewSite(t, org).Site.String()+"&q=1")}
		}},
		"community.listGateDeliveries": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: siteQ(org, w.NewSite(t, org), "/gate/deliveries")}
		}},
		"community.createGateDelivery": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/gate/deliveries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"description":"Blue box"}`, f.Site, f.Unit))
		}},
		"community.collectGateDelivery": {perms: p(community.PermGateOperate), status: 401 /* a guard operation needs a guard session: guard_session_required */, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			d := cmDelivery(w, t, w.NewSite(t, org))
			return jsonReq("POST", orgPath(org, "/gate/deliveries/"+d.String()+":collect"), `{"collected_by_name":"Ayse"}`)
		}},
	}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}
