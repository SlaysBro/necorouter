#!/bin/sh
# Install necorouter on Debian/Ubuntu, CentOS/RHEL/Rocky/Alma/Fedora and Alpine.
#
#   sh install.sh [--version TAG | --pre | --local | --build | --bin PATH] [--no-enable] [--no-deps]
#                 [--uninstall [--purge]]
#   default: download the latest stable release from GitHub (repository in source.conf)
#   sh uninstall.sh [--purge]        same as install.sh --uninstall
#
# Login: standard Linux (PAM). The router creates Linux accounts with the necorouter-shell shell;
# root and foreign accounts are not touched.
#
# By default the service is enabled at boot and started (systemd: systemctl enable --now;
# OpenRC/Alpine: rc-update add default; rc-service start). --no-enable only installs.
set -eu

BIN_SRC=""; MODE=download; VERSION_TAG=""; PRE=0; ENABLE=1; DEPS=1; UNINSTALL=0; PURGE=0
HERE=$(cd "$(dirname "$0")" && pwd)
while [ $# -gt 0 ]; do
	case "$1" in
		--bin) [ $# -ge 2 ] || { echo "--bin needs a path" >&2; exit 2; }; BIN_SRC=$2; shift ;;
		--local) MODE=local ;;
		--build) MODE=build ;;
		--pre) PRE=1 ;;
		--version) [ $# -ge 2 ] || { echo "--version needs a tag" >&2; exit 2; }; VERSION_TAG=$2; shift
			case $VERSION_TAG in v*) ;; *) VERSION_TAG=v$VERSION_TAG ;; esac ;;
		--enable) ENABLE=1 ;;          # already enabled by default
		--no-enable) ENABLE=0 ;;
		--no-deps) DEPS=0 ;;
		--uninstall) UNINSTALL=1 ;;
		--purge) PURGE=1 ;;
		-h|--help) sed -n '2,12p' "$0"; exit 0 ;;
		*) echo "unknown option: $1" >&2; exit 2 ;;
	esac
	shift
done
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }

. /etc/os-release 2>/dev/null || true
ID_ALL=" ${ID:-} ${ID_LIKE:-} "
case "$ID_ALL" in
	*" alpine "*) FAMILY=alpine ;;
	*" debian "*|*" ubuntu "*|*" astra "*|*" raspbian "*) FAMILY=debian ;;
	*" rhel "*|*" centos "*|*" fedora "*|*" rocky "*|*" almalinux "*|*" redos "*|*" ol "*|*" amzn "*|*" openeuler "*) FAMILY=rhel ;;
	*) FAMILY=unknown ;;
esac
# fallback detection by package manager if os-release is unusual
if [ $FAMILY = unknown ]; then
	if command -v apk >/dev/null 2>&1; then FAMILY=alpine
	elif command -v apt-get >/dev/null 2>&1; then FAMILY=debian
	elif command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then FAMILY=rhel; fi
fi
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then INIT=systemd
elif command -v rc-service >/dev/null 2>&1; then INIT=openrc
else INIT=none; fi

BINDIR=/usr/local/bin
SSHD_CONF=/etc/ssh/sshd_config
SHELL_BIN=$BINDIR/necorouter-shell

