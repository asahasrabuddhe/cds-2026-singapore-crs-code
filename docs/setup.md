# Setup

Everything in this repository runs on a Linux kernel, because namespaces,
cgroups, pivot_root and capabilities are Linux kernel features. On Windows
and macOS that means a virtual machine, and on Linux it means either your
own machine or a VM if you would rather not run a container runtime you
built an hour ago as root on your laptop.

One file, `provision/user-data.yaml`, provisions every VM below. It
installs Go 1.27, strace and the uidmap helpers, and writes the
two sysctls the rootless path needs. Whichever route you take, the box you
end up with is the same box.

When you are done, run the pre-flight inside it:

```bash
./scripts/check.sh
```

It prints one line per requirement, OK or FAIL, and ends with `ready` or
`not ready`. Every FAIL says what to do about it. If it says ready, you are
ready, and nothing else on this page matters.

## Route A: Multipass, on Windows, macOS or Linux

[Multipass](https://canonical.com/multipass) is Canonical's VM tool. It
installs the same way on all three operating systems, takes a cloud-init
file, and gives you an Ubuntu 24.04 machine in about two minutes. This is
the route the workshop assumes.

Install it:

- Windows: `winget install Canonical.Multipass`, or the installer from the
  site. It uses Hyper-V, which needs Windows 10 Pro or later, or
  VirtualBox on Home editions.
- macOS: `brew install --cask multipass`, or the installer from the site.
- Linux: `sudo snap install multipass`.

Then, from a clone of this repository:

```bash
multipass launch 24.04 --name crs --cpus 2 --memory 4G --disk 20G \
  --cloud-init provision/user-data.yaml
multipass shell crs
```

Inside the VM, get the code and check the box:

```bash
git clone https://github.com/asahasrabuddhe/cds-2026-singapore-crs-code.git ~/crs
cd ~/crs
./scripts/check.sh
```

Provisioning runs in the background after the VM boots. If `check.sh`
says Go is not on PATH, it has not finished yet; `cat /etc/crs-provisioned`
appears when it has, and `sudo cloud-init status --long` says what it is
doing.

Cloning inside the VM is the path of least surprise. If you would rather
edit on the host, mount the checkout instead:

```bash
multipass mount . crs:/home/ubuntu/crs
```

On Windows that needs `multipass set local.privileged-mounts=true` first,
and the mount is slower than a local disk. `scripts/vm.sh` wraps all of
this: `./scripts/vm.sh up` creates and mounts, `./scripts/vm.sh shell`
lands you in the repository.

## Route B: Linux, on the machine itself

Ubuntu 24.04 or Debian 13, with cgroup v2 (the default on both). Run the
steps the cloud-init file would have run:

```bash
sudo apt-get install -y uidmap strace make
curl -fsSL https://go.dev/dl/go1.27.1.linux-$(dpkg --print-architecture).tar.gz | sudo tar -C /usr/local -xzf -
export PATH=$PATH:/usr/local/go/bin

# The rootless path only. Ubuntu 23.10+ refuses user namespaces to any
# binary without an AppArmor profile; Debian has an older on/off switch.
sudo tee /etc/sysctl.d/99-crs.conf <<'EOF'
kernel.apparmor_restrict_unprivileged_userns = 0
kernel.unprivileged_userns_clone = 1
EOF
sudo sysctl -e --system
```

Then `./scripts/check.sh`. Other distributions work if they are on cgroup
v2 and allow unprivileged user namespaces; the pre-flight will tell you.

## Route C: WSL2, on Windows

WSL2 runs a real Linux kernel, so the runtime works there, with two
settings. First, systemd, so that cgroup v2 is mounted where the runtime
looks for it. In `/etc/wsl.conf` inside the distribution:

```ini
[boot]
systemd=true
```

Second, the kernel command line, in `%USERPROFILE%\.wslconfig` on the
Windows side:

```ini
[wsl2]
kernelCommandLine = cgroup_no_v1=all
```

Then `wsl --shutdown`, start Ubuntu 24.04 again, and follow Route B inside
it. The pre-flight checks the cgroup mount specifically, because this is
the setting that most often is not what you expect. If it reports cgroup
v1, use Route A instead of debugging WSL on the morning of a workshop.

This route is documented from Microsoft's WSL documentation and was not
exercised while writing this repository. Routes A, B and D were.

## Route D: Docker Desktop, as a fallback

A privileged container is a Linux environment with a kernel underneath
it, and the runtime runs inside one. It is a container inside a
container, so it proves less than the other routes, but it needs nothing
you do not already have if Docker Desktop is installed.

```bash
docker run --rm -it --privileged --cgroupns=private \
  -v "$PWD":/crs -w /crs golang:1.27 bash
apt-get update && apt-get install -y procps
make build rootfs
./mycontainer run ubuntu /bin/bash
```

`--cgroupns=private` gives the container its own cgroup root, which is
what lets the runtime create cgroups under it. The rootless path does not
work here, because the container's filesystem belongs to root and the
rootfs has to be writable by whoever runs the container. Everything with
root does.

## The presenter's box

The talk is rehearsed and delivered from an [OrbStack](https://orbstack.dev)
machine, because it boots in seconds and shares the Mac's filesystem at
the same path. Same cloud-init file:

```bash
make vm          # orb create -c provision/user-data.yaml ubuntu:noble crs-dev
make vm-shell
```

One thing to know about OrbStack: it runs its own kernel, without the
`apparmor_restrict_unprivileged_userns` sysctl. That is convenient, since
the rootless path just works, and it is why the pre-flight prints "not on
this kernel" for that line rather than a value. On a stock Ubuntu box the
sysctl exists and the cloud-init file turns it off.

## What the pre-flight checks, and why

| Line | Why it matters |
|---|---|
| Kernel 5.7 or later | `CLONE_INTO_CGROUP`, which puts the child in its cgroup at birth, arrived in 5.7. Ubuntu 24.04 ships 6.8. |
| cgroup v2 at `/sys/fs/cgroup` | The runtime writes `memory.max`, `cpu.max` and `pids.max`. cgroup v1 spells all of that differently and is not supported. |
| `cpu memory pids` controllers | What the three limits need. |
| `apparmor_restrict_unprivileged_userns = 0` | Ubuntu's allowlist for user namespaces. At its shipped value of 1, the rootless path is refused. Root is unaffected. |
| `unshare --user` works | The direct test of the line above. |
| Go 1.27 | What `go.mod` asks for. |
| `rootfs/ubuntu` | The image every demo runs. `make rootfs` pulls `ubuntu:24.04` with `img2rootfs`, once, while there is network. |
| sudo without a password | The demo script never prompts. Typing along by hand, a prompt is fine. |
| A container runs, as root and rootless | The whole thing, end to end. |
