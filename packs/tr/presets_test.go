// SPDX-License-Identifier: AGPL-3.0-or-later

package tr

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/openbekci/openbekci/sdk"
)

// The scenario tests below are the documentation of the presets: each one
// reads like a case. They evaluate the preset values with small helpers that
// mirror what the core's governance and billing modules do with the site
// settings; the core's own scenario tests (server/internal/sdktest/tr_test.go)
// drive the real modules with these values.

var site = sdk.SiteInfo{CountryCode: "TR", Timezone: "Europe/Istanbul", Currency: "TRY"}

// meeting is a gathering of a building: units and arsa payı (land share, in
// ten-thousandths of the whole), and what is present.
type meeting struct {
	units, present           int
	share, presentShare      float64
	call                     string // "first" or "second"
	eligibleVotes, voteCount int
}

func quorumMet(q sdk.QuorumDefaults, m meeting) bool {
	rule := q.FirstCall
	if m.call == "second" {
		rule = q.SecondCall
	}
	more := func(part, whole float64) bool {
		return part*float64(rule.Denominator) > whole*float64(rule.Numerator)
	}
	switch rule.Basis {
	case "none":
		return m.present > 0
	case "units":
		return more(float64(m.present), float64(m.units))
	case "share":
		return more(m.presentShare, m.share)
	case "units_and_share":
		return more(float64(m.present), float64(m.units)) && more(m.presentShare, m.share)
	}
	return false
}

func TestQuorum_24Units18OwnersPresentHolding55PercentOfShares_FirstCall_QuorumMet(t *testing.T) {
	q, _ := Rules{}.Quorum(context.Background(), site)
	if !quorumMet(q, meeting{units: 24, present: 18, share: 100, presentShare: 55, call: "first"}) {
		t.Fatal("18 of 24 owners and 55 % of the shares are a majority by number and by share")
	}
}

func TestQuorum_24Units13OwnersPresentHolding40PercentOfShares_FirstCall_NotMetBecauseTheShareIsShort(t *testing.T) {
	q, _ := Rules{}.Quorum(context.Background(), site)
	if quorumMet(q, meeting{units: 24, present: 13, share: 100, presentShare: 40, call: "first"}) {
		t.Fatal("a majority of owners without a majority of arsa payı is no quorum: both bases are required")
	}
}

func TestQuorum_24Units12OwnersPresentHolding60PercentOfShares_FirstCall_NotMetBecauseExactlyHalfIsNotAMajority(t *testing.T) {
	q, _ := Rules{}.Quorum(context.Background(), site)
	if quorumMet(q, meeting{units: 24, present: 12, share: 100, presentShare: 60, call: "first"}) {
		t.Fatal("12 of 24 is half, not more than half")
	}
}

func TestQuorum_24Units5OwnersPresent_SecondCall_NoQuorumIsRequired(t *testing.T) {
	q, _ := Rules{}.Quorum(context.Background(), site)
	if !quorumMet(q, meeting{units: 24, present: 5, share: 100, presentShare: 12, call: "second"}) {
		t.Fatal("on the second call the meeting is held with whoever came")
	}
	if quorumMet(q, meeting{units: 24, present: 0, call: "second"}) {
		t.Fatal("nobody present is no meeting")
	}
}

// threshold evaluation: yes votes (and share) against the base the rule names.
func thresholdMet(th sdk.Threshold, yes, present, eligible int) bool {
	base := eligible
	if th.Of == "present" {
		base = present
	}
	if th.Numerator*2 == th.Denominator { // a majority is strictly more than half
		return yes*th.Denominator > base*th.Numerator
	}
	return yes*th.Denominator >= base*th.Numerator
}

