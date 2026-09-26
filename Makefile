.PHONY: build check fmt vet test test-root lint rootfs clean vm vm-shell vm-down

# The binaries land at the repo root because that is what the demo script
# and the slides type, and it is shortest to say on stage.
build:
	go build -o mycontainer ./cmd/mycontainer
	go build -o img2rootfs ./cmd/img2rootfs

# Everything CI runs, and everything to run before committing.
check: fmt vet test lint

fmt:
	@out=$$(gofmt -l . ); \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	@echo "gofmt: clean"

vet:
	go vet ./...
	GOOS=linux go vet ./...

# The kernel-facing code is build-tagged linux; everything it decides is in
# packages with no tag, so this passes on a Mac. The Linux-only tests run
# inside the VM with `make test` there, or with test-root for the ones that
# need a real namespace.
test:
	go test ./...

# The end-to-end tests: build the binary, build a busybox rootfs, run real
# containers. Root, because that is what the runtime is written for; the
# rootless path is one of the cases.
test-root: build rootfs
	go test -tags integration -exec sudo ./cmd/... ./internal/container/...

lint:
	shellcheck scripts/*.sh

rootfs:
	./scripts/rootfs.sh rootfs/busybox

clean:
	rm -f mycontainer img2rootfs
	rm -rf rootfs

# ---------------------------------------------------------------- the VM ---
# One cloud-init file, provision/user-data.yaml, for every VM. These
# targets drive OrbStack, which is what the talk is rehearsed on. Multipass
# and Lima take the same file; see docs/setup.md.

VM ?= crs-dev

vm:
	./scripts/vm.sh up $(VM)

vm-shell:
	./scripts/vm.sh shell $(VM)

vm-down:
	./scripts/vm.sh down $(VM)
