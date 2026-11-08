package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// ifMatchOf reads an If-Match header value (the resource's version, optionally quoted).
func ifMatchOf[T ~string](h T) (int, error) {
	s := string(h)
	return ifMatchVersion(&s)
}

// bodyDoc reads the request body as the doc of the fields the client sent, without the
// fields that the path or the handler supplies.
func bodyDoc(isNil bool, body any, drop ...string) (tenancy.Doc, error) {
	if err := hasBody(isNil); err != nil {
		return nil, err
	}
	d, err := docOf(body)
	if err != nil {
		return nil, err
	}
	for _, k := range drop {
		delete(d, k)
	}
	return d, nil
}

func strParam[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func (s *apiServer) orgView(o tenancy.Org) map[string]any {
	return map[string]any{
		"id": o.ID, "slug": o.Slug, "name": o.Name, "default_locale": o.DefaultLocale, "timezone": o.Timezone, "country_code": o.CountryCode,
		"status": o.Status, "settings": o.Settings, "created_at": o.CreatedAt, "updated_at": o.UpdatedAt, "version": o.Version,
	}
}

func (s *apiServer) TenancyGetOrg(ctx context.Context, req gen.TenancyGetOrgRequestObject) (gen.TenancyGetOrgResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	o, err := t.Get(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Organization](s.orgView(o))
	return gen.TenancyGetOrg200JSONResponse(out), err
}

func (s *apiServer) TenancyUpdateOrg(ctx context.Context, req gen.TenancyUpdateOrgRequestObject) (gen.TenancyUpdateOrgResponseObject, error) {
	t, err := s.tn()
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
	b := req.Body
	p := tenancy.Patch{Name: b.Name, CountryCode: b.CountryCode, DefaultLocale: b.DefaultLocale, Timezone: b.Timezone}
	if b.Settings != nil {
		p.Settings = *b.Settings
	}
	o, err := t.Update(ctx, req.OrgId, v, p)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Organization](s.orgView(o))
	return gen.TenancyUpdateOrg200JSONResponse(out), err
}

func (s *apiServer) TenancyListSites(ctx context.Context, req gen.TenancyListSitesRequestObject) (gen.TenancyListSitesResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	items, next, err := t.ListSites(ctx, req.OrgId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	status, q := strParam(req.Params.Status), strings.ToLower(strParam(req.Params.Q))
	views := make([]map[string]any, 0, len(items))
	for _, st := range items {
		if (status != "" && st.Status != status) || (q != "" && !strings.Contains(strings.ToLower(st.Name), q)) {
			continue
		}
		views = append(views, siteView(req.OrgId, st))
	}
	out, err := listOf[gen.SiteList](views, next)
	return gen.TenancyListSites200JSONResponse(out), err
}

func (s *apiServer) TenancyCreateSite(ctx context.Context, req gen.TenancyCreateSiteRequestObject) (gen.TenancyCreateSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	site, err := t.CreateSite(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Site](siteView(req.OrgId, site))
	return gen.TenancyCreateSite201JSONResponse(out), err
}

func (s *apiServer) TenancyGetSite(ctx context.Context, req gen.TenancyGetSiteRequestObject) (gen.TenancyGetSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	site, err := t.GetSite(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Site](siteView(req.OrgId, site))
	return gen.TenancyGetSite200JSONResponse(out), err
}

func (s *apiServer) TenancyUpdateSite(ctx context.Context, req gen.TenancyUpdateSiteRequestObject) (gen.TenancyUpdateSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	site, err := t.UpdateSite(ctx, req.OrgId, req.SiteId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Site](siteView(req.OrgId, site))
	return gen.TenancyUpdateSite200JSONResponse(out), err
}

func (s *apiServer) TenancyGetSiteSettings(ctx context.Context, req gen.TenancyGetSiteSettingsRequestObject) (gen.TenancyGetSiteSettingsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	site, err := t.GetSite(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.SiteSettings](settingsView(site.Settings, site.Version))
	return gen.TenancyGetSiteSettings200JSONResponse(out), err
}

func (s *apiServer) TenancyUpdateSiteSettings(ctx context.Context, req gen.TenancyUpdateSiteSettingsRequestObject) (gen.TenancyUpdateSiteSettingsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	body, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	d, err := settingsDoc(body)
	if err != nil {
		return nil, err
	}
	site, err := t.UpdateSite(ctx, req.OrgId, req.SiteId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.SiteSettings](settingsView(site.Settings, site.Version))
	return gen.TenancyUpdateSiteSettings200JSONResponse(out), err
}

func (s *apiServer) TenancyGetBankDetails(ctx context.Context, req gen.TenancyGetBankDetailsRequestObject) (gen.TenancyGetBankDetailsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	bank, version, err := t.GetBankDetails(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BankDetails](bankView(bank, version))
	return gen.TenancyGetBankDetails200JSONResponse(out), err
}

func (s *apiServer) TenancySetBankDetails(ctx context.Context, req gen.TenancySetBankDetailsRequestObject) (gen.TenancySetBankDetailsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	body, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(body)
	site, err := t.UpdateSite(ctx, req.OrgId, req.SiteId, v, tenancy.Doc{"bank_details": raw})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.BankDetails](bankView(site.BankDetails, site.Version))
	return gen.TenancySetBankDetails200JSONResponse(out), err
}

