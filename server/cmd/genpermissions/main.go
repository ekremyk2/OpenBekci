// Command genpermissions writes api/src/components/permissions.yaml (the
// Permission enum and the role to permission matrix, x-openbekci-roles) from
// the Go permission registry. `task gen` runs it before bundling the spec.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/openbekci/openbekci/server/internal/permissions"
)

func main() {
	out := flag.String("out", "../api/src/components/permissions.yaml", "output file")
	flag.Parse()
	reg, err := permissions.NewRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "genpermissions:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, reg.SpecYAML(), 0o644); err != nil { //nolint:gosec // a source file in the repository, not a secret
		fmt.Fprintln(os.Stderr, "genpermissions:", err)
		os.Exit(1)
	}
}
