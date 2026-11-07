package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/platform/storage"
)

func TestLocalSweepTempRemovesOnlyOldLeftovers(t *testing.T) {
	root := t.TempDir()
	l := storage.NewLocal(root, nil)
	if n, err := l.SweepTemp(time.Hour); err != nil || n != 0 {
		t.Fatalf("sweep of an empty store = %d, %v", n, err)
	}
	if n, err := storage.NewLocal(filepath.Join(root, "missing"), nil).SweepTemp(time.Hour); err != nil || n != 0 {
		t.Fatalf("sweep of a store never written = %d, %v", n, err)
	}
	key := "11111111-1111-1111-1111-111111111111/26/11/22222222-2222-2222-2222-222222222222"
	if err := l.Put(context.Background(), key, strings.NewReader("real blob"), -1, ""); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, filepath.FromSlash(filepath.Dir(key)))
	old, fresh := filepath.Join(dir, ".tmp-old"), filepath.Join(dir, ".tmp-fresh")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("half"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	n, err := l.SweepTemp(time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v; want the one old leftover", n, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old leftover survived")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a write in progress was removed")
	}
	if _, err := l.Stat(context.Background(), key); err != nil {
		t.Error("a real blob was removed")
	}
}

func TestDirectDownloadsOnlyForS3(t *testing.T) {
	if storage.DirectDownloads(storage.NewLocal(t.TempDir(), nil)) {
		t.Error("the local driver streams through the server")
	}
	if !storage.DirectDownloads(&storage.S3{}) {
		t.Error("the S3 driver redirects to presigned URLs")
	}
}
