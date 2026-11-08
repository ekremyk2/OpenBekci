//go:build examplepack && !nopacks

package main

import "testing"

func TestExamplePackBuildListsTheExamplePack(t *testing.T) {
	if len(Official) != 1 || Official[0].Manifest().ID != "example" {
		t.Fatalf("Official = %v, want the example pack", Official)
	}
}
