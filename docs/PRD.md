# PRD — adbl (Local Ad Blocker)

Status: Approved · v1 · Linux only

## 1. Problem

Ad/tracker blocking today happens only inside browsers via extensions. Native apps,
OS telemetry and non-browser processes are unfiltered. Network-wide solutions
(Pi-hole, AdGuard Home) require a separate device or a root daemon.

## 2. Goal

A single desktop app (Go + Wails v3) that blocks ads/trackers **system-wide on this
machine** by acting as a local DNS sinkhole, shows what was blocked and when, and
**never leaves the machine without working DNS**.

## 3. Non-goals (v1)

- Cosmetic/page-element filtering (requires a browser extension layer)
- HTTPS interception / MITM (no CA installation)
- Network-wide blocking (other devices on the LAN)
- Windows / macOS support
- Defeating browser DNS-over-HTTPS bypass (documented limitation)

## 4. Users & stories

| As a... | I want to... | So that... |
|---|---|---|
| End user | Turn on protection with one click | ads/trackers stop resolving system-wide |
| End user | See a live dashboard of blocked traffic | I know what the app is doing |
| End user | Allow a domain that broke a site | sites work without disabling protection |
| End user | Quit the app safely | my DNS reverts to normal, no dead connection |
| Power user | Manage blocklist sources | I control what gets blocked |

## 5. Features

| Priority | Feature |
|---|---|
| **P0** | Local DNS sinkhole on `127.0.0.1:5453` (UDP+TCP, loopback only) |
| **P0** | System DNS switch via one-time polkit elevation + **guaranteed restore** (graceful exit, next-start repair, manual fallback documented) |
| **P0** | Blocklist download/parse/hot-swap (hosts, plain domain, `\|\|domain^` formats), periodic refresh |
| **P0** | Manual allow/block rules that survive blocklist updates |
| **P0** | Live query log (streaming into UI) |
| **P0** | SQLite history: raw query log (7d retention) + daily rollups (180d) |
| **P0** | Dashboard with live data visualization (KPI cards + sparklines, stacked area, donut, top-10 bars, live ticker) |
| **P1** | System tray (status, pause, quit), autostart, light/dark theme |
| **P1** | History page: 30-day bars, hourly heatmap, date range |
| **P2** | DoH upstream, per-client breakdown, block-mode variants, CSV export |

## 6. Acceptance criteria

- **AC1** `dig @127.0.0.1 -p 5453 ad.doubleclick.net` → NXDOMAIN; allowed domain → real answer.
- **AC2** Quit/crash → system DNS restored (on exit or next start); **never a dead-DNS blackhole**.
- **AC3** Dashboard counters update ≤1s after a query.
- **AC4** Blocklist refresh changes block decisions without app restart.
- **AC5** Zero elevated prompts after first enable (until re-apply/repair).
- **AC6** All data stays local; the only outbound traffic is blocklist HTTPS downloads.

## 7. Success metrics

- Blocks ≥90% of known ad/tracker domains in default sources (spot-check list).
- Added DNS latency <5ms p95 local; dashboard idle CPU near 0.
- Restore path verified on every session exit (checklist item in Phase 3/8).

## 8. Known limitations (documented in README)

- Apps using DoH (Firefox/Chrome default) bypass the sinkhole.
- Hard kill during an elevation prompt can strand DNS until next start/repair
  (`resolvectl revert` documented as manual fallback).
