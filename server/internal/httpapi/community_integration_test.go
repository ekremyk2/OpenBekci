package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// --- recorders for the module's interfaces ---

type recNotifier struct {
	mu    sync.Mutex
	items []jobs.Notify
}

func (r *recNotifier) Notify(_ context.Context, _ pgx.Tx, n jobs.Notify) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, n)
	return nil
}

func (r *recNotifier) byTemplate(tpl string) []jobs.Notify {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []jobs.Notify
	for _, n := range r.items {
		if n.Template == tpl {
			out = append(out, n)
		}
	}
	return out
}

type recCharger struct {
	mu      sync.Mutex
	charges []community.BookingCharge
	refunds []community.BookingRefund
}

func (r *recCharger) ChargeBooking(_ context.Context, _ pgx.Tx, c community.BookingCharge) (uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.charges = append(r.charges, c)
	return id.New(), nil
}

func (r *recCharger) RefundBooking(_ context.Context, _ pgx.Tx, b community.BookingRefund) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refunds = append(r.refunds, b)
	return nil
}

type recHooks struct {
	mu      sync.Mutex
	decided []community.VisitorDecision
	changed int
}

func (r *recHooks) hooks() community.Hooks {
	return community.Hooks{
		OnVisitorDecided: func(_ context.Context, _ pgx.Tx, d community.VisitorDecision) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.decided = append(r.decided, d)
			return nil
		},
		OnVisitorChanged: func(context.Context, pgx.Tx, uuid.UUID, string, uuid.UUID) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.changed++
			return nil
		},
	}
}

// --- the test world ---

type commEnv struct {
	*idEnv
	t   *testing.T
	w   *authztest.World
	org authztest.Org
	f   authztest.SiteFix
	ip  atomic.Int64
	// tokens of the role actors (one console session each)
	manager, board, auditor, guard string
}

func newCommEnv(t *testing.T) *commEnv {
	t.Helper()
	e := newIDEnv(t, false, false)
	w := authztest.NewWorld(t, e.db, e.svc)
	c := &commEnv{idEnv: e, t: t, w: w, org: w.OrgA}
	c.f = w.NewSite(t, c.org)
	c.manager = w.Token(t, w.Actor("site_manager"))
	c.board = w.Token(t, w.Actor("board_member"))
	c.auditor = w.Token(t, w.Actor("auditor"))
	c.guard = w.Token(t, w.Actor("guard"))
	return c
}

func (c *commEnv) path(p string) string { return "/v1/orgs/" + c.org.ID.String() + p }

func (c *commEnv) req(token, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	hs := []string{"Content-Type", "application/json", "Authorization", "Bearer " + token}
	if token == "" {
		hs = []string{"Content-Type", "application/json"}
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = fmt.Sprintf("203.0.113.%d:4000", 1+c.ip.Add(1)%250)
	all := append(hs, headers...)
	for i := 0; i+1 < len(all); i += 2 {
		if all[i+1] != "" {
			r.Header.Set(all[i], all[i+1])
		}
	}
	c.h.ServeHTTP(rec, r)
	return rec
}

func (c *commEnv) idem() string { return fmt.Sprintf("it-key-%d", bSeq.Add(1)) }

// want asserts the status and decodes the JSON object of the response.
func (c *commEnv) want(rec *httptest.ResponseRecorder, status int) map[string]any {
	c.t.Helper()
	if rec.Code != status {
		c.t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body)
	}
	out := map[string]any{}
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			c.t.Fatalf("decode %s: %v", rec.Body, err)
		}
	}
	return out
}

// problemCode asserts a problem response and returns its error field codes.
func (c *commEnv) problemCode(rec *httptest.ResponseRecorder, status int, code string) []string {
	c.t.Helper()
	p := c.want(rec, status)
	if p["code"] != code {
		c.t.Fatalf("code = %v, want %s: %s", p["code"], code, rec.Body)
	}
	var fields []string
	if errs, ok := p["errors"].([]any); ok {
		for _, e := range errs {
			fields = append(fields, fmt.Sprint(e.(map[string]any)["code"]))
		}
	}
	return fields
}

// resident makes a user with a person and an occupancy of kind on the unit and returns their token and person.
func (c *commEnv) resident(name string, unit uuid.UUID, kind string) (string, uuid.UUID) {
	c.t.Helper()
	u, err := c.svc.CreateUser(context.Background(), identity.CreateUserParams{Email: name + "@resident.example.test"})
	if err != nil {
		c.t.Fatal(err)
	}
	a := c.w.Person(c.t, c.org, authztest.Actor{Name: name, UserID: u.ID})
	c.w.NewOccupancy(c.t, c.f, unit, a.PersonID, kind)
	return c.w.Token(c.t, a), a.PersonID
}

