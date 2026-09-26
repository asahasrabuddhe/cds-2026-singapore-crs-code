package spec

import (
	"bytes"
	"strings"
	"testing"

	"go.ajitem.com/cds-crs/internal/caps"
	"go.ajitem.com/cds-crs/internal/cgroup"
	"go.ajitem.com/cds-crs/internal/namespace"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := Spec{
		ID:         "abc123def456",
		Rootfs:     "/home/ajitem/rootfs/alpine",
		Hostname:   "abc123def456",
		Namespaces: namespace.All(),
		Limits:     cgroup.Limits{Memory: 64 << 20, CPUs: 0.5, Pids: 32},
		Caps:       caps.Default(),
		Init:       true,
		Args:       []string{"/bin/sh", "-c", "echo hi"},
		Env:        []string{"PATH=/bin", "TERM=xterm"},
		Verbose:    true,
	}

	var buf bytes.Buffer
	if err := in.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := Decode(&buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if got.ID != in.ID || got.Rootfs != in.Rootfs || got.Hostname != in.Hostname {
		t.Errorf("scalars differ: got %+v", got)
	}
	if got.Namespaces != in.Namespaces {
		t.Errorf("Namespaces = %+v, want %+v", got.Namespaces, in.Namespaces)
	}
	if got.Limits != in.Limits {
		t.Errorf("Limits = %+v, want %+v", got.Limits, in.Limits)
	}
	if got.Caps.String() != in.Caps.String() {
		t.Errorf("Caps = %s, want %s", got.Caps, in.Caps)
	}
	if got.Init != in.Init {
		t.Errorf("Init = %v", got.Init)
	}
	if got.Verbose != in.Verbose {
		t.Errorf("Verbose = %v", got.Verbose)
	}
	if strings.Join(got.Args, " ") != strings.Join(in.Args, " ") {
		t.Errorf("Args = %v", got.Args)
	}
	if strings.Join(got.Env, " ") != strings.Join(in.Env, " ") {
		t.Errorf("Env = %v", got.Env)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode(strings.NewReader("not json")); err == nil {
		t.Error("want error")
	}
	if _, err := Decode(strings.NewReader("")); err == nil {
		t.Error("want error on empty input")
	}
}

func TestValidate(t *testing.T) {
	good := Spec{ID: "abc123", Rootfs: "/tmp/rootfs", Args: []string{"/bin/sh"}}

	tests := []struct {
		name string
		mut  func(*Spec)
	}{
		{"no id", func(s *Spec) { s.ID = "" }},
		{"no rootfs", func(s *Spec) { s.Rootfs = "" }},
		{"relative rootfs", func(s *Spec) { s.Rootfs = "rootfs/alpine" }},
		{"no command", func(s *Spec) { s.Args = nil }},
		{"negative pids", func(s *Spec) { s.Limits.Pids = -1 }},
		{"negative cpus", func(s *Spec) { s.Limits.CPUs = -0.5 }},
		{"negative memory", func(s *Spec) { s.Limits.Memory = -1 }},
		{"hostname without a uts namespace", func(s *Spec) { s.Hostname = "x"; s.Namespaces.UTS = false }},
	}

	if err := good.Validate(); err != nil {
		t.Fatalf("good spec rejected: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := good
			tt.mut(&s)
			if err := s.Validate(); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestNewID(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 12 {
		t.Errorf("len(%q) = %d, want 12", a, len(a))
	}
	if a == b {
		t.Errorf("two ids collided: %q", a)
	}
	for _, c := range a {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("id %q is not lowercase hex", a)
			break
		}
	}
}
