#!/bin/sh
# Run inside: unshare -n -u -m sh test/e2e.sh <path-to-binary>
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
echo "=== scenario ==="
$BIN shell <<'CLI'
enable
configure terminal
hostname hq-rtr
ip domain-name au-team.irpo
interface isp
 ip address 172.16.1.2/28
 ip nat outside
exit
interface vl100
 ip address 192.168.100.1/26
 ip nat inside
interface vl200
 ip address 192.168.200.1/28
 ip nat inside
 dhcp-server 1
exit
interface tunnel.0
 ip address 10.10.10.1/30
 ip tunnel 172.16.1.2 172.16.2.2 mode gre
 ip ospf authentication message-digest
 ip ospf message-digest-key 1 md5 P@ssw0rd
exit
port te0
 service-instance te0/isp
  encapsulation untagged
  connect ip interface isp
 exit
 service-instance te0/vl100
  encapsulation dot1q 100 exact
  rewrite pop 1
  connect ip interface vl100
 exit
 service-instance te0/vl200
  encapsulation dot1q 200 exact
  connect ip interface vl200
 exit
exit
username net_admin
 password P@ssw0rd
 role admin
exit
ip pool cli 192.168.200.2-192.168.200.14
dhcp-server 1
 pool cli 1
 mask 28
 gateway 192.168.200.1
 dns 192.168.100.2
 domain-name au-team.irpo
exit
ip nat pool LOCAL 192.168.100.1-192.168.100.62,
ip nat pool LOCAL 192.168.100.1-192.168.100.62
ip nat source dynamic inside-to-outside pool LOCAL overload 172.16.1.2
ip nat source static tcp 192.168.100.2 8080 172.16.1.2 8080
ip nat source static tcp 192.168.100.2 2027 172.16.1.2 2027
ip route 0.0.0.0/0 172.16.1.1
ntp timezone utc+3
router ospf 1
 ospf router-id 10.10.10.1
 passive-interface default
 no passive-interface tunnel.0
 network 10.10.10.0/30 area 0
 network 192.168.100.0/26 area 0
 area 0 authentication message-digest
end
write
show ip interface brief
show ip route
show port
show ip nat translations
show services
show interface tunnel.0
CLI
echo "=== kernel ==="
for d in /sys/class/net/*; do n=$(basename $d); echo "$n flags=$(cat $d/flags) mtu=$(cat $d/mtu) type=$(cat $d/type)"; done
echo "--- fib"; cat /proc/net/route
echo "--- ip_forward=$(cat /proc/sys/net/ipv4/ip_forward)"
nft list table ip necorouter
echo "=== daemon log ==="; cat $NECOROUTER_ROOT/daemon.log
echo "=== files ==="; ls -la $NECOROUTER_ROOT/etc/necorouter/; getent passwd net_admin admin
kill $DPID; wait $DPID 2>/dev/null
echo "=== after stop ==="; ls /sys/class/net; nft list tables; getent passwd net_admin || echo "net_admin removed"
echo ROOT=$NECOROUTER_ROOT
