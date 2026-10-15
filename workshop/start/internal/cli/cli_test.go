package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.ajitem.com/cds-crs/internal/namespace"
)

func TestParseRunDefaults(t *testing.T) {
	dir := t.TempDir()
	s, err := ParseRun([]string{dir, "/bin/sh", "-c", "id"}, false)
	if err != nil {
		t.Fatal(err)
	}

	if s.Rootfs != dir {
		t.Errorf("Rootfs = %q, want %q", s.Rootfs, dir)
	}
	if len(s.ID) != 12 {
		t.Errorf("ID = %q, want twelve hex characters", s.ID)
	}
	if s.Hostname != s.ID {
		t.Errorf("Hostname = %q, want the container id", s.Hostname)
	}
	// Everything except a user namespace, which is what rootful Docker
	// does. Root does not need one, and asking for one changes the answer
	// to "who am I", which is a different talk.
	want := namespace.All()
	want.User = false
	if s.Namespaces != want {
		t.Errorf("Namespaces = %s, want %s", s.Namespaces, want)
	}
	if !s.Init {
		t.Error("Init should default on")
	}
	if s.Caps.String() != "default" {
		t.Errorf("Caps = %s, want default", s.Caps)
	}
	if strings.Join(s.Args, " ") != "/bin/sh -c id" {
		t.Errorf("Args = %v", s.Args)
	}
	if !contains(s.Env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin") {
		t.Errorf("Env lacks the default PATH: %v", s.Env)
	}
	if !contains(s.Env, "HOME=/root") {
		t.Errorf("Env lacks HOME: %v", s.Env)
	}
}

func TestParseRunRootlessAddsAUserNamespace(t *testing.T) {
	dir := t.TempDir()
	s, err := ParseRun([]string{dir, "/bin/sh"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Namespaces.User {
		t.Error("rootless run without a user namespace")
	}

	s, err = ParseRun([]string{"-rootless", dir, "/bin/sh"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Namespaces.User {
		t.Error("-rootless did not add a user namespace")
	}
}

func TestParseRunFlags(t *testing.T) {
	dir := t.TempDir()
	s, err := ParseRun([]string{
		"-ns", "pid,mnt",
		"-hostname", "box",
		"-memory", "64m",
		"-cpus", "0.5",
		"-pids", "32",
		"-caps", "default,+CAP_SYS_ADMIN",
		"-init=false",
		"-v",
		"-e", "FOO=bar",
		"-e", "TERM=dumb",
		dir, "/bin/sh", "-c", "echo",
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	if s.Namespaces.String() != "pid,mnt,uts" {
		// -hostname implies uts, or the hostname would rename the host.
		t.Errorf("Namespaces = %s", s.Namespaces)
	}
	if s.Hostname != "box" {
		t.Errorf("Hostname = %q", s.Hostname)
	}
	if s.Limits.Memory != 64<<20 || s.Limits.CPUs != 0.5 || s.Limits.Pids != 32 {
		t.Errorf("Limits = %+v", s.Limits)
	}
	if s.Caps.String() != "default,+CAP_SYS_ADMIN" {
		t.Errorf("Caps = %s", s.Caps)
	}
	if s.Init {
		t.Error("Init not switched off")
	}
	if !s.Verbose {
		t.Error("-v not honoured")
	}
	if !contains(s.Env, "FOO=bar") || !contains(s.Env, "TERM=dumb") {
		t.Errorf("Env = %v", s.Env)
	}
	// A -e for a variable the defaults set replaces the default rather than
	// adding a second copy.
	if n := count(s.Env, "TERM="); n != 1 {
		t.Errorf("TERM appears %d times in %v", n, s.Env)
	}
}

func TestParseRunNoHostnameWithoutUTS(t *testing.T) {
	dir := t.TempDir()
	s, err := ParseRun([]string{"-ns", "pid,mnt", dir, "/bin/sh"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Hostname != "" {
		t.Errorf("Hostname = %q with no uts namespace, want empty", s.Hostname)
	}
}

func TestParseRunResolvesTheRootfs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, d := range []string{"rootfs/ubuntu", "mine"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		arg  string
		want string
	}{
		// A bare name is an image under rootfs/.
		{"ubuntu", "rootfs/ubuntu"},
		// Anything with a slash is a path.
		{"./mine", "mine"},
		{filepath.Join(dir, "mine"), "mine"},
	}
	for _, tt := range tests {
		s, err := ParseRun([]string{tt.arg, "/bin/sh"}, false)
		if err != nil {
			t.Fatalf("%s: %v", tt.arg, err)
		}
		want, _ := filepath.EvalSymlinks(filepath.Join(dir, tt.want))
		got, _ := filepath.EvalSymlinks(s.Rootfs)
		if got != want {
			t.Errorf("%s: Rootfs = %q, want %q", tt.arg, got, want)
		}
	}
}

func TestParseRunErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"rootfs but no command", []string{dir}},
		{"bad namespace", []string{"-ns", "pid,bogus", dir, "/bin/sh"}},
		{"bad memory", []string{"-memory", "lots", dir, "/bin/sh"}},
		{"bad capability", []string{"-caps", "+CAP_NOPE", dir, "/bin/sh"}},
		{"negative pids", []string{"-pids", "-1", dir, "/bin/sh"}},
		{"env without equals", []string{"-e", "FOO", dir, "/bin/sh"}},
		{"unknown flag", []string{"-bogus", dir, "/bin/sh"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseRun(tt.args, false); err == nil {
				t.Error("want error")
			}
		})
	}
}

func contains(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func count(env []string, prefix string) int {
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}
