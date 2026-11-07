package httpapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
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
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	return mreq{method: "POST", path: path, body: b.String(), headers: map[string]string{"Content-Type": mw.FormDataContentType()}}
}

// tokenFor returns a well-formed invitation token of org and the hash stored for it.
func tokenFor(org authztest.Org) (string, []byte) {
	tok := base64.RawURLEncoding.EncodeToString(org.ID[:]) + "." + fmt.Sprintf("secret%d", bSeq.Add(1))
	return tok, crypto.HashToken(tok)
}

func init() {
	siteOp := func(method, suffix string, ps []authz.Permission, status int, body string) mrow {
		return mrow{perms: ps, status: status, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq(method, orgPath(org, "/sites/"+f.Site.String()+suffix), body)
		}}
	}
	ifm := "If-Match"
	rows := map[string]mrow{
		"instance.getCodes": {status: 200, build: mget("/v1/codes")},
		"people.previewInvitation": {status: 404, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return mreq{method: "GET", path: "/v1/invitations/not-a-token"}
		}},
		"people.acceptInvitation": {status: 404, build: func(_ *authztest.World, _ authztest.Org, _ *testing.T) mreq {
			return jsonReq("POST", "/v1/invitations/not-a-token/accept", `{}`)
		}, override: func(actor, _ string) (int, bool) {
			if actor == "anonymous" {
				return 404, true
			}
			return 0, false
		}},
		"people.previewJoin": {status: 404, build: func(_ *authztest.World, _ authztest.Org, _ *testing.T) mreq {
			return mreq{method: "GET", path: "/v1/join/not-a-code"}
		}, override: func(actor, _ string) (int, bool) {
			if actor == "anonymous" {
				return 404, true
			}
			return 0, false
		}},
		"people.createClaim": {status: 404, build: func(_ *authztest.World, _ authztest.Org, _ *testing.T) mreq {
			return jsonReq("POST", "/v1/join/not-a-code/claims", fmt.Sprintf(`{"unit_id":%q,"kind":"owner"}`, uuid.New()))
		}},

		"tenancy.listSites":  {perms: perms(tenancy.PermSiteRead), status: 200, build: mget("/v1/orgs/{org_id}/sites")},
		"tenancy.createSite": {perms: perms(tenancy.PermSiteCreate), status: 201, build: fixed("POST", "/v1/orgs/{org_id}/sites", `{"name":"Matrix site"}`)},
		"tenancy.getSite":    siteOp("GET", "", perms(tenancy.PermSiteRead), 200, ""),
		"tenancy.updateSite": {perms: perms(tenancy.PermSiteManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("PATCH", orgPath(org, "/sites/"+f.Site.String()), `{"name":"Renamed"}`, ifm, `"1"`)
		}},
		"tenancy.listBlocks":  siteOp("GET", "/blocks", perms(tenancy.PermSiteRead), 200, ""),
		"tenancy.createBlock": siteOp("POST", "/blocks", perms(tenancy.PermSiteManage), 201, `{"name":"Block X"}`),
		"tenancy.updateBlock": {perms: perms(tenancy.PermSiteManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("PATCH", orgPath(org, "/blocks/"+f.Block.String()), `{"name":"Renamed block"}`, ifm, `"1"`)
		}},
		"tenancy.listUnits":  siteOp("GET", "/units", perms(tenancy.PermUnitRead), 200, ""),
		"tenancy.createUnit": siteOp("POST", "/units", perms(tenancy.PermUnitManage), 201, `{"door_number":"Z-1"}`),
		"tenancy.getUnit": {perms: perms(tenancy.PermUnitRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/units/"+w.NewSite(t, org).Unit.String())}
		}},
		"tenancy.updateUnit": {perms: perms(tenancy.PermUnitManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/units/"+w.NewSite(t, org).Unit.String()), `{"area_m2":90.5,"payer_overrides":{"aidat":"tenant"}}`, ifm, `"1"`)
		}},
		"tenancy.setUnitTags": {perms: perms(tenancy.PermUnitManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PUT", orgPath(org, "/units/"+w.NewSite(t, org).Unit.String()+"/tags"), `{"tags":["lift"]}`, ifm, `"1"`)
		}},
		"tenancy.listAllocationKeys":  siteOp("GET", "/allocation-keys", perms(tenancy.PermAllocationRead), 200, ""),
		"tenancy.createAllocationKey": siteOp("POST", "/allocation-keys", perms(tenancy.PermAllocationManage), 201, `{"code":"lift_key","name":"Lift","kind":"table"}`),
		"tenancy.getAllocationKey": {perms: perms(tenancy.PermAllocationRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/allocation-keys/"+w.NewSite(t, org).Key.String())}
		}},
		"tenancy.updateAllocationKey": {perms: perms(tenancy.PermAllocationManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/allocation-keys/"+w.NewSite(t, org).Key.String()), `{"name":"Renamed key"}`, ifm, `"1"`)
		}},
		"tenancy.setAllocationKeyValues": {perms: perms(tenancy.PermAllocationManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("PUT", orgPath(org, "/allocation-keys/"+f.Key.String()+"/values"), fmt.Sprintf(`{"values":[{"unit_id":%q,"value":"1/1"}]}`, f.Unit), ifm, `"1"`)
		}},
		"tenancy.listVehicles": siteOp("GET", "/vehicles", perms(tenancy.PermVehicleRead), 200, ""),
		"tenancy.createVehicle": {perms: perms(tenancy.PermVehicleManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/units/"+w.NewSite(t, org).Unit.String()+"/vehicles"), `{"plate":"34 abc 123"}`)
		}},
		"tenancy.deleteVehicle": {perms: perms(tenancy.PermVehicleManage), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "DELETE", path: orgPath(org, "/vehicles/"+w.NewVehicle(t, w.NewSite(t, org)).String())}
		}},
		"tenancy.exportVehicles":  siteOp("GET", "/vehicles/export", perms(tenancy.PermVehicleRead), 200, ""),
		"tenancy.exportSite":      siteOp("POST", "/export", perms(tenancy.PermSiteExport), 201, ""),
		"tenancy.listSiteExports": {perms: perms(tenancy.PermSiteExport), status: 200, build: mget("/v1/orgs/{org_id}/site-exports")},
		"tenancy.downloadSiteExport": {perms: perms(tenancy.PermSiteExport), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/site-exports/"+w.NewExport(t, w.NewSite(t, org)).String()+"/archive")}
		}},
		"tenancy.importSiteArchive": {perms: perms(tenancy.PermSiteImport), status: 201, build: func(_ *authztest.World, org authztest.Org, _ *testing.T) mreq {
			return multipartReq(orgPath(org, "/site-imports"), nil, "site.json", minimalArchive(org))
		}},
		"tenancy.offboardSite": {perms: perms(tenancy.PermSiteOffboard), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/sites/"+w.NewSite(t, org).Site.String()+"/offboard"), "")
		}, override: func(actor, target string) (int, bool) {
			if actor == "org_admin" && target == "own" {
				return 403, true // needs a recent re-authentication
			}
			return 0, false
		}},

		"people.listPersons":  {perms: perms(people.PermPersonRead), status: 200, build: mget("/v1/orgs/{org_id}/persons")},
		"people.createPerson": {perms: perms(people.PermPersonManage), status: 201, build: fixed("POST", "/v1/orgs/{org_id}/persons", `{"display_name":"Ayse Yilmaz","phone":"0532 123 45 67","national_id":"10000000146"}`)},
		"people.getPerson": {perms: perms(people.PermPersonRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/persons/"+w.NewPerson(t, org).String())}
		}},
		"people.updatePerson": {perms: perms(people.PermPersonManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/persons/"+w.NewPerson(t, org).String()), `{"notes":"hello"}`, ifm, `"1"`)
		}},
		"people.listOccupancies": {perms: perms(people.PermOccupancyRead), status: 200, build: mget("/v1/orgs/{org_id}/occupancies")},
		"people.createOccupancy": {perms: perms(people.PermOccupancyManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/units/"+f.Unit.String()+"/occupancies"), fmt.Sprintf(`{"person_id":%q,"kind":"owner","share_pct":50}`, w.NewPerson(t, org)))
		}},
		"people.updateOccupancy": {perms: perms(people.PermOccupancyManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			o := w.NewOccupancy(t, f, f.Unit, w.NewPerson(t, org), "owner")
			return jsonReq("PATCH", orgPath(org, "/occupancies/"+o.String()), `{"is_correspondent":true}`, ifm, `"1"`)
		}},
		"people.setMoneyAccess": {perms: perms(people.PermMoneyAccessGrant), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			o := w.NewOccupancy(t, f, f.Unit, w.NewPerson(t, org), "household")
			return jsonReq("PUT", orgPath(org, "/occupancies/"+o.String()+"/money-access"), `{"may_see_money":true}`, ifm, `"1"`)
		}},
		"people.moveOut": {perms: perms(people.PermMoveOutManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			o := w.NewOccupancy(t, f, f.Unit, w.NewPerson(t, org), "owner")
			return jsonReq("POST", orgPath(org, "/occupancies/"+o.String()+"/move-out"), `{"as_of":"2026-06-01"}`)
		}},
		"people.getMoveOut": {perms: perms(people.PermMoveOutRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/move-outs/"+w.NewMoveOut(t, w.NewSite(t, org)).String())}
		}},
		"people.listMemberships": {perms: perms(people.PermMembershipRead), status: 200, build: mget("/v1/orgs/{org_id}/memberships")},
		"people.createMembership": {perms: perms(people.PermMembershipManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/memberships"), fmt.Sprintf(`{"person_id":%q,"role":"accountant"}`, w.NewPerson(t, org)))
		}},
		"people.endMembership": {perms: perms(people.PermMembershipManage), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			m := w.NewMembership(t, org, w.NewPerson(t, org), "staff")
			return mreq{method: "DELETE", path: orgPath(org, "/memberships/"+m.String())}
		}},
		"people.listInvitations": {perms: perms(people.PermInvitationManage), status: 200, build: mget("/v1/orgs/{org_id}/invitations")},
		"people.createInvitation": {perms: perms(people.PermInvitationManage), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			return jsonReq("POST", orgPath(org, "/invitations"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"display_name":"Mehmet","phone":"05321112233","occupancy_kind":"owner","channel":"link"}`, f.Site, f.Unit))
		}},
		"people.revokeInvitation": {perms: perms(people.PermInvitationManage), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			_, h := tokenFor(org)
			return mreq{method: "DELETE", path: orgPath(org, "/invitations/"+w.NewInvitation(t, w.NewSite(t, org), "manager", h).String())}
		}},
		"people.resendInvitation": {perms: perms(people.PermInvitationManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			_, h := tokenFor(org)
			return jsonReq("POST", orgPath(org, "/invitations/"+w.NewInvitation(t, w.NewSite(t, org), "manager", h).String()+"/resend"), "")
		}},
		"people.listHouseholdInvitations": {perms: perms(people.PermInvitationHouse), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/units/"+w.NewSite(t, org).Unit.String()+"/household-invitations")}
		}},
		"people.createHouseholdInvitation": {perms: perms(people.PermInvitationHouse), status: 201, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("POST", orgPath(org, "/units/"+w.NewSite(t, org).Unit.String()+"/household-invitations"), `{"display_name":"Zeynep","phone":"05329998877","channel":"link"}`)
		}},
		"people.revokeHouseholdInvitation": {perms: perms(people.PermInvitationHouse), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			_, h := tokenFor(org)
			i := w.NewInvitation(t, f, "household", h)
			return mreq{method: "DELETE", path: orgPath(org, "/units/"+f.Unit.String()+"/household-invitations/"+i.String())}
		}},
		"people.endHouseholdOccupancy": {perms: perms(people.PermInvitationHouse), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			o := w.NewOccupancy(t, f, f.Unit, w.NewPerson(t, org), "household")
			return mreq{method: "DELETE", path: orgPath(org, "/units/"+f.Unit.String()+"/household/"+o.String())}
		}},
		"people.getJoinCode": {perms: perms(people.PermInvitationManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			f := w.NewSite(t, org)
			code, _ := tokenFor(org)
			w.NewJoinCode(t, f, code)
			return mreq{method: "GET", path: orgPath(org, "/sites/"+f.Site.String()+"/join-code")}
		}},
		"people.rotateJoinCode": siteOp("POST", "/join-code", perms(people.PermInvitationManage), 201, ""),
		"people.listClaims":     {perms: perms(people.PermClaimManage), status: 200, build: mget("/v1/orgs/{org_id}/unit-claims")},
		"people.approveClaim": {perms: perms(people.PermClaimManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			c := w.NewClaim(t, w.NewSite(t, org), w.Actor("no_membership").UserID)
			return jsonReq("POST", orgPath(org, "/unit-claims/"+c.String()+"/approve"), `{}`)
		}},
		"people.rejectClaim": {perms: perms(people.PermClaimManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			c := w.NewClaim(t, w.NewSite(t, org), w.Actor("no_membership").UserID)
			return jsonReq("POST", orgPath(org, "/unit-claims/"+c.String()+"/reject"), `{"note":"no"}`)
		}},
		"people.listDirectory":       siteOp("GET", "/directory", perms(people.PermDirectoryRead), 200, ""),
		"people.setDirectoryConsent": siteOp("PUT", "/directory-consent", perms(people.PermDirectoryConsent), 200, `{"share_name":true,"share_phone":false}`),
		"people.listVendors":         {perms: perms(people.PermVendorRead), status: 200, build: mget("/v1/orgs/{org_id}/vendors")},
		"people.createVendor":        {perms: perms(people.PermVendorManage), status: 201, build: fixed("POST", "/v1/orgs/{org_id}/vendors", `{"name":"Lift Co"}`)},
		"people.getVendor": {perms: perms(people.PermVendorRead), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "GET", path: orgPath(org, "/vendors/"+w.NewVendor(t, w.NewSite(t, org)).String())}
		}},
		"people.updateVendor": {perms: perms(people.PermVendorManage), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return jsonReq("PATCH", orgPath(org, "/vendors/"+w.NewVendor(t, w.NewSite(t, org)).String()), `{"name":"Renamed"}`, ifm, `"1"`)
		}},
		"people.deleteVendor": {perms: perms(people.PermVendorManage), status: 204, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			return mreq{method: "DELETE", path: orgPath(org, "/vendors/"+w.NewVendor(t, w.NewSite(t, org)).String())}
		}},
	}
	rows["tenancy.importUnits"] = mrow{perms: perms(tenancy.PermImportRun), status: 200, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
		f := w.NewSite(t, org)
		return multipartReq(orgPath(org, "/sites/"+f.Site.String()+"/import"), map[string]string{"dry_run": "true"}, "units.csv", "door_number,area_m2\nM-1,80\n")
	}}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}

// minimalArchive is a site archive with only the site row, under a new id.
func minimalArchive(org authztest.Org) string {
	sid := id.New()
	row := fmt.Sprintf(`{"id":%q,"org_id":%q,"name":"Imported","address":{},"timezone":"Europe/Istanbul","currency":"TRY","fiscal_year_start_month":1,"lock_date":null,`+
		`"settings":{},"bank_details":{},"status":"active","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","version":1}`, sid, org.ID)
	tables := []string{"block", "unit", "allocation_key", "allocation_key_value", "unit_tag", "vehicle", "facility", "person", "occupancy", "move_out", "directory_consent", "vendor", "unit_claim", "membership"}
	var b strings.Builder
	fmt.Fprintf(&b, `{"format":"openbekci.site-archive","version":1,"exported_at":"2026-01-01T00:00:00Z","source_org_id":%q,"site_id":%q,"site_name":"Imported","tables":{"site":[%s]`, uuid.New(), sid, row)
	for _, tb := range tables {
		fmt.Fprintf(&b, `,%q:[]`, tb)
	}
	b.WriteString("}}")
	return b.String()
}
