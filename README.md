[Русский](README.ru.md)

# necorouter

A software router for Linux with a command-line interface in the style of commercial routers.
It is a single Go binary that drives well-known Linux daemons (FRR, dnsmasq, chrony, nftables, strongSwan)
from one configuration file.

> **Status:** version 0.21, a learning and lab project. It covers the commands used in network-administration
> exercises and exams. It is not a production router and has not been verified on real hardware for every feature.

## About the project

necorouter was written as a study project: to learn how routers work by building one. It is not a copy of any
vendor's software. The command syntax is modelled on commercial router CLIs so that the commands feel familiar
to people who have worked with them.

The code was written with the help of **Claude**, an AI assistant by Anthropic, which was used for design,
code generation, tests and review. **OpenAI Codex** was also used to debug issues, improve tests and review changes.
The router itself is written in **Go** using only the standard library.

## Features

- Interface addresses, static routes, DNS, ports and VLANs (`port teN`, service instances, `dot1q`), GRE tunnels.
- NAT: dynamic NAT with overload, and static destination NAT with optional hairpin.
- DHCP server (dnsmasq), NTP client (chrony), OSPF (FRR).
- IPsec: IKEv1 and IKEv2 with pre-shared keys, GRE over IPsec (strongSwan).
- Route maps, prefix lists and policy filter lists for routing policy.
- Access lists (`filter-map ipv4`), security profiles.
- Users with roles, Linux accounts for login, password hashes only.
- Configuration: `write` saves the startup configuration, automatic backups, named configuration files, diff and history.
- Own event journal and audit log of user actions, independent of journald and syslog.
- CLI conveniences: `?` help, Tab completion, command history, paged output, `show ... | include/exclude/grep`.

## How it works

- `necorouter daemon` is a background service. It holds the running configuration and applies it to the system.
- `necorouter` (the CLI) connects to the daemon over a local Unix socket. Each login opens a session.
- Each system function is a separate standard daemon or tool, started only when the configuration needs it
  and stopped when the feature is removed:

| Function | Component |
|---|---|
| Login and SSH | Linux accounts (PAM), OpenSSH |
| NAT, filter-map, security profiles | nftables |
| DHCP and DNS | dnsmasq |
| OSPF | FRR (zebra, ospfd) |
| NTP | chrony |
| IPsec | strongSwan (charon, swanctl) |
| Ping and traceroute | iputils, traceroute |

Packages for each distribution are listed in [`packaging/DEPENDENCIES`](packaging/DEPENDENCIES).

## Supported systems

- **Distributions:** Debian and Ubuntu (also Astra Linux, Raspberry Pi OS); CentOS, RHEL, Rocky, AlmaLinux,
  Fedora, RedOS, Oracle Linux, Amazon Linux, openEuler; Alpine Linux.
- **Init systems:** systemd and OpenRC.
- **Architectures:** amd64 and arm64 (static binaries, `CGO_ENABLED=0`).
- **Build requirements:** Go 1.24 or newer.

The installer detects the distribution family from `/etc/os-release`. Not every distribution has been tested on
real hardware: the installer and service scripts have been checked in emulated environments, including Alpine.

## Installation

