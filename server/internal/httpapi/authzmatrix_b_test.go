package httpapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/crypto"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Matrix rows of phase B (tenancy and people). Every build seeds the
// resources it needs afresh through authztest, so the repeated mutating calls
// of the matrix (every role, every organization) always find their target.

func perms(p ...authz.Permission) []authz.Permission { return p }

var bSeq atomic.Int64

func orgPath(org authztest.Org, p string) string { return "/v1/orgs/" + org.ID.String() + p }

func jsonReq(method, path, body string, h ...string) mreq {
	r := mreq{method: method, path: path, body: body}
	if len(h) > 0 {
		r.headers = map[string]string{}
		for i := 0; i+1 < len(h); i += 2 {
			r.headers[h[i]] = h[i+1]
		}
	}
	return r
}

func multipartReq(path string, fields map[string]string, filename, content string) mreq {
	return multipartField(path, "file", fields, filename, content)
}

func multipartField(path, field string, fields map[string]string, filename, content string) mreq {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile(field, filename)
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	return mreq{method: "POST", path: path, body: b.String(), headers: map[string]string{"Content-Type": mw.FormDataContentType()}}
}

// tokenFor returns a well-formed invitation token of org and the hash stored for it.
func tokenFor(org authztest.Org) (string, []byte) {
	tok := base64.RawURLEncoding.EncodeToString(org.ID[:]) + "." + fmt.Sprintf("secret%d", bSeq.Add(1))
	return tok, crypto.HashToken(tok)
}

type idFn = func(*authztest.World, authztest.Org, *testing.T) string

