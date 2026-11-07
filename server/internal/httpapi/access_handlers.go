package httpapi

import (
	"context"
	"errors"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoAccess = errors.New("access service not configured")

func (s *apiServer) ac() (*access.Service, error) {
	if s.access == nil {
		return nil, problem.Internal(errNoAccess)
	}
	return s.access, nil
}

func cursorOf(c *gen.Cursor) string {
	if c == nil {
		return ""
	}
	return string(*c)
}

func limitOf(l *gen.Limit) int {
	if l == nil {
		return 0
	}
	return int(*l)
}

func strOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---- access points ----------------------------------------------------------

func (s *apiServer) AccessListAccessPoints(ctx context.Context, req gen.AccessListAccessPointsRequestObject) (gen.AccessListAccessPointsResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	items, next, err := a.ListPoints(ctx, req.OrgId, req.Params.SiteId, req.Params.AgentId, req.Params.Enabled, limitOf(req.Params.Limit), cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.AccessPointList](items, next)
	return gen.AccessListAccessPoints200JSONResponse(out), err
}

func (s *apiServer) AccessCreateAccessPoint(ctx context.Context, req gen.AccessCreateAccessPointRequestObject) (gen.AccessCreateAccessPointResponseObject, error) {
	a, err := s.ac()
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
	p, err := a.CreatePoint(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AccessPoint](p)
	return gen.AccessCreateAccessPoint201JSONResponse(out), err
}

func (s *apiServer) AccessGetAccessPoint(ctx context.Context, req gen.AccessGetAccessPointRequestObject) (gen.AccessGetAccessPointResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	p, err := a.GetPoint(ctx, req.OrgId, req.PointId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AccessPoint](p)
	return gen.AccessGetAccessPoint200JSONResponse(out), err
}

func (s *apiServer) AccessUpdateAccessPoint(ctx context.Context, req gen.AccessUpdateAccessPointRequestObject) (gen.AccessUpdateAccessPointResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(&req.Params.IfMatch)
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
	p, err := a.UpdatePoint(ctx, req.OrgId, req.PointId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AccessPoint](p)
	return gen.AccessUpdateAccessPoint200JSONResponse(out), err
}

func (s *apiServer) AccessDeleteAccessPoint(ctx context.Context, req gen.AccessDeleteAccessPointRequestObject) (gen.AccessDeleteAccessPointResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.DeletePoint(ctx, req.OrgId, req.PointId, v); err != nil {
		return nil, err
	}
	return gen.AccessDeleteAccessPoint204Response{}, nil
}

func (s *apiServer) AccessOpenAccessPoint(ctx context.Context, req gen.AccessOpenAccessPointRequestObject) (gen.AccessOpenAccessPointResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	var body access.OpenRequest
	if req.Body != nil {
		body.CredentialID = req.Body.CredentialId
		body.VisitorEntryID = req.Body.VisitorEntryId
	}
	guard := ""
	if req.Params.XGuardSession != nil {
		guard = string(*req.Params.XGuardSession)
	}
	c, err := a.OpenPoint(ctx, req.OrgId, req.PointId, body, guard)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AgentCommand](c)
	return gen.AccessOpenAccessPoint202JSONResponse(out), err
}

func (s *apiServer) AccessGetAgentCommand(ctx context.Context, req gen.AccessGetAgentCommandRequestObject) (gen.AccessGetAgentCommandResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	c, err := a.GetCommand(ctx, req.OrgId, req.CommandId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AgentCommand](c)
	return gen.AccessGetAgentCommand200JSONResponse(out), err
}

// ---- agents -----------------------------------------------------------------

func (s *apiServer) AccessListAgents(ctx context.Context, req gen.AccessListAgentsRequestObject) (gen.AccessListAgentsResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	items, next, err := a.ListAgents(ctx, req.OrgId, req.Params.SiteId, strOf(req.Params.Status), limitOf(req.Params.Limit), cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.AgentList](items, next)
	return gen.AccessListAgents200JSONResponse(out), err
}

