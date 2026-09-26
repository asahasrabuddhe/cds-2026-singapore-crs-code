#!/usr/bin/env bash
#
# demo.sh, the demo driver for "Building a Container Runtime from Scratch".
#
# One beat per pillar, each independent and repeatable. Every command the
# audience sees is echoed before it runs, so the terminal reads as a
# transcript. Run from the repository root, inside the Linux box.
#
#   ./scripts/demo.sh check      # pre-flight, the morning of the talk
#   ./scripts/demo.sh 1          # namespaces: what a container can see
#   ./scripts/demo.sh 2          # rootfs: where a container lives
#   ./scripts/demo.sh 3          # cgroups: what a container can use
#   ./scripts/demo.sh 4          # capabilities: what a container can do
#   ./scripts/demo.sh 5          # init: what PID 1 owes everybody else
#   ./scripts/demo.sh 6          # rootless: root in here, you out there
#   ./scripts/demo.sh 7          # strace: the syscalls, counted
#   ./scripts/demo.sh all        # rehearsal, with pauses
#
# Every beat except 6 runs as root, because that is what the runtime is
# written for. Beat 6 runs as you, and that is its point.

# shellcheck disable=SC2016  # the single-quoted $(...) are for the container's shell, not this one
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

readonly YELLOW=$'\e[33m' DIM=$'\e[2m' BOLD=$'\e[1m' RESET=$'\e[0m'
readonly ROOTFS="${ROOTFS:-rootfs/busybox}"
readonly BIN=./mycontainer

# ---------------------------------------------------------------- helpers ---

say()   { printf '\n%s▶ %s%s\n' "$YELLOW" "$1" "$RESET"; }
note()  { printf '%s  %s%s\n' "$DIM" "$1" "$RESET"; }
pause() { printf '%s  [enter]%s' "$DIM" "$RESET"; read -r _; }

banner() { printf '\n%s┌─ %s ─┐%s\n' "$BOLD" "$1" "$RESET"; }

# Echo a command, then run it. Output is indented so it reads as output.
run() {
  printf '\n%s$ %s%s\n' "$BOLD" "$*" "$RESET"
  "$@" 2>&1 | sed 's/^/  /'
}

# The runtime, as root. `sudo -n` never prompts: the VM grants it, and a
# prompt in the middle of a beat is worse than a failure.
ctr() { run sudo -n "$BIN" run "$@"; }

# A container that runs a shell script, which is most of them.
sh_in() { ctr "$@" "$ROOTFS" /bin/sh -c "$SCRIPT"; }

# The host PID of a container's PID 1, starting from a background job's
# PID. There may be a sudo and there is always the runtime in between, so
# walk down until a process reports itself as PID 1 of a PID namespace:
# the second field of NSpid in its status file.
find_init() {
  local pid="$1" child
  for _ in 1 2 3 4; do
    for child in $(pgrep -P "$pid"); do
      if [[ "$(awk '/^NSpid:/ {print $3}' "/proc/$child/status" 2>/dev/null)" == "1" ]]; then
        echo "$child"
        return 0
      fi
    done
    pid="$(pgrep -P "$pid" | head -1)"
    [[ -n "$pid" ]] || break
  done
  echo "could not find the container's PID 1 under $1" >&2
  return 1
}

need_build() {
  if [[ ! -x "$BIN" ]]; then
    echo "not built: make build" >&2
    exit 1
  fi
  if [[ ! -x "$ROOTFS/bin/busybox" ]]; then
    echo "no rootfs: make rootfs" >&2
    exit 1
  fi
}

# --------------------------------------------------- 1: namespaces --------
#
# Three runs of the same command with more namespaces each time. The first
# is the gotcha everybody hits: a PID namespace on its own changes nothing
# ps can see, because ps reads /proc and /proc is still the host's.

