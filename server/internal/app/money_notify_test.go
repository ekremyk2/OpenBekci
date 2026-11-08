package app

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/ledger"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/db/dbtest"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// recordingNotifier is the jobs.Notifier the money notifier hands its job to.
type recordingNotifier struct{ got []jobs.NotifyArgs }

func (r *recordingNotifier) Notify(_ context.Context, _ pgx.Tx, a jobs.NotifyArgs) error {
	r.got = append(r.got, a)
	return nil
}

type notifyWorld struct {
	d                                *db.DB
	org, site, unit, emptyUnit       uuid.UUID
	owner, tenant, former, debtorPer uuid.UUID
	payment                          uuid.UUID
}

func seedNotifyWorld(t *testing.T) notifyWorld {
	t.Helper()
	d := dbtest.New(t)
	w := notifyWorld{d: d, org: id.New(), site: id.New(), unit: id.New(), emptyUnit: id.New(),
		owner: id.New(), tenant: id.New(), former: id.New(), debtorPer: id.New(), payment: id.New()}
	block, account := id.New(), id.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		err := d.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), sql, args...)
			return err
		})
		if err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	exec(`INSERT INTO organization (id, org_id, slug, name) VALUES ($1, $1, $2, 'Org')`, w.org, "org-"+w.org.String())
	exec(`INSERT INTO site (id, org_id, name, timezone) VALUES ($1, $2, 'Site', 'Europe/Istanbul')`, w.site, w.org)
	exec(`INSERT INTO block (id, org_id, site_id, name, hidden) VALUES ($1, $2, $3, 'Main', true)`, block, w.org, w.site)
	for _, u := range []struct {
		id   uuid.UUID
		door string
	}{{w.unit, "A-1"}, {w.emptyUnit, "A-2"}} {
		exec(`INSERT INTO unit (id, org_id, site_id, block_id, door_number) VALUES ($1, $2, $3, $4, $5)`, u.id, w.org, w.site, block, u.door)
	}
	for _, p := range []uuid.UUID{w.owner, w.tenant, w.former, w.debtorPer} {
		exec(`INSERT INTO person (id, org_id, display_name, contact) VALUES ($1, $2, 'P', '{}')`, p, w.org)
	}
	occ := func(person uuid.UUID, kind, from, to string) {
		var vt any
		if to != "" {
			vt = to
		}
		exec(`INSERT INTO occupancy (id, org_id, site_id, unit_id, person_id, kind, side, valid_from, valid_to) VALUES ($1, $2, $3, $4, $5, $6, $6, $7::date, $8::date)`,
			id.New(), w.org, w.site, w.unit, person, kind, from, vt)
	}
	occ(w.owner, "owner", "2020-01-01", "")
	occ(w.tenant, "tenant", "2020-01-01", "")
	occ(w.former, "tenant", "2010-01-01", "2015-01-01") // moved out: never a recipient
	exec(`INSERT INTO account (id, org_id, site_id, code, name, type, subtype) VALUES ($1, $2, $3, '100', 'Bank', 'asset', 'bank')`, account, w.org, w.site)
	exec(`INSERT INTO payment (id, org_id, site_id, debtor_kind, debtor_person_id, method, gross, net, received_on, asset_account_id)
		VALUES ($1, $2, $3, 'person', $4, 'cash', 1000, 1000, '2026-01-01', $5)`, w.payment, w.org, w.site, w.debtorPer, account)
	return w
}

