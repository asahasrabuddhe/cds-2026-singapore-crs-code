#!/usr/bin/env bash
#
# vm.sh, the Linux box the runtime runs in.
#
# One cloud-init file, provision/user-data.yaml, and whichever VM tool the
# machine has. OrbStack is what the talk is rehearsed on; Multipass is what
# the workshop instructions use, because it is the one that installs the
# same way on Windows, macOS and Linux. Both mount this repository into
# the VM, so `make build` inside sees the same files as your editor.
#
#   ./scripts/vm.sh up [name]        create and provision
#   ./scripts/vm.sh shell [name]     a login shell, in this repo
#   ./scripts/vm.sh status
#   ./scripts/vm.sh down [name]      delete
#
# VM_PROVIDER=orb or =multipass forces one when both are installed.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

readonly USER_DATA="provision/user-data.yaml"
readonly REPO="$PWD"
readonly ACTION="${1:-status}"
readonly NAME="${2:-crs-dev}"

provider() {
  if [[ -n "${VM_PROVIDER:-}" ]]; then
    echo "$VM_PROVIDER"
  elif command -v orb >/dev/null; then
    echo orb
  elif command -v multipass >/dev/null; then
    echo multipass
  else
    echo "neither orb nor multipass is installed. See docs/setup.md." >&2
    exit 1
  fi
}

# Provisioning runs in the background after the VM is up. Wait for the
# marker the last runcmd writes, so `vm.sh up && vm.sh shell` lands on a
# box with Go on it.
wait_provisioned() {
  local run="$1"
  printf 'waiting for provisioning'
  for _ in $(seq 1 120); do
    if $run test -f /etc/crs-provisioned 2>/dev/null; then
      printf '\n'
      $run cat /etc/crs-provisioned
      return 0
    fi
    printf '.'
    sleep 5
  done
  printf '\nprovisioning did not finish in ten minutes; check cloud-init in the VM:\n'
  printf '  sudo cloud-init status --long\n'
  return 1
}

case "$(provider)" in
  orb)
    case "$ACTION" in
      up)
        if orb list 2>/dev/null | grep -q "^$NAME "; then
          echo "$NAME already exists"
        else
          orb create -c "$USER_DATA" ubuntu:noble "$NAME"
        fi
        wait_provisioned "orb -m $NAME"
        echo "then: ./scripts/vm.sh shell $NAME"
        ;;
      # OrbStack mounts /Users at the same path inside the machine, so the
      # repository's path is valid on both sides.
      shell)  exec orb -m "$NAME" -w "$REPO" bash -l ;;
      status) orb list ;;
      down)   orb delete -f "$NAME" ;;
      *)      echo "usage: vm.sh {up|shell|status|down} [name]" >&2; exit 2 ;;
    esac
    ;;
  multipass)
    case "$ACTION" in
      up)
        if multipass info "$NAME" >/dev/null 2>&1; then
          echo "$NAME already exists"
        else
          multipass launch 24.04 --name "$NAME" --cpus 2 --memory 4G --disk 20G \
            --cloud-init "$USER_DATA"
        fi
        # Mounted at the same place every time, so the docs can say ~/crs.
        multipass mount "$REPO" "$NAME:/home/ubuntu/crs" 2>/dev/null || true
        wait_provisioned "multipass exec $NAME --"
        echo "then: ./scripts/vm.sh shell $NAME"
        ;;
      shell)  exec multipass exec "$NAME" -- bash -lc 'cd ~/crs && exec bash -l' ;;
      status) multipass list ;;
      down)   multipass delete --purge "$NAME" ;;
      *)      echo "usage: vm.sh {up|shell|status|down} [name]" >&2; exit 2 ;;
    esac
    ;;
esac
