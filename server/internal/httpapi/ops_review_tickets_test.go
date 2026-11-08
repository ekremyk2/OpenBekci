package httpapi

import (
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// An auditor reads tickets but cannot comment; a merged ticket is terminal.
func TestIntegrationTicketCommentRightsAndMergedTicketsAreTerminal(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	otherUnit := e.w.NewUnit(t, s.SiteFix)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	bob := e.addResident(t, s.SiteFix, otherUnit, "tenant", "Bob")
	mgr := e.role(t, "site_manager")
	open := func(tok string) ticketJSON {
		rec := e.call(t, tok, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Elevator stuck","description":"floor 3"}`, s.Site, s.Category))
		mustStatus(t, rec, 201)
		return jsonOf[ticketJSON](t, rec)
	}
	first, dup := open(ann.token), open(bob.token)
	path := func(tk ticketJSON, rest string) string { return orgURL(org, "/tickets/"+tk.ID.String()+rest) }

	// the auditor holds tickets.read only: 403, and nobody is notified
	e.ops.notes.take()
	mustStatus(t, e.call(t, e.role(t, "auditor"), "GET", path(first, ""), ""), 200)
	mustStatus(t, e.call(t, e.role(t, "auditor"), "POST", path(first, "/comments"), `{"body":"observation"}`), 403)
	if got := e.ops.notes.take(); len(got) != 0 {
		t.Fatalf("a refused comment notified: %+v", got)
	}

	// a resident's comment tells the managers when nobody is assigned
	mustStatus(t, e.call(t, bob.token, "POST", path(first, "/comments"), `{"body":"same here"}`), 201)
	sent := e.ops.notes.take()
	if len(sent) != 1 || sent[0].Type != "ticket.commented" || len(sent[0].Recipients) < 2 {
		t.Fatalf("the reporter and a manager should be told: %+v", sent)
	}

	// merge dup into first: every change to dup answers 409
	mustStatus(t, e.call(t, mgr, "POST", path(dup, ":merge"), fmt.Sprintf(`{"into_ticket_id":%q}`, first.ID)), 200)
	mustStatus(t, e.call(t, mgr, "POST", path(dup, ":transition"), fmt.Sprintf(`{"status_id":%q}`, s.Open)), 409)
	mustStatus(t, e.call(t, ann.token, "PUT", path(dup, "/vote"), ""), 409)
	mustStatus(t, e.call(t, bob.token, "POST", path(dup, "/comments"), `{"body":"still broken"}`), 409)
	mustStatus(t, e.call(t, mgr, "POST", path(dup, "/comments"), `{"body":"closed"}`), 409)

	// the target of a merge that does not exist or is not visible is 404, not 422
	mustStatus(t, e.call(t, mgr, "POST", path(first, ":merge"), fmt.Sprintf(`{"into_ticket_id":%q}`, uuid.New())), 404)
}

// Two managers merging A into B and B into A at once never deadlock: one wins, the other is told 409.
func TestIntegrationConcurrentCrossMergesDoNotDeadlock(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	ann := e.addResident(t, s.SiteFix, s.Unit, "owner", "Ayse")
	mgr := e.role(t, "site_manager")
	var tk [2]ticketJSON
	for i := range tk {
		rec := e.call(t, ann.token, "POST", orgURL(org, "/tickets"), fmt.Sprintf(`{"site_id":%q,"category_id":%q,"title":"Leak %d","description":"x"}`, s.Site, s.Category, i))
		mustStatus(t, rec, 201)
		tk[i] = jsonOf[ticketJSON](t, rec)
	}
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := e.call(t, mgr, "POST", orgURL(org, "/tickets/"+tk[i].ID.String()+":merge"), fmt.Sprintf(`{"into_ticket_id":%q}`, tk[1-i].ID))
			codes[i] = rec.Code
		}()
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("cross merges answered %v, want one 200 and one 409", codes)
	}
}
