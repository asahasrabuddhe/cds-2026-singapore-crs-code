// Package namespace names the seven Linux namespaces a container is made of
// and turns a chosen set of them into clone(2) flags.
//
// The flag values are defined here rather than taken from package syscall,
// because syscall only has them on Linux and this package has to compile,
// and be tested, on the laptop the talk is written on. They are the
// kernel's ABI and have not changed since each namespace was added; the
// Linux-only test in this package checks them against the standard library
// anyway.
package namespace

import (
	"fmt"
	"strings"
)

// Clone flags, from <linux/sched.h>.
const (
	CloneNewNS     uintptr = 0x00020000 // mount
	CloneNewCgroup uintptr = 0x02000000 // cgroup root
	CloneNewUTS    uintptr = 0x04000000 // hostname and domain name
	CloneNewIPC    uintptr = 0x08000000 // System V IPC, POSIX message queues
	CloneNewUser   uintptr = 0x10000000 // UIDs, GIDs, capabilities
	CloneNewPID    uintptr = 0x20000000 // process IDs
	CloneNewNet    uintptr = 0x40000000 // interfaces, routes, ports
)

// Set is the namespaces a container will be created in.
type Set struct {
	PID   bool `json:"pid"`
	Mount bool `json:"mnt"`
	UTS   bool `json:"uts"`
	IPC   bool `json:"ipc"`
	Net   bool `json:"net"`
	User  bool `json:"user"`
	// Cgroup hides the host's cgroup tree: inside, /proc/self/cgroup reads
	// "0::/" and a cgroup2 mount shows the container's own limits at the
	// top level.
	Cgroup bool `json:"cgroup"`
}

// All is every namespace this runtime knows how to create.
func All() Set {
	return Set{PID: true, Mount: true, UTS: true, IPC: true, Net: true, User: true, Cgroup: true}
}

// Parse reads a comma-separated list such as "pid,mnt,uts". "all" and
// "none" are accepted on their own. Names are case-insensitive and "mount"
// is an alias for "mnt", because both get typed.
func Parse(s string) (Set, error) {
	var set Set
	s = strings.TrimSpace(s)
	if s == "" {
		return set, nil
	}

	parts := strings.Split(s, ",")
	for _, p := range parts {
		name := strings.ToLower(strings.TrimSpace(p))
		switch name {
		case "all", "none":
			if len(parts) != 1 {
				return Set{}, fmt.Errorf("namespace %q cannot be combined with other names", name)
			}
			if name == "all" {
				return All(), nil
			}
			return Set{}, nil
		case "pid":
			set.PID = true
		case "mnt", "mount":
			set.Mount = true
		case "uts":
			set.UTS = true
		case "ipc":
			set.IPC = true
		case "net":
			set.Net = true
		case "user":
			set.User = true
		case "cgroup":
			set.Cgroup = true
		default:
			return Set{}, fmt.Errorf("unknown namespace %q (want pid, mnt, uts, ipc, net, user or cgroup)", strings.TrimSpace(p))
		}
	}
	return set, nil
}

// CloneFlags is the value for SysProcAttr.Cloneflags.
func (s Set) CloneFlags() uintptr {
	// TODO(step 1): one bit per namespace that is on. The constants are
	// at the top of this file. Tests: go test ./internal/namespace/
	return 0
}

// String is the inverse of Parse, in a fixed order.
func (s Set) String() string {
	var names []string
	if s.PID {
		names = append(names, "pid")
	}
	if s.Mount {
		names = append(names, "mnt")
	}
	if s.UTS {
		names = append(names, "uts")
	}
	if s.IPC {
		names = append(names, "ipc")
	}
	if s.Net {
		names = append(names, "net")
	}
	if s.User {
		names = append(names, "user")
	}
	if s.Cgroup {
		names = append(names, "cgroup")
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}
