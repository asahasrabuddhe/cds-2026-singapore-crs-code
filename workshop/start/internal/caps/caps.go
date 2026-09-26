// Package caps names Linux capabilities and decides which ones a container
// keeps. Root inside a container is not root on the host mostly because of
// this list: a process running as UID 0 with CAP_SYS_ADMIN removed from its
// bounding set cannot mount, and no exec will ever give it back.
//
// The numbers are the kernel's, from <linux/capability.h>, and are defined
// here so the package builds on any OS.
package caps

import (
	"fmt"
	"sort"
	"strings"
)

// The capabilities this runtime refers to by name in code. The rest are
// still known by name through names below.
const (
	Chown          = 0
	DacOverride    = 1
	Fowner         = 3
	Fsetid         = 4
	Kill           = 5
	Setgid         = 6
	Setuid         = 7
	Setpcap        = 8
	NetBindService = 10
	NetRaw         = 13
	SysChroot      = 18
	SysAdmin       = 21
	Mknod          = 27
	AuditWrite     = 29
	Setfcap        = 31
)

// Last is CAP_LAST_CAP on every kernel from 5.9 on. Newer kernels may add
// more; prctl(PR_CAPBSET_DROP) on a number the kernel does not know returns
// EINVAL, which the Linux code treats as "nothing to drop".
const Last = 40

var names = [...]string{
	"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_DAC_READ_SEARCH", "CAP_FOWNER",
	"CAP_FSETID", "CAP_KILL", "CAP_SETGID", "CAP_SETUID", "CAP_SETPCAP",
	"CAP_LINUX_IMMUTABLE", "CAP_NET_BIND_SERVICE", "CAP_NET_BROADCAST",
	"CAP_NET_ADMIN", "CAP_NET_RAW", "CAP_IPC_LOCK", "CAP_IPC_OWNER",
	"CAP_SYS_MODULE", "CAP_SYS_RAWIO", "CAP_SYS_CHROOT", "CAP_SYS_PTRACE",
	"CAP_SYS_PACCT", "CAP_SYS_ADMIN", "CAP_SYS_BOOT", "CAP_SYS_NICE",
	"CAP_SYS_RESOURCE", "CAP_SYS_TIME", "CAP_SYS_TTY_CONFIG", "CAP_MKNOD",
	"CAP_LEASE", "CAP_AUDIT_WRITE", "CAP_AUDIT_CONTROL", "CAP_SETFCAP",
	"CAP_MAC_OVERRIDE", "CAP_MAC_ADMIN", "CAP_SYSLOG", "CAP_WAKE_ALARM",
	"CAP_BLOCK_SUSPEND", "CAP_AUDIT_READ", "CAP_PERFMON", "CAP_BPF",
	"CAP_CHECKPOINT_RESTORE",
}

// Name is the CAP_* spelling of a capability number.
func Name(n int) string {
	if n >= 0 && n < len(names) {
		return names[n]
	}
	return fmt.Sprintf("CAP_%d", n)
}

// Lookup finds a capability by name. "CAP_SYS_ADMIN", "sys_admin" and any
// case in between are all accepted.
func Lookup(name string) (int, error) {
	n := strings.ToUpper(strings.TrimSpace(name))
	if n == "" {
		return 0, fmt.Errorf("empty capability name")
	}
	if !strings.HasPrefix(n, "CAP_") {
		n = "CAP_" + n
	}
	for i, known := range names {
		if known == n {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unknown capability %q", strings.TrimSpace(name))
}

// Set is a set of capabilities, as a bitmask.
type Set struct {
	bits uint64
}

// Default is what Docker and the OCI runtime spec grant a container that
// did not ask for anything. Fourteen of forty-one. CAP_SYS_ADMIN, the one
// that mounts, is not among them.
func Default() Set {
	// TODO(step 4): the fourteen Docker grants by default. The constants
	// at the top of this file are exactly that list; add each one.
	// Tests: go test ./internal/caps/
	var s Set
	return s
}

// All is every capability the kernel has. What you hold as real root.
func All() Set {
	var s Set
	for i := 0; i <= Last; i++ {
		s.Add(i)
	}
	return s
}

func (s *Set) Add(c int)       { s.bits |= 1 << uint(c) }
func (s *Set) Remove(c int)    { s.bits &^= 1 << uint(c) }
func (s Set) Has(c int) bool   { return s.bits&(1<<uint(c)) != 0 }
func (s Set) IsEmpty() bool    { return s.bits == 0 }
func (s Set) Equal(o Set) bool { return s.bits == o.bits }

// Names lists the members in kernel order.
func (s Set) Names() []string {
	var out []string
	for i := 0; i <= Last; i++ {
		if s.Has(i) {
			out = append(out, Name(i))
		}
	}
	return out
}

// ToDrop is every capability up to Last that is not in the set, which is
// what gets removed from the bounding set. Ascending order.
func (s Set) ToDrop() []int {
	var out []int
	for i := 0; i <= Last; i++ {
		if !s.Has(i) {
			out = append(out, i)
		}
	}
	return out
}

// Parse reads a capability spec: an optional base ("default", "all" or
// "none") followed by edits ("+CAP_X" adds, "-CAP_X" removes, a bare name
// adds). Empty means the default set.
func Parse(spec string) (Set, error) {
	set := Default()
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return set, nil
	}

	for i, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		switch strings.ToLower(part) {
		case "default", "all", "none":
			if i != 0 {
				return Set{}, fmt.Errorf("capability base %q must come first", part)
			}
			switch strings.ToLower(part) {
			case "all":
				set = All()
			case "none":
				set = Set{}
			}
			continue
		}

		remove := false
		switch {
		case strings.HasPrefix(part, "+"):
			part = part[1:]
		case strings.HasPrefix(part, "-"):
			part = part[1:]
			remove = true
		}
		c, err := Lookup(part)
		if err != nil {
			return Set{}, err
		}
		if remove {
			set.Remove(c)
		} else {
			set.Add(c)
		}
	}
	return set, nil
}

// String is the shortest spec that Parse turns back into the set: one of
// the three bases, plus the edits that get from there to here.
func (s Set) String() string {
	switch {
	case s.IsEmpty():
		return "none"
	case s.Equal(All()):
		return "all"
	}

	base := Default()
	var added, removed []string
	for i := 0; i <= Last; i++ {
		switch {
		case s.Has(i) && !base.Has(i):
			added = append(added, "+"+Name(i))
		case !s.Has(i) && base.Has(i):
			removed = append(removed, "-"+Name(i))
		}
	}
	parts := append([]string{"default"}, added...)
	parts = append(parts, removed...)
	sort.Strings(parts[1:])
	return strings.Join(parts, ",")
}

// MarshalText and UnmarshalText carry the set through JSON as its spec
// string, so a container spec reads "default,+CAP_SYS_ADMIN" rather than a
// bitmask.
func (s Set) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func (s *Set) UnmarshalText(b []byte) error {
	parsed, err := Parse(string(b))
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}
