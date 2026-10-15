# 01. Namespaces: what a container can see

## What it shows

A namespace is a kernel object that a process looks through. Two
processes in different PID namespaces see different process tables; two
in different UTS namespaces see different hostnames. There are seven the
runtime knows about, and each is one bit in the flags passed to
`clone(2)`:

| Flag | Isolates | Inside the container |
|---|---|---|
| `CLONE_NEWPID` | process IDs | the first process is PID 1 |
| `CLONE_NEWNS` | the mount table | a private `/proc`, `/dev`, and the pivot |
| `CLONE_NEWUTS` | hostname | `hostname` is the container id |
| `CLONE_NEWIPC` | System V IPC, POSIX queues | the host's shared memory is invisible |
| `CLONE_NEWNET` | interfaces, routes, ports | a down `lo` and nothing else |
| `CLONE_NEWCGROUP` | the cgroup tree | `/proc/self/cgroup` reads `0::/` |
| `CLONE_NEWUSER` | UIDs, GIDs, capabilities | root, mapped to you |

The demo's first beat is the gotcha everybody hits. A PID namespace
changes the numbers the kernel hands out. It does not change what `ps`
prints, because `ps` reads `/proc`, and `/proc` is a filesystem the host
mounted long before the container existed. Until the container has its
own mount namespace and its own `/proc` mounted from inside the PID
namespace, `ps` shows the host.

The other thing worth saying out loud: a Go program cannot put itself
into a new PID namespace. The namespace only applies to children created
after the call, and the Go runtime is already several threads by the time
`main` runs. So the runtime re-executes its own binary as a child with the
clone flags set, and the child is the container. `runc init` is the same
trick.

## How to run it

```console
$ ./scripts/demo.sh 1
$ ./scripts/demo.sh 6                # the user namespace, as an ordinary user
```

By hand:

```console
$ sudo ./mycontainer run -ns pid,uts -hostname box ubuntu /bin/sh -c 'ls -d /proc/[0-9]* | wc -l'
$ sudo ./mycontainer run -ns pid,uts,mnt -hostname box ubuntu /bin/sh -c 'ls -d /proc/[0-9]* | wc -l'
$ ./mycontainer run ubuntu /bin/sh -c 'id; cat /proc/self/uid_map'
```

## Expected output

With `pid,uts` only, the hostname is `box`, the shell reports `$$` as a
low number, and `/proc` lists the host's processes, 26 on the demo box.
Add `mnt` and the count is 5: the runtime's init at PID 1, the shell, and
the pipeline counting them. With the default set, `sudo ./mycontainer run
ubuntu ps` reads:

```
    PID TTY          TIME CMD
      1 ?        00:00:00 exe
      9 ?        00:00:00 ps
```

PID 1 is the runtime's init, `/proc/self/exe` started again. The command
is a single-digit PID rather than 2, because the init's Go threads take
IDs from the same namespace first.

Rootless, `id` prints `uid=0(root)`, `/proc/self/uid_map` is one line,
`0 501 1` on the demo box, and `/proc/<pid>/status` on the host reads `Uid:
501` and `NSpid: <host pid> 1` for that same process.

## What it proves

Isolation is a per-process view, assembled one namespace at a time, and
each one only covers what it covers. Root inside a user namespace is a
mapping in a text file, not a privilege on the host. The code is
`internal/namespace`, which turns names into flags, and the `SysProcAttr`
literal in `internal/container`, which is the whole difference between
`exec.Command` and a container.
