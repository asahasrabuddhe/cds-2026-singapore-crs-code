// Package rootfs gives a container its own root filesystem: a fresh /proc
// and /dev inside a directory, then pivot_root(2) so that directory is "/"
// and the host's filesystem is unreachable. This is the difference between
// a container and chroot, and the syscall order matters enough that it is
// what the tests check.
//
// The mount syscalls sit behind the Mounter interface, so the sequence is
// tested with a recorder, without root. The Linux implementation is in
// rootfs_linux.go and is one line per method.
package rootfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// putOld is where the old root is parked during the pivot, relative to the
// new root. Deleted again before the container's command runs.
const putOld = ".old_root"

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

	// Everything the mounts need has to exist first. An image's /sys is
	// empty, and a rootfs assembled by hand may have no /proc or /dev.
	for _, d := range []string{"proc", "dev", "sys/fs/cgroup", putOld} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}

	steps := []struct {
		name string
		do   func() error
		// optional steps may fail without failing the container. The two
		// /sys mounts are: the kernel only lets a user namespace mount
		// sysfs if it owns the network namespace, and cgroup2 if it owns
		// the cgroup namespace, so a container started with fewer
		// namespaces than the default loses them and nothing else.
		optional bool
	}{
		// Mount events in this namespace must not propagate back to the
		// host, or unmounting the old root below unmounts the host's root.
		// systemd makes every mount shared by default, so this is not
		// optional.
		{"make / private", func() error {
			return m.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, "")
		}, false},
		// pivot_root requires new_root to be a mount point. A directory is
		// not one until it is bind-mounted onto itself.
		{"bind rootfs to itself", func() error {
			return m.Mount(dir, dir, "", syscall.MS_BIND|syscall.MS_REC, "")
		}, false},
		// procfs is per PID namespace: mounted here, from inside the new PID
		// namespace, it shows the container's processes and nothing else.
		{"mount /proc", func() error {
			return m.Mount("proc", filepath.Join(dir, "proc"), "proc", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, "")
		}, false},
		{"mount /dev", func() error {
			return m.Mount("tmpfs", filepath.Join(dir, "dev"), "tmpfs", syscall.MS_NOSUID|syscall.MS_STRICTATIME, "mode=755,size=65536k")
		}, false},
		{"populate /dev", func() error { return populateDev(m, dir) }, false},
		// Read-only, the way Docker mounts it. Writable sysfs is a way out.
		{"mount /sys", func() error {
			return m.Mount("sysfs", filepath.Join(dir, "sys"), "sysfs", syscall.MS_RDONLY|syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "")
		}, true},
		// The container's own cgroup, seen from inside. With a cgroup
		// namespace this is the container's leaf, so `cat
		// /sys/fs/cgroup/memory.max` in here reads the limit set out there.
		{"mount /sys/fs/cgroup", func() error {
			return m.Mount("cgroup2", filepath.Join(dir, "sys", "fs", "cgroup"), "cgroup2", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "")
		}, true},
		// snippet:start pivot
		// pivot_root wants to be called from inside new_root, with put_old
		// underneath it. "." is new_root once we are standing in it.
		{"chdir into rootfs", func() error { return m.Chdir(dir) }, false},
		{"pivot_root", func() error { return m.PivotRoot(".", putOld) }, false},
		{"chdir /", func() error { return m.Chdir("/") }, false},
		// The old root is still mounted at /.old_root and everything on
		// the host is reachable through it. Detach it, then delete the
		// directory so nothing even hints that it was there.
		{"unmount old root", func() error { return m.Unmount(putOld, syscall.MNT_DETACH) }, false},
		{"remove old root dir", func() error { return m.Remove(putOld) }, false},
		// snippet:end pivot
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
		if err := m.Mount("/dev/"+name, target, "", syscall.MS_BIND, ""); err != nil {
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
