package sdktest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/core/checkout"
	"github.com/openbekci/openbekci/server/internal/core/money"
	"github.com/openbekci/openbekci/server/internal/packs"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// flaky is the packs host with a provider whose CreateCheckout can be made to fail, and that counts what the core
// asked of it.
type flaky struct {
	*packs.Host
	mu       sync.Mutex
	failWith []error // consumed one per CreateCheckout call
	refs     []string
	status   int
}

func (f *flaky) CreateCheckout(ctx context.Context, org uuid.UUID, pack, provider string, req sdk.CheckoutRequest) (sdk.Checkout, error) {
	f.mu.Lock()
	f.refs = append(f.refs, req.Reference)
	var fail error
	if len(f.failWith) > 0 {
		fail, f.failWith = f.failWith[0], f.failWith[1:]
	}
	f.mu.Unlock()
	if fail != nil {
		return sdk.Checkout{}, fail
	}
	return f.Host.CreateCheckout(ctx, org, pack, provider, req)
}

func (f *flaky) PaymentStatus(ctx context.Context, org uuid.UUID, pack, provider, txn string) (sdk.PaymentStatus, error) {
	f.mu.Lock()
	f.status++
	f.mu.Unlock()
	return f.Host.PaymentStatus(ctx, org, pack, provider, txn)
}

func (e *cenv) withFlaky(fails ...error) *flaky {
	f := &flaky{Host: e.Host, failWith: fails}
	e.chk = checkout.NewService(checkout.Config{DB: e.Env.DB, Platform: e.Env.DB, Payments: e.Payments, Providers: f, Receipts: e.rcpt, BaseURL: "https://app.example.test"})
	e.Host.AttachCheckouts(e.chk)
	return f
}

func asProb(t *testing.T, err error) *problem.Error {
	t.Helper()
	var pe *problem.Error
	if !asProblem(err, &pe) {
		t.Fatalf("error %v is not a problem", err)
	}
	return pe
}

// A retry of a create whose provider call failed returns the same checkout and asks the provider under the same
// reference, so a hosted checkout the provider did create is never doubled; a replay of the finished one does not
// ask the provider at all.
func TestCheckoutCreateRetryAfterProviderFailureReusesTheCheckout(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 40000, "c")
	f := e.withFlaky(fmt.Errorf("account acct_SECRET123 is closed: %w", sdk.ErrRejected))
	in := checkout.CreateInput{UnitID: e.unit, DebtorKind: "owner", IdempotencyKey: "key-0001-aaaa"}

	_, err := e.chk.Create(e.Ctx(), e.Env.OrgID, in)
	pe := asProb(t, err)
	if pe.Status != 422 || strings.Contains(pe.Detail, "SECRET") || strings.Contains(pe.Detail, "acct_") {
		t.Fatalf("a provider rejection reached the client as %d %q", pe.Status, pe.Detail)
	}
	if n := e.Env.Int(`SELECT count(*) FROM checkout WHERE org_id = $1 AND status = 'failed' AND provider_txn_id IS NULL`, e.Env.OrgID); n != 1 {
		t.Fatalf("failed checkouts without a provider transaction = %d", n)
	}

	c, err := e.chk.Create(e.Ctx(), e.Env.OrgID, in)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if c.Status != checkout.StatusPending || c.CheckoutURL == nil {
		t.Fatalf("retry result = %+v", c)
	}
	if len(f.refs) != 2 || f.refs[0] != f.refs[1] || f.refs[0] != c.ID.String() {
		t.Fatalf("provider references = %v, want the same reference twice (the checkout id %s)", f.refs, c.ID)
	}
	if n := e.Env.Int(`SELECT count(*) FROM checkout WHERE org_id = $1`, e.Env.OrgID); n != 1 {
		t.Fatalf("%d checkouts after the retry, want 1", n)
	}

	again, err := e.chk.Create(e.Ctx(), e.Env.OrgID, in)
	if err != nil || again.ID != c.ID || again.CheckoutURL == nil || *again.CheckoutURL != *c.CheckoutURL {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	if len(f.refs) != 2 {
		t.Errorf("the replay asked the provider again (%v)", f.refs)
	}

	// the same key with another request
	other := in
	other.Amount = &money.Money{Amount: 100, Currency: "TRY"}
	if _, err := e.chk.Create(e.Ctx(), e.Env.OrgID, other); asProb(t, err).Code != problem.CodeIdempotencyMismatch {
		t.Errorf("same key, other request: %v", err)
	}
}

// Whatever a provider says stays in the log: the client gets a stable code and a fixed message.
func TestCheckoutProviderErrorsMapToStableProblems(t *testing.T) {
	e := newCheckoutEnv(t)
	e.charge("owner", 40000, "c")
	secret := "token sk_live_TOPSECRET rejected for merchant 12345"
	f := e.withFlaky(
		fmt.Errorf("%s: %w", secret, sdk.ErrUnavailable),
		fmt.Errorf("%s: %w", secret, sdk.ErrCredentials),
		errors.New(secret),
	)
	_ = f
	for i, want := range []struct {
		status int
		code   problem.Code
	}{{503, problem.CodePackError}, {409, problem.CodePaymentProviderUnavailable}, {502, problem.CodePackError}} {
		_, err := e.chk.Create(e.Ctx(), e.Env.OrgID, checkout.CreateInput{UnitID: e.unit, DebtorKind: "owner"})
		pe := asProb(t, err)
		if pe.Status != want.status || pe.Code != want.code {
			t.Errorf("case %d: %d %s, want %d %s", i, pe.Status, pe.Code, want.status, want.code)
		}
		if strings.Contains(pe.Detail, "TOPSECRET") || strings.Contains(pe.Detail, "12345") {
			t.Errorf("case %d: the provider's words reached the client: %q", i, pe.Detail)
		}
	}
}
