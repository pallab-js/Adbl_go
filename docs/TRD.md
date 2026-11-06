# TRD — Technical Requirements & Design

## 1. Stack (pinned)

| Layer | Choice | Notes |
|---|---|---|
| Framework | Wails **v3.1.0** (exact pin) | Go 1.26+, GTK4/WebKitGTK 6.0 on Linux |
| Language | Go 1.26.8 | host toolchain confirmed |
| DNS | `github.com/miekg/dns` | de-facto standard |
| DB | `modernc.org/sqlite` | pure Go — tests/CI stay CGo-free |
| Frontend | React 19 + TypeScript + Vite | Wails `react` template |
| Charts | **Recharts** | declarative; fallback uPlot if live-stream perf insufficient |
| Styling | plain CSS + design tokens | no Tailwind; no state lib (context + events) |

## 2. Repository layout

```
main.go                  app bootstrap, service/event registration, lifecycle + restore hooks
greetservice.go          (template leftover — removed in Phase 5)
internal/dnsengine/      server, pipeline, forwarder
internal/blocklist/      parsers, sources, manager, atomic store
internal/systemdns/      detect (resolvectl|nmcli|resolv.conf), apply, revert, state file
internal/store/          schema, batch writer, retention, rollups, stat queries
internal/service/        Wails services: dns, stats, queries, blocklists, rules, system
internal/config/         ~/.config/adbl/config.json
frontend/                React app
docs/                    PRD SDA TRD IMPLEMENTATION UI-UX
build/                   Wails build/packaging (generated)
```

Paths: config `~/.config/adbl/config.json` · data `~/.local/share/adbl/{adbl.db,state.json}`
· cache `~/.cache/adbl/`.

## 3. DNS engine

- Listeners: UDP + TCP on `127.0.0.1:5453`.
- Decision: allowlist → suffix-walk blocklist (`atomic.Pointer[map[string]struct{}]`,
  walk `a.b.c.com`, `b.c.com`, …) → forward.
- Forward: UDP to upstream, retry TCP on truncation, 3s timeout; upstream list =
  originals captured at apply time, fallback `1.1.1.1`, `9.9.9.9`.
- Block response: NXDOMAIN (default; mode setting is P2).
- Every query → `LogChan` (buffered) → batch writer.

## 4. Blocklist manager

- Formats parsed: hosts (`0.0.0.0 domain`), plain domain lines, `||domain^`.
- Default sources: StevenBlack base (hosts), hagezi light (hosts-form variant).
- Download: HTTPS, 50MB cap, per-source timeout, parse errors reported not fatal.
- Dedup → build map → **atomic swap** (no restart, race-safe under `-race`).
- Refresh: on start + every 24h; per-source enable/disable; manual refresh.
- Manual rules: allowlist takes precedence, stored in config, survive refresh.

## 5. systemdns

- **Scope: systemd-resolved only.** `resolvectl` accepts `ip:port` server specs
  (systemd ≥246; target 259+), so the unprivileged `:5453` design holds.
  Machines without resolved are reported unsupported (NM-without-resolved is a
  documented limitation — NM's `ipv4.dns` is IP/port-53 only).
- Detect: `LookPath(resolvectl && pkexec)` + `resolvectl status`.
- Default link: parse `ip route show default` → `dev <name>` (validated
  `^[a-zA-Z0-9_.-]{1,15}$`, no shell ever involved).
- Apply: read current servers/domains → **persist state file** → `pkexec
  resolvectl dns <link> 127.0.0.1:5453` → `pkexec resolvectl domain <link> '~.'`.
  On domain failure: automatic rollback to saved values (state kept only if
  rollback itself fails).
- Restore: re-apply saved servers/domains (single empty arg clears), then
  delete state file. State file with invalid link is rejected outright.
- Startup repair: state file present ⇒ engine rebinds `state.Listen` immediately;
  manual fallback documented (`resolvectl revert`).
- All elevation via `exec.Command("pkexec", …)` fixed argv; `run`/`elevate`/
  `look` are injectable so tests never touch the real system.

## 6. SQLite schema

```sql
queries(id INTEGER PRIMARY KEY, ts INTEGER, domain TEXT, qtype TEXT,
        blocked INTEGER, source TEXT)          -- raw, pruned ≥7d (config)
CREATE INDEX idx_queries_ts ON queries(ts);
CREATE INDEX idx_queries_blocked_ts ON queries(blocked, ts);
daily_stats(date TEXT, total INTEGER, blocked INTEGER, PRIMARY KEY(date))  -- 180d
meta(key TEXT PRIMARY KEY, val TEXT)           -- schema version, last refresh
```

Writes: channel → flusher (every 500ms or 200 rows) → single connection,
`PRAGMA journal_mode=WAL`. Retention prune daily.

## 7. Services (Wails bindings) & events

| Service | Methods | Events out |
|---|---|---|
| `DNSService` | `Status()`, `Start()`, `Stop()`, `Pause(minutes int)` | `dns:state` |
| `StatsService` | `Summary()`, `Series(range string)`, `TopBlocked(n int, range string)` | `stats:tick` (1s) |
| `QueryService` | `Recent(limit int)`, `Page(filter QueryFilter, cursor int)` | `dns:batch` (500ms) |
| `BlocklistService` | `Sources()`, `Refresh(id string)`, `Toggle(id string, on bool)` | `blocklist:status` |
| `RuleService` | `Add(rule)`, `Delete(domain string)`, `List()` | `rules:changed` |
| `SystemService` | `Detect()`, `Apply()`, `Revert()`, `ElevationStatus()` | `system:state` |

Events are registered with `application.RegisterEvent[T]` in `main.go` for typed
TS bindings. **Charts consume `stats:tick` aggregates, never raw query rows.**

## 8. Performance budgets

| Metric | Budget |
|---|---|
| Blocklist lookup | <1µs (suffix walk) |
| Added DNS latency | <5ms p95 local |
| UI event stream | ≥500 queries/s sustained (batched) |
| Startup → interactive dashboard | <2s |
| Memory with ~400k domains | <150MB (measure in Phase 2; optimize path: reversed-label sharing) |

## 9. Testing strategy

- **Unit:** parsers (3 formats + fixtures), block-decision matrix (exact, subdomain,
  wildcard-ish suffix, allowlist precedence), state save/restore, retention, rollup SQL.
- **Integration:** engine on ephemeral port + fake upstream (assert NXDOMAIN vs forward).
- **systemdns:** injected fake exec; assert exact argv, no shell.
- **Race:** `go test -race ./...` over swap + batcher.
- **Manual gate (Phase 3):** real apply → resolve → quit → reverted on this machine.
- **UI:** manual walkthrough vs PRD acceptance criteria (no e2e framework in v1).

## 10. Security checklist

- [x] Loopback-only bind (never `0.0.0.0`)
- [x] Fixed-argv elevation, validated interface names
- [x] Parameterized SQL only
- [x] HTTPS + size cap for blocklist downloads
- [x] No shell interpolation anywhere in elevation path
