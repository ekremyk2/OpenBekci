package governance

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/openbekci/openbekci/server/internal/problem"
)

// Threshold rules (open enum). The article mapping of each default is a legal
// claim marked (verify): the table is site settings, not code.
const (
	RuleSimpleMajorityPresent         = "simple_majority_present"
	RuleMajorityCountAndShare         = "majority_count_and_share"
	RuleFourFifths                    = "four_fifths"
	RuleUnanimity                     = "unanimity"
	VoteWeightPerUnit                 = "per_unit"
	VoteWeightPerOwner                = "per_owner"
	DefaultTopic                      = "default"
	callFirst                         = "first"
	callSecond                        = "second"
	epsilon                   float64 = 1e-9
)

var knownRules = []string{RuleSimpleMajorityPresent, RuleMajorityCountAndShare, RuleFourFifths, RuleUnanimity}

// QuorumRule is the quorum rule of a site.
type QuorumRule struct {
	FirstCallByCountPct      float64 `json:"first_call_by_count_pct"`
	FirstCallBySharePct      float64 `json:"first_call_by_share_pct"`
	SecondCallRequiresQuorum bool    `json:"second_call_requires_quorum"`
}

// Threshold is the decision rule of one topic.
type Threshold struct {
	Topic string  `json:"topic"`
	Rule  string  `json:"rule"`
	Note  *string `json:"note"`
}

// Settings are a site's governance rules (the API's GovernanceSettingsSet).
type Settings struct {
	Quorum                   QuorumRule  `json:"quorum"`
	Thresholds               []Threshold `json:"thresholds"`
	MaxVotesPerPersonPct     float64     `json:"max_votes_per_person_pct"`
	ProxyCapPct              float64     `json:"proxy_cap_pct"`
	ProxyCapSmallBuildingMax int64       `json:"proxy_cap_small_building_max"`
	SmallBuildingUnitLimit   int64       `json:"small_building_unit_limit"`
	CallMinDays              int64       `json:"call_min_days"`
	SecondCallMinDays        int64       `json:"second_call_min_days"`
	SecondCallMaxDays        int64       `json:"second_call_max_days"`
	TermReminderDays         int64       `json:"term_reminder_days"`
	VoteWeight               string      `json:"vote_weight"`
	OrdinaryAssemblyMonth    int64       `json:"ordinary_assembly_month"`
	DefaultTermMonths        int64       `json:"default_term_months"`
	// DissentWindowDays is how long after the meeting closed a dissent can still be added to a decision.
	DissentWindowDays int64 `json:"dissent_window_days"`
	// NoticeElectronicAllowed says the site's yonetim plani allows email and SMS
	// as a legal way to give notice (default off, verify).
	NoticeElectronicAllowed bool `json:"notice_electronic_allowed"`
	// DecisionDetailsVisibleToOwners is the site's transparency setting: owners
	// who are not staff see the dissents (names and text) and the vote counts of
	// decisions only when it is on (default off). Tenants never see them.
	DecisionDetailsVisibleToOwners bool `json:"decision_details_visible_to_owners"`
}

// PresetNote is a legal remark attached to a preset; Verify says a person
// with Turkish practice must confirm it before it is relied on.
type PresetNote struct {
	Key    string `json:"key"`
	Note   string `json:"note"`
	Verify bool   `json:"verify"`
}

func strp(s string) *string { return &s }

// GenericPreset is the default rule set of a site without a legal preset (a
// country pack): a majority of the units present on the first call and no
// quorum on the second, a simple majority of those present for every topic,
// one vote per unit without caps, a 15 day call, a second meeting 7 to 15 days
// later and the ordinary assembly in January. A pack fills the site settings
// quorum, thresholds, vote_weight and schedule with the values of its country
// (see FromSiteSettings); a site can set the same values by hand.
func GenericPreset() Settings {
	simple := func(topic string) Threshold { return Threshold{Topic: topic, Rule: RuleSimpleMajorityPresent} }
	return Settings{
		Quorum:                   QuorumRule{FirstCallByCountPct: 50, FirstCallBySharePct: 0, SecondCallRequiresQuorum: false},
		Thresholds:               []Threshold{simple(DefaultTopic), simple("budget"), simple("election"), simple("routine"), simple("plan_amendment"), simple("common_area_change"), simple("main_structure_change")},
		MaxVotesPerPersonPct:     100,
		ProxyCapPct:              100,
		ProxyCapSmallBuildingMax: 0,
		SmallBuildingUnitLimit:   0,
		CallMinDays:              15,
		SecondCallMinDays:        7,
		SecondCallMaxDays:        15,
		TermReminderDays:         90,
		VoteWeight:               VoteWeightPerUnit,
		OrdinaryAssemblyMonth:    1,
		DefaultTermMonths:        12,
		DissentWindowDays:        30,
	}
}

