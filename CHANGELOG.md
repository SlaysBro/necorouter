# Changelog

## v0.22-beta.2

- Allow comma-separated IPv4 addresses and ranges in `ip pool` and `ip nat pool`; pass multiple ranges through DHCP, NAT, and running-config.
- Add `update channel release|pre` to select stable or pre-release updates. `update` and `update autocheck` follow the selected channel.
- Add `update autocheck` and `no update autocheck` to check for and offer updates when an administrator first logs in after startup.
- Add `banner startup` and `no banner startup` to control the pre-login console and SSH banner.
- Fix CLI prompt suffixes and invalid-input marker alignment, including service-policy mode.
- Document the minimum CPU requirement: one core and two threads.

