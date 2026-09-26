// Package rootfs gives a container its own root filesystem: a fresh /proc
// and /dev inside a directory, then pivot_root(2) so that directory is "/"
// and the host's filesystem is unreachable. This is the difference between
// a container and chroot, and the syscall order matters enough that it is
// what the tests check.
//
// The mount syscalls sit behind the Mounter interface, so the sequence is
// tested with a recorder on any OS. The Linux implementation is in
// rootfs_linux.go and is one line per method.
package rootfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Mount flags, from <linux/mount.h>. Defined here so the package builds
// everywhere; the Linux test checks them against package syscall.
const (
	MS_RDONLY      uintptr = 0x1
	MS_NOSUID      uintptr = 0x2
	MS_NODEV       uintptr = 0x4
	MS_NOEXEC      uintptr = 0x8
	MS_BIND        uintptr = 0x1000
	MS_REC         uintptr = 0x4000
	MS_PRIVATE     uintptr = 0x40000
	MS_STRICTATIME uintptr = 0x1000000

	MNT_DETACH = 0x2
)

// putOld is where the old root is parked during the pivot, relative to the
// new root. Deleted again before the container's command runs.
const putOld = ".pivot_root"

// Mounter is the five syscalls the pivot needs.
type Mounter interface {
	Mount(source, target, fstype string, flags uintptr, data string) error
	Unmount(target string, flags int) error
	PivotRoot(newRoot, putOld string) error
	Chdir(dir string) error
	Remove(path string) error
}

// devices are bound from the host rather than created with mknod, because
// a user namespace refuses mknod for real devices and these six are all a
// shell needs.
var devices = []string{"null", "zero", "full", "random", "urandom", "tty"}

// symlinks are the conventions that make `cmd 2>/dev/stderr` work.
var symlinks = map[string]string{
	"fd":     "/proc/self/fd",
	"stdin":  "/proc/self/fd/0",
	"stdout": "/proc/self/fd/1",
	"stderr": "/proc/self/fd/2",
}

// Validate checks that dir could plausibly be a root filesystem before any
// mount is attempted: absolute, a directory, not empty, and not "/".
func Validate(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("rootfs %q must be an absolute path", dir)
	}
	if filepath.Clean(dir) == "/" {
		return errors.New("rootfs must not be the host's root")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("rootfs: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("rootfs %q is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("rootfs %q is empty (run scripts/rootfs.sh or img2rootfs first)", dir)
	}
	return nil
}

// Pivot makes dir the root filesystem of the calling process. It must run
// inside a new mount namespace, or it will do the same to the host.
func Pivot(m Mounter, dir string) error {
	if err := Validate(dir); err != nil {
		return err
	}

	// Everything the mounts need has to exist first. A rootfs built from
	// busybox has no /proc or /dev at all.
	for _, d := range []string{"proc", "dev", "sys/fs/cgroup", putOld} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}

	steps := []struct {
		name     string
		do       func() error
		optional bool
	}{
		// TODO(step 2): the sequence, in order. Each step is a mount or a
		// syscall through m. docs/workshop.md lists the seven, and the test
		// in rootfs_test.go pins the exact order and flags.
		//
		//   1. make / private, recursively (or the unmount at the end
		//      propagates to the host)
		//   2. bind rootfs onto itself (pivot_root wants a mount point)
		//   3. proc on rootfs/proc
		//   4. tmpfs on rootfs/dev, then populateDev
		//   5. sysfs on rootfs/sys (optional), cgroup2 on
		//      rootfs/sys/fs/cgroup (optional)
		//   6. chdir(rootfs), pivot_root(".", putOld), chdir("/")
		//   7. unmount putOld with MNT_DETACH, remove putOld
	}

	for _, s := range steps {
		if err := s.do(); err != nil {
			if s.optional {
				continue
			}
			return fmt.Errorf("rootfs: %s: %w", s.name, err)
		}
	}
	return nil
}

// populateDev creates the device nodes and symlinks in the fresh /dev. A
// bind mount needs a target file to exist, hence the touch.
func populateDev(m Mounter, dir string) error {
	dev := filepath.Join(dir, "dev")
	for _, name := range devices {
		target := filepath.Join(dev, name)
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			return err
		}
		_ = f.Close()
		if err := m.Mount("/dev/"+name, target, "", MS_BIND, ""); err != nil {
			return fmt.Errorf("bind /dev/%s: %w", name, err)
		}
	}
	for name, target := range symlinks {
		link := filepath.Join(dev, name)
		if err := os.Symlink(target, link); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
}
