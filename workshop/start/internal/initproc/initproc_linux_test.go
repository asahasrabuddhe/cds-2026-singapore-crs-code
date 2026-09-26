//go:build linux

package initproc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunReturnsTheChildsExitCode(t *testing.T) {
	code, err := Run([]string{"/bin/sh", "-c", "exit 7"}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Errorf("exit code %d, want 7", code)
	}
}

func TestRunReapsOrphans(t *testing.T) {
	// The inner shell forks a sleep into the background and exits at once,
	// so the sleep is orphaned and re-parented to this process (the
	// subreaper). It exits while the outer shell is still running. Without
	// the reaper loop it would sit in the process table as a zombie until
	// the outer shell exited and took the whole namespace with it, which on
	// a real host is the difference between a tidy container and a ps full
	// of <defunct>.
	code, err := Run([]string{"/bin/sh", "-c", `sh -c "sleep 0.1 &"; sleep 0.5; exit 0`}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if zombies := zombieChildren(t); len(zombies) > 0 {
		t.Errorf("zombies left behind: %v", zombies)
	}
}

func TestRunForwardsSignals(t *testing.T) {
	// The child sleeps for longer than the test is willing to wait. A
	// SIGTERM sent to this process must reach it, and the reported exit
	// code must say so.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()

	start := time.Now()
	code, err := Run([]string{"/bin/sleep", "30"}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if code != 143 {
		t.Errorf("exit code %d, want 143 (128+SIGTERM)", code)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the signal was not forwarded, the child ran to completion")
	}
}

// zombieChildren lists children of this process in state Z, via ps.
func zombieChildren(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("ps", "-o", "pid=,stat=", "--ppid", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		// ps exits 1 when nothing matched, which is the good case.
		return nil
	}
	var zombies []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.HasPrefix(fields[1], "Z") {
			zombies = append(zombies, fields[0])
		}
	}
	return zombies
}