func (s *apiServer) TenancyListBlocks(ctx context.Context, req gen.TenancyListBlocksRequestObject) (gen.TenancyListBlocksResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	items, err := t.ListBlocks(ctx, req.OrgId, req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	page, next, err := pagedSlice(items, func(b tenancy.Block) uuid.UUID { return b.ID }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, b := range page {
		views = append(views, blockView(req.OrgId, b))
	}
	out, err := listOf[gen.BlockList](views, next)
	return gen.TenancyListBlocks200JSONResponse(out), err
}

func limitOf[T ~int | ~int64](l *T) int {
	if l == nil {
		return 0
	}
	return int(*l)
}

func (s *apiServer) TenancyCreateBlock(ctx context.Context, req gen.TenancyCreateBlockRequestObject) (gen.TenancyCreateBlockResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	b, err := t.CreateBlock(ctx, req.OrgId, req.Body.SiteId, tenancy.Doc{"name": mustJSON(req.Body.Name)})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Block](blockView(req.OrgId, b))
	return gen.TenancyCreateBlock201JSONResponse(out), err
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (s *apiServer) TenancyGetBlock(ctx context.Context, req gen.TenancyGetBlockRequestObject) (gen.TenancyGetBlockResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	b, err := t.GetBlock(ctx, req.OrgId, req.BlockId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Block](blockView(req.OrgId, b))
	return gen.TenancyGetBlock200JSONResponse(out), err
}

func (s *apiServer) TenancyUpdateBlock(ctx context.Context, req gen.TenancyUpdateBlockRequestObject) (gen.TenancyUpdateBlockResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	b, err := t.UpdateBlock(ctx, req.OrgId, req.BlockId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Block](blockView(req.OrgId, b))
	return gen.TenancyUpdateBlock200JSONResponse(out), err
}

func (s *apiServer) TenancyDeleteBlock(ctx context.Context, req gen.TenancyDeleteBlockRequestObject) (gen.TenancyDeleteBlockResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := t.DeleteBlock(ctx, req.OrgId, req.BlockId, v); err != nil {
		return nil, err
	}
	return gen.TenancyDeleteBlock204Response{}, nil
}

func (s *apiServer) TenancyListUnits(ctx context.Context, req gen.TenancyListUnitsRequestObject) (gen.TenancyListUnitsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := tenancy.UnitFilter{BlockID: p.BlockId, Vacant: p.Vacant, Parent: p.ParentUnitId, Type: strParam(p.Type), Query: strParam(p.Q), Status: strParam(p.Status)}
	var items []tenancy.Unit
	var next string
	if p.TagId != nil {
		// unit tags are not served as a resource yet: no tag id exists, so no unit carries one
		items = []tenancy.Unit{}
	} else if items, next, err = t.ListUnits(ctx, req.OrgId, p.SiteId, f, pageOf(p.Limit, p.Cursor)); err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(items))
	for _, u := range items {
		views = append(views, unitView(req.OrgId, u))
	}
	out, err := listOf[gen.UnitList](views, next)
	return gen.TenancyListUnits200JSONResponse(out), err
}

func (s *apiServer) TenancyCreateUnit(ctx context.Context, req gen.TenancyCreateUnitRequestObject) (gen.TenancyCreateUnitResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body, "site_id")
	if err != nil {
		return nil, err
	}
	u, err := t.CreateUnit(ctx, req.OrgId, req.Body.SiteId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Unit](unitView(req.OrgId, u))
	return gen.TenancyCreateUnit201JSONResponse(out), err
}

