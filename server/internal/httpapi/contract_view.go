package httpapi

import (
	"context"
	"encoding/json"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// This file maps the service types to the draft contract's shapes (api/src/components/tenancy.yaml
// and people.yaml). The services keep their own types; the handlers translate.

func orElse(v any, d any) any {
	if v == nil {
		return d
	}
	return v
}

func nstr(m map[string]any, k string) any {
	if s, ok := m[k].(string); ok && s != "" {
		return s
	}
	return nil
}

func addressView(a map[string]any) map[string]any {
	line1, _ := a["line1"].(string)
	cc, _ := a["country_code"].(string)
	return map[string]any{
		"line1": line1, "line2": nstr(a, "line2"), "district": nstr(a, "district"), "city": nstr(a, "city"),
		"postal_code": nstr(a, "postal_code"), "country_code": cc,
	}
}

func siteView(org uuid.UUID, s tenancy.Site) map[string]any {
	return map[string]any{
		"id": s.ID, "org_id": org, "name": s.Name, "address": addressView(s.Address), "timezone": s.Timezone, "currency": s.Currency,
		"fiscal_year_start_month": s.FiscalYearStartMonth, "lock_date": s.LockDate, "status": s.Status,
		"created_at": s.CreatedAt, "updated_at": s.UpdatedAt, "version": s.Version,
	}
}

// settingsRenames maps the draft's setting names to the keys the tenancy module stores.
var settingsRenames = map[string]string{
	"owner_financial_transparency": tenancy.SettingTransparencyToOwners,
	"parking_storage_default":      tenancy.SettingAppurtenanceDefault,
}

func settingKey(draft string) string {
	if k, ok := settingsRenames[draft]; ok {
		return k
	}
	return draft
}

func settingsView(m map[string]any, version int) map[string]any {
	get := func(draft string, def any) any {
		if v, ok := m[settingKey(draft)]; ok && v != nil {
			return v
		}
		return def
	}
	access := map[string]any{"offline_grace_hours": 72, "agent_silent_minutes": 5, "plate_match": "exact", "maintenance_hour": nil}
	if a, ok := m["access"].(map[string]any); ok {
		for k, v := range a {
			access[k] = v
		}
	}
	return map[string]any{
		"tenants_are_portal_users":     get("tenants_are_portal_users", true),
		"directory_enabled":            get("directory_enabled", false),
		"owner_financial_transparency": get("owner_financial_transparency", false),
		"parking_storage_default":      get("parking_storage_default", tenancy.Appurtenance),
		"bills_enabled":                get("bills_enabled", false),
		"announcement_publishers":      get("announcement_publishers", "manager_only"),
		"ticket_archive_months":        get("ticket_archive_months", nil),
		"visitor_log_retention_days":   get("visitor_log_retention_days", 90),
		"legal_preset":                 get("legal_preset", nil),
		"access":                       access,
		"extra":                        get("extra", nil),
		"version":                      version,
	}
}

// settingsDoc turns a SiteSettingsUpdate body into the site doc that merges the stored settings.
func settingsDoc(body tenancy.Doc) (tenancy.Doc, error) {
	set := map[string]json.RawMessage{}
	for k, v := range body {
		set[settingKey(k)] = v
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return nil, problem.BadRequest("the request body could not be read")
	}
	return tenancy.Doc{"settings": raw}, nil
}

func bankView(m map[string]any, version int) map[string]any {
	return map[string]any{
		"iban": orElse(nstr(m, "iban"), ""), "holder": orElse(nstr(m, "holder"), ""), "bank_name": nstr(m, "bank_name"),
		"reference_hint": nstr(m, "reference_hint"), "suggested_reference": nil, "version": version,
	}
}

func blockView(org uuid.UUID, b tenancy.Block) map[string]any {
	return map[string]any{
		"id": b.ID, "org_id": org, "site_id": b.SiteID, "name": b.Name, "is_default": b.Hidden,
		"created_at": b.CreatedAt, "updated_at": b.UpdatedAt, "version": b.Version,
	}
}

func unitView(org uuid.UUID, u tenancy.Unit) map[string]any {
	po := map[string]string{}
	for k, v := range u.PayerOverrides {
		po[k] = v
	}
	return map[string]any{
		"id": u.ID, "org_id": org, "site_id": u.SiteID, "block_id": u.BlockID, "door_number": u.DoorNumber, "legal_ref": u.LegalRef,
		"type": u.Type, "area_m2": u.AreaM2, "parent_unit_id": u.ParentUnitID, "status": u.Status, "vacant": u.Vacant,
		"occupancy":  map[string]any{"owners": u.OccOwners, "tenants": u.OccTenants, "household": u.OccHousehold},
		"created_at": u.CreatedAt, "updated_at": u.UpdatedAt, "version": u.Version, "payer_overrides": po,
	}
}

func keyView(org uuid.UUID, k tenancy.AllocationKey) map[string]any {
	return map[string]any{
		"id": k.ID, "org_id": org, "site_id": k.SiteID, "code": k.Code, "name": k.Name, "kind": k.Kind, "is_system": k.IsDefault,
		"created_at": k.CreatedAt, "updated_at": k.UpdatedAt, "version": k.Version,
	}
}

func ratFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

func vehicleView(org uuid.UUID, v tenancy.Vehicle) map[string]any {
	return map[string]any{
		"id": v.ID, "org_id": org, "site_id": v.SiteID, "unit_id": v.UnitID, "plate": tenancy.FormatPlate(v.Plate), "plate_normalized": v.Plate,
		"description": v.Label, "archived": false, "warning": nil, "created_at": v.CreatedAt, "updated_at": v.CreatedAt, "version": v.Version,
	}
}

func transferStatus(s string) string {
	if s == "done" {
		return "succeeded"
	}
	return s
}

func transferView(org uuid.UUID, e tenancy.SiteExport) map[string]any {
	return map[string]any{
		"id": e.ID, "org_id": org, "direction": e.Direction, "site_id": e.SiteID, "archive_file_id": nil,
		"source_org_id": e.SourceOrgID, "target_org_id": e.TargetOrgID, "status": transferStatus(e.Status), "job_id": nil,
		"at": e.At, "created_at": e.At, "updated_at": e.At,
	}
}

// importReportView maps the importer's report to the draft's ImportReport.
func importReportView(r people.ImportReport) map[string]any {
	const maxRows = 1000
	rows := make([]map[string]any, 0, len(r.Rows))
	ok, warn, bad := 0, 0, 0
	for _, row := range r.Rows {
		switch row.Status {
		case "error":
			bad++
		case "warning":
			warn++
		default:
			ok++
		}
		errs := make([]map[string]any, 0, len(row.Messages))
		for _, m := range row.Messages {
			p := map[string]any{"level": m.Level}
			for k, v := range m.Params {
				p[k] = v
			}
			var field any
			if m.Column != "" {
				field = m.Column
			}
			errs = append(errs, map[string]any{"field": field, "code": m.Code, "message": nil, "params": p})
		}
		rows = append(rows, map[string]any{"row": row.Row, "status": row.Status, "errors": errs,
			"data": map[string]any{"door_number": row.DoorNumber, "action": row.Action}})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["status"] == "error" && rows[j]["status"] != "error" })
	truncated := len(rows) > maxRows
	if truncated {
		rows = rows[:maxRows]
	}
	return map[string]any{"dry_run": r.DryRun, "total_rows": len(r.Rows), "ok_rows": ok, "warning_rows": warn, "error_rows": bad,
		"rows": rows, "rows_truncated": truncated, "job_id": nil}
}

