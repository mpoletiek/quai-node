// Command quai-node sets up and runs a Quai Network node.
package main

import (
	"fmt"
	"os"

	"github.com/mpoletiek/quai-node/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "quai-node:", err)
		os.Exit(1)
	}
}
