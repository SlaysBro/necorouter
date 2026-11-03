#!/bin/sh
# Security profiles: nftables rules. Run inside: unshare -n -u -m sh test/security.sh
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${1:-$ROOT/dist/necorouter}
MKVETH=${2:-$ROOT/dist/mkveth}
export NECOROUTER_ROOT=$(mktemp -d)
mkdir -p $NECOROUTER_ROOT/etc/necorouter
mount -t sysfs sysfs /sys
$MKVETH veth0 veth1
echo "te0 veth0" > $NECOROUTER_ROOT/etc/necorouter/ports.map
$BIN daemon 2> $NECOROUTER_ROOT/daemon.log &
DPID=$!
sleep 1
FAIL=0
ok() { echo "PASS $1"; }
bad() { echo "FAIL $1"; FAIL=1; }
chk() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

$BIN shell <<'CLI' >/dev/null
enable
configure terminal
interface isp
 ip address 172.16.1.2/28
exit
port te0
 service-instance te0/isp
  encapsulation untagged
  connect ip interface isp
 exit
exit
end
CLI
sleep 1
T=$(nft list table ip necorouter_sec 2>&1)
chk "default profile: ssh is dropped on line ports" 'echo "$T" | grep -q "iifname \"veth0\" tcp dport 22 drop"'
chk "default profile: telnet and snmp dropped" 'echo "$T" | grep -q "tcp dport 23 drop" && echo "$T" | grep -q "udp dport 161 drop"'
chk "established connections are not cut" 'echo "$T" | grep -q "ct state established,related accept"'

$BIN shell <<'CLI' >/dev/null
enable
configure terminal
no security default
end
CLI
sleep 1
chk "no security default removes the table" '! nft list table ip necorouter_sec >/dev/null 2>&1'

$BIN shell <<'CLI' >/dev/null
enable
configure terminal
security-profile 1
 rule 0 permit tcp host 172.16.1.1 any eq ssh
 rule 1 deny tcp any any eq ssh
 rule 2 deny ospf any any
exit
security 1
end
CLI
sleep 1
T=$(nft list table ip necorouter_sec 2>&1)
chk "custom profile: rule order and accept" 'echo "$T" | grep -n "accept" | grep -q "saddr 172.16.1.1" && echo "$T" | grep -q "ip protocol ospf drop"'
chk "show running-config keeps the profile" '$BIN shell <<CLI2 | grep -q "security 1"
enable
show running-config
CLI2'
$BIN shell <<'CLI' >/dev/null
enable
configure terminal
no security 1
end
write
CLI
sleep 1
chk "no security: table removed" '! nft list table ip necorouter_sec >/dev/null 2>&1'
kill $DPID; wait $DPID 2>/dev/null
chk "no leftover tables after stop" '[ -z "$(nft list tables)" ]'
[ $FAIL = 0 ] && echo "ALL SECURITY CHECKS PASSED"
exit $FAIL
