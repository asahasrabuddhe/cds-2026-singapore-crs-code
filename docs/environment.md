# The demo environment

Everything here was run in the VM that [`setup.md`](setup.md) has every
attendee make: Multipass, Ubuntu 24.04, provisioned from
`provision/user-data.yaml`. `make vm` makes the presenter's the same way,
so the versions below are what everyone in the room has.

## Recorded versions

Measured on 2026-10-24, on an Apple Silicon Mac with Multipass 1.16.4.
State these on stage rather than guessing, and re-run the commands if
this repository is more than a few months old.

| Component | Version | How to check |
|---|---|---|
| Distribution | Ubuntu 24.04.5 LTS | `lsb_release -d` |
| Kernel | 6.8.0-147-generic, aarch64 | `uname -rm` |
| Go | 1.27.1 | `go version` |
| Image | `ubuntu:26.04` | `make rootfs`; `head -1 /var/lib/mycontainer/rootfs/ubuntu/etc/os-release` |
| strace | 6.8 | `strace -V` |
| uidmap (shadow) | 1:4.13+dfsg1-4ubuntu3.2 | `dpkg -s uidmap` |

Everything in this repository needs kernel 5.7 or later, for
`CLONE_INTO_CGROUP`. The pre-flight checks.

## The rootless path

Stock Ubuntu ships `kernel.apparmor_restrict_unprivileged_userns` as `1`,
which refuses a user namespace to any binary without an AppArmor profile,
and that includes everything built in this workshop. The cloud-init file
sets it to `0`. Say this on stage if anyone asks why a laptop of their
own refused the rootless path: everything with `sudo` is unaffected.

Rootless, the runtime needs a cgroup it may write to, and a login shell's
session scope belongs to root. Without limits the runtime says so and
carries on without a cgroup. With limits it refuses, because a limit that
silently did not apply is worse than no container.
`systemd-run --user --scope -p Delegate=yes ./mycontainer run ...` gives
it a delegated cgroup.

## Beat 7, counted

`./scripts/demo.sh 7` runs one container under `strace -f` and tallies the
calls. On this box, for `mycontainer run -memory 64m -pids 20 ubuntu
/bin/true`:

```
     75 prctl
     16 clone
     12 mount
      3 execve
      1 umount2
      1 sethostname
      1 pivot_root
      1 clone3
```

Read it as: one `clone3` carrying every namespace flag and the cgroup
descriptor, twelve `mount` calls (the private remount, the bind, `/proc`,
`/dev`, six device nodes, `/sys`, `/sys/fs/cgroup`), one `pivot_root`, one
`umount2` of the old root, and three `execve`: the runtime, its child,
and the command. Of the `prctl` calls, twenty-seven are
`PR_CAPBSET_DROP`, one is `PR_SET_PDEATHSIG` and one is
`PR_SET_CHILD_SUBREAPER`. The other forty-odd are the Go runtime naming
its memory with `PR_SET_VMA`, so the total moves by one or two from run
to run, as does the number of plain `clone` calls, which are the Go
runtime starting threads in both processes. The `clone3` line itself:

```
clone3({flags=CLONE_VM|CLONE_PIDFD|CLONE_VFORK|CLONE_NEWNS|CLONE_NEWCGROUP|CLONE_NEWUTS|CLONE_NEWIPC|CLONE_NEWPID|CLONE_NEWNET|CLONE_INTO_CGROUP, ..., cgroup=6}
```

That is the whole container, in one syscall, before the child has run an
instruction.

## The mount table

The image lives on the VM's own disk, so
`/var/lib/mycontainer/rootfs/ubuntu` is an ordinary ext4 directory, even
though the repository is a folder shared from the host. Inside a container, `mount` lists its root as
`/dev/sda1 on / type ext4`: the root is a bind mount of that directory,
and the device nodes in `/dev` are bind mounts of the VM's. Every other
mount the VM has is gone.

## The pids floor

`pids.max` counts the Go init's own threads. On this two-CPU VM the floor
is about seven and moves by one from run to run: just under it, `sh`
cannot start, and a little lower the Go runtime dies with "failed to
create new OS thread". More cores mean more threads. Keep `-pids` at
sixteen or more.
