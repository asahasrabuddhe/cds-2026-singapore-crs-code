// img2rootfs: pull an image from a registry and unpack it into a directory
// that mycontainer can use as a root filesystem.
//
//	img2rootfs alpine:3.22                 # writes rootfs/alpine
//	img2rootfs -o /tmp/ubuntu ubuntu:26.04
//
// No Docker daemon, no containerd, no library: the registry protocol is
// three GETs and a token, and an image layer is a tarball. Both fit in one
// file, and reading that file is the point.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"go.ajitem.com/cds-crs/internal/image"
)

func main() {
	out := flag.String("o", "", "output directory (default rootfs/<image name>)")
	arch := flag.String("arch", runtime.GOARCH, "architecture to pull")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: img2rootfs [-o dir] [-arch arm64|amd64] <image>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	ref, err := image.ParseRef(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "img2rootfs:", err)
		os.Exit(2)
	}
	dest := *out
	if dest == "" {
		dest = filepath.Join("rootfs", ref.Name())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	c := &image.Client{
		HTTP: &http.Client{Timeout: 10 * time.Minute},
		OS:   "linux",
		Arch: *arch,
	}
	if err := c.Pull(ctx, ref, dest, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "img2rootfs:", err)
		os.Exit(1)
	}
}
