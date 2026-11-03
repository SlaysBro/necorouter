#!/bin/sh
# Remove necorouter: same as install.sh --uninstall.
#   sh uninstall.sh           remove the service, binaries and router accounts; configs and history are kept
#   sh uninstall.sh --purge   same, plus remove /etc/necorouter, /var/lib/necorouter and /run/necorouter
# root and foreign accounts are left alone; /etc/issue and sshd_config are restored to their previous state.
set -eu
DIR=$(cd "$(dirname "$0")" && pwd)
exec sh "$DIR/install.sh" --uninstall "$@"
