package httpapi

import (
	"context"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
)

func meSites(in []identity.MeSite) []gen.MeSite {
	out, err := conv[[]gen.MeSite](in)
	if err != nil || out == nil {
		return []gen.MeSite{}
	}
	return out
}

func meOccupancies(in []identity.MeOccupancy) []gen.MeOccupancy {
	out, err := conv[[]gen.MeOccupancy](in)
	if err != nil || out == nil {
		return []gen.MeOccupancy{}
	}
	return out
}

func (s *apiServer) PeopleListHouseholdInvitations(ctx context.Context, req gen.PeopleListHouseholdInvitationsRequestObject) (gen.PeopleListHouseholdInvitationsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	items, err := p.ListHouseholdInvitations(ctx, req.OrgId, req.UnitId)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.InvitationList](items, "")
	return gen.PeopleListHouseholdInvitations200JSONResponse(out), err
}

func (s *apiServer) PeopleRevokeHouseholdInvitation(ctx context.Context, req gen.PeopleRevokeHouseholdInvitationRequestObject) (gen.PeopleRevokeHouseholdInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := p.RevokeHouseholdInvitation(ctx, req.OrgId, req.UnitId, req.InvitationId); err != nil {
		return nil, err
	}
	return gen.PeopleRevokeHouseholdInvitation204Response{}, nil
}

func (s *apiServer) PeopleEndHouseholdOccupancy(ctx context.Context, req gen.PeopleEndHouseholdOccupancyRequestObject) (gen.PeopleEndHouseholdOccupancyResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := p.EndHouseholdOccupancy(ctx, req.OrgId, req.UnitId, req.OccupancyId); err != nil {
		return nil, err
	}
	return gen.PeopleEndHouseholdOccupancy204Response{}, nil
}

func (s *apiServer) PeopleListPersons(ctx context.Context, req gen.PeopleListPersonsRequestObject) (gen.PeopleListPersonsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	q := ""
	if req.Params.Q != nil {
		q = *req.Params.Q
	}
	items, next, err := p.ListPersons(ctx, req.OrgId, q, req.Params.SiteId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.PersonList](items, next)
	return gen.PeopleListPersons200JSONResponse(out), err
}

func (s *apiServer) PeopleCreatePerson(ctx context.Context, req gen.PeopleCreatePersonRequestObject) (gen.PeopleCreatePersonResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.CreatePerson(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Person](v)
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
	out, err := conv[gen.Person](v)
	return gen.PeopleGetPerson200JSONResponse{Body: out, Headers: gen.PeopleGetPerson200ResponseHeaders{ETag: ptr(etag(v.Version))}}, err
}

func (s *apiServer) PeopleUpdatePerson(ctx context.Context, req gen.PeopleUpdatePersonRequestObject) (gen.PeopleUpdatePersonResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.UpdatePerson(ctx, req.OrgId, req.PersonId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Person](v)
	return gen.PeopleUpdatePerson200JSONResponse{Body: out, Headers: gen.PeopleUpdatePerson200ResponseHeaders{ETag: ptr(etag(v.Version))}}, err
}

func (s *apiServer) PeopleListOccupancies(ctx context.Context, req gen.PeopleListOccupanciesRequestObject) (gen.PeopleListOccupanciesResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	f := people.OccFilter{SiteID: req.Params.SiteId, UnitID: req.Params.UnitId, PersonID: req.Params.PersonId}
	if req.Params.Current != nil {
		f.CurrentOnly = *req.Params.Current
	}
	items, next, err := p.ListOccupancies(ctx, req.OrgId, f, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.OccupancyList](items, next)
	return gen.PeopleListOccupancies200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateOccupancy(ctx context.Context, req gen.PeopleCreateOccupancyRequestObject) (gen.PeopleCreateOccupancyResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.CreateOccupancy(ctx, req.OrgId, req.UnitId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Occupancy](v)
	return gen.PeopleCreateOccupancy201JSONResponse(out), err
}

func (s *apiServer) PeopleUpdateOccupancy(ctx context.Context, req gen.PeopleUpdateOccupancyRequestObject) (gen.PeopleUpdateOccupancyResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.UpdateOccupancy(ctx, req.OrgId, req.OccupancyId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Occupancy](v)
	return gen.PeopleUpdateOccupancy200JSONResponse{Body: out, Headers: gen.PeopleUpdateOccupancy200ResponseHeaders{ETag: ptr(etag(v.Version))}}, err
}

func (s *apiServer) PeopleSetMoneyAccess(ctx context.Context, req gen.PeopleSetMoneyAccessRequestObject) (gen.PeopleSetMoneyAccessResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	v, err := p.SetMoneyAccess(ctx, req.OrgId, req.OccupancyId, ver, req.Body.MaySeeMoney)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Occupancy](v)
	return gen.PeopleSetMoneyAccess200JSONResponse(out), err
}

