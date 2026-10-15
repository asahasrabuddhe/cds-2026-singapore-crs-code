#!/usr/bin/env bash
#
# check.sh, the pre-flight. Run it inside the Linux box before the talk.
# Every line is OK or FAIL, and the last line says whether you are ready.
#
#   ./scripts/check.sh
#
# It checks the box, pulls the image if it is missing, and runs one
# container with the finished runtime.

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

# CLONE_INTO_CGROUP, which step 3 uses, arrived in 5.7.
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
    fail "controllers missing:$missing" "the kernel has no$missing controller."
  fi
else
  fail "cgroup v2 is not mounted at /sys/fs/cgroup" "this box is on cgroup v1 or hybrid; see docs/setup.md."
fi

# A runtime that was SIGKILLed leaves its cgroup behind. Sweep them.
if stale="$(ls -d /sys/fs/cgroup/mycontainer/*/ 2>/dev/null)" && [[ -n "$stale" ]]; then
  n=0
  for d in $stale; do sudo -n rmdir "$d" 2>/dev/null && n=$((n + 1)); done
  note "swept $n stale cgroup(s) from an earlier killed run"
fi

# --------------------------------------------- unprivileged user namespaces ---
# The rootless path creates a user namespace and runs without sudo.

restrict=/proc/sys/kernel/apparmor_restrict_unprivileged_userns
if [[ -r "$restrict" ]]; then
  if [[ "$(cat "$restrict")" == "0" ]]; then
    ok "kernel.apparmor_restrict_unprivileged_userns = 0"
  else
    fail "kernel.apparmor_restrict_unprivileged_userns = $(cat "$restrict")" \
      "Ubuntu ships it as 1, which refuses user namespaces. provision/user-data.yaml sets it to 0."
  fi
else
  note "kernel.apparmor_restrict_unprivileged_userns: not on this kernel"
fi

if unshare --user --map-root-user true 2>/dev/null; then
  ok "an ordinary user can create a user namespace"
else
  fail "unshare --user is refused" "the rootless path will not work; everything with sudo still does."
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

for t in ipcmk ipcs; do
  command -v "$t" >/dev/null || fail "$t is not installed" "the IPC demo uses it on the host: sudo apt-get install util-linux"
done

if sudo -n true 2>/dev/null; then
  ok "sudo without a password"
else
  fail "sudo asks for a password" "fine for typing along; a prompt mid-demo is not."
fi

# ---------------------------------------------------------------- the image ---

if [[ -x rootfs/ubuntu/bin/bash ]]; then
  ok "rootfs/ubuntu is there"
elif make -s rootfs >/dev/null 2>&1 && [[ -x rootfs/ubuntu/bin/bash ]]; then
  ok "rootfs/ubuntu pulled"
else
  fail "could not pull rootfs/ubuntu" "make rootfs, with network"
fi

if [[ -d rootfs/ubuntu && "$(stat -c %u rootfs/ubuntu)" != "$(id -u)" ]]; then
  fail "rootfs/ubuntu belongs to uid $(stat -c %u rootfs/ubuntu), not you" \
    "pull it as yourself, not with sudo: rm -rf rootfs && make rootfs"
fi

# ---------------------------------------------------------------- the code ---

if [[ -d cmd/mycontainer ]]; then
  if go build -o mycontainer ./cmd/mycontainer 2>/tmp/crs-build.log; then
    ok "mycontainer builds"
    if out="$(sudo -n ./mycontainer run ubuntu /bin/true 2>&1)"; then
      ok "a container runs"
    else
      fail "the smoke-test container did not run" "$out"
    fi
  else
    fail "mycontainer does not build" "$(head -3 /tmp/crs-build.log)"
  fi
else
  note "no runtime here"
fi

echo
if [[ "$fails" -eq 0 ]]; then
  echo "${GREEN}ready${RESET}"
else
  echo "${RED}not ready: $fails problem(s)${RESET}"
  exit 1
fi
