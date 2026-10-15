# The workshop

Forty-five minutes. You arrive with a Linux box that passes
`./scripts/check.sh` and leave with a container runtime you wrote the
important parts of, and a test suite that says so.

There is no time in the room to install a VM. Do
[`setup.md`](setup.md) before the day, and if `check.sh` does not say
`ready`, pair with a neighbour rather than debug it during the session.

## How it works

Two copies of the same program are in this repository.

- The root of the repository is the finished runtime. It is the reference,
  the thing the demos run, and where you go when you are stuck.
- `workshop/start/` is where you work. Same layout, same tests, with the
  bodies of five functions replaced by a `TODO(step N)` comment. The tests
  are the finished ones, so they fail until you make them pass.

Six packages fail on Linux, five on a Mac. Five are the steps. The
sixth is `cli`, whose tests use the capability set from step 4, and it
goes green with that step. The init test only runs on Linux, which is the
difference on a Mac.

Everything else in `workshop/start/` is done for you and is not the point
of the day: the flag parsing, the spec that the parent hands the child over
a pipe, the re-exec through `/proc/self/exe`, the `mount` and `pivot_root`
wrappers, the capability name table. Read it if you like. Write the five
things below.

```bash
cd workshop/start
make build          # ./mycontainer, which runs a process with no costume yet
make rootfs         # links rootfs/ubuntu, pulled by the pre-flight before the day
go test ./...       # six packages fail on Linux. That is the syllabus.
```

## The steps

| Time | Step | File | Test | Checkpoint |
|---|---|---|---|---|
| 0:00 | Setup check and the shape of the thing | | `./scripts/check.sh` | a box that says `ready` |
| 0:06 | **1. Namespaces** | `internal/namespace/namespace.go`, `internal/container/container_linux.go` | `go test ./internal/namespace/` | `hostname` inside differs; `ps` still shows the host |
| 0:11 | **2. Rootfs** | `internal/rootfs/rootfs.go` | `go test ./internal/rootfs/` | `ps` shows two processes; `ls /` is the image; a file on the host's `/tmp` is gone |
| 0:21 | **3. cgroups** | `internal/cgroup/cgroup.go`, `container_linux.go` | `go test ./internal/cgroup/` | a fork bomb is refused; `cat /sys/fs/cgroup/pids.max` inside reads the limit |
| 0:29 | **4. Capabilities** | `internal/caps/caps.go` | `go test ./internal/caps/` | `mount` inside is refused; `CapBnd` reads `a80425fb` |
| 0:33 | **5. Init** | `internal/initproc/initproc_linux.go` | `go test ./internal/initproc/` | no zombie under PID 1 after an orphan exits |
| 0:40 | Wrap: the syscalls, counted | | | |
| At home | Rootless | `container_linux.go` | | `./mycontainer run` without sudo prints `uid=0` |

Each step below says what to write, what the test wants, and what to run
to see it. The finished version is in the same file at the repository root.
Copying it is allowed; understanding why it is in that order is the point.

### Step 1: namespaces, what it can see

`CloneFlags()` in `internal/namespace/namespace.go` turns the set of
namespaces into the flags for `clone(2)`. One bit per namespace, constants
at the top of the file. Then in `internal/container/container_linux.go`,
`sysProcAttr` needs `Cloneflags` set from it.

```go
attr.Cloneflags = s.Namespaces.CloneFlags()
```

Check:

```console
$ go test ./internal/namespace/
$ make build
$ sudo ./mycontainer run -ns pid,uts -hostname box ubuntu /bin/sh -c 'hostname; echo $$; ls -d /proc/[0-9]* | wc -l'
```

Until the wiring line is in, the runtime refuses `-hostname` with "not in
a new UTS namespace". That is deliberate: without `CLONE_NEWUTS` the call
would rename your VM.

The hostname is `box` and the shell's PID is small. `/proc` still lists
the host's processes, and that is the first thing worth understanding today:
`ps` reads `/proc`, and `/proc` is the host's until step 2 gives the
container its own. A PID namespace changes the numbers, not what a
filesystem shows.

### Step 2: rootfs, where it lives

`Pivot` in `internal/rootfs/rootfs.go` has an empty `steps` list and a
comment listing the seven. Fill them in, in that order, through the
`Mounter` interface `m`, and the test in `rootfs_test.go` will tell you
which call is wrong and how, because it records every call and compares
the sequence.

The order is the lesson. Make `/` private first or the unmount at the end
propagates to the host and unmounts the host's root. Bind the rootfs onto
itself or `pivot_root` returns EINVAL, because it wants a mount point and
a directory is not one. Mount `/proc` from inside the PID namespace or `ps`
shows the host. Do the pivot from inside the new root, with the old one
parked in a directory underneath it, then detach that directory and delete
it.

Check:

```console
$ go test ./internal/rootfs/
$ make build
$ touch /tmp/host-was-here
$ sudo ./mycontainer run ubuntu /bin/sh -c 'ps; ls /; ls /tmp; mount'
```

