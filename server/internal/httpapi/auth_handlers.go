package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoIdentity = errors.New("identity service not configured")

func (s *apiServer) ident() (*identity.Service, error) {
	if s.identity == nil {
		return nil, problem.Internal(errNoIdentity)
	}
	return s.identity, nil
}

func metaFrom(ctx context.Context, device *string) identity.Meta {
	ri := infoFrom(ctx)
	m := identity.Meta{IP: ri.ip, UserAgent: ri.ua}
	if device != nil {
		m.DeviceName = *device
	}
	return m
}

func nullStr(s *string) nullable.Nullable[string] {
	if s == nil {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(*s)
}

func authUserDTO(u identity.User) gen.AuthUser {
	return gen.AuthUser{Id: u.ID, Email: nullStr(u.Email), Phone: nullStr(u.Phone), Locale: u.Locale, IsPlatformAdmin: u.IsPlatformAdmin}
}

// remapField renames the field of a validation problem (the services name
// them after their own input, the credentials body calls both "identifier").
func remapField(err error, from, to string) error {
	var pe *problem.Error
	if errors.As(err, &pe) {
		for i := range pe.Fields {
			if pe.Fields[i].Field == from {
				pe.Fields[i].Field = to
			}
		}
	}
	return err
}

var sixDigitCode = regexp.MustCompile(`^[0-9]{6}$`)

func fieldError(field, code string) error {
	return problem.Validation(problem.Field{Field: field, Code: code})
}

// signIn verifies the credentials of a createSession/createToken body and
// returns the user. sent is true when no verification happened because a code
// or link was sent instead (createSession only, mayRequest).
func signIn(ctx context.Context, svc *identity.Service, c gen.Credentials, mayRequest bool) (u identity.User, sent bool, err error) {
	m := metaFrom(ctx, c.DeviceName)
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	switch c.Method {
	case "password":
		if str(c.Password) == "" {
			return identity.User{}, false, fieldError("password", "required")
		}
		u, err = svc.PasswordSignIn(ctx, c.Identifier, *c.Password, m)
		return u, false, err
	case "sms_otp":
		code := str(c.Code)
		if code == "" {
			if !mayRequest {
				return identity.User{}, false, fieldError("code", "required")
			}
			return identity.User{}, true, remapField(svc.RequestOTP(ctx, c.Identifier, m), "phone", "identifier")
		}
		if !sixDigitCode.MatchString(code) {
			return identity.User{}, false, fieldError("code", "invalid_format")
		}
		u, err = svc.OTPSignIn(ctx, c.Identifier, code, m)
		return u, false, err
	case "magic_link":
		if !mayRequest {
			return identity.User{}, false, fieldError("method", "invalid_value")
		}
		client := identity.ClientWeb
		if c.Client != nil && *c.Client == gen.CredentialsClientMobile {
			client = identity.ClientMobile
		}
		return identity.User{}, true, remapField(svc.RequestMagicLink(ctx, c.Identifier, client, m), "email", "identifier")
	}
	return identity.User{}, false, fieldError("method", "invalid_value")
}

func (s *apiServer) AuthCreateSession(ctx context.Context, req gen.AuthCreateSessionRequestObject) (gen.AuthCreateSessionResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	u, sent, err := signIn(ctx, svc, *req.Body, true)
	if err != nil {
		return nil, err
	}
	if sent {
		return gen.AuthCreateSession202Response{}, nil
	}
	ws, err := svc.IssueWebSession(ctx, u.ID, metaFrom(ctx, req.Body.DeviceName))
	if err != nil {
		return nil, err
	}
	http.SetCookie(infoFrom(ctx).w, authCookie(ws.Token, ws.ExpiresAt, s.insecureCookies))
	return gen.AuthCreateSession200JSONResponse(gen.SessionResponse{SessionId: ws.SessionID, ExpiresAt: ws.ExpiresAt, User: authUserDTO(u)}), nil
}

func tokenResponse(tp identity.TokenPair, u identity.User) gen.TokenResponse {
	return gen.TokenResponse{
		TokenType: "Bearer", AccessToken: tp.AccessToken, AccessExpiresAt: tp.AccessExpiresAt,
		RefreshToken: tp.RefreshToken, RefreshExpiresAt: tp.RefreshExpiresAt, SessionId: tp.SessionID, User: authUserDTO(u),
	}
}

func (s *apiServer) AuthCreateToken(ctx context.Context, req gen.AuthCreateTokenRequestObject) (gen.AuthCreateTokenResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	u, _, err := signIn(ctx, svc, *req.Body, false)
	if err != nil {
		return nil, err
	}
	tp, err := svc.IssueTokens(ctx, u.ID, metaFrom(ctx, req.Body.DeviceName))
	if err != nil {
		return nil, err
	}
	return gen.AuthCreateToken200JSONResponse(tokenResponse(tp, u)), nil
}

func (s *apiServer) AuthRefreshToken(ctx context.Context, req gen.AuthRefreshTokenRequestObject) (gen.AuthRefreshTokenResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	tp, uid, err := svc.Refresh(ctx, req.Body.RefreshToken, metaFrom(ctx, nil))
	if err != nil {
		return nil, err
	}
	u, err := svc.GetUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	return gen.AuthRefreshToken200JSONResponse(tokenResponse(tp, u)), nil
}

func (s *apiServer) AuthRequestOtp(ctx context.Context, req gen.AuthRequestOtpRequestObject) (gen.AuthRequestOtpResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	if err := svc.RequestOTP(ctx, req.Body.Phone, metaFrom(ctx, nil)); err != nil {
		return nil, err
	}
	return gen.AuthRequestOtp202Response{}, nil
}

func clientOf(c *gen.MagicLinkRequestClient) identity.Client {
	if c != nil && *c == gen.MagicLinkRequestClientMobile {
		return identity.ClientMobile
	}
	return identity.ClientWeb
}

func (s *apiServer) AuthRequestMagicLink(ctx context.Context, req gen.AuthRequestMagicLinkRequestObject) (gen.AuthRequestMagicLinkResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	if err := svc.RequestMagicLink(ctx, req.Body.Email, clientOf(req.Body.Client), metaFrom(ctx, nil)); err != nil {
		return nil, err
	}
	return gen.AuthRequestMagicLink202Response{}, nil
}

func (s *apiServer) AuthConsumeMagicLink(ctx context.Context, req gen.AuthConsumeMagicLinkRequestObject) (gen.AuthConsumeMagicLinkResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	m := metaFrom(ctx, req.Body.DeviceName)
	u, err := svc.MagicLinkSignIn(ctx, req.Body.Token, m)
	if err != nil {
		return nil, err
	}
	if req.Body.Client != nil && *req.Body.Client == gen.MagicLinkConsumeRequestClientMobile {
		tp, err := svc.IssueTokens(ctx, u.ID, m)
		if err != nil {
			return nil, err
		}
		return gen.AuthConsumeMagicLink200JSONResponse(gen.AuthResult{
			Client: gen.AuthResultClient("mobile"), SessionId: tp.SessionID, ExpiresAt: tp.AccessExpiresAt, User: authUserDTO(u),
			AccessToken: &tp.AccessToken, RefreshToken: &tp.RefreshToken, RefreshExpiresAt: &tp.RefreshExpiresAt,
		}), nil
	}
	ws, err := svc.IssueWebSession(ctx, u.ID, m)
	if err != nil {
		return nil, err
	}
	http.SetCookie(infoFrom(ctx).w, authCookie(ws.Token, ws.ExpiresAt, s.insecureCookies))
	return gen.AuthConsumeMagicLink200JSONResponse(gen.AuthResult{
		Client: gen.AuthResultClient("web"), SessionId: ws.SessionID, ExpiresAt: ws.ExpiresAt, User: authUserDTO(u),
	}), nil
}

func principal(ctx context.Context) (*authz.Principal, error) {
	p := authz.PrincipalFromContext(ctx)
	if p == nil {
		return nil, problem.Unauthenticated("authentication is required")
	}
	return p, nil
}

func (s *apiServer) AuthReauth(ctx context.Context, req gen.AuthReauthRequestObject) (gen.AuthReauthResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	method, err := req.Body.Discriminator()
	if err != nil {
		return nil, problem.BadRequest("the request body has no method")
	}
	var rm identity.ReauthMethod
	var secret string
	switch method {
	case "password":
		b, err := req.Body.AsReauthPassword()
		if err != nil {
			return nil, problem.BadRequest("malformed request")
		}
		rm, secret = identity.ReauthPassword, b.Password
	case "sms_otp":
		b, err := req.Body.AsReauthSmsOtp()
		if err != nil {
			return nil, problem.BadRequest("malformed request")
		}
		rm, secret = identity.ReauthSMSOTP, b.Code
	default:
		return nil, problem.Validation(problem.Field{Field: "method", Code: "invalid_value"})
	}
	until, err := svc.Reauth(ctx, p, rm, secret, metaFrom(ctx, nil))
	if err != nil {
		return nil, err
	}
	return gen.AuthReauth200JSONResponse(gen.ReauthResponse{ReauthUntil: until}), nil
}

func (s *apiServer) AuthChangePassword(ctx context.Context, req gen.AuthChangePasswordRequestObject) (gen.AuthChangePasswordResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.ChangePassword(ctx, p, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return gen.AuthChangePassword204Response{}, nil
}

// permissionGrants derives the effective permissions of an organization from
// the caller's active memberships (the same derivation the Resolver applies
// per request); absent site_ids and block_ids mean organization-wide.
func (s *apiServer) permissionGrants(o identity.OrgAccess) []gen.PermissionGrant {
	out := []gen.PermissionGrant{}
	if s.resolver == nil {
		return out
	}
	ms := make([]authz.Membership, 0, len(o.Roles))
	for _, r := range o.Roles {
		ms = append(ms, authz.Membership{Role: authz.Role(r.Role), ScopeType: authz.ScopeType(r.ScopeType), ScopeID: r.ScopeID})
	}
	for _, ps := range authz.Summarize(authz.GrantsFromMemberships(s.resolver.Registry(), ms)) {
		g := gen.PermissionGrant{Permission: gen.Permission(ps.Permission)}
		if len(ps.SiteIDs) > 0 {
			ids := ps.SiteIDs
			g.SiteIds = &ids
		}
		if len(ps.BlockIDs) > 0 {
			ids := ps.BlockIDs
			g.BlockIds = &ids
		}
		out = append(out, g)
	}
	return out
}

func (s *apiServer) MeGet(ctx context.Context, _ gen.MeGetRequestObject) (gen.MeGetResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	me, err := svc.Me(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	orgs := make([]gen.OrgAccess, 0, len(me.Organizations))
	for _, o := range me.Organizations {
		roles := make([]gen.RoleGrant, 0, len(o.Roles))
		for _, r := range o.Roles {
			g := gen.RoleGrant{Role: gen.RoleGrantRole(r.Role), ScopeType: gen.RoleGrantScopeType(r.ScopeType), SiteOwned: r.SiteOwned, ScopeId: nullable.NewNullNullable[uuid.UUID]()}
			if r.ScopeID != nil {
				g.ScopeId = nullable.NewNullableWithValue(*r.ScopeID)
			}
			roles = append(roles, g)
		}
		orgs = append(orgs, gen.OrgAccess{
			Id: o.ID, Slug: o.Slug, Name: o.Name, Roles: roles,
			DefaultLocale: o.DefaultLocale,
			Permissions:   s.permissionGrants(o),
			Sites:         meSites(o.Sites),
		})
	}
	return gen.MeGet200JSONResponse(gen.Me{
		User: gen.MeUser{
			Id: me.User.ID, DisplayName: me.DisplayName, Email: nullStr(me.User.Email), Phone: nullStr(me.User.Phone),
			Locale: me.User.Locale, IsPlatformAdmin: me.User.IsPlatformAdmin,
		},
		Organizations: orgs,
		Occupancies:   meOccupancies(me.Occupancies),
		Policies:      map[string]interface{}{},
	}), nil
}

func (s *apiServer) MeListSessions(ctx context.Context, _ gen.MeListSessionsRequestObject) (gen.MeListSessionsResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.ListSessions(ctx, p.UserID, p.FamilyID)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Session, 0, len(list))
	for _, si := range list {
		client := "web"
		if si.Kind != identity.KindWebCookie {
			client = "mobile"
		}
		it := gen.Session{
			Id: si.ID, Client: gen.SessionClient(client), DeviceName: nullStr(si.DeviceName), Ip: nullStr(si.IP),
			UserAgent: nullStr(si.UserAgent), CreatedAt: si.CreatedAt, ExpiresAt: si.ExpiresAt, Current: si.Current,
			LastUsedAt: nullable.NewNullNullable[time.Time](),
		}
		if si.LastUsedAt != nil {
			it.LastUsedAt = nullable.NewNullableWithValue(*si.LastUsedAt)
		}
		items = append(items, it)
	}
	return gen.MeListSessions200JSONResponse(gen.SessionList{Items: items, NextCursor: nullable.NewNullNullable[string]()}), nil
}

func (s *apiServer) MeRevokeSession(ctx context.Context, req gen.MeRevokeSessionRequestObject) (gen.MeRevokeSessionResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	family := p.FamilyID
	current := req.Id == "current"
	if !current {
		if family, err = uuid.Parse(req.Id); err != nil {
			return nil, problem.NotFound("no such session")
		}
	}
	found, err := svc.RevokeSession(ctx, p.UserID, family)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, problem.NotFound("no such session")
	}
	if p.ViaCookie && family == p.FamilyID {
		http.SetCookie(infoFrom(ctx).w, clearedCookie(s.insecureCookies))
	}
	return gen.MeRevokeSession204Response{}, nil
}

func (s *apiServer) MeRevokeAllSessions(ctx context.Context, _ gen.MeRevokeAllSessionsRequestObject) (gen.MeRevokeAllSessionsResponseObject, error) {
	svc, err := s.ident()
	if err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.RevokeAllSessions(ctx, p.UserID); err != nil {
		return nil, err
	}
	if p.ViaCookie {
		http.SetCookie(infoFrom(ctx).w, clearedCookie(s.insecureCookies))
	}
	return gen.MeRevokeAllSessions204Response{}, nil
}

func (s *apiServer) AuthDeleteSession(ctx context.Context, _ gen.AuthDeleteSessionRequestObject) (gen.AuthDeleteSessionResponseObject, error) {
	if _, err := s.MeRevokeSession(ctx, gen.MeRevokeSessionRequestObject{Id: "current"}); err != nil {
		return nil, err
	}
	return gen.AuthDeleteSession204Response{}, nil
}
