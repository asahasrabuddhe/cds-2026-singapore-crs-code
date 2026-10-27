# Setup

Everything in this repository runs on a Linux kernel, because namespaces,
cgroups, pivot_root and capabilities are Linux kernel features. So
everyone, on Windows, macOS or Linux, works in the same Ubuntu 24.04 VM,
made the same way with [Multipass](https://canonical.com/multipass). One
tool and one procedure for the whole room, and a container runtime you
built an hour ago runs as root in a VM rather than on your laptop.

The code lives on your machine, in a folder you edit with your own
editor. The VM sees the same folder, shared with `multipass mount`, and
builds and runs it on its Linux kernel. Edit on the left, run on the
right.

One file, `provision/user-data.yaml`, provisions the VM. It installs Go
1.27, strace and the uidmap helpers, and writes the sysctl the rootless
path needs.

Do this before the day. Forty-five minutes has no room for a VM install,
and the Ubuntu image is a download of about 600 MB.

Every command below runs the same in PowerShell, Terminal and a Linux
shell.

## 1. Install Multipass and git

- Windows: `winget install Canonical.Multipass` and `winget install
  Git.Git`. Multipass uses Hyper-V, which needs Windows 10 or 11 Pro,
  Enterprise or Education; on Windows Home, pick VirtualBox in the
  installer. Windows ships with shared folders turned off, so turn them
  on once: `multipass set local.privileged-mounts=true`.
- macOS: `brew install --cask multipass`. git comes with the Xcode command
  line tools, `xcode-select --install`.
- Linux: `sudo snap install multipass`, and git from your package manager.

## 2. Make the VM

```bash
multipass launch 24.04 --name crs --cpus 2 --memory 4G --disk 20G --cloud-init https://raw.githubusercontent.com/asahasrabuddhe/cds-2026-singapore-crs-code/main/provision/user-data.yaml
```

The VM boots in a minute or two, then provisions itself in the
background: packages, then Go. `multipass exec crs -- cat /etc/crs-provisioned`
prints the Go version when it is done.

## 3. Get the code, and share it with the VM

On your machine, in the folder where you keep code:

```bash
git clone https://github.com/asahasrabuddhe/cds-2026-singapore-crs-code.git crs
multipass mount crs crs:/home/ubuntu/crs
```

The folder `crs` on your machine is now `~/crs` in the VM, both ways,
and stays shared across restarts. If you cloned inside the VM before,
the mount sits on top of that copy, and there is nothing to clean up.

## 4. Check the box

```bash
multipass shell crs
cd ~/crs && ./scripts/check.sh
```

The pre-flight prints one line per requirement, OK or FAIL, and ends with
`ready` or `not ready`. Every FAIL says what to do about it. It also
pulls the image every container runs, `ubuntu:26.04`, into
`/var/lib/mycontainer/rootfs/ubuntu`, so run it while you have network.
If it says `ready`, you are ready.

## 5. Open it in your editor

Open the `crs` folder on your machine. The runtime is Linux only and your
editor is not running on Linux, so tell its Go tooling to read the code
as Linux, or every `syscall.CLONE_NEWPID` and every `_linux.go` file
shows up as an error.

- **VS Code or Cursor.** Install the Go extension (`golang.go`). The
  repository's `.vscode/settings.json` already sets `GOOS` to `linux`.
  The extension needs Go on your machine too, from
  [go.dev/dl](https://go.dev/dl); when it says `gopls` is missing, click
  Install. `F12` on a call should then jump to its definition.
- **GoLand.** Settings, Go, Build Tags, and set OS to `linux`.
- **Anything else.** Set `GOOS=linux` for `gopls`, or edit without it:
  the compiler that matters runs in the VM.

## In the workshop

Edit on your machine. Keep one terminal in the VM, `multipass shell crs`
then `cd ~/crs`, for `make build`, `go test` and `sudo ./mycontainer`.

You start on `main`, which is the runtime with five function bodies left
for you to write. The finished runtime is on the `final` branch. It is
there for when you are stuck, and it is the last resort.

Afterwards, `multipass delete --purge crs` removes the VM. Your folder
stays on your machine.

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

**`check.sh` cannot write to the repository, or `ls` in `~/crs` says
permission denied.** On macOS, Multipass cannot share a folder that macOS
keeps private, such as one under `/tmp`. Clone into your home directory.
On Windows, run `multipass set local.privileged-mounts=true` and mount
again.

**Scripts fail with `$'\r': command not found`.** git on Windows turned
the line endings into CRLF. The repository's `.gitattributes` prevents
this for a fresh clone; for an old one, `git rm --cached -r . && git reset
--hard`.

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
| `/var/lib/mycontainer/rootfs/ubuntu` | The image every demo runs. `make rootfs` pulls `ubuntu:26.04` with `img2rootfs`, once, while there is network. It lives on the VM's disk, because an image unpacked in a shared folder loses its symlinks and owners. |
| sudo without a password | The demo script never prompts. Typing along by hand, a prompt is fine. |
| A container runs | The runtime builds and starts a process as root. On `main` that is all it can do yet. |
