//go:build !windows

package main

import (
	"fmt"
	"os"
)

func runService(_ []string) {
	fmt.Fprintln(os.Stderr, "service-run is only supported on Windows")
	os.Exit(2)
}
