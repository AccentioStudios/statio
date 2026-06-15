// Command push is the single binary for both project parts:
//   - Part 1 (server): `statio agent run`, `statio init ...`, `statio env ...`, `statio status`.
//   - Part 2 (client):  `statio deploy` — invoked by the GitHub Action on the runner.
package main

import (
	"fmt"
	"os"

	"github.com/accentiostudios/statio/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "push:", err)
		os.Exit(1)
	}
}
