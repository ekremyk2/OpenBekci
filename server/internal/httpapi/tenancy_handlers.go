package httpapi

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/openbekci/openbekci/server/internal/core/codes"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func (s *apiServer) TenancyListSites(ctx context.Context, req gen.TenancyListSitesRequestObject) (gen.TenancyListSitesResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	items, next, err := t.ListSites(ctx, req.OrgId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.SiteList](items, next)
	return gen.TenancyListSites200JSONResponse(out), err
}

func (s *apiServer) TenancyCreateSite(ctx context.Context, req gen.TenancyCreateSiteRequestObject) (gen.TenancyCreateSiteResponseObject, error) {
	t, err := s.tn()
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
	site, err := t.CreateSite(ctx, req.OrgId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Site](site)
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
	out, err := conv[gen.Site](site)
	return gen.TenancyGetSite200JSONResponse{Body: out, Headers: gen.TenancyGetSite200ResponseHeaders{ETag: ptr(etag(site.Version))}}, err
}

func (s *apiServer) TenancyUpdateSite(ctx context.Context, req gen.TenancyUpdateSiteRequestObject) (gen.TenancyUpdateSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(req.Params.IfMatch)
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
	site, err := t.UpdateSite(ctx, req.OrgId, req.SiteId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Site](site)
	return gen.TenancyUpdateSite200JSONResponse{Body: out, Headers: gen.TenancyUpdateSite200ResponseHeaders{ETag: ptr(etag(site.Version))}}, err
}

func (s *apiServer) TenancyListBlocks(ctx context.Context, req gen.TenancyListBlocksRequestObject) (gen.TenancyListBlocksResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	items, err := t.ListBlocks(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.BlockList](items, "")
	return gen.TenancyListBlocks200JSONResponse(out), err
}

func (s *apiServer) TenancyCreateBlock(ctx context.Context, req gen.TenancyCreateBlockRequestObject) (gen.TenancyCreateBlockResponseObject, error) {
	t, err := s.tn()
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
	b, err := t.CreateBlock(ctx, req.OrgId, req.SiteId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Block](b)
	return gen.TenancyCreateBlock201JSONResponse(out), err
}

func (s *apiServer) TenancyUpdateBlock(ctx context.Context, req gen.TenancyUpdateBlockRequestObject) (gen.TenancyUpdateBlockResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(req.Params.IfMatch)
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
	b, err := t.UpdateBlock(ctx, req.OrgId, req.BlockId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Block](b)
	return gen.TenancyUpdateBlock200JSONResponse{Body: out, Headers: gen.TenancyUpdateBlock200ResponseHeaders{ETag: ptr(etag(b.Version))}}, err
}

func (s *apiServer) TenancyListUnits(ctx context.Context, req gen.TenancyListUnitsRequestObject) (gen.TenancyListUnitsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := tenancy.UnitFilter{BlockID: p.BlockId, Vacant: p.Vacant, Parent: p.ParentUnitId}
	if p.Type != nil {
		f.Type = *p.Type
	}
	if p.Q != nil {
		f.Query = *p.Q
	}
	items, next, err := t.ListUnits(ctx, req.OrgId, req.SiteId, f, pageOf(p.Limit, p.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.UnitList](items, next)
	return gen.TenancyListUnits200JSONResponse(out), err
}

func (s *apiServer) TenancyCreateUnit(ctx context.Context, req gen.TenancyCreateUnitRequestObject) (gen.TenancyCreateUnitResponseObject, error) {
	t, err := s.tn()
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
	u, err := t.CreateUnit(ctx, req.OrgId, req.SiteId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Unit](u)
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
	out, err := conv[gen.Unit](u)
	return gen.TenancyGetUnit200JSONResponse{Body: out, Headers: gen.TenancyGetUnit200ResponseHeaders{ETag: ptr(etag(u.Version))}}, err
}

func (s *apiServer) TenancyUpdateUnit(ctx context.Context, req gen.TenancyUpdateUnitRequestObject) (gen.TenancyUpdateUnitResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(req.Params.IfMatch)
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
	u, err := t.UpdateUnit(ctx, req.OrgId, req.UnitId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Unit](u)
	return gen.TenancyUpdateUnit200JSONResponse{Body: out, Headers: gen.TenancyUpdateUnit200ResponseHeaders{ETag: ptr(etag(u.Version))}}, err
}

func (s *apiServer) TenancySetUnitTags(ctx context.Context, req gen.TenancySetUnitTagsRequestObject) (gen.TenancySetUnitTagsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	u, err := t.SetUnitTags(ctx, req.OrgId, req.UnitId, v, req.Body.Tags)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Unit](u)
	return gen.TenancySetUnitTags200JSONResponse(out), err
}

func (s *apiServer) TenancyListAllocationKeys(ctx context.Context, req gen.TenancyListAllocationKeysRequestObject) (gen.TenancyListAllocationKeysResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	items, err := t.ListAllocationKeys(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.AllocationKeyList](items, "")
	return gen.TenancyListAllocationKeys200JSONResponse(out), err
}

func (s *apiServer) TenancyCreateAllocationKey(ctx context.Context, req gen.TenancyCreateAllocationKeyRequestObject) (gen.TenancyCreateAllocationKeyResponseObject, error) {
	t, err := s.tn()
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
	k, err := t.CreateAllocationKey(ctx, req.OrgId, req.SiteId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AllocationKey](k)
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
	out, err := conv[gen.AllocationKeyDetail](k)
	return gen.TenancyGetAllocationKey200JSONResponse{Body: out, Headers: gen.TenancyGetAllocationKey200ResponseHeaders{ETag: ptr(etag(k.Version))}}, err
}

