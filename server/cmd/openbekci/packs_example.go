//go:build examplepack && !nopacks

package main

import (
	"github.com/openbekci/openbekci/packs/example"
	"github.com/openbekci/openbekci/packs/tr"
	"github.com/openbekci/openbekci/sdk"
)

// taggedPacks under -tags examplepack: the Turkish pack and packs/example, the
// tiny pack the tests and the authoring guide use.
func taggedPacks() []sdk.Pack { return []sdk.Pack{tr.New(), example.New()} }
