//go:build !linux

package main

import (
	"fmt"
	"os"
)

// Namespaces, cgroups and pivot_root are Linux kernel features. On any
// other OS the binary builds, so go build ./... and go vet ./... work on a
// laptop, and says so when run.
func main() {
	fmt.Fprintln(os.Stderr, "mycontainer only runs on Linux. See docs/setup.md for a VM.")
	os.Exit(1)
}
