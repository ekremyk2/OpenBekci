package packs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/ledger"
)

// settingLegalPreset is the site setting that names the pack whose presets fill the site.
const settingLegalPreset = "legal_preset"

// presetValues evaluates the pack's presets for a site. A preset that fails or
// panics is skipped and logged; it never fails site creation or enablement.
// When a pack registers several presets of one kind the first one wins.
func (h *Host) presetValues(ctx context.Context, p *Pack, info sdk.SiteInfo) map[string]any {
	out := map[string]any{}
	set := func(key, what string, fn func() (any, error)) {
		if _, done := out[key]; done {
			return
		}
		var v any
		err := safe(p.Manifest.ID, what, func() (err error) { v, err = fn(); return err })
		if err != nil {
			h.log.WarnContext(ctx, "pack preset skipped", "pack", p.Manifest.ID, "preset", what, "error", err)
			return
		}
		raw, err := json.Marshal(v)
		if err != nil {
			h.log.WarnContext(ctx, "pack preset skipped", "pack", p.Manifest.ID, "preset", what, "error", err)
			return
		}
		var generic any
		_ = json.Unmarshal(raw, &generic)
		out[key] = generic
	}
	for _, x := range p.C.Quorum {
		set(sdk.SettingQuorum, "quorum "+x.ID(), func() (any, error) { return x.Quorum(ctx, info) })
	}
	for _, x := range p.C.Thresholds {
		set(sdk.SettingThresholds, "thresholds "+x.ID(), func() (any, error) { return x.Thresholds(ctx, info) })
	}
	for _, x := range p.C.VoteWeight {
		set(sdk.SettingVoteWeight, "vote weight "+x.ID(), func() (any, error) { return x.VoteWeight(ctx, info) })
	}
	for _, x := range p.C.Schedule {
		set(sdk.SettingSchedule, "schedule "+x.ID(), func() (any, error) { return x.Schedule(ctx, info) })
	}
	for _, x := range p.C.Transfer {
		set(sdk.SettingTransferRule, "transfer rule "+x.ID(), func() (any, error) { return x.Transfer(ctx, info) })
	}
	for _, x := range p.C.LateFee {
		set(sdk.SettingLateFee, "late fee "+x.ID(), func() (any, error) { return x.LateFee(ctx, info) })
	}
	if len(out) > 0 {
		out[settingLegalPreset] = p.Manifest.ID
	}
	return out
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// applyPresets fills the unchanged settings of one site from the pack's
// presets: a key is written when the site has no value for it, or when the
// value is still the one this pack wrote earlier ("pack default"). A key the
// site edited, or another pack filled, is left alone. Which keys are pack
// defaults is recorded in site_pack_default.
func (h *Host) applyPresets(ctx context.Context, tx pgx.Tx, org, site uuid.UUID, p *Pack) error {
	if !p.C.HasPresets() {
		return nil
	}
	var info sdk.SiteInfo
	var settingsRaw []byte
	var status string
	err := tx.QueryRow(ctx, `SELECT s.timezone, s.currency, s.settings, s.status, o.country_code FROM site s JOIN organization o ON o.id = s.org_id
		WHERE s.org_id = $1 AND s.id = $2 FOR UPDATE OF s`, org, site).Scan(&info.Timezone, &info.Currency, &settingsRaw, &status, &info.CountryCode)
	if err != nil {
		return err
	}
	if status != "active" {
		return nil
	}
	info.ID, info.Org = sdk.ID(site.String()), sdk.ID(org.String())
	values := h.presetValues(sdk.WithOrg(ctx, info.Org), p, info)
	if len(values) == 0 {
		return nil
	}
	settings := map[string]any{}
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		return err
	}
	type mark struct {
		pack  string
		value any
	}
	marks := map[string]mark{}
	rows, err := tx.Query(ctx, `SELECT key, pack_id, value FROM site_pack_default WHERE org_id = $1 AND site_id = $2`, org, site)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k, pk string
		var raw []byte
		if err := rows.Scan(&k, &pk, &raw); err != nil {
			rows.Close()
			return err
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		marks[k] = mark{pk, v}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	changed := map[string]any{}
	for _, k := range keys {
		cur, has := settings[k]
		m, marked := marks[k]
		if has && (!marked || m.pack != p.Manifest.ID || !jsonEqual(cur, m.value)) {
			continue
		}
		if has && jsonEqual(cur, values[k]) && marked {
			continue // already up to date
		}
		settings[k] = values[k]
		changed[k] = values[k]
		raw, _ := json.Marshal(values[k])
		if _, err := tx.Exec(ctx, `INSERT INTO site_pack_default (org_id, site_id, key, pack_id, value) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (org_id, site_id, key) DO UPDATE SET pack_id = EXCLUDED.pack_id, value = EXCLUDED.value, applied_at = now()`, org, site, k, p.Manifest.ID, raw); err != nil {
			return err
		}
	}
	if len(changed) == 0 {
		return nil
	}
	raw, _ := json.Marshal(settings)
	if _, err := tx.Exec(ctx, `UPDATE site SET settings = $3, version = version + 1, updated_at = now() WHERE org_id = $1 AND id = $2`, org, site, raw); err != nil {
		return err
	}
	if lf, ok := changed[sdk.SettingLateFee].(map[string]any); ok {
		if err := lateFeeDefault(ctx, tx, org, site, lf); err != nil {
			return err
		}
	}
	diff, _ := audit.Diff("site_settings", nil, changed)
	return audit.Record(ctx, tx, audit.Event{OrgID: org, SiteID: &site, Action: "pack.presets_applied", TargetType: "site", TargetID: site, Diff: diff})
}

// lateFeeDefault makes the site's automatic default late fee row (the one a
// site has until it sets its own) follow the preset's default rate and grace
// days. The schedule is append-only, but the default row may be replaced: it is
// deleted and inserted again with the same start date. A site that already set
// its own policy has no default row and is left alone.
func lateFeeDefault(ctx context.Context, tx pgx.Tx, org, site uuid.UUID, lf map[string]any) error {
	rate, ok := lf["default_monthly_rate_bps"].(float64)
	if !ok {
		return nil
	}
	grace, _ := lf["grace_days"].(float64)
	var from string
	var curRate, curGrace int
	err := tx.QueryRow(ctx, `SELECT from_on::text, rate_bps_per_month, grace_days FROM late_fee_policy WHERE org_id = $1 AND site_id = $2 AND is_default`, org, site).Scan(&from, &curRate, &curGrace)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if curRate == int(rate) && curGrace == int(grace) {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM late_fee_policy WHERE org_id = $1 AND site_id = $2 AND is_default`, org, site); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO late_fee_policy (id, org_id, site_id, from_on, rate_bps_per_month, grace_days, is_default) VALUES ($1, $2, $3, $4::date, $5, $6, true)`,
		uuid.New(), org, site, from, int(rate), int(grace))
	return err
}

// renameChart renames the site's system accounts by code from the pack's
// ChartOfAccounts in the organization's default locale (packs localize names,
// never structure).
func (h *Host) renameChart(ctx context.Context, tx pgx.Tx, org, site uuid.UUID, p *Pack) error {
	if len(p.C.Charts) == 0 {
		return nil
	}
	// the chart is created on first money use otherwise, after this hook ran: create it now so the names apply
	if _, err := ledger.EnsureChart(ctx, tx, org, site); err != nil {
		return err
	}
	var locale string
	if err := tx.QueryRow(ctx, `SELECT default_locale FROM organization WHERE id = $1`, org).Scan(&locale); err != nil {
		return err
	}
	for _, c := range p.C.Charts {
		var names map[string]string
		if err := safe(p.Manifest.ID, "chart "+c.ID(), func() error { names = c.Names(locale); return nil }); err != nil {
			h.log.WarnContext(ctx, "pack chart names skipped", "pack", p.Manifest.ID, "error", err)
			continue
		}
		for code, name := range names {
			if name == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE account SET name = $4, updated_at = now(), version = version + 1
				WHERE org_id = $1 AND site_id = $2 AND code = $3 AND is_system AND name <> $4`, org, site, code, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyToOrgSites applies presets and chart names of an enabled pack to every active site of the organization.
func (h *Host) applyToOrgSites(ctx context.Context, tx pgx.Tx, org uuid.UUID, p *Pack) error {
	rows, err := tx.Query(ctx, `SELECT id FROM site WHERE org_id = $1 AND status = 'active' ORDER BY id`, org)
	if err != nil {
		return err
	}
	var sites []uuid.UUID
	for rows.Next() {
		var s uuid.UUID
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		sites = append(sites, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range sites {
		if err := h.applyPresets(ctx, tx, org, s, p); err != nil {
			return fmt.Errorf("apply presets of pack %s to site %s: %w", p.Manifest.ID, s, err)
		}
		if err := h.renameChart(ctx, tx, org, s, p); err != nil {
			return err
		}
	}
	return nil
}

// SiteCreated implements tenancy.SiteHook: for a new site the presets of the
// organization's enabled packs whose countries include the organization's
// country (or that name none) are applied, the chart names are localized and
// the site.created event is delivered to subscribers.
func (h *Host) SiteCreated(ctx context.Context, tx pgx.Tx, org, site uuid.UUID) error {
	if len(h.packs) == 0 {
		return nil
	}
	var country string
	if err := tx.QueryRow(ctx, `SELECT country_code FROM organization WHERE id = $1`, org).Scan(&country); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT pack_id FROM pack_enablement WHERE org_id = $1 AND enabled ORDER BY pack_id`, org)
	if err != nil {
		return err
	}
	var on []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		on = append(on, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range on {
		p, ok := h.byID[id]
		if !ok {
			continue
		}
		if len(p.Manifest.Countries) == 0 || slices.Contains(p.Manifest.Countries, country) {
			if err := h.applyPresets(ctx, tx, org, site, p); err != nil {
				return err
			}
		}
		if err := h.renameChart(ctx, tx, org, site, p); err != nil {
			return err
		}
	}
	return h.Emit(ctx, tx, org, sdk.Event{Type: sdk.EventSiteCreated, IDs: map[string]sdk.ID{"site": sdk.ID(site.String())}})
}

// SiteDefaults returns which settings keys of a site are still pack defaults
// (the value equals what the pack wrote): key to pack id and value. The site
// settings endpoints show it as the "pack default" marker.
func (h *Host) SiteDefaults(ctx context.Context, org, site uuid.UUID) (map[string]PackDefault, error) {
	out := map[string]PackDefault{}
	err := h.cfg.DB.InTenantTx(ctx, org, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT settings FROM site WHERE org_id = $1 AND id = $2`, org, site).Scan(&raw); err != nil {
			return err
		}
		settings := map[string]any{}
		if err := json.Unmarshal(raw, &settings); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT key, pack_id, value FROM site_pack_default WHERE org_id = $1 AND site_id = $2 ORDER BY key`, org, site)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k, pk string
			var vr []byte
			if err := rows.Scan(&k, &pk, &vr); err != nil {
				return err
			}
			var v any
			_ = json.Unmarshal(vr, &v)
			if cur, ok := settings[k]; ok && jsonEqual(cur, v) {
				out[k] = PackDefault{Pack: pk, Value: v}
			}
		}
		return rows.Err()
	})
	return out, err
}

// PackDefault marks a site setting as still holding the value a pack wrote.
type PackDefault struct {
	Pack  string
	Value any
}
