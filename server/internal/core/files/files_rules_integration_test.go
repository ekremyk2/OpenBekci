package files_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

func (e *env) attach(ctx context.Context, fileID uuid.UUID, ref files.ScopeRef) (f files.File, err error) {
	err = e.db.InTenantTx(ctx, e.world.OrgA.ID, func(tx pgx.Tx) (err error) {
		f, err = e.svc.Attach(ctx, tx, e.world.OrgA.ID, fileID, ref)
		return err
	})
	return f, err
}

// Once a file is attached, the owning module's resolver decides, uploader or not.
func TestIntegrationUploaderLosesAccessWhenTheResolverStopsAllowing(t *testing.T) {
	e := newEnv(t, nil)
	staff, admin := e.world.Actor("staff"), e.world.Actor("org_admin")
	sctx, actx := e.ctx(staff, e.world.OrgA), e.ctx(admin, e.world.OrgA)
	ticket := e.ticket(staff)
	ref := files.ScopeRef{Type: ticketScope, ID: ticket}

	f := e.mustUpload(sctx, e.world.OrgA, "photo.txt", []byte("on the ticket"), &ref)
	if _, err := e.svc.Get(sctx, e.world.OrgA.ID, f.ID); err != nil {
		t.Fatalf("the uploader reads the attached file while the resolver allows: %v", err)
	}
	// the document moves to a place the uploader may not read
	e.mu.Lock()
	e.readers[ticket] = nil
	e.mu.Unlock()
	_, err := e.svc.Get(sctx, e.world.OrgA.ID, f.ID)
	wantStatus(t, err, 404, "Get by the uploader after the resolver denied")
	_, err = e.svc.Open(sctx, e.world.OrgA.ID, f.ID, "")
	wantStatus(t, err, 404, "Open by the uploader after the resolver denied")
	_, _, err = e.svc.List(sctx, e.world.OrgA.ID, files.ListParams{Scope: &ref})
	wantStatus(t, err, 404, "List by scope after the resolver denied")
	list, _, err := e.svc.List(sctx, e.world.OrgA.ID, files.ListParams{})
	if err != nil || len(list) != 0 {
		t.Errorf("an attached file is not listed among the uploader's drafts: %d, %v", len(list), err)
	}
	// files.manage stays the deliberate override
	if _, err := e.svc.Get(actx, e.world.OrgA.ID, f.ID); err != nil {
		t.Errorf("an administrator reads it: %v", err)
	}
	// a draft is still the uploader's
	d := e.mustUpload(sctx, e.world.OrgA, "draft.txt", []byte("draft"), nil)
	if _, err := e.svc.Get(sctx, e.world.OrgA.ID, d.ID); err != nil {
		t.Errorf("the uploader reads their own draft: %v", err)
	}
}

func TestIntegrationDeleteRules(t *testing.T) {
	e := newEnv(t, nil)
	staff, guard, admin := e.world.Actor("staff"), e.world.Actor("guard"), e.world.Actor("org_admin")
	sctx, gctx, actx := e.ctx(staff, e.world.OrgA), e.ctx(guard, e.world.OrgA), e.ctx(admin, e.world.OrgA)
	ref := files.ScopeRef{Type: ticketScope, ID: e.ticket(staff, guard)}

	draft := e.mustUpload(sctx, e.world.OrgA, "draft.txt", []byte("draft"), nil)
	attached := e.mustUpload(sctx, e.world.OrgA, "att.txt", []byte("attached"), &ref)

	wantStatus(t, e.svc.Delete(gctx, e.world.OrgA.ID, draft.ID), 404, "a stranger deleting a draft")
	if err := e.svc.Delete(sctx, e.world.OrgA.ID, draft.ID); err != nil {
		t.Errorf("the uploader discards their draft: %v", err)
	}
	wantStatus(t, e.svc.Delete(sctx, e.world.OrgA.ID, attached.ID), 409, "the uploader deleting an attached file")
	wantStatus(t, e.svc.Delete(gctx, e.world.OrgA.ID, attached.ID), 403, "a reader deleting an attached file")
	if err := e.svc.Delete(actx, e.world.OrgA.ID, attached.ID); err != nil {
		t.Errorf("files.manage deletes an attached file: %v", err)
	}
}

