//go:build linux

package rootfs

import (
	"os"
	"syscall"
)

// Linux is the Mounter that makes real syscalls.
type Linux struct{}

func (Linux) Mount(source, target, fstype string, flags uintptr, data string) error {
	return syscall.Mount(source, target, fstype, flags, data)
}

func (Linux) Unmount(target string, flags int) error {
	return syscall.Unmount(target, flags)
}

func (Linux) PivotRoot(newRoot, putOld string) error {
	return syscall.PivotRoot(newRoot, putOld)
}

func (Linux) Chdir(dir string) error { return os.Chdir(dir) }

func (Linux) Remove(path string) error { return os.Remove(path) }
