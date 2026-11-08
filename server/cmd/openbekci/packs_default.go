//go:build !nopacks && !examplepack

package main

import (
	"github.com/openbekci/openbekci/packs/tr"
	"github.com/openbekci/openbekci/sdk"
)

// taggedPacks are the official packs of the default build: the Turkish pack.
// -tags nopacks leaves the list empty, -tags examplepack adds packs/example.
func taggedPacks() []sdk.Pack { return []sdk.Pack{tr.New()} }
