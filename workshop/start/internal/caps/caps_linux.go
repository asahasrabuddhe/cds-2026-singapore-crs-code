//go:build linux

package caps

import (
	"fmt"
	"syscall"
)

// prCapbsetDrop is PR_CAPBSET_DROP from <linux/prctl.h>.
const prCapbsetDrop = 24

// DropBounding removes every capability not in keep from the calling
// thread's bounding set. The bounding set is the ceiling: a capability
// dropped from it cannot come back through any execve, which is what makes
// the drop stick for the command the container runs.
//
// It needs CAP_SETPCAP, which root has, and root inside a user namespace
// has. It returns how many were dropped, for the verbose log on stage.
func DropBounding(keep Set) (int, error) {
	dropped := 0
	for _, c := range keep.ToDrop() {
		_, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prCapbsetDrop, uintptr(c), 0, 0, 0, 0)
		switch errno {
		case 0:
			dropped++
		case syscall.EINVAL:
			// This kernel does not know a capability with this number.
			// Nothing to drop, then.
		default:
			return dropped, fmt.Errorf("prctl(PR_CAPBSET_DROP, %s): %w", Name(c), errno)
		}
	}
	return dropped, nil
}
