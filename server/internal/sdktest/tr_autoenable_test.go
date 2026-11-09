package sdktest_test

import (
	"testing"

	"github.com/openbekci/openbekci/server/internal/sdktest"
)

// Organizations that existed before the pack (country TR by default) get the
// Turkish defaults at startup, once, and an administrator's decision sticks.
func TestTRPackIsEnabledForExistingTurkishOrganizationsOnce(t *testing.T) {
	h := newTR(t, sdktest.WithoutEnable())
	ctx := h.Ctx()
	if s := siteSettings(t, h, h.Env.SiteID); s["legal_preset"] != nil {
		t.Fatalf("precondition: the site has no presets yet: %v", s)
	}
	n, err := h.Host.AutoEnableForCountry(ctx)
	if err != nil || n != 1 {
		t.Fatalf("auto enable: %d %v", n, err)
	}
	if on, _ := h.Host.Enabled(ctx, h.Env.OrgID, "tr"); !on {
		t.Fatal("the tr pack is not enabled")
	}
	if s := siteSettings(t, h, h.Env.SiteID); s["legal_preset"] != "tr" || s["quorum"] == nil {
		t.Fatalf("the Turkish defaults were not applied to the site: %v", s)
	}
	if c := h.Int(`SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = 'pack.enable'`, h.Env.OrgID); c != 1 {
		t.Fatalf("%d pack.enable audit events, want 1 (the automatic enablement is audited)", c)
	}
	if n, err := h.Host.AutoEnableForCountry(ctx); err != nil || n != 0 {
		t.Fatalf("a second run enabled %d organizations (%v): the step must be idempotent", n, err)
	}
	// the administrator disables the pack: a row exists, the step leaves it alone
	h.Disable()
	if n, _ := h.Host.AutoEnableForCountry(ctx); n != 0 {
		t.Fatal("a disabled pack was enabled again")
	}
	if on, _ := h.Host.Enabled(ctx, h.Env.OrgID, "tr"); on {
		t.Fatal("the decision to disable was overridden")
	}
}

func TestTRPackIsNotEnabledForOtherCountries(t *testing.T) {
	h := newTR(t, sdktest.WithoutEnable())
	h.Exec(`UPDATE organization SET country_code = 'DE' WHERE id = $1`, h.Env.OrgID)
	if n, err := h.Host.AutoEnableForCountry(h.Ctx()); err != nil || n != 0 {
		t.Fatalf("a German organization: %d %v", n, err)
	}
}
