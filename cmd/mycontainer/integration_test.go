//go:build linux && integration

// End-to-end tests. They build the binary, build a busybox rootfs, and run
// real containers, so they need Linux and root:
//
//	make test-root
//
// Each test pins one thing the talk claims on stage, so a claim that stops
// being true fails here before it fails in front of a room.
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var (
	binary string
	rootfs string
)

func TestMain(m *testing.M) {
	if os.Geteuid() != 0 {
		println("integration tests need root: make test-root")
		os.Exit(1)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(repo, "mycontainer")
	rootfs = filepath.Join(repo, "rootfs", "busybox")
	for _, p := range []string{binary, rootfs} {
		if _, err := os.Stat(p); err != nil {
			println("missing", p, "- run make build rootfs first")
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// runContainer executes `mycontainer run <args...> rootfs sh -c <script>` and
// returns combined output and exit code.
func runContainer(t *testing.T, args []string, script string) (string, int) {
	t.Helper()
	full := append([]string{"run"}, args...)
	full = append(full, rootfs, "/bin/sh", "-c", script)
	cmd := exec.Command(binary, full...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v\n%s", full, err, out.String())
	}
	return out.String(), code
}

func TestPIDNamespaceMakesTheCommandPID1sChild(t *testing.T) {
	out, _ := runContainer(t, nil, "echo pid=$$; ps")
	if !strings.Contains(out, "pid=") {
		t.Fatalf("no pid line:\n%s", out)
	}
	// init is PID 1 and the shell is a low number, not a host PID.
	if !regexp.MustCompile(`(?m)^\s+1 root\s+/proc/self/exe child`).MatchString(out) {
		t.Errorf("PID 1 is not our init:\n%s", out)
	}
	if strings.Contains(out, "systemd") {
		t.Errorf("host processes visible inside the container:\n%s", out)
	}
}

func TestWithoutAMountNamespaceProcIsTheHosts(t *testing.T) {
	// The gotcha: a PID namespace alone changes nothing that reads /proc,
	// because /proc is still the host's. Count its process directories
	// rather than run ps: without a mount namespace the command is the
	// host's own ps, and procps-ng on Ubuntu 24.04 aborts with "fatal
	// library error, lookup self" when /proc/self is a PID it was not told.
	count := "ls -d /proc/[0-9]* | wc -l"
	outside, _ := runContainer(t, []string{"-ns", "pid,uts"}, count)
	inside, _ := runContainer(t, []string{"-ns", "pid,uts,mnt"}, count)

	n := func(s string) int {
		v, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			t.Fatalf("not a count: %q", s)
		}
		return v
	}
	if host, own := n(outside), n(inside); host <= own || own > 5 {
		t.Errorf("without mnt /proc listed %d processes, with mnt %d; want the host's many, then a handful", host, own)
	}
}

func TestUTSNamespaceIsolatesTheHostname(t *testing.T) {
	host, _ := os.Hostname()
	out, _ := runContainer(t, []string{"-hostname", "inside"}, "hostname")
	if strings.TrimSpace(out) != "inside" {
		t.Errorf("hostname inside = %q", strings.TrimSpace(out))
	}
	after, _ := os.Hostname()
	if after != host {
		t.Fatalf("the host was renamed from %q to %q", host, after)
	}
}

func TestPivotRootHidesTheHostFilesystem(t *testing.T) {
	out, _ := runContainer(t, nil, "ls /; test -e /.pivot_root && echo LEAK; test -e /etc/os-release && echo HOST-ETC")
	if strings.Contains(out, "LEAK") {
		t.Error("the put_old directory survived")
	}
	if strings.Contains(out, "HOST-ETC") {
		t.Error("the host's /etc is reachable")
	}
	for _, want := range []string{"bin", "proc", "dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("rootfs is missing %s:\n%s", want, out)
		}
	}
}

func TestExitCodePropagates(t *testing.T) {
	if _, code := runContainer(t, nil, "exit 5"); code != 5 {
		t.Errorf("exit code %d, want 5", code)
	}
	if _, code := runContainer(t, []string{"-init=false"}, "exit 6"); code != 6 {
		t.Errorf("exit code without init %d, want 6", code)
	}
}

func TestCgroupLimitsAreVisibleFromInside(t *testing.T) {
	out, _ := runContainer(t, []string{"-memory", "64m", "-cpus", "0.5", "-pids", "32"},
		"cat /proc/self/cgroup; cat /sys/fs/cgroup/memory.max /sys/fs/cgroup/cpu.max /sys/fs/cgroup/pids.max")
	for _, want := range []string{"0::/\n", "67108864\n", "50000 100000\n", "32\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCgroupIsRemovedAfterExit(t *testing.T) {
	// Sweep what a killed or crashed earlier run may have left, so this
	// test judges only the container it runs.
	sweepCgroups()
	runContainer(t, []string{"-pids", "20"}, "true")
	entries, err := os.ReadDir("/sys/fs/cgroup/mycontainer")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("leftover cgroup %s", e.Name())
		}
	}
}

func TestPidsMaxStopsAForkBomb(t *testing.T) {
	// 20, not 8: the init's own threads count against the limit, see
	// container.Child. Forty forks against twenty slots still gets refused.
	out, _ := runContainer(t, []string{"-pids", "20"}, "for i in $(seq 40); do sleep 2 & done; sleep 0.3; echo forked=$(ps | grep -c 'sleep 2')")
	if !strings.Contains(out, "can't fork") {
		t.Errorf("fork was never refused:\n%s", out)
	}
	if strings.Contains(out, "failed to create new OS thread") {
		t.Errorf("the init itself was starved of threads:\n%s", out)
	}
}

func TestMemoryMaxKillsAHog(t *testing.T) {
	// ash builds a 100 MB string in a 64 MB cgroup and gets OOM-killed. A
	// dead shell exits 137, and its output never includes the last echo.
	out, code := runContainer(t, []string{"-memory", "64m"}, `a=$(head -c 100000000 /dev/zero | tr '\0' x); echo survived`)
	if strings.Contains(out, "survived") || code != 137 {
		t.Errorf("hog survived (code %d):\n%s", code, out)
	}
}

func TestDefaultCapabilitiesCannotMount(t *testing.T) {
	out, _ := runContainer(t, nil, "grep CapBnd /proc/self/status; mount -t tmpfs t /tmp && echo MOUNTED || echo DENIED")
	// 0xa80425fb is the bounding set Docker gives a default container. Same
	// fourteen bits.
	if !strings.Contains(out, "00000000a80425fb") {
		t.Errorf("bounding set is not the default fourteen:\n%s", out)
	}
	if !strings.Contains(out, "DENIED") {
		t.Errorf("mount succeeded without CAP_SYS_ADMIN:\n%s", out)
	}
}

func TestAddingSysAdminAllowsMount(t *testing.T) {
	out, _ := runContainer(t, []string{"-caps", "default,+CAP_SYS_ADMIN"}, "mount -t tmpfs t /tmp && echo MOUNTED || echo DENIED")
	if !strings.Contains(out, "MOUNTED") {
		t.Errorf("mount denied with CAP_SYS_ADMIN:\n%s", out)
	}
}

func TestInitReapsOrphansAndBareCommandDoesNot(t *testing.T) {
	// Seen from the host: a container whose PID 1 is `sleep` collects a
	// zombie, a container with our init in front does not. The orphan is a
	// background sleep whose shell exits at once, so it is re-parented to
	// PID 1 and PID 1 has to reap it. busybox runs sleep inside the forked
	// shell rather than exec'ing it, so the zombie's name is sh.
	script := `sh -c "sleep 0.2 &"; exec sleep 3`

	for _, tc := range []struct {
		name       string
		args       []string
		wantZombie bool
	}{
		{"init=false", []string{"-init=false"}, true},
		{"init", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := append([]string{"run"}, tc.args...)
			full = append(full, rootfs, "/bin/sh", "-c", script)
			cmd := exec.Command(binary, full...)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(1 * time.Second)

			// The container's PID 1, as the host numbers it: the one child
			// of the runtime process. Kill it, not the runtime, so the
			// runtime exits normally and removes its cgroup.
			init := containerPID(t, cmd.Process.Pid)
			defer func() { _ = syscall.Kill(init, syscall.SIGKILL); _ = cmd.Wait() }()

			// ps from the host sees the container's processes with their
			// real state. A zombie whose parent is the container's PID 1
			// is one PID 1 failed to reap.
			out, _ := exec.Command("ps", "-eo", "pid,ppid,stat,comm").Output()
			zombie := regexp.MustCompile(`(?m)^\s*\d+\s+` + strconv.Itoa(init) + `\s+Z`).MatchString(string(out))
			if zombie != tc.wantZombie {
				t.Errorf("zombie under the container's PID 1 = %v, want %v\n%s", zombie, tc.wantZombie, out)
			}
		})
	}
}