// ---- people ----

func nilIfEmpty(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func contactView(email, phone *string, extra map[string]any) map[string]any {
	c := map[string]any{"email": nilIfEmpty(email), "phone": nilIfEmpty(phone), "preferred_channel": nil, "address": nil}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

// personView is the draft Person. consents are the person's directory choices per site.
func personView(org uuid.UUID, p people.Person, consents []map[string]any, joinStatus string, masked bool) map[string]any {
	if joinStatus == "" {
		joinStatus = "no_account"
		if p.HasAccount {
			joinStatus = "joined"
		}
	}
	var nid any
	if masked && p.NationalID != nil {
		nid = *p.NationalID
	}
	var country any
	if masked {
		country = nilIfEmpty(p.NationalIDCountry)
	}
	if consents == nil {
		consents = []map[string]any{}
	}
	return map[string]any{
		"id": p.ID, "org_id": org, "user_id": p.UserID, "display_name": p.DisplayName, "contact": contactView(p.Email, p.Phone, nil),
		"national_id_masked": nid, "national_id_country": country, "gate_contact_phone": nilIfEmpty(p.GateContactPhone),
		"notes": nilIfEmpty(p.Notes), "join_status": joinStatus, "directory_consent": consents,
		"created_at": p.CreatedAt, "updated_at": p.UpdatedAt, "version": p.Version,
	}
}

// personDoc maps a PersonCreate or PersonUpdate body to the person doc.
func personDoc(body tenancy.Doc) (tenancy.Doc, error) {
	out := tenancy.Doc{}
	for k, v := range body {
		switch k {
		case "contact":
			var c map[string]json.RawMessage
			if err := json.Unmarshal(v, &c); err != nil {
				return nil, problem.Validation(problem.Field{Field: "contact", Code: "invalid_type"})
			}
			for ck, cv := range c {
				switch ck {
				case "email", "phone":
					out[ck] = cv
				case "preferred_channel", "address":
					// stored with the draft shape only once the person row has columns for them
				}
			}
		default:
			out[k] = v
		}
	}
	return out, nil
}

func occupancyView(org uuid.UUID, o people.Occupancy) map[string]any {
	return map[string]any{
		"id": o.ID, "org_id": org, "person_id": o.PersonID, "person_display_name": o.PersonName, "unit_id": o.UnitID,
		"unit_door_number": o.DoorNumber, "kind": o.Kind, "side": o.Side, "share_pct": o.SharePct,
		"is_correspondent": o.IsCorrespondent, "may_see_money": o.MayMoney, "valid_from": o.ValidFrom, "valid_to": o.ValidTo,
		"created_at": o.CreatedAt, "updated_at": o.UpdatedAt, "version": o.Version,
	}
}

func dateOf(t time.Time) string { return t.Format("2006-01-02") }

func membershipView(org uuid.UUID, m people.Membership) map[string]any {
	var to any
	if m.ValidTo != nil {
		to = dateOf(*m.ValidTo)
	}
	return map[string]any{
		"id": m.ID, "org_id": org, "person_id": m.PersonID, "person_display_name": m.PersonName, "role": m.Role,
		"scope_type": m.ScopeType, "scope_id": m.ScopeID, "valid_from": dateOf(m.ValidFrom), "valid_to": to,
		"decision_id": m.DecisionID, "site_owned": m.SiteOwned, "created_at": m.CreatedAt, "updated_at": m.CreatedAt, "version": m.Version,
	}
}

func maskContact(v *string) any {
	if v == nil || *v == "" {
		return "***"
	}
	s := *v
	if i := strings.IndexByte(s, '@'); i > 0 {
		return s[:1] + "***" + s[i:]
	}
	if len(s) > 4 {
		return strings.Repeat("*", len(s)-4) + s[len(s)-4:]
	}
	return "***"
}

func invitationView(org uuid.UUID, i people.Invitation, invitedBy *uuid.UUID) map[string]any {
	dest := i.Phone
	if i.Channel == "email" {
		dest = i.Email
	}
	exp := any(nil)
	if i.ExpiresAt != nil {
		exp = *i.ExpiresAt
	}
	acc := any(nil)
	if i.AcceptedAt != nil {
		acc = *i.AcceptedAt
	}
	ch := i.Channel
	if ch != "sms" && ch != "email" {
		ch = "sms"
	}
	return map[string]any{
		"id": i.ID, "org_id": org, "kind": i.Kind, "site_id": i.SiteID, "person_id": i.PersonID, "unit_id": i.UnitID, "channel": ch,
		"destination": maskContact(dest), "status": i.Status, "expires_at": orElse(exp, i.CreatedAt), "accepted_at": acc, "invited_by": invitedBy,
		"created_at": i.CreatedAt, "updated_at": i.CreatedAt,
	}
}

func claimView(org uuid.UUID, c people.UnitClaim, userID *uuid.UUID) map[string]any {
	var decidedAt any
	if c.DecidedAt != nil {
		decidedAt = *c.DecidedAt
	}
	var uid any = uuid.Nil
	if userID != nil {
		uid = *userID
	}
	return map[string]any{
		"id": c.ID, "org_id": org, "user_id": uid, "display_name": c.PersonName, "unit_id": c.UnitID, "unit_door_number": c.DoorNumber,
		"claimed_role": c.Kind, "status": c.Status, "decided_by": nil, "decided_at": decidedAt, "decision_note": c.DecisionNote,
		"note": c.Message, "created_at": c.CreatedAt, "updated_at": orElse(decidedAt, c.CreatedAt),
	}
}

func vendorView(org uuid.UUID, v people.Vendor) map[string]any {
	c := map[string]any{"email": nstr(v.Contact, "email"), "phone": nstr(v.Contact, "phone"), "preferred_channel": nil, "address": nil}
	return map[string]any{
		"id": v.ID, "org_id": org, "name": v.Name, "tax_id": v.TaxID, "contact": c, "default_expense_category_id": v.DefaultExpenseCategoryID,
		"archived": false, "created_at": v.CreatedAt, "updated_at": v.UpdatedAt, "version": v.Version,
	}
}

// moveOutView is the draft MoveOut; money amounts come from the money module (zero until then).
func moveOutView(org uuid.UUID, m people.MoveOut, currency string) map[string]any {
	amount := int64(0)
	if m.CarriedAmountMinor != nil {
		amount = *m.CarriedAmountMinor
	}
	if m.CarriedCurrency != nil && *m.CarriedCurrency != "" {
		currency = *m.CarriedCurrency
	}
	money := func(a int64) map[string]any { return map[string]any{"amount": a, "currency": currency} }
	var by any = uuid.Nil
	if m.DecidedBy != nil {
		by = *m.DecidedBy
	}
	var transferred any
	return map[string]any{
		"id": m.ID, "org_id": org, "occupancy_id": m.OccupancyID, "as_of": m.AsOf, "closing_statement_file_id": m.ClosingStatementFileID,
		"certificate_file_id": m.CertificateFileID, "open_charges": m.OpenCharges, "carried": []any{}, "disposition_reason": m.Notes,
		"transferred_to_person_id": transferred, "decided_by": by,
		"balance": map[string]any{"occupancy_id": m.OccupancyID, "as_of": m.AsOf, "principal": money(amount), "late_fee": money(0), "credit": money(0),
			"total": money(amount), "default_open_charges": m.OpenCharges, "carried": []any{}},
		"created_at": m.CreatedAt, "updated_at": m.CreatedAt,
	}
}

// pagedSlice cuts items (already ordered) at the cursor, the id of the last item of the previous page.
func pagedSlice[T any](items []T, idOf func(T) uuid.UUID, limit int, cursor string) ([]T, string, error) {
	p := tenancy.PageParams{Cursor: cursor, Limit: limit}
	lim, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	start := 0
	if after != uuid.Nil {
		for i, it := range items {
			if idOf(it) == after {
				start = i + 1
				break
			}
		}
	}
	rest := items[start:]
	if len(rest) <= lim {
		return rest, "", nil
	}
	rest = rest[:lim]
	return rest, tenancy.CursorOf(idOf(rest[len(rest)-1])), nil
}

// reauthFor checks the permission at organization scope, then the recent re-authentication
// the draft requires for site export, import and offboarding.
func reauthFor(ctx context.Context, org uuid.UUID, perm authz.Permission) error {
	if err := authz.Require(ctx, perm, authz.Scope{OrgID: org}); err != nil {
		return err
	}
	return authz.PrincipalFromContext(ctx).RequireReauth(time.Now())
}
