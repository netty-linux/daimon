// harness2-isolation-audit is an optional diagnostic, never a fixture launcher.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/netty-linux/daimon/internal/harness2isolation"
)

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "isolation audit: no arguments accepted")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	s, err := harness2isolation.Inspect(ctx)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "isolation audit unavailable; no execution performed")
		os.Exit(3)
	}
	fmt.Println(string(s.JSON()))
	// Metadata is not admission: even a complete observation report refuses work.
	os.Exit(3)
}
