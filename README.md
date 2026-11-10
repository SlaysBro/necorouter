[Русский](README.ru.md)

# necorouter

A software router for Linux with a command-line interface in the style of commercial routers.
One Go binary drives well-known Linux daemons (FRR, dnsmasq, chrony, nftables, strongSwan) from one configuration.

## Contents

- [About](#about)
- [Installation](#installation)
- [Quick start](#quick-start)
- [Modes and roles](#modes-and-roles)
- [Features](#features)
- [How it works](#how-it-works)
- [Traffic shaping and bufferbloat](#traffic-shaping-and-bufferbloat)
- [Updating](#updating)
- [Command reference and documentation](#command-reference-and-documentation)
- [Limitations](#limitations)
- [Building and testing](#building-and-testing)
- [Versioning](#versioning)
- [License](#license)
- [Contributing](#contributing)

## About

necorouter was written as a study project: to learn how routers work by building one. It is not a copy of any
vendor's software. The command syntax is modelled on commercial router CLIs so that the commands feel familiar
to people who have worked with them.

> **Status:** version 0.23-beta.1, a learning and lab project. It covers the commands used in network-administration
> exercises and exams. It is not a production router and has not been verified on real hardware for every feature.

The code was written with the help of **Claude**, an AI assistant by Anthropic, which was used for design,
code generation, tests and review. **OpenAI Codex** was also used to debug issues, improve tests and review changes.
The router itself is written in **Go** using only the standard library.

Main functions: interface addresses, static routes, DNS, ports and VLANs, GRE tunnels, NAT, bandwidth control
(CAKE), DHCP server, NTP client, OSPF, IPsec (IKEv1 and IKEv2), route maps, access lists, security profiles,
users with roles, configuration files with history, and an audit log of user actions.

## Installation

### Requirements and supported systems

- **Distributions:** Debian and Ubuntu (also Astra Linux, Raspberry Pi OS); CentOS, RHEL, Rocky, AlmaLinux,
  Fedora, RedOS, Oracle Linux, Amazon Linux, openEuler; Alpine Linux.
- **Init systems:** systemd and OpenRC.
- **Architectures:** amd64 and arm64 (static binaries, `CGO_ENABLED=0`).
- **Minimum hardware:** 1 CPU core and 2 threads.
- **Build requirements:** Go 1.24 or newer (only if you build from sources).

The installer detects the distribution family from `/etc/os-release`. Not every distribution has been tested on
real hardware: the installer and service scripts have been checked in emulated environments, including Alpine.
Packages for each distribution are listed in [`packaging/DEPENDENCIES`](packaging/DEPENDENCIES).

### Install

1. Get the installer. Clone the repository, or download the source archive from the
   [Releases](https://github.com/SlaysBro/necorouter/releases) page:

       git clone https://github.com/SlaysBro/necorouter.git
       cd necorouter

2. Install as root. By default the installer downloads the latest stable release from GitHub, checks it against
   `SHA256SUMS`, attempts to install the required system packages, copies the binary to `/usr/local/bin`,
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

3. Check that the service runs:

       necorouter version
       systemctl status necorouter        # systemd
       rc-service necorouter status       # OpenRC / Alpine

4. Log in over SSH or on the console with a Linux account. The built-in account is `admin` with the
   password `admin`. The router warns about the default password after login; change it at once:

       configure terminal
       username admin
       password NEW_PASSWORD
       end

5. Map ports to network cards. Ports are assigned to physical interfaces through
   `/etc/necorouter/ports.map`. New cards are added automatically and matched by MAC address.
   A new card is named by its speed: `e0` (up to 100 Mbit/s), `ge0` (1 Gbit/s), `te0` (10 Gbit/s and faster,
   or when the speed is unknown). Names that already exist in `ports.map` are kept.

### Uninstall

    sudo sh packaging/install.sh --uninstall           # keep configurations
    sudo sh packaging/install.sh --uninstall --purge   # remove everything, including router accounts

## Quick start

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

Useful commands while working: `?` shows what can be typed next, `Tab` completes a word, `show running-config`
shows the active configuration, `write` saves it, and `exit` leaves the current mode.

## Modes and roles

The CLI has three modes, as in EcoRouter. The prompt shows the current one.

| Prompt | Mode | Entered by | Used for |
|---|---|---|---|
| `necorouter>` | user mode | login | status (`show version`, `show interface`, `show log` and others), `terminal`; `ping` for admin only |
| `necorouter#` | privileged mode | `enable` | the `show` commands of the role, `ping`, `traceroute`; for admin also `write`, `copy`, `reload`, `configure terminal` |
| `necorouter(config)#` | configuration mode | `configure terminal` | the configuration, admin only |

Roles:

- `admin` has everything.
- `noc` (auditor) sees the status in user mode and the running configuration, with the secrets hidden (OSPF keys, IPsec pre-shared keys and the enable password hash are shown as `*****`). In privileged mode it also sees the other `show` commands (users, ARP, NTP, clock, services, DHCP leases, route maps), and can use `ping` and `traceroute`. It cannot read the startup configuration, the saved files or the features, and cannot change the configuration.
- `helpdesk` can enter privileged mode and use `ping` and `traceroute` there; in user mode it has `disable` and `help`. It has no `show` commands.
- A user without a role cannot enter privileged mode.

A configuration file is replayed without the role and feature checks: the startup configuration is read by the daemon, and only admin can load another file with `copy`. The same commands typed by a user stay closed while their feature is off.

Inside a sub-mode, a top-level configuration command typed without indentation (for example `ip route` or `ip pool` in `(config-router)#`) is taken from the configuration mode, and the sub-mode is left, as in a configuration file.

A command the role may not use is reported as `% Invalid input detected`, the same as an unknown command. The full list of commands per mode is in the reference, [`docs/index.html`](docs/index.html).

## Features

**Addressing and ports**
- Interface addresses, static routes, DNS servers and domain name, address pools (`ip pool`).
- Ports and VLANs: `port NAME` (`e0`, `ge0`, `te0`, …) is mapped to a network card, service instances `service-instance PORT/NAME`
  with `encapsulation untagged` or `dot1q`, GRE tunnels.

**NAT and security**
- Dynamic NAT with overload and static destination NAT with optional hairpin (`ip nat`).
- Access lists (`filter-map ipv4`) and security profiles (`security-profile`).

**Routing**
- OSPF (FRR), route maps, prefix lists and policy filter lists for routing policy.
- IPsec with IKEv1 and IKEv2, pre-shared keys, GRE over IPsec (strongSwan).

**Services**
- DHCP server (dnsmasq): `dhcp-server N`, pools, gateway and DNS for clients. Leases: `show dhcp-server clients`,
  forgotten with `clear dhcp-server clients` (privileged mode, admin only).
- NTP client (chrony): `ntp server ADDRESS`; `ntp synchronize` steps the clock at once.
- Bandwidth control with service policies on CAKE (see [Traffic shaping](#traffic-shaping-and-bufferbloat)).

**Users and access**
- Users with roles `admin`, `noc`, `helpdesk`; the rights of each role are in [Modes and roles](#modes-and-roles). Passwords are kept only in Linux accounts, never in the configuration.
- Login is ordinary Linux (PAM, OpenSSH). The router creates the accounts for `username NAME`.

**Configuration files**
- `write` saves the startup configuration; `write file NAME` saves named files, `show files config` lists them.
- `show startup-config` (admin only), `show running-config` (for `noc` with the secrets hidden), and optional history, diff and copying between files (`feature config-features`).
- A setting that needs a feature, loaded from a file, stays in the configuration and takes effect once `feature config-features` is in the configuration.
- Factory defaults: `copy empty-config startup-config` (applied after reload), and `copy empty-config running-config` (replaces the running configuration and applies it now; admin only, asks for confirmation).
- Autosave copies (`autosave interval MIN [keep N]`, `no autosave`): listed with `show config history`, removed one at a time with `delete config file NAME`. No command deletes all copies at once.

**Logging and CLI**
- An event journal and an audit log of user actions, independent of journald and syslog.
- Pasting a block of configuration lines at once: every line is executed in order.
- `show banner startup` shows the pre-login banner (console and SSH): its state and text. `banner startup` and `no banner startup` switch it.
- `?` help, Tab completion, command history, Ctrl-L to clear the screen, `show ... | include/exclude/begin/grep`. There is no output redirection to a file, as in EcoRouter.
- Long `show` output opens on a full-screen page; `q` returns to the console. `show log follow` opens a live page that scrolls with new messages until you scroll up or press `q` or Ctrl-C.

Features that are not part of the standard syntax are switched on with `feature NAME`; `show features` lists them.

## How it works

- `necorouter daemon` is a background service. It holds the running configuration and applies it to the system.
- `necorouter` (the CLI) connects to the daemon over a local Unix socket. Each login opens a session.
- Each system function is a separate standard daemon or tool, started only when the configuration needs it
  and stopped when the feature is removed:

| Function | Component |
|---|---|
| Login and SSH | Linux accounts (PAM), OpenSSH |
| NAT, filter-map, security profiles | nftables |
| Bandwidth control | `tc` CAKE qdisc and IFB via service policies |
| DHCP and DNS | dnsmasq |
| OSPF | FRR (zebra, ospfd) |
| NTP | chrony |
| IPsec | strongSwan (charon, swanctl) |
| Ping and traceroute | iputils, traceroute |

## Traffic shaping and bufferbloat

When a link is saturated, queues can increase ping latency. Configure service policies on the WAN service-instance.
Start with limits around 90–95% of measured sustained speeds so CAKE can keep the bottleneck queue on the router.
The `in` policy shapes downloads through IFB; `out` shapes uploads on the WAN egress. Linux kernel support for
CAKE (`sch_cake`) is required.

For a measured 100/10 Mbps link, start around 95/9 Mbps:

    configure terminal
    service-policy WAN-DOWN
     bandwidth mbps 95
    exit
    service-policy WAN-UP
     bandwidth mbps 9
    exit
    port te0
     service-instance wan
      encapsulation untagged
      connect ip interface wan
      service-policy WAN-DOWN in
      service-policy WAN-UP out
    end
    write
    show service-policy

Use your actual port and service-instance names. The rates are explicit; the router does not infer your provider's
sustained link speed.

## Updating

From privileged mode:

    update check            current version, latest stable release and latest pre-release
    update list [N]         the newest N releases (default 10)
    update                  install the latest release from the selected channel
    update pre              install the newest pre-release
    update to TAG           install a given release
    update channel          show the selected update channel
    update channel release  use stable releases
    update channel pre      use pre-releases for `update` and autocheck
    update autocheck        check on login and offer available updates
    no update autocheck     disable automatic checks

The update runs in the background; the router restarts and sessions end. Progress is in
`/var/log/necorouter/update.log`. Releases are checked against `SHA256SUMS`; the checksums come from the same
release, so they protect against damaged downloads, not against a compromised release. These commands update
necorouter only, not the operating system. The update also installs the installer files of the release
(`necorouter-packaging.tar.gz`).

## Command reference and documentation

- [`docs/index.html`](docs/index.html): the reference of every command with a navigable command tree.
  Open it in a browser. The commands are the same as in `?`.
- [Releases](https://github.com/SlaysBro/necorouter/releases): version history with release notes and binaries.
- [`CHANGELOG.md`](CHANGELOG.md): what changed in each version.
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
    make docs           # rewrite the command tree in docs/index.html from the code
    make test           # go vet, unit tests (incl. the docs check) and end-to-end scripts

`make test` needs root and `unshare`; the end-to-end scripts create network namespaces and virtual interfaces.
Unit tests alone: `go test ./...`. Adding a command: define its node in the file of its mode (`internal/cli/tree_exec.go` for user and privileged mode, `tree_config.go` for the global configuration, one file per object such as `tree_iface.go`) and put it in the list of that mode. Say who may use it on the node itself: `.For(mode, roles...)`; a command that depends on a feature also gets `.When(featOn(...))`. Then run `make docs`. The tests check that the command is reachable from every configuration mode, that the modes and roles named in `For` exist, that the handler agrees with what the node offers, and that the documentation is up to date. After a command is added or changed, run `make docs`: the documentation page embeds the command tree, and `make test` fails while the page is out of date.

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