// GenericPresetNotes are the remarks about how the system evaluates meetings;
// they hold for every country. Legal remarks belong to the country's pack.
func GenericPresetNotes() []PresetNote {
	return []PresetNote{
		{Key: "abstentions_and_ties", Note: "An abstention counts as present but is not a yes, so it counts against every majority; a tie rejects, because every majority is strict. A simple majority of those present is measured against everybody present, so a present owner who does not vote counts against too. Unanimity and four-fifths are measured against all eligible votes, and votes above a person's cap do not count, so unanimity is unreachable when one person holds more than the cap."},
		{Key: "call_held", Note: "Quorum and the vote evaluation follow the call that was actually opened (first, or second after the first failed its quorum), never the clock. Attendance does not carry from the first call to the second."},
		{Key: "co_owners_check_in", Note: "Co-owners of a unit vote through one representative. A co-owner who is not the representative cannot check in on their own: the representative is recorded in_person for the unit, and only if the representative is absent can the unit be represented by a proxy, which counts against the proxy cap."},
	}
}

func invalid(field string, reason string) problem.Field {
	f := problem.Field{Field: field, Code: "invalid_value"}
	if reason != "" {
		f.Params = map[string]any{"reason": reason}
	}
	return f
}

// Validate checks that the settings are usable. It does not enforce the law's
// numbers: the site may deviate where its yonetim plani allows, so presets are
// only defaults.
func (s Settings) Validate() *problem.Error {
	var fs []problem.Field
	pct := func(name string, v float64, max float64) {
		if math.IsNaN(v) || v < 0 || v > max {
			fs = append(fs, invalid(name, ""))
		}
	}
	pct("quorum.first_call_by_count_pct", s.Quorum.FirstCallByCountPct, 100)
	pct("quorum.first_call_by_share_pct", s.Quorum.FirstCallBySharePct, 100)
	if s.MaxVotesPerPersonPct <= 0 || s.MaxVotesPerPersonPct > 100 {
		fs = append(fs, invalid("max_votes_per_person_pct", ""))
	}
	if s.ProxyCapPct <= 0 || s.ProxyCapPct > 100 {
		fs = append(fs, invalid("proxy_cap_pct", ""))
	}
	if s.ProxyCapSmallBuildingMax < 0 || s.ProxyCapSmallBuildingMax > 100 {
		fs = append(fs, invalid("proxy_cap_small_building_max", ""))
	}
	if s.SmallBuildingUnitLimit < 0 || s.SmallBuildingUnitLimit > 10000 {
		fs = append(fs, invalid("small_building_unit_limit", ""))
	}
	if s.CallMinDays < 1 || s.CallMinDays > 365 {
		fs = append(fs, invalid("call_min_days", ""))
	}
	if s.SecondCallMinDays < 1 || s.SecondCallMaxDays < s.SecondCallMinDays || s.SecondCallMaxDays > 365 {
		fs = append(fs, invalid("second_call_max_days", "window"))
	}
	if s.TermReminderDays < 1 || s.TermReminderDays > 365 {
		fs = append(fs, invalid("term_reminder_days", ""))
	}
	if s.VoteWeight != VoteWeightPerUnit && s.VoteWeight != VoteWeightPerOwner {
		fs = append(fs, invalid("vote_weight", "unknown"))
	}
	if s.OrdinaryAssemblyMonth < 1 || s.OrdinaryAssemblyMonth > 12 {
		fs = append(fs, invalid("ordinary_assembly_month", ""))
	}
	if s.DefaultTermMonths < 1 || s.DefaultTermMonths > 120 {
		fs = append(fs, invalid("default_term_months", ""))
	}
	if s.DissentWindowDays < 1 || s.DissentWindowDays > 365 {
		fs = append(fs, invalid("dissent_window_days", ""))
	}
	seen := map[string]bool{}
	for i, t := range s.Thresholds {
		name := fmt.Sprintf("thresholds[%d]", i)
		switch {
		case strings.TrimSpace(t.Topic) == "" || len(t.Topic) > 60:
			fs = append(fs, invalid(name+".topic", ""))
		case seen[t.Topic]:
			fs = append(fs, invalid(name+".topic", "duplicate"))
		}
		if !slices.Contains(knownRules, t.Rule) {
			fs = append(fs, invalid(name+".rule", "unknown"))
		}
		seen[t.Topic] = true
	}
	if !seen[DefaultTopic] {
		fs = append(fs, invalid("thresholds", "default_topic_required"))
	}
	if len(fs) > 0 {
		return problem.Validation(fs...)
	}
	return nil
}

