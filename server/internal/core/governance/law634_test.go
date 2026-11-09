package governance

import (
	"context"
	"encoding/json"

	"github.com/openbekci/openbekci/packs/tr"
	"github.com/openbekci/openbekci/sdk"
)

// law634 is the Law 634 rule set as the Turkish pack presets it into the site
// settings and FromSiteSettings reads it back: the fixture of the rule tests.
// (server/internal/core/governance/govtest has the same for other packages.)
func law634() Settings {
	ctx, site := context.Background(), sdk.SiteInfo{CountryCode: "TR"}
	r := tr.Rules{}
	q, _ := r.Quorum(ctx, site)
	th, _ := r.Thresholds(ctx, site)
	vw, _ := r.VoteWeight(ctx, site)
	sc, _ := r.Schedule(ctx, site)
	m := map[string]any{}
	for k, v := range map[string]any{siteKeyQuorum: q, siteKeyThresholds: th, siteKeyVoteWeight: vw, siteKeySchedule: sc} {
		raw, _ := json.Marshal(v)
		var g any
		_ = json.Unmarshal(raw, &g)
		m[k] = g
	}
	return FromSiteSettings(m)
}
