//go:build nopacks

package main

import "github.com/openbekci/openbekci/sdk"

// taggedPacks under -tags nopacks: the build contains no pack, whatever else is tagged.
func taggedPacks() []sdk.Pack { return nil }