func init() {
	ifm := "If-Match"
	one := `"1"`
	idem := func() []string { return []string{"Idempotency-Key", "matrix-" + uuid.NewString()} }
	// get builds GET path with {id} replaced by mk's resource.
	get := func(path string, ps []authz.Permission, mk idFn) mrow {
		return mrow{perms: ps, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, strings.ReplaceAll(path, "{id}", mk(w, org, t)))}
		}}
	}
	// siteGet is GET /sites/{site}{suffix}.
	siteGet := func(suffix string, ps []authz.Permission) mrow {
		return mrow{perms: ps, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/sites/"+w.NewSite(t, org).Site.String()+suffix)}
		}}
	}
	// siteQuery is a list under the organization with the site as a query parameter.
	siteQuery := func(path string, ps []authz.Permission) mrow {
		return mrow{perms: ps, status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, path+"?site_id="+w.NewSite(t, org).Site.String())}
		}}
	}
	// send builds a request with {id} replaced by mk's resource and the given body.
	send := func(method, path string, ps []authz.Permission, status int, mk idFn, body string, h ...string) mrow {
		return mrow{perms: ps, status: status, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq(method, orgPath(org, strings.ReplaceAll(path, "{id}", mk(w, org, t))), body, h...)
		}}
	}
	siteID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewSite(t, org).Site.String()
	}
	blockID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewSite(t, org).Block.String()
	}
	unitID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewSite(t, org).Unit.String()
	}
	keyID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewSite(t, org).Key.String()
	}
	personID := func(w *authztest.World, org authztest.Org, t *testing.T) string { return w.NewPerson(t, org).String() }
	vehicleID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewVehicle(t, w.NewSite(t, org)).String()
	}
	vendorID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewVendor(t, w.NewSite(t, org)).String()
	}
	exportID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewExport(t, w.NewSite(t, org)).String()
	}
	membershipID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewMembership(t, org, w.NewPerson(t, org), "staff").String()
	}
	invitationID := func(kind string) idFn {
		return func(w *authztest.World, org authztest.Org, t *testing.T) string {
			_, h := tokenFor(org)
			return w.NewInvitation(t, w.NewSite(t, org), kind, h).String()
		}
	}
	occID := func(kind string) idFn {
		return func(w *authztest.World, org authztest.Org, t *testing.T) string {
			f := w.NewSite(t, org)
			return w.NewOccupancy(t, f, f.Unit, w.NewPerson(t, org), kind).String()
		}
	}
	claimID := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewClaim(t, w.NewSite(t, org), w.Actor("no_membership").UserID).String()
	}
	emptyBlock := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		f := w.NewSite(t, org)
		b := id.New()
		w.Exec(t, `INSERT INTO block (id, org_id, site_id, name) VALUES ($1, $2, $3, 'Empty')`, b, org.ID, f.Site)
		return b.String()
	}
	spareUnit := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		return w.NewUnit(t, w.NewSite(t, org)).String()
	}
	customKey := func(w *authztest.World, org authztest.Org, t *testing.T) string {
		f := w.NewSite(t, org)
		k := id.New()
		w.Exec(t, `INSERT INTO allocation_key (id, org_id, site_id, code, name, kind) VALUES ($1, $2, $3, 'custom', 'Custom', 'table')`, k, org.ID, f.Site)
		return k.String()
	}
	noReauth := func(actor, target string) (int, bool) {
		if actor == "org_admin" && target == "own" {
			return 403, true // needs a recent re-authentication
		}
		return 0, false
	}
	noReauthAny := func(actor, target string) (int, bool) {
		if (actor == "org_admin" || actor == "site_manager") && target == "own" {
			return 403, true
		}
		return 0, false
	}
	withOverride := func(r mrow, o func(string, string) (int, bool)) mrow { r.override = o; return r }
	notTheirUnit := func(actor, target string) (int, bool) {
		if target == "own" && (actor == "org_admin" || actor == "site_manager") {
			return 404, true // managers own no unit: the household of one's own unit is empty
		}
		return 0, false
	}
	anon404 := func(actor, _ string) (int, bool) {
		if actor == "anonymous" {
			return 404, true
		}
		return 0, false
	}
	rows := map[string]mrow{
		"codes.listCodes": {perms: perms(people.PermCodesRead), status: 200, build: mget("/v1/orgs/{org_id}/codes")},
		"people.previewInvitation": {status: 404, build: func(_ *authztest.World, _ authztest.Org, _ *testing.T) mreq {
			return mreq{method: "GET", path: "/v1/invitations/not-a-token"}
		}},
		"people.acceptInvitation": {status: 404, build: func(_ *authztest.World, _ authztest.Org, _ *testing.T) mreq {
			return jsonReq("POST", "/v1/invitations/not-a-token:accept", `{}`)
		}, override: anon404},
		"people.getJoinInfo": {status: 404, build: func(_ *authztest.World, _ authztest.Org, _ *testing.T) mreq {
			return mreq{method: "GET", path: "/v1/join/not-a-code"}
		}, override: anon404},
		"people.listJoinUnits": {status: 404, build: func(_ *authztest.World, _ authztest.Org, _ *testing.T) mreq {
			return mreq{method: "GET", path: "/v1/join/not-a-code/units"}
		}, override: anon404},
		"people.createUnitClaim": {status: 404, build: func(_ *authztest.World, _ authztest.Org, _ *testing.T) mreq {
			return jsonReq("POST", "/v1/join/not-a-code/claims", fmt.Sprintf(`{"unit_id":%q,"claimed_role":"owner"}`, uuid.New()))
		}},

		// tenancy
		"tenancy.getOrg": {perms: perms(tenancy.PermOrgRead), status: 200, build: mget("/v1/orgs/{org_id}")},
		"tenancy.updateOrg": {perms: perms(tenancy.PermOrgManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", "/v1/orgs/"+org.ID.String(), `{"name":"Renamed by the matrix","settings":{"matrix":true}}`, ifm, `"`+strconv.Itoa(w.OrgVersion(t, org))+`"`)
		}},
		"tenancy.listSites": {perms: perms(tenancy.PermSiteRead), status: 200, build: mget("/v1/orgs/{org_id}/sites")},
		"tenancy.createSite": {perms: perms(tenancy.PermSiteManage), status: 201,
			build: fixed("POST", "/v1/orgs/{org_id}/sites", `{"name":"Matrix site","timezone":"Europe/Istanbul","currency":"TRY"}`)},
		"tenancy.getSite":            get("/sites/{id}", perms(tenancy.PermSiteRead), siteID),
		"tenancy.updateSite":         send("PATCH", "/sites/{id}", perms(tenancy.PermSiteManage), 200, siteID, `{"name":"Renamed"}`, ifm, one),
		"tenancy.getSiteSettings":    siteGet("/settings", perms(tenancy.PermSiteRead)),
		"tenancy.updateSiteSettings": send("PATCH", "/sites/{id}/settings", perms(tenancy.PermSiteManage), 200, siteID, `{"bills_enabled":true,"owner_financial_transparency":true}`, ifm, one),
		"tenancy.getBankDetails":     siteGet("/bank-details", perms(tenancy.PermSiteBankDetails)),
		"tenancy.setBankDetails": send("PUT", "/sites/{id}/bank-details", perms(tenancy.PermSiteManage), 200, siteID,
			`{"iban":"TR330006100519786457841326","holder":"Matrix Site Management"}`, ifm, one),
		"tenancy.listBlocks": siteQuery("/blocks", perms(tenancy.PermSiteRead)),
		"tenancy.createBlock": {perms: perms(tenancy.PermSiteManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/blocks"), fmt.Sprintf(`{"site_id":%q,"name":"Block X"}`, w.NewSite(t, org).Site))
		}},
		"tenancy.getBlock":    get("/blocks/{id}", perms(tenancy.PermSiteRead), blockID),
		"tenancy.updateBlock": send("PATCH", "/blocks/{id}", perms(tenancy.PermSiteManage), 200, blockID, `{"name":"Renamed block"}`, ifm, one),
		"tenancy.deleteBlock": send("DELETE", "/blocks/{id}", perms(tenancy.PermSiteManage), 204, emptyBlock, "", ifm, one),
		"tenancy.listUnits":   siteQuery("/units", perms(tenancy.PermUnitRead)),
		"tenancy.createUnit": {perms: perms(tenancy.PermUnitManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/units"), fmt.Sprintf(`{"site_id":%q,"block_id":%q,"door_number":"Z-1","type":"flat"}`, f.Site, f.Block))
		}},
		"tenancy.getUnit":     get("/units/{id}", perms(tenancy.PermUnitRead), unitID),
		"tenancy.updateUnit":  send("PATCH", "/units/{id}", perms(tenancy.PermUnitManage), 200, unitID, `{"area_m2":90.5,"payer_overrides":{"aidat":"tenant"}}`, ifm, one),
		"tenancy.deleteUnit":  send("DELETE", "/units/{id}", perms(tenancy.PermUnitManage), 204, spareUnit, "", ifm, one),
		"tenancy.setUnitTags": send("PUT", "/units/{id}/tags", perms(tenancy.PermUnitManage), 200, unitID, `{"tags":["lift"]}`, ifm, one),
		"tenancy.importUnits": {perms: perms(tenancy.PermUnitImport), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return multipartReq(orgPath(org, "/units:import?dry_run=true"), map[string]string{"site_id": f.Site.String()}, "units.csv", "door_number,area_m2\nM-1,80\n")
		}},
		"tenancy.listAllocationKeys": siteQuery("/allocation-keys", perms(tenancy.PermUnitRead)),
		"tenancy.createAllocationKey": {perms: perms(tenancy.PermSiteManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/allocation-keys"), fmt.Sprintf(`{"site_id":%q,"code":"lift_key","name":"Lift","kind":"table"}`, w.NewSite(t, org).Site))
		}},
		"tenancy.getAllocationKey":        get("/allocation-keys/{id}", perms(tenancy.PermUnitRead), keyID),
		"tenancy.updateAllocationKey":     send("PATCH", "/allocation-keys/{id}", perms(tenancy.PermSiteManage), 200, keyID, `{"name":"Renamed key"}`, ifm, one),
		"tenancy.deleteAllocationKey":     send("DELETE", "/allocation-keys/{id}", perms(tenancy.PermSiteManage), 204, customKey, "", ifm, one),
		"tenancy.listAllocationKeyValues": get("/allocation-keys/{id}/values", perms(tenancy.PermUnitRead), keyID),
		"tenancy.putAllocationKeyValues": {perms: perms(tenancy.PermSiteManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("PUT", orgPath(org, "/allocation-keys/"+f.Key.String()+"/values"), fmt.Sprintf(`{"items":[{"unit_id":%q,"value":1,"computed":false}]}`, f.Unit), ifm, one)
		}},
		"tenancy.listVehicles": siteQuery("/vehicles", perms(tenancy.PermVehicleRead)),
		"tenancy.createVehicle": {perms: perms(tenancy.PermVehicleManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/vehicles"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"plate":"34 abc 123"}`, f.Site, f.Unit))
		}},
		"tenancy.getVehicle":    get("/vehicles/{id}", perms(tenancy.PermVehicleRead), vehicleID),
		"tenancy.updateVehicle": send("PATCH", "/vehicles/{id}", perms(tenancy.PermVehicleManage), 200, vehicleID, `{"description":"red car"}`, ifm, one),
		"tenancy.deleteVehicle": send("DELETE", "/vehicles/{id}", perms(tenancy.PermVehicleManage), 204, vehicleID, "", ifm, one),
		"tenancy.exportVehicles": {perms: perms(tenancy.PermVehicleRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/vehicles:export?site_id="+w.NewSite(t, org).Site.String())}
		}},
		"tenancy.exportSite": {perms: perms(tenancy.PermSiteExport), status: 202, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/sites/"+w.NewSite(t, org).Site.String()+":export"), `{}`)
		}, override: noReauth},
		"tenancy.importSite": {perms: perms(tenancy.PermSiteExport), status: 202, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return multipartField(orgPath(org, "/sites:import"), "archive", nil, "site.json", minimalArchive(org))
		}, override: noReauth},
		"tenancy.offboardSite": {perms: perms(tenancy.PermSiteOffboard), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/sites/"+f.Site.String()+":offboard"), fmt.Sprintf(`{"confirm_site_name":%q}`, f.SiteName))
		}, override: noReauth},
		"tenancy.listSiteTransfers":           {perms: perms(tenancy.PermSiteExport), status: 200, build: mget("/v1/orgs/{org_id}/site-transfers")},
		"tenancy.getSiteTransfer":             get("/site-transfers/{id}", perms(tenancy.PermSiteExport), exportID),
		"tenancy.downloadSiteTransferArchive": get("/site-transfers/{id}/archive", perms(tenancy.PermSiteExport), exportID),

		// people
		"people.listPersons": {perms: perms(people.PermPersonRead), status: 200, build: mget("/v1/orgs/{org_id}/persons")},
		"people.createPerson": {perms: perms(people.PermPersonManage), status: 201,
			build: fixed("POST", "/v1/orgs/{org_id}/persons", `{"display_name":"Ayse Yilmaz","contact":{"phone":"0532 123 45 67"}}`)},
		"people.getPerson":       get("/persons/{id}", perms(people.PermPersonRead), personID),
		"people.updatePerson":    send("PATCH", "/persons/{id}", perms(people.PermPersonManage), 200, personID, `{"notes":"hello"}`, ifm, one),
		"people.getNationalId":   withOverride(get("/persons/{id}/national-id", perms(people.PermNationalID), personID), noReauthAny),
		"people.setNationalId":   send("PUT", "/persons/{id}/national-id", perms(people.PermNationalID), 200, personID, `{"country_code":"TR","value":"10000000146"}`),
		"people.clearNationalId": send("DELETE", "/persons/{id}/national-id", perms(people.PermNationalID), 204, personID, ""),
		"people.listOccupancies": {perms: perms(people.PermOccupancyRead), status: 200, build: mget("/v1/orgs/{org_id}/occupancies")},
		"people.createOccupancy": {perms: perms(people.PermOccupancyManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/occupancies"), fmt.Sprintf(`{"person_id":%q,"unit_id":%q,"kind":"owner","share_pct":50,"valid_from":"2026-01-01"}`, w.NewPerson(t, org), f.Unit))
		}},
		"people.getOccupancy":    get("/occupancies/{id}", perms(people.PermOccupancyRead), occID("owner")),
		"people.updateOccupancy": send("PATCH", "/occupancies/{id}", perms(people.PermOccupancyManage), 200, occID("owner"), `{"is_correspondent":true}`, ifm, one),
		"people.deleteOccupancy": send("DELETE", "/occupancies/{id}", perms(people.PermOccupancyManage), 204, occID("owner"), "", ifm, one),
		"people.setMoneyAccess":  send("PUT", "/occupancies/{id}/money-access", perms(people.PermMoneyAccessGrant), 200, occID("household"), `{"may_see_money":true}`),
		"people.moveOut": {perms: perms(people.PermMoveOutManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			o := w.NewOccupancy(t, f, f.Unit, w.NewPerson(t, org), "owner")
			return jsonReq("POST", orgPath(org, "/occupancies/"+o.String()+"/move-out"), `{"as_of":"2026-06-01"}`, idem()...)
		}},
		"people.listMoveOuts": {perms: perms(people.PermMoveOutManage), status: 200, build: mget("/v1/orgs/{org_id}/move-outs")},
		"people.getMoveOut": get("/move-outs/{id}", perms(people.PermMoveOutManage), func(w *authztest.World, org authztest.Org, t *testing.T) string {
			return w.NewMoveOut(t, w.NewSite(t, org)).String()
		}),
		"people.listMemberships": {perms: perms(people.PermMembershipRead), status: 200, build: mget("/v1/orgs/{org_id}/memberships")},
		"people.createMembership": {perms: perms(people.PermMembershipManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/memberships"), fmt.Sprintf(`{"person_id":%q,"role":"accountant","scope_type":"org","valid_from":"2026-01-01"}`, w.NewPerson(t, org)))
		}},
		"people.getMembership":    get("/memberships/{id}", perms(people.PermMembershipRead), membershipID),
		"people.deleteMembership": send("DELETE", "/memberships/{id}", perms(people.PermMembershipManage), 204, membershipID, "", ifm, one),
		"people.listInvitations":  {perms: perms(people.PermInvitationManage), status: 200, build: mget("/v1/orgs/{org_id}/invitations")},
		"people.createInvitation": {perms: perms(people.PermInvitationManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/invitations"), fmt.Sprintf(`{"site_id":%q,"person_id":%q,"channel":"sms"}`, f.Site, w.NewPerson(t, org)))
		}},
		"people.getInvitation":    get("/invitations/{id}", perms(people.PermInvitationManage), invitationID("manager")),
		"people.revokeInvitation": send("DELETE", "/invitations/{id}", perms(people.PermInvitationManage), 204, invitationID("manager"), ""),
		"people.resendInvitation": {perms: perms(people.PermInvitationManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/invitations/"+invitationID("manager")(w, org, t)+":resend"), "")
		}},
		"people.createHouseholdInvitation": {perms: perms(people.PermInvitationHouse), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/units/"+w.NewSite(t, org).Unit.String()+"/household-invitations"), `{"display_name":"Zeynep","channel":"sms","destination":"05329998877"}`)
		}},
		"me.listHouseholdMembers":     {perms: perms(people.PermInvitationHouse), status: 200, build: mget("/v1/orgs/{org_id}/me/household-members")},
		"me.listHouseholdInvitations": {perms: perms(people.PermInvitationHouse), status: 200, build: mget("/v1/orgs/{org_id}/me/household-invitations")},
		"me.revokeHouseholdInvitation": {perms: perms(people.PermInvitationHouse), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "DELETE", path: orgPath(org, "/me/household-invitations/"+invitationID("household")(w, org, t))}
		}, override: notTheirUnit},
		"me.endHouseholdOccupancy": {perms: perms(people.PermHouseholdEnd), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "DELETE", path: orgPath(org, "/me/household-occupancies/"+occID("household")(w, org, t))}
		}, override: notTheirUnit},
		"people.getJoinCode": {perms: perms(people.PermClaimManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			code, _ := tokenFor(org)
			w.NewJoinCode(t, f, code)
			return mreq{method: "GET", path: orgPath(org, "/sites/"+f.Site.String()+"/join-code")}
		}},
		"people.setJoinCodeEnabled": send("PUT", "/sites/{id}/join-code", perms(people.PermClaimManage), 200, siteID, `{"enabled":true}`),
		"people.rotateJoinCode": {perms: perms(people.PermClaimManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/sites/"+w.NewSite(t, org).Site.String()+"/join-code:rotate"), "")
		}},
		"people.listUnitClaims": {perms: perms(people.PermClaimManage), status: 200, build: mget("/v1/orgs/{org_id}/unit-claims")},
		"people.getUnitClaim":   get("/unit-claims/{id}", perms(people.PermClaimManage), claimID),
		"people.approveUnitClaim": {perms: perms(people.PermClaimManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/unit-claims/"+claimID(w, org, t)+":approve"), `{}`)
		}},
		"people.rejectUnitClaim": {perms: perms(people.PermClaimManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/unit-claims/"+claimID(w, org, t)+":reject"), `{"reason":"no"}`)
		}},
		"people.listDirectory": siteQuery("/directory", perms(people.PermDirectoryRead)),
		"people.listVendors":   {perms: perms(people.PermVendorRead), status: 200, build: mget("/v1/orgs/{org_id}/vendors")},
		"people.createVendor":  {perms: perms(people.PermVendorManage), status: 201, build: fixed("POST", "/v1/orgs/{org_id}/vendors", `{"name":"Lift Co"}`)},
		"people.getVendor":     get("/vendors/{id}", perms(people.PermVendorRead), vendorID),
		"people.updateVendor":  send("PATCH", "/vendors/{id}", perms(people.PermVendorManage), 200, vendorID, `{"name":"Renamed"}`, ifm, one),
		"people.deleteVendor":  send("DELETE", "/vendors/{id}", perms(people.PermVendorManage), 204, vendorID, ""),
		"people.listRoles":     {perms: perms(people.PermRolesRead), status: 200, build: mget("/v1/orgs/{org_id}/roles")},

		// the caller's own person and units
		"people.getMyPerson": {perms: perms(people.PermMeManage), status: 200, build: mget("/v1/orgs/{org_id}/me/person")},
		"people.updateMyPerson": {perms: perms(people.PermMeManage), status: 200, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/me/person"), `{"display_name":"Me"}`, ifm, one)
		}},
		"people.listMyOccupancies": {perms: perms(people.PermMeManage), status: 200, build: mget("/v1/orgs/{org_id}/me/occupancies")},
		"people.setMyDirectoryConsent": {perms: perms(people.PermMeManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PUT", orgPath(org, "/me/directory-consent"), fmt.Sprintf(`{"site_id":%q,"consent":true,"show_phone":false}`, w.NewSite(t, org).Site))
		}},
	}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}

// minimalArchive is a site archive with only the site row, under a new id.
func minimalArchive(org authztest.Org) string {
	sid := id.New()
	row := fmt.Sprintf(`{"id":%q,"org_id":%q,"name":"Imported","address":{},"timezone":"Europe/Istanbul","currency":"TRY","fiscal_year_start_month":1,"lock_date":null,`+
		`"settings":{},"bank_details":{},"status":"active","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","version":1}`, sid, org.ID)
	tables := []string{"block", "unit", "allocation_key", "allocation_key_value", "unit_tag", "vehicle", "facility", "facility_block", "person", "occupancy", "move_out", "directory_consent", "vendor", "unit_claim", "membership"}
	var b strings.Builder
	fmt.Fprintf(&b, `{"format":"openbekci.site-archive","version":1,"exported_at":"2026-01-01T00:00:00Z","source_org_id":%q,"site_id":%q,"site_name":"Imported","tables":{"site":[%s]`, uuid.New(), sid, row)
	for _, tb := range tables {
		fmt.Fprintf(&b, `,%q:[]`, tb)
	}
	b.WriteString("}}")
	return b.String()
}
