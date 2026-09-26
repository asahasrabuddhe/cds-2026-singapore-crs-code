//go:build linux

// Package container is the runtime: a parent that creates the cgroup and
// clones the child into new namespaces, and a child that becomes PID 1,
// pivots into the rootfs, drops capabilities and runs the command.
//
// The two halves are two processes, and they have to be. Namespaces are a
// property of a process, and the flags that create them are passed to
// clone(2). A Go program cannot unshare itself into a PID namespace after
// the fact, because the runtime is already multi-threaded by the time main
// runs and a new PID namespace only takes effect for children anyway. So
// the parent re-executes its own binary, /proc/self/exe, with the clone
// flags set, and the child picks up where the parent left off. runc does
// exactly this.
package container

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"go.ajitem.com/cds-crs/internal/caps"
	"go.ajitem.com/cds-crs/internal/cgroup"
	"go.ajitem.com/cds-crs/internal/initproc"
	"go.ajitem.com/cds-crs/internal/rootfs"
	"go.ajitem.com/cds-crs/internal/spec"
)

// ChildCommand is the argv[1] the parent re-executes itself with. Hidden
// from the usage text on purpose: it only makes sense with fd 3 attached.
const ChildCommand = "child"

// specFD is where the child finds its spec. 0, 1 and 2 are stdio, and
// ExtraFiles starts numbering after them.
const specFD = 3

// Run is the parent half. It returns the container's exit code.
func Run(s spec.Spec, log io.Writer) (int, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	if err := rootfs.Validate(s.Rootfs); err != nil {
		return 0, err
	}

	// ---------------------------------------------------------- cgroup ---
	// Created before the child exists, so the child is born inside it and
	// its very first fork already counts against pids.max. That is what
	// CLONE_INTO_CGROUP gives us: no window where the limit is not there.
	group, err := createCgroup(s, log)
	if err != nil {
		return 0, err
	}
	if group != nil {
		defer func() {
			if err := group.Remove(); err != nil {
				fmt.Fprintf(log, "mycontainer: remove cgroup: %v\n", err)
			}
		}()
	}

	// ------------------------------------------------------------ spec ---
	// The child gets its instructions over a pipe rather than argv, so the
	// command line stays readable in ps and the spec can grow without the
	// two halves drifting apart.
	specRead, specWrite, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	if err := s.Encode(specWrite); err != nil {
		return 0, err
	}
	_ = specWrite.Close()

	// ----------------------------------------------------------- clone ---
	// The same binary, again, as "mycontainer child". Everything that
	// makes it a container and not a process happens inside clone(2),
	// through SysProcAttr.
	cmd := exec.Command("/proc/self/exe", ChildCommand)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.ExtraFiles = []*os.File{specRead} // the spec, as fd 3
	cmd.SysProcAttr = sysProcAttr(s, group)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start container: %w", err)
	}
	_ = specRead.Close()

	if s.Verbose {
		fmt.Fprintf(log, "mycontainer: %s is host pid %d\n", s.ID, cmd.Process.Pid)
	}

	// Ctrl-C, or a kill aimed at this process, belongs to the container.
	// Forward everything catchable to its PID 1, the way `docker run` does,
	// and let PID 1 decide what to do with it: our init passes it on to
	// the command, a bare command as PID 1 mostly ignores it.
	sigs := make(chan os.Signal, 16)
	signal.Notify(sigs)
	defer signal.Stop(sigs)
	go func() {
		for sig := range sigs {
			if s, ok := sig.(syscall.Signal); ok && s != syscall.SIGCHLD {
				_ = cmd.Process.Signal(s)
			}
		}
	}()

	err = cmd.Wait()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return initproc.ExitCode(exitErr.Sys().(syscall.WaitStatus)), nil
	default:
		return 0, err
	}
}

// sysProcAttr is the whole difference between exec.Command and a
// container. Six clone flags, a cgroup to be born into, and, when running
// unprivileged, one line of UID mapping.
func sysProcAttr(s spec.Spec, group *cgroup.Group) *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{
		// If this parent dies, the kernel kills the container.
		Pdeathsig: syscall.SIGKILL,
	}
	// TODO(step 1): attr.Cloneflags from s.Namespaces.CloneFlags().
	// TODO(step 3): if group != nil, UseCgroupFD and CgroupFD = group.FD,
	// so the child is born inside its cgroup (CLONE_INTO_CGROUP).
	// TODO(step 6, if there is time): when s.Namespaces.User, one
	// UidMappings and one GidMappings entry, container 0 to os.Getuid()
	// and os.Getgid(), size 1, and GidMappingsEnableSetgroups false.
	return attr
}

