package governance

import (
	"testing"

	"github.com/google/uuid"
)

func TestPresetIsValid(t *testing.T) {
	if err := TurkishPreset().Validate(); err != nil {
		t.Fatalf("Turkish preset does not validate: %v", err)
	}
	for _, n := range TurkishPresetNotes() {
		if !n.Verify {
			t.Errorf("note %q is a legal claim and must be marked verify", n.Key)
		}
	}
}

// Presets are only defaults: a site may deviate where its yonetim plani allows,
// so legal numbers are editable; only sanity is validated.
func TestSettingsAreSiteEditable(t *testing.T) {
	s := TurkishPreset()
	s.Quorum.FirstCallByCountPct = 66.7
	s.Quorum.SecondCallRequiresQuorum = true
	s.CallMinDays = 7
	s.SecondCallMinDays, s.SecondCallMaxDays = 3, 30
	s.ProxyCapPct, s.ProxyCapSmallBuildingMax = 10, 1
	s.MaxVotesPerPersonPct = 25
	s.VoteWeight = VoteWeightPerOwner
	s.OrdinaryAssemblyMonth = 3
	s.Thresholds[0].Rule = RuleFourFifths
	if err := s.Validate(); err != nil {
		t.Fatalf("a site-edited rule set must validate: %v", err)
	}
}

func TestSettingsValidation(t *testing.T) {
	cases := map[string]func(*Settings){
		"percent over 100":           func(s *Settings) { s.Quorum.FirstCallBySharePct = 101 },
		"no default topic":           func(s *Settings) { s.Thresholds = s.Thresholds[1:] },
		"unknown rule":               func(s *Settings) { s.Thresholds[1].Rule = "coin_toss" },
		"duplicate topic":            func(s *Settings) { s.Thresholds = append(s.Thresholds, s.Thresholds[1]) },
		"window reversed":            func(s *Settings) { s.SecondCallMinDays, s.SecondCallMaxDays = 15, 7 },
		"cap zero":                   func(s *Settings) { s.MaxVotesPerPersonPct = 0 },
		"unknown weight":             func(s *Settings) { s.VoteWeight = "per_share" },
		"month out of range":         func(s *Settings) { s.OrdinaryAssemblyMonth = 13 },
		"reminder lead out of range": func(s *Settings) { s.TermReminderDays = 0 },
	}
	for name, mutate := range cases {
		s := TurkishPreset()
		mutate(&s)
		if s.Validate() == nil {
			t.Errorf("%s: want a validation problem", name)
		}
	}
}

func TestEvalQuorum(t *testing.T) {
	law := TurkishPreset().Quorum
	strict := QuorumRule{FirstCallByCountPct: 50, FirstCallBySharePct: 50, SecondCallRequiresQuorum: true}
	elig := Weight{Votes: 100, Share: 1000}
	cases := []struct {
		name    string
		q       QuorumRule
		call    string
		present Weight
		want    bool
	}{
		{"first call, majority by both", law, callFirst, Weight{51, 510}, true},
		{"first call, exactly half is not a majority", law, callFirst, Weight{50, 500}, false},
		{"first call, number but not share", law, callFirst, Weight{60, 400}, false},
		{"first call, share but not number", law, callFirst, Weight{40, 700}, false},
		{"second call, no quorum, anybody", law, callSecond, Weight{1, 5}, true},
		{"second call, no quorum, nobody", law, callSecond, Weight{0, 0}, false},
		{"second call, site requires quorum", strict, callSecond, Weight{30, 300}, false},
		{"second call, site requires quorum, met", strict, callSecond, Weight{80, 900}, true},
	}
	for _, c := range cases {
		res := EvalQuorum(c.q, c.call, elig, c.present)
		if res.Reached != c.want {
			t.Errorf("%s: reached = %v, want %v", c.name, res.Reached, c.want)
		}
	}
	if r := EvalQuorum(law, callSecond, elig, Weight{1, 1}); r.RequiredCountPct != nil || !r.NoQuorumSecondCall {
		t.Errorf("a second call without quorum has no required percentages: %+v", r)
	}
}

