package sdk

import "context"

// SiteInfo is what presets know about the site they fill.
type SiteInfo struct {
	ID          ID
	Org         ID
	CountryCode string // the organization's ISO 3166-1 country
	Timezone    string
	Currency    string
}

// Site settings keys the presets fill. Each holds one JSON object whose shape
// is the matching Defaults type below. The settings endpoints show a key as a
// "pack default" while its value still equals what the pack wrote; a site that
// edits it owns it from then on and later enablements leave it alone.
const (
	SettingQuorum       = "quorum"
	SettingThresholds   = "thresholds"
	SettingVoteWeight   = "vote_weight"
	SettingSchedule     = "schedule"
	SettingTransferRule = "transfer_rule"
	SettingLateFee      = "late_fee_policy"
)

// A preset returns default values for existing site settings, so a site
// without a pack can configure the same rules by hand and a site with a pack
// can still deviate where its management plan allows. Governance and billing
// read site settings exclusively, never the pack.
//
// Core call sites: site creation (organizations whose country is one of the
// pack's Manifest.Countries, when the pack is enabled) and pack enablement,
// both only for settings the site has not changed. Panics and errors in a
// preset skip that preset and are logged; they never fail site creation.

// QuorumPreset fills the quorum bases of the first and second call.
type QuorumPreset interface {
	ID() string
	Quorum(ctx context.Context, site SiteInfo) (QuorumDefaults, error)
}

// QuorumDefaults is the value of SettingQuorum.
type QuorumDefaults struct {
	FirstCall  QuorumRule `json:"first_call"`
	SecondCall QuorumRule `json:"second_call"`
}

// QuorumRule is a quorum: Basis is "units" (share of units present), "share"
// (share of the land share present), "units_and_share" (both must be met) or
// "none". The quorum is met when the part present is more than
// Numerator/Denominator of the whole (1/2 is a majority).
type QuorumRule struct {
	Basis       string `json:"basis"`
	Numerator   int    `json:"numerator"`
	Denominator int    `json:"denominator"`
}

// ThresholdPreset fills the per-topic decision threshold table.
type ThresholdPreset interface {
	ID() string
	Thresholds(ctx context.Context, site SiteInfo) (ThresholdDefaults, error)
}

// ThresholdDefaults is the value of SettingThresholds.
type ThresholdDefaults struct {
	// Default applies to topics not listed.
	Default Threshold `json:"default"`
	// ByTopic maps a topic key to its threshold.
	ByTopic map[string]Threshold `json:"by_topic,omitempty"`
}

// Threshold is a decision threshold: Of is "present" (of those present) or
// "all" (of all units or shares); the fraction is Numerator/Denominator.
type Threshold struct {
	Of          string `json:"of"`
	Numerator   int    `json:"numerator"`
	Denominator int    `json:"denominator"`
}

// VoteWeightPreset fills the vote weight rules.
type VoteWeightPreset interface {
	ID() string
	VoteWeight(ctx context.Context, site SiteInfo) (VoteWeightDefaults, error)
}

// VoteWeightDefaults is the value of SettingVoteWeight.
type VoteWeightDefaults struct {
	// Weight is "per_unit" or "by_share".
	Weight string `json:"weight"`
	// PerPersonCapPercent caps one person's weight; zero means no cap.
	PerPersonCapPercent int `json:"per_person_cap_percent"`
	// PerPersonCapNumerator and PerPersonCapDenominator give the same cap as an exact fraction of all votes (1 and 3 are
	// one third); when both are non-zero they take precedence over PerPersonCapPercent.
	PerPersonCapNumerator   int `json:"per_person_cap_numerator,omitempty"`
	PerPersonCapDenominator int `json:"per_person_cap_denominator,omitempty"`
	// CoOwnerRepresentation is "single_representative" or "each_by_share".
	CoOwnerRepresentation string `json:"co_owner_representation"`
	// ProxyCapPerPerson is how many other owners one person may represent;
	// zero means no cap. It applies in buildings of at most
	// ProxySmallBuildingUnitLimit units (in every building when that is zero).
	ProxyCapPerPerson int `json:"proxy_cap_per_person"`
	// ProxySmallBuildingUnitLimit is the size up to which ProxyCapPerPerson
	// applies; larger buildings use ProxyCapPercent. Zero means no such split.
	ProxySmallBuildingUnitLimit int `json:"proxy_small_building_unit_limit,omitempty"`
	// ProxyCapPercent caps the weight one person may hold as a proxy; zero means no cap.
	ProxyCapPercent int `json:"proxy_cap_percent"`
}

// SchedulePreset fills the meeting timing rules.
type SchedulePreset interface {
	ID() string
	Schedule(ctx context.Context, site SiteInfo) (ScheduleDefaults, error)
}

// ScheduleDefaults is the value of SettingSchedule.
type ScheduleDefaults struct {
	MinNoticeDays       int `json:"min_notice_days"`
	SecondMeetingMinDay int `json:"second_meeting_min_days"`
	SecondMeetingMaxDay int `json:"second_meeting_max_days"`
	// OrdinaryAssemblyMonth is 1 to 12.
	OrdinaryAssemblyMonth int `json:"ordinary_assembly_month"`
}

// TransferRule says what happens to a unit's debt on move-out or sale.
type TransferRule interface {
	ID() string
	Transfer(ctx context.Context, site SiteInfo) (TransferDefaults, error)
}

// TransferDefaults is the value of SettingTransferRule.
type TransferDefaults struct {
	// DebtOnMoveOut is "stays_with_person" or "moves_to_new_owner".
	DebtOnMoveOut string `json:"debt_on_move_out"`
}

// LateFeePreset fills the late fee policy.
type LateFeePreset interface {
	ID() string
	LateFee(ctx context.Context, site SiteInfo) (LateFeeDefaults, error)
}

// LateFeeDefaults is the value of SettingLateFee.
type LateFeeDefaults struct {
	// MonthlyRateCeilingBps is the highest monthly rate allowed in basis
	// points; nil means no ceiling.
	MonthlyRateCeilingBps *int `json:"monthly_rate_ceiling_bps"`
	DefaultMonthlyRateBps int  `json:"default_monthly_rate_bps"`
	GraceDays             int  `json:"grace_days"`
	ProRata               bool `json:"pro_rata"`
}
