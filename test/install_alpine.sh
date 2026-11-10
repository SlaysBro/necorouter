#!/bin/sh
# Regression: packaging/install.sh on Alpine (OpenRC, busybox utilities, no sshd host keys).
# Environment: tmpfs over /etc, fake /etc/os-release=Alpine, stubs for apk/rc-*/sshd/ssh-keygen.
# Usage:  unshare -m sh test/install_alpine.sh            (SH=/path/to/busybox sh for ash as on Alpine)
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
SH=${SH:-sh}
BIN=${BIN:-$HERE/dist/necorouter-x86_64}
[ -f "$BIN" ] || (cd "$HERE" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$BIN" ./cmd/necorouter)
W=$(mktemp -d /tmp/alpine-test.XXXXXX)
mkdir -p "$W/bin"
LOG=$W/calls.log
for c in apk rc-update groupdel addgroup usermod gpasswd chpasswd; do
	printf '#!/bin/sh\necho "%s $*" >> %s\nexit 0\n' "$c" "$LOG" > "$W/bin/$c"
done
printf '#!/bin/sh\necho "passwd $*" >> %s\nexit 0\n' "$LOG" > "$W/bin/passwd"
# adduser and userdel/deluser modify the fake /etc/passwd so the result can be checked
cat > "$W/bin/adduser" <<EOR
#!/bin/sh
echo "adduser \$*" >> $LOG
sh=; prev=; for a; do [ "\$prev" = -s ] && sh=\$a; prev=\$a; name=\$a; done
echo "\$name:x:1100:1100::/home/\$name:\$sh" >> /etc/passwd
EOR
for c in userdel deluser; do
cat > "$W/bin/$c" <<EOR
#!/bin/sh
echo "$c \$*" >> $LOG
for a; do name=\$a; done
grep -v "^\$name:" /etc/passwd > /etc/passwd.tmp; cat /etc/passwd.tmp > /etc/passwd; rm -f /etc/passwd.tmp
EOR
done
# rc-service: status "not running", start/stop/reload succeed
cat > "$W/bin/rc-service" <<EOR
#!/bin/sh
echo "rc-service \$*" >> $LOG
[ "\$2" = status ] && exit 3
exit 0
EOR
# sshd as in reality: -t fails without host keys; old OpenSSH does not know Include
cat > "$W/bin/sshd" <<EOR
#!/bin/sh
echo "sshd \$*" >> $LOG
if [ "\$1" = -t ]; then
	ls /etc/ssh/ssh_host_*_key >/dev/null 2>&1 || { echo "sshd: no hostkeys available -- exiting." >&2; exit 1; }
	if [ -f $W/reject_include ] && grep -q "^Include" /etc/ssh/sshd_config; then echo "Bad configuration option: Include" >&2; exit 1; fi
fi
exit 0
EOR
cat > "$W/bin/ssh-keygen" <<EOR
#!/bin/sh
echo "ssh-keygen \$*" >> $LOG
[ "\$1" = -A ] && touch /etc/ssh/ssh_host_ed25519_key
exit 0
EOR
chmod +x "$W"/bin/*
cp "$BIN" "$W/necorouter-bin"
mkdir -p "$W/packaging" && cp "$HERE/packaging/install.sh" "$HERE/packaging/necorouter.service" "$HERE/packaging/necorouter.openrc" "$W/packaging/" && cp -r "$HERE/packaging/etc" "$W/packaging/"
cp "$HERE/README.md" "$W/" 2>/dev/null || true

unshare -m -p -f --mount-proc env W="$W" SH="$SH" LOG="$LOG" sh -c '
set -eu
mount -t tmpfs tmpfs /etc
mount -t tmpfs tmpfs /run                   # no systemd here: installer must pick OpenRC, not the host systemd
printf "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.20.3\n" > /etc/os-release
mkdir -p /etc/ssh /etc/ssh/sshd_config.d /etc/init.d /var/lib/necorouter
mount -t tmpfs tmpfs /var/lib/necorouter     # router state must not survive the test
printf "{\"users\":[\"neto\"]}\n" > /var/lib/necorouter/state.json
printf "Welcome \\\\n \\\\l\n" > /etc/issue
printf "Welcome to Alpine Linux 3.20\n" > /etc/motd
printf "#Port 22\nPasswordAuthentication no\n" > /etc/ssh/sshd_config
printf "root:x:0:0:root:/root:/bin/ash\nneto:x:1001:1001::/home/neto:/usr/local/bin/necorouter-shell\nbob:x:1002:1002::/home/bob:/bin/ash\n" > /etc/passwd
PATH=$W/bin:/usr/bin:/bin:/usr/sbin:/sbin
snap() {
	cp /etc/ssh/sshd_config "$W/sshd_config.$1"
	cp /etc/passwd "$W/passwd.$1"
	cp /etc/shells "$W/shells.$1" 2>/dev/null || true
	cp /etc/issue "$W/issue.$1"
	cp /etc/motd "$W/motd.$1"
}
run() { # $1 = label, the rest are install.sh arguments
	l=$1; shift
	"$SH" "$W/packaging/install.sh" "$@" > "$W/out.$l" 2>&1 && echo 0 > "$W/rc.$l" || echo $? > "$W/rc.$l"
	snap "$l"
}
run first --bin "$W/necorouter-bin"
[ -f /etc/init.d/necorouter ] && cp /etc/init.d/necorouter "$W/init-installed" || true
cp /var/lib/necorouter/issue/orig "$W/issue.orig" 2>/dev/null || true
cp /var/lib/necorouter/motd/orig "$W/motd.orig" 2>/dev/null || true
run again --bin "$W/necorouter-bin"          # a repeated run does not duplicate the blocks
# sshd that rejects unknown directives (and Include from old OpenSSH)
touch "$W/reject_include"
run strict --bin "$W/necorouter-bin"
rm -f "$W/reject_include"
"$SH" "$W/packaging/install.sh" --uninstall > "$W/out-un.txt" 2>&1 && echo 0 > "$W/rc-un" || echo $? > "$W/rc-un"
snap un
'
fail=0
ok() { echo "PASS $1"; }; bad() { echo "FAIL $1"; fail=1; }
has() { grep -q "$2" "$1"; }
for st in first again strict; do
	[ "$(cat "$W/rc.$st")" = 0 ] && ok "[$st] installer exits 0" || { bad "[$st] installer exit $(cat "$W/rc.$st")"; cat "$W/out.$st"; }
done
has "$W/out.first" "WARN: sshd" && { bad "sshd warning"; cat "$W/out.first"; } || ok "sshd config validates (host keys generated first)"
has "$LOG" "ssh-keygen -A" && ok "ssh-keygen -A called when no host keys" || bad "host keys not generated"
has "$LOG" "rc-update add sshd default" && ok "sshd enabled in default runlevel" || bad "sshd not enabled"
has "$LOG" "rc-service sshd start" && ok "sshd started" || bad "sshd not started"
has "$LOG" "rc-update add necorouter default" && ok "necorouter enabled in autostart by default" || bad "necorouter autostart"
has "$LOG" "rc-service necorouter start" && ok "necorouter started by default" || bad "necorouter not started"
[ -f "$W/init-installed" ] && ok "OpenRC script installed" || bad "init script missing"
has "$W/shells.first" "necorouter-shell" && ok "shell registered in /etc/shells" || bad "shell not registered"
has "$LOG" "rc-service dnsmasq stop\|rc-update del dnsmasq" && ok "system dnsmasq disabled" || bad "system dnsmasq untouched"
has "$W/issue.first" '\\\\_' && ok "banner written to /etc/issue with doubled backslashes" || bad "banner in /etc/issue"
has "$W/issue.orig" "^Welcome" && ok "previous /etc/issue backed up" || bad "no issue backup"
[ ! -s "$W/motd.first" ] && ok "system greeting cleared from /etc/motd" || bad "motd still has text"
[ ! -s "$W/motd.again" ] && ok "motd stays cleared after a repeated install" || bad "motd refilled"
has "$W/motd.orig" "Alpine" && ok "previous /etc/motd kept for uninstall" || bad "no motd backup"
has "$W/sshd_config.first" "^PrintMotd no" && ok "sshd PrintMotd disabled" || bad "PrintMotd"
head -1 "$W/sshd_config.first" | grep -q "^# BEGIN necorouter-top" && ok "Banner block is at the top (before any Match)" || bad "top block missing"
has "$W/sshd_config.first" "^Banner /etc/necorouter/banner" && ok "Banner set" || bad "Banner"
has "$W/sshd_config.first" "^#Port 22" && ok "original sshd_config kept" || bad "original config damaged"
has "$W/sshd_config.first" "^PasswordAuthentication no" && ok "global PasswordAuthentication untouched" || bad "global setting changed"
tail -9 "$W/sshd_config.first" | grep -q "^Match Group necorouter" && ok "Match Group block at the end" || bad "Match block not last"
has "$W/sshd_config.first" "^	PasswordAuthentication yes" && ok "password auth enabled only for the router group" || bad "Match PasswordAuthentication"
has "$W/sshd_config.first" "AllowTcpForwarding no" && has "$W/sshd_config.first" "PermitUserRC no" && ok "forwarding/rc disabled for router accounts" || bad "restrictions"
has "$W/sshd_config.first" "Match User" && bad "unexpected gateway block" || ok "no gateway block"
[ "$(grep -c '^# BEGIN necorouter$' "$W/sshd_config.again")" = 1 ] && [ "$(grep -c '^# BEGIN necorouter-top$' "$W/sshd_config.again")" = 1 ] && ok "repeated install keeps exactly one block of each kind" || bad "duplicated blocks"
has "$W/out.strict" "WARN: sshd" && bad "[strict] sshd warning" || ok "[strict] sshd config valid"
has "$W/sshd_config.strict" "^Include" && bad "installer must not add Include" || ok "installer adds no Include"
has "$W/passwd.first" "^necorouter:" && bad "gateway account must not exist" || ok "no gateway account"
# uninstall
[ "$(cat "$W/rc-un")" = 0 ] && ok "uninstall exits 0" || { bad "uninstall failed"; cat "$W/out-un.txt"; }
has "$W/issue.un" "^Welcome" && ok "uninstall restores the original /etc/issue" || bad "issue not restored"
has "$W/motd.un" "Alpine" && ok "uninstall restores the original /etc/motd" || bad "motd not restored"
has "$W/sshd_config.un" "necorouter" && bad "sshd block left after uninstall" || ok "sshd blocks removed on uninstall"
has "$W/passwd.un" "^neto:" && bad "router account left" || ok "router-created account removed"
has "$W/passwd.un" "^bob:" && has "$W/passwd.un" "^root:" && ok "root and foreign accounts untouched" || bad "foreign account removed"
has "$LOG" "userdel.* bob\|deluser.* bob\|userdel.* root" && bad "foreign account touched" || ok "no deletion of foreign accounts"
[ "$fail" = 0 ] && echo "ALL INSTALL CHECKS PASSED" || { echo "FAILED"; exit 1; }
