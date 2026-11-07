package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi/gen"
	"github.com/openbekci/openbekci/server/internal/problem"
)

var (
	errNoTenancy = errors.New("tenancy service not configured")
	errNoAudit   = errors.New("audit service not configured")
)

func orgDTO(o tenancy.Org) gen.Organization {
	return gen.Organization{
		Id: o.ID, Slug: o.Slug, Name: o.Name, Status: gen.OrganizationStatus(o.Status),
		CountryCode: o.CountryCode, DefaultLocale: o.DefaultLocale, Timezone: o.Timezone,
		Version: o.Version, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
}

func etag(version int) string { return `"` + strconv.Itoa(version) + `"` }

func (s *apiServer) OrgGet(ctx context.Context, req gen.OrgGetRequestObject) (gen.OrgGetResponseObject, error) {
	if s.tenancy == nil {
		return nil, problem.Internal(errNoTenancy)
	}
	o, err := s.tenancy.Get(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	return gen.OrgGet200JSONResponse{Body: orgDTO(o), Headers: gen.OrgGet200ResponseHeaders{ETag: ptr(etag(o.Version))}}, nil
}

func (s *apiServer) OrgUpdate(ctx context.Context, req gen.OrgUpdateRequestObject) (gen.OrgUpdateResponseObject, error) {
	if s.tenancy == nil {
		return nil, problem.Internal(errNoTenancy)
	}
	if req.Params.IfMatch == nil {
		return nil, problem.Validation(problem.Field{Field: "If-Match", Code: "required"})
	}
	version, err := strconv.Atoi(strings.Trim(*req.Params.IfMatch, `"`))
	if err != nil {
		return nil, problem.Validation(problem.Field{Field: "If-Match", Code: "invalid_format"})
	}
	if req.Body == nil {
		return nil, problem.BadRequest("the request body is missing")
	}
	b := req.Body
	o, err := s.tenancy.Update(ctx, req.OrgId, version, tenancy.Patch{
		Name: b.Name, CountryCode: b.CountryCode, DefaultLocale: b.DefaultLocale, Timezone: b.Timezone,
	})
	if err != nil {
		return nil, err
	}
	return gen.OrgUpdate200JSONResponse{Body: orgDTO(o), Headers: gen.OrgUpdate200ResponseHeaders{ETag: ptr(etag(o.Version))}}, nil
}

func ptr[T any](v T) *T { return &v }

func (s *apiServer) AuditList(ctx context.Context, req gen.AuditListRequestObject) (gen.AuditListResponseObject, error) {
	if s.audit == nil {
		return nil, problem.Internal(errNoAudit)
	}
	p := req.Params
	f := audit.Filter{Actor: p.Actor, From: p.From, To: p.To}
	if p.Action != nil {
		f.Action = *p.Action
	}
	if p.TargetType != nil {
		f.TargetType = *p.TargetType
	}
	var limit int
	if p.Limit != nil {
		limit = *p.Limit
	}
	var cursor string
	if p.Cursor != nil {
		cursor = *p.Cursor
	}
	events, next, err := s.audit.List(ctx, req.OrgId, f, cursor, limit)
	if err != nil {
		return nil, err
	}
	items := make([]gen.AuditEvent, 0, len(events))
	for _, e := range events {
		items = append(items, auditDTO(e))
	}
	out := gen.AuditEventList{Items: items, NextCursor: nullable.NewNullNullable[string]()}
	if next != "" {
		out.NextCursor = nullable.NewNullableWithValue(next)
	}
	return gen.AuditList200JSONResponse(out), nil
}

func (s *apiServer) PlatformListAudit(ctx context.Context, req gen.PlatformListAuditRequestObject) (gen.PlatformListAuditResponseObject, error) {
	if s.audit == nil {
		return nil, problem.Internal(errNoAudit)
	}
	p := req.Params
	f := audit.PlatformFilter{Actor: p.Actor, From: p.From, To: p.To}
	if p.Action != nil {
		f.Action = *p.Action
	}
	var limit int
	if p.Limit != nil {
		limit = *p.Limit
	}
	var cursor string
	if p.Cursor != nil {
		cursor = *p.Cursor
	}
	events, next, err := s.audit.ListPlatform(ctx, f, cursor, limit)
	if err != nil {
		return nil, err
	}
	items := make([]gen.AuditEvent, 0, len(events))
	for _, e := range events {
		items = append(items, auditDTO(e))
	}
	out := gen.AuditEventList{Items: items, NextCursor: nullable.NewNullNullable[string]()}
	if next != "" {
		out.NextCursor = nullable.NewNullableWithValue(next)
	}
	return gen.PlatformListAudit200JSONResponse(out), nil
}

func auditDTO(e audit.Stored) gen.AuditEvent {
	diff := map[string]gen.AuditChange{}
	var raw map[string]audit.Change
	if json.Unmarshal(e.Diff, &raw) == nil {
		for k, c := range raw {
			ch := gen.AuditChange{}
			if len(c.Before) > 0 {
				_ = json.Unmarshal(c.Before, &ch.Before)
			}
			if len(c.After) > 0 {
				_ = json.Unmarshal(c.After, &ch.After)
			}
			if c.HashBefore != "" {
				ch.HashBefore = ptr(c.HashBefore)
			}
			if c.HashAfter != "" {
				ch.HashAfter = ptr(c.HashAfter)
			}
			diff[k] = ch
		}
	}
	return gen.AuditEvent{
		Id: e.ID, At: e.At, ActorKind: gen.AuditEventActorKind(e.ActorKind), Action: e.Action,
		TargetType: e.TargetType, TargetId: e.TargetID, Diff: diff,
		ActorUserId: nullUUID(e.ActorUserID), SiteId: nullUUID(e.SiteID), Ip: nullStr(e.IP), RequestId: nullStr(e.RequestID),
	}
}

func nullUUID(id *uuid.UUID) nullable.Nullable[uuid.UUID] {
	if id == nil {
		return nullable.NewNullNullable[uuid.UUID]()
	}
	return nullable.NewNullableWithValue(*id)
}