// createCgroup makes the container's cgroup, or explains why it could
// not. Without limits a missing cgroup is a warning, because a box that
// does not delegate cgroups to users can still run a container. With
// limits it is an error, because a limit that silently did not apply is
// worse than no container.
func createCgroup(s spec.Spec, log io.Writer) (*cgroup.Group, error) {
	parent := "/"
	if os.Geteuid() != 0 {
		own, err := cgroup.OwnPath()
		if err != nil {
			return nil, err
		}
		parent = own
	}

	group, err := cgroup.Create(cgroup.Root, parent, s.ID, s.Limits)
	if err != nil {
		if len(s.Limits.Files()) == 0 {
			fmt.Fprintf(log, "mycontainer: no cgroup for this container: %v\n", err)
			if os.Geteuid() != 0 {
				fmt.Fprintf(log, "mycontainer: rootless needs a delegated cgroup: systemd-run --user --scope -p Delegate=yes ./mycontainer run ...\n")
			}
			return nil, nil
		}
		return nil, fmt.Errorf("cgroup: %w", err)
	}
	if err := group.Open(); err != nil {
		_ = group.Remove()
		return nil, err
	}
	if s.Verbose {
		fmt.Fprintf(log, "mycontainer: cgroup %s\n", group.Path)
	}
	return group, nil
}

// Child is the other half. It runs as PID 1 of the new PID namespace, in
// the new mount namespace, and turns itself into the container.
func Child() (int, error) {
	// Capabilities are a property of a thread, not a process. Go schedules
	// goroutines across threads, so without this the prctl that drops the
	// bounding set can run on one thread and the fork of the command on
	// another, and the command starts with every capability intact. It
	// happened during development, only in the rootless run, and only
	// sometimes. Never unlocked: this goroutine ends in exec or as init.
	runtime.LockOSThread()

	// This process stays resident as the container's init, inside the
	// container's cgroup, and pids.max counts threads. A Go program starts
	// several and grows more when it is busy; if it needs one while the
	// cgroup is full it dies with "failed to create new OS thread", taking
	// the container with it. One P keeps the runtime small. tini, the C
	// init Docker ships, has one thread; this is the price of Go here, and
	// the reason -pids below about 16 is not a good idea.
	runtime.GOMAXPROCS(1)

	s, err := spec.Decode(os.NewFile(specFD, "spec"))
	if err != nil {
		return 0, err
	}
	say := func(format string, args ...any) {
		if s.Verbose {
			fmt.Fprintf(os.Stderr, "mycontainer[%d]: "+format+"\n", append([]any{os.Getpid()}, args...)...)
		}
	}
	say("child running, namespaces %s", s.Namespaces)

	// 1. Hostname. Only visible in here, because of CLONE_NEWUTS. If the
	//    clone flags did not include it, this process shares the host's
	//    UTS namespace and would rename the host; refuse rather than do
	//    that, because the flags are the first thing anyone edits.
	if s.Hostname != "" {
		if sameNamespace("uts") {
			return 0, errors.New("refusing to set the hostname: not in a new UTS namespace (is CLONE_NEWUTS in the clone flags?)")
		}
		if err := syscall.Sethostname([]byte(s.Hostname)); err != nil {
			return 0, fmt.Errorf("sethostname: %w", err)
		}
		say("hostname %s", s.Hostname)
	}

	// 2. Filesystem. Only possible in here, because of CLONE_NEWNS.
	if s.Namespaces.Mount {
		if err := rootfs.Pivot(rootfs.Linux{}, s.Rootfs); err != nil {
			return 0, err
		}
		say("pivot_root into %s", s.Rootfs)
	}

	// 3. Capabilities. Root in here is about to mean less than it did.
	dropped, err := caps.DropBounding(s.Caps)
	if err != nil {
		return 0, err
	}
	say("dropped %d capabilities, kept %s", dropped, s.Caps)

	// 4. The command, with the container's environment, not the host's.
	os.Clearenv()
	for _, kv := range s.Env {
		if k, v, ok := cutEnv(kv); ok {
			_ = os.Setenv(k, v)
		}
	}
	if s.Init {
		say("running %v as a child, staying as init", s.Args)
		return initproc.Run(s.Args, s.Env)
	}
	path, err := exec.LookPath(s.Args[0])
	if err != nil {
		return 0, err
	}
	say("exec %s as pid 1", path)
	return 0, syscall.Exec(path, s.Args, s.Env)
}

// sameNamespace reports whether this process is in the same namespace of
// the given kind as the host's PID 1. /proc/<pid>/ns/<kind> is a symlink
// whose target names the namespace, so two processes share one exactly
// when the targets match.
func sameNamespace(kind string) bool {
	mine, err1 := os.Readlink("/proc/self/ns/" + kind)
	host, err2 := os.Readlink("/proc/1/ns/" + kind)
	return err1 == nil && err2 == nil && mine == host
}

func cutEnv(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return "", "", false
}