Two processes. The Ubuntu image's tree. An empty `/tmp`, without the file
you just made on the host. A mount table with
nothing from the host on it.

### Step 3: cgroups, what it can use

`Files()` in `internal/cgroup/cgroup.go` returns the files to write into
the container's cgroup directory: `memory.max`, `cpu.max`, `pids.max`, and
`memory.swap.max` set to `0` alongside `memory.max`, or the memory limit
is a suggestion. `cpuMax` formats the quota: the period is 100,000
microseconds, so 0.5 CPUs is `50000 100000`. Then in `sysProcAttr`, when
there is a group:

```go
attr.UseCgroupFD = true
attr.CgroupFD = group.FD
```

That is `CLONE_INTO_CGROUP`: the child is born inside its cgroup, and the
first fork of a fork bomb is already counted.

Check:

```console
$ go test ./internal/cgroup/
$ make build
$ sudo ./mycontainer run -pids 20 ubuntu /bin/sh -c 'cat /sys/fs/cgroup/pids.max; for i in $(seq 40); do sleep 2 & done'
$ sudo ./mycontainer run -memory 64m ubuntu /bin/sh -c 'a=$(head -c 100000000 /dev/zero | tr "\0" x); echo survived'; echo $?
```

`20`, then `can't fork`. Then nothing, and 137.

Two things you will hit if you go off the path. The init is a Go program
inside the cgroup, and `pids.max` counts its threads, so below about 16 the
init dies before the bomb does. And cgroup v2 refuses to enable controllers
in a directory that has processes in it; `Create` handles that with a leaf,
and it is why the runtime as root creates its groups directly under
`/sys/fs/cgroup`, which is exempt.

### Step 4: capabilities, what it can do

`Default()` in `internal/caps/caps.go` returns the fourteen capabilities
Docker grants a container that asked for nothing. The constants at the top
of the file are exactly that list. `DropBounding` in `caps_linux.go`
already removes everything not in the set from the bounding set, one
`prctl(PR_CAPBSET_DROP)` each.

Check:

```console
$ go test ./internal/caps/
$ make build
$ sudo ./mycontainer run ubuntu /bin/sh -c 'grep CapBnd /proc/self/status; mount -t tmpfs none /tmp'
$ sudo ./mycontainer run -caps default,+CAP_SYS_ADMIN ubuntu /bin/sh -c 'mount -t tmpfs none /tmp && echo MOUNTED'
```

`a80425fb` and a refusal, then `a82425fb` and `MOUNTED`. Bit 21.

One Go trap, already handled in `Child` and worth reading: capabilities
belong to a thread, and Go moves goroutines between threads, so the child
locks itself to one OS thread before the drop. Without that, the command
is sometimes forked from a thread that still holds everything.

### Step 5: init, what PID 1 owes

`reap` in `internal/initproc/initproc_linux.go` is the naive version: it
waits for its own child and nobody else. An orphan re-parented to PID 1 is
never collected and sits in the process table as a zombie. Rewrite it as a
loop on `wait4(-1, WNOHANG)`: any child, ours or adopted, until `ECHILD`
(nothing left) or `0` (children exist, none exited). Remember the main
child's exit code when its PID comes back; forget the rest.

Check, on Linux:

```console
$ go test ./internal/initproc/
$ make build
$ sudo ./mycontainer run ubuntu /bin/sh -c 'sh -c "sleep 0.3 &"; exec sleep 5' &
$ sleep 1; ps -eo pid,ppid,stat,comm | grep -E 'Z|sleep|exe'
```

No `Z`. Run the same with `-init=false` and there is one, under the
`sleep` that is now PID 1. That is the difference between a container that
runs for a week and one that fills its process table.

### At home: rootless

Six lines in `sysProcAttr`: when `s.Namespaces.User` is set, one
`UidMappings` entry and one `GidMappings` entry mapping container 0 to
your own UID and GID, size 1, with `GidMappingsEnableSetgroups` false.
Then, with no sudo:

```console
$ ./mycontainer run ubuntu /bin/sh -c 'id; cat /proc/self/uid_map'
```

`uid=0(root)` inside, `0 1000 1` in the map, and `/proc/<pid>/status` on
the host says `Uid: 1000`. Both true. Where that leads, `/etc/subuid`,
`newuidmap` and a whole UID range, is a different talk.

## If you fall behind

Copy the finished file over yours. Every step's file at the repository
root is the answer, with the comments that explain it:

```bash
cp ../../internal/rootfs/rootfs.go internal/rootfs/rootfs.go
```

Then `go test ./internal/rootfs/` is green and you carry on with the next
step. Nobody is keeping score.

## If a test passes on your laptop and the container does not run

The pure packages test on macOS; the container only runs on Linux.
`make build` on a Mac produces a binary that says so. Everything in the
checkpoints runs inside the VM from `docs/setup.md`.
