#!/bin/sh
# Regression: install.sh finds the binary (dist/, --bin file or directory, PATH, installed copy),
# rejects unsuitable files with a clear message and reinstalls safely.
# Usage: unshare -m sh test/install_find.sh
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
SH=${SH:-sh}
BIN=${BIN:-$HERE/dist/necorouter-x86_64}
[ -f "$BIN" ] || (cd "$HERE" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$BIN" ./cmd/necorouter)
W=$(mktemp -d /tmp/instfind.XXXXXX)
export W SH BIN HERE
unshare -m sh -c '
set -eu
mount -t tmpfs tmpfs /etc
mount -t tmpfs tmpfs /run                   # no systemd here: installer must not pick the host systemd
mkdir -p /etc/ssh /usr/local/bin
printf "ID=debian\n" > /etc/os-release
mkproj() { # fresh copy of the project without go.mod (so it is not built from source)
	rm -rf "$W/proj"; mkdir -p "$W/proj/packaging" "$W/proj/dist" "$W/proj/test/fixtures"
	cp -r "$HERE"/packaging/* "$W/proj/packaging/"
}
fail=0
ok() { echo "PASS $1"; }
bad() { echo "FAIL $1"; fail=1; }
run() { (cd "${CWD:-$W}" && PATH=/usr/bin:/bin:/usr/sbin:/sbin "$SH" "$@") ; }
uname_m=$(uname -m)
case $uname_m in x86_64) A=x86_64 ;; aarch64) A=aarch64 ;; *) A=$uname_m ;; esac

# 1) run from the project: dist/necorouter-<arch>, without --bin
mkproj; cp "$BIN" "$W/proj/dist/necorouter-$A"
rm -f /usr/local/bin/necorouter /usr/bin/necorouter
run "$W/proj/packaging/install.sh" --local --no-deps > "$W/o1" 2>&1 && ok "finds dist/necorouter-$A without --bin" || { bad "dist lookup"; cat "$W/o1"; }
[ -x /usr/local/bin/necorouter ] && ok "binary installed" || bad "binary missing"
[ -e /usr/bin/necorouter ] && ok "reachable from /usr/bin (sudo secure_path)" || bad "no /usr/bin link"

# 2) reinstall from an already installed copy (no binary nearby), without "same file"
mkproj
run "$W/proj/packaging/install.sh" --local --no-deps > "$W/o2" 2>&1 && ok "reinstall from installed copy" || { bad "reinstall"; cat "$W/o2"; }

# 3) --bin with a directory and with a file lacking the execute permission
mkproj; mkdir "$W/dl"; cp "$BIN" "$W/dl/necorouter"; chmod 644 "$W/dl/necorouter"
rm -f /usr/local/bin/necorouter /usr/bin/necorouter
run "$W/proj/packaging/install.sh" --local --no-deps --bin "$W/dl" > "$W/o3" 2>&1 && ok "--bin DIR, non-executable file fixed" || { bad "--bin dir"; cat "$W/o3"; }

# 4) junk file: clear error with a list of the paths checked
mkproj; echo "not a binary" > "$W/proj/dist/necorouter-$A"
rm -f /usr/local/bin/necorouter /usr/bin/necorouter
if run "$W/proj/packaging/install.sh" --local --no-deps > "$W/o4" 2>&1; then bad "garbage accepted"; else
	grep -q "no working binary found" "$W/o4" && grep -q "necorouter-$A" "$W/o4" && grep -q -- "--local\|--bin" "$W/o4" && ok "clear error for unusable binary" || { bad "error text"; cat "$W/o4"; }
fi
[ ! -e /usr/local/bin/necorouter ] && ok "nothing installed on failure" || bad "partial install"

# 5) --bin without a value
mkproj
if run "$W/proj/packaging/install.sh" --bin > "$W/o5" 2>&1; then bad "--bin without value"; else grep -q "needs a path" "$W/o5" && ok "--bin without path reported" || { bad "msg"; cat "$W/o5"; }; fi

# 6) uninstall removes the links
mkproj; cp "$BIN" "$W/proj/dist/necorouter-$A"
run "$W/proj/packaging/install.sh" --local --no-deps >/dev/null 2>&1
run "$W/proj/packaging/install.sh" --uninstall >/dev/null 2>&1
[ ! -e /usr/local/bin/necorouter ] && [ ! -e /usr/bin/necorouter ] && ok "uninstall removes binary and /usr/bin link" || bad "uninstall leftovers"
[ "$fail" = 0 ] && echo "ALL FIND CHECKS PASSED" || { echo FAILED; exit 1; }
'
