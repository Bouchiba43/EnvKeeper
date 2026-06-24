// Command env-sync is the entry point for the env-sync backup daemon. All
// command wiring lives in internal/cli; this file only translates a CLI error
// into a process exit code.
package main

import (
	"fmt"
	"os"

	"github.com/Bouchiba43/EnvKeeper/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "env-sync:", err)
		os.Exit(1)
	}
}