func TestPersonVoteCap(t *testing.T) {
	// 100 votes: nobody counts for more than 33 (one third, rounded down)
	in := []PersonVotes{{"a", Weight{40, 400}}, {"b", Weight{10, 100}}, {"c", Weight{33, 330}}}
	out := ApplyPersonCap(in, 100, 100.0/3)
	if out[0].Votes != 33 || out[1].Votes != 10 || out[2].Votes != 33 {
		t.Fatalf("capped votes = %d %d %d, want 33 10 33", out[0].Votes, out[1].Votes, out[2].Votes)
	}
	if got := out[0].Share; got < 329.99 || got > 330.01 {
		t.Errorf("a's share scales with the cap: %v, want 330", got)
	}
	if sum := SumWeights(out...); sum.Votes != 76 {
		t.Errorf("sum = %d votes, want 76", sum.Votes)
	}
	// 99 votes: exactly 33
	if got := ApplyPersonCap([]PersonVotes{{"x", Weight{50, 50}}}, 99, 100.0/3)[0].Votes; got != 33 {
		t.Errorf("cap of 99 votes = %d, want 33", got)
	}
	// a tiny building still lets everybody count for at least one vote
	if got := ApplyPersonCap([]PersonVotes{{"x", Weight{2, 2}}}, 2, 100.0/3)[0].Votes; got != 1 {
		t.Errorf("cap of 2 votes = %d, want 1", got)
	}
}