func (s *apiServer) TenancyGetUnit(ctx context.Context, req gen.TenancyGetUnitRequestObject) (gen.TenancyGetUnitResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	u, err := t.GetUnit(ctx, req.OrgId, req.UnitId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Unit](unitView(req.OrgId, u))
	return gen.TenancyGetUnit200JSONResponse(out), err
}

func (s *apiServer) TenancyUpdateUnit(ctx context.Context, req gen.TenancyUpdateUnitRequestObject) (gen.TenancyUpdateUnitResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body)
	if err != nil {
		return nil, err
	}
	if d.Has("payer_overrides") && !d.IsNull("payer_overrides") {
		// PATCH merges by key; a null value removes the override
		cur, err := t.GetUnit(ctx, req.OrgId, req.UnitId)
		if err != nil {
			return nil, err
		}
		merged := map[string]any{}
		for k, val := range cur.PayerOverrides {
			merged[k] = val
		}
		var patch map[string]any
		if err := d.Get("payer_overrides", &patch); err != nil {
			return nil, err
		}
		for k, val := range patch {
			if val == nil {
				delete(merged, k)
			} else {
				merged[k] = val
			}
		}
		d["payer_overrides"] = mustJSON(merged)
	}
	u, err := t.UpdateUnit(ctx, req.OrgId, req.UnitId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Unit](unitView(req.OrgId, u))
	return gen.TenancyUpdateUnit200JSONResponse(out), err
}

func (s *apiServer) TenancyDeleteUnit(ctx context.Context, req gen.TenancyDeleteUnitRequestObject) (gen.TenancyDeleteUnitResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := t.DeleteUnit(ctx, req.OrgId, req.UnitId, v); err != nil {
		return nil, err
	}
	return gen.TenancyDeleteUnit204Response{}, nil
}

func (s *apiServer) TenancySetUnitTags(ctx context.Context, req gen.TenancySetUnitTagsRequestObject) (gen.TenancySetUnitTagsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	u, err := t.SetUnitTags(ctx, req.OrgId, req.UnitId, v, req.Body.Tags)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Unit](unitView(req.OrgId, u))
	return gen.TenancySetUnitTags200JSONResponse(out), err
}

func (s *apiServer) TenancyImportUnits(ctx context.Context, req gen.TenancyImportUnitsRequestObject) (gen.TenancyImportUnitsResponseObject, error) {
	p, err := s.pp()
	if err != nil {
		return nil, err
	}
	up, err := readUpload(req.Body)
	if err != nil {
		return nil, err
	}
	siteID, err := uuid.Parse(up.fields["site_id"])
	if err != nil {
		return nil, problem.Validation(problem.Field{Field: "site_id", Code: "invalid_format"})
	}
	ir := people.ImportRequest{Filename: up.filename, Data: up.data, DryRun: req.Params.DryRun != nil && *req.Params.DryRun}
	if m := up.fields["mapping"]; m != "" {
		if ir.Mapping, err = columnMapping(m); err != nil {
			return nil, err
		}
	}
	rep, err := p.ImportUnits(ctx, req.OrgId, siteID, ir)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ImportReport](importReportView(rep))
	if err != nil {
		return nil, err
	}
	if rep.Applied {
		return gen.TenancyImportUnits202JSONResponse(out), nil
	}
	return gen.TenancyImportUnits200JSONResponse(out), nil
}

// columnMapping reads the multipart `mapping` field: the draft's ColumnMapping
// ({"columns": {"door_number": "Daire No"}, ...}) or just the columns object.
func columnMapping(raw string) (map[string]string, error) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil, problem.Validation(problem.Field{Field: "mapping", Code: "invalid_format"})
	}
	if cols, ok := generic["columns"]; ok {
		raw = string(cols)
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, problem.Validation(problem.Field{Field: "mapping", Code: "invalid_format"})
	}
	return out, nil
}

