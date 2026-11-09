package latefee_test

import (
	"errors"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/core/money/latefee"
)

// The ceiling is a legal preset, not a rule of the calculator: without one a
// policy may use any rate up to the schema's sanity bound; with one (the Turkish
// pack's 5 %) a higher rate is refused.
func TestCeilingIsAPresetNotARuleOfTheCalculator(t *testing.T) {
	p := latefee.Policy{Currency: money.TRY, RateBPSPerMonth: 900, Simple: true}
	if err := p.Validate(); err != nil {
		t.Fatalf("no ceiling: %v", err)
	}
	p.CeilingBPS = 500
	if err := p.Validate(); !errors.Is(err, latefee.ErrRateTooHigh) {
		t.Fatalf("with a 5 %% ceiling 9 %% is too high: %v", err)
	}
	p.RateBPSPerMonth = 500
	if err := p.Validate(); err != nil {
		t.Fatalf("the ceiling itself is allowed: %v", err)
	}
	p.CeilingBPS, p.RateBPSPerMonth = 0, latefee.HardMaxBPS+1
	if err := p.Validate(); !errors.Is(err, latefee.ErrRateTooHigh) {
		t.Fatalf("more than 100 %% a month is refused whatever the preset: %v", err)
	}
}
