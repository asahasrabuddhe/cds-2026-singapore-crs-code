//go:build linux

package namespace

import (
	"syscall"
	"testing"
)

// The package defines the values itself so it compiles and tests on a Mac.
// On Linux, check them against the ones the standard library ships.
func TestCloneFlagsMatchSyscall(t *testing.T) {
	tests := []struct {
		name string
		ours uintptr
		std  uintptr
	}{
		{"CLONE_NEWNS", CloneNewNS, syscall.CLONE_NEWNS},
		{"CLONE_NEWCGROUP", CloneNewCgroup, syscall.CLONE_NEWCGROUP},
		{"CLONE_NEWUTS", CloneNewUTS, syscall.CLONE_NEWUTS},
		{"CLONE_NEWIPC", CloneNewIPC, syscall.CLONE_NEWIPC},
		{"CLONE_NEWUSER", CloneNewUser, syscall.CLONE_NEWUSER},
		{"CLONE_NEWPID", CloneNewPID, syscall.CLONE_NEWPID},
		{"CLONE_NEWNET", CloneNewNet, syscall.CLONE_NEWNET},
	}
	for _, tt := range tests {
		if tt.ours != tt.std {
			t.Errorf("%s: ours %#x, syscall %#x", tt.name, tt.ours, tt.std)
		}
	}
}