# sshd_config gets two marked blocks:
#   top    - Banner (the directive must come before any Match);
#   bottom - Match restrictions (Match applies to the end of the file, so only at the end).
# We leave everything else in the file alone; if `sshd -t` fails, the previous file is restored.
TOP_BEGIN='# BEGIN necorouter-top'; TOP_END='# END necorouter-top'
BLOCK_BEGIN='# BEGIN necorouter'
BLOCK_END='# END necorouter'
strip_blocks() {
	sed -e "/^$TOP_BEGIN\$/,/^$TOP_END\$/d" -e "/^$BLOCK_BEGIN\$/,/^$BLOCK_END\$/d" "$1"
}
install_sshd_block() {
	[ -f "$SSHD_CONF" ] || return 0
	if [ -x "$SSHD" ]; then ensure_hostkeys; fi   # without host keys sshd -t fails and the block would be rolled back needlessly
	[ -f "$SSHD_CONF.necorouter.bak" ] || cp -p "$SSHD_CONF" "$SSHD_CONF.necorouter.bak"
	cp -p "$SSHD_CONF" "$SSHD_CONF.necorouter.prev"
	{
		cat <<TOP
$TOP_BEGIN
Banner /etc/necorouter/banner
PrintMotd no
$TOP_END
TOP
		strip_blocks "$SSHD_CONF"
		cat <<BLK
$BLOCK_BEGIN
Match Group necorouter
	PasswordAuthentication yes
	AllowTcpForwarding no
	AllowAgentForwarding no
	X11Forwarding no
	PermitTunnel no
	GatewayPorts no
	PermitUserRC no
$BLOCK_END
BLK
	} > "$SSHD_CONF.necorouter.tmp"
	cat "$SSHD_CONF.necorouter.tmp" > "$SSHD_CONF"; rm -f "$SSHD_CONF.necorouter.tmp"
	if [ -x "$SSHD" ] && ! "$SSHD" -t >/dev/null 2>&1; then
		cat "$SSHD_CONF.necorouter.prev" > "$SSHD_CONF"
		echo "WARN: sshd rejected the necorouter block; sshd_config left unchanged"
	fi
	rm -f "$SSHD_CONF.necorouter.prev"
}
# /etc/motd (text after a login) is emptied, so the system's own greeting (Alpine, Debian...) is not shown.
# The original is kept in $MOTD_DIR and restored on uninstall.
MOTD_DIR=/var/lib/necorouter/motd
motd_sync() {
	mkdir -p "$MOTD_DIR"
	if [ ! -f "$MOTD_DIR/orig" ] && [ ! -f "$MOTD_DIR/none" ]; then
		if [ -f /etc/motd ]; then cp -p /etc/motd "$MOTD_DIR/orig"; else : > "$MOTD_DIR/none"; fi
	fi
	: > /etc/motd
}
motd_restore() {
	if [ -f "$MOTD_DIR/orig" ]; then cp -p "$MOTD_DIR/orig" /etc/motd
	elif [ -f "$MOTD_DIR/none" ]; then rm -f /etc/motd; fi
	rm -rf "$MOTD_DIR"
}
remove_sshd_block() {
	[ -f "$SSHD_CONF" ] || return 0
	grep -q "^$BLOCK_BEGIN\$\|^$TOP_BEGIN\$" "$SSHD_CONF" || return 0
	strip_blocks "$SSHD_CONF" > "$SSHD_CONF.necorouter.tmp" && cat "$SSHD_CONF.necorouter.tmp" > "$SSHD_CONF"
	rm -f "$SSHD_CONF.necorouter.tmp"
}
# Linux accounts created by the router: shell = necorouter-shell. root and foreign
# accounts are not touched.
router_accounts() {
	[ -f /etc/passwd ] || return 0
	while IFS=: read -r n _ uid _ _ _ sh; do
		[ "$sh" = "$SHELL_BIN" ] && [ "${uid:-0}" -ge 1000 ] 2>/dev/null && echo "$n"
	done < /etc/passwd
	return 0
}
del_account() {
	if command -v userdel >/dev/null 2>&1; then userdel -r "$1" >/dev/null 2>&1 || true; else deluser --remove-home "$1" >/dev/null 2>&1 || true; fi
}

svc_disable() { # $1 = name of the system service replaced by the built-in instance
	case $INIT in
		systemd) systemctl disable --now "$1" >/dev/null 2>&1 || true ;;
		openrc) rc-service "$1" stop >/dev/null 2>&1 || true; rc-update del "$1" >/dev/null 2>&1 || true ;;
	esac
}

# sshd on Alpine (and often in minimal images) is installed without host keys and
# is not enabled at boot; without them the config check fails and ssh
# is unavailable. We fix that here.
SSHD=$(command -v sshd 2>/dev/null || true)
[ -n "$SSHD" ] || SSHD=/usr/sbin/sshd

ensure_hostkeys() {
	if ! ls /etc/ssh/ssh_host_*_key >/dev/null 2>&1 && command -v ssh-keygen >/dev/null 2>&1; then
		ssh-keygen -A >/dev/null 2>&1 || echo "WARN: could not generate sshd host keys"
	fi
}

