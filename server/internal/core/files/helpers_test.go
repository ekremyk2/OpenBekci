package files_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/audit"
)

// TestMain keys the audit hashes of PII fields (the file name is one), as the
// server does at start-up from SECRET_KEY.
func TestMain(m *testing.M) {
	if err := audit.SetPIIKey([]byte("0123456789abcdef0123456789abcdef")); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// walk calls fn for every regular file under root.
func walk(root string, fn func(path string)) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			fn(p)
		}
		return nil
	})
}