// RuleFor returns the threshold rule of a topic; an empty or unknown topic
// falls back to the default topic.
func (s Settings) RuleFor(topic string) Threshold {
	var def Threshold
	for _, t := range s.Thresholds {
		if t.Topic == topic {
			return t
		}
		if t.Topic == DefaultTopic {
			def = t
		}
	}
	return def
}

// HasTopic reports whether the table has the topic.
func (s Settings) HasTopic(topic string) bool {
	return slices.ContainsFunc(s.Thresholds, func(t Threshold) bool { return t.Topic == topic })
}

// Weight is a number of votes and the arsa payi (land share) behind them.
type Weight struct {
	Votes int64
	Share float64
}

// QuorumResult is the evaluation of a quorum.
type QuorumResult struct {
	Call               string
	RequiredCountPct   *float64
	RequiredSharePct   *float64
	Reached            bool
	ByCount, ByShare   bool
	NoQuorumSecondCall bool
}

// EvalQuorum evaluates the quorum of a call. First call: more than the
// configured percentage of the votes by number and by share (50 means a
// majority). Second call: no quorum when the site says so, then it is reached
// once anybody is present.
func EvalQuorum(q QuorumRule, call string, eligible, present Weight) QuorumResult {
	res := QuorumResult{Call: call}
	if call == callSecond && !q.SecondCallRequiresQuorum {
		res.NoQuorumSecondCall = true
		res.Reached = present.Votes > 0
		res.ByCount, res.ByShare = res.Reached, res.Reached
		return res
	}
	rc, rs := q.FirstCallByCountPct, q.FirstCallBySharePct
	res.RequiredCountPct, res.RequiredSharePct = &rc, &rs
	// a base of zero percent is no requirement on that base (the generic quorum has no share rule)
	res.ByCount = rc <= 0 || exceeds(float64(present.Votes), float64(eligible.Votes), rc)
	res.ByShare = rs <= 0 || exceeds(present.Share, eligible.Share, rs)
	res.Reached = res.ByCount && res.ByShare
	return res
}

// exceeds reports x > total*pct/100, with a tolerance for float shares.
func exceeds(x, total, pct float64) bool {
	return x*100 > total*pct+epsilon
}

// atLeast reports x >= total*num/den.
func atLeast(x, total float64, num, den float64) bool {
	return x*den+epsilon >= total*num
}

// CallOf says which call a meeting is at, at time-of-day now: the second call
// once its time has come.
func CallOf(nowAfterSecond bool) string {
	if nowAfterSecond {
		return callSecond
	}
	return callFirst
}

// PersonVotes is what one person holds in the room: own votes plus proxies.
type PersonVotes struct {
	PersonID string
	Weight
}

// ApplyPersonCap enforces the per-person cap: nobody counts for more than
// floor(eligibleVotes * maxPct / 100) votes (and the same fraction of the
// share they would otherwise carry). It returns the capped weights in order.
func ApplyPersonCap(in []PersonVotes, eligibleVotes int64, maxPct float64) []PersonVotes {
	capVotes := int64(math.Floor(float64(eligibleVotes)*maxPct/100 + epsilon))
	if capVotes < 1 {
		capVotes = 1
	}
	out := make([]PersonVotes, len(in))
	for i, p := range in {
		if p.Votes > capVotes {
			p.Share = p.Share * float64(capVotes) / float64(p.Votes)
			p.Votes = capVotes
		}
		out[i] = p
	}
	return out
}

