package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func meSites(in []identity.MeSite) []gen.MeSite {
	out, err := conv[[]gen.MeSite](in)
	if err != nil || out == nil {
		return []gen.MeSite{}
	}
	return out
}

// meOccupancies maps the identity module's occupancies to the draft's MeOccupancy
// (a former occupancy is kind former_occupant; a household member has no side).
func meOccupancies(in []identity.MeOccupancy) []gen.MeOccupancy {
	raw, err := conv[[]map[string]any](in)
	if err != nil {
		return []gen.MeOccupancy{}
	}
	views := make([]map[string]any, 0, len(raw))
	for _, o := range raw {
		kind, _ := o["kind"].(string)
		if cur, ok := o["current"].(bool); ok && !cur {
			kind = "former_occupant"
		}
		side := o["side"]
		if kind == "household" {
			side = nil
		}
		views = append(views, map[string]any{
			"id": o["id"], "org_id": o["org_id"], "site_id": o["site_id"], "unit_id": o["unit_id"], "unit_label": o["unit_label"],
			"kind": kind, "side": side, "may_see_money": o["may_see_money"], "valid_from": o["valid_from"], "valid_to": o["valid_to"],
		})
	}
	out, err := conv[[]gen.MeOccupancy](views)
	if err != nil || out == nil {
		return []gen.MeOccupancy{}
	}
	return out
}

// dateToStamp turns a YYYY-MM-DD date into the timestamp the membership service reads.
func dateToStamp(d tenancy.Doc, key string) {
	var s string
	if raw, ok := d[key]; ok && json.Unmarshal(raw, &s) == nil && len(s) == 10 {
		d[key] = mustJSON(s + "T00:00:00Z")
	}
}

func canSeeNationalID(ctx context.Context, org uuid.UUID) bool {
	return authz.GrantsFromContext(ctx).HasInOrg(people.PermNationalID, org)
}

func (s *apiServer) personOut(ctx context.Context, org uuid.UUID, p people.Person) map[string]any {
	return personView(org, p, nil, "", canSeeNationalID(ctx, org))
}

// ---- persons ----

func (s *apiServer) PeopleListPersons(ctx context.Context, req gen.PeopleListPersonsRequestObject) (gen.PeopleListPersonsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	items, next, err := p.ListPersons(ctx, req.OrgId, strParam(req.Params.Q), req.Params.SiteId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	var onUnit map[uuid.UUID]bool
	if req.Params.UnitId != nil {
		occs, _, err := p.ListOccupancies(ctx, req.OrgId, people.OccFilter{UnitID: req.Params.UnitId}, tenancy.PageParams{Limit: 200})
		if err != nil {
			return nil, err
		}
		onUnit = map[uuid.UUID]bool{}
		for _, o := range occs {
			onUnit[o.PersonID] = true
		}
	}
	js := strParam(req.Params.JoinStatus)
	// role and occupancy_kind narrow the page to the people the other registers name
	var withRole, withKind map[uuid.UUID]bool
	if role := strParam(req.Params.Role); role != "" {
		ms, _, err := p.ListMemberships(ctx, req.OrgId, nil, req.Params.SiteId, tenancy.PageParams{Limit: 200})
		if err != nil {
			return nil, err
		}
		withRole = map[uuid.UUID]bool{}
		for _, m := range ms {
			if m.Role == role && m.Current {
				withRole[m.PersonID] = true
			}
		}
	}
	if kind := strParam(req.Params.OccupancyKind); kind != "" {
		occs, _, err := p.ListOccupancies(ctx, req.OrgId, people.OccFilter{SiteID: req.Params.SiteId, CurrentOnly: true}, tenancy.PageParams{Limit: 200})
		if err != nil {
			return nil, err
		}
		withKind = map[uuid.UUID]bool{}
		for _, o := range occs {
			if o.Kind == kind {
				withKind[o.PersonID] = true
			}
		}
	}
	views := make([]map[string]any, 0, len(items))
	for _, it := range items {
		v := s.personOut(ctx, req.OrgId, it)
		if (onUnit != nil && !onUnit[it.ID]) || (js != "" && v["join_status"] != js) ||
			(withRole != nil && !withRole[it.ID]) || (withKind != nil && !withKind[it.ID]) {
			continue
		}
		views = append(views, v)
	}
	out, err := listOf[gen.PersonList](views, next)
	return gen.PeopleListPersons200JSONResponse(out), err
}

// onlySite is the one site a site-scoped caller manages (so that a person created without
// a site is registered there); empty for organization-wide holders.
func onlySite(ctx context.Context, perm authz.Permission) *uuid.UUID {
	orgWide, sites, _ := authz.GrantsFromContext(ctx).ScopeIDs(perm)
	if !orgWide && len(sites) == 1 {
		return &sites[0]
	}
	return nil
}

func (s *apiServer) PeopleCreatePerson(ctx context.Context, req gen.PeopleCreatePersonRequestObject) (gen.PeopleCreatePersonResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	body, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	d, err := personDoc(body)
	if err != nil {
		return nil, err
	}
	if site := onlySite(ctx, people.PermPersonManage); site != nil {
		d["site_id"] = mustJSON(*site)
	}
	v, err := p.CreatePerson(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Person](s.personOut(ctx, req.OrgId, v))
	return gen.PeopleCreatePerson201JSONResponse(out), err
}

func (s *apiServer) PeopleGetPerson(ctx context.Context, req gen.PeopleGetPersonRequestObject) (gen.PeopleGetPersonResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetPerson(ctx, req.OrgId, req.PersonId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Person](s.personOut(ctx, req.OrgId, v))
	return gen.PeopleGetPerson200JSONResponse(out), err
}

func (s *apiServer) PeopleUpdatePerson(ctx context.Context, req gen.PeopleUpdatePersonRequestObject) (gen.PeopleUpdatePersonResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	body, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	d, err := personDoc(body)
	if err != nil {
		return nil, err
	}
	v, err := p.UpdatePerson(ctx, req.OrgId, req.PersonId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Person](s.personOut(ctx, req.OrgId, v))
	return gen.PeopleUpdatePerson200JSONResponse(out), err
}

func nationalIDView(v people.Person, value any) map[string]any {
	return map[string]any{"set": v.NationalID != nil, "country_code": nilIfEmpty(v.NationalIDCountry), "masked": v.NationalID, "value": value}
}

func (s *apiServer) PeopleGetNationalId(ctx context.Context, req gen.PeopleGetNationalIdRequestObject) (gen.PeopleGetNationalIdResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := authz.RequireInOrg(ctx, people.PermNationalID, req.OrgId); err != nil {
		return nil, err
	}
	full, err := p.RevealNationalID(ctx, req.OrgId, req.PersonId)
	if err != nil {
		return nil, err
	}
	v, err := p.GetPerson(ctx, req.OrgId, req.PersonId)
	if err != nil {
		return nil, err
	}
	var value any
	if full != "" {
		value = full
	}
	out, err := conv[gen.NationalId](nationalIDView(v, value))
	return gen.PeopleGetNationalId200JSONResponse(out), err
}

func (s *apiServer) PeopleSetNationalId(ctx context.Context, req gen.PeopleSetNationalIdRequestObject) (gen.PeopleSetNationalIdResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := authz.RequireInOrg(ctx, people.PermNationalID, req.OrgId); err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	cur, err := p.GetPerson(ctx, req.OrgId, req.PersonId)
	if err != nil {
		return nil, err
	}
	v, err := p.UpdatePerson(ctx, req.OrgId, req.PersonId, cur.Version,
		tenancy.Doc{"national_id": mustJSON(req.Body.Value), "national_id_country": mustJSON(strings.ToUpper(req.Body.CountryCode))})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.NationalId](nationalIDView(v, nil))
	return gen.PeopleSetNationalId200JSONResponse(out), err
}

