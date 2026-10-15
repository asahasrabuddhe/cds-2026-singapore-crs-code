//go:build linux && integration

// End-to-end tests: real containers, as root, inside the VM.
//
//	make test-root
//
// Each test pins one thing a workshop step or a demo shows, so a step that
// stops working fails here before it fails in front of a room.
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

var repo, binary string

func TestMain(m *testing.M) {
	if os.Geteuid() != 0 {
		println("integration tests need root: make test-root")
		os.Exit(1)
	}
	var err error
	if repo, err = filepath.Abs("../.."); err != nil {
		panic(err)
	}
	binary = filepath.Join(repo, "mycontainer")
	for _, p := range []string{binary, filepath.Join(repo, "rootfs", "ubuntu", "bin", "bash")} {
		if _, err := os.Stat(p); err != nil {
			println("missing", p, "- run make build rootfs first")
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

type result struct {
	stdout, stderr string
	code           int
}

// run executes mycontainer from the repository root, where rootfs/ lives.
func run(t *testing.T, args ...string) result {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = repo
	return wait(t, cmd)
}

func wait(t *testing.T, cmd *exec.Cmd) result {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := result{stdout: out.String(), stderr: errb.String()}
	if exitErr, ok := err.(*exec.ExitError); ok {
		r.code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("%v: %v\n%s", cmd.Args, err, errb.String())
	}
	return r
}

// runAsUser executes mycontainer as the user who ran sudo, with no
// privileges at all: the rootless path.
func runAsUser(t *testing.T, args ...string) (result, int) {
	t.Helper()
	uid, err1 := strconv.Atoi(os.Getenv("SUDO_UID"))
	gid, err2 := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err1 != nil || err2 != nil || uid == 0 {
		t.Skip("run through sudo by an ordinary user: make test-root")
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = repo
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
	return wait(t, cmd), uid
}

// sh is `run ubuntu /bin/sh -c script`.
func sh(script string) []string {
	return []string{"run", "ubuntu", "/bin/sh", "-c", script}
}

func count(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("not a count: %q", s)
	}
	return n
}

// ------------------------------------------------- PID namespace, step 1 ---

func TestBothSidesLogTheirPID(t *testing.T) {
	r := run(t, sh("true")...)
	if !regexp.MustCompile(`(?m)^\[run\] as pid \d+$`).MatchString(r.stderr) {
		t.Errorf("no [run] line:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr, "[child] as pid 1\n") {
		t.Errorf("the child is not PID 1:\n%s", r.stderr)
	}
}

func TestTheCommandIsPID1WithoutTheInit(t *testing.T) {
	// With -init=false the child execs the command, so the command
	// inherits PID 1.
	r := run(t, "run", "-init=false", "ubuntu", "/bin/sh", "-c", "echo $$")
	if strings.TrimSpace(r.stdout) != "1" {
		t.Errorf("$$ = %q, want 1\n%s", r.stdout, r.stderr)
	}
}

func TestExitCodeComesBack(t *testing.T) {
	if r := run(t, sh("exit 5")...); r.code != 5 {
		t.Errorf("exit code %d, want 5", r.code)
	}
}

// -------------------------------------------------------- rootfs, step 2 ---

func TestTheContainerHasItsOwnProc(t *testing.T) {
	// A mount namespace, a fresh /proc mounted from inside the PID
	// namespace, and /proc lists the container: the shell, ls and wc.
	if n := count(t, run(t, sh("ls -d /proc/[0-9]* | wc -l")...).stdout); n > 5 {
		t.Errorf("/proc lists %d processes; want only the container's own", n)
	}
}

func TestTheHostFilesystemIsGone(t *testing.T) {
	marker := filepath.Join("/tmp", "crs-host-marker-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(marker)

	r := run(t, sh("test -e "+marker+" && echo HOST; test -e /.pivot_root && echo OLDROOT; cat /etc/os-release")...)
	if strings.Contains(r.stdout, "HOST") {
		t.Error("a file on the host's /tmp is visible inside")
	}
	if strings.Contains(r.stdout, "OLDROOT") {
		t.Error("the old root's parking directory survived the pivot")
	}
	if !strings.Contains(r.stdout, "Ubuntu") {
		t.Errorf("the rootfs is not the ubuntu image:\n%s", r.stdout)
	}
}

func TestAMissingImageIsAnError(t *testing.T) {
	r := run(t, "run", "no-such-image", "/bin/true")
	if r.code == 0 || !strings.Contains(r.stderr, "rootfs") {
		t.Errorf("want a rootfs error, got code %d:\n%s", r.code, r.stderr)
	}
}

// -------------------------------------------------------- user namespace ---

func TestRootInsideTheUserOutside(t *testing.T) {
	r, uid := runAsUser(t, sh("id -u; cat /proc/self/uid_map")...)
	if r.code != 0 {
		t.Fatalf("no sudo, no container: code %d\n%s", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 2 || lines[0] != "0" {
		t.Fatalf("want uid 0 and a one-line map, got:\n%s", r.stdout)
	}
	if got := strings.Fields(lines[1]); len(got) != 3 || got[0] != "0" || got[1] != strconv.Itoa(uid) || got[2] != "1" {
		t.Errorf("uid_map = %q, want 0 %d 1", lines[1], uid)
	}
}

func TestUnderSudoThereIsNoUserNamespace(t *testing.T) {
	// As Docker does: rootful by default, a user namespace
	// only when not root or asked for with -rootless. The map is the
	// identity map of the initial namespace.
	r := run(t, sh("cat /proc/self/uid_map")...)
	if f := strings.Fields(r.stdout); len(f) != 3 || f[0] != "0" || f[1] != "0" || f[2] != "4294967295" {
		t.Errorf("uid_map = %q, want the initial namespace's", r.stdout)
	}
}

// --------------------------------------------------------- UTS namespace ---

func TestTheHostnameIsTheContainerID(t *testing.T) {
	before, _ := os.Hostname()
	r, _ := runAsUser(t, sh("hostname")...)
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(strings.TrimSpace(r.stdout)) {
		t.Errorf("hostname inside = %q, want the twelve-character container id\n%s", r.stdout, r.stderr)
	}
	if after, _ := os.Hostname(); after != before {
		t.Fatalf("the host was renamed from %q to %q", before, after)
	}
}

// --------------------------------------------------------- IPC namespace ---

func TestTheHostsSharedMemoryIsInvisible(t *testing.T) {
	// The 2025 demo: make a segment on the host, then look for it inside.
	out, err := exec.Command("ipcmk", "-M", "1024").Output()
	if err != nil {
		t.Fatalf("ipcmk: %v", err)
	}
	m := regexp.MustCompile(`id: (\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("ipcmk said %q", out)
	}
	shmid := m[1]
	defer exec.Command("ipcrm", "-m", shmid).Run()

	r, _ := runAsUser(t, sh("ipcs -m")...)
	if !strings.Contains(r.stdout, "Shared Memory Segments") {
		t.Fatalf("ipcs did not run:\n%s%s", r.stdout, r.stderr)
	}
	for _, line := range strings.Split(r.stdout, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[1] == shmid {
			t.Errorf("the host's segment %s is visible inside:\n%s", shmid, r.stdout)
		}
	}
}

// ------------------------------------------------------- cgroups, step 3 ---

func TestLimitsAreVisibleInside(t *testing.T) {
	r := run(t, "run", "-memory", "64m", "-cpus", "0.5", "-pids", "20", "ubuntu", "/bin/sh", "-c",
		"cat /proc/self/cgroup /sys/fs/cgroup/memory.max /sys/fs/cgroup/cpu.max /sys/fs/cgroup/pids.max")
	// 0::/ is the cgroup namespace: the container's cgroup is its root.
	for _, want := range []string{"0::/\n", "67108864\n", "50000 100000\n", "20\n"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q in:\n%s%s", want, r.stdout, r.stderr)
		}
	}
}

func TestAForkBombIsRefused(t *testing.T) {
	r := run(t, "run", "-pids", "20", "ubuntu", "/bin/sh", "-c", "for i in $(seq 40); do sleep 2 & done; wait")
	if !strings.Contains(r.stderr, "Cannot fork") {
		t.Errorf("fork was never refused:\n%s", r.stderr)
	}
}

func TestAMemoryHogIsKilled(t *testing.T) {
	// A 100 MB string in a 64 MB cgroup. The OOM killer takes the shell,
	// and a death by SIGKILL is exit 137.
	r := run(t, "run", "-memory", "64m", "ubuntu", "/bin/sh", "-c", "a=$(head -c 100000000 /dev/zero | tr '\\0' x); echo survived")
	if strings.Contains(r.stdout, "survived") || r.code != 137 {
		t.Errorf("the hog survived, code %d:\n%s", r.code, r.stdout)
	}
}

func TestTheCgroupIsRemovedAfterExit(t *testing.T) {
	sweep()
	run(t, "run", "-pids", "20", "ubuntu", "/bin/true")
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

// sweep removes cgroups an earlier, killed run left behind, so the test
// judges only the container it runs.
func sweep() {
	entries, _ := os.ReadDir("/sys/fs/cgroup/mycontainer")
	for _, e := range entries {
		if e.IsDir() {
			_ = os.Remove(filepath.Join("/sys/fs/cgroup/mycontainer", e.Name()))
		}
	}
}

// ---------------------------------- capabilities and init, steps 4 and 5 ---

func TestEveryNamespaceIsAFlag(t *testing.T) {
	// The step 1 gotcha, on demand: without mnt, /proc is the host's.
	c := "ls -d /proc/[0-9]* | wc -l"
	host := count(t, run(t, "run", "-ns", "pid,uts", "ubuntu", "/bin/sh", "-c", c).stdout)
	own := count(t, run(t, "run", "-ns", "pid,uts,mnt", "ubuntu", "/bin/sh", "-c", c).stdout)
	if host <= own || own > 5 {
		t.Errorf("/proc listed %d without mnt and %d with; want the host's many, then a handful", host, own)
	}
}

func TestAChosenHostname(t *testing.T) {
	before, _ := os.Hostname()
	if r := run(t, "run", "-hostname", "inside", "ubuntu", "hostname"); strings.TrimSpace(r.stdout) != "inside" {
		t.Errorf("hostname inside = %q", r.stdout)
	}
	if after, _ := os.Hostname(); after != before {
		t.Fatalf("the host was renamed from %q to %q", before, after)
	}
}

func TestRootCannotMountWithTheDefaultCapabilities(t *testing.T) {
	r := run(t, sh("grep CapBnd /proc/self/status; mount -t tmpfs none /tmp && echo MOUNTED || echo DENIED")...)
	// 0xa80425fb: the fourteen capabilities Docker leaves a container.
	if !strings.Contains(r.stdout, "00000000a80425fb") {
		t.Errorf("the bounding set is not the default fourteen:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "DENIED") {
		t.Errorf("mount succeeded without CAP_SYS_ADMIN:\n%s", r.stdout)
	}
}

func TestOneCapabilityAndRootCanMount(t *testing.T) {
	r := run(t, "run", "-caps", "default,+CAP_SYS_ADMIN", "ubuntu", "/bin/sh", "-c", "mount -t tmpfs none /tmp && echo MOUNTED || echo DENIED")
	if !strings.Contains(r.stdout, "MOUNTED") {
		t.Errorf("mount denied with CAP_SYS_ADMIN:\n%s%s", r.stdout, r.stderr)
	}
}

// containerPID is the host PID of the container's PID 1: the one child of
// the runtime process.
func containerPID(t *testing.T, parent int) int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(parent)).Output()
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("no container under %d: %q", parent, out)
	}
	return pid
}

func start(t *testing.T, args ...string) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = repo
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	return cmd, containerPID(t, cmd.Process.Pid)
}

func TestTheInitReapsOrphansAndABareCommandDoesNot(t *testing.T) {
	// An orphan re-parented to PID 1, which exits while PID 1 runs on. A
	// command as PID 1 never waits for it; the init does.
	script := `sh -c "sleep 0.2 &"; exec sleep 3`
	for _, tc := range []struct {
		name       string
		init       string
		wantZombie bool
	}{
		{"init=false", "-init=false", true},
		{"init", "-init=true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, init := start(t, "run", tc.init, "ubuntu", "/bin/sh", "-c", script)
			defer func() { _ = syscall.Kill(init, syscall.SIGKILL); _ = cmd.Wait() }()
			out, _ := exec.Command("ps", "-eo", "pid,ppid,stat").Output()
			zombie := regexp.MustCompile(`(?m)^\s*\d+\s+` + strconv.Itoa(init) + `\s+Z`).MatchString(string(out))
			if zombie != tc.wantZombie {
				t.Errorf("zombie under PID 1 = %v, want %v", zombie, tc.wantZombie)
			}
		})
	}
}

func TestTheInitForwardsSignals(t *testing.T) {
	// SIGTERM at the container's PID 1. The kernel drops it for a bare
	// sleep as PID 1; the init passes it on and the container exits 143.
	for _, tc := range []struct {
		name     string
		init     string
		wantDead bool
	}{
		{"init", "-init=true", true},
		{"init=false", "-init=false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, init := start(t, "run", tc.init, "ubuntu", "sleep", "30")
			_ = syscall.Kill(init, syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
				if !tc.wantDead {
					t.Error("PID 1 died of a SIGTERM it never handled")
				} else if code := cmd.ProcessState.ExitCode(); code != 143 {
					t.Errorf("exit code %d, want 143", code)
				}
			case <-time.After(2 * time.Second):
				if tc.wantDead {
					t.Error("the SIGTERM never reached the command")
				}
				_ = syscall.Kill(init, syscall.SIGKILL)
				<-done
			}
		})
	}
}

func TestTheContainerDiesWithTheRuntime(t *testing.T) {
	cmd, init := start(t, "run", "ubuntu", "sleep", "30")
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(init, 0); err == nil {
		_ = syscall.Kill(init, syscall.SIGKILL)
		t.Errorf("container %d outlived its runtime", init)
	}
	// A SIGKILLed runtime never removes its cgroup.
	sweep()
}

func TestCtrlCReachesTheContainer(t *testing.T) {
	// SIGTERM at the runtime itself, which is what Ctrl-C or a kill on the
	// `mycontainer run` PID does. The parent forwards it to PID 1, and the
	// init forwards it to the command.
	cmd, _ := start(t, "run", "ubuntu", "sleep", "30")
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		if code := cmd.ProcessState.ExitCode(); code != 143 {
			t.Errorf("exit code %d, want 143", code)
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		t.Error("SIGTERM to the runtime never reached the container")
	}
}
