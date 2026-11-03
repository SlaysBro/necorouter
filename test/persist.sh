#!/bin/sh
# unshare -n -u -m sh test/persist.sh
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${1:-$ROOT/dist/necorouter}
export NECOROUTER_ROOT=$(mktemp -d)
chmod 755 $NECOROUTER_ROOT; mkdir -p $NECOROUTER_ROOT/etc/necorouter
mount -t sysfs sysfs /sys
$MKVETH veth0 veth1
echo "te0 veth0" > $NECOROUTER_ROOT/etc/necorouter/ports.map
start() { $BIN daemon 2>>$NECOROUTER_ROOT/daemon.log & DPID=$!; sleep 1; }
stop() { kill $DPID; wait $DPID 2>/dev/null; }
start
$BIN shell <<'CLI' >/dev/null
enable
configure terminal
hostname br-rtr
ip domain-name au-team.irpo
username neto
 password P@ssw0rd
 role admin
exit
username viewer
 password v1ewer
 role noc
exit
interface isp
 description "to isp"
 ip address 172.16.2.2/28
 ip nat outside
exit
interface lan
 ip address 192.168.3.1/28
 ip nat inside
exit
port te0
 service-instance te0/isp
  encapsulation untagged
  connect ip interface isp
 exit
exit
ip pool dhcp 192.168.3.2-192.168.3.14
dhcp-server 1
 pool dhcp 1
 mask 28
 gateway 192.168.3.1
 dns 192.168.100.2 8.8.8.8
 domain-name au-team.irpo
exit
ip nat pool LAN 192.168.3.1-192.168.3.14
ip nat source dynamic inside-to-outside pool LAN overload interface isp
ip nat source static tcp 192.168.3.2 8080 172.16.2.2 8080
ip route 0.0.0.0/0 172.16.2.1
ip name-server 77.88.8.8
ntp server 172.16.2.1
ntp timezone utc+3
router ospf 1
 ospf router-id 10.10.10.2
 passive-interface default
 no passive-interface lan
 network 192.168.3.0/28 area 0
 area 0 authentication message-digest
end
write
CLI
$BIN shell -c "show running-config" > $NECOROUTER_ROOT/before.txt
stop
echo "--- startup-config on disk:"; cat $NECOROUTER_ROOT/etc/necorouter/startup-config
start
$BIN shell -c "show running-config" > $NECOROUTER_ROOT/after.txt
if diff $NECOROUTER_ROOT/before.txt $NECOROUTER_ROOT/after.txt; then echo "ROUNDTRIP: running-config identical after restart"; else echo "ROUNDTRIP: DIFFERENT"; fi
echo "--- replay errors in log:"; grep -i 'startup-config' $NECOROUTER_ROOT/daemon.log || echo "(none)"
echo "--- applied after restart:"; $BIN shell -c "show ip route;show ip interface brief;show services"
echo "--- -c as root:"; $BIN shell -c "show hostname"
echo "--- as configured admin user neto (SO_PEERCRED, no password):"
NU=$(getent passwd neto | cut -d: -f3); NG=$(getent passwd neto | cut -d: -f4)
setpriv --reuid=$NU --regid=$NG --clear-groups $BIN shell <<'CLI'
enable
configure terminal
hostname by-neto
end
show hostname
CLI
echo "--- as viewer (role noc): enable must be refused"
VU=$(getent passwd viewer | cut -d: -f3); VG=$(getent passwd viewer | cut -d: -f4)
printf 'enable\nshow hostname\n' | setpriv --reuid=$VU --regid=$VG --clear-groups $BIN shell
echo "--- unconfigured uid 65534: must be denied"
printf 'show hostname\n' | setpriv --reuid=65534 --regid=65534 --clear-groups $BIN shell; echo "exit=$?"
stop