func tableOf(t *testing.T) sdk.ThresholdDefaults {
	t.Helper()
	d, err := Rules{}.Thresholds(context.Background(), site)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestThresholds_EveryRowCitesAnArticleAndEveryTopicIsServed(t *testing.T) {
	d := tableOf(t)
	for _, r := range ThresholdTable() {
		if r.Article == "" || r.Note == "" {
			t.Errorf("row %q cites no article or note", r.Topic)
		}
		got := d.Default
		if r.Topic != "default" {
			var ok bool
			if got, ok = d.ByTopic[r.Topic]; !ok {
				t.Fatalf("topic %q is not in the preset", r.Topic)
			}
		}
		if got != r.Threshold {
			t.Errorf("topic %q: preset %+v, table %+v", r.Topic, got, r.Threshold)
		}
	}
}

func TestThresholds_PlanAmendment_20Of24OwnersInFavour_FourFifthsReached(t *testing.T) {
	th := tableOf(t).ByTopic["plan_amendment"]
	if !thresholdMet(th, 20, 22, 24) {
		t.Fatal("20 of 24 is more than four fifths (19.2)")
	}
	if thresholdMet(th, 19, 24, 24) {
		t.Fatal("19 of 24 is short of four fifths (19.2)")
	}
}

func TestThresholds_MainStructureChange_23Of24InFavour_UnanimityNotReached(t *testing.T) {
	th := tableOf(t).ByTopic["main_structure_change"]
	if thresholdMet(th, 23, 24, 24) || !thresholdMet(th, 24, 24, 24) {
		t.Fatal("a change of the main structure needs every owner")
	}
}

func TestThresholds_RoutineMatter_7OfThe12PresentInFavour_MajorityOfThosePresent(t *testing.T) {
	th := tableOf(t).ByTopic["routine"]
	if th.Of != "present" || !thresholdMet(th, 7, 12, 24) || thresholdMet(th, 6, 12, 24) {
		t.Fatalf("routine matters are decided by a majority of those present: %+v", th)
	}
}

func TestThresholds_Budget_13Of24OwnersInFavour_MajorityOfAllOwners(t *testing.T) {
	th := tableOf(t).ByTopic["budget"]
	if th.Of != "all" || !thresholdMet(th, 13, 15, 24) || thresholdMet(th, 12, 15, 24) {
		t.Fatalf("the budget needs a majority of all owners: %+v", th)
	}
}

func TestVoteWeight_OneVotePerUnitWhateverTheArsaPayi(t *testing.T) {
	v, _ := Rules{}.VoteWeight(context.Background(), site)
	if v.Weight != "per_unit" || v.CoOwnerRepresentation != "single_representative" {
		t.Fatalf("%+v", v)
	}
}

func TestVoteWeight_OneOwnerOf30Units_IsCappedAtOneThirdOfTheVotes(t *testing.T) {
	v, _ := Rules{}.VoteWeight(context.Background(), site)
	total := 90 // a 90-unit building; one investor holds 50 units
	cap := total * v.PerPersonCapNumerator / v.PerPersonCapDenominator
	if cap != 30 {
		t.Fatalf("cap = %d", cap)
	}
	if 50 <= cap {
		t.Fatal("the investor holds more than the cap")
	}
}

// proxyAllowed reports whether a person may represent one more owner.
func proxyAllowed(v sdk.VoteWeightDefaults, units, totalVotes, heldOthers, heldVotes int) bool {
	if v.ProxySmallBuildingUnitLimit > 0 && units <= v.ProxySmallBuildingUnitLimit {
		return heldOthers+1 <= v.ProxyCapPerPerson
	}
	limit := int(math.Floor(float64(totalVotes) * float64(v.ProxyCapPercent) / 100))
	if limit < 1 {
		limit = 1
	}
	return heldVotes+1 <= limit
}

func TestProxy_Building24Units_ThirdProxyHolderRepresentsAtMostTwoOthers(t *testing.T) {
	v, _ := Rules{}.VoteWeight(context.Background(), site)
	if !proxyAllowed(v, 24, 24, 1, 1) {
		t.Fatal("a second proxy is allowed in a building of 40 units or fewer")
	}
	if proxyAllowed(v, 24, 24, 2, 2) {
		t.Fatal("a third proxy is refused in a building of 40 units or fewer")
	}
}

func TestProxy_Building120Units_AProxyHolderRepresentsAtMostFivePercentOfTheVotes(t *testing.T) {
	v, _ := Rules{}.VoteWeight(context.Background(), site)
	if !proxyAllowed(v, 120, 120, 5, 5) { // 6 of 120 = 5 %
		t.Fatal("six votes are 5 % of 120")
	}
	if proxyAllowed(v, 120, 120, 6, 6) { // 7 of 120 > 5 %
		t.Fatal("seven votes are more than 5 % of 120")
	}
}

func TestSchedule_CallSentOn1March_MeetingNotBefore16March(t *testing.T) {
	s, _ := Rules{}.Schedule(context.Background(), site)
	sent := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	earliest := sent.AddDate(0, 0, s.MinNoticeDays)
	if earliest != time.Date(2027, 3, 16, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("earliest meeting %v", earliest)
	}
}

func TestSchedule_FirstMeeting10January_SecondMeetingBetween17And25January(t *testing.T) {
	s, _ := Rules{}.Schedule(context.Background(), site)
	first := time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)
	lo, hi := first.AddDate(0, 0, s.SecondMeetingMinDay), first.AddDate(0, 0, s.SecondMeetingMaxDay)
	if lo.Day() != 17 || hi.Day() != 25 || s.OrdinaryAssemblyMonth != 1 {
		t.Fatalf("window %v to %v, month %d", lo, hi, s.OrdinaryAssemblyMonth)
	}
}

