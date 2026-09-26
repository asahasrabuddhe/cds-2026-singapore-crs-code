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

build:
	go build -o mycontainer ./cmd/mycontainer

test:
	go test ./...

rootfs:
	../../scripts/rootfs.sh rootfs/busybox

check:
	go vet ./...
	GOOS=linux go vet ./...
