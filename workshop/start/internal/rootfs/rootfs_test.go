package rootfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recorder is a Mounter that records every call in order and does nothing.
// The whole point of the package is the order, so that is what the tests
// check.
type recorder struct {
	calls []string
	fail  string // a call prefix that returns an error
}

func (r *recorder) record(format string, args ...any) error {
	call := fmt.Sprintf(format, args...)
	r.calls = append(r.calls, call)
	if r.fail != "" && strings.HasPrefix(call, r.fail) {
		return errors.New("injected failure")
	}
	return nil
}

func (r *recorder) Mount(source, target, fstype string, flags uintptr, data string) error {
	return r.record("mount %s %s %s %#x %q", source, target, fstype, flags, data)
}
func (r *recorder) Unmount(target string, flags int) error {
	return r.record("unmount %s %#x", target, flags)
}
func (r *recorder) PivotRoot(newRoot, putOld string) error {
	return r.record("pivot_root %s %s", newRoot, putOld)
}
func (r *recorder) Chdir(dir string) error   { return r.record("chdir %s", dir) }
func (r *recorder) Remove(path string) error { return r.record("remove %s", path) }

func newRootfs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"bin", "etc", "proc", "dev"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidate(t *testing.T) {
	good := newRootfs(t)
	if err := Validate(good); err != nil {
		t.Fatalf("good rootfs rejected: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"missing", filepath.Join(t.TempDir(), "nope")},
		{"a file, not a directory", filepath.Join(good, "bin", "sh")},
		{"relative", "rootfs/alpine"},
		{"empty directory", t.TempDir()},
		{"the host root", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.path); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestPivotSequence(t *testing.T) {
	dir := newRootfs(t)
	rec := &recorder{}

	if err := Pivot(rec, dir); err != nil {
		t.Fatalf("Pivot: %v", err)
	}

	want := []string{
		// 1. Stop mount events leaking to the host, recursively.
		fmt.Sprintf("mount  / %s %#x %q", "", MS_REC|MS_PRIVATE, ""),
		// 2. pivot_root needs new_root to be a mount point. Bind it to itself.
		fmt.Sprintf("mount %s %s %s %#x %q", dir, dir, "", MS_BIND|MS_REC, ""),
		// 3. A fresh /proc, or ps inside reads the host's process table.
		fmt.Sprintf("mount proc %s/proc proc %#x %q", dir, MS_NOEXEC|MS_NOSUID|MS_NODEV, ""),
		// 4. An empty /dev, then the handful of nodes a shell expects, bound
		//    from the host because mknod is refused in a user namespace.
		fmt.Sprintf("mount tmpfs %s/dev tmpfs %#x %q", dir, MS_NOSUID|MS_STRICTATIME, "mode=755,size=65536k"),
		fmt.Sprintf("mount /dev/null %s/dev/null  %#x %q", dir, MS_BIND, ""),
		fmt.Sprintf("mount /dev/zero %s/dev/zero  %#x %q", dir, MS_BIND, ""),
		fmt.Sprintf("mount /dev/full %s/dev/full  %#x %q", dir, MS_BIND, ""),
		fmt.Sprintf("mount /dev/random %s/dev/random  %#x %q", dir, MS_BIND, ""),
		fmt.Sprintf("mount /dev/urandom %s/dev/urandom  %#x %q", dir, MS_BIND, ""),
		fmt.Sprintf("mount /dev/tty %s/dev/tty  %#x %q", dir, MS_BIND, ""),
		// 5. /sys read-only, and the container's own cgroup under it.
		fmt.Sprintf("mount sysfs %s/sys sysfs %#x %q", dir, MS_RDONLY|MS_NOSUID|MS_NODEV|MS_NOEXEC, ""),
		fmt.Sprintf("mount cgroup2 %s/sys/fs/cgroup cgroup2 %#x %q", dir, MS_NOSUID|MS_NODEV|MS_NOEXEC, ""),
		// 6. The pivot itself, from inside new_root, with the old root parked
		//    in a directory underneath it.
		fmt.Sprintf("chdir %s", dir),
		fmt.Sprintf("pivot_root . %s", putOld),
		"chdir /",
		// 7. Detach the old root and delete the parking directory. From here
		//    the host filesystem is unreachable.
		fmt.Sprintf("unmount %s %#x", putOld, MNT_DETACH),
		fmt.Sprintf("remove %s", putOld),
	}

	if len(rec.calls) != len(want) {
		t.Fatalf("got %d calls, want %d:\n%s", len(rec.calls), len(want), strings.Join(rec.calls, "\n"))
	}
	for i := range want {
		if rec.calls[i] != want[i] {
			t.Errorf("call %d:\n got %s\nwant %s", i, rec.calls[i], want[i])
		}
	}
}

func TestPivotPreparesTheFilesystemBeforeMounting(t *testing.T) {
	dir := newRootfs(t)
	// No /proc or /dev in this rootfs at all, as in one assembled by hand.
	// Pivot must create them rather than fail on the mount.
	_ = os.Remove(filepath.Join(dir, "proc"))
	_ = os.Remove(filepath.Join(dir, "dev"))

	if err := Pivot(&recorder{}, dir); err != nil {
		t.Fatalf("Pivot: %v", err)
	}

	for _, p := range []string{"proc", "dev", "sys/fs/cgroup", putOld, "dev/null", "dev/tty"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s not created: %v", p, err)
		}
	}
	// The symlinks that make `echo > /dev/stderr` work.
	for link, target := range map[string]string{
		"dev/fd":     "/proc/self/fd",
		"dev/stdin":  "/proc/self/fd/0",
		"dev/stdout": "/proc/self/fd/1",
		"dev/stderr": "/proc/self/fd/2",
	} {
		got, err := os.Readlink(filepath.Join(dir, link))
		if err != nil {
			t.Errorf("%s: %v", link, err)
			continue
		}
		if got != target {
			t.Errorf("%s -> %s, want %s", link, got, target)
		}
	}
}

func TestPivotStopsAtTheFirstFailure(t *testing.T) {
	dir := newRootfs(t)
	rec := &recorder{fail: "pivot_root"}

	err := Pivot(rec, dir)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "pivot_root") {
		t.Errorf("error does not name the failing step: %v", err)
	}
	last := rec.calls[len(rec.calls)-1]
	if !strings.HasPrefix(last, "pivot_root") {
		t.Errorf("kept going after the failure, last call %q", last)
	}
}

func TestPivotRejectsABadRootfsBeforeTouchingAnything(t *testing.T) {
	rec := &recorder{}
	if err := Pivot(rec, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("want error")
	}
	if len(rec.calls) != 0 {
		t.Errorf("made %d calls before validating: %v", len(rec.calls), rec.calls)
	}
}

func TestPivotToleratesAFailedSysMount(t *testing.T) {
	// A user namespace that does not own a network namespace may not mount
	// sysfs, and one without a cgroup namespace may not mount cgroup2. A
	// container with -ns pid,mnt still has to start.
	dir := newRootfs(t)
	for _, prefix := range []string{"mount sysfs", "mount cgroup2"} {
		rec := &recorder{fail: prefix}
		if err := Pivot(rec, dir); err != nil {
			t.Errorf("%s failing was fatal: %v", prefix, err)
		}
		last := rec.calls[len(rec.calls)-1]
		if !strings.HasPrefix(last, "remove") {
			t.Errorf("after %s failed the sequence stopped at %q", prefix, last)
		}
	}
}
