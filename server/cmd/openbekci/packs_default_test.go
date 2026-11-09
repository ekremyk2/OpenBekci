//go:build !nopacks && !examplepack

package main

import "testing"

// The default build contains no pack: a pack is compiled in only by listing it
// in Official (a fork) or with -tags examplepack (tests).
func TestDefaultBuildHasNoPacks(t *testing.T) {
	if len(Official) != 0 {
		t.Fatalf("the default build lists %d packs", len(Official))
	}
}
