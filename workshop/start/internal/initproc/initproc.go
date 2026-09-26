// Package initproc is the PID 1 of a container, and it has two jobs that
// the command you asked to run almost certainly does not do itself.
//
// The first is reaping. When a process exits, its parent has to call wait
// on it or it stays in the process table as a zombie. When a parent exits
// before its children, the kernel re-parents those children to PID 1, and
// from then on PID 1 is the only process that can reap them. A container
// whose PID 1 is `sleep` or `nginx` accumulates zombies forever. This is
// why Docker has --init, and why tini exists.
//
// The second is signals. The kernel refuses to deliver a signal with the
// default disposition to PID 1 of a PID namespace, so `docker stop` on a
// container whose PID 1 never installed a handler waits ten seconds and
// then sends SIGKILL. A real init catches every signal it can and passes it
// on to the process it started.
//
// The loop itself is Linux only, because wait4 semantics and the subreaper
// prctl are. The two decisions it makes, what exit code to report and which
// signals to forward, are portable and tested everywhere.
package initproc

import "syscall"

// ExitCode turns a wait status into the code this init should exit with,
// following the shell convention that a death by signal N reports 128+N.
func ExitCode(ws syscall.WaitStatus) int {
	if ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}

// forwardable reports whether a signal received by PID 1 should be passed
// on to the workload.
func forwardable(sig syscall.Signal) bool {
	switch sig {
	case syscall.SIGCHLD, syscall.SIGKILL, syscall.SIGSTOP:
		return false
	}
	return true
}
