#!/bin/sh
# Kernel-level regression: a repeated "ip address" replaces the address on a veth.
# Usage: unshare -n -u -m sh test/addr_replace.sh   (root required)
set -eu
cd "$(dirname "$0")/.."
NR_NETNS=1 go test -count=1 ./internal/cli -run 'TestKernelAddressReplace' -v
