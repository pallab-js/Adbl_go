# Implementation Plan — adbl

Phased delivery; each phase has a **verify gate** that must pass before the next
starts. Docs (this folder) are built in Phase 0 as the blueprint; README /
.gitignore / LICENSE are finalized in Phase 8 as specified.

## Phase 0 — Scaffold + planning docs ✅

- [x] `git init`, Wails v3.1.0 `react` template at repo root
- [x] PRD, SDA, TRD, UI-UX, this file
- [x] **Gate:** build works — GTK4 headers pending user install, developing on legacy `-tags gtk3` meanwhile

## Phase 1 — DNS engine core (pure Go) ✅

- [x] `internal/dnsengine`: UDP+TCP server, suffix-walk decision, forwarder (UDP, TCP-on-truncation, 3s failover), event hooks
- [x] `internal/blocklist`: parsers (hosts / plain / `||domain^`) + atomic `Store`
- [x] **Gate:** `go test -race ./internal/...` green; manual `dig` on 127.0.0.1:5453 → NXDOMAIN blocked / NXDOMAIN subdomain / NOERROR forwarded

## Phase 2 — Blocklist manager ✅

- [x] HTTPS download (30MB cap), dedupe, atomic swap, per-source toggles, 24h auto-refresh, manual allow/block rules (allow wins)
- [x] `internal/config`: persisted rules/flags (atomic tmp+rename)
- [x] Default sources verified: StevenBlack hosts (77,760) + HaGeZi light ABP (87,225), fetched in 0.8s
- [x] **Gate:** real-source test blocks known ad domains, leaves `example.com` alone; `-race` clean incl. concurrent refresh

## Phase 3 — System DNS integration + safety ✅

- [x] `internal/systemdns`: detect, default-link discovery, state file **before** elevation, pkexec apply/restore with fixed argv, rollback on partial failure, tampered-state rejection
- [x] Scope decision: **systemd-resolved only** — `resolvectl` supports `ip:port` since systemd 246 (we target 259+); NM-without-resolved reported unsupported (documented)
- [x] **Gate (live, this machine):** apply → system-wide NXDOMAIN for blocked, real A for allowed, `~.` routing active → restore → original v4+v6 servers back, state file removed, resolution healthy. Orphan-process port conflict found & fixed during gate.

## Phase 4 — Query logging + stats ✅

- [x] `internal/store`: schema, WAL, batch writer (500ms/200 rows), cursor list
  with LIKE-escape, summary/series/top-N, rollup+retention in one transaction
- [x] **Gate:** `go test -race ./...` green (batcher flush, retention, rollup SQL)

## Phase 5 — Wails services + bindings ✅

- [x] `internal/service` (dns, stats, queries, blocklists, rules, system),
  6 typed events registered (`dns:batch` 500ms, `stats:tick` 1s,
  `blocklist:status`, `dns:state`, `system:state`, `rules:changed`)
- [x] `main.go` composition root: startup repair (serve-again / stale-state
  discard / restore-on-bind-failure), teardown restore hook, event tickers
- [x] `greetservice.go` deleted, template App.tsx stubbed with a live service call
- [x] **Gate:** `wails3 generate bindings` → 6 services/21 methods/6 events; a
  frontend `DNSService.Status()` call returned
  `{"running":true,"addr":"127.0.0.1:5453","pausedUntil":0}` through the
  generated bindings; app runs clean, engine binds, query persisted to SQLite,
  no state file created without an explicit Apply.

## Phase 6 — React UI

Shell (sidebar + status pill), Dashboard (all visualization widgets), Live Log,
Blocklists, Rules, History, Settings, first-run wizard, theme, empty/loading/error states.

- **Gate:** manual walkthrough vs PRD AC1–AC6 with live backend.

## Phase 7 — Polish (P1)

Tray (status/pause/quit), autostart, persistence of window geometry, toasts,
confirmations, keyboard nav pass.

## Phase 8 — Ship

README (screenshots, install, limitations, repair commands), final `.gitignore`,
LICENSE (MIT), release build via Taskfile, final PRD acceptance audit.

## Risks

| Risk | Mitigation |
|---|---|
| GTK4/WebKitGTK 6.0 missing → build blocked | User installs `libgtk-4-dev libwebkitgtk-6.0-dev`; fallback `-tags gtk3` (must verify it still exists in v3.1) |
| `resolvectl '~.'` routing not sending queries to us | Phase 3 gate verifies real resolution path |
| DNS blackhole after hard kill | state-file repair + documented `resolvectl revert` |
| Recharts live-stream perf | coarser buckets / uPlot fallback (TRD §8 budgets) |
| Blocklist memory >150MB | measure in Phase 2; optimize path documented (SDA ADR-8) |

## Log

- 2026-11-06: Phase 0 complete (template + docs; build on `-tags gtk3` pending GTK4 install).
- 2026-11-06: Phase 1 complete. Fixed two real bugs found by tests: TCP/UDP
  diverging ephemeral ports (`:0`), re-entrant Stop monitor.
- 2026-11-06: Phase 2 complete. Real sources: 165k domains total, 0.8s fetch.
  `ValidDomain` hardened against invalid labels (`-bad.com`).
- 2026-11-06: Phase 3 complete — **live gate passed**: `resolvectl dns enp0s1
  127.0.0.1:5453` + `~.` applied via pkexec; system dig → NXDOMAIN (blocked),
  NOERROR+A (allowed); restore returned `192.168.29.1 2405:201:…` exactly;
  state file removed. Decision: resolved-only (dropped nmcli path).
- 2026-11-06: Phase 4 complete. Two fixture bugs fixed (flush-tail poll race,
  seed order vs cursor); production code untouched by them.
- 2026-11-06: Phase 5 complete. Double-Apply idempotence guard added after
  spotting the self-blackhole bug (re-snapshot would save our own address as
  "original"). Gate verified via temp event readout (removed after): frontend
  service call returned live engine status through generated bindings.