func (s *apiServer) TenancyListAllocationKeys(ctx context.Context, req gen.TenancyListAllocationKeysRequestObject) (gen.TenancyListAllocationKeysResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	items, err := t.ListAllocationKeys(ctx, req.OrgId, req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	kind := strParam(req.Params.Kind)
	filtered := items[:0:0]
	for _, k := range items {
		if kind == "" || k.Kind == kind {
			filtered = append(filtered, k)
		}
	}
	page, next, err := pagedSlice(filtered, func(k tenancy.AllocationKey) uuid.UUID { return k.ID }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, k := range page {
		views = append(views, keyView(req.OrgId, k))
	}
	out, err := listOf[gen.AllocationKeyList](views, next)
	return gen.TenancyListAllocationKeys200JSONResponse(out), err
}

func (s *apiServer) TenancyCreateAllocationKey(ctx context.Context, req gen.TenancyCreateAllocationKeyRequestObject) (gen.TenancyCreateAllocationKeyResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body, "site_id")
	if err != nil {
		return nil, err
	}
	k, err := t.CreateAllocationKey(ctx, req.OrgId, req.Body.SiteId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AllocationKey](keyView(req.OrgId, k))
	return gen.TenancyCreateAllocationKey201JSONResponse(out), err
}

func (s *apiServer) TenancyGetAllocationKey(ctx context.Context, req gen.TenancyGetAllocationKeyRequestObject) (gen.TenancyGetAllocationKeyResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	k, err := t.GetAllocationKey(ctx, req.OrgId, req.KeyId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AllocationKey](keyView(req.OrgId, k.AllocationKey))
	return gen.TenancyGetAllocationKey200JSONResponse(out), err
}

func (s *apiServer) TenancyUpdateAllocationKey(ctx context.Context, req gen.TenancyUpdateAllocationKeyRequestObject) (gen.TenancyUpdateAllocationKeyResponseObject, error) {
	t, err := s.tn()
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
	cur, err := t.GetAllocationKey(ctx, req.OrgId, req.KeyId)
	if err != nil {
		return nil, err
	}
	b := req.Body
	var bad []problem.Field
	if b.Code != nil && *b.Code != cur.Code {
		bad = append(bad, problem.Field{Field: "code", Code: "invalid_value"})
	}
	if b.Kind != nil && string(*b.Kind) != cur.Kind {
		bad = append(bad, problem.Field{Field: "kind", Code: "invalid_value"})
	}
	if len(bad) > 0 {
		return nil, problem.Validation(bad...) // the code and kind of a key are fixed once it exists
	}
	name := cur.Name
	if b.Name != nil {
		name = *b.Name
	}
	k, err := t.UpdateAllocationKey(ctx, req.OrgId, req.KeyId, v, tenancy.Doc{"name": mustJSON(name)})
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AllocationKey](keyView(req.OrgId, k))
	return gen.TenancyUpdateAllocationKey200JSONResponse(out), err
}

func (s *apiServer) TenancyDeleteAllocationKey(ctx context.Context, req gen.TenancyDeleteAllocationKeyRequestObject) (gen.TenancyDeleteAllocationKeyResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := t.DeleteAllocationKey(ctx, req.OrgId, req.KeyId, v); err != nil {
		return nil, err
	}
	return gen.TenancyDeleteAllocationKey204Response{}, nil
}