func TestInitForwardsSignals(t *testing.T) {
	// SIGTERM to the container's host PID reaches the sleep inside and the
	// container exits 143. Without init, PID 1 is sleep, the kernel drops
	// the signal, and the container is still running a second later.
	for _, tc := range []struct {
		name     string
		args     []string
		wantDead bool
	}{
		{"init", nil, true},
		{"init=false", []string{"-init=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := append([]string{"run"}, tc.args...)
			full = append(full, rootfs, "/bin/sleep", "30")
			cmd := exec.Command(binary, full...)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)

			init := containerPID(t, cmd.Process.Pid)
			_ = syscall.Kill(init, syscall.SIGTERM)

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if !tc.wantDead {
					t.Errorf("container died on SIGTERM: %v", err)
				}
			case <-time.After(2 * time.Second):
				if tc.wantDead {
					t.Error("container ignored SIGTERM")
				}
				_ = syscall.Kill(init, syscall.SIGKILL)
				<-done
			}
		})
	}
}

// containerPID is the host PID of the container's PID 1: the one child of
// the runtime process.
func containerPID(t *testing.T, parent int) int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(parent)).Output()
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("could not find the container's pid under %d: %q", parent, out)
	}
	return pid
}

func TestParentDeathKillsTheContainer(t *testing.T) {
	// SIGKILL the runtime. The container must not outlive it: PR_SET_PDEATHSIG.
	cmd := exec.Command(binary, "run", rootfs, "/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	init := containerPID(t, cmd.Process.Pid)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(init, 0); err == nil {
		_ = syscall.Kill(init, syscall.SIGKILL)
		t.Errorf("container pid %d survived its parent", init)
	}
	// A SIGKILLed parent never removes its cgroup. Known, documented, and
	// swept here so the next test starts clean.
	sweepCgroups()
}