func (w notifyWorld) enqueue(t *testing.T, typ string, payload map[string]any) []jobs.NotifyArgs {
	t.Helper()
	raw, _ := json.Marshal(payload)
	rec := &recordingNotifier{}
	err := w.d.InTenantTx(context.Background(), w.org, func(tx pgx.Tx) error {
		return moneyNotifier{out: rec}.Enqueue(context.Background(), tx, ledger.Notification{Type: typ, OrgID: w.org, SiteID: w.site, Payload: raw})
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec.got
}

func TestMoneyNotifierRecipients(t *testing.T) {
	w := seedNotifyWorld(t)
	ids := func(l ...uuid.UUID) []string {
		var s []string
		for _, i := range l {
			s = append(s, i.String())
		}
		sort.Strings(s)
		return s
	}
	try := func(minor int) map[string]any { return map[string]any{"amount": minor, "currency": "TRY"} }
	cases := []struct {
		name       string
		payload    map[string]any
		recipients []string
		wantUnit   string
	}{
		{"owner debtor: the current owners of the unit only",
			map[string]any{"payment_id": id.New().String(), "unit_id": w.unit.String(), "debtor_kind": "owner", "amount": try(125050), "receipt_no": "R-1"},
			ids(w.owner), "A-1"},
		{"tenant debtor: the current tenants only",
			map[string]any{"payment_id": id.New().String(), "unit_id": w.unit.String(), "debtor_kind": "tenant", "amount": try(125050), "receipt_no": "R-2"},
			ids(w.tenant), "A-1"},
		{"person debtor named in the payload",
			map[string]any{"payment_id": id.New().String(), "debtor_kind": "person", "person_id": w.debtorPer.String(), "amount": try(500), "receipt_no": "R-3"},
			ids(w.debtorPer), ""},
		{"person debtor read from the payment row (debtor_person_id)",
			map[string]any{"payment_id": w.payment.String()},
			ids(w.debtorPer), ""},
		{"a unit without residents has nobody to tell",
			map[string]any{"payment_id": id.New().String(), "unit_id": w.emptyUnit.String(), "debtor_kind": "owner"},
			nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := w.enqueue(t, ledger.NotifyPaymentConfirmed, c.payload)
			if c.recipients == nil {
				if len(got) != 0 {
					t.Fatalf("emitted %+v, want nothing", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("emitted %d jobs, want 1", len(got))
			}
			a := got[0]
			if a.Type != "payment.confirmed" || a.OrgID != w.org || a.SiteID == nil || *a.SiteID != w.site {
				t.Fatalf("args = %+v", a)
			}
			var rec []string
			for _, r := range a.Recipients {
				if r.PersonID == nil {
					t.Fatalf("recipient without a person: %+v", r)
				}
				rec = append(rec, r.PersonID.String())
			}
			sort.Strings(rec)
			if !reflect.DeepEqual(rec, c.recipients) {
				t.Fatalf("recipients = %v, want %v", rec, c.recipients)
			}
			// the money notifier leaves the link and the idempotency key to the
			// notification service (jobs.Notifier.Notify fills Key when empty)
			if a.Key != "" || a.Link != "" || len(a.Channels) != 0 {
				t.Fatalf("key, link and channels are the dispatcher's: %+v", a)
			}
			if c.wantUnit != "" && a.Payload["unit"] != c.wantUnit {
				t.Fatalf("payload unit = %v, want %s", a.Payload["unit"], c.wantUnit)
			}
			if _, ok := c.payload["amount"].(map[string]any); ok {
				if s, _ := a.Payload["amount"].(string); s == "" {
					t.Fatalf("amount not rendered for the template: %v", a.Payload["amount"])
				}
			}
			if rn, ok := c.payload["receipt_no"]; ok && a.Payload["receipt_no"] != rn {
				t.Fatalf("receipt_no = %v", a.Payload["receipt_no"])
			}
		})
	}
}

func TestMoneyNotifierMapsTypesAndChannels(t *testing.T) {
	w := seedNotifyWorld(t)
	got := w.enqueue(t, ledger.NotifyDunningReminder, map[string]any{
		"unit_id": w.unit.String(), "debtor_kind": "owner", "amount_due": map[string]any{"amount": 9900, "currency": "TRY"}, "channel": "sms",
	})
	if len(got) != 1 {
		t.Fatalf("emitted %d jobs, want 1", len(got))
	}
	a := got[0]
	if a.Type != "dunning.reminder" || !reflect.DeepEqual(a.Channels, []string{jobs.ChannelInApp, jobs.ChannelSMS}) {
		t.Fatalf("args = %+v", a)
	}
	if s, _ := a.Payload["amount"].(string); s == "" || s != a.Payload["amount_due"] {
		t.Fatalf("amount mirrors amount_due: %v / %v", a.Payload["amount"], a.Payload["amount_due"])
	}
}
