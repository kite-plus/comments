// Command comments runs the Kite Plus comment service: the HTTP server, the
// background worker and the operator commands.
package main

import (
	"fmt"
	"os"

	"github.com/kite-plus/comments/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "comments:", err)
		os.Exit(1)
	}
}
