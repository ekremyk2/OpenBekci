package httpapi

import (
	"context"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
)

// PeopleJoinSignUp starts a resident's sign-up through a site's join code. The
// join code is checked first (the same identical 404 as the join read for any
// code that is not usable); then the identity module sends the code or link,
// answering 202 for a new and an existing contact alike.
func (s *apiServer) PeopleJoinSignUp(ctx context.Context, req gen.PeopleJoinSignUpRequestObject) (gen.PeopleJoinSignUpResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	ref, err := p.CheckJoinCode(ctx, req.Code)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in := identity.SignUpInput{
		Code: req.Code, Phone: strParam(req.Body.Phone), Email: strParam(req.Body.Email), Name: strParam(req.Body.Name),
		Locale: strParam(req.Body.Locale), Client: identity.ClientWeb,
	}
	if req.Body.Client != nil && *req.Body.Client == gen.JoinSignUpRequestClientMobile {
		in.Client = identity.ClientMobile
	}
	if err := svc.RequestSignUp(ctx, in, metaFrom(ctx, nil)); err != nil {
		return nil, err
	}
	channel := "sms"
	if in.Phone == "" {
		channel = "email"
	}
	if err := p.AuditJoinSignUp(ctx, ref, channel); err != nil {
		return nil, err
	}
	return gen.PeopleJoinSignUp202Response{}, nil
}

// PeopleListMyUnitClaims lists the caller's own unit claims in every
// organization, with the site and unit labels.
func (s *apiServer) PeopleListMyUnitClaims(ctx context.Context, req gen.PeopleListMyUnitClaimsRequestObject) (gen.PeopleListMyUnitClaimsResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	pr, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAccount(ctx, identity.PermMeRead); err != nil {
		return nil, err
	}
	claims, err := svc.MyUnitClaims(ctx, pr.UserID, strParam(req.Params.Status))
	if err != nil {
		return nil, err
	}
	page, next, err := pagedSlice(claims, func(c identity.MyUnitClaim) uuid.UUID { return c.ID }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, c := range page {
		var decidedAt any
		if c.DecidedAt != nil {
			decidedAt = *c.DecidedAt
		}
		var decidedBy any
		if c.DecidedBy != nil {
			decidedBy = *c.DecidedBy
		}
		views = append(views, map[string]any{
			"id": c.ID, "org_id": c.OrgID, "user_id": pr.UserID, "display_name": c.PersonName, "unit_id": c.UnitID,
			"unit_door_number": c.DoorNumber, "claimed_role": c.Kind, "status": c.Status, "decided_by": decidedBy,
			"decided_at": decidedAt, "decision_note": c.DecisionNote, "note": c.Message, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
			"org_name": c.OrgName, "site_id": c.SiteID, "site_name": c.SiteName, "block_name": c.BlockName,
		})
	}
	out, err := listOf[gen.UnitClaimList](views, next)
	return gen.PeopleListMyUnitClaims200JSONResponse(out), err
}
