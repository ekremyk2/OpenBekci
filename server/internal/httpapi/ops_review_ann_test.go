package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/announcements"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// The SMS preview counts billed segments (UCS-2 for Turkish glyphs) and leaves
// out the people who muted announcements.
func TestIntegrationAnnouncementPreviewCountsSegmentsAndMutes(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	a1 := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Bob")
	mgr := e.role(t, "site_manager")
	preview := func(title string) (recipients, sms, segments int) {
		rec := e.call(t, mgr, "POST", orgURL(org, "/announcements:preview"), fmt.Sprintf(`{"site_id":%q,"audience":{"kind":"site"},"channels":["sms"],"title":%q}`, s.Site, title))
		mustStatus(t, rec, 200)
		v := jsonOf[struct {
			R int `json:"recipient_count"`
			S int `json:"sms_count"`
			G int `json:"sms_segments"`
		}](t, rec)
		return v.R, v.S, v.G
	}
	if r, n, g := preview(strings.Repeat("a", 60)); r != 2 || n != 2 || g != 2 {
		t.Fatalf("GSM text: recipients %d, SMS %d, segments %d", r, n, g)
	}
	// the same length in Turkish glyphs is UCS-2: 70 characters a segment, so two segments each
	if r, n, g := preview(strings.Repeat("ş", 80)); r != 2 || n != 2 || g != 4 {
		t.Fatalf("UCS-2 text: recipients %d, SMS %d, segments %d", r, n, g)
	}
	// a very long title is capped in the text message, so the bill stays bounded
	if _, n, g := preview(strings.Repeat("ş", 200)); g > 3*n {
		t.Fatalf("an uncapped title: %d segments for %d messages", g, n)
	}
	// somebody who muted announcements is addressed but gets no message
	e.w.Exec(t, `INSERT INTO notification_mute (org_id, person_id, type) VALUES ($1, $2, 'announcement.published')`, org.ID, a1.person)
	if r, n, g := preview(strings.Repeat("a", 60)); r != 2 || n != 1 || g != 1 {
		t.Fatalf("after a mute: recipients %d, SMS %d, segments %d", r, n, g)
	}
}

// A pinned notice shows to the people of its audience when they read it, also to
// a resident who moved in after it was published; an ordinary one stays with its
// recipients. Scheduled publishing never publishes an expired notice and runs
// the gateway check.
func TestIntegrationPinnedAnnouncementsReachLaterResidentsAndScheduledRules(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	mgr := e.role(t, "site_manager")
	publish := func(title string, pinned bool) uuid.UUID {
		rec := e.call(t, mgr, "POST", orgURL(org, "/announcements"), fmt.Sprintf(`{"site_id":%q,"title":%q,"body_md":"text","audience":{"kind":"site"},"channels":["in_app"],"pinned":%v}`, s.Site, title, pinned))
		mustStatus(t, rec, 201)
		a := jsonOf[struct {
			ID uuid.UUID `json:"id"`
		}](t, rec)
		mustStatus(t, e.call(t, mgr, "POST", orgURL(org, "/announcements/"+a.ID.String()+":publish"), ``, "Idempotency-Key", "pub-"+title), 200)
		return a.ID
	}
	house := publish("House rules", true)
	news := publish("Water cut", false)

	late := e.addResident(t, s.SiteFix, e.w.NewUnit(t, s.SiteFix), "tenant", "Late")
	list := e.call(t, late.token, "GET", orgURL(org, "/announcements")+"?site_id="+s.Site.String(), "")
	mustStatus(t, list, 200)
	if !strings.Contains(list.Body.String(), house.String()) || strings.Contains(list.Body.String(), news.String()) {
		t.Fatalf("the late resident sees: %s", list.Body.String())
	}
	mustStatus(t, e.call(t, late.token, "GET", orgURL(org, "/announcements/"+house.String()), ""), 200)
	mustStatus(t, e.call(t, late.token, "GET", orgURL(org, "/announcements/"+news.String()), ""), 404)
	// reading it does not make them part of the audience count
	mustStatus(t, e.call(t, late.token, "PUT", orgURL(org, "/announcements/"+house.String()+"/read"), ``), 204)
	rec := e.call(t, mgr, "GET", orgURL(org, "/announcements/"+house.String()+"/reads"), "")
	if r := jsonOf[struct {
		AudienceCount int `json:"audience_count"`
		ReadCount     int `json:"read_count"`
	}](t, rec); r.AudienceCount != 1 || r.ReadCount != 0 {
		t.Fatalf("reads: %s", rec.Body.String())
	}

	// scheduled: an expired one is archived, never published; one that needs SMS stays put without a provider
	expired, sms := id.New(), id.New()
	for _, r := range []struct {
		id       uuid.UUID
		channels string
		expires  string
	}{{expired, "{in_app}", "now() - interval '1 minute'"}, {sms, "{in_app,sms}", "NULL"}} {
		e.w.Exec(t, fmt.Sprintf(`INSERT INTO announcement (id, org_id, site_id, title, body_md, status, publish_at, expires_at, channels)
			VALUES ($1, $2, $3, 'Scheduled', 'x', 'scheduled', now() - interval '1 hour', %s, $4::text[])`, r.expires), r.id, org.ID, s.Site, r.channels)
	}
	noSMS := announcements.NewService(e.db, e.files, e.ops.notes, announcements.Config{Mail: true})
	if err := noSMS.PublishDue(context.Background()); err == nil {
		t.Fatalf("a scheduled SMS without a provider was published")
	}
	status := func(a uuid.UUID) string {
		var st string
		err := e.db.InTenantTx(context.Background(), org.ID, func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `SELECT status FROM announcement WHERE org_id = $1 AND id = $2`, org.ID, a).Scan(&st)
		})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if status(expired) != "archived" || status(sms) != "scheduled" {
		t.Fatalf("expired: %s, sms: %s", status(expired), status(sms))
	}
}