func (s *apiServer) TenancyUpdateAllocationKey(ctx context.Context, req gen.TenancyUpdateAllocationKeyRequestObject) (gen.TenancyUpdateAllocationKeyResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	v, err := ifMatchVersion(req.Params.IfMatch)
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
	k, err := t.UpdateAllocationKey(ctx, req.OrgId, req.KeyId, v, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AllocationKey](k)
	return gen.TenancyUpdateAllocationKey200JSONResponse{Body: out, Headers: gen.TenancyUpdateAllocationKey200ResponseHeaders{ETag: ptr(etag(k.Version))}}, err
}

func (s *apiServer) TenancySetAllocationKeyValues(ctx context.Context, req gen.TenancySetAllocationKeyValuesRequestObject) (gen.TenancySetAllocationKeyValuesResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	if err := hasBody(req.Body == nil); err != nil {
		return nil, err
	}
	vals := make([]tenancy.KeyValue, 0, len(req.Body.Values))
	for _, v := range req.Body.Values {
		vals = append(vals, tenancy.KeyValue{UnitID: v.UnitId, Value: v.Value})
	}
	ver, err := ifMatchVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	k, err := t.SetAllocationKeyValues(ctx, req.OrgId, req.KeyId, ver, vals)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.AllocationKeyDetail](k)
	return gen.TenancySetAllocationKeyValues200JSONResponse(out), err
}

func (s *apiServer) TenancyListVehicles(ctx context.Context, req gen.TenancyListVehiclesRequestObject) (gen.TenancyListVehiclesResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	q := ""
	if req.Params.Q != nil {
		q = *req.Params.Q
	}
	items, next, err := t.ListVehicles(ctx, req.OrgId, req.SiteId, req.Params.UnitId, q, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.VehicleList](items, next)
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
	d, err := docOf(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.CreateVehicle(ctx, req.OrgId, req.UnitId, d)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Vehicle](v)
	return gen.TenancyCreateVehicle201JSONResponse(out), err
}

func (s *apiServer) TenancyDeleteVehicle(ctx context.Context, req gen.TenancyDeleteVehicleRequestObject) (gen.TenancyDeleteVehicleResponseObject, error) {
	t, err := s.tn()
	if err != nil {
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
	b, err := t.ExportVehiclesCSV(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	return gen.TenancyExportVehicles200TextcsvResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
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
	ir := people.ImportRequest{Filename: up.filename, Data: up.data}
	switch up.fields["dry_run"] {
	case "", "false", "0":
	case "true", "1":
		ir.DryRun = true
	default:
		return nil, problem.Validation(problem.Field{Field: "dry_run", Code: "invalid_value"})
	}
	if m := up.fields["mapping"]; m != "" {
		if err := json.Unmarshal([]byte(m), &ir.Mapping); err != nil {
			return nil, problem.Validation(problem.Field{Field: "mapping", Code: "invalid_format"})
		}
	}
	rep, err := p.ImportUnits(ctx, req.OrgId, req.SiteId, ir)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.ImportReport](rep)
	return gen.TenancyImportUnits200JSONResponse(out), err
}

func (s *apiServer) TenancyExportSite(ctx context.Context, req gen.TenancyExportSiteRequestObject) (gen.TenancyExportSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	e, err := t.ExportSite(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.SiteExport](e)
	return gen.TenancyExportSite201JSONResponse(out), err
}

func (s *apiServer) TenancyListSiteExports(ctx context.Context, req gen.TenancyListSiteExportsRequestObject) (gen.TenancyListSiteExportsResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	items, next, err := t.ListSiteExports(ctx, req.OrgId, pageOf(req.Params.Limit, req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out, err := listOf[gen.SiteExportList](items, next)
	return gen.TenancyListSiteExports200JSONResponse(out), err
}

func (s *apiServer) TenancyDownloadSiteExport(ctx context.Context, req gen.TenancyDownloadSiteExportRequestObject) (gen.TenancyDownloadSiteExportResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	data, _, err := t.ExportArchive(ctx, req.OrgId, req.ExportId, false)
	if err != nil {
		return nil, err
	}
	return gen.TenancyDownloadSiteExport200ApplicationoctetStreamResponse{Body: bytes.NewReader(data), ContentLength: int64(len(data))}, nil
}

func (s *apiServer) TenancyImportSiteArchive(ctx context.Context, req gen.TenancyImportSiteArchiveRequestObject) (gen.TenancyImportSiteArchiveResponseObject, error) {
	t, err := s.tn()
	if err != nil {
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
	out, err := conv[gen.SiteExport](e)
	return gen.TenancyImportSiteArchive201JSONResponse(out), err
}

func (s *apiServer) TenancyOffboardSite(ctx context.Context, req gen.TenancyOffboardSiteRequestObject) (gen.TenancyOffboardSiteResponseObject, error) {
	t, err := s.tn()
	if err != nil {
		return nil, err
	}
	site, err := t.OffboardSite(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out, err := conv[gen.Site](site)
	return gen.TenancyOffboardSite200JSONResponse(out), err
}

func (s *apiServer) InstanceGetCodes(_ context.Context, req gen.InstanceGetCodesRequestObject) (gen.InstanceGetCodesResponseObject, error) {
	locale := ""
	if req.Params.Locale != nil {
		locale = *req.Params.Locale
	}
	l, sections := codes.Labels(locale)
	out, err := conv[gen.Codes](map[string]any{"locale": l, "sections": sections})
	return gen.InstanceGetCodes200JSONResponse(out), err
}