func TestTransfer_OwnerSellsWith4000LiraDue_DebtStaysWithTheSeller(t *testing.T) {
	r, _ := Rules{}.Transfer(context.Background(), site)
	if r.DebtOnMoveOut != "stays_with_person" {
		t.Fatalf("%+v", r)
	}
}

// monthlyFee is the fee for days late in a 30-day month on principal at the
// monthly rate in basis points, accruing per day.
func monthlyFee(principal int64, rateBps, daysLate, daysInMonth int) float64 {
	return float64(principal) * float64(rateBps) / 10000 * float64(daysLate) / float64(daysInMonth)
}

func TestLateFee_DefaultIsFivePercentAMonthWhichIsAlsoTheCeiling(t *testing.T) {
	f, _ := Rules{}.LateFee(context.Background(), site)
	if f.MonthlyRateCeilingBps == nil || *f.MonthlyRateCeilingBps != 500 || f.DefaultMonthlyRateBps != 500 || f.GraceDays != 0 || !f.ProRata {
		t.Fatalf("%+v", f)
	}
}

func TestLateFee_1000LiraDueAndAFullMonthLate_Costs50Lira(t *testing.T) {
	if got := monthlyFee(100000, 500, 30, 30); math.Abs(got-5000) > 1e-9 {
		t.Fatalf("fee = %v kurus", got)
	}
}

func TestLateFee_1000LiraDueAnd15DaysLateInA30DayMonth_CostsHalfAMonth(t *testing.T) {
	if got := monthlyFee(100000, 500, 15, 30); math.Abs(got-2500) > 1e-9 {
		t.Fatalf("fee = %v kurus: the accrual is pro-rated by day", got)
	}
}

func TestLateFee_ASiteMayChooseALowerRateButNeverAHigherOne(t *testing.T) {
	f, _ := Rules{}.LateFee(context.Background(), site)
	allowed := func(bps int) bool { return bps >= 0 && bps <= *f.MonthlyRateCeilingBps }
	if !allowed(300) || !allowed(500) || allowed(501) {
		t.Fatal("the ceiling is 500 bps")
	}
}

func TestEveryPresetIsSerializableAndIdentifiesThePack(t *testing.T) {
	if (Rules{}).ID() != ID {
		t.Fatal("the presets must carry the pack id")
	}
}
