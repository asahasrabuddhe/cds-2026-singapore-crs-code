"""Make the workshop branch from the finished runtime.

The final branch is the finished runtime. main, where the room works, is
the same tree with the bodies of five functions replaced by TODO(step N)
comments, so attendees write them and the finished tests tell them when
they are done. After any change on final, bring main up to date:

    git switch main
    git restore --source final --staged --worktree .
    python3 scripts/stub.py
    git commit -am "Bring main up to date with final"

It stubs the five bodies in place and strips the snippet anchors, which
belong to the finished code. Running it twice changes nothing.
"""
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parent.parent
START = ROOT


def replace_body(rel, signature, body):
    """Replace the body of the top-level func that starts with signature,
    up to its closing brace at column 0."""
    p = START / rel
    s = p.read_text()
    i = s.index(signature)
    j = s.index('\n}\n', i)
    p.write_text(s[:i] + signature + '\n' + body + '\n}\n' + s[j + 3:])


def stub():
    replace_body('internal/namespace/namespace.go',
                 'func (s Set) CloneFlags() uintptr {',
                 '''	var f uintptr
	if s.PID {
		f |= syscall.CLONE_NEWPID
	}
	// TODO(step 1): the other six, the same way. Mount is
	// syscall.CLONE_NEWNS; the rest are CLONE_NEW and their own name.
	// Tests: go test ./internal/namespace/
	return f''')

    p = START / 'internal/rootfs/rootfs.go'
    s = p.read_text()
    i = s.index('	steps := []struct {')
    j = s.index('	for _, s := range steps {')
    s = s[:i] + '''	steps := []struct {
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

''' + s[j:]
    p.write_text(s)

    replace_body('internal/cgroup/cgroup.go',
                 'func (l Limits) Files() map[string]string {',
                 '''	// TODO(step 3): memory.max in bytes (and memory.swap.max, or the
	// limit is a suggestion), cpu.max as "<quota> <period>" via cpuMax,
	// pids.max as a count. Zero means "no limit" and writes nothing.
	// Tests: go test ./internal/cgroup/
	return map[string]string{}''')
    replace_body('internal/cgroup/cgroup.go',
                 'func cpuMax(cpus float64) string {',
                 '''	// TODO(step 3): the kernel's period is 100000 microseconds. 0.5 CPUs
	// is half of that as the quota; zero or less is "max".
	return ""''')

    replace_body('internal/caps/caps.go',
                 'func Default() Set {',
                 '''	// TODO(step 4): the fourteen Docker grants by default. The constants
	// at the top of this file are those fourteen plus SysAdmin, the one
	// Docker leaves out; add the fourteen.
	// Tests: go test ./internal/caps/
	var s Set
	return s''')

    replace_body('internal/initproc/initproc_linux.go',
                 'func reap(main int) (code int, done bool, err error) {',
                 '''	// TODO(step 5): this waits for our own child and nobody else. An
	// orphan re-parented to us is never collected and sits in the process
	// table as a zombie. Loop on wait4(-1, WNOHANG) instead: any child,
	// until there is nothing left, and remember main's exit code.
	// Tests, on Linux: go test ./internal/initproc/
	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(main, &ws, 0, nil); err != nil {
		return 0, false, err
	}
	return ExitCode(ws), true, nil''')

    replace_body('internal/container/container_linux.go',
                 'func sysProcAttr(s spec.Spec, group *cgroup.Group) *syscall.SysProcAttr {',
                 '''	attr := &syscall.SysProcAttr{
		// If this parent dies, the kernel kills the container.
		Pdeathsig: syscall.SIGKILL,
	}
	// TODO(step 1): attr.Cloneflags from s.Namespaces.CloneFlags().
	// TODO(step 3): if group != nil, UseCgroupFD and CgroupFD = group.FD,
	// so the child is born inside its cgroup (CLONE_INTO_CGROUP).
	// TODO(step 6, if there is time): when s.Namespaces.User, one
	// UidMappings and one GidMappings entry, container 0 to os.Getuid()
	// and os.Getgid(), size 1, and GidMappingsEnableSetgroups false.
	return attr''')


def strip_anchors():
    for f in START.rglob('*.go'):
        s = f.read_text()
        t = re.sub(r'\n[ \t]*// snippet:(start|end) \S+', '', s)
        if t != s:
            f.write_text(t)


if __name__ == '__main__':
    stub()
    strip_anchors()
    n = sum(f.read_text().count('TODO(step') for f in START.rglob('*.go'))
    print(f'stubbed: {n} TODO(step) markers')
