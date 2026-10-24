# Setup

Everything in this repository runs on a Linux kernel, because namespaces,
cgroups, pivot_root and capabilities are Linux kernel features. So
everyone, on Windows, macOS or Linux, works in the same Ubuntu 24.04 VM,
made the same way with [Multipass](https://canonical.com/multipass). One
tool and one procedure for the whole room, and a container runtime you
built an hour ago runs as root in a VM rather than on your laptop.

One file, `provision/user-data.yaml`, provisions the VM. It installs Go
1.27, strace and the uidmap helpers, and writes the sysctl the rootless
path needs.

Do this before the day. Forty-five minutes has no room for a VM install,
and the Ubuntu image is a download of about 600 MB.

## 1. Install Multipass

- Windows: `winget install Canonical.Multipass`, or the installer from the
  site. It uses Hyper-V, which needs Windows 10 or 11 Pro, Enterprise or
  Education. On Windows Home, pick VirtualBox in the installer.
- macOS: `brew install --cask multipass`, or the installer from the site.
- Linux: `sudo snap install multipass`.

## 2. Make the VM

From any terminal on your machine. The same command on all three
operating systems:

```bash
multipass launch 24.04 --name crs --cpus 2 --memory 4G --disk 20G --cloud-init https://raw.githubusercontent.com/asahasrabuddhe/cds-2026-singapore-crs-code/main/provision/user-data.yaml
```

The VM boots in a minute or two, then provisions itself in the
background: packages, then Go. `multipass exec crs -- cat /etc/crs-provisioned`
prints the Go version when it is done.

## 3. Get the code, and check the box

```bash
multipass shell crs
git clone https://github.com/asahasrabuddhe/cds-2026-singapore-crs-code.git ~/crs
cd ~/crs && ./scripts/check.sh
```

The pre-flight prints one line per requirement, OK or FAIL, and ends with
`ready` or `not ready`. Every FAIL says what to do about it. It also
pulls the image every container runs, `ubuntu:26.04`, into
`rootfs/ubuntu`, so run it while you have network. If it says `ready`,
you are ready, and nothing else on this page matters.

## In the workshop

Everything happens inside the VM. Open two terminals and run
`multipass shell crs` in each: one to edit, one to build and test. `vim`
and `nano` are both there. The code lives on the VM's own disk, not
shared from your machine, so the runtime sees an ordinary Linux
filesystem.

Afterwards, `multipass delete --purge crs` removes the VM and everything
in it.

## When it does not work

**`check.sh` says Go is not on PATH.** Provisioning has not finished.
`sudo cloud-init status --long` inside the VM says what it is doing.
Wait for `/etc/crs-provisioned`, then log out and `multipass shell crs`
again.

**`multipass launch` times out.** The image download is slow. The VM
usually finishes anyway; `multipass list` shows it. If it does not,
`multipass delete --purge crs` and run the launch again.

**Windows says Hyper-V is not available.** Windows Home has no Hyper-V.
Reinstall Multipass and choose VirtualBox as the driver.

**Anything else on the day.** Pair with a neighbour and fix it after.

## What the pre-flight checks, and why

| Line | Why it matters |
|---|---|
| Kernel 5.7 or later | `CLONE_INTO_CGROUP`, which puts the child in its cgroup at birth, arrived in 5.7. Ubuntu 24.04 ships 6.8. |
| cgroup v2 at `/sys/fs/cgroup` | The runtime writes `memory.max`, `cpu.max` and `pids.max`. cgroup v1 spells all of that differently and is not supported. |
| `cpu memory pids` controllers | What the three limits need. |
| `apparmor_restrict_unprivileged_userns = 0` | Ubuntu's allowlist for user namespaces. At its shipped value of 1, the rootless path is refused. Root is unaffected. |
| `unshare --user` works | The direct test of the line above. |
| Go 1.27 | What `go.mod` asks for. |
| `rootfs/ubuntu` | The image every demo runs. `make rootfs` pulls `ubuntu:26.04` with `img2rootfs`, once, while there is network. |
| sudo without a password | The demo script never prompts. Typing along by hand, a prompt is fine. |
| A container runs, as root and rootless | The whole thing, end to end. |
