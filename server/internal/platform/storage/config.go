package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Config selects and configures the driver (OPENBEKCI_STORAGE_*).
type Config struct {
	// Driver is "local" (default) or "s3".
	Driver string
	// DataDir is the instance data directory; the local driver stores under
	// <DataDir>/files.
	DataDir string
	// SigningKey is the instance secret the local signed URLs derive from.
	SigningKey []byte
	S3         S3Config
}

// New builds the configured driver and the signer for locally served
// downloads (the S3 driver presigns itself; the signer is still returned so
// callers have one code path).
func New(ctx context.Context, c Config) (Blob, *Signer, error) {
	signer := NewSigner(c.SigningKey)
	switch strings.ToLower(c.Driver) {
	case "", "local":
		if c.DataDir == "" {
			return nil, nil, fmt.Errorf("storage: the local driver needs a data directory")
		}
		return NewLocal(filepath.Join(c.DataDir, "files"), signer), signer, nil
	case "s3":
		b, err := NewS3(ctx, c.S3)
		if err != nil {
			return nil, nil, err
		}
		return b, signer, nil
	}
	return nil, nil, fmt.Errorf("storage: unknown driver %q (want local or s3)", c.Driver)
}
