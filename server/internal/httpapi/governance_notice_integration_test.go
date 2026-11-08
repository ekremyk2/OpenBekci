package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeFiles is a governance.FileChecker that knows the files it is given.
type fakeFiles map[uuid.UUID]bool

func (f fakeFiles) FileExists(_ context.Context, _ pgx.Tx, _, id uuid.UUID) (bool, error) {
	return f[id], nil
}

// The delivery record is what a court reads: no future dates, nothing before
// the call, electronic notice only where the site allows it, a proof that is a
// real file, and a late flag against call_min_days.
func TestIntegrationGovernanceNoticeValidation(t *testing.T) {
	e := newAuthzEnv(t)
	w := e.w
	c := gcall{t, e}
	mgr := w.Actor("site_manager")
	org := w.OrgA
	g := govSeed(t, w, org, "called")
	proof := uuid.New()
	e.gov.SetFileChecker(fakeFiles{proof: true})
	defer e.gov.SetFileChecker(fakeFiles{})
	path := orgPath(org, "/meetings/"+g.meeting.String()+"/notices/"+g.p1.String())
	body := func(method string, sent time.Time, extra string) string {
		return fmt.Sprintf(`{"phase":"call","method":%q,"sent_at":%q%s}`, method, sent.UTC().Format(time.RFC3339), extra)
	}
	now := time.Now().UTC()
	c.must(422, mgr, "PUT", path, body("registered_mail", now.Add(72*time.Hour), ""))  // in the future
	c.must(422, mgr, "PUT", path, body("registered_mail", now.Add(-72*time.Hour), "")) // before the call
	c.must(422, mgr, "PUT", path, body("email", now, ""))                              // electronic notice is off by default
	c.must(422, mgr, "PUT", path, body("sms", now, ""))
	c.must(422, mgr, "PUT", path, body("registered_mail", now, fmt.Sprintf(`,"proof_file_id":%q`, uuid.New()))) // not a file
	ok := c.must(200, mgr, "PUT", path, body("registered_mail", now, fmt.Sprintf(`,"proof_file_id":%q,"tracking_no":"PTT1"`, proof)))
	if gstr(ok, "proof_file_id") != proof.String() || ok["late"] != false {
		t.Fatalf("notice = %v", ok)
	}
	// a call that reached the owner less than call_min_days before the meeting is late
	w.Exec(t, `UPDATE meeting SET scheduled_at = now() + interval '10 days', second_call_at = now() + interval '20 days' WHERE org_id = $1 AND id = $2`, org.ID, g.meeting)
	late := c.must(200, mgr, "PUT", path, body("registered_mail", now, ""))
	if late["late"] != true {
		t.Fatalf("a call 10 days before the meeting (minimum 15) must be flagged late: %v", late)
	}
	// where the site allows it, email counts
	w.Exec(t, `UPDATE meeting SET settings_snapshot = jsonb_set(settings_snapshot, '{notice_electronic_allowed}', 'true') WHERE org_id = $1 AND id = $2`, org.ID, g.meeting)
	c.must(200, mgr, "PUT", path, body("email", now, ""))
}
