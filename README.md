# Build Your Own Container Runtime in Go

Companion code for **Build Your Own Container Runtime in Go**, the
forty-five-minute workshop at ContainerDays Singapore 2026.

We all run containers. Most of us could not build one. This repository is
the one you build in the room: a container runtime in Go, standard library
only, that isolates PID, mount, UTS, IPC, network and cgroup namespaces,
limits memory, CPU and processes through cgroup v2, pivots into its own
root filesystem, drops to Docker's fourteen default capabilities, and runs
a PID 1 that reaps orphans and forwards signals. A process wearing a
costume, one piece at a time, with `mycontainer run` at the end of it.

The slides live in a sibling repository,
[`cds-2026-singapore-crs-slides`](https://github.com/asahasrabuddhe/cds-2026-singapore-crs-slides).

## Before the workshop

You need a Linux box that passes the pre-flight. [`docs/setup.md`](docs/setup.md)
has one for Windows, macOS and Linux, all provisioned from the same
cloud-init file; Multipass is the route that works the same way on all
three. Do this before the day: forty-five minutes has no room for a VM
install, and the image is a download.

```bash
multipass launch 24.04 --name crs --cpus 2 --memory 4G --disk 20G \
  --cloud-init provision/user-data.yaml
multipass shell crs
git clone https://github.com/asahasrabuddhe/cds-2026-singapore-crs-code.git ~/crs
cd ~/crs && ./scripts/check.sh
```

When it prints `ready`, you are ready.

## In the workshop

[`docs/workshop.md`](docs/workshop.md) is the plan: five steps, one per
pillar, each with the file to edit, the test that tells you when you are
done, and the command that shows it working. You work in
`workshop/start/`, which is this runtime with the bodies of five functions
replaced by `TODO(step N)` comments and the finished tests left in place.

```bash
cd workshop/start
make build rootfs
go test ./...            # six packages fail on Linux. That is the syllabus.
```

The repository root is the finished runtime, the reference for when you
are stuck, and what `scripts/demo.sh` runs.

## The finished runtime

```bash
make build         # ./mycontainer and ./img2rootfs
make rootfs        # rootfs/busybox, from busybox-static, in a second
sudo ./mycontainer run rootfs/busybox /bin/sh
```

PID 1 is the runtime's init and the shell is its child, `ls /` is the busybox
tree, `mount` shows nothing from the host, and `mount -t tmpfs none /tmp`
is refused. Flags turn each piece on or off:

```bash
sudo ./mycontainer run -ns pid,uts rootfs/busybox /bin/sh -c 'ls /proc'   # no mount ns: /proc is the host's
sudo ./mycontainer run -memory 64m -cpus 0.5 -pids 20 rootfs/busybox /bin/sh
sudo ./mycontainer run -caps default,+CAP_SYS_ADMIN rootfs/busybox /bin/sh
sudo ./mycontainer run -init=false rootfs/busybox /bin/sleep 30       # sleep is PID 1, with everything that entails
./mycontainer run rootfs/busybox /bin/sh -c id                        # rootless: uid 0 inside, you outside
sudo ./mycontainer run -v rootfs/busybox /bin/true                    # narrate every step
./img2rootfs alpine:3.22                                              # a real image, no Docker needed
```

`scripts/demo.sh 1` to `7` are the demos, one per pillar, each independent.
Every beat has a doc:

| Beat | Doc |
|---|---|
| Namespaces | [`docs/01-namespaces.md`](docs/01-namespaces.md) |
| Rootfs | [`docs/02-rootfs.md`](docs/02-rootfs.md) |
| cgroups | [`docs/03-cgroups.md`](docs/03-cgroups.md) |
| Capabilities | [`docs/04-capabilities.md`](docs/04-capabilities.md) |
| Init | [`docs/05-init.md`](docs/05-init.md) |
| Environment and the strace tally | [`docs/environment.md`](docs/environment.md) |

## Layout

```
cmd/mycontainer/       the binary: run (parent) and child (PID 1 inside)
cmd/img2rootfs/        pull an image into a directory, standard library only
internal/namespace/    the seven namespaces, as clone flags          step 1
internal/rootfs/       mounts and pivot_root, in the order that works step 2
internal/cgroup/       a cgroup v2 leaf and its limit files          step 3
internal/caps/         capability names, the default fourteen, the drop  step 4
internal/initproc/     PID 1: reap orphans, forward signals          step 5
internal/spec/         what the parent tells the child
internal/cli/          flags to spec
internal/container/    the parent and the child, wired together (Linux only)
internal/image/        registry client and layer unpacking
workshop/start/        the same program with the five bodies removed
scripts/               demo.sh, check.sh, rootfs.sh, vm.sh
provision/             one cloud-init file for every VM
docs/                  setup, the workshop plan, one explainer per pillar
```

Every package under `internal/` except `container` compiles and tests on
macOS and Windows: the clone flags, mount flags and capability numbers are
defined in Go rather than taken from package `syscall`, the pivot sequence
runs against a recorder, and cgroup v2 is files, so a temp dir stands in
for `/sys/fs/cgroup`. That is what lets the workshop's tests run on your
laptop while the container itself needs the VM.

## Tests

```bash
make check          # gofmt, go vet (both build tags), go test, shellcheck
make test-root      # inside the VM: the end-to-end suite, as root
```

## Things worth knowing before you extend it

- **Capabilities are per thread.** The child locks itself to one OS thread
  before dropping the bounding set, or Go may fork the command from a
  thread that still holds everything.
- **`pids.max` counts the init's own threads.** A Go init needs a few, and
  it dies if it cannot create one. Keep `-pids` above about 16.
- **A SIGKILLed runtime leaks its cgroup.** The child dies with it
  (`PR_SET_PDEATHSIG`), but nothing removes the directory. `check.sh`
  sweeps them.
- **Rootless cgroups need delegation.** Under a plain login session the
  runtime says so and carries on without one. `systemd-run --user --scope
  -p Delegate=yes ./mycontainer run ...` gives it a cgroup it may write to.
- **The rootfs must be writable by whoever runs the container.**
- **There is no network.** A network namespace with a down loopback and
  nothing else. Wiring it up is a different talk.

## Licence

MIT. See [`LICENSE`](LICENSE).
