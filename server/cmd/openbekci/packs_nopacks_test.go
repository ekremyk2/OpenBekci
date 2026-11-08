//go:build nopacks

package main

import "testing"

// -tags nopacks leaves Official empty, whatever other pack tags are given.
func TestNopacksBuildHasNoPacks(t *testing.T) {
	if len(Official) != 0 {
		t.Fatalf("a nopacks build lists %d packs", len(Official))
	}
}