ensure_sshd() {
	if [ ! -x "$SSHD" ]; then echo "WARN: sshd is not installed; ssh access to the router will not work"; return; fi
	ensure_hostkeys
	if ! "$SSHD" -t >/dev/null 2>&1; then echo "WARN: sshd -t failed, check /etc/ssh/sshd_config"; return; fi
	case $INIT in
		systemd)
			U=sshd; systemctl list-unit-files ssh.service >/dev/null 2>&1 && U=ssh
			systemctl enable "$U" >/dev/null 2>&1 || true
			if systemctl is-active --quiet "$U"; then systemctl reload "$U" >/dev/null 2>&1 || true; else systemctl start "$U" >/dev/null 2>&1 || true; fi ;;
		openrc)
			U=sshd
			rc-update add "$U" default >/dev/null 2>&1 || true
			if rc-service "$U" status >/dev/null 2>&1; then rc-service "$U" reload >/dev/null 2>&1 || true; else rc-service "$U" start >/dev/null 2>&1 || true; fi ;;
	esac
}

if [ $UNINSTALL = 1 ]; then
	case $INIT in
		systemd) systemctl disable --now necorouter 2>/dev/null || true; rm -f /etc/systemd/system/necorouter.service; systemctl daemon-reload ;;
		openrc) rc-service necorouter stop 2>/dev/null || true; rc-update del necorouter 2>/dev/null || true; rm -f /etc/init.d/necorouter ;;
	esac
	# banner: restore the previous /etc/issue (while the binary is still in place)
	[ -x $BINDIR/necorouter ] && $BINDIR/necorouter issue restore 2>/dev/null || true
	motd_restore
	# router accounts: those it created are deleted, adopted ones get their previous shell back
	[ -x $BINDIR/necorouter ] && $BINDIR/necorouter cleanup >/dev/null 2>&1 || true
	for u in $(router_accounts); do del_account "$u"; done   # fallback path (no state record)
	if command -v groupdel >/dev/null 2>&1; then groupdel necorouter >/dev/null 2>&1 || true; else delgroup necorouter >/dev/null 2>&1 || true; fi
	remove_sshd_block
	rm -f $BINDIR/necorouter $BINDIR/necorouter-shell
	rm -rf /usr/local/share/necorouter
	[ -L /usr/bin/necorouter ] && [ "$(readlink /usr/bin/necorouter)" = "$BINDIR/necorouter" ] && rm -f /usr/bin/necorouter
	if [ -f /etc/shells ]; then grep -v 'necorouter-shell' /etc/shells > /etc/shells.tmp && cat /etc/shells.tmp > /etc/shells; rm -f /etc/shells.tmp; fi
	if [ -x "$SSHD" ] && "$SSHD" -t >/dev/null 2>&1; then
		case $INIT in
			systemd) systemctl reload sshd >/dev/null 2>&1 || true ;;
			openrc) rc-service sshd reload >/dev/null 2>&1 || true ;;
		esac
	fi
	if [ $PURGE = 1 ]; then
		# router directories only: configs, history, archives, log, state
		rm -rf /etc/necorouter /var/lib/necorouter /run/necorouter
		echo "removed completely (router accounts deleted, /etc/issue restored, configurations and history deleted)."
	else
		echo "removed (router accounts deleted, /etc/issue restored). Configuration kept in /etc/necorouter; add --purge to delete it."
	fi
	exit 0
fi

# ---- binary ----
# Where the binary comes from (first match wins):
#   --bin PATH     a file or a directory with the binary
#   --local        an existing binary: next to the script, in dist/, the current directory, PATH, or installed
#   --build        build from the sources next to this script (needs Go)
#   default        download the latest stable release from GitHub (see source.conf) and verify SHA256SUMS
# --pre takes the newest release including pre-releases; --version TAG takes a specific release.
case $(uname -m) in
	x86_64|amd64) ARCHS="x86_64 amd64"; ASSET_ARCH=x86_64 ;;
	aarch64|arm64) ARCHS="aarch64 arm64"; ASSET_ARCH=aarch64 ;;
	*) ARCHS=$(uname -m); ASSET_ARCH="" ;;
