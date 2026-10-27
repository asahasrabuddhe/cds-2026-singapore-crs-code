#!/usr/bin/env bash
#
# vm.sh, the Linux box the runtime runs in.
#
# The same Multipass VM docs/setup.md has every attendee make, from
# provision/user-data.yaml, with this checkout shared into it at ~/crs,
# the way every attendee shares theirs. Edit here, run there.
#
#   ./scripts/vm.sh up [name]        create, provision and share this checkout
#   ./scripts/vm.sh shell [name]     a login shell, in ~/crs
#   ./scripts/vm.sh status
#   ./scripts/vm.sh down [name]      delete

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

readonly USER_DATA="provision/user-data.yaml"
readonly ACTION="${1:-status}"
readonly NAME="${2:-crs}"

if ! command -v multipass >/dev/null; then
  echo "multipass is not installed. See docs/setup.md." >&2
  exit 1
fi

# Provisioning runs in the background after the VM is up. Wait for the
# marker the last runcmd writes, so `vm.sh up && vm.sh shell` lands on a
# box with Go on it.
wait_provisioned() {
  printf 'waiting for provisioning'
  for _ in $(seq 1 120); do
    if multipass exec "$NAME" -- test -f /etc/crs-provisioned 2>/dev/null; then
      printf '\n'
      multipass exec "$NAME" -- cat /etc/crs-provisioned
      return 0
    fi
    printf '.'
    sleep 5
  done
  printf '\nprovisioning did not finish in ten minutes; check cloud-init in the VM:\n'
  printf '  multipass exec %s -- sudo cloud-init status --long\n' "$NAME"
  return 1
}

case "$ACTION" in
  up)
    if multipass info "$NAME" >/dev/null 2>&1; then
      echo "$NAME already exists"
    else
      multipass launch 24.04 --name "$NAME" --cpus 2 --memory 4G --disk 20G \
        --cloud-init "$USER_DATA"
    fi
    wait_provisioned
    if ! multipass info "$NAME" | grep -q '=> /home/ubuntu/crs$'; then
      multipass mount "$PWD" "$NAME:/home/ubuntu/crs"
    fi
    echo "then: ./scripts/vm.sh shell $NAME"
    ;;
  shell)  exec multipass exec "$NAME" -- bash -lc 'cd ~/crs && exec bash -l' ;;
  status) multipass list ;;
  down)   multipass delete --purge "$NAME" ;;
  *)      echo "usage: vm.sh {up|shell|status|down} [name]" >&2; exit 2 ;;
esac
