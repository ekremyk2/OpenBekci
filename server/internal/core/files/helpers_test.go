package files_test

import (
	"io/fs"
	"path/filepath"
)

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