func TestIntegrationQuarantinedFilesCannotBeAttachedAndExpire(t *testing.T) {
	e := newEnv(t, func(c *files.Config) {
		c.Scanner = scanFunc(func(_ context.Context, r io.Reader) (files.Verdict, error) {
			b, _ := io.ReadAll(r)
			return files.Verdict{Infected: bytes.Contains(b, []byte("EICAR")), Signature: "Eicar"}, nil
		})
	})
	staff := e.world.Actor("staff")
	ctx := e.ctx(staff, e.world.OrgA)
	ref := files.ScopeRef{Type: ticketScope, ID: e.ticket(staff)}

	bad := e.mustUpload(ctx, e.world.OrgA, "virus.txt", []byte("EICAR"), nil)
	if bad.Status != files.StatusQuarantined || bad.RetentionUntil == nil {
		t.Fatalf("a flagged draft: %+v", bad)
	}
	_, err := e.attach(ctx, bad.ID, ref)
	wantStatus(t, err, 409, "attaching a quarantined file")
	if got, _ := e.svc.Get(ctx, e.world.OrgA.ID, bad.ID); got.Scope != nil || got.RetentionUntil == nil {
		t.Errorf("a refused attach changed the file: %+v", got)
	}
	// uploaded straight into a scope, a flagged file still gets an expiry and takes no slot of the limit
	flagged := e.mustUpload(ctx, e.world.OrgA, "virus2.txt", []byte("EICAR"), &ref)
	if flagged.Status != files.StatusQuarantined || flagged.RetentionUntil == nil {
		t.Errorf("a flagged file uploaded with a scope must expire: %+v", flagged)
	}
	for i := 0; i < 2; i++ { // the test scope allows 2 files
		e.mustUpload(ctx, e.world.OrgA, "ok.txt", []byte("fine"), &ref)
	}
	e.clock.advance(8 * 24 * time.Hour)
	res, err := e.svc.Prune(context.Background())
	if err != nil || res.Expired != 2 {
		t.Errorf("prune = %+v, %v; both flagged files must expire", res, err)
	}
}

func TestIntegrationScannerDownStoresNothing(t *testing.T) {
	e := newEnv(t, func(c *files.Config) {
		c.Scanner = scanFunc(func(context.Context, io.Reader) (files.Verdict, error) {
			return files.Verdict{}, errors.New("connection refused")
		})
	})
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	for _, name := range []string{"a.txt", "a.jpg"} {
		content := []byte("hello")
		if strings.HasSuffix(name, "jpg") {
			content = photo(t, 20, 20)
		}
		_, err := e.upload(ctx, e.world.OrgA, name, content, nil)
		wantStatus(t, err, 503, "upload with the scanner down")
	}
	n := 0
	if err := walk(e.root, func(string) { n++ }); err != nil {
		t.Fatal(err)
	}
	if n != 0 || e.fileRows() != 0 {
		t.Errorf("%d blobs and %d rows left behind; the scan runs before anything is written", n, e.fileRows())
	}
}

// A scan must not read the blob back from storage: the upload is scanned from the spooled copy.
func TestIntegrationNonImageIsScannedBeforeItIsStored(t *testing.T) {
	var e *env
	e = newEnv(t, func(c *files.Config) {
		c.Scanner = scanFunc(func(context.Context, io.Reader) (files.Verdict, error) {
			n := 0
			_ = walk(e.root, func(string) { n++ })
			if n != 0 {
				t.Errorf("the scanner ran with %d blobs already in storage", n)
			}
			return files.Verdict{}, nil
		})
	})
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	e.mustUpload(ctx, e.world.OrgA, "a.txt", []byte("scan me first"), nil)
}

