// Command ivy is the single self-hosted mail binary.
package main

import (
	"os"

	"github.com/AutumnsGrove/Ivy/cmd"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cmd.New(version).Execute(); err != nil {
		os.Exit(1)
	}
}
