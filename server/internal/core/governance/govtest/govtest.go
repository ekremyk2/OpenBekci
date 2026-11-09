// Package govtest gives tests of other packages the Law 634 rule set the way a
// site gets it from the Turkish pack: as the site settings the pack's presets
// write, and as the governance settings those read back as.
package govtest

import (
	"context"
	"encoding/json"

	"github.com/openbekci/openbekci/packs/tr"
	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/governance"
)

// SiteSettings returns the site settings keys the Turkish presets fill
// (quorum, thresholds, vote_weight, schedule, transfer_rule, late_fee_policy,
// legal_preset), JSON-shaped, ready to merge into site.settings.
func SiteSettings() map[string]any {
	ctx, site := context.Background(), sdk.SiteInfo{CountryCode: "TR", Currency: "TRY"}
	r := tr.Rules{}
	q, _ := r.Quorum(ctx, site)
	th, _ := r.Thresholds(ctx, site)
	vw, _ := r.VoteWeight(ctx, site)
	sc, _ := r.Schedule(ctx, site)
	tf, _ := r.Transfer(ctx, site)
	lf, _ := r.LateFee(ctx, site)
	out := map[string]any{"legal_preset": tr.ID}
	for k, v := range map[string]any{sdk.SettingQuorum: q, sdk.SettingThresholds: th, sdk.SettingVoteWeight: vw, sdk.SettingSchedule: sc, sdk.SettingTransferRule: tf, sdk.SettingLateFee: lf} {
		raw, _ := json.Marshal(v)
		var g any
		_ = json.Unmarshal(raw, &g)
		out[k] = g
	}
	return out
}

// Law634 is the governance rule set of a site that has the Turkish presets.
func Law634() governance.Settings { return governance.FromSiteSettings(SiteSettings()) }
