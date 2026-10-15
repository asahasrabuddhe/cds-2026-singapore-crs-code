# 03. cgroups: what a container can use

## What it shows

cgroup v2 is a filesystem. A directory under `/sys/fs/cgroup` is a group,
the files in it are its knobs, and writing a PID to `cgroup.procs` puts a
process in it. The runtime creates `/sys/fs/cgroup/mycontainer/<id>`,
writes three files, and starts the container in it:

| File | Written | Meaning |
|---|---|---|
| `memory.max` | `67108864` | 64 MB, then the OOM killer |
| `memory.swap.max` | `0` | or `memory.max` is a suggestion |
| `cpu.max` | `50000 100000` | 50 ms of every 100 ms: half a core |
| `pids.max` | `20` | fork returns EAGAIN past twenty |

Two details that are easy to get wrong. First, a cgroup that has
processes in it may not enable controllers for its children, cgroup v2's
"no internal processes" rule. The root cgroup is exempt, so as root the
runtime creates its group directly under `/sys/fs/cgroup`. Under a
delegated user cgroup the shell that started the runtime is sitting in the
parent, so `internal/cgroup` first moves whatever is there into a leaf,
the same trick systemd uses. Second, the child is put in the cgroup by
`clone3` itself, with `CLONE_INTO_CGROUP` and a file descriptor on the
directory (`SysProcAttr.CgroupFD`), so there is no window between fork
and the write to `cgroup.procs` in which the first fork of a fork bomb is
unaccounted. That flag arrived in kernel 5.7.

The cgroup namespace (`CLONE_NEWCGROUP`) makes the container's cgroup its
root, so `/proc/self/cgroup` inside reads `0::/` and a `cgroup2` mount at
`/sys/fs/cgroup` shows the container's own files. That is what lets the
demo ask the container what limits it got.

## How to run it

```console
$ ./scripts/demo.sh 3
```

By hand:

```console
$ sudo ./mycontainer run -memory 64m -cpus 0.5 -pids 20 ubuntu /bin/sh -c \
    'cat /proc/self/cgroup; cat /sys/fs/cgroup/memory.max /sys/fs/cgroup/cpu.max /sys/fs/cgroup/pids.max'
$ sudo ./mycontainer run -pids 20 ubuntu /bin/sh -c 'for i in $(seq 40); do sleep 2 & done'
$ sudo ./mycontainer run -memory 64m ubuntu /bin/sh -c 'a=$(head -c 100000000 /dev/zero | tr "\0" x); echo survived'; echo $?
```

## Expected output

The first prints `0::/`, `67108864`, `50000 100000`, `20`. The fork bomb
prints `/bin/sh: 0: Cannot fork`, which is dash reporting EAGAIN. The hog
prints nothing and exits 137, which is 128 plus SIGKILL: the OOM killer
found the shell holding a 100 MB string in a 64 MB group. After each,
`ls /sys/fs/cgroup/mycontainer/` shows no container directories, because
the runtime removes its group on exit.

## What it proves

Limits are a directory the kernel enforces, and a container's are visible
from inside it. Two honest caveats, both in the README: `pids.max` counts
the init's own threads, so a Go init needs a limit above about 16; and a
runtime that is SIGKILLed leaves its group behind, which `check.sh`
sweeps. Rootless, the runtime needs a cgroup it may write to. Under a
plain login session there is none, it says so and carries on without one;
`systemd-run --user --scope -p Delegate=yes` provides one.
