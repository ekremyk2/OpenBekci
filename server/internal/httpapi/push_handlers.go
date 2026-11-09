package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbekci/openbekci/server/internal/core/devices"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoDevices = errors.New("devices service not configured")

func (s *apiServer) dev() (*devices.Service, error) {
	if s.devices == nil {
		return nil, problem.Internal(errNoDevices)
	}
	return s.devices, nil
}

func deviceDTO(d devices.Device) gen.Device {
	return gen.Device{
		Id: d.ID, PushKind: d.PushKind, RelayId: nullStr(d.RelayID), AppVersion: nullStr(d.AppVersion), Locale: nullStr(d.Locale),
		Enabled: d.Enabled, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func (s *apiServer) MeRegisterDevice(ctx context.Context, req gen.MeRegisterDeviceRequestObject) (gen.MeRegisterDeviceResponseObject, error) {
	svc, err := s.dev()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAccount(ctx, identity.PermSessionsManageOwn); err != nil {
		return nil, err
	}
	b := req.Body
	in := devices.Input{PushKind: b.PushKind, Token: b.Token}
	if b.RelayId != nil {
		in.RelayID = *b.RelayId
	}
	if b.AppVersion != nil {
		in.AppVersion = *b.AppVersion
	}
	if b.Locale != nil {
		in.Locale = *b.Locale
	}
	d, err := svc.Register(ctx, p, in)
	if err != nil {
		return nil, err
	}
	return gen.MeRegisterDevice200JSONResponse(deviceDTO(d)), nil
}

func (s *apiServer) MeDeleteDevice(ctx context.Context, req gen.MeDeleteDeviceRequestObject) (gen.MeDeleteDeviceResponseObject, error) {
	svc, err := s.dev()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAccount(ctx, identity.PermSessionsManageOwn); err != nil {
		return nil, err
	}
	if err := svc.Delete(ctx, p, req.Id); err != nil {
		return nil, err
	}
	return gen.MeDeleteDevice204Response{}, nil
}

func (s *apiServer) MeCreateWebLoginLink(ctx context.Context, req gen.MeCreateWebLoginLinkRequestObject) (gen.MeCreateWebLoginLinkResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAccount(ctx, identity.PermSessionsManageOwn); err != nil {
		return nil, err
	}
	next := ""
	if req.Body != nil && req.Body.Next != nil {
		next = *req.Body.Next
	}
	l, err := svc.IssueWebLoginLink(ctx, p, next)
	if err != nil {
		return nil, err
	}
	return gen.MeCreateWebLoginLink201JSONResponse(gen.WebLoginLink{Token: l.Token, Url: l.URL, ExpiresAt: l.ExpiresAt}), nil
}

func (s *apiServer) AuthExchangeWebLoginLink(ctx context.Context, req gen.AuthExchangeWebLoginLinkRequestObject) (gen.AuthExchangeWebLoginLinkResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	m := metaFrom(ctx, req.Body.DeviceName)
	u, err := svc.ExchangeWebLoginLink(ctx, req.Body.Token, m)
	if err != nil {
		return nil, err
	}
	ws, err := svc.IssueWebSession(ctx, u.ID, m)
	if err != nil {
		return nil, err
	}
	http.SetCookie(infoFrom(ctx).w, authCookie(ws.Token, ws.ExpiresAt, s.insecureCookies))
	return gen.AuthExchangeWebLoginLink200JSONResponse(gen.AuthResult{
		Client: gen.AuthResultClient("web"), SessionId: ws.SessionID, ExpiresAt: ws.ExpiresAt, User: authUserDTO(u),
	}), nil
}

func (s *apiServer) OperationsGetNotification(ctx context.Context, req gen.OperationsGetNotificationRequestObject) (gen.OperationsGetNotificationResponseObject, error) {
	n, err := s.nt()
	if err != nil {
		return nil, err
	}
	v, err := n.Get(ctx, req.OrgId, req.NotificationId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Notification](v)
	return gen.OperationsGetNotification200JSONResponse(out), err
}
