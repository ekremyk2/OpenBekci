package governance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

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

// Presets returns the built-in presets. The Turkish one is the default of
// every site; other countries arrive with their packs.
func Presets() []Preset {
	return []Preset{{ID: PresetTR634, CountryCode: "TR", Name: "Turkiye, Kat Mulkiyeti Kanunu (634)", Settings: TurkishPreset(), Notes: TurkishPresetNotes()}}
}

// loadSettings reads a site's settings; a site without a row follows the
// Turkish preset at version 1. Stored JSON missing a newer field takes the
// preset's value for it.
func loadSettings(ctx context.Context, tx pgx.Tx, orgID, siteID uuid.UUID, forUpdate bool) (SettingsView, error) {
	q := `SELECT preset, settings, version FROM governance_settings WHERE org_id = $1 AND site_id = $2`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	v := SettingsView{SiteID: siteID, Settings: TurkishPreset(), PresetNotes: TurkishPresetNotes()}
	var raw []byte
	err := tx.QueryRow(ctx, q, orgID, siteID).Scan(&v.Preset, &raw, &v.Version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		v.Preset, v.Version = ptr(PresetTR634), 1
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
// expectedVersion (If-Match). Law 634 numbers are defaults, not limits: the
// site may deviate where its yonetim plani allows, so only sanity is checked.
// It needs meetings.manage.
func (s *Service) PutSettings(ctx context.Context, orgID, siteID uuid.UUID, expectedVersion int, in Settings) (SettingsView, error) {
	if err := authz.RequireAnywhere(ctx, PermMeetingsManage); err != nil {
		return SettingsView{}, err
	}
	if in.VoteWeight == "" {
		in.VoteWeight = VoteWeightPerUnit
	}
	if in.OrdinaryAssemblyMonth == 0 {
		in.OrdinaryAssemblyMonth = TurkishPreset().OrdinaryAssemblyMonth
	}
	if in.DissentWindowDays == 0 {
		in.DissentWindowDays = TurkishPreset().DissentWindowDays
	}
	if in.DefaultTermMonths == 0 {
		in.DefaultTermMonths = TurkishPreset().DefaultTermMonths
	}
	if perr := in.Validate(); perr != nil {
		return SettingsView{}, perr
	}
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
		var preset *string
		for _, p := range Presets() {
			if reflect.DeepEqual(p.Settings, in) {
				preset = ptr(p.ID)
			}
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
		diff, err := audit.Diff("governance_settings", settingsAudit(before.Settings), settingsAudit(in))
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "governance.settings_update", TargetType: "governance_settings", TargetID: siteID, Diff: diff})
	})
	return out, dbErr(err, "governance settings")
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
