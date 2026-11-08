//go:build !nopacks && !examplepack

package main

import "testing"

// The default build contains the official Turkish pack and nothing else; a
// fork adds its pack by listing it in Official, -tags nopacks removes all.
func TestDefaultBuildListsTheTurkishPack(t *testing.T) {
	if len(Official) != 1 || Official[0].Manifest().ID != "tr" {
		t.Fatalf("the default build lists %v", Official)
	}
}