demo_1() {
  banner "1. NAMESPACES: what a container can see"

  say "The host"
  run hostname
  run sh -c 'ls -d /proc/[0-9]* | wc -l'

  say "A PID and UTS namespace, and nothing else"
  SCRIPT='echo "hostname: $(hostname)"; echo "my pid: $$"; echo "processes in /proc: $(ls -d /proc/[0-9]* | wc -l)"'
  sh_in -ns pid,uts -hostname box
  note "New hostname, and a small PID. But /proc still lists the host's processes."
  note "ps reads /proc, and /proc is the host's. A PID namespace is not enough."

  say "Add a mount namespace, and a fresh /proc with it"
  sh_in -ns pid,uts,mnt -hostname box
  note "A handful now: our init at PID 1, this shell, and the pipeline itself."

  say "Inside, in full: ps"
  SCRIPT='ps'
  sh_in
}

# -------------------------------------------------------- 2: rootfs -------

demo_2() {
  banner "2. ROOTFS: where a container lives"

  say "Same directory, seen from the host"
  run ls "$ROOTFS"

  say "Seen from inside, after pivot_root"
  SCRIPT='ls /; echo; echo "os-release: $(cat /etc/os-release 2>&1)"; echo "old root: $(ls -d /.pivot_root 2>&1)"'
  sh_in
  note "The host's /etc/os-release is gone. So is the directory the old root"
  note "was parked in during the pivot. There is no path back."

  say "What is mounted in there"
  SCRIPT='mount'
  sh_in
  note "Everything here was mounted by the runtime: the rootfs, /proc, /dev"
  note "and six device nodes bound from the host, /sys read-only, and the"
  note "container's own cgroup. Nothing else from the host."
}

# ------------------------------------------------------- 3: cgroups -------

demo_3() {
  banner "3. CGROUPS: what a container can use"

  say "Ask for limits, then ask the container what it got"
  SCRIPT='cat /proc/self/cgroup; echo "memory.max: $(cat /sys/fs/cgroup/memory.max)"; echo "cpu.max:    $(cat /sys/fs/cgroup/cpu.max)"; echo "pids.max:   $(cat /sys/fs/cgroup/pids.max)"'
  sh_in -memory 64m -cpus 0.5 -pids 20
  note "0::/ is the cgroup namespace at work: the container's cgroup is its root."

  say "Forty forks against a limit of twenty"
  SCRIPT='for i in $(seq 40); do sleep 2 & done; sleep 0.3; echo "running: $(ps | grep -c "sleep 2")"'
  sh_in -pids 20
  note "EAGAIN from fork. The shell reports it, the kernel refused it, nothing"
  note "else on the box noticed."

  say "A 100 MB string in a 64 MB container"
  SCRIPT='a=$(head -c 100000000 /dev/zero | tr "\0" x); echo survived'
  sh_in -memory 64m
  note "No output, exit 137: 128 + SIGKILL. The OOM killer, scoped to one cgroup."

  say "And the cgroup is gone once the container is"
  run ls /sys/fs/cgroup/mycontainer/
}

# -------------------------------------------------- 4: capabilities -------

demo_4() {
  banner "4. CAPABILITIES: what a container can do"

  say "Root inside. Try to mount something."
  SCRIPT='id; grep CapBnd /proc/self/status; mount -t tmpfs none /tmp && echo MOUNTED || echo DENIED'
  sh_in
  note "uid 0, and mount is refused. a80425fb is fourteen bits: the same"
  note "fourteen capabilities Docker leaves a container. CAP_SYS_ADMIN is not one."

  say "Give it CAP_SYS_ADMIN back"
  sh_in -caps default,+CAP_SYS_ADMIN
  note "One bit. That is the whole difference."

  say "Or everything, which is what --privileged means"
  SCRIPT='grep CapBnd /proc/self/status'
  sh_in -caps all
}

# ----------------------------------------------------------- 5: init ------
#
# Two containers running `sleep`, started in the background from here so
# the host side of the story fits in one pane. The first has sleep itself
# as PID 1. The second has the runtime's init at PID 1 and sleep under it.