func istanbul(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// slot is hour:00 Istanbul time, days ahead.
func slot(t *testing.T, daysAhead, hour int) time.Time {
	t.Helper()
	n := time.Now().In(istanbul(t)).AddDate(0, 0, daysAhead)
	return time.Date(n.Year(), n.Month(), n.Day(), hour, 0, 0, 0, istanbul(t))
}

func (c *commEnv) facility(rules string) string {
	c.t.Helper()
	body := fmt.Sprintf(`{"site_id":%q,"name":"Community hall","description":"The hall","rules":%s}`, c.f.Site, rules)
	return c.want(c.req(c.manager, "POST", c.path("/facilities"), body), 201)["id"].(string)
}

const hallRules = `{"opening_hours":[{"days":[1,2,3,4,5,6,7],"start":"08:00","end":"22:00"}],"max_duration_minutes":240,"max_days_ahead":30,"min_notice_minutes":60,` +
	`"quota_per_unit":2,"quota_period":"month","who_may_book":["owner","tenant"],"fee":{"amount":20000,"currency":"TRY"},"cancel_refund_cutoff_hours":24}`

func (c *commEnv) book(token, facility string, unit uuid.UUID, from, to time.Time) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"facility_id":%q,"unit_id":%q,"start":%q,"end":%q}`, facility, unit, rfc(from), rfc(to))
	return c.req(token, "POST", c.path("/bookings"), body, "Idempotency-Key", c.idem())
}

// --- bookings (C1) ---

func TestIntegrationCommunityBookingRules(t *testing.T) {
	c := newCommEnv(t)
	fid := c.facility(hallRules)
	owner, ownerPerson := c.resident("owner1", c.f.Unit, "owner")
	household, _ := c.resident("household1", c.f.Unit, "household")
	_ = ownerPerson

	// within the rules: confirmed at once, the fee becomes a charge on the booker's side
	d := 5
	b := c.want(c.book(owner, fid, c.f.Unit, slot(t, d, 10), slot(t, d, 12)), 201)
	if b["status"] != "confirmed" || b["fee"].(map[string]any)["amount"].(float64) != 20000 || b["charge_id"] == nil {
		t.Fatalf("booking = %v", b)
	}
	if len(c.charge.charges) != 1 || c.charge.charges[0].Side != "owner" || c.charge.charges[0].Amount.Amount != 20000 {
		t.Fatalf("charges = %+v", c.charge.charges)
	}

	// the rules: hours, duration, notice, horizon, who may book, taken slot, quota
	cases := []struct {
		name     string
		token    string
		from, to time.Time
		status   int
		code     string
		field    string
	}{
		{"before opening", owner, slot(t, d, 6), slot(t, d, 7), 422, "validation_failed", "outside_opening_hours"},
		{"too long", owner, slot(t, d+1, 9), slot(t, d+1, 14), 422, "validation_failed", "too_long"},
		{"too soon", owner, time.Now().Add(10 * time.Minute), time.Now().Add(70 * time.Minute), 422, "validation_failed", "too_soon"},
		{"too far ahead", owner, slot(t, 60, 10), slot(t, 60, 11), 422, "validation_failed", "too_far_ahead"},
		{"household may not book", household, slot(t, d+1, 10), slot(t, d+1, 11), 422, "validation_failed", "not_allowed_to_book"},
		{"overlaps the first booking", owner, slot(t, d, 11), slot(t, d, 13), 409, "conflict", ""},
	}
	for _, tc := range cases {
		rec := c.book(tc.token, fid, c.f.Unit, tc.from, tc.to)
		fields := c.problemCode(rec, tc.status, tc.code)
		if tc.field != "" && !contains(fields, tc.field) {
			t.Errorf("%s: field codes %v, want %s", tc.name, fields, tc.field)
		}
	}
	// the quota: two per month for the unit
	c.want(c.book(owner, fid, c.f.Unit, slot(t, d, 14), slot(t, d, 15)), 201)
	if fields := c.problemCode(c.book(owner, fid, c.f.Unit, slot(t, d, 16), slot(t, d, 17)), 422, "validation_failed"); !contains(fields, "quota_exceeded") {
		t.Errorf("quota: %v", fields)
	}
	// a resident cannot book for a unit they do not live in
	other := c.w.NewUnit(t, c.f)
	c.problemCode(c.book(owner, fid, other, slot(t, d, 18), slot(t, d, 19)), 403, "insufficient_permission")

	// residents see slots as taken, never names; the manager sees the booker
	stranger, _ := c.resident("owner2", other, "owner")
	av := c.want(c.req(stranger, "GET", c.path("/facilities/"+fid+"/availability?from="+rfc(slot(t, d, 0))+"&to="+rfc(slot(t, d+1, 0))), ""), 200)
	if taken := av["taken"].([]any); len(taken) != 2 {
		t.Fatalf("taken = %v", taken)
	}
	if strings.Contains(c.req(stranger, "GET", c.path("/facilities/"+fid+"/availability?from="+rfc(slot(t, d, 0))+"&to="+rfc(slot(t, d+1, 0))), "").Body.String(), "owner1") {
		t.Fatal("availability leaked a name")
	}
	list := c.want(c.req(stranger, "GET", c.path("/bookings?site_id="+c.f.Site.String()), ""), 200)
	if n := len(list["items"].([]any)); n != 0 {
		t.Fatalf("a resident of another unit sees %d bookings", n)
	}
	mine := c.want(c.req(owner, "GET", c.path("/bookings?site_id="+c.f.Site.String()), ""), 200)
	if n := len(mine["items"].([]any)); n != 2 {
		t.Fatalf("the booker sees %d bookings, want 2", n)
	}
	all := c.want(c.req(c.manager, "GET", c.path("/bookings?site_id="+c.f.Site.String()+"&status=confirmed"), ""), 200)
	first := all["items"].([]any)[0].(map[string]any)
	if first["person_display_name"] == nil {
		t.Fatalf("the manager must see who booked: %v", first)
	}
	// another resident's single booking read is a 404, the booker's is fine
	bid := b["id"].(string)
	c.want(c.req(stranger, "GET", c.path("/bookings/"+bid), ""), 404)
	got := c.want(c.req(owner, "GET", c.path("/bookings/"+bid), ""), 200)
	if got["person_display_name"] == nil {
		t.Fatal("the booker sees their own name")
	}

	// blocked dates: a booking inside is refused; a block over a booking is a conflict that is not forced
	s := slot(t, d+2, 10)
	c.want(c.req(c.manager, "POST", c.path("/facilities/"+fid+"/blocks"), fmt.Sprintf(`{"start":%q,"end":%q,"reason":"Painting"}`, rfc(s), rfc(s.Add(3*time.Hour)))), 201)
	c.problemCode(c.book(owner, fid, c.f.Unit, slot(t, d+2, 11), slot(t, d+2, 12)), 409, "conflict")
	c.problemCode(c.req(c.manager, "POST", c.path("/facilities/"+fid+"/blocks"), fmt.Sprintf(`{"start":%q,"end":%q}`, rfc(slot(t, d, 9)), rfc(slot(t, d, 13)))), 409, "conflict")
	av = c.want(c.req(owner, "GET", c.path("/facilities/"+fid+"/availability?from="+rfc(slot(t, d+2, 0))+"&to="+rfc(slot(t, d+3, 0))), ""), 200)
	if len(av["blocked"].([]any)) != 1 {
		t.Fatalf("blocked = %v", av["blocked"])
	}
	// a resident cannot manage facilities
	c.problemCode(c.req(owner, "POST", c.path("/facilities"), fmt.Sprintf(`{"site_id":%q,"name":"X","rules":%s}`, c.f.Site, hallRules)), 403, "insufficient_permission")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestIntegrationCommunityBookingCancelRefund(t *testing.T) {
	c := newCommEnv(t)
	fid := c.facility(hallRules)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")

	// cancelled before the 24 h cutoff: the fee is refunded automatically
	far := c.want(c.book(owner, fid, c.f.Unit, slot(t, 6, 10), slot(t, 6, 11)), 201)
	done := c.want(c.req(owner, "POST", c.path("/bookings/"+far["id"].(string)+":cancel"), `{"reason":"plans changed"}`, "Idempotency-Key", c.idem()), 200)
	if done["status"] != "cancelled" || done["refund"] == nil || len(c.charge.refunds) != 1 || c.charge.refunds[0].Amount.Amount != 20000 {
		t.Fatalf("cancel before cutoff: %v refunds=%+v", done, c.charge.refunds)
	}
	// the slot is free again
	c.want(c.book(owner, fid, c.f.Unit, slot(t, 6, 10), slot(t, 6, 11)), 201)
	// cancelling twice is a conflict
	c.problemCode(c.req(owner, "POST", c.path("/bookings/"+far["id"].(string)+":cancel"), ``, "Idempotency-Key", c.idem()), 409, "conflict")

	// inside the cutoff: kept (a booking 3 hours away needs a short notice rule, so seed it)
	near := id.New()
	c.w.Exec(t, `INSERT INTO booking (id, org_id, site_id, facility_id, unit_id, start_at, end_at, debtor_kind, charge_id, fee_amount, fee_currency)
		VALUES ($1, $2, $3, $4, $5, now() + interval '3 hours', now() + interval '4 hours', 'owner', $6, 20000, 'TRY')`, near, c.org.ID, c.f.Site, uuid.MustParse(fid), c.f.Unit, id.New())
	kept := c.want(c.req(owner, "POST", c.path("/bookings/"+near.String()+":cancel"), ``, "Idempotency-Key", c.idem()), 200)
	if kept["status"] != "cancelled" || kept["refund"] != nil || len(c.charge.refunds) != 1 {
		t.Fatalf("cancel inside cutoff: %v", kept)
	}
	// a manager cancelling someone else's booking refunds it in full
	again := c.want(c.book(owner, fid, c.f.Unit, slot(t, 7, 10), slot(t, 7, 11)), 201)
	mgr := c.want(c.req(c.manager, "POST", c.path("/bookings/"+again["id"].(string)+":cancel"), `{"reason":"maintenance"}`, "Idempotency-Key", c.idem()), 200)
	if mgr["refund"] == nil || len(c.charge.refunds) != 2 {
		t.Fatalf("manager cancel: %v", mgr)
	}
}

// Two confirmed bookings of one facility can never overlap, however the
// requests race: the exclusion constraint is the last line, the facility lock
// makes the losers a clean 409.
func TestIntegrationCommunityBookingRace(t *testing.T) {
	c := newCommEnv(t)
	open := `{"opening_hours":[{"days":[1,2,3,4,5,6,7],"start":"00:00","end":"00:00"}],"max_duration_minutes":480,"max_days_ahead":30,"min_notice_minutes":null,"quota_per_unit":null,"quota_period":null,"who_may_book":["owner"],"fee":null,"cancel_refund_cutoff_hours":null}`
	fid := c.facility(open)
	start := slot(t, 4, 10)
	var wg sync.WaitGroup
	var created, conflicts atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := c.book(c.manager, fid, c.f.Unit, start, start.Add(2*time.Hour))
			switch rec.Code {
			case 201:
				created.Add(1)
			case 409:
				conflicts.Add(1)
			default:
				t.Errorf("unexpected %d: %s", rec.Code, rec.Body)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 || conflicts.Load() != 9 {
		t.Fatalf("created %d, conflicts %d; want 1 and 9", created.Load(), conflicts.Load())
	}
	// below the service: the database itself refuses an overlapping confirmed booking, but not a cancelled one
	err := c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO booking (id, org_id, site_id, facility_id, unit_id, start_at, end_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id.New(), c.org.ID, c.f.Site, uuid.MustParse(fid), c.f.Unit, start.Add(time.Hour), start.Add(3*time.Hour))
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "booking_no_overlap") {
		t.Fatalf("overlapping insert: %v", err)
	}
	c.w.Exec(t, `INSERT INTO booking (id, org_id, site_id, facility_id, unit_id, start_at, end_at, status) VALUES ($1, $2, $3, $4, $5, $6, $7, 'cancelled')`,
		id.New(), c.org.ID, c.f.Site, uuid.MustParse(fid), c.f.Unit, start.Add(time.Hour), start.Add(3*time.Hour))
	// a facility with bookings cannot be deleted, only disabled
	c.problemCode(c.req(c.manager, "DELETE", c.path("/facilities/"+fid), "", "If-Match", `"1"`), 409, "conflict")
}

// --- visitors (C2) ---

func TestIntegrationCommunityVisitorQRAdmission(t *testing.T) {
	c := newCommEnv(t)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	other := c.w.NewUnit(t, c.f)
	stranger, _ := c.resident("owner2", other, "owner")

	from, to := time.Now().Add(-10*time.Minute), time.Now().Add(2*time.Hour)
	body := fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Mehmet Kaya","plate":"34 abc 123","window_from":%q,"window_to":%q}`, c.f.Site, c.f.Unit, rfc(from), rfc(to))
	e := c.want(c.req(owner, "POST", c.path("/visitor-entries"), body), 201)
	qr, _ := e["qr_token"].(string)
	if !strings.HasPrefix(qr, "obk1.") || e["plate"] != "34ABC123" || e["approval_status"] != "pre_approved" || e["source"] != "pre_registered" {
		t.Fatalf("pre-registration = %v", e)
	}
	entryID := e["id"].(string)

	// the token is shown to the current occupants of the unit and to nobody else:
	// not to managers, board members or auditors, not in the list either
	if got := c.want(c.req(owner, "GET", c.path("/visitor-entries/"+entryID), ""), 200); got["qr_token"] != qr {
		t.Fatalf("the unit's people see the QR again: %v", got["qr_token"])
	}
	member, _ := c.resident("household1", c.f.Unit, "household")
	if got := c.want(c.req(member, "GET", c.path("/visitor-entries/"+entryID), ""), 200); got["qr_token"] != qr {
		t.Fatal("a household member of the unit sees the QR")
	}
	for role, tok := range map[string]string{"manager": c.manager, "board": c.board, "auditor": c.auditor} {
		if got := c.want(c.req(tok, "GET", c.path("/visitor-entries/"+entryID), ""), 200); got["qr_token"] != nil {
			t.Fatalf("%s must not see the token on GET", role)
		}
		list := c.want(c.req(tok, "GET", c.path("/visitor-entries?site_id="+c.f.Site.String()), ""), 200)
		if items, _ := list["items"].([]any); len(items) == 0 {
			t.Fatalf("%s: the entry is missing from the list", role)
		} else {
			for _, it := range items {
				if it.(map[string]any)["qr_token"] != nil {
					t.Fatalf("%s must not see a token in the list", role)
				}
			}
		}
	}
	c.want(c.req(stranger, "GET", c.path("/visitor-entries/"+entryID), ""), 404)
	c.problemCode(c.req(c.guard, "GET", c.path("/visitor-entries/"+entryID), ""), 403, "insufficient_permission")

	// rotating: managers and board have the operation but never receive the new token; the occupants do, and the old QR stops
	for role, tok := range map[string]string{"manager": c.manager, "board": c.board} {
		rot := c.want(c.req(tok, "POST", c.path("/visitor-entries/"+entryID+":rotate-qr"), ""), 200)
		if rot["qr_token"] != nil {
			t.Fatalf("%s got the rotated token", role)
		}
	}
	c.problemCode(c.req(c.auditor, "POST", c.path("/visitor-entries/"+entryID+":rotate-qr"), ""), 403, "insufficient_permission")
	c.problemCode(c.req(c.guard, "POST", c.path("/visitor-entries/"+entryID+":rotate-qr"), ""), 403, "insufficient_permission")
	c.want(c.req(stranger, "POST", c.path("/visitor-entries/"+entryID+":rotate-qr"), ""), 404)
	reissued := c.want(c.req(owner, "GET", c.path("/visitor-entries/"+entryID), ""), 200)["qr_token"]
	if reissued == nil || reissued == qr {
		t.Fatalf("the occupants should hold a new token, got %v", reissued)
	}
	if res := c.want(c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, qr)), 200); res["valid"] != false {
		t.Fatalf("the old QR must not scan after a rotation: %v", res)
	}
	own := c.want(c.req(owner, "POST", c.path("/visitor-entries/"+entryID+":rotate-qr"), ""), 200)
	if own["qr_token"] == nil || own["qr_token"] == reissued {
		t.Fatalf("an occupant who rotates gets the new token: %v", own["qr_token"])
	}
	qr = own["qr_token"].(string)
	// only the hash is a lookup key
	var hashLen int
	_ = c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT length(qr_token_hash) FROM visitor_entry WHERE id = $1`, entryID).Scan(&hashLen)
	})
	if hashLen != 32 {
		t.Fatalf("stored hash length = %d", hashLen)
	}

	// the guard scans: valid now, minimal answer
	scan := c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, qr))
	res := c.want(scan, 200)
	if res["valid"] != true || res["reason"] != "match" || res["entry"].(map[string]any)["unit_door_number"] != c.f.DoorNum {
		t.Fatalf("scan = %v", res)
	}
	assertMinimal(t, scan.Body.Bytes())
	// admit; a second arrival and a second scan are refused
	arrive := c.req(c.guard, "POST", c.path("/gate/visitors/"+entryID+":arrive"), "")
	if v := c.want(arrive, 200); v["arrived_at"] == nil {
		t.Fatalf("arrive = %v", v)
	}
	assertMinimal(t, arrive.Body.Bytes())
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors/"+entryID+":arrive"), ""), 409, "conflict")
	if res := c.want(c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, qr)), 200); res["valid"] != false || res["reason"] != "used_up" {
		t.Fatalf("second scan = %v", res)
	}
	left := c.want(c.req(c.guard, "POST", c.path("/gate/visitors/"+entryID+":leave"), ""), 200)
	if left["left_at"] == nil {
		t.Fatalf("leave = %v", left)
	}
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors/"+entryID+":leave"), ""), 409, "conflict")

	// the other verdicts
	mk := func(name string, from, to time.Time) (id, qr string) {
		e := c.want(c.req(owner, "POST", c.path("/visitor-entries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":%q,"window_from":%q,"window_to":%q}`, c.f.Site, c.f.Unit, name, rfc(from), rfc(to))), 201)
		return e["id"].(string), e["qr_token"].(string)
	}
	verdict := func(qr string) string {
		return c.want(c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, qr)), 200)["reason"].(string)
	}
	_, later := mk("Later", time.Now().Add(3*time.Hour), time.Now().Add(4*time.Hour))
	if got := verdict(later); got != "outside_window" {
		t.Errorf("before the window: %s", got)
	}
	_, soon := mk("Soon", time.Now().Add(40*time.Minute), time.Now().Add(2*time.Hour))
	if got := verdict(soon); got != "outside_window" {
		t.Errorf("more than 30 minutes early: %s", got)
	}
	_, nearly := mk("Nearly", time.Now().Add(20*time.Minute), time.Now().Add(2*time.Hour))
	if got := verdict(nearly); got != "match" {
		t.Errorf("within the 30 minute slack: %s", got)
	}
	cancelID, cancelQR := mk("Cancelled", from, to)
	c.want(c.req(owner, "POST", c.path("/visitor-entries/"+cancelID+":cancel"), ""), 200)
	if got := verdict(cancelQR); got != "revoked" {
		t.Errorf("cancelled: %s", got)
	}
	if got := verdict("obk1.0123456789012345678901234567890123456789012"); got != "unknown" {
		t.Errorf("unknown token: %s", got)
	}
	if got := verdict("not a qr"); got != "unknown" {
		t.Errorf("not a QR: %s", got)
	}
	// update before arrival: plate re-normalized; a used entry cannot change
	upd := c.want(c.req(owner, "PATCH", c.path("/visitor-entries/"+nearly2(c, owner)), `{"plate":"06 sö 1234"}`, "If-Match", `"1"`), 200)
	if upd["plate"] != "06SO1234" {
		t.Fatalf("update = %v", upd)
	}
	c.problemCode(c.req(owner, "PATCH", c.path("/visitor-entries/"+entryID), `{"name":"X"}`, "If-Match", `"1"`), 409, "version_conflict")

	// a resident cannot register a visitor for another unit
	c.problemCode(c.req(owner, "POST", c.path("/visitor-entries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"X","window_from":%q,"window_to":%q}`, c.f.Site, other, rfc(from), rfc(to))), 404, "not_found")
}

func nearly2(c *commEnv, owner string) string {
	e := c.want(c.req(owner, "POST", c.path("/visitor-entries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Edit me","window_from":%q,"window_to":%q}`,
		c.f.Site, c.f.Unit, rfc(time.Now()), rfc(time.Now().Add(time.Hour)))), 201)
	return e["id"].(string)
}

// assertMinimal fails when a guard response carries what guards never see:
// contacts, national ids, balances or QR tokens. It scans JSON keys, not text.
func assertMinimal(t *testing.T, body []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	var walk func(any, string)
	walk = func(x any, path string) {
		switch x := x.(type) {
		case map[string]any:
			for k, child := range x {
				for _, bad := range []string{"phone", "email", "contact", "national", "tckn", "balance", "debt", "qr_token", "token", "iban", "address", "person_id", "display_name", "notes"} {
					if strings.Contains(strings.ToLower(k), bad) {
						t.Errorf("guard response has a forbidden key %q at %s: %s", k, path, body)
					}
				}
				walk(child, path+"."+k)
			}
		case []any:
			for i, child := range x {
				walk(child, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(v, "$")
}

func TestIntegrationCommunityRecurringVisitors(t *testing.T) {
	c := newCommEnv(t)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	body := fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Cleaner","plate":"34 CLN 01","days":[1,2,3,4,5,6,7],"start":"00:00","end":"23:59","valid_from":"2026-01-01"}`, c.f.Site, c.f.Unit)
	r := c.want(c.req(owner, "POST", c.path("/recurring-visitors"), body), 201)
	qr := r["qr_token"].(string)
	if !strings.HasPrefix(qr, "obk1.") || r["status"] != "active" {
		t.Fatalf("recurring = %v", r)
	}
	rvID := r["id"].(string)
	// the long-lived QR is shown to the unit's occupants only; managers rotate it instead
	for role, tok := range map[string]string{"manager": c.manager, "board": c.board, "auditor": c.auditor} {
		if got := c.want(c.req(tok, "GET", c.path("/recurring-visitors/"+rvID), ""), 200); got["qr_token"] != nil {
			t.Fatalf("%s must not see the recurring token on GET", role)
		}
		list := c.want(c.req(tok, "GET", c.path("/recurring-visitors?site_id="+c.f.Site.String()), ""), 200)
		for _, it := range list["items"].([]any) {
			if it.(map[string]any)["qr_token"] != nil {
				t.Fatalf("%s must not see a recurring token in the list", role)
			}
		}
	}
	c.problemCode(c.req(c.guard, "GET", c.path("/recurring-visitors/"+rvID), ""), 403, "insufficient_permission")
	if rot := c.want(c.req(c.manager, "POST", c.path("/recurring-visitors/"+rvID+":rotate-qr"), ""), 200); rot["qr_token"] != nil {
		t.Fatal("a manager must not receive the rotated recurring token")
	}
	c.problemCode(c.req(c.auditor, "POST", c.path("/recurring-visitors/"+rvID+":rotate-qr"), ""), 403, "insufficient_permission")
	if got := c.want(c.req(owner, "GET", c.path("/recurring-visitors/"+rvID), ""), 200); got["qr_token"] == nil || got["qr_token"] == qr {
		t.Fatalf("the occupant should hold the new recurring token: %v", got["qr_token"])
	} else {
		qr = got["qr_token"].(string)
	}
	// scanning creates (once) the visit; arriving logs it; a second scan after arrival makes a new visit
	scan := func() map[string]any {
		return c.want(c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, qr)), 200)
	}
	s1, s2 := scan(), scan()
	e1, e2 := s1["entry"].(map[string]any), s2["entry"].(map[string]any)
	if s1["valid"] != true || e1["recurring"] != true || e1["id"] != e2["id"] {
		t.Fatalf("scans = %v / %v", s1, s2)
	}
	c.want(c.req(c.guard, "POST", c.path("/gate/visitors/"+e1["id"].(string)+":arrive"), ""), 200)
	if s3 := scan(); s3["entry"].(map[string]any)["id"] == e1["id"] {
		t.Fatal("the next visit is a new entry")
	}
	// visit list of the unit shows the visits; update then revoke takes effect at the gate at once
	upd := c.want(c.req(owner, "PATCH", c.path("/recurring-visitors/"+rvID), `{"end":"23:30"}`, "If-Match", `"2"`), 200)
	if upd["end"] != "23:30" {
		t.Fatalf("update = %v", upd)
	}
	rev := c.want(c.req(owner, "POST", c.path("/recurring-visitors/"+rvID+":revoke"), ""), 200)
	if rev["status"] != "revoked" {
		t.Fatalf("revoke = %v", rev)
	}
	if res := scan(); res["valid"] != false || res["reason"] != "revoked" {
		t.Fatalf("scan after revoke = %v", res)
	}
	c.problemCode(c.req(owner, "POST", c.path("/recurring-visitors/"+rvID+":revoke"), ""), 409, "conflict")
	// an invalid rule
	bad := strings.Replace(body, `"days":[1,2,3,4,5,6,7]`, `"days":[0]`, 1)
	c.problemCode(c.req(owner, "POST", c.path("/recurring-visitors"), bad), 422, "validation_failed")
	// a rule that does not hold today
	iso := int(time.Now().In(istanbul(t)).Weekday())
	if iso == 0 {
		iso = 7
	}
	day := iso%7 + 1 // the next ISO weekday, which is not today's
	r2 := c.want(c.req(owner, "POST", c.path("/recurring-visitors"), strings.Replace(body, `[1,2,3,4,5,6,7]`, fmt.Sprintf("[%d]", day), 1)), 201)
	if res := c.want(c.req(c.guard, "POST", c.path("/gate/visitors:scan"), fmt.Sprintf(`{"token":%q}`, r2["qr_token"])), 200); res["reason"] != "outside_window" {
		t.Fatalf("scan on the wrong weekday = %v", res)
	}
}

// --- walk-in approval, end to end ---

func TestIntegrationCommunityWalkInApproval(t *testing.T) {
	c := newCommEnv(t)
	owner, ownerPerson := c.resident("owner1", c.f.Unit, "owner")
	c.w.Exec(t, `UPDATE person SET gate_contact_phone = '+905550001122' WHERE id = $1`, ownerPerson)
	other := c.w.NewUnit(t, c.f)
	stranger, _ := c.resident("owner2", other, "owner")

	walk := c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Courier Ali","plate":"34 zz 999"}`, c.f.Site, c.f.Unit))
	w1 := c.want(walk, 201)
	assertMinimal(t, walk.Body.Bytes())
	if w1["approval_status"] != "requested" || w1["approval_expires_at"] == nil {
		t.Fatalf("walk-in = %v", w1)
	}
	entryID := w1["id"].(string)
	// the resident was notified with an approve / deny link
	notes := c.notes.byTemplate("visitor.approval_requested")
	if len(notes) != 1 || len(notes[0].PersonIDs) != 1 || notes[0].PersonIDs[0] != ownerPerson || !strings.HasPrefix(notes[0].Data["link"].(string), testBase) {
		t.Fatalf("notifications = %+v", notes)
	}
	// the guard cannot arrive the visitor yet; the call contact is the shared gate number only
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors/"+entryID+":arrive"), ""), 409, "conflict")
	cc := c.req(c.guard, "GET", c.path("/gate/visitors/"+entryID+"/call-contact"), "")
	contact := c.want(cc, 200)
	if contact["phone"] != "+905550001122" || len(contact) != 2 {
		t.Fatalf("call contact = %v", contact)
	}
	// the resident (and only the unit's people) see and answer the request from the portal
	reqs := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests?status=pending"), ""), 200)["items"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["visitor_name"] != "Courier Ali" || reqs[0].(map[string]any)["unit_door_number"] != c.f.DoorNum {
		t.Fatalf("requests = %v", reqs)
	}
	requestID := reqs[0].(map[string]any)["id"].(string)
	if n := len(c.want(c.req(stranger, "GET", c.path("/visitor-approval-requests"), ""), 200)["items"].([]any)); n != 0 {
		t.Fatalf("another unit sees %d requests", n)
	}
	c.want(c.req(stranger, "POST", c.path("/visitor-approval-requests/"+requestID+":decide"), `{"decision":"approve"}`), 404)
	dec := c.want(c.req(owner, "POST", c.path("/visitor-approval-requests/"+requestID+":decide"), `{"decision":"approve"}`), 200)
	if dec["status"] != "approved" || dec["decided_by"] != ownerPerson.String() {
		t.Fatalf("decision = %v", dec)
	}
	c.problemCode(c.req(owner, "POST", c.path("/visitor-approval-requests/"+requestID+":decide"), `{"decision":"deny"}`), 409, "conflict")
	if len(c.hooks.decided) != 1 || !c.hooks.decided[0].Approved || c.hooks.decided[0].DecidedBy != ownerPerson {
		t.Fatalf("OnVisitorDecided = %+v", c.hooks.decided)
	}
	// the guard polls, then admits
	polled := c.want(c.req(c.guard, "GET", c.path("/gate/visitors/"+entryID), ""), 200)
	if polled["approval_status"] != "approved" {
		t.Fatalf("polled = %v", polled)
	}
	c.problemCode(c.req(c.guard, "GET", c.path("/gate/visitors/"+entryID+"/call-contact"), ""), 409, "conflict")
	arr := c.req(c.guard, "POST", c.path("/gate/visitors/"+entryID+":arrive"), "")
	if v := c.want(arr, 200); v["arrived_at"] == nil {
		t.Fatalf("arrive = %v", v)
	}
	assertMinimal(t, arr.Body.Bytes())
	// the audit trail names the decision
	if n := c.countAudit("visitor.decide", requestID); n != 1 {
		t.Fatalf("decide audit events = %d", n)
	}

	// deny: the entry is denied and never admitted
	w2 := c.want(c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Door to door"}`, c.f.Site, c.f.Unit)), 201)
	r2 := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests?status=pending"), ""), 200)["items"].([]any)[0].(map[string]any)["id"].(string)
	c.want(c.req(owner, "POST", c.path("/visitor-approval-requests/"+r2+":decide"), `{"decision":"deny"}`), 200)
	if v := c.want(c.req(c.guard, "GET", c.path("/gate/visitors/"+w2["id"].(string)), ""), 200); v["approval_status"] != "denied" {
		t.Fatalf("denied = %v", v)
	}
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors/"+w2["id"].(string)+":arrive"), ""), 409, "conflict")

	// no answer in time: the answer is a conflict, the entry expires, the call contact closes
	w3 := c.want(c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Late"}`, c.f.Site, c.f.Unit)), 201)
	r3 := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests?status=pending"), ""), 200)["items"].([]any)[0].(map[string]any)["id"].(string)
	c.w.Exec(t, `UPDATE visitor_approval_request SET expires_at = now() - interval '1 minute' WHERE id = $1`, r3)
	c.problemCode(c.req(owner, "POST", c.path("/visitor-approval-requests/"+r3+":decide"), `{"decision":"approve"}`), 409, "conflict")
	if v := c.want(c.req(c.guard, "GET", c.path("/gate/visitors/"+w3["id"].(string)), ""), 200); v["approval_status"] != "expired" {
		t.Fatalf("lazily expired = %v", v)
	}
	if n, err := c.comm().ExpireVisitorsOrg(context.Background(), c.org.ID); err != nil || n < 1 {
		t.Fatalf("expiry job: %d %v", n, err)
	}
	if st := c.want(c.req(owner, "GET", c.path("/visitor-approval-requests?status=expired"), ""), 200)["items"].([]any); len(st) != 1 {
		t.Fatalf("expired requests = %v", st)
	}
	var status string
	_ = c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT approval_status FROM visitor_entry WHERE id = $1`, w3["id"]).Scan(&status)
	})
	if status != "expired" {
		t.Fatalf("stored entry status = %s", status)
	}
	// a unit nobody lives in: nobody to ask
	empty := c.w.NewUnit(t, c.f)
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/visitors:walk-in"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Nobody"}`, c.f.Site, empty)), 409, "conflict")
}

