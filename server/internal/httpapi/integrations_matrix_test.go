package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/authztest"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/integrations"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// jobRecorder stands in for the River client in the HTTP tests: it records the
// delivery jobs the integrations service queues (the tests then run Deliver on
// them directly).
type jobRecorder struct {
	mu   sync.Mutex
	args []integrations.DeliverArgs
}

func (r *jobRecorder) insert(_ context.Context, _ pgx.Tx, args river.JobArgs, _ *river.InsertOpts) error {
	a, ok := args.(integrations.DeliverArgs)
	if !ok {
		return fmt.Errorf("unexpected job %T", args)
	}
	r.mu.Lock()
	r.args = append(r.args, a)
	r.mu.Unlock()
	return nil
}

func (r *jobRecorder) jobs() []integrations.DeliverArgs {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]integrations.DeliverArgs(nil), r.args...)
}

// testRoots holds the certificates of the fake webhook receivers the tests start.
var testRoots = x509.NewCertPool()

const testEncryptionKey = "0123456789abcdef0123456789abcdef-enc"

// newTestIntegrations builds the integrations service of the test environments:
// loopback receivers are allowed (the delivery tests run fake receivers on
// 127.0.0.1) and there is no per-key rate limit.
func newTestIntegrations(t testing.TB, d *db.DB, reg *authz.Registry, log *slog.Logger, rec *jobRecorder, notify jobs.Notifier) *integrations.Service {
	t.Helper()
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s, err := integrations.NewService(d, integrations.Config{
		Registry: reg, EncryptionKey: []byte(testEncryptionKey), Log: log, Notifier: notify, TLSConfig: &tls.Config{RootCAs: testRoots, MinVersion: tls.VersionTLS12},
		AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, KeyRatePerMinute: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetInsert(rec.insert)
	return s
}

var integSeq atomic.Int64

type integFix struct{ endpoint, key uuid.UUID }

// seedIntegrations gives the organization an endpoint and an API key.
func seedIntegrations(w *authztest.World, t *testing.T, org authztest.Org) integFix {
	t.Helper()
	n := integSeq.Add(1)
	x := integFix{endpoint: id.New(), key: id.New()}
	w.Exec(t, `INSERT INTO webhook_endpoint (id, org_id, url, secret_sealed, events) VALUES ($1, $2, $3, decode('00', 'hex'), ARRAY['ticket.created'])`,
		x.endpoint, org.ID, fmt.Sprintf("https://93.184.216.34/hook-%d", n))
	w.Exec(t, `INSERT INTO api_key (id, org_id, name, prefix, secret_hash, scopes, created_by) VALUES ($1, $2, $3, $4, decode('00', 'hex'), ARRAY['tickets.read'], $5)`,
		x.key, org.ID, fmt.Sprintf("matrix key %d", n), fmt.Sprintf("mx%06d", n), w.Actor("org_admin").UserID)
	return x
}

func init() {

	keysRead, keysManage := perms(integrations.PermAPIKeysRead), perms(integrations.PermAPIKeysManage)
	hooksRead, hooksManage := perms(integrations.PermWebhooksRead), perms(integrations.PermWebhooksManage)
	ifm := "If-Match"
	rowOf := func(ps []authz.Permission, status int, method string, path func(x integFix) string, body string, h ...string) mrow {
		return mrow{perms: ps, status: status, build: func(w *authztest.World, org authztest.Org, t *testing.T) mreq {
			x := seedIntegrations(w, t, org)
			return jsonReq(method, orgPath(org, path(x)), body, h...)
		}}
	}
	rows := map[string]mrow{
		"integrations.listApiKeys": rowOf(keysRead, 200, "GET", func(integFix) string { return "/api-keys" }, ""),
		// creating, rotating and revoking keys need a recent re-authentication: the matrix signs in
		// with a plain access token, so the permitted role is stopped by reauth_required (403)
		"integrations.createApiKey": rowOf(keysManage, 403, "POST", func(integFix) string { return "/api-keys" }, `{"name":"matrix","scopes":["tickets.read"]}`),
		"integrations.rotateApiKey": rowOf(keysManage, 403, "POST", func(x integFix) string { return "/api-keys/" + x.key.String() + ":rotate" }, ""),
		"integrations.revokeApiKey": rowOf(keysManage, 403, "POST", func(x integFix) string { return "/api-keys/" + x.key.String() + ":revoke" }, ""),

		"integrations.listWebhookEndpoints": rowOf(hooksRead, 200, "GET", func(integFix) string { return "/webhook-endpoints" }, ""),
		"integrations.createWebhookEndpoint": rowOf(hooksManage, 201, "POST", func(integFix) string { return "/webhook-endpoints" },
			`{"url":"https://93.184.216.34/matrix","events":["ticket.created"]}`),
		"integrations.getWebhookEndpoint": rowOf(hooksRead, 200, "GET", func(x integFix) string { return "/webhook-endpoints/" + x.endpoint.String() }, ""),
		"integrations.updateWebhookEndpoint": rowOf(hooksManage, 200, "PATCH", func(x integFix) string { return "/webhook-endpoints/" + x.endpoint.String() },
			`{"active":false}`, ifm, `"1"`),
		"integrations.deleteWebhookEndpoint": rowOf(hooksManage, 204, "DELETE", func(x integFix) string { return "/webhook-endpoints/" + x.endpoint.String() }, ""),
		"integrations.testWebhookEndpoint":   rowOf(hooksManage, 202, "POST", func(x integFix) string { return "/webhook-endpoints/" + x.endpoint.String() + ":test" }, ""),
		"integrations.listWebhookDeliveries": rowOf(hooksRead, 200, "GET", func(x integFix) string { return "/webhook-endpoints/" + x.endpoint.String() + "/deliveries" }, ""),
	}
	for k, v := range rows {
		authzMatrixRows[k] = v
	}
}