// Attach holds the row; the maintenance job skips a draft that is being attached.
func TestIntegrationPruneSkipsAFileBeingAttached(t *testing.T) {
	e := newEnv(t, nil)
	staff := e.world.Actor("staff")
	ctx := e.ctx(staff, e.world.OrgA)
	ref := files.ScopeRef{Type: ticketScope, ID: e.ticket(staff)}
	draft := e.mustUpload(ctx, e.world.OrgA, "draft.txt", []byte("about to be attached"), nil)
	e.clock.advance(8 * 24 * time.Hour) // the draft is past its expiry

	attached, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- e.db.InTenantTx(ctx, e.world.OrgA.ID, func(tx pgx.Tx) error {
			if _, err := e.svc.Attach(ctx, tx, e.world.OrgA.ID, draft.ID, ref); err != nil {
				close(attached)
				return err
			}
			close(attached)
			<-release // the transaction stays open, holding the row
			return nil
		})
	}()
	<-attached
	res, err := e.svc.Prune(context.Background())
	if err != nil || res.Expired != 0 || res.Purged != 0 {
		t.Errorf("prune during the attach = %+v, %v; the locked draft must be skipped", res, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	g, err := e.svc.Get(ctx, e.world.OrgA.ID, draft.ID)
	if err != nil || g.Scope == nil || g.Status != files.StatusReady {
		t.Errorf("after the attach committed: %+v, %v", g, err)
	}
	if res, err = e.svc.Prune(context.Background()); err != nil || res.Expired != 0 {
		t.Errorf("prune after the attach = %+v, %v; an attached file has no expiry", res, err)
	}
}

// A draft that prune already expired cannot be attached any more.
func TestIntegrationAttachAfterPruneExpiredIsRefused(t *testing.T) {
	e := newEnv(t, nil)
	staff := e.world.Actor("staff")
	ctx := e.ctx(staff, e.world.OrgA)
	ref := files.ScopeRef{Type: ticketScope, ID: e.ticket(staff)}
	draft := e.mustUpload(ctx, e.world.OrgA, "draft.txt", []byte("too late"), nil)
	e.clock.advance(8 * 24 * time.Hour)
	if res, err := e.svc.Prune(context.Background()); err != nil || res.Expired != 1 {
		t.Fatalf("prune = %+v, %v", res, err)
	}
	_, err := e.attach(ctx, draft.ID, ref)
	wantStatus(t, err, 404, "attaching an expired draft")
}

// directBlob is a store whose signed URLs are the store's own, like S3.
type directBlob struct{ *storage.Local }

func (directBlob) DirectDownloads() bool { return true }

func TestIntegrationDownloadRedirectsWhenTheStoreServesIt(t *testing.T) {
	e := newEnv(t, nil)
	ctx := e.ctx(e.world.Actor("org_admin"), e.world.OrgA)
	f := e.mustUpload(ctx, e.world.OrgA, "a.txt", []byte("direct"), nil)
	svc := files.NewService(e.db, directBlob{e.blob}, files.Config{Signer: storage.NewSigner([]byte("test secret")), Now: e.clock.now})
	d, err := svc.Open(ctx, e.world.OrgA.ID, f.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.RedirectURL == "" || d.Body != nil {
		t.Errorf("want a redirect and no body, got %+v", d)
	}
	// an unauthorized caller gets no URL
	_, err = svc.Open(e.ctx(e.world.Actor("guard"), e.world.OrgA), e.world.OrgA.ID, f.ID, "")
	wantStatus(t, err, 404, "redirect for someone who cannot read the file")
}

// One pass works through more than a single batch.
func TestIntegrationPruneWorksThroughBatchesUntilExhausted(t *testing.T) {
	e := newEnv(t, nil)
	const n = 450 // more than two batches of 200
	err := e.db.InPlatformTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `
			INSERT INTO file (id, org_id, key, name, mime, status, deleted_at)
			SELECT gen_random_uuid(), $1, 'ghost/' || g, 'x.txt', 'text/plain', 'deleted', now()
			FROM generate_series(1, $2) g`, e.world.OrgA.ID, n)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Prune(context.Background())
	if err != nil || res.Purged != n {
		t.Fatalf("prune = %+v, %v; want all %d purged in one run", res, err, n)
	}
	if e.fileRows() != 0 {
		t.Errorf("%d rows left", e.fileRows())
	}
}