func sweepCgroups() {
	if stale, err := os.ReadDir("/sys/fs/cgroup/mycontainer"); err == nil {
		for _, e := range stale {
			if e.IsDir() {
				_ = os.Remove(filepath.Join("/sys/fs/cgroup/mycontainer", e.Name()))
			}
		}
	}
}

func TestParentForwardsSignalsToTheContainer(t *testing.T) {
	// SIGTERM to the runtime process itself, which is what Ctrl-C in a
	// terminal or a `kill` on the `mycontainer run` pid does. With init
	// the container exits 143. Without it, PID 1 is sleep and ignores the
	// forwarded signal, so the runtime is still waiting a second later.
	for _, tc := range []struct {
		name     string
		args     []string
		wantDead bool
	}{
		{"init", nil, true},
		{"init=false", []string{"-init=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := append([]string{"run"}, tc.args...)
			full = append(full, rootfs, "/bin/sleep", "30")
			cmd := exec.Command(binary, full...)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			init := containerPID(t, cmd.Process.Pid)
			_ = cmd.Process.Signal(syscall.SIGTERM)

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if !tc.wantDead {
					t.Errorf("runtime exited on SIGTERM without init: %v", err)
				} else if code := cmd.ProcessState.ExitCode(); code != 143 {
					t.Errorf("exit code %d, want 143", code)
				}
			case <-time.After(2 * time.Second):
				if tc.wantDead {
					t.Error("SIGTERM to the runtime never reached the container")
				}
				_ = syscall.Kill(init, syscall.SIGKILL)
				<-done
			}
		})
	}
}

func TestRootlessRunsAsAnOrdinaryUser(t *testing.T) {
	// Drop to an unprivileged user and run the whole thing again. Root
	// inside, the caller's uid on the host, no cgroup unless delegated.
	nobody := "65534"
	full := []string{"run", rootfs, "/bin/sh", "-c", "id -u; cat /proc/self/uid_map; grep CapBnd /proc/self/status"}
	cmd := exec.Command(binary, full...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	// The rootfs has to be readable by that user; a busybox rootfs is.
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "permission denied") && strings.Contains(string(out), "user namespace") {
			t.Skip("unprivileged user namespaces are disabled on this kernel")
		}
		t.Fatalf("%v\n%s", err, out)
	}
	s := string(out)
	if !regexp.MustCompile(`(?m)^0$`).MatchString(s) {
		t.Errorf("not root inside:\n%s", s)
	}
	if !strings.Contains(s, "0      "+nobody) && !regexp.MustCompile(`0\s+`+nobody+`\s+1`).MatchString(s) {
		t.Errorf("uid_map does not map 0 to %s:\n%s", nobody, s)
	}
	if !strings.Contains(s, "00000000a80425fb") {
		t.Errorf("rootless bounding set is not the default fourteen:\n%s", s)
	}
}