func (s *apiServer) TenancyListAllocationKeyValues(ctx context.Context, req gen.TenancyListAllocationKeyValuesRequestObject) (gen.TenancyListAllocationKeyValuesResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	k, err := t.GetAllocationKey(ctx, req.OrgId, req.KeyId)
	if err != nil {
		return nil, err
	}
	weights, err := t.KeyWeights(ctx, req.OrgId, k.SiteID, k.Code, "", req.Params.BlockId)
	if err != nil {
		return nil, err
	}
	computed := k.Kind == tenancy.KeyEqual || k.Kind == tenancy.KeyArea
	type item struct {
		ID    uuid.UUID
		Value float64
	}
	ids := make([]uuid.UUID, 0, len(weights))
	for id := range weights {
		ids = append(ids, id)
	}
	sortUUIDs(ids)
	all := make([]item, 0, len(ids))
	total := 0.0
	for _, id := range ids {
		f := ratFloat(weights[id])
		total += f
		all = append(all, item{id, f})
	}
	complete := computed || len(k.Values) >= len(weights)
	page, next, err := pagedSlice(all, func(i item) uuid.UUID { return i.ID }, limitOf(req.Params.Limit), strParam(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(page))
	for _, i := range page {
		views = append(views, map[string]any{"unit_id": i.ID, "value": i.Value, "computed": computed})
	}
	var nc any
	if next != "" {
		nc = next
	}
	out, err := conv[gen.AllocationKeyValueList](map[string]any{"items": views, "next_cursor": nc, "total": total, "complete": complete})
	return gen.TenancyListAllocationKeyValues200JSONResponse(out), err
}

func sortUUIDs(ids []uuid.UUID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && bytes.Compare(ids[j][:], ids[j-1][:]) < 0; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

func (s *apiServer) TenancyPutAllocationKeyValues(ctx context.Context, req gen.TenancyPutAllocationKeyValuesRequestObject) (gen.TenancyPutAllocationKeyValuesResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	vals := make([]tenancy.KeyValue, 0, len(req.Body.Items))
	for _, it := range req.Body.Items {
		vals = append(vals, tenancy.KeyValue{UnitID: it.UnitId, Value: strconv.FormatFloat(float64(it.Value), 'f', -1, 32)})
	}
	k, err := t.SetAllocationKeyValues(ctx, req.OrgId, req.KeyId, v, vals)
	if err != nil {
		return nil, err
	}
	total := 0.0
	for _, kv := range k.Values {
		if r, ok := tenancy.ParseWeight(kv.Value); ok {
			total += ratFloat(r)
		}
	}
	out, err := conv[gen.AllocationKeyValuesSummary](map[string]any{"count": len(k.Values), "total": total})
	return gen.TenancyPutAllocationKeyValues200JSONResponse(out), err
}

func (s *apiServer) TenancyListVehicles(ctx context.Context, req gen.TenancyListVehiclesRequestObject) (gen.TenancyListVehiclesResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	var items []tenancy.Vehicle
	var next string
	page := pageOf(req.Params.Limit, req.Params.Cursor)
	switch {
	case req.Params.Archived != nil && *req.Params.Archived:
		items = []tenancy.Vehicle{} // vehicles are never archived yet
	case req.Params.SiteId == nil:
		items, next, err = acrossSites(ctx, t, req.OrgId, func(v tenancy.Vehicle) uuid.UUID { return v.ID }, page.Limit, func(site uuid.UUID) ([]tenancy.Vehicle, error) {
			vs, _, err := t.ListVehicles(ctx, req.OrgId, site, req.Params.UnitId, strParam(req.Params.Q), page)
			return vs, err
		})
	default:
		items, next, err = t.ListVehicles(ctx, req.OrgId, *req.Params.SiteId, req.Params.UnitId, strParam(req.Params.Q), page)
	}
	if err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(items))
	for _, v := range items {
		views = append(views, vehicleView(req.OrgId, v))
	}
	out, err := listOf[gen.VehicleList](views, next)
	return gen.TenancyListVehicles200JSONResponse(out), err
}

func (s *apiServer) TenancyCreateVehicle(ctx context.Context, req gen.TenancyCreateVehicleRequestObject) (gen.TenancyCreateVehicleResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	if req.Body.Archived != nil && *req.Body.Archived {
		return nil, problem.Validation(problem.Field{Field: "archived", Code: "invalid_value"})
	}
	d := tenancy.Doc{"plate": mustJSON(req.Body.Plate)}
	if req.Body.Description.IsSpecified() {
		d["label"] = mustJSON(req.Body.Description)
	}
	v, err := t.CreateVehicle(ctx, req.OrgId, req.Body.UnitId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vehicle](vehicleView(req.OrgId, v))
	return gen.TenancyCreateVehicle201JSONResponse(out), err
}

func (s *apiServer) TenancyGetVehicle(ctx context.Context, req gen.TenancyGetVehicleRequestObject) (gen.TenancyGetVehicleResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := t.GetVehicle(ctx, req.OrgId, req.VehicleId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vehicle](vehicleView(req.OrgId, v))
	return gen.TenancyGetVehicle200JSONResponse(out), err
}

func (s *apiServer) TenancyUpdateVehicle(ctx context.Context, req gen.TenancyUpdateVehicleRequestObject) (gen.TenancyUpdateVehicleResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	ver, err := ifMatchOf(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	d, err := bodyDoc(req.Body == nil, req.Body, "archived")
	if err != nil {
		return nil, err
	}
	if req.Body.Archived != nil && *req.Body.Archived {
		return nil, problem.Validation(problem.Field{Field: "archived", Code: "invalid_value"})
	}
	if raw, ok := d["description"]; ok {
		d["label"] = raw
		delete(d, "description")
	}
	v, err := t.UpdateVehicle(ctx, req.OrgId, req.VehicleId, ver, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vehicle](vehicleView(req.OrgId, v))
	return gen.TenancyUpdateVehicle200JSONResponse(out), err
}

func (s *apiServer) TenancyDeleteVehicle(ctx context.Context, req gen.TenancyDeleteVehicleRequestObject) (gen.TenancyDeleteVehicleResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if _, err := ifMatchOf(req.Params.IfMatch); err != nil {
		return nil, err
	}
	if err := t.DeleteVehicle(ctx, req.OrgId, req.VehicleId); err != nil {
		return nil, err
	}
	return gen.TenancyDeleteVehicle204Response{}, nil
}

func (s *apiServer) TenancyExportVehicles(ctx context.Context, req gen.TenancyExportVehiclesRequestObject) (gen.TenancyExportVehiclesResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if f := strParam(req.Params.Format); f != "" && f != "csv" {
		return nil, problem.Validation(problem.Field{Field: "format", Code: "invalid_value"})
	}
	b, err := t.ExportVehiclesCSV(ctx, req.OrgId, req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	return gen.TenancyExportVehicles200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
}

func (s *apiServer) TenancyExportSite(ctx context.Context, req gen.TenancyExportSiteRequestObject) (gen.TenancyExportSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := reauthFor(ctx, req.OrgId, tenancy.PermSiteExport); err != nil {
		return nil, err
	}
	e, err := t.ExportSite(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.SiteTransfer](transferView(req.OrgId, e))
	return gen.TenancyExportSite202JSONResponse(out), err
}

func (s *apiServer) TenancyImportSite(ctx context.Context, req gen.TenancyImportSiteRequestObject) (gen.TenancyImportSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := reauthFor(ctx, req.OrgId, tenancy.PermSiteImport); err != nil {
		return nil, err
	}
	up, err := readUpload(req.Body)
	if err != nil {
		return nil, err
	}
	e, err := t.ImportSite(ctx, req.OrgId, up.data)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.SiteTransfer](transferView(req.OrgId, e))
	return gen.TenancyImportSite202JSONResponse(out), err
}

func (s *apiServer) TenancyOffboardSite(ctx context.Context, req gen.TenancyOffboardSiteRequestObject) (gen.TenancyOffboardSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := reauthFor(ctx, req.OrgId, tenancy.PermSiteOffboard); err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	cur, err := t.GetSite(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	if req.Body.ConfirmSiteName != cur.Name {
		return nil, problem.Validation(problem.Field{Field: "confirm_site_name", Code: "invalid_value"})
	}
	site, err := t.OffboardSite(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	// the offboarding leaves no record of its own yet: the transfer carries the site id
	tr := tenancy.SiteExport{ID: site.ID, Direction: "offboard", SiteID: &site.ID, SiteName: &site.Name, SourceOrgID: &req.OrgId, Status: "done", At: site.UpdatedAt}
	out, err := conv[gen.SiteTransfer](transferView(req.OrgId, tr))
	return gen.TenancyOffboardSite200JSONResponse(out), err
}

func (s *apiServer) TenancyListSiteTransfers(ctx context.Context, req gen.TenancyListSiteTransfersRequestObject) (gen.TenancyListSiteTransfersResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	items, next, err := t.ListSiteExports(ctx, req.OrgId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	dir := strParam(req.Params.Direction)
	views := make([]map[string]any, 0, len(items))
	for _, e := range items {
		if dir == "" || e.Direction == dir {
			views = append(views, transferView(req.OrgId, e))
		}
	}
	out, err := listOf[gen.SiteTransferList](views, next)
	return gen.TenancyListSiteTransfers200JSONResponse(out), err
}

func (s *apiServer) TenancyGetSiteTransfer(ctx context.Context, req gen.TenancyGetSiteTransferRequestObject) (gen.TenancyGetSiteTransferResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	e, err := t.GetSiteExport(ctx, req.OrgId, req.TransferId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.SiteTransfer](transferView(req.OrgId, e))
	return gen.TenancyGetSiteTransfer200JSONResponse(out), err
}

func (s *apiServer) TenancyDownloadSiteTransferArchive(ctx context.Context, req gen.TenancyDownloadSiteTransferArchiveRequestObject) (gen.TenancyDownloadSiteTransferArchiveResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	data, _, err := t.ExportArchive(ctx, req.OrgId, req.TransferId, false)
	if err != nil {
		return nil, err
	}
	return gen.TenancyDownloadSiteTransferArchive200ApplicationoctetStreamResponse{Body: bytes.NewReader(data), ContentLength: int64(len(data))}, nil
}
