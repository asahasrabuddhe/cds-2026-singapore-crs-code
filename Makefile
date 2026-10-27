.PHONY: build check fmt vet test test-root lint rootfs rootfs-all clean vm vm-shell vm-down

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

# GOOS=linux, so this also works from the host side of a shared folder.
vet:
	GOOS=linux go vet ./...
	GOOS=linux go vet -tags integration ./...

# The unit tests, inside the VM. No root needed.
test:
	go test ./...

# Real containers, as root, inside the VM. The image is pulled as you, not
# as root, so the rootless tests can use it too.
test-root: build rootfs
	go test -tags integration -exec sudo ./...

lint:
	shellcheck scripts/*.sh

# An image is what `mycontainer run <image>` runs. The 2025 version
# exported one through Docker; img2rootfs pulls it from the registry with
# the standard library and needs no Docker at all.
#
# Images live on the VM's own disk, in IMAGES, not in the repository: the
# repository may be a folder shared from the host, and an image unpacked
# there loses its symlinks and owners. The directory belongs to you, not
# root, so the rootless path can use what you pulled. `make rootfs/alpine`
# pulls one more, `make rootfs-all` pulls all seven.
IMAGES = /var/lib/mycontainer/rootfs

IMAGE_ubuntu   = ubuntu:26.04
IMAGE_debian13 = debian:13-slim
IMAGE_al2023   = amazonlinux:2023
IMAGE_rocky9   = rockylinux:9
IMAGE_alpine   = alpine:3.22
IMAGE_opensuse = opensuse/leap:15.6
IMAGE_void     = ghcr.io/void-linux/void-glibc:latest

ROOTFS_ALL = ubuntu debian13 al2023 rocky9 alpine opensuse void

rootfs: rootfs/ubuntu

rootfs-all: $(addprefix rootfs/,$(ROOTFS_ALL))

rootfs/%: | $(IMAGES)
	@test -d $(IMAGES)/$* || { go build -o img2rootfs ./cmd/img2rootfs && ./img2rootfs -o $(IMAGES)/$* $(IMAGE_$*); }

$(IMAGES):
	sudo install -d -o $$(id -u) -g $$(id -g) $@

clean:
	rm -f mycontainer img2rootfs

# ---------------------------------------------------------------- the VM ---
# The same Multipass VM as everyone else's, from provision/user-data.yaml;
# see docs/setup.md.

VM ?= crs

vm:
	./scripts/vm.sh up $(VM)

vm-shell:
	./scripts/vm.sh shell $(VM)

vm-down:
	./scripts/vm.sh down $(VM)
