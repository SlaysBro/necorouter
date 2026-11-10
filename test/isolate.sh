#!/bin/sh
# Runs a command in a private view of the system files the daemon writes: /etc is a copy on tmpfs
# (accounts, locks removed), and /run, /var/lib, /var/log are empty tmpfs. Changes stay in the namespace.
# /home is not hidden: the runner's checkout lives there. useradd -m creates homes in the real /home.
# Usage: unshare -n -u -m sh test/isolate.sh COMMAND [ARGS...]
set -eu
src=$(mktemp -d)
cp -a /etc/. "$src"/
mount -t tmpfs tmpfs /etc
cp -a "$src"/. /etc/
rm -rf "$src"
rm -f /etc/*.lock /etc/*.lock.* 2>/dev/null || true
for d in /run /var/lib /var/log; do mount -t tmpfs tmpfs "$d"; done
exec "$@"