func (s *apiServer) PeopleMoveOut(ctx context.Context, req gen.PeopleMoveOutRequestObject) (gen.PeopleMoveOutResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	in, err := people.ParseMoveOut(d)
	if err != nil {
		return nil, err
	}
	v, err := p.MoveOutOccupancy(ctx, req.OrgId, req.OccupancyId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.MoveOut](v)
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
	out, err := conv[gen.MoveOut](v)
	return gen.PeopleGetMoveOut200JSONResponse(out), err
}

func (s *apiServer) PeopleListMemberships(ctx context.Context, req gen.PeopleListMembershipsRequestObject) (gen.PeopleListMembershipsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	items, next, err := p.ListMemberships(ctx, req.OrgId, req.Params.PersonId, req.Params.SiteId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.MembershipList](items, next)
	return gen.PeopleListMemberships200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateMembership(ctx context.Context, req gen.PeopleCreateMembershipRequestObject) (gen.PeopleCreateMembershipResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.CreateMembership(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Membership](v)
	return gen.PeopleCreateMembership201JSONResponse(out), err
}

func (s *apiServer) PeopleEndMembership(ctx context.Context, req gen.PeopleEndMembershipRequestObject) (gen.PeopleEndMembershipResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := p.EndMembership(ctx, req.OrgId, req.MembershipId); err != nil {
		return nil, err
	}
	return gen.PeopleEndMembership204Response{}, nil
}

func (s *apiServer) PeopleListInvitations(ctx context.Context, req gen.PeopleListInvitationsRequestObject) (gen.PeopleListInvitationsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	status := ""
	if req.Params.Status != nil {
		status = *req.Params.Status
	}
	items, next, err := p.ListInvitations(ctx, req.OrgId, req.Params.SiteId, status, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.InvitationList](items, next)
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
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.CreateInvitation(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Invitation](v)
	return gen.PeopleCreateInvitation201JSONResponse(out), err
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
	out, err := conv[gen.Invitation](v)
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
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.CreateHouseholdInvitation(ctx, req.OrgId, req.UnitId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Invitation](v)
	return gen.PeopleCreateHouseholdInvitation201JSONResponse(out), err
}

func (s *apiServer) PeopleGetJoinCode(ctx context.Context, req gen.PeopleGetJoinCodeRequestObject) (gen.PeopleGetJoinCodeResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.GetJoinCode(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.JoinCode](v)
	return gen.PeopleGetJoinCode200JSONResponse(out), err
}

func (s *apiServer) PeopleRotateJoinCode(ctx context.Context, req gen.PeopleRotateJoinCodeRequestObject) (gen.PeopleRotateJoinCodeResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.RotateJoinCode(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.JoinCode](v)
	return gen.PeopleRotateJoinCode201JSONResponse(out), err
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
	out, err := conv[gen.InvitationPreview](v)
	return gen.PeoplePreviewInvitation200JSONResponse(out), err
}

func (s *apiServer) PeopleAcceptInvitation(ctx context.Context, req gen.PeopleAcceptInvitationRequestObject) (gen.PeopleAcceptInvitationResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	var in people.AcceptInput
	if req.Body != nil {
		if req.Body.Password != nil {
			in.Password = *req.Body.Password
		}
		if req.Body.Locale != nil {
			in.Locale = *req.Body.Locale
		}
	}
	v, err := p.AcceptInvitation(ctx, req.Token, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AcceptResult](v)
	return gen.PeopleAcceptInvitation200JSONResponse(out), err
}

func (s *apiServer) PeoplePreviewJoin(ctx context.Context, req gen.PeoplePreviewJoinRequestObject) (gen.PeoplePreviewJoinResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	v, err := p.PreviewJoin(ctx, req.Code)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.JoinPreview](v)
	return gen.PeoplePreviewJoin200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateClaim(ctx context.Context, req gen.PeopleCreateClaimRequestObject) (gen.PeopleCreateClaimResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.CreateClaim(ctx, req.Code, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.UnitClaim](v)
	return gen.PeopleCreateClaim201JSONResponse(out), err
}

func (s *apiServer) PeopleListClaims(ctx context.Context, req gen.PeopleListClaimsRequestObject) (gen.PeopleListClaimsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	status := ""
	if req.Params.Status != nil {
		status = *req.Params.Status
	}
	items, next, err := p.ListClaims(ctx, req.OrgId, req.Params.SiteId, status, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.UnitClaimList](items, next)
	return gen.PeopleListClaims200JSONResponse(out), err
}

func (s *apiServer) PeopleApproveClaim(ctx context.Context, req gen.PeopleApproveClaimRequestObject) (gen.PeopleApproveClaimResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.ApproveClaim(ctx, req.OrgId, req.ClaimId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.UnitClaim](v)
	return gen.PeopleApproveClaim200JSONResponse(out), err
}

func (s *apiServer) PeopleRejectClaim(ctx context.Context, req gen.PeopleRejectClaimRequestObject) (gen.PeopleRejectClaimResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.RejectClaim(ctx, req.OrgId, req.ClaimId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.UnitClaim](v)
	return gen.PeopleRejectClaim200JSONResponse(out), err
}

func (s *apiServer) PeopleListDirectory(ctx context.Context, req gen.PeopleListDirectoryRequestObject) (gen.PeopleListDirectoryResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	q := ""
	if req.Params.Q != nil {
		q = *req.Params.Q
	}
	items, err := p.ListDirectory(ctx, req.OrgId, req.SiteId, q)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.DirectoryList](items, "")
	return gen.PeopleListDirectory200JSONResponse(out), err
}

