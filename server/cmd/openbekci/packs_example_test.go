//go:build examplepack && !nopacks

package main

import "testing"

func TestExamplePackBuildListsTheExamplePack(t *testing.T) {
	if len(Official) != 2 || Official[0].Manifest().ID != "tr" || Official[1].Manifest().ID != "example" {
		t.Fatalf("Official = %v, want the Turkish and the example pack", Official)
	}
}
