package cgroup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMemory(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "64m", want: 64 << 20},
		{in: "64M", want: 64 << 20},
		{in: "512k", want: 512 << 10},
		{in: "1g", want: 1 << 30},
		{in: "1G", want: 1 << 30},
		{in: "1024", want: 1024},
		{in: "0", want: 0},
		{in: " 64m ", want: 64 << 20},
		{in: "", wantErr: true},
		{in: "m", wantErr: true},
		{in: "64mb", wantErr: true},
		{in: "-1m", wantErr: true},
		{in: "1.5g", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseMemory(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCPUMax(t *testing.T) {
	tests := []struct {
		cpus float64
		want string
	}{
		{0.5, "50000 100000"},
		{1, "100000 100000"},
		{2, "200000 100000"},
		{0.25, "25000 100000"},
		{0, "max 100000"},
	}
	for _, tt := range tests {
		if got := cpuMax(tt.cpus); got != tt.want {
			t.Errorf("cpuMax(%v) = %q, want %q", tt.cpus, got, tt.want)
		}
	}
}

func TestLimitsFiles(t *testing.T) {
	tests := []struct {
		name string
		lim  Limits
		want map[string]string
	}{
		{
			name: "nothing set writes nothing",
			lim:  Limits{},
			want: map[string]string{},
		},
		{
			name: "memory only, and swap goes with it",
			lim:  Limits{Memory: 64 << 20},
			want: map[string]string{"memory.max": "67108864", "memory.swap.max": "0"},
		},
		{
			name: "all three",
			lim:  Limits{Memory: 64 << 20, CPUs: 0.5, Pids: 32},
			want: map[string]string{
				"memory.max":      "67108864",
				"memory.swap.max": "0",
				"cpu.max":         "50000 100000",
				"pids.max":        "32",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.lim.Files()
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestLimitsControllers(t *testing.T) {
	tests := []struct {
		lim  Limits
		want string
	}{
		{Limits{}, ""},
		{Limits{Memory: 1}, "+memory"},
		{Limits{Memory: 1, CPUs: 1, Pids: 1}, "+cpu +memory +pids"},
		{Limits{Pids: 5}, "+pids"},
	}
	for _, tt := range tests {
		if got := tt.lim.controllers(); got != tt.want {
			t.Errorf("%+v: got %q, want %q", tt.lim, got, tt.want)
		}
	}
}

func TestParseOwnPath(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name:    "v2 only, the modern default",
			content: "0::/user.slice/user-1000.slice/session-3.scope\n",
			want:    "/user.slice/user-1000.slice/session-3.scope",
		},
		{
			name:    "root cgroup",
			content: "0::/\n",
			want:    "/",
		},
		{
			name:    "hybrid: the v2 line is picked out of the v1 noise",
			content: "12:pids:/user.slice\n1:name=systemd:/user.slice\n0::/user.slice/leaf\n",
			want:    "/user.slice/leaf",
		},
		{
			name:    "no v2 line",
			content: "12:pids:/user.slice\n",
			wantErr: true,
		},
		{
			name:    "empty",
			content: "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOwnPath(strings.NewReader(tt.content))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeFS builds a pretend cgroup v2 mount in a temp dir. The real thing is
// files and directories, so a temp dir is an exact stand-in for everything
// except the kernel's reaction to what gets written.
func fakeFS(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range append([]string{"."}, dirs...) {
		p := filepath.Join(root, d)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"cgroup.subtree_control", "cgroup.procs"} {
			if err := os.WriteFile(filepath.Join(p, f), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestCreateWritesLimitsAndEnablesControllers(t *testing.T) {
	root := fakeFS(t)

	g, err := Create(root, "/", "abc123", Limits{Memory: 64 << 20, CPUs: 0.5, Pids: 32})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := filepath.Join(root, "mycontainer", "abc123")
	if g.Path != want {
		t.Errorf("Path = %q, want %q", g.Path, want)
	}

	// The root and the intermediate "mycontainer" directory both need the
	// controllers switched on for their children, or the leaf's files do
	// not exist on a real kernel.
	for _, dir := range []string{root, filepath.Join(root, "mycontainer")} {
		if got := read(t, filepath.Join(dir, "cgroup.subtree_control")); got != "+cpu +memory +pids" {
			t.Errorf("%s/cgroup.subtree_control = %q", dir, got)
		}
	}

	for file, val := range map[string]string{
		"memory.max":      "67108864",
		"memory.swap.max": "0",
		"cpu.max":         "50000 100000",
		"pids.max":        "32",
	} {
		if got := read(t, filepath.Join(want, file)); got != val {
			t.Errorf("%s = %q, want %q", file, got, val)
		}
	}
}

func TestCreateUnderADelegatedParent(t *testing.T) {
	root := fakeFS(t, "user.slice", "user.slice/user-1000.slice")

	g, err := Create(root, "/user.slice/user-1000.slice", "abc123", Limits{Pids: 10})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := filepath.Join(root, "user.slice", "user-1000.slice", "mycontainer", "abc123")
	if g.Path != want {
		t.Errorf("Path = %q, want %q", g.Path, want)
	}

	// Only the parent and the intermediate get written. Ancestors above the
	// delegated parent are not ours to touch, and on a real box writing them
	// as an unprivileged user fails.
	if got := read(t, filepath.Join(root, "cgroup.subtree_control")); got != "" {
		t.Errorf("root subtree_control was written: %q", got)
	}
	if got := read(t, filepath.Join(root, "user.slice", "cgroup.subtree_control")); got != "" {
		t.Errorf("user.slice subtree_control was written: %q", got)
	}
	for _, dir := range []string{
		filepath.Join(root, "user.slice", "user-1000.slice"),
		filepath.Join(root, "user.slice", "user-1000.slice", "mycontainer"),
	} {
		if got := read(t, filepath.Join(dir, "cgroup.subtree_control")); got != "+pids" {
			t.Errorf("%s/cgroup.subtree_control = %q", dir, got)
		}
	}
}

func TestCreateMovesOccupantsToALeafFirst(t *testing.T) {
	// cgroup v2's "no internal processes" rule: a cgroup that holds
	// processes may not enable controllers for its children. The parent
	// here holds two, the shell and us, so they have to be moved into a leaf
	// before subtree_control is written.
	root := fakeFS(t, "scope")
	parent := filepath.Join(root, "scope")
	if err := os.WriteFile(filepath.Join(parent, "cgroup.procs"), []byte("4242\n4243\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Create(root, "/scope", "abc123", Limits{Pids: 10}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	leaf := filepath.Join(parent, "mycontainer-runtime")
	if got := read(t, filepath.Join(leaf, "cgroup.procs")); got != "4242\n4243\n" {
		t.Errorf("leaf cgroup.procs = %q, want both pids moved", got)
	}
}

func TestCreateUnderTheRootLeavesItsProcessesAlone(t *testing.T) {
	// The root cgroup is exempt from the "no internal processes" rule, and
	// on a real machine it holds kernel threads, such as kthreadd as pid 2,
	// that no write to cgroup.procs can move. Running as root, Create must
	// not try.
	root := fakeFS(t)
	if err := os.WriteFile(filepath.Join(root, "cgroup.procs"), []byte("2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Create(root, "/", "abc123", Limits{Pids: 10}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "mycontainer-runtime")); !os.IsNotExist(err) {
		t.Errorf("a leaf was made under the root cgroup: %v", err)
	}
	if got := read(t, filepath.Join(root, "cgroup.subtree_control")); got != "+pids" {
		t.Errorf("root cgroup.subtree_control = %q", got)
	}
}

func TestCreateWithNoLimitsStillMakesACgroup(t *testing.T) {
	// A container with no limits still gets its own cgroup: that is where
	// `cat /proc/self/cgroup` inside it points, and it is the handle the
	// kernel would use for accounting even with nothing capped.
	root := fakeFS(t)
	g, err := Create(root, "/", "abc123", Limits{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(g.Path); err != nil {
		t.Errorf("cgroup dir missing: %v", err)
	}
	if got := read(t, filepath.Join(root, "cgroup.subtree_control")); got != "" {
		t.Errorf("subtree_control written with nothing to enable: %q", got)
	}
}

func TestRemove(t *testing.T) {
	root := fakeFS(t)
	g, err := Create(root, "/", "abc123", Limits{Pids: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The kernel's cgroupfs deletes the virtual files for you on rmdir. A
	// temp dir does not, so clear them to simulate that.
	entries, _ := os.ReadDir(g.Path)
	for _, e := range entries {
		_ = os.Remove(filepath.Join(g.Path, e.Name()))
	}
	if err := g.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(g.Path); !os.IsNotExist(err) {
		t.Errorf("cgroup dir still present after Remove")
	}
}

func TestIDIsRejectedIfItCouldEscapeTheTree(t *testing.T) {
	root := fakeFS(t)
	for _, id := range []string{"", "..", "a/b", "../x", "."} {
		if _, err := Create(root, "/", id, Limits{}); err == nil {
			t.Errorf("id %q was accepted", id)
		}
	}
}

func TestOpenGivesADirectoryFD(t *testing.T) {
	root := fakeFS(t)
	g, err := Create(root, "/", "abc123", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if g.FD != -1 {
		t.Errorf("FD before Open = %d, want -1", g.FD)
	}
	if err := g.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if g.FD < 0 {
		t.Errorf("FD after Open = %d", g.FD)
	}
	// Remove closes the descriptor before deleting the directory.
	if err := g.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if g.FD != -1 {
		t.Errorf("FD after Remove = %d, want -1", g.FD)
	}
}