func (s *apiServer) PeopleSetDirectoryConsent(ctx context.Context, req gen.PeopleSetDirectoryConsentRequestObject) (gen.PeopleSetDirectoryConsentResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.SetDirectoryConsent(ctx, req.OrgId, req.SiteId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.DirectoryConsent](v)
	return gen.PeopleSetDirectoryConsent200JSONResponse(out), err
}

func (s *apiServer) PeopleListVendors(ctx context.Context, req gen.PeopleListVendorsRequestObject) (gen.PeopleListVendorsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	items, next, err := p.ListVendors(ctx, req.OrgId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.VendorList](items, next)
	return gen.PeopleListVendors200JSONResponse(out), err
}

func (s *apiServer) PeopleCreateVendor(ctx context.Context, req gen.PeopleCreateVendorRequestObject) (gen.PeopleCreateVendorResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.CreateVendor(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vendor](v)
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
	out, err := conv[gen.Vendor](v)
	return gen.PeopleGetVendor200JSONResponse{Body: out, Headers: gen.PeopleGetVendor200ResponseHeaders{ETag: ptr(etag(v.Version))}}, err
}

func (s *apiServer) PeopleUpdateVendor(ctx context.Context, req gen.PeopleUpdateVendorRequestObject) (gen.PeopleUpdateVendorResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := p.UpdateVendor(ctx, req.OrgId, req.VendorId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vendor](v)
	return gen.PeopleUpdateVendor200JSONResponse{Body: out, Headers: gen.PeopleUpdateVendor200ResponseHeaders{ETag: ptr(etag(v.Version))}}, err
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
