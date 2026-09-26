#!/usr/bin/env bash
#
# check.sh, the pre-flight. Run it inside the Linux box before the talk,
# and before the workshop if you are an attendee. Every line is OK or
# FAIL, and the last line says whether you are ready.
#
#   ./scripts/check.sh
#
# It does not fix anything. Each FAIL names the file in docs/ that does.

set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

readonly GREEN=$'\e[32m' RED=$'\e[31m' DIM=$'\e[2m' RESET=$'\e[0m'
fails=0

ok()   { printf '%s  OK  %s %s\n' "$GREEN" "$RESET" "$1"; }
fail() { printf '%s FAIL %s %s\n' "$RED" "$RESET" "$1"; printf '%s       %s%s\n' "$DIM" "$2" "$RESET"; fails=$((fails + 1)); }
note() { printf '%s       %s%s\n' "$DIM" "$1" "$RESET"; }

# ------------------------------------------------------------- the kernel ---

if [[ "$(uname -s)" == "Linux" ]]; then
  ok "Linux, kernel $(uname -r)"
else
  fail "this is $(uname -s), not Linux" "the runtime needs a Linux kernel. docs/setup.md has a VM for every OS."
  echo; echo "not ready"; exit 1
fi

# Everything here is kernel 5.7 or later: CLONE_INTO_CGROUP arrived in 5.7.
kver="$(uname -r | cut -d. -f1,2)"
if [[ "$(printf '%s\n5.7\n' "$kver" | sort -V | head -1)" == "5.7" ]]; then
  ok "kernel $kver is 5.7 or later"
else
  fail "kernel $kver is older than 5.7" "CLONE_INTO_CGROUP needs 5.7. Ubuntu 24.04 ships 6.8."
fi

# ------------------------------------------------------------- cgroup v2 ---

if [[ "$(stat -fc %T /sys/fs/cgroup 2>/dev/null)" == "cgroup2fs" ]]; then
  ok "cgroup v2 mounted at /sys/fs/cgroup"
  controllers="$(cat /sys/fs/cgroup/cgroup.controllers)"
  missing=""
  for c in cpu memory pids; do
    [[ " $controllers " == *" $c "* ]] || missing="$missing $c"
  done
  if [[ -z "$missing" ]]; then
    ok "controllers available: $controllers"
  else
    fail "controllers missing:$missing" "the kernel has no$missing controller. Unusual; see docs/03-cgroups.md."
  fi
else
  fail "cgroup v2 is not mounted at /sys/fs/cgroup" "this box is on cgroup v1 or hybrid. docs/03-cgroups.md explains how to switch."
fi

# --------------------------------------------- unprivileged user namespaces ---
# Only the rootless path needs these, so they warn rather than fail.

restrict=/proc/sys/kernel/apparmor_restrict_unprivileged_userns
if [[ -r "$restrict" ]]; then
  if [[ "$(cat "$restrict")" == "0" ]]; then
    ok "kernel.apparmor_restrict_unprivileged_userns = 0"
  else
    fail "kernel.apparmor_restrict_unprivileged_userns = $(cat "$restrict")" \
      "Ubuntu ships it as 1, which refuses the rootless path. provision/user-data.yaml sets it to 0."
  fi
else
  note "kernel.apparmor_restrict_unprivileged_userns: not on this kernel"
fi

clone=/proc/sys/kernel/unprivileged_userns_clone
if [[ -r "$clone" ]] && [[ "$(cat "$clone")" != "1" ]]; then
  fail "kernel.unprivileged_userns_clone = $(cat "$clone")" "Debian's switch for the rootless path. provision/user-data.yaml sets it to 1."
fi

if unshare --user --map-root-user true 2>/dev/null; then
  ok "an ordinary user can create a user namespace"
else
  fail "unshare --user is refused" "the rootless demo will not work; everything with sudo still does."
fi

# ------------------------------------------------------------------- tools ---

if command -v go >/dev/null; then
  gov="$(go version | awk '{print $3}' | sed 's/^go//')"
  if [[ "$(printf '%s\n1.27\n' "$gov" | sort -V | head -1)" == "1.27" ]]; then
    ok "go $gov"
  else
    fail "go $gov is older than 1.27" "go.mod asks for 1.27. https://go.dev/dl"
  fi
else
  fail "go is not on PATH" "log out and back in after provisioning, or: export PATH=\$PATH:/usr/local/go/bin"
fi

if command -v busybox >/dev/null; then
  ok "busybox $(busybox 2>&1 | head -1 | awk '{print $2}')"
else
  fail "busybox is not installed" "sudo apt-get install busybox-static"
fi

if sudo -n true 2>/dev/null; then
  ok "sudo without a password"
else
  fail "sudo asks for a password" "fine for typing along; the demo script assumes it does not."
fi

# A runtime that was SIGKILLed leaves its cgroup behind, and the cgroups
# beat of the demo says "gone once the container is". Sweep them.
if stale="$(ls -d /sys/fs/cgroup/mycontainer/*/ 2>/dev/null)" && [[ -n "$stale" ]]; then
  n=0
  for d in $stale; do sudo -n rmdir "$d" 2>/dev/null && n=$((n + 1)); done
  note "swept $n stale cgroup(s) from an earlier killed run"
fi

# ---------------------------------------------------------------- the code ---

if go build -o mycontainer ./cmd/mycontainer 2>/tmp/crs-build.log; then
  ok "mycontainer builds"
else
  fail "mycontainer does not build" "$(head -3 /tmp/crs-build.log)"
fi

if [[ -x rootfs/busybox/bin/busybox ]] || ./scripts/rootfs.sh rootfs/busybox >/dev/null 2>&1; then
  ok "rootfs/busybox is ready"
else
  fail "could not build rootfs/busybox" "./scripts/rootfs.sh"
fi

if [[ -x mycontainer && -x rootfs/busybox/bin/busybox ]]; then
  if out="$(sudo -n ./mycontainer run -pids 8 rootfs/busybox /bin/sh -c 'hostname; cat /sys/fs/cgroup/pids.max' 2>&1)" \
     && [[ "$out" == *"8"* ]]; then
    ok "a container runs as root, with a cgroup limit visible inside"
  else
    fail "the smoke test container did not run as root" "$out"
  fi
  if out="$(./mycontainer run rootfs/busybox /bin/sh -c 'id -u' 2>/dev/null)" && [[ "$out" == "0" ]]; then
    ok "a container runs rootless, and is root inside"
  else
    fail "the rootless smoke test did not run" "see the user namespace lines above."
  fi
fi

echo
if [[ "$fails" -eq 0 ]]; then
  echo "${GREEN}ready${RESET}"
else
  echo "${RED}not ready: $fails problem(s)${RESET}"
  exit 1
fi