esac
SEARCHED=""
usable() { # $1 = path; true if it is a working binary for this machine
	[ -f "$1" ] || return 1
	SEARCHED="$SEARCHED
    $1"
	[ -x "$1" ] || chmod +x "$1" 2>/dev/null || true
	"$1" version >/dev/null 2>&1
}
pick_in_dir() { # $1 = directory
	for n in necorouter $(for a in $ARCHS; do echo "necorouter-$a"; done); do
		if usable "$1/$n"; then BIN_SRC="$1/$n"; return 0; fi
	done
	return 1
}
find_local() {
	for d in "$HERE" "$HERE/.." "$HERE/../dist" "$HERE/dist" "$PWD" "$PWD/dist"; do
		[ -d "$d" ] && pick_in_dir "$d" && return 0
	done
	P=$(command -v necorouter 2>/dev/null || true)
	if [ -n "$P" ] && usable "$P"; then BIN_SRC=$P; return 0; fi
	usable "$BINDIR/necorouter" && { BIN_SRC="$BINDIR/necorouter"; return 0; }
	return 1
}
find_bin_arg() {
	if [ -d "$BIN_SRC" ]; then pick_in_dir "$BIN_SRC" && return 0; return 1; fi
	usable "$BIN_SRC" && return 0
	return 1
}
build_source() {
	[ -f "$HERE/../go.mod" ] && command -v go >/dev/null 2>&1 || { echo "--build needs Go and the sources next to install.sh" >&2; return 1; }
	echo "==> building necorouter from source"
	TMPB=$(mktemp -d)
	(cd "$HERE/.." && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$TMPB/necorouter" ./cmd/necorouter) || return 1
	BIN_SRC="$TMPB/necorouter"
}
# GitHub repository and release URLs. NECOROUTER_RELEASE_BASE overrides the base (used by tests).
REPO=$(sed -n 's/^REPO=//p' "$HERE/source.conf" 2>/dev/null | head -1)
RELEASE_BASE=${NECOROUTER_RELEASE_BASE:-https://github.com/$REPO/releases}
has_downloader() {
	command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 ||
	{ command -v busybox >/dev/null 2>&1 && busybox --list 2>/dev/null | grep -qx wget; } ||
	command -v python3 >/dev/null 2>&1
}
ensure_downloader() {
	has_downloader && return 0
	[ "$DEPS" = 1 ] || return 1
	echo "==> installing curl for release download"
	case $FAMILY in
		debian)
			export DEBIAN_FRONTEND=noninteractive
			apt-get update -qq || true
			apt-get install -y -qq --no-install-recommends curl >/dev/null 2>&1 || true ;;
		rhel)
			PM=$(command -v dnf || command -v yum)
			$PM install -y -q curl >/dev/null 2>&1 || true ;;
		alpine)
			apk update -q || true
			apk add -q curl >/dev/null 2>&1 || true ;;
		*) return 1 ;;
	esac
	command -v curl >/dev/null 2>&1
}
fetch() { # $1 = URL, $2 = file
	if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"
	elif command -v busybox >/dev/null 2>&1 && busybox --list 2>/dev/null | grep -qx wget; then
		busybox wget -q -O "$2" "$1"
	elif command -v python3 >/dev/null 2>&1; then
		python3 -c '
import sys, urllib.request
with urllib.request.urlopen(sys.argv[1]) as response:
	data = response.read()
if sys.argv[2] == "/dev/stdout":
	sys.stdout.buffer.write(data)
else:
	with open(sys.argv[2], "wb") as output:
		output.write(data)
' "$1" "$2"
	else
		echo "no downloader found (need curl, wget, BusyBox wget, or Python 3)" >&2
		return 127
	fi
}
download_release() {
	[ -n "$ASSET_ARCH" ] || { echo "no release binary for $(uname -m)" >&2; return 1; }
	[ -n "$REPO" ] || { echo "packaging/source.conf does not name a repository" >&2; return 1; }
	ensure_downloader || { echo "no downloader available; install curl/wget/Python 3, or use --local / --build / --bin" >&2; return 1; }
	asset=necorouter-$ASSET_ARCH
	if [ -n "$VERSION_TAG" ]; then base=$RELEASE_BASE/download/$VERSION_TAG
	elif [ "$PRE" = 1 ]; then
		tag=$(fetch "https://api.github.com/repos/$REPO/releases" /dev/stdout 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
		[ -n "$tag" ] || { echo "could not find a pre-release tag" >&2; return 1; }
		base=$RELEASE_BASE/download/$tag
	else base=$RELEASE_BASE/latest/download
	fi
	TMPB=$(mktemp -d)
	echo "==> downloading $asset from $base"
	fetch "$base/$asset" "$TMPB/$asset" || { echo "download failed: $base/$asset" >&2; return 1; }
	fetch "$base/SHA256SUMS" "$TMPB/SHA256SUMS" || { echo "download failed: $base/SHA256SUMS" >&2; return 1; }
	if ! (cd "$TMPB" && grep " $asset\$" SHA256SUMS > one.sum && sha256sum -c one.sum >/dev/null 2>&1); then
		echo "checksum mismatch for $asset: not installed" >&2; return 1
	fi
	chmod 0755 "$TMPB/$asset"
	usable "$TMPB/$asset" || { echo "downloaded $asset does not run on this machine" >&2; return 1; }
	BIN_SRC="$TMPB/$asset"
}
if [ -n "$BIN_SRC" ]; then
	find_bin_arg || { echo "necorouter: no working binary at $BIN_SRC for this machine ($(uname -m))" >&2; exit 1; }
elif [ "$MODE" = local ]; then
	find_local || { echo "necorouter: no working binary found for this machine ($(uname -m))." >&2; [ -n "$SEARCHED" ] && printf 'Checked:%s\n' "$SEARCHED" >&2; echo "Fix: pass --bin PATH, or use --build, or drop --local to download a release." >&2; exit 1; }
elif [ "$MODE" = build ]; then
	build_source || exit 1
else
	download_release || { echo "Fix: check the network, use --pre/--version TAG, or --local / --build / --bin PATH" >&2; exit 1; }
fi
echo "==> using binary: $BIN_SRC"

# ---- dependencies (best effort; anything unavailable only produces a warning) ----
if [ $DEPS = 1 ]; then
	echo "==> installing dependencies ($FAMILY)"
	case $FAMILY in
		debian)
			export DEBIAN_FRONTEND=noninteractive
			apt-get update -qq || true
			for p in openssh-server openssh-client passwd nftables dnsmasq-base frr chrony tzdata iputils-ping traceroute strongswan-charon strongswan-swanctl; do
				apt-get install -y -qq --no-install-recommends "$p" >/dev/null 2>&1 || echo "WARN: package $p not installed"
			done ;;
		rhel)
			PM=$(command -v dnf || command -v yum)
			for p in openssh-server openssh-clients shadow-utils nftables dnsmasq frr chrony tzdata iputils traceroute strongswan; do
				$PM install -y -q "$p" >/dev/null 2>&1 || echo "WARN: package $p not installed (frr may need EPEL/FRR repo)"
			done ;;
		alpine)
			apk update -q || true
			for p in openssh openssh-client-default nftables dnsmasq frr chrony tzdata iputils traceroute shadow strongswan; do
				apk add -q "$p" >/dev/null 2>&1 || echo "WARN: package $p not installed"
			done ;;
		*) echo "WARN: unknown distribution; install manually: sshd, nftables, dnsmasq, frr, chrony, tzdata, strongswan" ;;
	esac
	# the router runs its own dnsmasq/frr/chrony/charon instances, so system services are not needed
	for s in dnsmasq frr chronyd chrony strongswan strongswan-starter strongswan-swanctl; do svc_disable "$s"; done
