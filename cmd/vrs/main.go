// vrs — snapshots for your code.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/justinkadima/vrs/internal/cli"
)

func main() {
	err := cli.Run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	if errors.Is(err, cli.ErrUsage) {
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "vrs:", err)
	os.Exit(1)
}