// SumWeights adds weights.
func SumWeights(ws ...PersonVotes) Weight {
	var w Weight
	for _, p := range ws {
		w.Votes += p.Votes
		w.Share += p.Share
	}
	return w
}

// ProxyHeld is what a proxy holder already represents.
type ProxyHeld struct {
	Grantors int64 // distinct owners already represented by proxy
	Votes    int64 // votes behind them
}

// CheckProxyCap checks a further proxy of addVotes votes for one holder. In a
// building of at most SmallBuildingUnitLimit units a holder represents at most
// ProxyCapSmallBuildingMax other owners; in a larger one at most ProxyCapPct %
// of all votes (at least one vote). It returns a reason code or "".
func CheckProxyCap(s Settings, totalUnits, totalVotes int64, held ProxyHeld, addVotes int64) string {
	if totalUnits <= s.SmallBuildingUnitLimit {
		if held.Grantors+1 > s.ProxyCapSmallBuildingMax {
			return "proxy_cap_small_building"
		}
		return ""
	}
	limit := int64(math.Floor(float64(totalVotes)*s.ProxyCapPct/100 + epsilon))
	if limit < 1 {
		limit = 1
	}
	if held.Votes+addVotes > limit {
		return "proxy_cap_pct"
	}
	return ""
}

// Counts are the in-room counts of an agenda item. NoShares says the shares
// were not recorded: the rules that need them then evaluate to undetermined
// (shares are never estimated).
type Counts struct {
	Yes, No, Abstain                int64
	YesShare, NoShare, AbstainShare float64
	NoShares                        bool
}

// Basis is what a threshold is measured against.
type Basis struct {
	Eligible Weight
	Present  Weight
	Call     string
	// NoQuorumSecondCall relaxes the majority rules to those present (art. 29, verify).
	NoQuorumSecondCall bool
}

// Outcome hints of a threshold evaluation.
const (
	HintAccepted     = "accepted"
	HintRejected     = "rejected"
	HintUndetermined = "undetermined"
)

// EvalThreshold applies a rule to the counts. The majority rule is measured
// against all eligible votes on the first call and against those present on a
// second call without quorum; four-fifths and unanimity are always measured
// against all eligible votes (so unanimity is unreachable when a capped
// person holds more than the cap).
//
// Tie and abstention rules: an abstention is present but is not a yes, so it
// counts against every rule that measures yes against a base; a tie (exactly
// half) rejects, because every majority is strict. simple_majority_present is
// measured against those present, so a present owner who does not vote also
// counts against.
//
// Rules that need the arsa payi are undetermined when the shares were not
// recorded: nothing is estimated.
func EvalThreshold(rule string, c Counts, b Basis) string {
	voted := c.Yes + c.No + c.Abstain
	if voted == 0 {
		return HintUndetermined
	}
	if c.NoShares && rule != RuleSimpleMajorityPresent {
		return HintUndetermined
	}
	switch rule {
	case RuleSimpleMajorityPresent:
		if b.Present.Votes == 0 {
			return HintUndetermined
		}
		if c.Yes*2 > b.Present.Votes {
			return HintAccepted
		}
		return HintRejected
	case RuleMajorityCountAndShare:
		base := b.Eligible
		if b.Call == callSecond && b.NoQuorumSecondCall {
			base = b.Present
		}
		if exceeds(float64(c.Yes), float64(base.Votes), 50) && exceeds(c.YesShare, base.Share, 50) {
			return HintAccepted
		}
		return HintRejected
	case RuleFourFifths:
		if atLeast(float64(c.Yes), float64(b.Eligible.Votes), 4, 5) && atLeast(c.YesShare, b.Eligible.Share, 4, 5) {
			return HintAccepted
		}
		return HintRejected
	case RuleUnanimity:
		if c.Yes == b.Eligible.Votes && math.Abs(c.YesShare-b.Eligible.Share) <= epsilon*math.Max(1, b.Eligible.Share)*1e3 {
			return HintAccepted
		}
		return HintRejected
	}
	return HintUndetermined
}
