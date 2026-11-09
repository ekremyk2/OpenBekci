package packs

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// maxWebhookBody caps what a webhook or callback may send.
const maxWebhookBody = 1 << 20

// Mount adds the pack routes to r: the provider webhook of every payment
// provider, POST /v1/orgs/{org_id}/packs/{pack_id}/payments/{provider}/webhook, and the
// public routes the packs registered, under /v1/orgs/{org_id}/packs/{pack_id}/.
// The tenant is taken from the path, no session is required (the pack verifies
// the caller), the pack must be enabled for the organization (404 otherwise)
// and panics are recovered as pack_error. None of this is in the OpenAPI spec.
func (h *Host) Mount(r chi.Router) {
	r.HandleFunc("/v1/orgs/{org_id}/packs/{pack_id}/*", h.serve)
}

func (h *Host) serve(w http.ResponseWriter, r *http.Request) {
	org, err := uuid.Parse(chi.URLParam(r, "org_id"))
	packID := chi.URLParam(r, "pack_id")
	rest := "/" + chi.URLParam(r, "*")
	pk, ok := h.byID[packID]
	if err != nil || !ok {
		problem.Write(w, r, problem.NotFound("no such pack route"))
		return
	}
	on, err := h.Enabled(r.Context(), org, packID)
	if err != nil {
		problem.Write(w, r, problem.Internal(err))
		return
	}
	if !on {
		problem.Write(w, r, problem.NotFound("the pack is not enabled for this organization"))
		return
	}
	if r.Method == http.MethodPost {
		if provider, perr := splitProviderPath(rest); perr == nil {
			h.serveWebhook(w, r, org, packID, provider)
			return
		}
	}
	for _, rt := range pk.C.Routes {
		if rt.Method == r.Method && rt.Path == rest {
			h.serveRoute(w, r, org, packID, rt)
			return
		}
	}
	problem.Write(w, r, problem.NotFound("no such pack route"))
}

func (h *Host) serveRoute(w http.ResponseWriter, r *http.Request, org uuid.UUID, packID string, rt sdk.Route) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)
	ctx := sdk.WithOrg(r.Context(), sdk.ID(org.String()))
	rec := &statusRecorder{ResponseWriter: w}
	err := safe(packID, "route "+rt.Method+" "+rt.Path, func() error { rt.Handler(rec, r.WithContext(ctx)); return nil })
	if err != nil {
		h.log.ErrorContext(ctx, "pack route panicked", "pack", packID, "path", rt.Path, "error", err)
		if !rec.wrote {
			problem.Write(w, r, MapError(packID, err))
		}
	}
}

// statusRecorder notes whether the handler started a response, so a panic
// after that does not append a second one.
type statusRecorder struct {
	http.ResponseWriter
	wrote bool
}

func (s *statusRecorder) WriteHeader(code int) { s.wrote = true; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

func (h *Host) serveWebhook(w http.ResponseWriter, r *http.Request, org uuid.UUID, packID, provider string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			problem.Write(w, r, problem.New(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge, "the webhook is too large"))
			return
		}
		problem.Write(w, r, problem.BadRequest("the request body could not be read"))
		return
	}
	res, err := h.HandleWebhook(r.Context(), org, packID, provider, sdk.WebhookRequest{
		Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Query: r.URL.Query(), Body: body, ReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeResponse(w, res.Response)
}