demo_5() {
  banner "5. INIT: what PID 1 owes everybody else"

  local script='sh -c "sleep 0.3 &"; exec sleep 30'

  say "sleep as PID 1: a child exits, and nobody reaps it"
  printf '\n%s$ sudo ./mycontainer run -init=false %s /bin/sh -c %q &%s\n' "$BOLD" "$ROOTFS" "$script" "$RESET"
  sudo -n "$BIN" run -init=false "$ROOTFS" /bin/sh -c "$script" &
  local parent=$!
  sleep 1
  local init
  init="$(find_init "$parent")"
  run ps -o pid,ppid,stat,comm -p "$init" --ppid "$init"
  note "sleep is the container's PID 1. Under it, a zombie (state Z): the"
  note "background job's shell, orphaned onto PID 1 and never collected."

  say "Now send PID 1 a SIGTERM, the way docker stop would"
  run sudo -n kill -TERM "$init"
  sleep 1
  run ps -o pid,stat,comm -p "$init"
  note "Still running. The kernel drops a default-disposition signal sent to"
  note "the init of a PID namespace. This is why docker stop waits ten seconds"
  note "and then sends SIGKILL."
  sudo -n kill -KILL "$init" 2>/dev/null
  wait "$parent" 2>/dev/null

  say "The same, with the runtime's init in front"
  printf '\n%s$ sudo ./mycontainer run %s /bin/sh -c %q &%s\n' "$BOLD" "$ROOTFS" "$script" "$RESET"
  sudo -n "$BIN" run "$ROOTFS" /bin/sh -c "$script" &
  parent=$!
  sleep 1
  init="$(find_init "$parent")"
  run ps -o pid,ppid,stat,comm -p "$init" --ppid "$init"
  note "The runtime's init is PID 1, sleep is its child, and there is no"
  note "zombie: init reaped it."

  run sudo -n kill -TERM "$init"
  wait "$parent"
  note "exit $?: 128 + SIGTERM. Forwarded to sleep, and reported back out."
}

# ------------------------------------------------------- 6: rootless ------

demo_6() {
  banner "6. ROOTLESS: root in here, you out there"

  say "No sudo this time"
  run id -u
  SCRIPT='id; cat /proc/self/uid_map; grep CapBnd /proc/self/status'
  run "$BIN" run "$ROOTFS" /bin/sh -c "$SCRIPT"
  note "uid 0 inside. The map says: container 0 is host $(id -u), and that is"
  note "the only line. Same fourteen capabilities, in a namespace that owns"
  note "nothing on the host."

  say "The host's view of the same process"
  "$BIN" run "$ROOTFS" /bin/sleep 5 &
  local parent=$!
  sleep 0.5
  local init
  init="$(find_init "$parent")"
  run grep -E '^(Name|Uid|NSpid):' "/proc/$init/status"
  note "Uid $(id -u) on the host, PID 1 inside, host PID $init. Both true."
  wait "$parent"
}

# --------------------------------------------------------- 7: strace ------

demo_7() {
  banner "7. STRACE: the syscalls, counted"

  if ! command -v strace >/dev/null; then
    echo "strace is not installed" >&2
    return 1
  fi

  say "Every syscall this talk was about, in one container start"
  local trace=/tmp/mycontainer.trace
  run sudo -n strace -f -o "$trace" \
    -e trace=clone,clone3,unshare,sethostname,mount,pivot_root,umount2,prctl,execve \
    "$BIN" run -memory 64m -pids 20 "$ROOTFS" /bin/true

  say "Tallied"
  run sh -c "grep -oE '^[0-9]+ +[a-z_0-9]+\(' '$trace' | awk '{print \$2}' | tr -d '(' | sort | uniq -c | sort -rn"
  note "One clone3, with the namespace flags and the cgroup fd. One"
  note "sethostname, a dozen mounts, one pivot_root, one umount2. About"
  note "thirty prctl: twenty-seven PR_CAPBSET_DROP plus the Go runtime's."
  note "Three execve: the runtime, its child, then the command. The plain"
  note "clone calls are the Go runtime starting threads."

  say "The clone3 itself"
  run sh -c "grep -m1 'clone3(' '$trace'"
}

# ------------------------------------------------------------- main ------

main() {
  local target="${1:-check}"
  case "$target" in
    check) exec ./scripts/check.sh ;;
    1|2|3|4|5|6|7)
      need_build
      "demo_$target"
      ;;
    all)
      need_build
      for n in 1 2 3 4 5 6 7; do "demo_$n"; pause; done
      ;;
    *)
      printf 'usage: %s {check|1|2|3|4|5|6|7|all}\n' "$0" >&2
      exit 2
      ;;
  esac
}

main "$@"
