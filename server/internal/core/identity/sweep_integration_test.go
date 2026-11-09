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

func TestIntegrationSweepPendingSignUps(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	snd, ml := &sms.Recorder{}, &mail.Recorder{}
	svc := identity.NewService(d, identity.Config{
		InstanceName: "Test Site", BaseURL: "https://bekci.example.org", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		SMS: snd, Mail: ml, Now: clk.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	signUp := func(phone, ip string) {
		t.Helper()
		if err := svc.RequestSignUp(ctx, identity.SignUpInput{Code: "c.d", Phone: phone}, identity.Meta{IP: ip}); err != nil {
			t.Fatal(err)
		}
		if err := svc.Drain(ctx); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string, args ...any) (n int) {
		t.Helper()
		if err := d.InPlatformTx(ctx, func(tx pgx.Tx) error { return tx.QueryRow(ctx, q, args...).Scan(&n) }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	age := func(phone string, by time.Duration) {
		t.Helper()
		if err := d.InPlatformTx(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE "user" SET created_at = created_at - $2::interval WHERE phone = $1`, phone, by.String())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	signUp("+905550000001", "198.51.100.1") // abandoned
	signUp("+905550000002", "198.51.100.2") // abandoned, but recent
	signUp("+905550000003", "198.51.100.3") // verified
	e2eActive(t, svc, snd, "+905550000003")
	e2eKnownUser(t, svc, "+905550000004") // a regular account, however old
	age("+905550000001", 25*time.Hour)
	age("+905550000003", 25*time.Hour)
	age("+905550000004", 25*time.Hour)

	n, err := identity.SweepPendingSignUps(ctx, d, time.Now(), identity.PendingMaxAge)
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v; want exactly the old abandoned account", n, err)
	}
	if got := count(`SELECT count(*) FROM "user" WHERE phone = '+905550000001'`); got != 0 {
		t.Error("the abandoned account is still there")
	}
	if got := count(`SELECT count(*) FROM otp_code WHERE target = '+905550000001'`); got != 0 {
		t.Errorf("%d codes of the abandoned account left", got)
	}
	for _, p := range []string{"+905550000002", "+905550000003", "+905550000004"} {
		if got := count(`SELECT count(*) FROM "user" WHERE phone = $1`, p); got != 1 {
			t.Errorf("%s was swept", p)
		}
	}
	// a swept contact can sign up again
	signUp("+905550000001", "198.51.100.9")
	if got := count(`SELECT count(*) FROM "user" WHERE phone = '+905550000001' AND status = 'pending'`); got != 1 {
		t.Error("the contact could not sign up again")
	}
}

// e2eActive completes the pending sign-up of phone with the code it was sent.
func e2eActive(t *testing.T, svc *identity.Service, r *sms.Recorder, phone string) identity.User {
	t.Helper()
	u, err := svc.OTPSignIn(context.Background(), phone, lastCode(t, r), identity.Meta{IP: "203.0.113.50"})
	if err != nil || u.Status != "active" {
		t.Fatalf("verify sign-up: %v %+v", err, u)
	}
	return u
}

func e2eKnownUser(t *testing.T, svc *identity.Service, phone string) identity.User {
	t.Helper()
	u, err := svc.CreateUser(context.Background(), identity.CreateUserParams{Phone: phone})
	if err != nil {
		t.Fatal(err)
	}
	return u
}
