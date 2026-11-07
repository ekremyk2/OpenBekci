package tenancy

import "testing"

func TestValidateVisitorRetentionDays(t *testing.T) {
	for v, ok := range map[float64]bool{7: true, 90: true, 365: true, 6: false, 0: false, 366: false, 1000: false, 30.5: false} {
		got := validateSettings(map[string]any{SettingVisitorRetentionDays: v})
		if (len(got) == 0) != ok {
			t.Errorf("visitor_retention_days %v: problems %v, want valid=%v", v, got, ok)
		}
	}
	if got := validateSettings(map[string]any{SettingVisitorRetentionDays: "90"}); len(got) != 1 || got[0].Code != "invalid_type" {
		t.Errorf("a string retention: %v", got)
	}
}
