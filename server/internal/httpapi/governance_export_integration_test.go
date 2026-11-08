package httpapi

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// A site hand-over carries the governance paperwork: meetings, agenda items,
// attendance (the hazirun), vote counts, polls, notices, decisions with their
// numbers (decision_counter) and the generated documents; the elected-role
// membership keeps pointing at its decision.
func TestIntegrationGovernanceTravelsWithTheSite(t *testing.T) {
	p := newPB(t)
	g := govSeed(t, p.w, p.org, "closed")
	// poll, vote counts, a poll answer and a membership created by the decision
	p.w.Exec(t, `INSERT INTO poll (org_id, site_id, agenda_item_id, question, opens_at, closes_at) VALUES ($1, $2, $3, 'Agree?', now() - interval '3 days', now() - interval '1 day')`,
		p.org.ID, g.f.Site, g.item)
	p.w.Exec(t, `INSERT INTO poll_answer (org_id, site_id, agenda_item_id, unit_id, answer, answered_by) VALUES ($1, $2, $3, $4, 'yes', $5)`,
		p.org.ID, g.f.Site, g.item, g.u1, g.p1)
	p.w.Exec(t, `INSERT INTO vote_count (org_id, site_id, agenda_item_id, yes_count, no_count, abstain_count, yes_share, no_share, abstain_share) VALUES ($1, $2, $3, 1, 0, 0, 1, 0, 0)`,
		p.org.ID, g.f.Site, g.item)
	p.w.Exec(t, `INSERT INTO meeting_document (id, org_id, site_id, meeting_id, kind, file_name, mime, content) VALUES ($1, $2, $3, $4, 'minutes', 'minutes.pdf', 'application/pdf', '\x255044462d'::bytea)`,
		id.New(), p.org.ID, g.f.Site, g.meeting)
	elected := id.New()
	p.w.Exec(t, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from, valid_to, site_owned, decision_id) VALUES ($1, $2, $3, 'board_member', 'site', $4, '2026-01-01', '2027-01-01', true, $5)`,
		elected, p.org.ID, g.p1, g.f.Site, g.decision)

	exp := p.must(p.call("POST", p.base()+"/sites/"+g.f.Site.String()+"/export", "", p.adm), 201).Body
	arch := p.must(p.call("GET", p.base()+"/site-exports/"+str(exp, "id")+"/archive", "", p.adm), 200).Raw
	bAdm := p.w.Token(t, p.w.BAdmin)
	p.must(p.upload("/v1/orgs/"+p.w.OrgB.ID.String()+"/site-imports", bAdm, nil, "archive.json", []byte(arch)), 201)

	count := func(q string, args ...any) int {
		var n int
		if err := p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), q, args...).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	b := p.w.OrgB.ID
	for table, want := range map[string]int{"meeting": 1, "agenda_item": 1, "decision": 1, "decision_counter": 1, "attendance": 1, "meeting_notice": 2, "poll": 1, "poll_answer": 1, "vote_count": 1, "meeting_document": 1} {
		if got := count(`SELECT count(*) FROM `+table+` WHERE org_id = $1 AND site_id = $2`, b, g.f.Site); got != want {
			t.Errorf("%s: %d rows imported, want %d", table, got, want)
		}
	}
	if got := count(`SELECT count(*) FROM membership m JOIN decision d ON d.org_id = m.org_id AND d.id = m.decision_id WHERE m.org_id = $1 AND m.id = $2`, b, elected); got != 1 {
		t.Errorf("the elected membership lost its decision: %d", got)
	}
	if got := count(`SELECT last_no FROM decision_counter WHERE org_id = $1 AND site_id = $2`, b, g.f.Site); got != 1 {
		t.Errorf("decision counter = %d, want 1 so the numbers continue", got)
	}
	// the tokens are not carried
	if got := count(`SELECT count(*) FROM attendance_token WHERE org_id = $1`, b); got != 0 {
		t.Errorf("%d check-in tokens were carried over", got)
	}
}
