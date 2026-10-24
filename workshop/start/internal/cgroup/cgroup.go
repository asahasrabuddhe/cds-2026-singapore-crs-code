// Package cgroup creates a cgroup v2 leaf for a container and writes its
// limits. cgroup v2 is a filesystem, so everything here is file I/O and the
// tests run against a temp dir on any OS. Only the kernel's reaction to a
// write is missing from the tests, and that is what the VM is for.
package cgroup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Root is where systemd mounts the unified hierarchy.
const Root = "/sys/fs/cgroup"

// dirName is the intermediate directory every container lives under, so
// `ls /sys/fs/cgroup/mycontainer` lists the running containers.
const dirName = "mycontainer"

// leafName holds the processes that were already in the parent cgroup when
// we arrived. See ensureLeaf.
const leafName = "mycontainer-runtime"

// Limits is what the container may use. A zero field means "no limit",
// which writes nothing rather than "max", so a box that does not delegate
// a controller still runs a container that asked for nothing.
type Limits struct {
	Memory int64   `json:"memory"` // bytes
	CPUs   float64 `json:"cpus"`   // 0.5 is half a core
	Pids   int     `json:"pids"`
}

// Group is a created cgroup.
type Group struct {
	// Path is the absolute directory, for example
	// /sys/fs/cgroup/mycontainer/abc123.
	Path string
	// FD is an open descriptor on Path after Open, for
	// SysProcAttr.CgroupFD: clone3 with CLONE_INTO_CGROUP puts the child in
	// the cgroup at birth, so there is never a moment when it is outside
	// its limits. -1 until Open is called.
	FD int

	file *os.File
}

// ParseMemory reads "64m", "512k", "1g" or a bare byte count.
func ParseMemory(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, errors.New("empty memory limit")
	}

	mult := int64(1)
	switch s[len(s)-1] {
	case 'k':
		mult = 1 << 10
		s = s[:len(s)-1]
	case 'm':
		mult = 1 << 20
		s = s[:len(s)-1]
	case 'g':
		mult = 1 << 30
		s = s[:len(s)-1]
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("memory limit %q: want a number with an optional k, m or g", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("memory limit %q: must not be negative", s)
	}
	return n * mult, nil
}

// cpuMax formats cpu.max: "<quota> <period>" in microseconds. The period is
// the kernel's default of 100ms, so 0.5 CPUs is 50ms of every 100ms.
func cpuMax(cpus float64) string {
	// TODO(step 3): the kernel's period is 100000 microseconds. 0.5 CPUs
	// is half of that as the quota; zero or less is "max".
	return ""
}

// Files is the cgroup files to write, keyed by filename.
func (l Limits) Files() map[string]string {
	// TODO(step 3): memory.max in bytes (and memory.swap.max, or the
	// limit is a suggestion), cpu.max as "<quota> <period>" via cpuMax,
	// pids.max as a count. Zero means "no limit" and writes nothing.
	// Tests: go test ./internal/cgroup/
	return map[string]string{}
}

// controllers is the line for cgroup.subtree_control that switches on the
// controllers these limits need, in the order the kernel lists them.
func (l Limits) controllers() string {
	var c []string
	if l.CPUs > 0 {
		c = append(c, "+cpu")
	}
	if l.Memory > 0 {
		c = append(c, "+memory")
	}
	if l.Pids > 0 {
		c = append(c, "+pids")
	}
	return strings.Join(c, " ")
}

// OwnPath is the cgroup this process is in, relative to Root, read from
// /proc/self/cgroup. Under a systemd user session that is something like
// /user.slice/user-1000.slice/session-3.scope, and it is the only place an
// unprivileged process is allowed to create children.
func OwnPath() (string, error) {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	defer f.Close()
	return parseOwnPath(f)
}

// parseOwnPath picks the v2 line, "0::<path>", out of /proc/self/cgroup. A
// hybrid box also lists v1 controllers as "<n>:<name>:<path>", which are
// not ours.
func parseOwnPath(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			return path, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("no cgroup v2 entry in /proc/self/cgroup (is this box on cgroup v1?)")
}

// Create makes <root>/<parent>/mycontainer/<id>, enables the controllers
// the limits need along the way, and writes the limit files.
//
// root is the mount point, normally Root. parent is the cgroup under which
// we are allowed to create children: "/" as root, or OwnPath() otherwise.
// Only parent and the intermediate directory get their subtree_control
// written, because everything above parent belongs to somebody else.
func Create(root, parent, id string, limits Limits) (*Group, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return nil, fmt.Errorf("invalid container id %q", id)
	}

	parentDir := filepath.Join(root, parent)
	midDir := filepath.Join(parentDir, dirName)
	leafDir := filepath.Join(midDir, id)

	if err := os.MkdirAll(midDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", midDir, err)
	}

	if c := limits.controllers(); c != "" {
		// A cgroup with processes in it may not enable controllers for its
		// children. The root cgroup is exempt, which is as well: on a real
		// machine it holds kernel threads that cannot be moved, so as root
		// under "/" there is nothing to do. Under a delegated user scope the
		// rule always bites, because the shell that started us is sitting
		// there.
		if filepath.Clean(parent) != "/" {
			if err := ensureLeaf(parentDir); err != nil {
				return nil, err
			}
		}
		for _, dir := range []string{parentDir, midDir} {
			if err := writeFile(filepath.Join(dir, "cgroup.subtree_control"), c); err != nil {
				return nil, fmt.Errorf("enable controllers in %s: %w", dir, err)
			}
		}
	}

	if err := os.Mkdir(leafDir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create %s: %w", leafDir, err)
	}

	for name, value := range limits.Files() {
		if err := writeFile(filepath.Join(leafDir, name), value); err != nil {
			return nil, fmt.Errorf("set %s: %w", name, err)
		}
	}

	return &Group{Path: leafDir, FD: -1}, nil
}

// Open opens the cgroup directory for use as SysProcAttr.CgroupFD.
func (g *Group) Open() error {
	f, err := os.Open(g.Path)
	if err != nil {
		return fmt.Errorf("open cgroup: %w", err)
	}
	g.FD = int(f.Fd())
	g.file = f
	return nil
}

// ensureLeaf moves whatever processes are in dir into a child cgroup, so
// that dir itself holds none and may enable controllers. systemd calls the
// same trick "the leaf pattern". Empty means nothing to do.
func ensureLeaf(dir string) error {
	procs, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		return fmt.Errorf("read %s/cgroup.procs: %w", dir, err)
	}
	pids := strings.Fields(string(procs))
	if len(pids) == 0 {
		return nil
	}

	leaf := filepath.Join(dir, leafName)
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", leaf, err)
	}
	// One pid per write: the kernel accepts exactly one per write(2) to
	// cgroup.procs, and a temp dir has to behave the same way to be a fair
	// stand-in, so append rather than replace.
	f, err := os.OpenFile(filepath.Join(leaf, "cgroup.procs"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, pid := range pids {
		if _, err := f.WriteString(pid + "\n"); err != nil {
			return fmt.Errorf("move pid %s into %s: %w", pid, leaf, err)
		}
	}
	return nil
}

// Remove deletes the container's cgroup. The kernel refuses while a process
// is still in it, so call it after the container has exited.
func (g *Group) Remove() error {
	if g.file != nil {
		_ = g.file.Close()
		g.file, g.FD = nil, -1
	}
	return os.Remove(g.Path)
}

// writeFile writes without O_TRUNC and without creating the file, because
// cgroupfs files always exist and a create would mask a typo in the name.
// The tests run against a temp dir where the files do not exist yet, so
// creation is allowed there; cgroupfs simply ignores the flag.
func writeFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