func (s *apiServer) AccessCreateAgent(ctx context.Context, req gen.AccessCreateAgentRequestObject) (gen.AccessCreateAgentResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	e, err := a.CreateAgent(ctx, req.OrgId, req.Body.SiteId, req.Body.Name)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AgentEnrolment](e)
	return gen.AccessCreateAgent201JSONResponse(out), err
}

func (s *apiServer) AccessGetAgent(ctx context.Context, req gen.AccessGetAgentRequestObject) (gen.AccessGetAgentResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	ag, err := a.GetAgent(ctx, req.OrgId, req.AgentId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Agent](ag)
	return gen.AccessGetAgent200JSONResponse(out), err
}

func (s *apiServer) AccessUpdateAgent(ctx context.Context, req gen.AccessUpdateAgentRequestObject) (gen.AccessUpdateAgentResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(&req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	ag, err := a.UpdateAgent(ctx, req.OrgId, req.AgentId, v, req.Body.Name)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Agent](ag)
	return gen.AccessUpdateAgent200JSONResponse(out), err
}

func (s *apiServer) AccessReenrolAgent(ctx context.Context, req gen.AccessReenrolAgentRequestObject) (gen.AccessReenrolAgentResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	e, err := a.ReenrolAgent(ctx, req.OrgId, req.AgentId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AgentEnrolment](e)
	return gen.AccessReenrolAgent200JSONResponse(out), err
}

func (s *apiServer) AccessRevokeAgent(ctx context.Context, req gen.AccessRevokeAgentRequestObject) (gen.AccessRevokeAgentResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	ag, err := a.RevokeAgent(ctx, req.OrgId, req.AgentId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Agent](ag)
	return gen.AccessRevokeAgent200JSONResponse(out), err
}

func (s *apiServer) AccessRotateAgentToken(ctx context.Context, req gen.AccessRotateAgentTokenRequestObject) (gen.AccessRotateAgentTokenResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	ag, err := a.ForceRotation(ctx, req.OrgId, req.AgentId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Agent](ag)
	return gen.AccessRotateAgentToken200JSONResponse(out), err
}

// ---- credentials and events ---------------------------------------------------

func (s *apiServer) AccessListCredentials(ctx context.Context, req gen.AccessListCredentialsRequestObject) (gen.AccessListCredentialsResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := access.CredentialFilter{Kind: strOf(p.Kind), Status: strOf(p.Status), SourceType: strOf(p.SourceType), Query: strOf(p.Q), UnitID: p.UnitId, PointID: p.PointId}
	items, next, err := a.ListCredentials(ctx, req.OrgId, p.SiteId, f, limitOf(p.Limit), cursorOf(p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.CredentialList](items, next)
	return gen.AccessListCredentials200JSONResponse(out), err
}

func (s *apiServer) AccessGetCredential(ctx context.Context, req gen.AccessGetCredentialRequestObject) (gen.AccessGetCredentialResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	c, err := a.GetCredential(ctx, req.OrgId, req.CredentialId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Credential](c)
	return gen.AccessGetCredential200JSONResponse(out), err
}

func (s *apiServer) AccessRevokeCredential(ctx context.Context, req gen.AccessRevokeCredentialRequestObject) (gen.AccessRevokeCredentialResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	if err := a.RevokeCredential(ctx, req.OrgId, req.CredentialId); err != nil {
		return nil, err
	}
	return gen.AccessRevokeCredential204Response{}, nil
}

func (s *apiServer) AccessListAccessEvents(ctx context.Context, req gen.AccessListAccessEventsRequestObject) (gen.AccessListAccessEventsResponseObject, error) {
	a, err := s.ac()
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := access.EventFilter{PointID: p.PointId, Decision: strOf(p.Decision), Reason: strOf(p.Reason), From: p.From, To: p.To, Plate: strOf(p.Plate), After: p.After}
	items, next, err := a.ListEvents(ctx, req.OrgId, p.SiteId, f, limitOf(p.Limit), cursorOf(p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.AccessEventList](items, next)
	return gen.AccessListAccessEvents200JSONResponse(out), err
}
