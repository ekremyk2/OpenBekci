package httpapi

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/documents"
	"github.com/openbekci/openbekci/server/internal/platform/id"
)

// Filing the same generated record from concurrent transactions yields one
// document and no orphan: the losers' drafts are discarded.
func TestIntegrationFileGeneratedConcurrentFilingsMakeOneDocument(t *testing.T) {
	e := newOpsEnv(t)
	org := e.w.OrgA
	s := newD2Site(e.w, org, t)
	source := id.New()
	const n = 6
	var wg sync.WaitGroup
	docs := make([]documents.Document, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.db.InTenantTx(context.Background(), org.ID, func(tx pgx.Tx) error {
				var err error
				docs[i], err = e.ops.documents.FileGenerated(context.Background(), tx, documents.GeneratedParams{
					OrgID: org.ID, SiteID: s.Site, Title: "Minutes", FileName: "minutes.txt", Content: strings.NewReader("agenda"),
					SystemKey: "minutes", FolderName: "Minutes", GeneratedType: "meeting_minutes", GeneratedID: source})
				return err
			})
		}()
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("filing %d: %v", i, errs[i])
		}
		if docs[i].ID != docs[0].ID {
			t.Fatalf("filings returned different documents: %v and %v", docs[0].ID, docs[i].ID)
		}
	}
	var ndocs, live, orphans int
	err := e.db.InTenantTx(context.Background(), org.ID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM document WHERE org_id = $1 AND generated_id = $2`, org.ID, source).Scan(&ndocs); err != nil {
			return err
		}
		if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM file WHERE org_id = $1 AND scope_type = 'document' AND scope_id = $2 AND status = 'ready' AND retention_until IS NULL`, org.ID, docs[0].ID).Scan(&live); err != nil {
			return err
		}
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM file WHERE org_id = $1 AND site_id = $2 AND source = 'generated' AND scope_type IS NULL AND status <> 'deleted'`, org.ID, s.Site).Scan(&orphans)
	})
	if err != nil {
		t.Fatal(err)
	}
	if ndocs != 1 || live != 1 || orphans != 0 {
		t.Fatalf("documents %d, attached files %d, orphan drafts %d", ndocs, live, orphans)
	}
}
