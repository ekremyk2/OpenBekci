// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"

	"github.com/openbekci/openbekci/sdk"
)

// Rules implements the six legal presets of the Turkish pack: the values of
// Kat Mülkiyeti Kanunu No. 634 (Law 634) as site-setting defaults. They are
// defaults, not limits: the yönetim planı of a site may deviate where the law
// allows, and every value is editable per site. LEGAL.md cites each one and
// marks which still await the legal maintainer's confirmation.
//
// The scenario tests in presets_test.go read like case law and document the
// values.
type Rules struct{}

// ID implements the preset interfaces.
func (Rules) ID() string { return ID }

// Quorum: on the first call a majority (more than half) by both the number of
// owners and the arsa payı (land share) must be present; on the second call no
// quorum is required (Law 634 art. 29, verify).
func (Rules) Quorum(context.Context, sdk.SiteInfo) (sdk.QuorumDefaults, error) {
	return sdk.QuorumDefaults{
		FirstCall:  sdk.QuorumRule{Basis: "units_and_share", Numerator: 1, Denominator: 2},
		SecondCall: sdk.QuorumRule{Basis: "none"},
	}, nil
}

// Threshold rules, as the pairs of Of / fraction the SDK knows.
var (
	// MajorityOfThoseAndShare: a majority of all owners by number and by arsa payı (art. 32).
	majorityAll = sdk.Threshold{Of: "all", Numerator: 1, Denominator: 2}
	// majorityPresent: a majority of those present.
	majorityPresent = sdk.Threshold{Of: "present", Numerator: 1, Denominator: 2}
	fourFifths      = sdk.Threshold{Of: "all", Numerator: 4, Denominator: 5}
	unanimity       = sdk.Threshold{Of: "all", Numerator: 1, Denominator: 1}
)

// ThresholdRow is one row of the per-topic table with its citation.
type ThresholdRow struct {
	Topic     string
	Threshold sdk.Threshold
	// Article is the Law 634 article the row rests on.
	Article string
	// Note says what the row decides; every row is a (verify) item.
	Note string
}

// ThresholdTable is the per-topic decision table. The topic keys are the ones
// the core's governance module uses: "default" applies to every other topic.
func ThresholdTable() []ThresholdRow {
	return []ThresholdRow{
		{"default", majorityAll, "art. 32", "decisions of the kat malikleri kurulu need a majority by number of owners and by arsa payı"},
		{"budget", majorityAll, "art. 32", "the budget and the dues (aidat) are decisions like any other"},
		{"election", majorityAll, "art. 32", "election of the yönetici, the board and the denetçi"},
		{"routine", majorityPresent, "art. 29, yönetim planı", "routine matters decided by those present where the yönetim planı allows"},
		{"plan_amendment", fourFifths, "art. 28", "changing the yönetim planı needs four fifths of all owners by number and by arsa payı"},
		{"common_area_change", fourFifths, "art. 19", "significant changes and additions to common areas need four fifths"},
		{"main_structure_change", unanimity, "art. 18", "changes to the main structure and the rights of each owner need all owners"},
	}
}

// Thresholds implements sdk.ThresholdPreset.
func (Rules) Thresholds(context.Context, sdk.SiteInfo) (sdk.ThresholdDefaults, error) {
	out := sdk.ThresholdDefaults{ByTopic: map[string]sdk.Threshold{}}
	for _, r := range ThresholdTable() {
		if r.Topic == "default" {
			out.Default = r.Threshold
			continue
		}
		out.ByTopic[r.Topic] = r.Threshold
	}
	return out, nil
}

// VoteWeight: one vote per unit regardless of arsa payı; co-owners vote
// through one representative; a person holds at most one third of all votes
// (art. 31, verify); a person represents at most 5 % of all votes as a proxy,
// and in buildings of 40 units or fewer at most two other owners (verify).
func (Rules) VoteWeight(context.Context, sdk.SiteInfo) (sdk.VoteWeightDefaults, error) {
	return sdk.VoteWeightDefaults{
		Weight:                      "per_unit",
		PerPersonCapPercent:         33,
		PerPersonCapNumerator:       1,
		PerPersonCapDenominator:     3,
		CoOwnerRepresentation:       "single_representative",
		ProxyCapPerPerson:           2,
		ProxyCapPercent:             5,
		ProxySmallBuildingUnitLimit: 40,
	}, nil
}

// Schedule: the call goes out at least 15 days before the meeting; the second
// meeting is 7 to 15 days after the first; the ordinary assembly meets in
// January unless the yönetim planı says otherwise (art. 29, 30, verify).
func (Rules) Schedule(context.Context, sdk.SiteInfo) (sdk.ScheduleDefaults, error) {
	return sdk.ScheduleDefaults{MinNoticeDays: 15, SecondMeetingMinDay: 7, SecondMeetingMaxDay: 15, OrdinaryAssemblyMonth: 1}, nil
}

// Transfer: the remaining debt stays with the leaving person (decision S7);
// the Turkish rule equals the core default and the pack only confirms it.
func (Rules) Transfer(context.Context, sdk.SiteInfo) (sdk.TransferDefaults, error) {
	return sdk.TransferDefaults{DebtOnMoveOut: "stays_with_person"}, nil
}

// LateFeeCeilingBps is the statutory monthly late fee (gecikme tazminatı) in
// basis points: 5 % per month (art. 20, verify). The site may set a lower
// rate, never a higher one.
const LateFeeCeilingBps = 500

// LateFee: 5 % per month is both the default and the ceiling; no grace days;
// the fee accrues per day (the monthly rate divided by the days of the month),
// on principal only (verify).
func (Rules) LateFee(context.Context, sdk.SiteInfo) (sdk.LateFeeDefaults, error) {
	ceil := LateFeeCeilingBps
	return sdk.LateFeeDefaults{MonthlyRateCeilingBps: &ceil, DefaultMonthlyRateBps: LateFeeCeilingBps, GraceDays: 0, ProRata: true}, nil
}

var (
	_ sdk.QuorumPreset     = Rules{}
	_ sdk.ThresholdPreset  = Rules{}
	_ sdk.VoteWeightPreset = Rules{}
	_ sdk.SchedulePreset   = Rules{}
	_ sdk.TransferRule     = Rules{}
	_ sdk.LateFeePreset    = Rules{}
)
