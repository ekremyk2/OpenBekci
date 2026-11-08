package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The one-time enrolment token is a secret: the create and re-enrol responses are
// never stored by the idempotency layer, whatever Idempotency-Key the client sends.
func TestIntegrationEnrolTokenIsNotStoredByIdempotency(t *testing.T) {
	p := newPB(t)
	f := p.w.NewSite(t, p.org)
	site := f.Site.String()
	base := p.base()
	body := fmt.Sprintf(`{"site_id":%q,"name":"Key Pi"}`, site)
	r := p.must(p.call("POST", base+"/agents", body, p.adm, "Idempotency-Key", "enrol-secret-001"), 201)
	token := str(r.Body, "enrolment_token")
	agentID := str(r.Body["agent"].(map[string]any), "id")
	re := p.must(p.call("POST", base+"/agents/"+agentID+":reenrol", "", p.adm, "Idempotency-Key", "enrol-secret-002"), 200)
	if token == "" || str(re.Body, "enrolment_token") == "" {
		t.Fatalf("no tokens: %s %s", r.Raw, re.Raw)
	}
	var stored int
	err := p.e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_key WHERE response_body::text LIKE '%obk_enr_%'`).Scan(&stored)
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("%d idempotency rows hold an enrolment token", stored)
	}
}
