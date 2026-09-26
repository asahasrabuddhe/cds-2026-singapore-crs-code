#!/usr/bin/env bash
#
# rootfs.sh, build a root filesystem from busybox in about a second.
#
#   ./scripts/rootfs.sh              # writes ./rootfs/busybox
#   ./scripts/rootfs.sh /tmp/r       # somewhere else
#
# A container needs a directory that looks like "/" from the inside. The
# usual way to get one is to unpack an image, and cmd/img2rootfs does that.
# This script is the way that needs no registry, no network and no image:
# busybox-static is one binary that is also sh, ps, ls, cat, hostname, id,
# mount and a hundred other things, so a rootfs is that binary plus a
# symlink for each name.
#
# It is what the tests use, and it is the safe choice for a workshop room
# where the conference wifi is a rumour.

set -euo pipefail

readonly OUT="${1:-rootfs/busybox}"

if ! command -v busybox >/dev/null; then
  echo "busybox not found. On Ubuntu and Debian: sudo apt-get install busybox-static" >&2
  exit 1
fi

# The binary must be static: the rootfs has no libc, no dynamic loader, no
# /lib at all. `busybox-static` is, and this refuses anything else rather
# than producing a rootfs that fails with "no such file or directory" for
# a file that plainly exists, which is the least helpful error in Unix.
if command -v ldd >/dev/null && ldd "$(command -v busybox)" 2>/dev/null | grep -q '=>'; then
  echo "busybox at $(command -v busybox) is dynamically linked; install busybox-static" >&2
  exit 1
fi

rm -rf "$OUT"
mkdir -p "$OUT"/{bin,sbin,etc,proc,sys,dev,tmp,root,usr/bin,usr/sbin}

cp "$(command -v busybox)" "$OUT/bin/busybox"

# One symlink per applet. `busybox --list` prints them, and busybox looks
# at argv[0] to decide which one it is. The list includes "busybox" itself,
# which must be skipped or the binary becomes a symlink to itself and every
# command fails with "too many levels of symbolic links".
for applet in $("$OUT/bin/busybox" --list); do
  [[ "$applet" == "busybox" ]] && continue
  ln -sf busybox "$OUT/bin/$applet"
done

# Enough of /etc for `id` to print a name and `sh` to find a prompt.
cat > "$OUT/etc/passwd" <<'EOF'
root:x:0:0:root:/root:/bin/sh
nobody:x:65534:65534:nobody:/:/bin/false
EOF
cat > "$OUT/etc/group" <<'EOF'
root:x:0:
nobody:x:65534:
EOF
echo 'busybox' > "$OUT/etc/hostname"
cat > "$OUT/etc/profile" <<'EOF'
export PS1='\h:\w# '
EOF

chmod 1777 "$OUT/tmp"

echo "rootfs: $OUT ($(du -sh "$OUT" | cut -f1), $(find "$OUT/bin" -type l | wc -l | tr -d ' ') applets)"
