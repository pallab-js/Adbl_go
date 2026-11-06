# adbl — Local Ad Blocker

System-wide ad/tracker blocking for your Linux desktop, built with **Go + Wails v3**
and a **React dashboard**. adbl runs a local DNS sinkhole on `127.0.0.1:5453`,
points system DNS at it (one polkit prompt), and blocks ads/trackers for every
app — browsers, native apps, and OS telemetry alike — without browser extensions
or HTTPS interception.

## What it does

- **Blocks** ads & trackers at the DNS layer (NXDOMAIN) for all processes, not just the browser.
- **Monitors** a live query log with per-query block/allow status and source attribution.
- **Tracks** history: raw query log (7 days) + daily rollups (180 days) in a local SQLite DB.
- **Visualizes** the dashboard: live throughput area chart, blocked-share donut, top-blocked bar charts, daily/30-day/90-day trends, weekday×hour heatmap.
- **Controls**: one-click protect/pause, manual allow/block rules that survive blocklist refreshes, per-source toggles, on-demand blocklist refresh.
- **Never breaks your internet**: previous DNS is saved before any change and restored on exit, on next start, and is documented for manual recovery (`resolvectl revert`).

## Requirements

- Linux with **systemd-resolved** (`resolvectl`) and **pkexec**
- Go 1.26+ and Node.js (for the frontend build)
- GTK4 + WebKitGTK 6.0 dev headers (default Wails v3 stack)
  ```bash
  sudo apt install libgtk-4-dev libwebkitgtk-6.0-dev
  ```

## Build & run

```bash
# frontend (node_modules already present; run once after clone)
cd frontend && npm install && cd ..

# development (hot reload)
wails3 dev

# production build (needs GTK4 dev headers; see above)
wails3 build
```

Output lands in `bin/`. First run: turn protection on in the app → a single polkit
password prompt → system DNS points at adbl. Quit the app → original DNS restored.

## Safety

The engine binds **only** `127.0.0.1:5453` — never an open resolver. System DNS is
changed only through a fixed `pkexec resolvectl` argv (no shell string), and the
previous config is persisted to `~/.local/share/adbl/state.json` before any change.
If adbl is ever force-killed while protection is on, start adbl again or run
`resolvectl revert` manually.

Known limitation: browsers using DNS-over-HTTPS by default bypass the sinkhole
(DoH support is a planned follow-up, not v1).

## Project layout

```
docs/          PRD, SDA, TRD, implementation plan, UI/UX specs
frontend/      React 19 + TypeScript + Vite dashboard (Recharts charts)
internal/
  blocklist/   source parsers (hosts / plain / ||domain^) + atomic block+allow store
  config/      ~/.config/adbl/config.json persistence
  dnsengine/   loopback DNS server + upstream forwarder
  service/     Wails services (DNS, system, stats, queries, blocklists, rules)
  store/       SQLite WAL DB: batched writes, rollups, retention, queries
  systemdns/   resolvectl apply/revert via pkexec, state-based repair
main.go        Wails bootstrap + event wiring + restore-on-exit
events.go      live log batcher + 1s stats ticker
build/         Wails packaging config (Taskfile, linux/darwin/windows icons)
```

## Docs

`docs/PRD.md`, `docs/SDA.md`, `docs/TRD.md`, `docs/IMPLEMENTATION.md`,
`docs/UI-UX.md` — written up front as the build blueprint (Phase 0).

## License

[MIT](LICENSE).
