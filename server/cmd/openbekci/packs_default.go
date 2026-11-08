//go:build !nopacks && !examplepack

package main

import "github.com/openbekci/openbekci/sdk"

// taggedPacks are the packs a build adds through build tags: none by default.
func taggedPacks() []sdk.Pack { return nil }