func (c *commEnv) comm() *community.Service { return c.idEnv.comm }

func (c *commEnv) countAudit(action, target string) int {
	c.t.Helper()
	var n int
	err := c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE org_id = $1 AND action = $2 AND target_id = $3`, c.org.ID, action, target).Scan(&n)
	})
	if err != nil {
		c.t.Fatal(err)
	}
	return n
}

// --- guard console: sessions, PIN limits, attribution ---

func TestIntegrationCommunityGuardSessions(t *testing.T) {
	c := newCommEnv(t)
	guardActor := c.w.Actor("guard")
	setPin := func(person uuid.UUID, pin string) {
		c.want(c.req(c.manager, "PUT", c.path("/persons/"+person.String()+"/gate-pin"), fmt.Sprintf(`{"pin":%q}`, pin)), 204)
	}
	session := func(token string, person uuid.UUID, pin string) *httptest.ResponseRecorder {
		return c.req(token, "POST", c.path("/gate/sessions"), fmt.Sprintf(`{"site_id":%q,"person_id":%q,"pin":%q}`, c.f.Site, person, pin))
	}

	// the booth lists guards by name and id only
	guards := c.req(c.guard, "GET", c.path("/gate/guards?site_id="+c.f.Site.String()), "")
	items := c.want(guards, 200)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("guards = %v", items)
	}
	g := items[0].(map[string]any)
	if len(g) != 3 || g["person_id"] != guardActor.PersonID.String() || g["has_pin"] != false {
		t.Fatalf("guard ref = %v", g)
	}
	// a PIN is 4 to 8 digits; a guard without one cannot switch in; managers set it, guards cannot
	c.problemCode(c.req(c.manager, "PUT", c.path("/persons/"+guardActor.PersonID.String()+"/gate-pin"), `{"pin":"12"}`), 422, "validation_failed")
	c.problemCode(c.req(c.guard, "PUT", c.path("/persons/"+guardActor.PersonID.String()+"/gate-pin"), `{"pin":"1234"}`), 403, "insufficient_permission")
	c.problemCode(session(c.guard, guardActor.PersonID, "2468"), 401, "invalid_grant")
	nonGuard := c.w.NewPerson(t, c.org)
	c.problemCode(c.req(c.manager, "PUT", c.path("/persons/"+nonGuard.String()+"/gate-pin"), `{"pin":"1234"}`), 409, "conflict")
	setPin(guardActor.PersonID, "2468")
	if g := c.want(c.req(c.guard, "GET", c.path("/gate/guards?site_id="+c.f.Site.String()), ""), 200)["items"].([]any)[0].(map[string]any); g["has_pin"] != true {
		t.Fatalf("has_pin = %v", g)
	}
	// the PIN itself is stored hashed
	var hash string
	_ = c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT pin_hash FROM gate_pin WHERE person_id = $1`, guardActor.PersonID).Scan(&hash)
	})
	if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, "2468") {
		t.Fatalf("pin hash = %q", hash)
	}

	// a wrong PIN, an unknown person and a person who is not a guard answer the same
	for _, p := range []uuid.UUID{uuid.New(), nonGuard} {
		rec := session(c.board, p, "2468") // board holds no gate.operate: refused before the PIN is looked at
		c.problemCode(rec, 403, "insufficient_permission")
	}
	consoleB := c.w.Token(t, guardActor)
	bodies := map[string]bool{}
	for _, p := range []uuid.UUID{uuid.New(), nonGuard} {
		rec := session(consoleB, p, "2468")
		c.problemCode(rec, 401, "invalid_grant")
		bodies[strings.SplitN(rec.Body.String(), `"instance"`, 2)[0]] = true
	}
	if rec := session(consoleB, guardActor.PersonID, "1111"); rec.Code != 401 {
		t.Fatalf("wrong pin: %d", rec.Code)
	} else {
		bodies[strings.SplitN(rec.Body.String(), `"instance"`, 2)[0]] = true
	}
	if len(bodies) != 1 {
		t.Fatalf("the failures are distinguishable: %v", bodies)
	}

	// 5 failures lock the guard (the wrong PIN above was the first), even for the right PIN
	for i := 0; i < 4; i++ {
		c.problemCode(session(consoleB, guardActor.PersonID, "1111"), 401, "invalid_grant")
	}
	locked := session(consoleB, guardActor.PersonID, "2468")
	c.problemCode(locked, 429, "rate_limited")
	if locked.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After on the lock")
	}
	// setting a PIN clears the lock
	setPin(guardActor.PersonID, "2468")
	okRec := session(consoleB, guardActor.PersonID, "2468")
	sess := c.want(okRec, 201)
	token := sess["token"].(string)
	if token == "" || sess["person_id"] != guardActor.PersonID.String() || sess["display_name"] != "guard" || sess["expires_at"] == nil {
		t.Fatalf("session = %v", sess)
	}
	if n := c.countAudit("gate.pin_failed", guardActor.PersonID.String()); n != 6 {
		t.Fatalf("failed attempts audited = %d, want 6", n)
	}

	// actions are attributed to the guard of the session
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	e := c.want(c.req(owner, "POST", c.path("/visitor-entries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Guest","window_from":%q,"window_to":%q}`,
		c.f.Site, c.f.Unit, rfc(time.Now()), rfc(time.Now().Add(time.Hour)))), 201)
	entryID := e["id"].(string)
	console := c.want(c.req(consoleB, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "", "X-Guard-Session", token), 200)
	if len(console["expected_visitors"].([]any)) != 1 || console["agent"] != nil {
		t.Fatalf("console = %v", console)
	}
	c.want(c.req(consoleB, "POST", c.path("/gate/visitors/"+entryID+":arrive"), "", "X-Guard-Session", token), 200)
	var who string
	_ = c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT diff->'guard_person_id'->>'after' FROM audit_event WHERE org_id = $1 AND action = 'gate.arrive' AND target_id = $2`, c.org.ID, entryID).Scan(&who)
	})
	if who != guardActor.PersonID.String() {
		t.Fatalf("arrival attributed to %q, want the guard %s", who, guardActor.PersonID)
	}
	var by *uuid.UUID
	_ = c.w.DB.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT arrived_by FROM visitor_entry WHERE id = $1`, entryID).Scan(&by)
	})
	if by == nil || *by != guardActor.PersonID {
		t.Fatalf("arrived_by = %v", by)
	}

	// a token of another console, an unknown one, an expired one and one of another site
	c.problemCode(c.req(c.guard, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "", "X-Guard-Session", token), 403, "insufficient_permission")
	c.problemCode(c.req(consoleB, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "", "X-Guard-Session", "nonsense"), 401, "invalid_grant")
	site2 := c.w.NewSite(t, c.org)
	c.problemCode(c.req(consoleB, "GET", c.path("/gate/console?site_id="+site2.Site.String()), "", "X-Guard-Session", token), 403, "insufficient_permission")
	c.w.Exec(t, `UPDATE guard_session SET expires_at = now() - interval '1 minute' WHERE person_id = $1`, guardActor.PersonID)
	c.problemCode(c.req(consoleB, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "", "X-Guard-Session", token), 401, "invalid_grant")
	// a new session of the same console ends the earlier one
	s1 := c.want(session(consoleB, guardActor.PersonID, "2468"), 201)["token"].(string)
	s2 := c.want(session(consoleB, guardActor.PersonID, "2468"), 201)["token"].(string)
	c.problemCode(c.req(consoleB, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "", "X-Guard-Session", s1), 401, "invalid_grant")
	c.want(c.req(consoleB, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "", "X-Guard-Session", s2), 200)
	// the guard membership ending ends the session (the manager's login is the console here)
	mgrSession := c.want(session(c.manager, guardActor.PersonID, "2468"), 201)["token"].(string)
	c.want(c.req(c.manager, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "", "X-Guard-Session", mgrSession), 200)
	c.w.Exec(t, `UPDATE membership SET valid_to = now() WHERE person_id = $1 AND role = 'guard'`, guardActor.PersonID)
	c.problemCode(c.req(c.manager, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "", "X-Guard-Session", mgrSession), 401, "invalid_grant")
}

func TestIntegrationCommunityGuardConsoleLock(t *testing.T) {
	c := newCommEnv(t)
	guardActor := c.w.Actor("guard")
	c.want(c.req(c.manager, "PUT", c.path("/persons/"+guardActor.PersonID.String()+"/gate-pin"), `{"pin":"13579"}`), 204)
	console := c.w.Token(t, guardActor)
	// 20 failures from one console in an hour (spread over people, so no guard lock) lock the console
	for i := 0; i < 20; i++ {
		rec := c.req(console, "POST", c.path("/gate/sessions"), fmt.Sprintf(`{"site_id":%q,"person_id":%q,"pin":"0000"}`, c.f.Site, uuid.New()))
		c.problemCode(rec, 401, "invalid_grant")
	}
	rec := c.req(console, "POST", c.path("/gate/sessions"), fmt.Sprintf(`{"site_id":%q,"person_id":%q,"pin":"13579"}`, c.f.Site, guardActor.PersonID))
	c.problemCode(rec, 429, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// another console is not affected
	other := c.w.Token(t, guardActor)
	c.want(c.req(other, "POST", c.path("/gate/sessions"), fmt.Sprintf(`{"site_id":%q,"person_id":%q,"pin":"13579"}`, c.f.Site, guardActor.PersonID)), 201)
}

// --- parcels (C3), plates, console snapshot ---

func TestIntegrationCommunityParcelsAndPlates(t *testing.T) {
	c := newCommEnv(t)
	owner, ownerPerson := c.resident("owner1", c.f.Unit, "owner")
	other := c.w.NewUnit(t, c.f)
	stranger, _ := c.resident("owner2", other, "owner")
	// vehicles: registered plate, formatted for the guard
	c.want(c.req(c.manager, "POST", c.path("/units/"+c.f.Unit.String()+"/vehicles"), `{"plate":"34 abc 123"}`), 201)
	pl := c.req(c.guard, "GET", c.path("/gate/plates?site_id="+c.f.Site.String()+"&q=34abc"), "")
	plates := c.want(pl, 200)["items"].([]any)
	if len(plates) != 1 || plates[0].(map[string]any)["unit_door_number"] != c.f.DoorNum || plates[0].(map[string]any)["plate"] != "34 ABC 123" {
		t.Fatalf("plates = %v", plates)
	}
	assertMinimal(t, pl.Body.Bytes())

	create := c.req(c.guard, "POST", c.path("/gate/deliveries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"description":"Blue box"}`, c.f.Site, c.f.Unit))
	d := c.want(create, 201)
	assertMinimal(t, create.Body.Bytes())
	if d["status"] != "waiting" || d["unit_door_number"] != c.f.DoorNum {
		t.Fatalf("delivery = %v", d)
	}
	notes := c.notes.byTemplate("delivery.received")
	if len(notes) != 1 || notes[0].PersonIDs[0] != ownerPerson {
		t.Fatalf("parcel notification = %+v", notes)
	}
	// the resident sees their unit's parcels, the other unit sees none, the guard sees waiting ones by door
	mine := c.want(c.req(owner, "GET", c.path("/deliveries?site_id="+c.f.Site.String()), ""), 200)["items"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["description"] != "Blue box" {
		t.Fatalf("resident parcels = %v", mine)
	}
	if n := len(c.want(c.req(stranger, "GET", c.path("/deliveries?site_id="+c.f.Site.String()), ""), 200)["items"].([]any)); n != 0 {
		t.Fatalf("another unit sees %d parcels", n)
	}
	waiting := c.req(c.guard, "GET", c.path("/gate/deliveries?site_id="+c.f.Site.String()+"&status=waiting&unit_door_number="+c.f.DoorNum), "")
	if n := len(c.want(waiting, 200)["items"].([]any)); n != 1 {
		t.Fatalf("waiting parcels = %d", n)
	}
	assertMinimal(t, waiting.Body.Bytes())
	col := c.want(c.req(c.guard, "POST", c.path("/gate/deliveries/"+d["id"].(string)+":collect"), `{"collected_by_name":"Ayse"}`), 200)
	if col["status"] != "collected" || col["collected_by_name"] != "Ayse" {
		t.Fatalf("collect = %v", col)
	}
	c.problemCode(c.req(c.guard, "POST", c.path("/gate/deliveries/"+d["id"].(string)+":collect"), `{"collected_by_name":"Ayse"}`), 409, "conflict")

	// the console snapshot is minimal too
	snap := c.req(c.guard, "GET", c.path("/gate/console?site_id="+c.f.Site.String()), "")
	c.want(snap, 200)
	assertMinimal(t, snap.Body.Bytes())
	// guards are not residents: the portal parcel list is not theirs
	c.problemCode(c.req(c.guard, "GET", c.path("/deliveries?site_id="+c.f.Site.String()), ""), 403, "insufficient_permission")
}

