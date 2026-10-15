.PHONY: build test rootfs check

# The workshop's starting point. Same layout as the finished runtime two
# directories up, with the function bodies you write today replaced by
# TODO comments. The tests are the finished ones: make them pass.
#
#   go test ./internal/namespace/     step 1
#   go test ./internal/rootfs/        step 2
#   go test ./internal/cgroup/        step 3
#   go test ./internal/caps/          step 4
#   go test ./internal/initproc/      step 5, Linux only
#
# workshop/stub.py rebuilds this directory from the finished runtime.

build:
	go build -o mycontainer ./cmd/mycontainer

test:
	go test ./...

# The image lives once, in the repository's own rootfs/, pulled by the
# pre-flight. This links it here, so `mycontainer run ubuntu` finds it.
rootfs:
	@test -d ../../rootfs/ubuntu || $(MAKE) -C ../.. rootfs
	ln -sfn ../../rootfs rootfs

check:
	go vet ./...
	GOOS=linux go vet ./...
