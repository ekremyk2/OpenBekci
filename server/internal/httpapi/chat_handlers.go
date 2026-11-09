package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/oapi-codegen/nullable"

	chatcore "github.com/openbekci/openbekci/server/internal/core/chat"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (s *apiServer) chatSvc() (*chatcore.Service, error) {
	if s.chat == nil {
		return nil, problem.Internal(errors.New("chat module is not wired"))
	}
	return s.chat, nil
}

func chatLinkDTO(l chatcore.Link) gen.ChatLink {
	out := gen.ChatLink{Id: l.ID, OrgId: l.OrgID, Provider: l.Provider, Status: gen.ChatLinkStatus(l.Status), CreatedAt: l.CreatedAt, LinkedAt: nullable.NewNullNullable[time.Time]()}
	if l.LinkedAt != nil {
		out.LinkedAt = nullable.NewNullableWithValue(*l.LinkedAt)
	}
	return out
}

func chatLinkDTOs(ls []chatcore.Link) []gen.ChatLink {
	out := make([]gen.ChatLink, 0, len(ls))
	for _, l := range ls {
		out = append(out, chatLinkDTO(l))
	}
	return out
}

func (s *apiServer) OperationsListChatLinks(ctx context.Context, req gen.OperationsListChatLinksRequestObject) (gen.OperationsListChatLinksResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ls, err := svc.ListMine(ctx, req.OrgId, p.UserID)
	if err != nil {
		return nil, problem.Internal(err)
	}
	return gen.OperationsListChatLinks200JSONResponse(gen.ChatLinkList{Items: chatLinkDTOs(ls)}), nil
}

func (s *apiServer) OperationsStartChatLink(ctx context.Context, req gen.OperationsStartChatLinkRequestObject) (gen.OperationsStartChatLinkResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := svc.Start(ctx, req.OrgId, p.UserID, req.Provider)
	if err != nil {
		return nil, err
	}
	return gen.OperationsStartChatLink200JSONResponse(gen.ChatLinkStarted{Provider: st.Provider, DeepLink: st.DeepLink, ExpiresAt: st.ExpiresAt}), nil
}

func (s *apiServer) OperationsDeleteChatLink(ctx context.Context, req gen.OperationsDeleteChatLinkRequestObject) (gen.OperationsDeleteChatLinkResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.Unlink(ctx, req.OrgId, p.UserID, req.LinkId); err != nil {
		return nil, err
	}
	return gen.OperationsDeleteChatLink204Response{}, nil
}

func chatSettingsDTO(ps chatcore.ProviderSettings) gen.ChatProviderSettings {
	return gen.ChatProviderSettings{Provider: ps.Provider, Schema: ps.Schema, Values: ps.Values, SecretsSet: ps.SecretsSet, Configured: ps.Configured, WebhookUrl: ps.WebhookURL}
}

func (s *apiServer) OperationsListChatSettings(ctx context.Context, req gen.OperationsListChatSettingsRequestObject) (gen.OperationsListChatSettingsResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	all, err := svc.Settings(ctx, req.OrgId)
	if err != nil {
		return nil, problem.Internal(err)
	}
	items := make([]gen.ChatProviderSettings, 0, len(all))
	for _, ps := range all {
		items = append(items, chatSettingsDTO(ps))
	}
	return gen.OperationsListChatSettings200JSONResponse(gen.ChatSettingsList{Items: items}), nil
}

func (s *apiServer) OperationsUpdateChatSettings(ctx context.Context, req gen.OperationsUpdateChatSettingsRequestObject) (gen.OperationsUpdateChatSettingsResponseObject, error) {
	svc, err := s.chatSvc()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	ps, err := svc.UpdateSettings(ctx, req.OrgId, req.Provider, req.Body.Values)
	if err != nil {
		return nil, err
	}
	return gen.OperationsUpdateChatSettings200JSONResponse(chatSettingsDTO(ps)), nil
}
