// Command push is the single binary for both project parts:
//   - Part 1 (server): `push agent run`, `push init ...`, `push env ...`, `push status`.
//   - Part 2 (client):  `push deploy` — invoked by the GitHub Action on the runner.
package main

import (
	"fmt"
	"os"

	"github.com/accentiostudios/push/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "push:", err)
		os.Exit(1)
	}
}