func (s *apiServer) PeopleClearNationalId(ctx context.Context, req gen.PeopleClearNationalIdRequestObject) (gen.PeopleClearNationalIdResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := authz.RequireInOrg(ctx, people.PermNationalID, req.OrgId); err != nil {
		return nil, err
	}
	cur, err := p.GetPerson(ctx, req.OrgId, req.PersonId)
	if err != nil {
		return nil, err
	}
	if _, err := p.UpdatePerson(ctx, req.OrgId, req.PersonId, cur.Version, tenancy.Doc{"national_id": json.RawMessage("null")}); err != nil {
		return nil, err
	}
	return gen.PeopleClearNationalId204Response{}, nil
}

// ---- occupancies ----

func occupancyDoc(body tenancy.Doc) tenancy.Doc {
	delete(body, "unit_id")
	delete(body, "may_see_money")
	return body
}

func (s *apiServer) PeopleListOccupancies(ctx context.Context, req gen.PeopleListOccupanciesRequestObject) (gen.PeopleListOccupanciesResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	f := people.OccFilter{UnitID: req.Params.UnitId, PersonID: req.Params.PersonId}
	if req.Params.Active != nil {
		f.CurrentOnly = *req.Params.Active
	}
	items, next, err := p.ListOccupancies(ctx, req.OrgId, f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	kind, side, asOf := strParam(req.Params.Kind), strParam(req.Params.Side), ""
	if req.Params.AsOf != nil {
		asOf = req.Params.AsOf.String()
	}
	views := make([]map[string]any, 0, len(items))
	for _, o := range items {
		if (kind != "" && o.Kind != kind) || (side != "" && o.Side != side) {
			continue
		}
		if asOf != "" && (o.ValidFrom > asOf || (o.ValidTo != nil && *o.ValidTo <= asOf)) {
			continue
		}
		views = append(views, occupancyView(req.OrgId, o))
	}
	out, err := listOf[gen.OccupancyList](views, next)
	return gen.PeopleListOccupancies200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateOccupancy(ctx context.Context, req gen.PeopleCreateOccupancyRequestObject) (gen.PeopleCreateOccupancyResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	mayMoney := req.Body.MaySeeMoney != nil && *req.Body.MaySeeMoney
	v, err := p.CreateOccupancy(ctx, req.OrgId, req.Body.UnitId, occupancyDoc(d))
	if err != nil {
		return nil, err
	}
	if mayMoney {
		if v, err = p.SetMoneyAccess(ctx, req.OrgId, v.ID, v.Version, true); err != nil {
			return nil, err
		}
	}
	out, err := conv[gen.Occupancy](occupancyView(req.OrgId, v))
	return gen.PeopleCreateOccupancy201JSONResponse(out), err
}

func (s *apiServer) PeopleGetOccupancy(ctx context.Context, req gen.PeopleGetOccupancyRequestObject) (gen.PeopleGetOccupancyResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetOccupancy(ctx, req.OrgId, req.OccupancyId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Occupancy](occupancyView(req.OrgId, v))
	return gen.PeopleGetOccupancy200JSONResponse(out), err
}

func (s *apiServer) PeopleUpdateOccupancy(ctx context.Context, req gen.PeopleUpdateOccupancyRequestObject) (gen.PeopleUpdateOccupancyResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	cur, err := p.GetOccupancy(ctx, req.OrgId, req.OccupancyId)
	if err != nil {
		return nil, err
	}
	// the person, unit, kind and side of an occupancy are fixed: a change is a move-out and a new occupancy
	var bad []problem.Field
	for _, k := range []struct {
		name string
		same func(raw json.RawMessage) bool
	}{
		{"person_id", func(r json.RawMessage) bool {
			var v uuid.UUID
			return json.Unmarshal(r, &v) == nil && v == cur.PersonID
		}},
		{"unit_id", func(r json.RawMessage) bool { var v uuid.UUID; return json.Unmarshal(r, &v) == nil && v == cur.UnitID }},
		{"kind", func(r json.RawMessage) bool { var v string; return json.Unmarshal(r, &v) == nil && v == cur.Kind }},
		{"side", func(r json.RawMessage) bool { var v string; return json.Unmarshal(r, &v) == nil && v == cur.Side }},
	} {
		if raw, ok := d[k.name]; ok {
			if !k.same(raw) {
				bad = append(bad, problem.Field{Field: k.name, Code: "invalid_value"})
			}
			delete(d, k.name)
		}
	}
	if len(bad) > 0 {
		return nil, problem.Validation(bad...)
	}
	var money *bool
	if raw, ok := d["may_see_money"]; ok {
		var b bool
		_ = json.Unmarshal(raw, &b)
		money = &b
		delete(d, "may_see_money")
	}
	v := cur
	if len(d) > 0 {
		if v, err = p.UpdateOccupancy(ctx, req.OrgId, req.OccupancyId, ver, d); err != nil {
			return nil, err
		}
		ver = v.Version
	}
	if money != nil && *money != v.MayMoney {
		if v, err = p.SetMoneyAccess(ctx, req.OrgId, req.OccupancyId, ver, *money); err != nil {
			return nil, err
		}
	}
	out, err := conv[gen.Occupancy](occupancyView(req.OrgId, v))
	return gen.PeopleUpdateOccupancy200JSONResponse(out), err
}

func (s *apiServer) PeopleDeleteOccupancy(ctx context.Context, req gen.PeopleDeleteOccupancyRequestObject) (gen.PeopleDeleteOccupancyResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := p.DeleteOccupancy(ctx, req.OrgId, req.OccupancyId, ver); err != nil {
		return nil, err
	}
	return gen.PeopleDeleteOccupancy204Response{}, nil
}

func (s *apiServer) PeopleSetMoneyAccess(ctx context.Context, req gen.PeopleSetMoneyAccessRequestObject) (gen.PeopleSetMoneyAccessResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	cur, err := p.GetOccupancy(ctx, req.OrgId, req.OccupancyId)
	if err != nil {
		return nil, err
	}
	v, err := p.SetMoneyAccess(ctx, req.OrgId, req.OccupancyId, cur.Version, req.Body.MaySeeMoney)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Occupancy](occupancyView(req.OrgId, v))
	return gen.PeopleSetMoneyAccess200JSONResponse(out), err
}

func (s *apiServer) siteCurrency(ctx context.Context, org, site uuid.UUID) string {
	if s.tenancy != nil {
		if st, err := s.tenancy.GetSite(ctx, org, site); err == nil {
			return st.Currency
		}
	}
	return "TRY"
}

func (s *apiServer) PeopleMoveOut(ctx context.Context, req gen.PeopleMoveOutRequestObject) (gen.PeopleMoveOutResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	b := req.Body
	if b.TransferredToPersonId != nil {
		// carrying the debt to another person needs the money module (phase C)
		return nil, problem.Validation(problem.Field{Field: "transferred_to_person_id", Code: "invalid_value"})
	}
	in := people.MoveOutInput{AsOf: b.AsOf.Time, OpenCharges: "carry_to_person", EndHousehold: true}
	if b.OpenCharges != nil {
		in.OpenCharges = string(*b.OpenCharges)
	}
	if b.DispositionReason != nil {
		in.Notes = b.DispositionReason
	}
	if (in.OpenCharges == "write_off" || in.OpenCharges == "keep_on_unit") && (in.Notes == nil || strings.TrimSpace(*in.Notes) == "") {
		return nil, problem.Validation(problem.Field{Field: "disposition_reason", Code: "required"})
	}
	v, err := p.MoveOutOccupancy(ctx, req.OrgId, req.OccupancyId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MoveOut](moveOutView(req.OrgId, v, s.siteCurrency(ctx, req.OrgId, v.SiteID)))
	return gen.PeopleMoveOut201JSONResponse(out), err
}

func (s *apiServer) PeopleGetMoveOut(ctx context.Context, req gen.PeopleGetMoveOutRequestObject) (gen.PeopleGetMoveOutResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetMoveOut(ctx, req.OrgId, req.MoveOutId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MoveOut](moveOutView(req.OrgId, v, s.siteCurrency(ctx, req.OrgId, v.SiteID)))
	return gen.PeopleGetMoveOut200JSONResponse(out), err
}

func (s *apiServer) PeopleListMoveOuts(ctx context.Context, req gen.PeopleListMoveOutsRequestObject) (gen.PeopleListMoveOutsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	f := people.MoveOutFilter{SiteID: req.Params.SiteId, UnitID: req.Params.UnitId, PersonID: req.Params.PersonId}
	if req.Params.From != nil {
		f.From = req.Params.From.String()
	}
	if req.Params.To != nil {
		f.To = req.Params.To.String()
	}
	items, next, err := p.ListMoveOuts(ctx, req.OrgId, f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	cur := map[uuid.UUID]string{}
	views := make([]map[string]any, 0, len(items))
	for _, m := range items {
		c, ok := cur[m.SiteID]
		if !ok {
			c = s.siteCurrency(ctx, req.OrgId, m.SiteID)
			cur[m.SiteID] = c
		}
		views = append(views, moveOutView(req.OrgId, m, c))
	}
	out, err := listOf[gen.MoveOutList](views, next)
	return gen.PeopleListMoveOuts200JSONResponse(out), err
}

// ---- memberships ----

func (s *apiServer) PeopleListMemberships(ctx context.Context, req gen.PeopleListMembershipsRequestObject) (gen.PeopleListMembershipsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	var siteID *uuid.UUID
	if req.Params.ScopeId != nil && strParam(req.Params.ScopeType) == "site" {
		siteID = req.Params.ScopeId
	}
	items, next, err := p.ListMemberships(ctx, req.OrgId, req.Params.PersonId, siteID, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	role, st := strParam(req.Params.Role), strParam(req.Params.ScopeType)
	views := make([]map[string]any, 0, len(items))
	for _, m := range items {
		if (role != "" && m.Role != role) || (st != "" && m.ScopeType != st) {
			continue
		}
		if req.Params.Active != nil && *req.Params.Active != m.Current {
			continue
		}
		if req.Params.AsOf != nil {
			at := req.Params.AsOf.Time
			if m.ValidFrom.After(at.AddDate(0, 0, 1)) || (m.ValidTo != nil && !m.ValidTo.After(at)) {
				continue
			}
		}
		views = append(views, membershipView(req.OrgId, m))
	}
	out, err := listOf[gen.MembershipList](views, next)
	return gen.PeopleListMemberships200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateMembership(ctx context.Context, req gen.PeopleCreateMembershipRequestObject) (gen.PeopleCreateMembershipResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body, "site_owned")
	if err != nil {
		return nil, err
	}
	dateToStamp(d, "valid_from")
	dateToStamp(d, "valid_to")
	v, err := p.CreateMembership(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Membership](membershipView(req.OrgId, v))
	return gen.PeopleCreateMembership201JSONResponse(out), err
}

func (s *apiServer) PeopleGetMembership(ctx context.Context, req gen.PeopleGetMembershipRequestObject) (gen.PeopleGetMembershipResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetMembership(ctx, req.OrgId, req.MembershipId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Membership](membershipView(req.OrgId, v))
	return gen.PeopleGetMembership200JSONResponse(out), err
}

func (s *apiServer) PeopleDeleteMembership(ctx context.Context, req gen.PeopleDeleteMembershipRequestObject) (gen.PeopleDeleteMembershipResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if _, err := ifMatchOf(req.Params.IfMatch); err != nil {
		return nil, err
	}
	if err := p.EndMembership(ctx, req.OrgId, req.MembershipId); err != nil {
		return nil, err
	}
	return gen.PeopleDeleteMembership204Response{}, nil
}

// ---- invitations ----

func (s *apiServer) PeopleListInvitations(ctx context.Context, req gen.PeopleListInvitationsRequestObject) (gen.PeopleListInvitationsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	items, next, err := p.ListInvitations(ctx, req.OrgId, req.Params.SiteId, strParam(req.Params.Status), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	kind := strParam(req.Params.Kind)
	views := make([]map[string]any, 0, len(items))
	for _, i := range items {
		if (kind != "" && i.Kind != kind) || (req.Params.PersonId != nil && (i.PersonID == nil || *i.PersonID != *req.Params.PersonId)) ||
			(req.Params.UnitId != nil && (i.UnitID == nil || *i.UnitID != *req.Params.UnitId)) {
			continue
		}
		views = append(views, invitationView(req.OrgId, i, nil))
	}
	out, err := listOf[gen.InvitationList](views, next)
	return gen.PeopleListInvitations200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateInvitation(ctx context.Context, req gen.PeopleCreateInvitationRequestObject) (gen.PeopleCreateInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	b := req.Body
	d := tenancy.Doc{"site_id": mustJSON(b.SiteId), "person_id": mustJSON(b.PersonId), "channel": mustJSON(string(b.Channel))}
	if b.UnitId != nil {
		d["unit_id"] = mustJSON(*b.UnitId)
	}
	if b.Destination != nil {
		if b.Channel == "email" {
			d["email"] = mustJSON(*b.Destination)
		} else {
			d["phone"] = mustJSON(*b.Destination)
		}
	}
	v, err := p.CreateInvitation(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Invitation](invitationView(req.OrgId, v, nil))
	return gen.PeopleCreateInvitation201JSONResponse(out), err
}

func (s *apiServer) PeopleGetInvitation(ctx context.Context, req gen.PeopleGetInvitationRequestObject) (gen.PeopleGetInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetInvitation(ctx, req.OrgId, req.InvitationId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Invitation](invitationView(req.OrgId, v, nil))
	return gen.PeopleGetInvitation200JSONResponse(out), err
}

func (s *apiServer) PeopleRevokeInvitation(ctx context.Context, req gen.PeopleRevokeInvitationRequestObject) (gen.PeopleRevokeInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := p.RevokeInvitation(ctx, req.OrgId, req.InvitationId); err != nil {
		return nil, err
	}
	return gen.PeopleRevokeInvitation204Response{}, nil
}

func (s *apiServer) PeopleResendInvitation(ctx context.Context, req gen.PeopleResendInvitationRequestObject) (gen.PeopleResendInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.ResendInvitation(ctx, req.OrgId, req.InvitationId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Invitation](invitationView(req.OrgId, v, nil))
	return gen.PeopleResendInvitation200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateHouseholdInvitation(ctx context.Context, req gen.PeopleCreateHouseholdInvitationRequestObject) (gen.PeopleCreateHouseholdInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	b := req.Body
	d := tenancy.Doc{"display_name": mustJSON(b.DisplayName), "channel": mustJSON(string(b.Channel))}
	if b.Channel == "email" {
		d["email"] = mustJSON(b.Destination)
	} else {
		d["phone"] = mustJSON(b.Destination)
	}
	v, err := p.CreateHouseholdInvitation(ctx, req.OrgId, req.UnitId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Invitation](invitationView(req.OrgId, v, nil))
	return gen.PeopleCreateHouseholdInvitation201JSONResponse(out), err
}

func (s *apiServer) PeoplePreviewInvitation(ctx context.Context, req gen.PeoplePreviewInvitationRequestObject) (gen.PeoplePreviewInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.PreviewInvitation(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	ch := v.Channel
	if ch != "sms" && ch != "email" {
		ch = "sms"
	}
	exp := any(time.Now().Add(24 * time.Hour))
	if v.ExpiresAt != nil {
		exp = *v.ExpiresAt
	}
	var invited any
	if v.InvitedName != "" {
		invited = v.InvitedName
	}
	out, err := conv[gen.InvitationPreview](map[string]any{
		"kind": v.Kind, "org_name": v.OrgName, "site_name": v.SiteName, "unit_door_number": v.DoorNumber, "person_display_name": invited,
		"channel": ch, "expires_at": exp, "existing_account": false,
	})
	return gen.PeoplePreviewInvitation200JSONResponse(out), err
}

func (s *apiServer) PeopleAcceptInvitation(ctx context.Context, req gen.PeopleAcceptInvitationRequestObject) (gen.PeopleAcceptInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	var in people.AcceptInput
	mobile := false
	if req.Body != nil {
		in.Password = strParam(req.Body.Password)
		in.Locale = strParam(req.Body.Locale)
		mobile = req.Body.Client != nil && string(*req.Body.Client) == "mobile"
	}
	res, err := p.AcceptInvitation(ctx, req.Token, in)
	if err != nil {
		return nil, err
	}
	// the link proves the contact, as a magic link does: the caller is signed in as the linked account
	u, err := svc.GetUser(ctx, res.UserID)
	if err != nil {
		return nil, err
	}
	m := metaFrom(ctx, nil)
	if mobile {
		tp, err := svc.IssueTokens(ctx, u.ID, m)
		if err != nil {
			return nil, err
		}
		return gen.PeopleAcceptInvitation200JSONResponse(gen.AuthResult{
			Client: gen.AuthResultClient("mobile"), SessionId: tp.SessionID, ExpiresAt: tp.AccessExpiresAt, User: authUserDTO(u),
			AccessToken: &tp.AccessToken, RefreshToken: &tp.RefreshToken, RefreshExpiresAt: &tp.RefreshExpiresAt,
		}), nil
	}
	ws, err := svc.IssueWebSession(ctx, u.ID, m)
	if err != nil {
		return nil, err
	}
	http.SetCookie(infoFrom(ctx).w, authCookie(ws.Token, ws.ExpiresAt, s.insecureCookies))
	return gen.PeopleAcceptInvitation200JSONResponse(gen.AuthResult{
		Client: gen.AuthResultClient("web"), SessionId: ws.SessionID, ExpiresAt: ws.ExpiresAt, User: authUserDTO(u),
	}), nil
}

// ---- join code and claims ----

func joinCodeView(j people.JoinCodeState) map[string]any {
	return map[string]any{"code": j.Code, "url": j.URL, "enabled": j.Enabled, "rotated_at": j.CreatedAt}
}

func (s *apiServer) PeopleGetJoinCode(ctx context.Context, req gen.PeopleGetJoinCodeRequestObject) (gen.PeopleGetJoinCodeResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetJoinCodeState(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.JoinCode](joinCodeView(v))
	return gen.PeopleGetJoinCode200JSONResponse(out), err
}

func (s *apiServer) PeopleSetJoinCodeEnabled(ctx context.Context, req gen.PeopleSetJoinCodeEnabledRequestObject) (gen.PeopleSetJoinCodeEnabledResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := p.SetJoinCodeEnabled(ctx, req.OrgId, req.SiteId, req.Body.Enabled)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.JoinCode](joinCodeView(v))
	return gen.PeopleSetJoinCodeEnabled200JSONResponse(out), err
}

func (s *apiServer) PeopleRotateJoinCode(ctx context.Context, req gen.PeopleRotateJoinCodeRequestObject) (gen.PeopleRotateJoinCodeResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.RotatedJoinCode(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.JoinCode](joinCodeView(v))
	return gen.PeopleRotateJoinCode200JSONResponse(out), err
}

func (s *apiServer) PeopleGetJoinInfo(ctx context.Context, req gen.PeopleGetJoinInfoRequestObject) (gen.PeopleGetJoinInfoResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.PreviewJoinUnits(ctx, req.Code)
	if err != nil {
		return nil, err
	}
	blocks := []map[string]any{}
	seen := map[string]bool{}
	for _, u := range v.Units {
		if !seen[u.BlockName] {
			seen[u.BlockName] = true
			blocks = append(blocks, map[string]any{"id": uuid.NewSHA1(uuid.NameSpaceOID, []byte(u.BlockName)), "name": u.BlockName})
		}
	}
	out, err := conv[gen.JoinInfo](map[string]any{"site_name": v.SiteName, "org_name": v.OrgName, "blocks": blocks})
	return gen.PeopleGetJoinInfo200JSONResponse(out), err
}

func (s *apiServer) PeopleListJoinUnits(ctx context.Context, req gen.PeopleListJoinUnitsRequestObject) (gen.PeopleListJoinUnitsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.PreviewJoinUnits(ctx, req.Code)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strParam(req.Params.Q))
	var items []people.JoinUnit
	for _, u := range v.Units {
		if q == "" || strings.Contains(strings.ToLower(u.DoorNumber), q) {
			items = append(items, u)
		}
	}
	page, next, err := pagedSlice(items, func(u people.JoinUnit) uuid.UUID { return u.ID }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, u := range page {
		views = append(views, map[string]any{"id": u.ID, "door_number": u.DoorNumber, "block_name": u.BlockName})
	}
	out, err := listOf[gen.JoinUnitList](views, next)
	return gen.PeopleListJoinUnits200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateUnitClaim(ctx context.Context, req gen.PeopleCreateUnitClaimRequestObject) (gen.PeopleCreateUnitClaimResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d := tenancy.Doc{"unit_id": mustJSON(req.Body.UnitId), "kind": mustJSON(string(req.Body.ClaimedRole))}
	if req.Body.Note != nil {
		d["message"] = mustJSON(*req.Body.Note)
	}
	v, err := p.CreateClaim(ctx, req.Code, d)
	if err != nil {
		return nil, err
	}
	uid := &authz.PrincipalFromContext(ctx).UserID
	out, err := conv[gen.UnitClaim](claimView(claimOrg(req.Code), v, uid))
	return gen.PeopleCreateUnitClaim201JSONResponse(out), err
}

// claimOrg is the organization a join code belongs to (codes are "<org>.<random>").
func claimOrg(code string) uuid.UUID {
	prefix, _, _ := strings.Cut(code, ".")
	b, err := base64.RawURLEncoding.DecodeString(prefix)
	if err != nil || len(b) != 16 {
		return uuid.Nil
	}
	return uuid.UUID(b)
}

func (s *apiServer) PeopleListUnitClaims(ctx context.Context, req gen.PeopleListUnitClaimsRequestObject) (gen.PeopleListUnitClaimsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	items, next, err := p.ListClaims(ctx, req.OrgId, req.Params.SiteId, strParam(req.Params.Status), pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(items))
	for _, c := range items {
		if req.Params.UnitId != nil && c.UnitID != *req.Params.UnitId {
			continue
		}
		views = append(views, claimView(req.OrgId, c, nil))
	}
	out, err := listOf[gen.UnitClaimList](views, next)
	return gen.PeopleListUnitClaims200JSONResponse(out), err
}

func (s *apiServer) PeopleGetUnitClaim(ctx context.Context, req gen.PeopleGetUnitClaimRequestObject) (gen.PeopleGetUnitClaimResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetClaim(ctx, req.OrgId, req.ClaimId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.UnitClaim](claimView(req.OrgId, v, nil))
	return gen.PeopleGetUnitClaim200JSONResponse(out), err
}

func (s *apiServer) PeopleApproveUnitClaim(ctx context.Context, req gen.PeopleApproveUnitClaimRequestObject) (gen.PeopleApproveUnitClaimResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	if raw, ok := d["note"]; ok {
		d["decision_note"] = raw
		delete(d, "note")
	}
	v, err := p.ApproveClaim(ctx, req.OrgId, req.ClaimId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.UnitClaim](claimView(req.OrgId, v, nil))
	return gen.PeopleApproveUnitClaim200JSONResponse(out), err
}

func (s *apiServer) PeopleRejectUnitClaim(ctx context.Context, req gen.PeopleRejectUnitClaimRequestObject) (gen.PeopleRejectUnitClaimResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := p.RejectClaim(ctx, req.OrgId, req.ClaimId, tenancy.Doc{"note": mustJSON(req.Body.Reason)})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.UnitClaim](claimView(req.OrgId, v, nil))
	return gen.PeopleRejectUnitClaim200JSONResponse(out), err
}

// ---- directory, vendors, roles, codes ----

func (s *apiServer) PeopleListDirectory(ctx context.Context, req gen.PeopleListDirectoryRequestObject) (gen.PeopleListDirectoryResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	var rows []people.DirectoryEntry
	if req.Params.SiteId != nil {
		if rows, err = p.ListDirectory(ctx, req.OrgId, *req.Params.SiteId, strParam(req.Params.Q)); err != nil {
			return nil, err
		}
	} else {
		t, err := s.tn()
		if err != nil {
			return nil, err
		}
		sites, _, err := t.ListSites(ctx, req.OrgId, tenancy.PageParams{Limit: 200})
		if err != nil {
			return nil, err
		}
		for _, st := range sites {
			if st.Status != "active" {
				continue
			}
			part, err := p.ListDirectory(ctx, req.OrgId, st.ID, strParam(req.Params.Q))
			if err != nil {
				if isForbidden(err) {
					continue
				}
				return nil, err
			}
			rows = append(rows, part...)
		}
	}
	type entry struct {
		id    uuid.UUID
		name  string
		phone *string
		units []map[string]any
	}
	var order []uuid.UUID
	byPerson := map[uuid.UUID]*entry{}
	for _, r := range rows {
		if req.Params.UnitId != nil && r.UnitID != *req.Params.UnitId {
			continue
		}
		e, ok := byPerson[r.PersonID]
		if !ok {
			e = &entry{id: r.PersonID, name: r.DisplayName, phone: r.Phone}
			byPerson[r.PersonID] = e
			order = append(order, r.PersonID)
		}
		e.units = append(e.units, map[string]any{"unit_id": r.UnitID, "door_number": r.DoorNumber, "kind": r.Kind})
	}
	all := make([]*entry, 0, len(order))
	for _, id := range order {
		all = append(all, byPerson[id])
	}
	sort.SliceStable(all, func(i, j int) bool { return string(all[i].id[:]) < string(all[j].id[:]) })
	page, next, err := pagedSlice(all, func(e *entry) uuid.UUID { return e.id }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, e := range page {
		views = append(views, map[string]any{"person_id": e.id, "display_name": e.name, "units": e.units, "phone": e.phone, "email": nil})
	}
	out, err := listOf[gen.DirectoryEntryList](views, next)
	return gen.PeopleListDirectory200JSONResponse(out), err
}

func vendorDoc(body tenancy.Doc) (tenancy.Doc, error) {
	d := tenancy.Doc{}
	for k, v := range body {
		switch k {
		case "archived":
			var b bool
			if json.Unmarshal(v, &b) == nil && b {
				return nil, problem.Validation(problem.Field{Field: "archived", Code: "invalid_value"}) // delete the vendor instead
			}
		case "contact":
			var c map[string]json.RawMessage
			if err := json.Unmarshal(v, &c); err != nil {
				return nil, problem.Validation(problem.Field{Field: "contact", Code: "invalid_type"})
			}
			for ck, cv := range c {
				if ck == "email" || ck == "phone" {
					d[ck] = cv
				}
			}
		default:
			d[k] = v
		}
	}
	return d, nil
}

func (s *apiServer) PeopleListVendors(ctx context.Context, req gen.PeopleListVendorsRequestObject) (gen.PeopleListVendorsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	var items []people.Vendor
	var next string
	if req.Params.Archived != nil && *req.Params.Archived {
		items = []people.Vendor{} // vendors are never archived yet
	} else if items, next, err = p.ListVendors(ctx, req.OrgId, pageOf(req.Params.Limit, req.Params.Cursor)); err != nil {
		return nil, err
	}
	q := strings.ToLower(strParam(req.Params.Q))
	views := make([]map[string]any, 0, len(items))
	for _, v := range items {
		if q == "" || strings.Contains(strings.ToLower(v.Name), q) {
			views = append(views, vendorView(req.OrgId, v))
		}
	}
	out, err := listOf[gen.VendorList](views, next)
	return gen.PeopleListVendors200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateVendor(ctx context.Context, req gen.PeopleCreateVendorRequestObject) (gen.PeopleCreateVendorResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	body, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	d, err := vendorDoc(body)
	if err != nil {
		return nil, err
	}
	v, err := p.CreateVendor(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vendor](vendorView(req.OrgId, v))
	return gen.PeopleCreateVendor201JSONResponse(out), err
}

func (s *apiServer) PeopleGetVendor(ctx context.Context, req gen.PeopleGetVendorRequestObject) (gen.PeopleGetVendorResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetVendor(ctx, req.OrgId, req.VendorId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vendor](vendorView(req.OrgId, v))
	return gen.PeopleGetVendor200JSONResponse(out), err
}

func (s *apiServer) PeopleUpdateVendor(ctx context.Context, req gen.PeopleUpdateVendorRequestObject) (gen.PeopleUpdateVendorResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	body, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	d, err := vendorDoc(body)
	if err != nil {
		return nil, err
	}
	v, err := p.UpdateVendor(ctx, req.OrgId, req.VendorId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vendor](vendorView(req.OrgId, v))
	return gen.PeopleUpdateVendor200JSONResponse(out), err
}

func (s *apiServer) PeopleDeleteVendor(ctx context.Context, req gen.PeopleDeleteVendorRequestObject) (gen.PeopleDeleteVendorResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := p.DeleteVendor(ctx, req.OrgId, req.VendorId); err != nil {
		return nil, err
	}
	return gen.PeopleDeleteVendor204Response{}, nil
}

func (s *apiServer) PeopleListRoles(ctx context.Context, req gen.PeopleListRolesRequestObject) (gen.PeopleListRolesResponseObject, error) {
	if s.resolver == nil {
		return nil, problem.Internal(errors.New("authorization is not configured"))
	}
	if err := authz.RequireInOrg(ctx, people.PermRolesRead, req.OrgId); err != nil {
		return nil, err
	}
	_, labels := codes.Labels("en")
	reg := s.resolver.Registry()
	roles := make([]map[string]any, 0, 7)
	for _, r := range authz.BuiltinRoles() {
		ps := []string{}
		for _, p := range reg.RolePermissions(r) {
			ps = append(ps, string(p))
		}
		roles = append(roles, map[string]any{"role": string(r), "description": labels["roles"][string(r)], "permissions": ps})
	}
	out, err := conv[gen.RoleMatrix](map[string]any{"roles": roles})
	return gen.PeopleListRoles200JSONResponse(out), err
}

func (s *apiServer) CodesListCodes(ctx context.Context, req gen.CodesListCodesRequestObject) (gen.CodesListCodesResponseObject, error) {
	if err := authz.RequireInOrg(ctx, people.PermCodesRead, req.OrgId); err != nil {
		return nil, err
	}
	locale, sections := codes.Labels(strParam(req.Params.Locale))
	enums := map[string][]map[string]any{}
	errs := []map[string]any{}
	for name, m := range sections {
		list := make([]map[string]any, 0, len(m))
		for code, label := range m {
			list = append(list, map[string]any{"code": code, "label": label})
		}
		sort.Slice(list, func(i, j int) bool { return list[i]["code"].(string) < list[j]["code"].(string) })
		if name == "errors" || name == "problem_codes" {
			errs = list
			continue
		}
		enums[name] = list
	}
	out, err := conv[gen.Codes](map[string]any{"locale": locale, "enums": enums, "errors": errs, "ticket_statuses": []any{}})
	return gen.CodesListCodes200JSONResponse(out), err
}

// ---- me ----

func (s *apiServer) PeopleGetMyPerson(ctx context.Context, req gen.PeopleGetMyPersonRequestObject) (gen.PeopleGetMyPersonResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.MyPerson(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Person](personView(req.OrgId, v, nil, "", true))
	return gen.PeopleGetMyPerson200JSONResponse(out), err
}

func (s *apiServer) PeopleUpdateMyPerson(ctx context.Context, req gen.PeopleUpdateMyPersonRequestObject) (gen.PeopleUpdateMyPersonResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	body, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	d, err := personDoc(body)
	if err != nil {
		return nil, err
	}
	v, err := p.UpdateMyPerson(ctx, req.OrgId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Person](personView(req.OrgId, v, nil, "", true))
	return gen.PeopleUpdateMyPerson200JSONResponse(out), err
}

func (s *apiServer) PeopleSetMyDirectoryConsent(ctx context.Context, req gen.PeopleSetMyDirectoryConsentRequestObject) (gen.PeopleSetMyDirectoryConsentResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	b := req.Body
	v, err := p.SetDirectoryConsent(ctx, req.OrgId, b.SiteId, tenancy.Doc{"share_name": mustJSON(b.Consent), "share_phone": mustJSON(b.ShowPhone)})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DirectoryConsent](map[string]any{"site_id": v.SiteID, "consent": v.ShareName, "show_phone": v.SharePhone})
	return gen.PeopleSetMyDirectoryConsent200JSONResponse(out), err
}

func (s *apiServer) PeopleListMyOccupancies(ctx context.Context, req gen.PeopleListMyOccupanciesRequestObject) (gen.PeopleListMyOccupanciesResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	items, err := p.MyOccupancies(ctx, req.OrgId, req.Params.Active != nil && *req.Params.Active)
	if err != nil {
		return nil, err
	}
	page, next, err := pagedSlice(items, func(o people.Occupancy) uuid.UUID { return o.ID }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, o := range page {
		views = append(views, occupancyView(req.OrgId, o))
	}
	out, err := listOf[gen.OccupancyList](views, next)
	return gen.PeopleListMyOccupancies200JSONResponse(out), err
}

// myHouseholdUnits are the units the caller owns or rents now: where they may manage a household.
func myHouseholdUnits(ctx context.Context, only *uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, o := range authz.GrantsFromContext(ctx).Current() {
		if o.Kind == authz.OccHousehold || seen[o.UnitID] || (only != nil && o.UnitID != *only) {
			continue
		}
		seen[o.UnitID] = true
		out = append(out, o.UnitID)
	}
	return out
}

func isForbidden(err error) bool {
	var pe *problem.Error
	return errors.As(err, &pe) && pe.Status == http.StatusForbidden
}

func isNotFound(err error) bool {
	var pe *problem.Error
	return errors.As(err, &pe) && pe.Status == http.StatusNotFound
}

func (s *apiServer) MeListHouseholdInvitations(ctx context.Context, req gen.MeListHouseholdInvitationsRequestObject) (gen.MeListHouseholdInvitationsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := authz.RequireInOrg(ctx, people.PermInvitationHouse, req.OrgId); err != nil {
		return nil, err
	}
	status := strParam(req.Params.Status)
	var all []people.Invitation
	for _, unit := range myHouseholdUnits(ctx, req.Params.UnitId) {
		items, err := p.ListHouseholdInvitations(ctx, req.OrgId, unit)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	sort.SliceStable(all, func(i, j int) bool { return string(all[i].ID[:]) < string(all[j].ID[:]) })
	page, next, err := pagedSlice(all, func(i people.Invitation) uuid.UUID { return i.ID }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, i := range page {
		if status == "" || i.Status == status {
			views = append(views, invitationView(req.OrgId, i, nil))
		}
	}
	out, err := listOf[gen.InvitationList](views, next)
	return gen.MeListHouseholdInvitations200JSONResponse(out), err
}

func (s *apiServer) MeRevokeHouseholdInvitation(ctx context.Context, req gen.MeRevokeHouseholdInvitationRequestObject) (gen.MeRevokeHouseholdInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := authz.RequireInOrg(ctx, people.PermInvitationHouse, req.OrgId); err != nil {
		return nil, err
	}
	for _, unit := range myHouseholdUnits(ctx, nil) {
		err := p.RevokeHouseholdInvitation(ctx, req.OrgId, unit, req.InvitationId)
		if err == nil {
			return gen.MeRevokeHouseholdInvitation204Response{}, nil
		}
		if !isNotFound(err) {
			return nil, err
		}
	}
	return nil, problem.NotFound("no such household invitation")
}

func (s *apiServer) MeEndHouseholdOccupancy(ctx context.Context, req gen.MeEndHouseholdOccupancyRequestObject) (gen.MeEndHouseholdOccupancyResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := authz.RequireInOrg(ctx, people.PermHouseholdEnd, req.OrgId); err != nil {
		return nil, err
	}
	for _, unit := range myHouseholdUnits(ctx, nil) {
		err := p.EndHouseholdOccupancy(ctx, req.OrgId, unit, req.OccupancyId)
		if err == nil {
			o, err := p.GetOccupancy(ctx, req.OrgId, req.OccupancyId)
			if err != nil {
				return nil, err
			}
			out, err := conv[gen.Occupancy](occupancyView(req.OrgId, o))
			return gen.MeEndHouseholdOccupancy200JSONResponse(out), err
		}
		if !isNotFound(err) {
			return nil, err
		}
	}
	return nil, problem.NotFound("no such household member")
}

func (s *apiServer) MeListHouseholdMembers(ctx context.Context, req gen.MeListHouseholdMembersRequestObject) (gen.MeListHouseholdMembersResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	items, err := p.MyHouseholdMembers(ctx, req.OrgId, req.Params.UnitId)
	if err != nil {
		return nil, err
	}
	page, next, err := pagedSlice(items, func(o people.Occupancy) uuid.UUID { return o.ID }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, o := range page {
		views = append(views, occupancyView(req.OrgId, o))
	}
	out, err := listOf[gen.OccupancyList](views, next)
	return gen.MeListHouseholdMembers200JSONResponse(out), err
}

// acrossSites runs list for every site of the organization the caller reads and
// merges the answers (each is ordered by id after the same cursor), for the
// lists whose site_id is optional.
func acrossSites[T any](ctx context.Context, t *tenancy.Service, org uuid.UUID, idOf func(T) uuid.UUID, limit int, list func(site uuid.UUID) ([]T, error)) ([]T, string, error) {
	sites, _, err := t.ListSites(ctx, org, tenancy.PageParams{Limit: 200})
	if err != nil {
		return nil, "", err
	}
	var all []T
	for _, st := range sites {
		if st.Status != "active" {
			continue
		}
		items, err := list(st.ID)
		if err != nil {
			var pe *problem.Error
			if errors.As(err, &pe) && pe.Status == http.StatusForbidden {
				continue // a site the caller may read but not this register of
			}
			return nil, "", err
		}
		all = append(all, items...)
	}
	sort.SliceStable(all, func(i, j int) bool { return idOf(all[i]).String() < idOf(all[j]).String() })
	if limit <= 0 {
		limit = tenancy.DefaultLimit
	}
	next := ""
	if len(all) > limit {
		all = all[:limit]
		next = tenancy.CursorOf(idOf(all[len(all)-1]))
	}
	return all, next, nil
}
