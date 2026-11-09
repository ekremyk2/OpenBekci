package httpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/privacy"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (s *apiServer) priv() (*privacy.Service, error) {
	if s.privacy == nil {
		return nil, problem.Internal(errors.New("privacy module is not wired"))
	}
	return s.privacy, nil
}

// consentItems returns the privacy notice and consent texts of the organization in a
// locale (empty: the organization's): the enabled packs' when they have any, else the
// generic notice. State is pending for every text; withState fills the caller's decisions.
func (s *apiServer) consentItems(ctx context.Context, org uuid.UUID, locale string) (string, []gen.Consent, error) {
	locale = strings.ToLower(locale)
	if locale == "" && s.tenancy != nil {
		if o, err := s.tenancy.Get(ctx, org); err == nil {
			locale = o.DefaultLocale
		}
	}
	if i := strings.IndexAny(locale, "-_"); i > 0 {
		locale = locale[:i]
	}
	if locale != "tr" {
		locale = "en"
	}
	out := []gen.Consent{}
	if s.packs != nil {
		items, err := s.packs.Consents(ctx, org, locale)
		if err != nil {
			return "", nil, err
		}
		for _, it := range items {
			pack := it.Pack
			out = append(out, gen.Consent{Id: it.ID, Pack: &pack, Version: it.Version, Locale: it.Locale, Markdown: it.Markdown})
		}
	}
	if len(out) == 0 {
		out = append(out, gen.Consent{Id: "core.privacy_notice", Version: "1", Locale: locale, Markdown: genericPrivacyNotice[locale]})
	}
	for i := range out {
		out[i].Required = privacy.Required(out[i].Id)
		out[i].State = gen.ConsentStatePending
		out[i].DecidedAt = nullable.NewNullNullable[time.Time]()
	}
	return locale, out, nil
}

// withState fills the caller's decision on each text; a decision on another version counts as none.
func (s *apiServer) withState(ctx context.Context, org uuid.UUID, items []gen.Consent) error {
	p := authz.PrincipalFromContext(ctx)
	if s.privacy == nil || p == nil {
		return nil
	}
	states, err := s.privacy.States(ctx, org, p.UserID)
	if err != nil {
		return problem.Internal(err)
	}
	for i := range items {
		if st, ok := states[items[i].Id]; ok && st.Version == items[i].Version {
			items[i].State = gen.ConsentState(st.Decision)
			items[i].DecidedAt = nullable.NewNullableWithValue(st.DecidedAt)
		}
	}
	return nil
}

// CodesListConsents serves the privacy notice and consent texts with the caller's decisions.
func (s *apiServer) CodesListConsents(ctx context.Context, req gen.CodesListConsentsRequestObject) (gen.CodesListConsentsResponseObject, error) {
	if err := authz.RequireInOrg(ctx, codes.PermRead, req.OrgId); err != nil {
		return nil, err
	}
	locale, items, err := s.consentItems(ctx, req.OrgId, deref(req.Params.Locale))
	if err != nil {
		return nil, err
	}
	if err := s.withState(ctx, req.OrgId, items); err != nil {
		return nil, err
	}
	return gen.CodesListConsents200JSONResponse(gen.ConsentList{Locale: locale, Items: items}), nil
}

// decideConsent records the caller's decision on the current version of a text.
func (s *apiServer) decideConsent(ctx context.Context, org uuid.UUID, purpose string, body *gen.ConsentDecision, decision string) (gen.Consent, error) {
	svc, err := s.priv()
	if err != nil {
		return gen.Consent{}, err
	}
	if err := authz.RequireInOrg(ctx, people.PermMeManage, org); err != nil {
		return gen.Consent{}, err
	}
	p, err := principal(ctx)
	if err != nil {
		return gen.Consent{}, err
	}
	if body == nil || body.Version == "" {
		return gen.Consent{}, problem.Validation(problem.Field{Field: "version", Code: "required"})
	}
	_, items, err := s.consentItems(ctx, org, "")
	if err != nil {
		return gen.Consent{}, err
	}
	i := 0
	for i < len(items) && items[i].Id != purpose {
		i++
	}
	if i == len(items) {
		return gen.Consent{}, problem.NotFound("no such consent text")
	}
	if items[i].Version != body.Version {
		return gen.Consent{}, problem.Conflict("the text has version " + items[i].Version + "; read it again before deciding")
	}
	if err := svc.Decide(ctx, org, p.UserID, purpose, body.Version, decision); err != nil {
		return gen.Consent{}, problem.Internal(err)
	}
	if err := s.withState(ctx, org, items[i:i+1]); err != nil {
		return gen.Consent{}, err
	}
	return items[i], nil
}

