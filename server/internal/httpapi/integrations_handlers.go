package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/oapi-codegen/nullable"

	"github.com/openbekci/openbekci/server/internal/core/integrations"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoIntegrations = errors.New("integrations service not configured")

func (s *apiServer) integ() (*integrations.Service, error) {
	if s.integrations == nil {
		return nil, problem.Internal(errNoIntegrations)
	}
	return s.integrations, nil
}

// nullPtr is the value of an optional nullable field, nil when absent or null.
func nullPtr[T any](n nullable.Nullable[T]) *T {
	if !n.IsSpecified() || n.IsNull() {
		return nil
	}
	v, err := n.Get()
	if err != nil {
		return nil
	}
	return &v
}

// ---- API keys -----------------------------------------------------------------

func (s *apiServer) IntegrationsListApiKeys(ctx context.Context, req gen.IntegrationsListApiKeysRequestObject) (gen.IntegrationsListApiKeysResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	items, next, err := svc.ListAPIKeys(ctx, req.OrgId, strOf(req.Params.Status), limitOf(req.Params.Limit), cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.ApiKeyList](items, next)
	return gen.IntegrationsListApiKeys200JSONResponse(out), err
}

func (s *apiServer) IntegrationsCreateApiKey(ctx context.Context, req gen.IntegrationsCreateApiKeyRequestObject) (gen.IntegrationsCreateApiKeyResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	in := integrations.APIKeyInput{Name: req.Body.Name, Scopes: req.Body.Scopes}
	if id := nullPtr(req.Body.SiteId); id != nil {
		in.SiteID = id
	}
	if t := nullPtr[time.Time](req.Body.ExpiresAt); t != nil {
		in.ExpiresAt = t
	}
	created, err := svc.CreateAPIKey(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ApiKeyCreated](created)
	return gen.IntegrationsCreateApiKey201JSONResponse(out), err
}

func (s *apiServer) IntegrationsRotateApiKey(ctx context.Context, req gen.IntegrationsRotateApiKeyRequestObject) (gen.IntegrationsRotateApiKeyResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	created, err := svc.RotateAPIKey(ctx, req.OrgId, req.KeyId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ApiKeyCreated](created)
	return gen.IntegrationsRotateApiKey200JSONResponse(out), err
}

func (s *apiServer) IntegrationsRevokeApiKey(ctx context.Context, req gen.IntegrationsRevokeApiKeyRequestObject) (gen.IntegrationsRevokeApiKeyResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	k, err := svc.RevokeAPIKey(ctx, req.OrgId, req.KeyId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ApiKey](k)
	return gen.IntegrationsRevokeApiKey200JSONResponse(out), err
}

// ---- webhook endpoints ---------------------------------------------------------

func (s *apiServer) IntegrationsListWebhookEndpoints(ctx context.Context, req gen.IntegrationsListWebhookEndpointsRequestObject) (gen.IntegrationsListWebhookEndpointsResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	items, next, err := svc.ListEndpoints(ctx, req.OrgId, limitOf(req.Params.Limit), cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.WebhookEndpointList](items, next)
	return gen.IntegrationsListWebhookEndpoints200JSONResponse(out), err
}

func (s *apiServer) IntegrationsCreateWebhookEndpoint(ctx context.Context, req gen.IntegrationsCreateWebhookEndpointRequestObject) (gen.IntegrationsCreateWebhookEndpointResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	created, err := svc.CreateEndpoint(ctx, req.OrgId, integrations.EndpointInput{
		URL: req.Body.Url, Events: req.Body.Events, Description: nullPtr(req.Body.Description), Active: req.Body.Active,
	})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.WebhookEndpointCreated](created)
	return gen.IntegrationsCreateWebhookEndpoint201JSONResponse(out), err
}

func (s *apiServer) IntegrationsGetWebhookEndpoint(ctx context.Context, req gen.IntegrationsGetWebhookEndpointRequestObject) (gen.IntegrationsGetWebhookEndpointResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	e, err := svc.GetEndpoint(ctx, req.OrgId, req.EndpointId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.WebhookEndpoint](e)
	return gen.IntegrationsGetWebhookEndpoint200JSONResponse(out), err
}

func (s *apiServer) IntegrationsUpdateWebhookEndpoint(ctx context.Context, req gen.IntegrationsUpdateWebhookEndpointRequestObject) (gen.IntegrationsUpdateWebhookEndpointResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
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
	e, err := svc.UpdateEndpoint(ctx, req.OrgId, req.EndpointId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.WebhookEndpoint](e)
	return gen.IntegrationsUpdateWebhookEndpoint200JSONResponse(out), err
}

func (s *apiServer) IntegrationsDeleteWebhookEndpoint(ctx context.Context, req gen.IntegrationsDeleteWebhookEndpointRequestObject) (gen.IntegrationsDeleteWebhookEndpointResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	if err := svc.DeleteEndpoint(ctx, req.OrgId, req.EndpointId); err != nil {
		return nil, err
	}
	return gen.IntegrationsDeleteWebhookEndpoint204Response{}, nil
}

func (s *apiServer) IntegrationsTestWebhookEndpoint(ctx context.Context, req gen.IntegrationsTestWebhookEndpointRequestObject) (gen.IntegrationsTestWebhookEndpointResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	d, err := svc.TestEndpoint(ctx, req.OrgId, req.EndpointId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.WebhookDelivery](d)
	return gen.IntegrationsTestWebhookEndpoint202JSONResponse(out), err
}

func (s *apiServer) IntegrationsListWebhookDeliveries(ctx context.Context, req gen.IntegrationsListWebhookDeliveriesRequestObject) (gen.IntegrationsListWebhookDeliveriesResponseObject, error) {
	svc, err := s.integ()
	if err != nil {
		return nil, err
	}
	items, next, err := svc.ListDeliveries(ctx, req.OrgId, req.EndpointId, strOf(req.Params.Status), limitOf(req.Params.Limit), cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.WebhookDeliveryList](items, next)
	return gen.IntegrationsListWebhookDeliveries200JSONResponse(out), err
}
