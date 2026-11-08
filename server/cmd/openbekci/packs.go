package main

import "github.com/openbekci/openbekci/sdk"

// Official lists the packs compiled into the server. There is no init()
// registration: a fork adds its pack to this slice (see docs/packs/README.md).
// The pack list a build gets is chosen by build tags: the default build has the
// Turkish pack (packs_default.go), -tags examplepack adds packs/example (used
// by tests), -tags nopacks leaves the slice empty whatever else is tagged.
var Official = append([]sdk.Pack{}, taggedPacks()...)
