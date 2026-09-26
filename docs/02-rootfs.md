# 02. Rootfs: where a container lives

## What it shows

A directory becomes the container's `/`, and the host's filesystem stops
existing from the container's point of view. `chroot` also changes what
`/` means, but it leaves the old root reachable to anything that can open
a file descriptor outside it, which root can. `pivot_root(2)` swaps the
mount that is the root of the mount namespace, and once the old one is
detached there is nothing to escape to.

The sequence in `internal/rootfs` is the order that works, and every step
is there because the one after it fails without it:

1. `mount("", "/", MS_REC|MS_PRIVATE)`. systemd makes every mount shared
   by default, so without this the unmount at the end would propagate to
   the host and unmount the host's root.
2. `mount(rootfs, rootfs, MS_BIND|MS_REC)`. `pivot_root` needs its new
   root to be a mount point, and a directory is not one until it is bound
   to itself.
3. `mount("proc", rootfs/proc, "proc")`, from inside the PID namespace, so
   `ps` in the container sees the container.
4. A tmpfs on `rootfs/dev`, then `null`, `zero`, `full`, `random`,
   `urandom` and `tty` bind-mounted from the host. `mknod` would need
   `CAP_MKNOD` in the initial user namespace, which the rootless path does
   not have; a bind mount works for both.
5. `sysfs` on `rootfs/sys`, read only, and `cgroup2` on
   `rootfs/sys/fs/cgroup`, so the container can read its own limits. Both
   are allowed to fail, because a user namespace may only mount them if it
   owns the network and cgroup namespaces respectively.
6. `chdir(rootfs)`, `pivot_root(".", ".pivot_root")`, `chdir("/")`.
7. `umount2(".pivot_root", MNT_DETACH)`, then delete the directory.

The tests in `internal/rootfs` check that exact sequence against a
recorder, so a reordering is a failing test rather than a mystery EINVAL
on stage.

## How to run it

```console
$ ./scripts/demo.sh 2
```

By hand:

```console
$ sudo ./mycontainer run rootfs/busybox /bin/sh -c 'ls /; cat /etc/os-release; ls -d /.pivot_root; mount'
```

## Expected output

`ls /` lists `bin dev etc proc root sbin sys tmp usr`, which is the busybox
tree and nothing else. `cat /etc/os-release` fails with "No such file or
directory", because that file is the host's and the host is gone. So does
`ls -d /.pivot_root`. `mount` lists only what the runtime mounted:

```
mac on / type virtiofs (rw,relatime)
proc on /proc type proc (rw,nosuid,nodev,noexec,relatime)
tmpfs on /dev type tmpfs (rw,nosuid,size=65536k,mode=755)
none on /dev/null type tmpfs (rw,relatime,size=492k,mode=755)
...
sysfs on /sys type sysfs (ro,nosuid,nodev,noexec,relatime)
cgroup2 on /sys/fs/cgroup type cgroup2 (rw,nosuid,nodev,noexec,relatime,nsdelegate)
```

The first line says `virtiofs` on the demo box because the repository is
shared from the Mac; on a rootfs under `/home` it reads `ext4`.

## What it proves

The filesystem a container sees is a mount table, built from inside a
mount namespace, with the host's root swapped out and detached. Nothing
about the directory changed. Only which mount is `/`, and for whom.
