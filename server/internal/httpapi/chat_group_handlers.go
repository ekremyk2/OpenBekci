package httpapi

import (
	"context"
	"time"

	"github.com/oapi-codegen/nullable"

	chatcore "github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
)

func siteChatGroupDTO(g chatcore.Group) gen.SiteChatGroup {
	out := gen.SiteChatGroup{Status: gen.SiteChatGroupStatus(g.Status), Title: nullable.NewNullNullable[string](), Kind: nullable.NewNullNullable[gen.SiteChatGroupKind](),
		LinkedAt: nullable.NewNullNullable[time.Time](), CodeExpiresAt: nullable.NewNullNullable[time.Time](), BotUsername: nullStr(emptyToNil(g.BotUsername)), WhatsappInviteUrl: nullStr(g.WhatsAppInviteURL)}
	if g.Status == "linked" {
		out.Title, out.Kind = nullable.NewNullableWithValue(g.Title), nullable.NewNullableWithValue(gen.SiteChatGroupKind(g.Kind))
	}
	if g.LinkedAt != nil {
		out.LinkedAt = nullable.NewNullableWithValue(*g.LinkedAt)
	}
	if g.ExpiresAt != nil {
		out.CodeExpiresAt = nullable.NewNullableWithValue(*g.ExpiresAt)
	}
	return out
}

func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *apiServer) OperationsGetSiteChatGroup(ctx context.Context, req gen.OperationsGetSiteChatGroupRequestObject) (gen.OperationsGetSiteChatGroupResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	g, err := svc.GroupGet(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	return gen.OperationsGetSiteChatGroup200JSONResponse(siteChatGroupDTO(g)), nil
}

func (s *apiServer) OperationsStartSiteChatGroupLink(ctx context.Context, req gen.OperationsStartSiteChatGroupLinkRequestObject) (gen.OperationsStartSiteChatGroupLinkResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	st, err := svc.GroupStart(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	return gen.OperationsStartSiteChatGroupLink200JSONResponse(gen.SiteChatGroupStarted{Code: st.Code, ExpiresAt: st.ExpiresAt, BotUsername: nullStr(emptyToNil(st.BotUsername))}), nil
}

func (s *apiServer) OperationsUnlinkSiteChatGroup(ctx context.Context, req gen.OperationsUnlinkSiteChatGroupRequestObject) (gen.OperationsUnlinkSiteChatGroupResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	if err := svc.GroupUnlink(ctx, req.OrgId, req.SiteId); err != nil {
		return nil, err
	}
	return gen.OperationsUnlinkSiteChatGroup204Response{}, nil
}

func (s *apiServer) OperationsUpdateSiteChatGroup(ctx context.Context, req gen.OperationsUpdateSiteChatGroupRequestObject) (gen.OperationsUpdateSiteChatGroupResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	var url *string
	if v, err := req.Body.WhatsappInviteUrl.Get(); err == nil {
		url = &v
	}
	if err := svc.SetWhatsAppInvite(ctx, req.OrgId, req.SiteId, url); err != nil {
		return nil, err
	}
	g, err := svc.GroupGet(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	return gen.OperationsUpdateSiteChatGroup200JSONResponse(siteChatGroupDTO(g)), nil
}

func (s *apiServer) OperationsGetSiteChatInfo(ctx context.Context, req gen.OperationsGetSiteChatInfoRequestObject) (gen.OperationsGetSiteChatInfoResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	u, err := svc.WhatsAppInvite(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	return gen.OperationsGetSiteChatInfo200JSONResponse(gen.SiteChatInfo{WhatsappInviteUrl: nullStr(u)}), nil
}
