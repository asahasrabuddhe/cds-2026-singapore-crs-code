package initproc

import (
	"syscall"
	"testing"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		ws   syscall.WaitStatus
		want int
	}{
		{"exit 0", exited(0), 0},
		{"exit 3", exited(3), 3},
		{"exit 255", exited(255), 255},
		// The shell convention: a process killed by signal N exits 128+N.
		{"killed by SIGTERM", signaled(syscall.SIGTERM), 143},
		{"killed by SIGKILL", signaled(syscall.SIGKILL), 137},
		{"killed by SIGINT", signaled(syscall.SIGINT), 130},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.ws); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// The encoding of a wait status is the same on Linux and Darwin: low seven
// bits hold the signal, the next byte the exit code.
func exited(code int) syscall.WaitStatus             { return syscall.WaitStatus(code << 8) }
func signaled(sig syscall.Signal) syscall.WaitStatus { return syscall.WaitStatus(sig) }

func TestForwardable(t *testing.T) {
	// SIGCHLD is how the reaper learns there is something to reap; passing
	// it on would tell the child about its own children, which is nonsense.
	// SIGKILL and SIGSTOP cannot be caught, so they never arrive here.
	for sig, want := range map[syscall.Signal]bool{
		syscall.SIGTERM: true,
		syscall.SIGINT:  true,
		syscall.SIGHUP:  true,
		syscall.SIGUSR1: true,
		syscall.SIGCHLD: false,
		syscall.SIGKILL: false,
		syscall.SIGSTOP: false,
	} {
		if got := forwardable(sig); got != want {
			t.Errorf("forwardable(%v) = %v, want %v", sig, got, want)
		}
	}
}
