//go:build linux

// mycontainer: a container runtime small enough to read in one sitting.
//
//	mycontainer run [flags] <rootfs> <command> [args...]
//
// One binary, two roles. Invoked with "run" it is the parent: it builds
// the spec, creates the cgroup, and clones itself into new namespaces.
// Invoked with "child" it is the container's PID 1. See package container
// for why there have to be two.
package main

import (
	"fmt"
	"os"

	"go.ajitem.com/cds-crs/internal/cli"
	"go.ajitem.com/cds-crs/internal/container"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	var (
		code int
		err  error
	)
	switch os.Args[1] {
	case "run":
		code, err = runContainer(os.Args[2:])
	case container.ChildCommand:
		code, err = container.Child()
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, cli.Usage)
		return
	default:
		usage()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "mycontainer: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runContainer(args []string) (int, error) {
	s, err := cli.ParseRun(args, !cli.IsRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "mycontainer: %v\n\n%s", err, cli.Usage)
		os.Exit(2)
	}
	return container.Run(s, os.Stderr)
}

func usage() {
	fmt.Fprint(os.Stderr, cli.Usage)
	os.Exit(2)
}
