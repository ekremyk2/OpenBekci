package governance

import (
	"math"
	"testing"
)

// A site without a legal preset starts from the generic defaults.
func TestSiteWithoutPresetGetsGenericDefaults(t *testing.T) {
	got := FromSiteSettings(map[string]any{"name": "x"})
	want := GenericPreset()
	if got.Quorum != want.Quorum || got.CallMinDays != 15 || got.MaxVotesPerPersonPct != 100 || got.ProxyCapPct != 100 || got.SmallBuildingUnitLimit != 0 {
		t.Fatalf("%+v", got)
	}
	if rule := got.RuleFor("budget").Rule; rule != RuleSimpleMajorityPresent {
		t.Fatalf("generic rule for every topic is a simple majority of those present, got %s", rule)
	}
	// with the generic quorum a majority of the units is enough, shares are not asked for
	q := EvalQuorum(got.Quorum, callFirst, Weight{Votes: 10}, Weight{Votes: 6})
	if !q.Reached {
		t.Fatalf("6 of 10 units is a majority: %+v", q)
	}
	if EvalQuorum(got.Quorum, callFirst, Weight{Votes: 10}, Weight{Votes: 5}).Reached {
		t.Fatal("5 of 10 is not more than half")
	}
}

// What the Turkish pack writes into the site settings reads back as the Law
// 634 rule set of the plan (section 6.2).
func TestLaw634PresetReadsBackAsThePlanStates(t *testing.T) {
	s := law634()
	if s.Quorum.FirstCallByCountPct != 50 || s.Quorum.FirstCallBySharePct != 50 || s.Quorum.SecondCallRequiresQuorum {
		t.Errorf("quorum %+v", s.Quorum)
	}
	for topic, rule := range map[string]string{
		DefaultTopic: RuleMajorityCountAndShare, "budget": RuleMajorityCountAndShare, "election": RuleMajorityCountAndShare, "routine": RuleSimpleMajorityPresent,
		"plan_amendment": RuleFourFifths, "common_area_change": RuleFourFifths, "main_structure_change": RuleUnanimity,
	} {
		if got := s.RuleFor(topic).Rule; got != rule {
			t.Errorf("topic %s: %s, want %s", topic, got, rule)
		}
	}
	if math.Abs(s.MaxVotesPerPersonPct-100.0/3) > 1e-9 || s.ProxyCapPct != 5 || s.ProxyCapSmallBuildingMax != 2 || s.SmallBuildingUnitLimit != 40 {
		t.Errorf("votes %+v", s)
	}
	if s.CallMinDays != 15 || s.SecondCallMinDays != 7 || s.SecondCallMaxDays != 15 || s.OrdinaryAssemblyMonth != 1 || s.VoteWeight != VoteWeightPerUnit {
		t.Errorf("schedule %+v", s)
	}
	if err := s.Validate(); err != nil {
		t.Error(err)
	}
}

// A site that lowers a value the preset set needs a reason; a site without a
// preset is compared with the generic defaults only.
func TestDeviationsAreMeasuredAgainstTheSitesDefaults(t *testing.T) {
	weak := law634()
	weak.CallMinDays = 7
	weak.Quorum.FirstCallBySharePct = 30
	if got := belowLegalDefaults(weak, law634()); len(got) != 2 {
		t.Errorf("against the preset: %v", got)
	}
	if got := belowLegalDefaults(weak, GenericPreset()); len(got) != 1 || got[0] != "call_min_days" {
		t.Errorf("against the generic defaults: %v", got)
	}
	strict := GenericPreset()
	strict.ProxyCapSmallBuildingMax, strict.SmallBuildingUnitLimit = 2, 40
	if got := belowLegalDefaults(strict, GenericPreset()); len(got) != 0 {
		t.Errorf("a stricter proxy cap is no deviation: %v", got)
	}
}
