//go:build linux

package rootfs

import (
	"syscall"
	"testing"
)

func TestFlagsMatchSyscall(t *testing.T) {
	tests := []struct {
		name string
		ours uintptr
		std  uintptr
	}{
		{"MS_RDONLY", MS_RDONLY, syscall.MS_RDONLY},
		{"MS_NOSUID", MS_NOSUID, syscall.MS_NOSUID},
		{"MS_NODEV", MS_NODEV, syscall.MS_NODEV},
		{"MS_NOEXEC", MS_NOEXEC, syscall.MS_NOEXEC},
		{"MS_BIND", MS_BIND, syscall.MS_BIND},
		{"MS_REC", MS_REC, syscall.MS_REC},
		{"MS_PRIVATE", MS_PRIVATE, syscall.MS_PRIVATE},
		{"MS_STRICTATIME", MS_STRICTATIME, syscall.MS_STRICTATIME},
		{"MNT_DETACH", MNT_DETACH, syscall.MNT_DETACH},
	}
	for _, tt := range tests {
		if tt.ours != tt.std {
			t.Errorf("%s: ours %#x, syscall %#x", tt.name, tt.ours, tt.std)
		}
	}
}
