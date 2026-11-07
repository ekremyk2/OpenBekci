package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/problem"
)

func TestIntegrationRefreshKeepsDeviceName(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "dev@example.org"})
	tp, err := e.svc.IssueTokens(ctx, u.ID, identity.Meta{IP: "203.0.113.7", DeviceName: "Pixel 9"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // the refresh request carries no device name
		if tp, _, err = e.svc.Refresh(ctx, tp.RefreshToken, identity.Meta{IP: "203.0.113.8"}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.svc.ListSessions(ctx, u.ID, tp.SessionID)
	if err != nil || len(list) != 1 || list[0].DeviceName == nil || *list[0].DeviceName != "Pixel 9" {
		t.Fatalf("sessions = %+v, %v; want the device name kept", list, err)
	}
}

// The previous refresh token is accepted once inside the 30 s window and
// answered with the very same pair, so a lost rotation response is recoverable.
func TestIntegrationRefreshGraceWindow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t, identity.CreateUserParams{Email: "grace@example.org"})

	issue := func() identity.TokenPair {
		t.Helper()
		tp, err := e.svc.IssueTokens(ctx, u.ID, meta)
		if err != nil {
			t.Fatal(err)
		}
		return tp
	}

	// lost response: the retry gets the same pair, and it works
	tp1 := issue()
	tp2, _, err := e.svc.Refresh(ctx, tp1.RefreshToken, meta)
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(20 * time.Second)
	again, uid, err := e.svc.Refresh(ctx, tp1.RefreshToken, meta)
	if err != nil || uid != u.ID {
		t.Fatalf("replay inside the window: %v", err)
	}
	if again != tp2 {
		t.Fatalf("replay = %+v, want the same pair %+v", again, tp2)
	}
	if _, err := e.svc.Authenticate(ctx, identity.KindAccessToken, again.AccessToken); err != nil {
		t.Fatalf("replayed access token: %v", err)
	}
	// the replayed pair keeps rotating normally
	tp3, _, err := e.svc.Refresh(ctx, again.RefreshToken, meta)
	if err != nil {
		t.Fatalf("refresh with the replayed pair: %v", err)
	}
	// but the old token works only once: a second replay is reuse and kills the family
	_, _, err = e.svc.Refresh(ctx, tp1.RefreshToken, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	if _, err := e.svc.Authenticate(ctx, identity.KindAccessToken, tp3.AccessToken); err == nil {
		t.Error("the family survived a second replay")
	}

	// outside the window it is plain reuse
	tp4 := issue()
	if _, _, err := e.svc.Refresh(ctx, tp4.RefreshToken, meta); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(identity.RefreshGrace + time.Second)
	_, _, err = e.svc.Refresh(ctx, tp4.RefreshToken, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)

	// once the new refresh token has been used, the old one is a leak even inside the window
	tp5 := issue()
	tp6, _, _ := e.svc.Refresh(ctx, tp5.RefreshToken, meta)
	if _, _, err := e.svc.Refresh(ctx, tp6.RefreshToken, meta); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.Refresh(ctx, tp5.RefreshToken, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
	if _, err := e.svc.Authenticate(ctx, identity.KindAccessToken, tp6.AccessToken); err == nil {
		t.Error("the family survived the reuse of a token whose successor was already used")
	}

	// a logged-out session is not resurrected by the window
	tp7 := issue()
	tp8, _, _ := e.svc.Refresh(ctx, tp7.RefreshToken, meta)
	if _, err := e.svc.RevokeSession(ctx, u.ID, tp8.SessionID); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.svc.Refresh(ctx, tp7.RefreshToken, meta)
	wantProblem(t, err, 401, problem.CodeInvalidGrant)
}
