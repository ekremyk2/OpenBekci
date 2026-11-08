// Package id generates entity identifiers (UUIDv7).
package id

import "github.com/google/uuid"

// New returns a new time-ordered UUIDv7.
//
// uuid.NewV7 only fails when crypto/rand fails, which since Go 1.24 crashes the
// process instead of returning an error, so Must adds no new failure mode.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}
