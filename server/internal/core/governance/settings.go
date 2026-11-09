package governance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// SettingsView is a site's governance settings as the API shows them.
type SettingsView struct {
	SiteID      uuid.UUID    `json:"site_id"`
	Preset      *string      `json:"preset"`
	Settings    Settings     `json:"settings"`
	PresetNotes []PresetNote `json:"preset_notes"`
	Version     int          `json:"version"`
}

// Preset is a country's default rule set.
type Preset struct {
	ID          string       `json:"id"`
	CountryCode string       `json:"country_code"`
	Name        string       `json:"name"`
	Settings    Settings     `json:"settings"`
	Notes       []PresetNote `json:"notes"`
}

// Presets returns the built-in country presets: none. A country's rules arrive
// with its pack as site settings (see FromSiteSettings); a site without a pack
// starts from GenericPreset and can set the same values by hand.
func Presets() []Preset { return nil }

// siteBase returns the settings a site starts from and the id of the pack that
// filled them ("" for the generic defaults): the site settings written by a
// legal preset over GenericPreset.
func siteBase(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID) (Settings, string, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT settings FROM site WHERE org_id = $1 AND id = $2`, orgID, siteID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return GenericPreset(), "", nil
	}
	if err != nil {
		return Settings{}, "", err
	}
	site := map[string]any{}
	if err := json.Unmarshal(raw, &site); err != nil {
		return Settings{}, "", err
	}
	return FromSiteSettings(site), LegalPreset(site), nil
}

// loadSettings reads a site's settings; a site without a row follows the
// defaults its legal preset (or the generic one) gave it, at version 1. Stored
// JSON missing a newer field takes the default's value for it.
func loadSettings(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, forUpdate bool) (SettingsView, error) {
	q := `SELECT preset, settings, version FROM governance_settings WHERE org_id = $1 AND site_id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	base, pack, err := siteBase(ctx, tx, orgID, siteID)
	if err != nil {
		return SettingsView{}, err
	}
	v := SettingsView{SiteID: siteID, Settings: base, PresetNotes: GenericPresetNotes()}
	var raw []byte
	err = tx.QueryRow(ctx, q, orgID, siteID).Scan(&v.Preset, &raw, &v.Version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		v.Version = 1
		if pack != "" {
			v.Preset = ptr(pack)
		}
		return v, nil
	case err != nil:
		return v, err
	}
	if err := json.Unmarshal(raw, &v.Settings); err != nil {
		return v, err
	}
	return v, nil
}

// GetSettings returns the governance settings of a site. It needs meetings.read.
func (s *Service) GetSettings(ctx context.Context, orgID, siteID uuid.UUID) (SettingsView, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return SettingsView{}, err
	}
	var out SettingsView
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if _, err = requireSite(ctx, tx, PermMeetingsRead, siteID); err != nil {
			return err
		}
		if _, err = tenancy.LoadSite(ctx, tx, orgID, siteID, false, true); err != nil {
			return err
		}
		out, err = loadSettings(ctx, tx, orgID, siteID, false)
		return err
	})
	return out, dbErr(err, "governance settings")
}

