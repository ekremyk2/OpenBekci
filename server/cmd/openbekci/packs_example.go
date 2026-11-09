//go:build examplepack && !nopacks

package main

import (
	"github.com/openbekci/openbekci/packs/example"
	"github.com/openbekci/openbekci/sdk"
)

// taggedPacks under -tags examplepack: packs/example, the tiny pack the tests
// and the authoring guide use.
func taggedPacks() []sdk.Pack { return []sdk.Pack{example.New()} }
