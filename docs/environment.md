# The demo environment

Everything here was run on the box that `make vm` builds: an OrbStack
machine provisioned from `provision/user-data.yaml`. The same file builds
the Multipass and Lima boxes in [`setup.md`](setup.md), so the versions
below are what an attendee gets too, apart from the kernel.

## Recorded versions

Measured on 2026-10-15. State these on stage rather than guessing, and
re-run the commands if this repository is more than a few months old.

| Component | Version | How to check |
|---|---|---|
| Distribution | Ubuntu 24.04.5 LTS | `lsb_release -d` |
| Kernel | 7.0.14-orbstack, aarch64 | `uname -r` |
| Go | 1.27.1 | `go version` |
| Image | `ubuntu:24.04`, layer `90812e242c93` | `make rootfs`; `head -1 rootfs/ubuntu/etc/os-release` |
| strace | 6.8 | `strace -V` |
| uidmap (shadow) | 1:4.13+dfsg1-4ubuntu3.2 | `dpkg -s uidmap` |

A stock Ubuntu 24.04 image, which is what Multipass launches, ships kernel
6.8. Everything in this repository needs 5.7 or later, for
`CLONE_INTO_CGROUP`. The pre-flight checks.

## The kernel, and two things OrbStack does differently

OrbStack runs its own kernel rather than Ubuntu's. Two consequences are
visible from the demos.

The `kernel.apparmor_restrict_unprivileged_userns` sysctl does not exist
on it, so the rootless path works with no configuration and the
pre-flight prints "not on this kernel" for that line. On stock Ubuntu the
sysctl exists, ships as `1`, refuses a user namespace to any binary
without an AppArmor profile, and the cloud-init file sets it to `0`. Say
this on stage if anyone asks why their laptop refused the rootless demo:
the rest of the talk, everything with `sudo`, is unaffected.

The shell's cgroup is `/.lxc` rather than a systemd session scope, so the
rootless beat prints "no cgroup for this container" and carries on. On a
systemd box the message is the same for a different reason: the session
scope belongs to root. Either way the fix is a delegated scope, which
`systemd-run --user --scope -p Delegate=yes` provides, and which is not
demonstrated because it is not what the talk is about.

## Beat 7, counted

`./scripts/demo.sh 7` runs one container under `strace -f` and tallies the
calls. On this box, for `mycontainer run -memory 64m -pids 20 ubuntu
/bin/true`:

```
     31 prctl
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
`umount2` of the old root, twenty-seven `prctl(PR_CAPBSET_DROP)` plus a
handful the Go runtime makes, and three `execve`: the runtime, its child,
and the command. The plain `clone` calls are the Go runtime starting
threads in both processes; their number varies from run to run. The `clone3` line itself:

```
clone3({flags=CLONE_VM|CLONE_PIDFD|CLONE_VFORK|CLONE_NEWNS|CLONE_NEWCGROUP|CLONE_NEWUTS|CLONE_NEWIPC|CLONE_NEWPID|CLONE_NEWNET|CLONE_INTO_CGROUP, ..., cgroup=6}
```

That is the whole container, in one syscall, before the child has run an
instruction.

## The rootfs on virtiofs

The repository is shared from the Mac, so `rootfs/ubuntu` sits on a
virtiofs mount and `mount` inside a container lists its root as `mac on /
type virtiofs`. It works, and it is slower than a local disk. For the
talk, either accept the odd first line of the mount listing or clone the
repository somewhere local in the VM and pull the image there.
