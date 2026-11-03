#!/bin/sh
# filter-map ipv4: nftables rules. Run inside: unshare -n -u -m sh test/filtermap.sh
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
filter-map ipv4 WEB 10
 match tcp 10.0.0.0/24 any eq www
 match tcp 10.0.0.0/24 any eq 443
 set accept
exit
filter-map ipv4 WEB 20
 match 1 host 10.0.0.9 any
 set discard
exit
filter-map ipv4 TUN 10
 match gre host 172.16.1.2 host 172.16.1.3
 set accept
exit
interface isp
 set filter-map in WEB
 set filter-map out TUN
end
CLI
sleep 1
T=$(nft list table ip necorouter_fm 2>&1)
chk "table is created" 'echo "$T" | grep -q "table ip necorouter_fm"'
chk "in chain: tcp 80 accepted from the network" 'echo "$T" | grep -q "iifname \"veth0\" ip saddr 10.0.0.0/24 tcp dport 80 accept"'
chk "in chain: every match line gives its own rule" 'echo "$T" | grep -q "tcp dport 443 accept"'
chk "in chain: discard becomes drop" 'echo "$T" | grep -q "ip protocol icmp ip saddr 10.0.0.9 drop"'
chk "in chain ends with an implicit reject" 'echo "$T" | grep -q "iifname \"veth0\" reject"'
chk "out chain: gre allowed" 'echo "$T" | grep -q "oifname \"veth0\" ip protocol gre ip saddr 172.16.1.2 ip daddr 172.16.1.3 accept"'
chk "out chain ends with an implicit reject" 'echo "$T" | grep -q "oifname \"veth0\" reject"'
chk "forward hook only (the router itself is not filtered)" 'echo "$T" | grep -q "hook forward" && ! echo "$T" | grep -q "hook input"'
chk "show filter-map ipv4" '$BIN shell <<CLI | grep -q "match tcp 10.0.0.0/24 any eq 80"
enable
show filter-map ipv4
CLI'
chk "running-config has the lists and the bindings" '$BIN shell <<CLI | grep -q "set filter-map in WEB 10"
enable
show running-config
CLI'

$BIN shell <<'CLI' >/dev/null
enable
configure terminal
interface isp
 no set filter-map in WEB
 no set filter-map out TUN
end
CLI
sleep 1
chk "no bindings removes the table" '! nft list table ip necorouter_fm >/dev/null 2>&1'

kill $DPID 2>/dev/null
wait $DPID 2>/dev/null
[ $FAIL = 0 ] && echo "ALL FILTER-MAP CHECKS PASSED" || { echo FAILED; cat $NECOROUTER_ROOT/daemon.log; exit 1; }