// --- retention (KVKK) and the access projection ---

func TestIntegrationCommunityRetentionAndCredentials(t *testing.T) {
	c := newCommEnv(t)
	owner, _ := c.resident("owner1", c.f.Unit, "owner")
	ctx := context.Background()
	old, mid, fresh := cmEntry(c.w, t, c.f, "pre_approved", time.Now().AddDate(0, 0, -121), time.Now().AddDate(0, 0, -120)),
		cmEntry(c.w, t, c.f, "pre_approved", time.Now().AddDate(0, 0, -41), time.Now().AddDate(0, 0, -40)),
		cmEntry(c.w, t, c.f, "pre_approved", time.Now().AddDate(0, 0, -2), time.Now().AddDate(0, 0, -1))
	c.w.Exec(t, `INSERT INTO delivery (id, org_id, site_id, unit_id, received_at, collected_at, collected_by_name) VALUES ($1, $2, $3, $4, now() - interval '100 days', now() - interval '99 days', 'X')`,
		id.New(), c.org.ID, c.f.Site, c.f.Unit)
	exists := func(e uuid.UUID) bool {
		var n int
		_ = c.w.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM visitor_entry WHERE id = $1`, e).Scan(&n)
		})
		return n == 1
	}
	// the default is 90 days
	if _, err := c.comm().PruneVisitorLogsOrg(ctx, c.org.ID); err != nil {
		t.Fatal(err)
	}
	if exists(old) || !exists(mid) || !exists(fresh) {
		t.Fatalf("after the default prune: old=%v mid=%v fresh=%v", exists(old), exists(mid), exists(fresh))
	}
	var parcels int
	_ = c.w.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM delivery WHERE org_id = $1`, c.org.ID).Scan(&parcels)
	})
	if parcels != 0 {
		t.Fatalf("an old collected parcel survived: %d", parcels)
	}
	// a site may keep less (never more without a reason): 30 days
	c.w.Exec(t, `UPDATE site SET settings = settings || '{"visitor_retention_days": 30}'::jsonb WHERE id = $1`, c.f.Site)
	if _, err := c.comm().PruneVisitorLogsOrg(ctx, c.org.ID); err != nil {
		t.Fatal(err)
	}
	if exists(mid) || !exists(fresh) {
		t.Fatalf("after the 30 day prune: mid=%v fresh=%v", exists(mid), exists(fresh))
	}

	// credentials for the access module: QR hash with window plus slack, the plate, and revocations in a delta
	from, to := time.Now().Add(time.Hour), time.Now().Add(3*time.Hour)
	e := c.want(c.req(owner, "POST", c.path("/visitor-entries"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Guest","plate":"34 xyz 12","window_from":%q,"window_to":%q}`, c.f.Site, c.f.Unit, rfc(from), rfc(to))), 201)
	token := strings.TrimPrefix(e["qr_token"].(string), "obk1.")
	rv := c.want(c.req(owner, "POST", c.path("/recurring-visitors"), fmt.Sprintf(`{"site_id":%q,"unit_id":%q,"name":"Nanny","days":[1,2,3],"start":"22:00","end":"06:00","valid_from":"2026-01-01"}`, c.f.Site, c.f.Unit)), 201)
	creds, err := c.comm().VisitorCredentials(ctx, c.org.ID, c.f.Site, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]community.VisitorCredential{}
	for _, cr := range creds {
		byKey[cr.SourceID.String()+"/"+cr.Kind] = cr
	}
	qrCred, ok := byKey[e["id"].(string)+"/visitor_qr"]
	if !ok || qrCred.Value != community.TokenHashHex(token) || qrCred.PointScope != "all" || qrCred.MaxUses == nil || *qrCred.MaxUses != 1 || !qrCred.Active {
		t.Fatalf("qr credential = %+v", qrCred)
	}
	if want := from.Add(-30 * time.Minute).Truncate(time.Second); !qrCred.ValidFrom.Truncate(time.Second).Equal(want) {
		t.Fatalf("valid_from %v, want %v (window minus 30 minutes)", qrCred.ValidFrom, want)
	}
	if pc, ok := byKey[e["id"].(string)+"/plate"]; !ok || pc.Value != "34XYZ12" || pc.PointScope != "all_vehicle" {
		t.Fatalf("plate credential = %+v", pc)
	}
	rc, ok := byKey[rv["id"].(string)+"/visitor_qr"]
	if !ok || rc.Recurring == nil || rc.Recurring.Start != "22:00" || rc.Recurring.End != "06:00" || rc.MaxUses != nil || rc.Timezone != "Europe/Istanbul" {
		t.Fatalf("recurring credential = %+v", rc)
	}
	// a cancellation leaves the snapshot and shows up inactive in the delta
	mark := time.Now().Add(-time.Second)
	c.want(c.req(owner, "POST", c.path("/visitor-entries/"+e["id"].(string)+":cancel"), ""), 200)
	snapshot, _ := c.comm().VisitorCredentials(ctx, c.org.ID, c.f.Site, time.Time{})
	for _, cr := range snapshot {
		if cr.SourceID.String() == e["id"].(string) {
			t.Fatalf("a cancelled visitor is still in the snapshot: %+v", cr)
		}
	}
	delta, _ := c.comm().VisitorCredentials(ctx, c.org.ID, c.f.Site, mark)
	found := false
	for _, cr := range delta {
		if cr.SourceID.String() == e["id"].(string) && cr.Kind == "visitor_qr" {
			found = true
			if cr.Active {
				t.Fatalf("the delta must carry the revocation: %+v", cr)
			}
		}
	}
	if !found {
		t.Fatal("the cancellation is missing from the delta")
	}
	if c.hooks.changed == 0 {
		t.Fatal("OnVisitorChanged was never called")
	}
}