1. **Get the installer.** Clone the repository (or download the source archive from the
   [Releases](https://github.com/SlaysBro/necorouter/releases) page):

       git clone https://github.com/SlaysBro/necorouter.git
       cd necorouter

2. **Install** as root. By default the installer downloads the latest stable release from GitHub,
   checks it against `SHA256SUMS`, attempts to install the required system packages, copies the binary to `/usr/local/bin`,
   creates the `/usr/bin/necorouter` link, and enables and starts the service:

       sudo sh packaging/install.sh

   The GitHub repository is set in [`packaging/source.conf`](packaging/source.conf).

   Downloads use an available `curl`, `wget`, BusyBox `wget`, or Python 3. If none is available, the installer
   tries to install `curl` through the system package manager (unless `--no-deps` is set). The checksum file is
   downloaded from the same release; it detects transfer damage but is not a separate signature of the release.
   The installer does not upgrade the whole operating system; the package manager may update packages required by the router.

   Installer options:

| Option | Meaning |
|---|---|
| *(none)* | download the latest stable release (pre-releases are skipped) |
| `--version TAG` | install a specific release, for example `--version 0.21` |
| `--pre` | install the newest release, including pre-releases (betas) |
| `--build` | build from the sources next to the installer instead of downloading (needs Go 1.24+) |
| `--local` | use a binary that is already present: next to the installer, in `dist/`, or in `PATH` |
| `--bin PATH` | install this binary file (or a directory containing it) |
| `--no-enable` | install only, do not enable or start the service |
| `--no-deps` | do not install system packages |
| `--uninstall [--purge]` | remove the router; `--purge` also deletes the router accounts, configurations, history and logs |

   Running the installer again installs the latest release over the current one; the configuration is kept.

   To build yourself instead: `make` (builds `dist/necorouter-x86_64` and `dist/necorouter-aarch64`, needs Go 1.24+),
   then `sudo sh packaging/install.sh --local`.

3. **Check** that the service runs:

       necorouter version
       systemctl status necorouter        # systemd
       rc-service necorouter status       # OpenRC / Alpine

4. **Log in** over SSH or on the console with a Linux account. The built-in account is `admin` with the
   password `admin`. The router warns about the default password after login; change it at once:

       configure terminal
       username admin
       password NEW_PASSWORD
       end

5. **Map ports to network cards.** Ports `te0`, `te1`, … are assigned to physical interfaces through
   `/etc/necorouter/ports.map`. New cards are added automatically and matched by MAC address.

### Uninstall

    sudo sh packaging/install.sh --uninstall           # keep configurations
    sudo sh packaging/install.sh --uninstall --purge   # remove everything, including router accounts

## Updating

From privileged mode:

    update check            current version, latest stable release and latest pre-release
    update list [N]         the newest N releases (default 10)
    update                  install the latest stable release (asks for confirmation)
    update pre              install the newest pre-release
    update to TAG           install a given release, for example `update to v0.21`; older releases are downgrades

The update runs in the background; the router restarts and sessions end. Progress is in
`/var/log/necorouter/update.log`. Releases are checked against `SHA256SUMS`; the checksums come from the same
release, so they protect against damaged downloads, not against a compromised release. These commands update
necorouter only, not the operating system.

## Quick start: an example configuration

This example puts one interface on a LAN, adds a user, and serves DHCP on that LAN.
It assumes the physical interface for port `te0` is set in `ports.map`.

    hostname router1
    ip domain-name lab.local
    !
    interface lan
     ip address 192.168.10.1/24
    !
    port te0
     service-instance te0/lan
      encapsulation untagged
      connect ip interface lan
    !
    end

How to apply it:

1. Log in and enter privileged mode with `enable`, then `configure terminal`.
2. Paste the lines above, or type them one by one.
3. Check the result with `show running-config`.
4. Save with `write`. Without `write`, the configuration is lost when the daemon restarts.

Useful commands while working: `?` shows what can be typed next, `show running-config` shows the active
configuration, `write` saves it, and `exit` leaves the current mode. The complete command reference is in
[`docs/index.html`](docs/index.html).

## Command reference and documentation

- [`docs/index.html`](docs/index.html): an interactive reference of every command, with search and a dark theme.
  Open it in a browser.
- [Releases](https://github.com/SlaysBro/necorouter/releases): version history with release notes and binaries.
- [`packaging/DEPENDENCIES`](packaging/DEPENDENCIES): system packages per distribution.

## Limitations

- Static IPsec (`manual` keys, `show crypto sa`) is not implemented; only IKE-based IPsec is.
- BGP is not implemented. Route maps support OSPF redistribution, not BGP attributes.
- Remote syslog and `aaa` with TACACS+ or RADIUS are not implemented.
- Some commands follow the design of commercial routers, not an official specification. Check unusual commands
  against the behaviour you need before relying on them.
- Real OSPF on FRR, DHCP, NAT and GRE on real hardware and on every supported distribution are not verified.

## Building and testing

    make                # static binaries for amd64 and arm64
    make host           # binary for this machine
    make test           # go vet, unit tests and end-to-end scripts

`make test` needs root and `unshare`; the end-to-end scripts create network namespaces and virtual interfaces.
Unit tests alone: `go test ./...`.

## Versioning

Versions have the form `0.NN` for feature releases and `0.NN.P` for small changes and fixes on top of `0.NN`
(for example `0.22`, then `0.22.1`). Feature releases raise `NN` by one; fixes raise `P`.
Pre-releases are tagged `0.NN-beta.N`. The first public release is `0.21`.
Version 1.0 is not planned yet.

## License

necorouter is free software: you can redistribute it and/or modify it under the terms of the
[GNU General Public License version 3](LICENSE) as published by the Free Software Foundation.
It is distributed WITHOUT ANY WARRANTY; see the license for details.

## Contributing

Bug reports and suggestions are welcome as issues in the GitHub repository. There is no formal contribution process yet.
