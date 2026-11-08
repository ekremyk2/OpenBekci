package people

import (
	"context"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/ratelimit"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// limits are the abuse limits of the unauthenticated and portal-initiated
// endpoints (M3): per client address and, for signed-in callers, per user.
// They are per instance and in memory (backend plan 8).
type limits struct {
	previewIP, acceptIP     *ratelimit.Limiter
	claimIP, claimUser      *ratelimit.Limiter
	householdIP, householdU *ratelimit.Limiter
}

// maxPendingHousehold caps unanswered household invitations per unit.
const maxPendingHousehold = 10

func newLimits() limits {
	return limits{
		previewIP:   ratelimit.New(60, 2*time.Second),
		acceptIP:    ratelimit.New(20, 6*time.Second),
		claimIP:     ratelimit.New(20, 30*time.Second),
		claimUser:   ratelimit.New(5, 12*time.Minute),
		householdIP: ratelimit.New(30, 30*time.Second),
		householdU:  ratelimit.New(10, 6*time.Minute),
	}
}

func clientIP(ctx context.Context) string {
	if ip := audit.RequestMetaFrom(ctx).IP; ip != "" {
		return ip
	}
	return "unknown"
}

// allow takes a token from l for key, or answers 429.
func allow(l *ratelimit.Limiter, key string) error {
	if ok, wait := l.Allow(key); !ok {
		return problem.RateLimited(wait)
	}
	return nil
}

// allowIPAndActor applies an address limiter and, when signed in, a user limiter.
func allowIPAndActor(ctx context.Context, byIP, byUser *ratelimit.Limiter) error {
	if err := allow(byIP, clientIP(ctx)); err != nil {
		return err
	}
	if p := authz.PrincipalFromContext(ctx); p != nil && byUser != nil {
		return allow(byUser, p.UserID.String())
	}
	return nil
}
