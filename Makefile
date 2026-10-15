.PHONY: build check fmt vet test test-root lint rootfs clean vm vm-shell vm-down

# The binaries land at the repo root, where the demo types them.
build:
	go build -o img2rootfs ./cmd/img2rootfs
	@if [ -d cmd/mycontainer ]; then go build -o mycontainer ./cmd/mycontainer; fi

# Everything CI runs, and everything to run before committing.
check: fmt vet test lint

fmt:
	@out=$$(gofmt -l . ); \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	@echo "gofmt: clean"

vet:
	go vet ./...
	GOOS=linux go vet ./...
	GOOS=linux go vet -tags integration ./...

# The pure packages, on any OS.
test:
	go test ./...

# Real containers, as root, inside the VM. The rootfs is pulled as you, not
# as root, so the rootless tags can write to it too.
test-root: build rootfs
	go test -tags integration -exec sudo ./...

lint:
	shellcheck scripts/*.sh

# rootfs/<image> is what `mycontainer run <image>` runs. The 2025 version
# exported an image through Docker; img2rootfs pulls it from the registry
# with the standard library and needs no Docker at all.
rootfs: rootfs/ubuntu

rootfs/ubuntu:
	go build -o img2rootfs ./cmd/img2rootfs
	./img2rootfs -o rootfs/ubuntu ubuntu:24.04

clean:
	rm -f mycontainer img2rootfs
	rm -rf rootfs

# ---------------------------------------------------------------- the VM ---
# One cloud-init file, provision/user-data.yaml, for every VM. These
# targets drive OrbStack or Multipass; see docs/setup.md.

VM ?= crs-dev

vm:
	./scripts/vm.sh up $(VM)

vm-shell:
	./scripts/vm.sh shell $(VM)

vm-down:
	./scripts/vm.sh down $(VM)