func TestProxyCaps(t *testing.T) {
	s := TurkishPreset() // 5 % of the votes; at most two others in buildings of up to 40 units
	cases := []struct {
		name         string
		units, votes int64
		held         ProxyHeld
		add          int64
		want         string
	}{
		{"small building, first proxy", 30, 30, ProxyHeld{}, 1, ""},
		{"small building, second proxy", 30, 30, ProxyHeld{Grantors: 1, Votes: 1}, 1, ""},
		{"small building, third proxy", 30, 30, ProxyHeld{Grantors: 2, Votes: 2}, 1, "proxy_cap_small_building"},
		{"forty units is still small", 40, 40, ProxyHeld{Grantors: 2}, 1, "proxy_cap_small_building"},
		{"forty-one units, 5 % of 100 votes is 5", 100, 100, ProxyHeld{Grantors: 4, Votes: 4}, 1, ""},
		{"forty-one units, over 5 %", 100, 100, ProxyHeld{Grantors: 5, Votes: 5}, 1, "proxy_cap_pct"},
		{"large building, one proxy of 6 units", 100, 100, ProxyHeld{}, 6, "proxy_cap_pct"},
		{"cap is at least one vote", 41, 41, ProxyHeld{}, 1, ""},
	}
	for _, c := range cases {
		if got := CheckProxyCap(s, c.units, c.votes, c.held, c.add); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// the caps are site settings
	s.SmallBuildingUnitLimit, s.ProxyCapSmallBuildingMax = 10, 3
	if got := CheckProxyCap(s, 30, 30, ProxyHeld{Grantors: 2, Votes: 2}, 1); got == "" {
		t.Error("with a 10-unit small-building limit a 30-unit building uses the percentage cap (5 % of 30 = 1)")
	}
}

func TestEvalThreshold(t *testing.T) {
	elig := Weight{Votes: 100, Share: 1000}
	first := Basis{Eligible: elig, Present: Weight{70, 700}, Call: callFirst}
	second := Basis{Eligible: elig, Present: Weight{30, 300}, Call: callSecond, NoQuorumSecondCall: true}
	cases := []struct {
		name  string
		rule  string
		c     Counts
		basis Basis
		want  string
	}{
		{"majority of all, first call", RuleMajorityCountAndShare, Counts{Yes: 51, No: 19, YesShare: 510}, first, HintAccepted},
		{"majority of all, number short", RuleMajorityCountAndShare, Counts{Yes: 50, No: 20, YesShare: 600}, first, HintRejected},
		{"majority of all, share short", RuleMajorityCountAndShare, Counts{Yes: 60, No: 10, YesShare: 400}, first, HintRejected},
		{"second call without quorum: majority of those present", RuleMajorityCountAndShare, Counts{Yes: 16, No: 14, YesShare: 160, NoShare: 140}, second, HintAccepted},
		{"second call without quorum: exactly half", RuleMajorityCountAndShare, Counts{Yes: 15, No: 15, YesShare: 150, NoShare: 150}, second, HintRejected},
		{"four fifths", RuleFourFifths, Counts{Yes: 80, No: 0, YesShare: 800}, first, HintAccepted},
		{"four fifths, one short", RuleFourFifths, Counts{Yes: 79, No: 0, YesShare: 800}, first, HintRejected},
		{"four fifths stays on all owners at second call", RuleFourFifths, Counts{Yes: 30, YesShare: 300}, second, HintRejected},
		{"unanimity", RuleUnanimity, Counts{Yes: 100, YesShare: 1000}, first, HintAccepted},
		{"unanimity, one missing", RuleUnanimity, Counts{Yes: 99, YesShare: 990}, first, HintRejected},
		{"simple majority of those present", RuleSimpleMajorityPresent, Counts{Yes: 4, No: 2, Abstain: 1}, Basis{Present: Weight{7, 7}, Call: callFirst}, HintAccepted},
		{"simple majority, a present owner who does not vote counts against", RuleSimpleMajorityPresent, Counts{Yes: 4, No: 1}, Basis{Present: Weight{8, 8}, Call: callFirst}, HintRejected},
		{"a tie rejects", RuleSimpleMajorityPresent, Counts{Yes: 3, No: 3}, Basis{Present: Weight{6, 6}, Call: callFirst}, HintRejected},
		{"shares not recorded: never estimated", RuleMajorityCountAndShare, Counts{Yes: 51, No: 19, NoShares: true}, first, HintUndetermined},
		{"simple majority, abstentions count against", RuleSimpleMajorityPresent, Counts{Yes: 3, No: 2, Abstain: 1}, Basis{Present: Weight{6, 6}, Call: callFirst}, HintRejected},
		{"nothing recorded", RuleMajorityCountAndShare, Counts{}, first, HintUndetermined},
		{"unknown rule", "coin_toss", Counts{Yes: 1}, first, HintUndetermined},
	}
	for _, c := range cases {
		if got := EvalThreshold(c.rule, c.c, c.basis); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestTopicTable(t *testing.T) {
	s := TurkishPreset()
	if r := s.RuleFor("plan_amendment"); r.Rule != RuleFourFifths {
		t.Errorf("plan_amendment = %s", r.Rule)
	}
	if r := s.RuleFor("no_such_topic"); r.Topic != DefaultTopic {
		t.Errorf("unknown topics fall back to the default rule, got %q", r.Topic)
	}
	if !s.HasTopic("election") || s.HasTopic("zzz") {
		t.Error("HasTopic")
	}
}

func TestLeadBucket(t *testing.T) {
	leads := reminderLeads(TurkishPreset()) // 7, 30, 90
	want := map[int]int{200: -1, 91: -1, 90: 90, 60: 90, 31: 90, 30: 30, 8: 30, 7: 7, 0: 7, -1: 0, -20: 0}
	for days, b := range want {
		if got := leadBucket(leads, days); got != b {
			t.Errorf("days left %d: bucket %d, want %d", days, got, b)
		}
	}
	short := TurkishPreset()
	short.TermReminderDays = 20
	if got := reminderLeads(short); len(got) != 2 || got[0] != 7 || got[1] != 20 {
		t.Errorf("leads for a 20-day setting = %v, want [7 20]", got)
	}
}

func TestSnapshotWeights(t *testing.T) {
	sn := Snapshot{VoteWeight: VoteWeightPerUnit}
	o := SnapOwner{Units: []SnapUnit{{Share: 2}, {Share: 3}, {Share: 5}}}
	o.Units[0].UnitID[0], o.Units[1].UnitID[0], o.Units[2].UnitID[0] = 1, 2, 3
	if w := sn.WeightOf(o, nil); w.Votes != 3 || w.Share != 10 {
		t.Errorf("per unit, all units: %+v", w)
	}
	if w := sn.WeightOf(o, []uuid.UUID{o.Units[0].UnitID, o.Units[2].UnitID}); w.Votes != 2 || w.Share != 7 {
		t.Errorf("per unit, two units: %+v", w)
	}
	sn.VoteWeight = VoteWeightPerOwner
	if w := sn.WeightOf(o, nil); w.Votes != 1 || w.Share != 10 {
		t.Errorf("per owner: %+v", w)
	}
}
