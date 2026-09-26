package caps

import (
	"slices"
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "CAP_CHOWN", want: 0},
		{in: "CAP_SYS_ADMIN", want: 21},
		{in: "CAP_NET_ADMIN", want: 12},
		{in: "CAP_NET_RAW", want: 13},
		{in: "CAP_SYS_PTRACE", want: 19},
		{in: "CAP_CHECKPOINT_RESTORE", want: 40},
		{in: "chown", want: 0},
		{in: "sys_admin", want: 21},
		{in: "Cap_Sys_Admin", want: 21},
		{in: " CAP_SYS_ADMIN ", want: 21},
		{in: "", wantErr: true},
		{in: "CAP_", wantErr: true},
		{in: "CAP_DOES_NOT_EXIST", wantErr: true},
		{in: "21", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Lookup(tt.in)
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

func TestNameRoundTrips(t *testing.T) {
	for i := 0; i <= Last; i++ {
		name := Name(i)
		got, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(Name(%d)=%q): %v", i, name, err)
		}
		if got != i {
			t.Errorf("Lookup(Name(%d)) = %d", i, got)
		}
	}
	if got := Name(Last + 1); got != "CAP_41" {
		t.Errorf("Name past the end = %q, want a numeric fallback", got)
	}
}

func TestDefaultSet(t *testing.T) {
	// The fourteen capabilities Docker and the OCI runtime spec hand a
	// container by default, in kernel order, pinned so a change here is a
	// deliberate one.
	want := []string{
		"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_FOWNER", "CAP_FSETID",
		"CAP_KILL", "CAP_SETGID", "CAP_SETUID", "CAP_SETPCAP",
		"CAP_NET_BIND_SERVICE", "CAP_NET_RAW", "CAP_SYS_CHROOT", "CAP_MKNOD",
		"CAP_AUDIT_WRITE", "CAP_SETFCAP",
	}
	got := Default().Names()
	if !slices.Equal(got, want) {
		t.Errorf("Default() = %v\nwant %v", got, want)
	}
	if Default().Has(SysAdmin) {
		t.Error("the default set must not hold CAP_SYS_ADMIN")
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "empty is the default set", in: "", want: Default().Names()},
		{name: "default by name", in: "default", want: Default().Names()},
		{name: "all", in: "all", want: All().Names()},
		{name: "none", in: "none", want: nil},
		{name: "add to the default", in: "default,+CAP_SYS_ADMIN", want: append(Default().Names(), "CAP_SYS_ADMIN")},
		{name: "drop from the default", in: "default,-CAP_NET_RAW", want: without(Default().Names(), "CAP_NET_RAW")},
		{name: "add and drop", in: "-CAP_NET_RAW,+CAP_SYS_ADMIN", want: append(without(Default().Names(), "CAP_NET_RAW"), "CAP_SYS_ADMIN")},
		{name: "start from none and add", in: "none,+CAP_NET_BIND_SERVICE", want: []string{"CAP_NET_BIND_SERVICE"}},
		{name: "short names", in: "none,+net_raw", want: []string{"CAP_NET_RAW"}},
		{name: "a bare name is an add", in: "none,CAP_KILL", want: []string{"CAP_KILL"}},
		{name: "unknown capability", in: "+CAP_NOPE", wantErr: true},
		{name: "base after an edit is an error", in: "+CAP_KILL,all", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got.Names())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Names() is in kernel order; the expectations are built by
			// appending, so compare as sets.
			if !slices.Equal(slices.Sorted(slices.Values(got.Names())), slices.Sorted(slices.Values(tt.want))) {
				t.Errorf("got %v\nwant %v", got.Names(), tt.want)
			}
		})
	}
}

func TestToDrop(t *testing.T) {
	// Everything the kernel knows about, minus what we keep, is what has to
	// leave the bounding set. The order is ascending so the prctl loop on
	// stage reads predictably.
	keep := Set{}
	keep.Add(Chown)
	keep.Add(SysAdmin)

	drop := keep.ToDrop()
	if len(drop) != Last+1-2 {
		t.Fatalf("ToDrop returned %d caps, want %d", len(drop), Last+1-2)
	}
	if slices.Contains(drop, Chown) || slices.Contains(drop, SysAdmin) {
		t.Error("kept capability appears in the drop list")
	}
	if !slices.IsSorted(drop) {
		t.Error("drop list is not sorted")
	}

	if got := All().ToDrop(); len(got) != 0 {
		t.Errorf("All().ToDrop() = %v, want nothing", got)
	}
	if got := (Set{}).ToDrop(); len(got) != Last+1 {
		t.Errorf("empty set drops %d, want %d", len(got), Last+1)
	}
}

func TestString(t *testing.T) {
	if got := (Set{}).String(); got != "none" {
		t.Errorf("empty = %q", got)
	}
	if got := All().String(); got != "all" {
		t.Errorf("all = %q", got)
	}
	if got := Default().String(); got != "default" {
		t.Errorf("default = %q", got)
	}
	s := Default()
	s.Add(SysAdmin)
	if got := s.String(); got != "default,+CAP_SYS_ADMIN" {
		t.Errorf("default plus one = %q", got)
	}
	s = Default()
	s.Remove(NetRaw)
	if got := s.String(); got != "default,-CAP_NET_RAW" {
		t.Errorf("default minus one = %q", got)
	}
}

func without(names []string, drop string) []string {
	var out []string
	for _, n := range names {
		if n != drop {
			out = append(out, n)
		}
	}
	return out
}
