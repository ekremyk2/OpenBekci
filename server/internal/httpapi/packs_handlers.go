package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/instance"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var errNoPacks = errors.New("packs runtime not configured")

func (s *apiServer) pk() (*packs.Host, error) {
	if s.packs == nil {
		return nil, problem.Internal(errNoPacks)
	}
	return s.packs, nil
}

// settingsViewWithPacks is settingsView plus the "pack default" marker: the
// settings keys a pack filled in that still hold the value the pack wrote.
func (s *apiServer) settingsViewWithPacks(ctx context.Context, org, site uuid.UUID, settings map[string]any, version int) map[string]any {
	v := settingsView(settings, version)
	if s.packs == nil {
		return v
	}
	defs, err := s.packs.SiteDefaults(ctx, org, site)
	if err != nil || len(defs) == 0 {
		return v
	}
	m := make(map[string]any, len(defs))
	for k, d := range defs {
		m[k] = map[string]any{"pack": d.Pack, "value": d.Value}
	}
	v["pack_defaults"] = m
	return v
}

func packDetailsDTO(in []instance.PackDetail) []gen.InstancePack {
	out := make([]gen.InstancePack, 0, len(in))
	for _, p := range in {
		out = append(out, gen.InstancePack{Id: p.ID, Version: p.Version, MinSdk: p.MinSDK, Countries: p.Countries, Enabled: p.Enabled})
	}
	return out
}

func packDTO(st packs.State) gen.Pack {
	schemas := make(map[string]interface{}, len(st.Schemas))
	for k, v := range st.Schemas {
		schemas[k] = v
	}
	out := gen.Pack{
		Id: st.ID, Version: st.Version, MinSdk: st.MinSDK, Countries: st.Countries, Provides: st.Provides,
		Enabled: st.Enabled, EnabledAt: nullable.NewNullNullable[time.Time](),
		SettingsSchemas: schemas, Settings: st.Settings, SecretsSet: st.SecretsSet, Sites: []gen.PackSiteSettings{},
	}
	if st.Provides == nil {
		out.Provides = []string{}
	}
	if st.EnabledAt != nil {
		out.EnabledAt = nullable.NewNullableWithValue(*st.EnabledAt)
	}
	for _, ss := range st.Sites {
		out.Sites = append(out.Sites, gen.PackSiteSettings{SiteId: ss.SiteID, Values: ss.Values, SecretsSet: ss.SecretsSet})
	}
	return out
}

func (s *apiServer) PacksList(ctx context.Context, req gen.PacksListRequestObject) (gen.PacksListResponseObject, error) {
	h, err := s.pk()
	if err != nil {
		return nil, err
	}
	list, err := h.List(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Pack, 0, len(list))
	for _, st := range list {
		items = append(items, packDTO(st))
	}
	return gen.PacksList200JSONResponse(gen.PackList{Items: items}), nil
}

func (s *apiServer) PacksEnable(ctx context.Context, req gen.PacksEnableRequestObject) (gen.PacksEnableResponseObject, error) {
	h, err := s.pk()
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if req.Body != nil && req.Body.Settings != nil {
		values = *req.Body.Settings
	}
	st, err := h.Enable(ctx, req.OrgId, req.PackId, values)
	if err != nil {
		return nil, err
	}
	return gen.PacksEnable200JSONResponse(packDTO(st)), nil
}

func (s *apiServer) PacksDisable(ctx context.Context, req gen.PacksDisableRequestObject) (gen.PacksDisableResponseObject, error) {
	h, err := s.pk()
	if err != nil {
		return nil, err
	}
	st, err := h.Disable(ctx, req.OrgId, req.PackId)
	if err != nil {
		return nil, err
	}
	return gen.PacksDisable200JSONResponse(packDTO(st)), nil
}

func (s *apiServer) PacksUpdateSettings(ctx context.Context, req gen.PacksUpdateSettingsRequestObject) (gen.PacksUpdateSettingsResponseObject, error) {
	h, err := s.pk()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, problem.Validation(problem.Field{Field: "body", Code: "required"})
	}
	var scope sdk.Scope
	switch req.Body.Scope {
	case "org":
		scope = sdk.OrgScope()
	case "site":
		if req.Body.SiteId == nil {
			return nil, problem.Validation(problem.Field{Field: "site_id", Code: "required"})
		}
		scope = sdk.SiteScope(sdk.ID(req.Body.SiteId.String()))
	default:
		return nil, problem.Validation(problem.Field{Field: "scope", Code: "invalid_value"})
	}
	res, isSite, err := h.UpdateSettings(ctx, req.OrgId, req.PackId, scope, req.Body.Values)
	if err != nil {
		return nil, err
	}
	out := gen.PackSettings{Scope: req.Body.Scope, Values: res.Values, SecretsSet: res.SecretsSet, SiteId: nullable.NewNullNullable[openapi_types.UUID]()}
	if res.SecretsSet == nil {
		out.SecretsSet = []string{}
	}
	if isSite {
		out.SiteId = nullable.NewNullableWithValue(res.SiteID)
	}
	return gen.PacksUpdateSettings200JSONResponse(out), nil
}
