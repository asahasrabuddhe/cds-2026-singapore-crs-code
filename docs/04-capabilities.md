# 04. Capabilities: what a container can do

## What it shows

Root inside a container is `uid=0` and cannot mount a filesystem. Both
halves of that are true at once because since Linux 2.2 the kernel has
not asked "is this UID 0". It asks whether the process holds the
capability for the operation. `mount(2)` wants `CAP_SYS_ADMIN`, and a
default container does not have it.

There are 41 capabilities. Docker and the OCI runtime spec grant a
container fourteen by default, and `internal/caps` grants the same
fourteen:

```
CAP_CHOWN CAP_DAC_OVERRIDE CAP_FOWNER CAP_FSETID CAP_KILL CAP_SETGID
CAP_SETUID CAP_SETPCAP CAP_NET_BIND_SERVICE CAP_NET_RAW CAP_SYS_CHROOT
CAP_MKNOD CAP_AUDIT_WRITE CAP_SETFCAP
```

As a bitmask that is `00000000a80425fb`, which is what `CapBnd` in
`/proc/self/status` reads inside a Docker container and inside one of
ours. The runtime removes the other 27 from the bounding set with
`prctl(PR_CAPBSET_DROP)`, one call each. The bounding set is the ceiling:
a capability dropped from it cannot come back through any `execve`, which
is why dropping it once in the child is enough for every command the
container ever runs.

One Go detail, learned the hard way. Capabilities belong to a thread, not
a process, and Go moves goroutines between threads. The child locks
itself to one OS thread before the drop, or the command may be forked from
a thread that never dropped anything. It happened during development,
only on the rootless path, only sometimes.

## How to run it

```console
$ ./scripts/demo.sh 4
```

By hand:

```console
$ sudo ./mycontainer run rootfs/busybox /bin/sh -c 'grep CapBnd /proc/self/status; mount -t tmpfs none /tmp'
$ sudo ./mycontainer run -caps default,+CAP_SYS_ADMIN rootfs/busybox /bin/sh -c 'mount -t tmpfs none /tmp && echo MOUNTED'
$ sudo ./mycontainer run -caps all rootfs/busybox /bin/sh -c 'grep CapBnd /proc/self/status'
```

`-caps` takes a base, `default`, `all` or `none`, followed by `+CAP_X` and
`-CAP_X` edits.

## Expected output

The first prints `CapBnd: 00000000a80425fb` and `mount: permission denied
(are you root?)`, which is busybox being funny: it is root. The second
prints `CapBnd: 00000000a82425fb` and `MOUNTED`. One bit, bit 21, is the
difference. The third prints `000001ffffffffff`, all 41, which is what
`--privileged` means.

## What it proves

Root is a set of capabilities, and a container is a process that was
handed fourteen of them. Everything a container cannot do as root is a
missing bit in the bounding set. The rootless path drops the same 27, in
a user namespace where even the fourteen only reach what your own UID
already reached.
