// Command haiven is the Haiven SDK CLI for game developers integrating the Haiven SDK.
package main

import (
	"os"

	"github.com/haiven-dev/haiven-cli/internal/cli"
)

func main() {
	os.Exit(cli.Main())
}
