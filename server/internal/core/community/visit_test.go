package community

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEvalVisitFollowsRecurringStatus(t *testing.T) {
	now := time.Now()
	rv := uuid.New()
	visit := VisitorEntry{ApprovalStatus: "pre_approved", WindowFrom: now, WindowTo: now, RecurringVisitorID: &rv}
	if got := evalVisit(visit, now, "active"); got != ReasonMatch {
		t.Errorf("active recurring visitor: %s", got)
	}
	for _, st := range []string{"revoked", "expired", ""} {
		if got := evalVisit(visit, now, st); got != ReasonRevoked {
			t.Errorf("recurring status %q: %s, want revoked", st, got)
		}
	}
	single := VisitorEntry{ApprovalStatus: "pre_approved", WindowFrom: now, WindowTo: now}
	if got := evalVisit(single, now, ""); got != ReasonMatch {
		t.Errorf("single entry: %s", got)
	}
}