fi

# FRR with a vty group (frrvty) does not start as root unless root is in that group:
#   privs_init: user(root) is not part of vty group specified(frrvty)
# The router starts the daemons itself as user frr; if there is no such user,
# we add root to the group (fallback).
if command -v getent >/dev/null 2>&1 && getent group frrvty >/dev/null 2>&1 && ! id frr >/dev/null 2>&1; then
	echo "==> no 'frr' user: adding root to group frrvty"
	{ usermod -aG frrvty root || addgroup root frrvty; } >/dev/null 2>&1 || echo "WARN: add root to group frrvty manually"
fi

# ---- files ----
echo "==> installing files"
mkdir -p $BINDIR
# reinstall from an already installed copy: install complains "same file"
if [ "$(cd "$(dirname "$BIN_SRC")" && pwd)/$(basename "$BIN_SRC")" != "$BINDIR/necorouter" ]; then
	install -m 0755 "$BIN_SRC" $BINDIR/necorouter.new && mv -f $BINDIR/necorouter.new $BINDIR/necorouter
fi
# packaging files for `update` (the router runs this installer to install a new release)
SHARE=/usr/local/share/necorouter/packaging
if [ "$(cd "$HERE" && pwd)" != "$SHARE" ]; then
	mkdir -p $SHARE/etc/necorouter
	for f in install.sh uninstall.sh source.conf DEPENDENCIES necorouter.service necorouter.openrc; do
		[ -f "$HERE/$f" ] && cp "$HERE/$f" $SHARE/$f
	done
	cp "$HERE/etc/necorouter/banner" $SHARE/etc/necorouter/banner 2>/dev/null || true
