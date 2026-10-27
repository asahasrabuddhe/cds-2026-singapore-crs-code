package namespace

import (
	"syscall"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Set
		wantErr bool
	}{
		{name: "one", in: "pid", want: Set{PID: true}},
		{name: "several", in: "pid,mnt,uts", want: Set{PID: true, Mount: true, UTS: true}},
		{name: "all seven", in: "pid,mnt,uts,ipc,net,user,cgroup", want: Set{PID: true, Mount: true, UTS: true, IPC: true, Net: true, User: true, Cgroup: true}},
		{name: "spaces are tolerated", in: " pid , mnt ", want: Set{PID: true, Mount: true}},
		{name: "case does not matter", in: "PID,Mnt", want: Set{PID: true, Mount: true}},
		{name: "mount is an alias for mnt", in: "mount", want: Set{Mount: true}},
		{name: "all", in: "all", want: All()},
		{name: "none", in: "none", want: Set{}},
		{name: "empty means none", in: "", want: Set{}},
		{name: "repeat is fine", in: "pid,pid", want: Set{PID: true}},
		{name: "unknown name", in: "pid,time", wantErr: true},
		{name: "all mixed with a name is an error", in: "all,pid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCloneFlags(t *testing.T) {
	tests := []struct {
		name string
		set  Set
		want uintptr
	}{
		{name: "nothing", set: Set{}, want: 0},
		{name: "pid only", set: Set{PID: true}, want: syscall.CLONE_NEWPID},
		{name: "pid and mount", set: Set{PID: true, Mount: true}, want: syscall.CLONE_NEWPID | syscall.CLONE_NEWNS},
		{name: "everything", set: All(), want: syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWNET | syscall.CLONE_NEWUSER | syscall.CLONE_NEWCGROUP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.set.CloneFlags(); got != tt.want {
				t.Errorf("got %#x, want %#x", got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		set  Set
		want string
	}{
		{Set{}, "none"},
		{Set{PID: true}, "pid"},
		{Set{Mount: true, PID: true}, "pid,mnt"},
		{All(), "pid,mnt,uts,ipc,net,user,cgroup"},
	}
	for _, tt := range tests {
		if got := tt.set.String(); got != tt.want {
			t.Errorf("%+v: got %q, want %q", tt.set, got, tt.want)
		}
	}
}

func TestParseRoundTrips(t *testing.T) {
	for _, s := range []Set{{}, {PID: true}, {Mount: true, UTS: true}, All()} {
		got, err := Parse(s.String())
		if err != nil {
			t.Fatalf("Parse(%q): %v", s.String(), err)
		}
		if got != s {
			t.Errorf("Parse(%q) = %+v, want %+v", s.String(), got, s)
		}
	}
}
