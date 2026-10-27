//go:build linux

package initproc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Run starts argv as a child of the calling process, then acts as its init
// until it exits: every catchable signal is forwarded to the child, every
// orphan re-parented to us is reaped, and the child's exit code is returned.
//
// It is meant to be called by PID 1 of a PID namespace. Called anywhere
// else it still works, because it asks to be a subreaper, which is how the
// test exercises it without a namespace.
func Run(argv, env []string) (int, error) {
	// PR_SET_CHILD_SUBREAPER: orphans of our descendants come to us rather
	// than to the real PID 1. A no-op when we already are PID 1.
	const prSetChildSubreaper = 36
	if _, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0, 0, 0, 0); errno != 0 {
		return 0, fmt.Errorf("prctl(PR_SET_CHILD_SUBREAPER): %w", errno)
	}

	// Subscribe before starting the child, or a fast exit races the
	// subscription and the SIGCHLD is lost.
	sigs := make(chan os.Signal, 32)
	signal.Notify(sigs)
	defer signal.Stop(sigs)

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	main := cmd.Process.Pid

	for sig := range sigs {
		s, ok := sig.(syscall.Signal)
		if !ok {
			continue
		}
		if s != syscall.SIGCHLD {
			if forwardable(s) {
				_ = syscall.Kill(main, s)
			}
			continue
		}

		// One SIGCHLD may stand for several exits, so drain until wait4
		// reports nothing left. -1 means any child, ours or adopted.
		code, done, err := reap(main)
		if err != nil {
			return 0, err
		}
		if done {
			// The workload is gone. Anything still running is an orphan of
			// it, and when this process exits as PID 1 the kernel kills the
			// whole namespace anyway. Do it now so the exit is not delayed
			// by a straggler holding the terminal.
			//
			// Only as PID 1. pid -1 means "every process I may signal", and
			// outside a PID namespace that is the user's whole session. The
			// test suite found this out the hard way.
			if os.Getpid() == 1 {
				_ = syscall.Kill(-1, syscall.SIGKILL)
				reapAll()
			}
			return code, nil
		}
	}
	return 0, errors.New("signal channel closed")
}

// reapAll blocks until no children remain. Called after the SIGKILL to
// everything, so it is a formality, but it means PID 1 exits with a clean
// process table rather than leaving the kernel to sweep up.
func reapAll() {
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(-1, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return
		}
	}
}

// reap collects every exited child. It reports the exit code of main and
// done=true once main itself has been collected.
func reap(main int) (code int, done bool, err error) {
	// TODO(step 5): this waits for our own child and nobody else. An
	// orphan re-parented to us is never collected and sits in the process
	// table as a zombie. Loop on wait4(-1, WNOHANG) instead: any child,
	// until there is nothing left, and remember main's exit code.
	// Tests, on Linux: go test ./internal/initproc/
	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(main, &ws, 0, nil); err != nil {
		return 0, false, err
	}
	return ExitCode(ws), true, nil
}