fi
# sudo/su on CentOS and others restrict PATH (secure_path) without /usr/local/bin
for l in necorouter; do
	[ -e /usr/bin/$l ] || ln -s $BINDIR/$l /usr/bin/$l 2>/dev/null || true
done
ln -sf necorouter $BINDIR/necorouter-shell
mkdir -p /etc/necorouter /var/lib/necorouter
chmod 755 /etc/necorouter
# pre-login banner: take packaging/etc/necorouter/banner, only if /etc/necorouter does not have one yet
# (admin edits are preserved on reinstall); if the file is absent, no banner is shown
if [ ! -f /etc/necorouter/banner ] && [ -f "$HERE/etc/necorouter/banner" ]; then
	install -m 0644 "$HERE/etc/necorouter/banner" /etc/necorouter/banner
fi
# a file for sshd and /etc/issue for the console (getty/login); visible before root login too
"$BINDIR/necorouter" issue sync || echo "WARN: could not write the banner to /etc/issue"
motd_sync || echo "WARN: could not clear /etc/motd"
for f in README.md; do [ -f "$HERE/../$f" ] && cp "$HERE/../$f" /usr/local/share/doc/necorouter/ || true; done

# ---- account tool check ----
for t in chpasswd; do command -v $t >/dev/null 2>&1 || echo "WARN: $t not found - router accounts cannot get passwords"; done
{ command -v useradd >/dev/null 2>&1 || command -v adduser >/dev/null 2>&1; } || echo "WARN: neither useradd nor adduser found - router accounts cannot be created"
command -v usermod >/dev/null 2>&1 || echo "WARN: usermod not found - existing accounts cannot be taken over (install shadow-utils / shadow)"

# ---- sshd: banner before the password prompt, restrictions for router accounts ----
touch /etc/shells
grep -q necorouter-shell /etc/shells 2>/dev/null || echo $BINDIR/necorouter-shell >> /etc/shells
install_sshd_block
ensure_sshd

# ---- service ----
case $INIT in
	systemd)
		install -m 0644 "$HERE/necorouter.service" /etc/systemd/system/necorouter.service
		systemctl daemon-reload
		if [ $ENABLE = 1 ]; then
			systemctl enable necorouter >/dev/null 2>&1 || echo "WARN: could not enable necorouter in autostart"
			systemctl restart necorouter >/dev/null 2>&1 || echo "WARN: could not start necorouter (journalctl -u necorouter)"
		fi ;;
	openrc)
		mkdir -p /etc/init.d
		install -m 0755 "$HERE/necorouter.openrc" /etc/init.d/necorouter
		if [ $ENABLE = 1 ]; then
			rc-update add necorouter default >/dev/null 2>&1 || echo "WARN: could not enable necorouter in autostart"
			if rc-service necorouter status >/dev/null 2>&1; then rc-service necorouter restart >/dev/null 2>&1 || echo "WARN: could not restart necorouter"
			else rc-service necorouter start >/dev/null 2>&1 || echo "WARN: could not start necorouter"; fi
		fi ;;
	*) echo "WARN: no systemd/OpenRC found; run '$BINDIR/necorouter daemon' manually" ;;
esac

echo
echo "necorouter installed."
UP=0
case $INIT in
	systemd) [ $ENABLE = 1 ] && systemctl is-active --quiet necorouter && UP=1 ;;
	openrc) [ $ENABLE = 1 ] && rc-service necorouter status >/dev/null 2>&1 && UP=1 ;;
esac
if [ $UP = 1 ]; then echo "The router is UP and enabled in autostart."
elif [ $ENABLE = 1 ]; then echo "WARN: the router is installed and enabled, but is not running - check the service log."
else
	echo "The router is NOT running (--no-enable: nothing is started, no autostart)."
	case $INIT in
		systemd) echo "Bring it up:   systemctl enable --now necorouter" ;;
		openrc) echo "Bring it up:   rc-update add necorouter default && rc-service necorouter start" ;;
	esac
fi
echo "CLI:           ssh admin@<host> or the console login (admin / admin) - the router is the login shell of the accounts it creates; root keeps its normal shell"
echo "Passwords:     Linux accounts only ('username NAME' / 'password PASS' in the CLI, or passwd NAME). CHANGE admin's default password: passwd admin"
echo "Config:        /etc/necorouter/startup-config      Ports: /etc/necorouter/ports.map     "