// CodesAcceptConsent records that the caller accepts a consent text.
func (s *apiServer) CodesAcceptConsent(ctx context.Context, req gen.CodesAcceptConsentRequestObject) (gen.CodesAcceptConsentResponseObject, error) {
	c, err := s.decideConsent(ctx, req.OrgId, req.Purpose, req.Body, privacy.Accepted)
	if err != nil {
		return nil, err
	}
	return gen.CodesAcceptConsent200JSONResponse(c), nil
}

// CodesDeclineConsent records that the caller declines a consent text.
func (s *apiServer) CodesDeclineConsent(ctx context.Context, req gen.CodesDeclineConsentRequestObject) (gen.CodesDeclineConsentResponseObject, error) {
	c, err := s.decideConsent(ctx, req.OrgId, req.Purpose, req.Body, privacy.Declined)
	if err != nil {
		return nil, err
	}
	return gen.CodesDeclineConsent200JSONResponse(c), nil
}

// pendingConsents lists, per organization of the caller, the required consent texts they
// have not accepted at the current version (GET /v1/me).
func (s *apiServer) pendingConsents(ctx context.Context, userID uuid.UUID, orgs []gen.OrgAccess) error {
	if s.privacy == nil {
		return nil
	}
	accepted, err := s.privacy.Accepted(ctx, userID)
	if err != nil {
		return problem.Internal(err)
	}
	for i := range orgs {
		_, items, err := s.consentItems(ctx, orgs[i].Id, orgs[i].DefaultLocale)
		if err != nil {
			return err
		}
		pending := []string{}
		for _, it := range items {
			if it.Required && accepted[orgs[i].Id][it.Id] != it.Version {
				pending = append(pending, it.Id)
			}
		}
		orgs[i].PendingConsents = &pending
	}
	return nil
}

// PeopleRequestDataExport queues the export of a person's data.
func (s *apiServer) PeopleRequestDataExport(ctx context.Context, req gen.PeopleRequestDataExportRequestObject) (gen.PeopleRequestDataExportResponseObject, error) {
	svc, err := s.priv()
	if err != nil {
		return nil, err
	}
	id, err := svc.RequestExport(ctx, req.OrgId, req.PersonId)
	if err != nil {
		return nil, err
	}
	return gen.PeopleRequestDataExport202JSONResponse(gen.DataExportRequested{RequestId: id, Status: gen.DataExportRequestedStatusQueued}), nil
}

// PeopleErasePerson erases a person (people.erase and a recent re-authentication).
func (s *apiServer) PeopleErasePerson(ctx context.Context, req gen.PeopleErasePersonRequestObject) (gen.PeopleErasePersonResponseObject, error) {
	svc, err := s.priv()
	if err != nil {
		return nil, err
	}
	reason := ""
	if req.Body != nil {
		reason = strings.TrimSpace(req.Body.Reason)
	}
	if reason == "" || len(reason) > 500 {
		return nil, problem.Validation(problem.Field{Field: "reason", Code: "invalid_value"})
	}
	if _, err := svc.Erase(ctx, req.OrgId, req.PersonId, reason, deref(req.Body.OverrideOpenBalance)); err != nil {
		return nil, err
	}
	return gen.PeopleErasePerson204Response{}, nil
}
