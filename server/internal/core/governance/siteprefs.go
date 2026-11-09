package governance

import (
	"encoding/json"
	"slices"
)

// Site settings keys a legal preset (a country pack) fills, and the one that
// names the pack. Their JSON shapes are the SDK's preset defaults; they are
// mirrored here because the core never imports a pack, and read from the site
// settings only: governance does not know which pack, if any, wrote them.
const (
	siteKeyQuorum      = "quorum"
	siteKeyThresholds  = "thresholds"
	siteKeyVoteWeight  = "vote_weight"
	siteKeySchedule    = "schedule"
	siteKeyLegalPreset = "legal_preset"
)

type siteQuorumRule struct {
	Basis       string `json:"basis"`
	Numerator   int    `json:"numerator"`
	Denominator int    `json:"denominator"`
}

type siteQuorum struct {
	FirstCall  siteQuorumRule `json:"first_call"`
	SecondCall siteQuorumRule `json:"second_call"`
}

type siteThreshold struct {
	Of          string `json:"of"`
	Numerator   int    `json:"numerator"`
	Denominator int    `json:"denominator"`
}

type siteThresholds struct {
	Default siteThreshold            `json:"default"`
	ByTopic map[string]siteThreshold `json:"by_topic"`
}

type siteVoteWeight struct {
	Weight                      string `json:"weight"`
	PerPersonCapPercent         int    `json:"per_person_cap_percent"`
	PerPersonCapNumerator       int    `json:"per_person_cap_numerator"`
	PerPersonCapDenominator     int    `json:"per_person_cap_denominator"`
	ProxyCapPerPerson           int    `json:"proxy_cap_per_person"`
	ProxySmallBuildingUnitLimit int    `json:"proxy_small_building_unit_limit"`
	ProxyCapPercent             int    `json:"proxy_cap_percent"`
}

type siteSchedule struct {
	MinNoticeDays         int `json:"min_notice_days"`
	SecondMeetingMinDays  int `json:"second_meeting_min_days"`
	SecondMeetingMaxDays  int `json:"second_meeting_max_days"`
	OrdinaryAssemblyMonth int `json:"ordinary_assembly_month"`
}

func siteValue(site map[string]any, key string, into any) bool {
	v, ok := site[key]
	if !ok || v == nil {
		return false
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, into) == nil
}

func pctOf(num, den int) float64 { return 100 * float64(num) / float64(den) }

// ruleOfThreshold maps a threshold of the site settings to the rule the
// evaluation knows: a majority of those present, a majority of all by number
// and share, four fifths of all, or all. A threshold with another fraction has
// no rule; ok is false and the topic is left at the generic default.
func ruleOfThreshold(t siteThreshold) (string, bool) {
	if t.Denominator <= 0 {
		return "", false
	}
	switch {
	case t.Of == "present" && t.Numerator*2 == t.Denominator:
		return RuleSimpleMajorityPresent, true
	case t.Of == "all" && t.Numerator*2 == t.Denominator:
		return RuleMajorityCountAndShare, true
	case t.Of == "all" && t.Numerator*5 == t.Denominator*4:
		return RuleFourFifths, true
	case t.Of == "all" && t.Numerator == t.Denominator:
		return RuleUnanimity, true
	}
	return "", false
}

// FromSiteSettings returns the governance settings a site starts from: the
// generic defaults, overlaid with whatever a legal preset wrote into the site
// settings under quorum, thresholds, vote_weight and schedule. A site without
// a pack has none of these keys and gets the generic defaults.
func FromSiteSettings(site map[string]any) Settings {
	s := GenericPreset()
	var q siteQuorum
	if siteValue(site, siteKeyQuorum, &q) {
		f := q.FirstCall
		var pct float64
		if f.Denominator > 0 {
			pct = pctOf(f.Numerator, f.Denominator)
		}
		switch f.Basis {
		case "units":
			s.Quorum.FirstCallByCountPct, s.Quorum.FirstCallBySharePct = pct, 0
		case "share":
			s.Quorum.FirstCallByCountPct, s.Quorum.FirstCallBySharePct = 0, pct
		case "units_and_share":
			s.Quorum.FirstCallByCountPct, s.Quorum.FirstCallBySharePct = pct, pct
		case "none":
			s.Quorum.FirstCallByCountPct, s.Quorum.FirstCallBySharePct = 0, 0
		}
		s.Quorum.SecondCallRequiresQuorum = q.SecondCall.Basis != "" && q.SecondCall.Basis != "none"
	}
	var th siteThresholds
	if siteValue(site, siteKeyThresholds, &th) {
		set := func(topic string, t siteThreshold) {
			rule, ok := ruleOfThreshold(t)
			if !ok {
				return
			}
			if i := slices.IndexFunc(s.Thresholds, func(x Threshold) bool { return x.Topic == topic }); i >= 0 {
				s.Thresholds[i].Rule = rule
				return
			}
			s.Thresholds = append(s.Thresholds, Threshold{Topic: topic, Rule: rule})
		}
		set(DefaultTopic, th.Default)
		topics := make([]string, 0, len(th.ByTopic))
		for k := range th.ByTopic {
			topics = append(topics, k)
		}
		slices.Sort(topics)
		for _, k := range topics {
			set(k, th.ByTopic[k])
		}
	}
	var vw siteVoteWeight
	if siteValue(site, siteKeyVoteWeight, &vw) {
		if vw.Weight == VoteWeightPerUnit || vw.Weight == VoteWeightPerOwner {
			s.VoteWeight = vw.Weight
		}
		switch {
		case vw.PerPersonCapNumerator > 0 && vw.PerPersonCapDenominator > 0:
			s.MaxVotesPerPersonPct = pctOf(vw.PerPersonCapNumerator, vw.PerPersonCapDenominator)
		case vw.PerPersonCapPercent > 0:
			s.MaxVotesPerPersonPct = float64(vw.PerPersonCapPercent)
		}
		if vw.ProxyCapPercent > 0 {
			s.ProxyCapPct = float64(vw.ProxyCapPercent)
		}
		if vw.ProxyCapPerPerson > 0 {
			s.ProxyCapSmallBuildingMax = int64(vw.ProxyCapPerPerson)
			s.SmallBuildingUnitLimit = int64(vw.ProxySmallBuildingUnitLimit)
			if s.SmallBuildingUnitLimit <= 0 {
				s.SmallBuildingUnitLimit = 10000 // the cap per person applies in every building
			}
		}
	}
	var sc siteSchedule
	if siteValue(site, siteKeySchedule, &sc) {
		if sc.MinNoticeDays > 0 {
			s.CallMinDays = int64(sc.MinNoticeDays)
		}
		if sc.SecondMeetingMinDays > 0 && sc.SecondMeetingMaxDays >= sc.SecondMeetingMinDays {
			s.SecondCallMinDays, s.SecondCallMaxDays = int64(sc.SecondMeetingMinDays), int64(sc.SecondMeetingMaxDays)
		}
		if sc.OrdinaryAssemblyMonth >= 1 && sc.OrdinaryAssemblyMonth <= 12 {
			s.OrdinaryAssemblyMonth = int64(sc.OrdinaryAssemblyMonth)
		}
	}
	return s
}

// LegalPreset returns the id of the pack whose presets filled the site
// settings (the site setting legal_preset), "" when none did.
func LegalPreset(site map[string]any) string {
	s, _ := site[siteKeyLegalPreset].(string)
	return s
}
