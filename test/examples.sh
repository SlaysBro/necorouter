#!/bin/sh
# Check: the example configurations are applied by the parser in full without errors.
# unshare -n -u -m sh test/examples.sh
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${1:-$ROOT/dist/necorouter}
MKVETH=${2:-$ROOT/dist/mkveth}
# the binaries come from make; build them when they are missing (the same targets make test uses)
[ -x "$BIN" ] || (cd "$ROOT" && make host) || exit 1
[ -x "$MKVETH" ] || (cd "$ROOT" && make dist/mkveth) || exit 1
mount -t sysfs sysfs /sys
N=0
for ex in example-1 example-2; do N=$((N+1))
  export NECOROUTER_ROOT=$(mktemp -d); chmod 755 $NECOROUTER_ROOT
  mkdir -p $NECOROUTER_ROOT/etc/necorouter
  $MKVETH veth${N}0 veth${N}1; $MKVETH veth${N}2 veth${N}3
  printf "te0 veth${N}0\nte1 veth${N}2\n" > $NECOROUTER_ROOT/etc/necorouter/ports.map
  cp test/fixtures/$ex.conf $NECOROUTER_ROOT/etc/necorouter/startup-config
  $BIN daemon 2>$NECOROUTER_ROOT/daemon.log & P=$!; sleep 1
  echo "=== $ex: apply errors (only FRR/chrony/dnsmasq and VLAN/GRE without kernel drivers are expected) ==="
  cat $NECOROUTER_ROOT/daemon.log | sed 's/^necorouter: [0-9/]* [0-9:]* //'
  $BIN shell -c "show hostname;show ip interface brief;show ip route"
  kill $P; wait $P 2>/dev/null
  ip=0; for l in $(ls /sys/class/net); do :; done
  nft list tables 2>&1 | head -2
done