// PutSettings replaces a site's governance settings if they are still at
// expectedVersion (If-Match). A legal preset's numbers are defaults, not limits: the
// site may deviate where its yonetim plani allows, so only sanity is checked.
// It needs meetings.manage.
func (s *Service) PutSettings(ctx context.Context, orgID, siteID uuid.UUID, expectedVersion int, in Settings, deviationReason string) (SettingsView, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return SettingsView{}, err
	}
	if in.VoteWeight == "" {
		in.VoteWeight = VoteWeightPerUnit
	}
	if in.OrdinaryAssemblyMonth == 0 {
		in.OrdinaryAssemblyMonth = GenericPreset().OrdinaryAssemblyMonth
	}
	if in.DissentWindowDays == 0 {
		in.DissentWindowDays = GenericPreset().DissentWindowDays
	}
	if in.DefaultTermMonths == 0 {
		in.DefaultTermMonths = GenericPreset().DefaultTermMonths
	}
	if perr := in.Validate(); perr != nil {
		return SettingsView{}, perr
	}
	deviationReason = strings.TrimSpace(deviationReason)
	var out SettingsView
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := requireSite(ctx, tx, PermMeetingsManage, siteID)
		if err != nil {
			return err
		}
		if _, err = tenancy.LoadSite(ctx, tx, orgID, siteID, true, false); err != nil {
			return err
		}
		before, err := loadSettings(ctx, tx, orgID, siteID, true)
		if err != nil {
			return err
		}
		if before.Version != expectedVersion {
			return errVersion
		}
		// the defaults of the site: a legal preset's values, else the generic ones
		base, pack, err := siteBase(ctx, tx, orgID, siteID)
		if err != nil {
			return err
		}
		deviations := belowLegalDefaults(in, base)
		if len(deviations) > 0 && deviationReason == "" {
			return problem.Validation(problem.Field{Field: "deviation_reason", Code: "required", Params: map[string]any{"deviations": deviations}})
		}
		var preset *string
		if pack != "" && reflect.DeepEqual(base, in) {
			preset = ptr(pack)
		}
		raw := mustJSON(in)
		if _, err := tx.Exec(ctx, `INSERT INTO governance_settings (org_id, site_id, preset, settings, version) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (org_id, site_id) DO UPDATE SET preset = $3, settings = $4, version = governance_settings.version + 1, updated_at = now()`,
			orgID, siteID, preset, raw, before.Version+1); err != nil {
			return err
		}
		if out, err = loadSettings(ctx, tx, orgID, siteID, false); err != nil {
			return err
		}
		after := settingsAudit(in)
		if len(deviations) > 0 {
			after["deviations_from_the_default"] = deviations
			after["deviation_reason"] = deviationReason
		}
		diff, err := audit.Diff("governance_settings", settingsAudit(before.Settings), after)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "governance.settings_update", TargetType: "governance_settings", TargetID: siteID, Diff: diff})
	})
	return out, dbErr(err, "governance settings")
}

var ruleStrength = map[string]int{RuleSimpleMajorityPresent: 1, RuleMajorityCountAndShare: 2, RuleFourFifths: 3, RuleUnanimity: 4}

// belowLegalDefaults lists the settings that are more permissive than the site's defaults (the legal preset of
// its pack, or the generic ones): a lower quorum or notice time, higher proxy or per-person caps,
// a weaker threshold for a topic the preset knows. The site may deviate where
// its yonetim plani allows, but a deviation needs a reason on record.
func belowLegalDefaults(in, p Settings) []string {
	var out []string
	add := func(cond bool, name string) {
		if cond {
			out = append(out, name)
		}
	}
	add(in.Quorum.FirstCallByCountPct < p.Quorum.FirstCallByCountPct, "quorum.first_call_by_count_pct")
	add(in.Quorum.FirstCallBySharePct < p.Quorum.FirstCallBySharePct, "quorum.first_call_by_share_pct")
	add(in.MaxVotesPerPersonPct > p.MaxVotesPerPersonPct+epsilon, "max_votes_per_person_pct")
	add(in.ProxyCapPct > p.ProxyCapPct, "proxy_cap_pct")
	add(p.SmallBuildingUnitLimit > 0 && p.ProxyCapSmallBuildingMax > 0 && in.ProxyCapSmallBuildingMax > p.ProxyCapSmallBuildingMax, "proxy_cap_small_building_max")
	add(in.CallMinDays < p.CallMinDays, "call_min_days")
	add(in.SecondCallMinDays < p.SecondCallMinDays, "second_call_min_days")
	for _, pt := range p.Thresholds {
		for _, it := range in.Thresholds {
			if it.Topic == pt.Topic && ruleStrength[it.Rule] < ruleStrength[pt.Rule] {
				out = append(out, "thresholds."+pt.Topic)
			}
		}
	}
	return out
}

func settingsAudit(s Settings) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(mustJSON(s), &m)
	return m
}

// ListPresets lists the presets, optionally of one country. It needs meetings.read.
func (s *Service) ListPresets(ctx context.Context, orgID uuid.UUID, countryCode string) ([]Preset, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsRead); err != nil {
		return nil, err
	}
	out := []Preset{}
	for _, p := range Presets() {
		if countryCode == "" || p.CountryCode == countryCode {
			out = append(out, p)
		}
	}
	return out, nil
}

var _ = problem.NotFound
