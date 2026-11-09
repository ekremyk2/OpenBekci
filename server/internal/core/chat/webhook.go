package chat

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/platform/chat"
	"github.com/openbekci/openbekci/server/internal/problem"
)

const maxWebhookBody = 1 << 20

// Mount adds the provider webhook routes: public, the organization from the
// path, not in the OpenAPI spec (like the routes of packs). The provider
// authenticates every request itself (secret header or signature); anything
// unauthenticated is a 401 before the body is looked at.
//
//	POST /v1/orgs/{org_id}/chat/webhooks/{provider}  updates and messages
//	GET  /v1/orgs/{org_id}/chat/webhooks/{provider}  the provider's URL verification (WhatsApp)
func (s *Service) Mount(r chi.Router) {
	r.Post("/v1/orgs/{org_id}/chat/webhooks/{provider}", s.webhook)
	r.Get("/v1/orgs/{org_id}/chat/webhooks/{provider}", s.challenge)
}

func (s *Service) target(w http.ResponseWriter, r *http.Request) (uuid.UUID, chat.Provider, bool) {
	org, err := uuid.Parse(chi.URLParam(r, "org_id"))
	if err != nil {
		problem.Write(w, r, problem.NotFound("not found"))
		return uuid.Nil, nil, false
	}
	p, err := s.provider(r.Context(), org, chi.URLParam(r, "provider"))
	if err != nil {
		problem.Write(w, r, problem.NotFound("not found")) // unconfigured and unknown look the same
		return uuid.Nil, nil, false
	}
	return org, p, true
}

func (s *Service) webhook(w http.ResponseWriter, r *http.Request) {
	org, p, ok := s.target(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		problem.Write(w, r, problem.New(http.StatusRequestEntityTooLarge, problem.CodeValidationFailed, "the body is too large"))
		return
	}
	if err := p.VerifyWebhook(r, body); err != nil {
		problem.Write(w, r, problem.Unauthenticated("the webhook is not authentic"))
		return
	}
	msgs, err := p.ParseInbound(body)
	if err != nil {
		// authentic but unreadable: answer 200 so the provider does not retry it forever
		s.cfg.Log.WarnContext(r.Context(), "chat webhook: unreadable body", "provider", p.ID(), "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := s.Inbound(r.Context(), org, p.ID(), msgs); err != nil {
		problem.Write(w, r, problem.Internal(err)) // the provider retries
		return
	}
	if gb, ok := p.(chat.GroupBot); ok { // group links, bot commands and poll votes
		if err := s.Update(r.Context(), org, gb, body); err != nil {
			problem.Write(w, r, problem.Internal(err))
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) challenge(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.target(w, r)
	if !ok {
		return
	}
	c, isC := p.(chat.Challenger)
	if !isC {
		problem.Write(w, r, problem.NotFound("not found"))
		return
	}
	answer, ok := c.Challenge(r.URL.Query())
	if !ok {
		problem.Write(w, r, problem.Unauthenticated("the verify token is wrong"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, answer) //nolint:gosec // text/plain with nosniff, the challenge is echoed only after the verify token matched
}
