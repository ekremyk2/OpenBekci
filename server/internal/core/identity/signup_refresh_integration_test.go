package identity_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/mail"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

// A new request for a contact that is still pending overwrites the name and
// locale, so whoever finally verifies has the name they typed.
func TestIntegrationSignUpAgainOverwritesPendingNameAndLocale(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	snd := &sms.Recorder{}
	svc := identity.NewService(d, identity.Config{
		InstanceName: "Test", BaseURL: "https://bekci.example.org", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		SMS: snd, Mail: &mail.Recorder{}, Now: clk.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	signUp := func(in identity.SignUpInput, ip string) {
		t.Helper()
		in.Code = "c.d"
		if err := svc.RequestSignUp(ctx, in, identity.Meta{IP: ip}); err != nil {
			t.Fatal(err)
		}
		if err := svc.Drain(ctx); err != nil {
			t.Fatal(err)
		}
	}
	state := func(col, val string) (name *string, locale, status string) {
		t.Helper()
		if err := d.InPlatformTx(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT display_name, locale, status FROM "user" WHERE `+col+` = $1`, val).Scan(&name, &locale, &status)
		}); err != nil {
			t.Fatal(err)
		}
		return
	}

	signUp(identity.SignUpInput{Phone: "+905550007001", Name: "Stranger", Locale: "tr"}, "198.51.100.1")
	signUp(identity.SignUpInput{Phone: "0555 000 70 01", Name: "The Owner", Locale: "en"}, "198.51.100.2")
	if n, l, s := state("phone", "+905550007001"); n == nil || *n != "The Owner" || l != "en" || s != "pending" {
		t.Fatalf("after the second request: %v %q %q", n, l, s)
	}
	// the code of the second request verifies, and the name stays
	u, err := svc.OTPSignIn(ctx, "+905550007001", lastCode(t, snd), identity.Meta{IP: "203.0.113.50"})
	if err != nil || u.Status != "active" {
		t.Fatalf("verify: %v %+v", err, u)
	}
	// once active, a later sign-up changes nothing about the account
	signUp(identity.SignUpInput{Phone: "+905550007001", Name: "Intruder", Locale: "tr"}, "198.51.100.3")
	if n, l, s := state("phone", "+905550007001"); n == nil || *n != "The Owner" || l != "en" || s != "active" {
		t.Fatalf("active account changed: %v %q %q", n, l, s)
	}

	// the same through email, and an empty name clears the earlier one
	signUp(identity.SignUpInput{Email: "x@example.org", Name: "Stranger"}, "198.51.100.4")
	signUp(identity.SignUpInput{Email: "X@Example.org"}, "198.51.100.5")
	if n, _, s := state("email", "x@example.org"); n != nil || s != "pending" {
		t.Fatalf("email: name %v, status %q; want no name, pending", n, s)
	}
}
