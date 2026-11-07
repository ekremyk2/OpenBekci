package tenancy

import (
	"testing"
)

func TestNormalizePlate(t *testing.T) {
	for in, want := range map[string]string{
		"34 ABC 123":  "34ABC123",
		"34abc123":    "34ABC123",
		" 06-ÇŞ-1234": "06CS1234",
		"34.ğ.99":     "34G99",
	} {
		got, ok := NormalizePlate(in)
		if !ok || got != want {
			t.Errorf("NormalizePlate(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "1", "34 ABC 123 456 789 000", "34*ABC"} {
		if _, ok := NormalizePlate(bad); ok {
			t.Errorf("NormalizePlate(%q) should be rejected", bad)
		}
	}
	if got := FormatPlate("34ABC123"); got != "34 ABC 123" {
		t.Errorf("FormatPlate = %q", got)
	}
	if got := FormatPlate("XY99"); got != "XY99" {
		t.Errorf("a non-Turkish plate stays as is, got %q", got)
	}
}

func TestParseWeightAndShareWarnings(t *testing.T) {
	for _, ok := range []string{"12/1000", "0.012", "350", "0,5", "1/1"} {
		if _, valid := ParseWeight(ok); !valid {
			t.Errorf("ParseWeight(%q) should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-1", "+1", "1/0", "a/b", "1.5/2", "1e3", "12 / 3"} {
		if _, valid := ParseWeight(bad); valid {
			t.Errorf("ParseWeight(%q) should be invalid", bad)
		}
	}
	// 4 units of 250/1000 add up; 3 of them do not
	full := []KeyValue{{Value: "250/1000"}, {Value: "250/1000"}, {Value: "250/1000"}, {Value: "250/1000"}}
	if w := ShareWarnings(full); len(w) != 0 {
		t.Errorf("a complete share key has no warning: %v", w)
	}
	if w := ShareWarnings(full[:3]); len(w) != 1 || w[0].Code != "share_sum_mismatch" {
		t.Errorf("an incomplete share key warns: %v", w)
	}
	if w := ShareWarnings([]KeyValue{{Value: "0.5"}, {Value: "0.5"}}); len(w) != 0 {
		t.Errorf("decimals adding to one: %v", w)
	}
	if w := ShareWarnings([]KeyValue{{Value: "0.5"}, {Value: "0.6"}}); len(w) != 1 {
		t.Errorf("decimals not adding to one: %v", w)
	}
}

func TestEffectivePayer(t *testing.T) {
	site := Site{Settings: map[string]any{SettingPayerDefaults: map[string]any{"aidat": "tenant"}}}
	u := Unit{PayerOverrides: map[string]string{"water": "owner"}}
	for chargeType, want := range map[string]string{"water": "owner", "aidat": "tenant", "other": "owner"} {
		if got := EffectivePayer(u, site, chargeType, "owner"); got != want {
			t.Errorf("EffectivePayer(%s) = %s, want %s", chargeType, got, want)
		}
	}
	u.PayerOverrides["aidat"] = "owner" // the unit's override beats the site default
	if EffectivePayer(u, site, "aidat", "tenant") != "owner" {
		t.Error("the unit override must win")
	}
}

func TestSiteTypedSettingsDefaults(t *testing.T) {
	s := Site{Settings: map[string]any{}}.TypedSettings()
	if !s.TenantsArePortalUsers || s.DirectoryEnabled || s.TransparencyToOwners || s.AppurtenanceDefault != Appurtenance {
		t.Errorf("defaults: %+v", s)
	}
	s = Site{Settings: map[string]any{SettingDirectoryEnabled: true, SettingAppurtenanceDefault: Independent}}.TypedSettings()
	if !s.DirectoryEnabled || s.AppurtenanceDefault != Independent {
		t.Errorf("typed: %+v", s)
	}
}
