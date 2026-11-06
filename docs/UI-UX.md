# UI/UX — adbl

## 1. Principles

1. **Status at a glance** — protection state is always visible (pill + tray icon).
2. **Charts first** — the dashboard answers "what is being blocked?" visually before text.
3. **One-click safety** — every action reversible; destructive actions confirm.
4. **Never break the internet** — DNS changes always show progress → success → repair states.
5. **Accessible by default** — status never color-only, keyboard reachable, reduced-motion respected.

## 2. Information architecture

```
Sidebar
├── Dashboard      KPI cards, live charts, live ticker
├── Live Log       full query stream, filters, per-row actions
├── Blocklists     sources, refresh, coverage bars, domain lookup
├── Rules          manual allow / block entries
├── History        30-day bars, hourly heatmap, trends
└── Settings       upstream, retention, theme, autostart, DNS state
Top bar: status pill (Protected / Paused / Off) + pause menu + window controls
```

## 3. Design tokens

CSS variables, dark default, light theme via `[data-theme="light"]`:

| Token | Dark | Role |
|---|---|---|
| `--bg` / `--bg-elev` | `#0b0f17` / `#131926` | surfaces |
| `--fg` / `--fg-dim` | `#e6eaf2` / `#8b94a7` | text |
| `--accent` | `#4f8cff` | primary actions, "allowed" series |
| `--danger` | `#ff5d73` | blocked series, destructive |
| `--ok` | `#3ecf8e` | protected state |
| `--warn` | `#f5b942` | paused, stale blocklist |
| radius 10px, base font 14px, mono for domains |

Charts use only `--accent` (allowed) and `--danger` (blocked); status adds an
icon/text alongside color (✓ Protected / ⏸ Paused / ✕ Off).

## 4. Dashboard (visualization spec)

| Widget | Chart type | Data | Update |
|---|---|---|---|
| KPI ×4: Queries today, Blocked today, Block %, Sources active | big number + **sparkline** (24h, 15-min buckets) | `stats:tick` | 1s |
| Live throughput | **stacked area**, rolling 60 min, 1-min buckets | `stats:tick` | 1s |
| Blocked share | **donut**, center % | `stats:tick` | 1s |
| Top blocked today | **horizontal bars**, top 10, click → "Allow domain" popover | `TopBlocked` | on tick |
| Live ticker | streaming rows: domain (mono), type badge, ✓/✕, **capped at 200 rows**, pause on hover | `dns:batch` | 500ms |
| Blocklist coverage | per-source **bars** (domain counts) + "updated Xm ago" | `blocklist:status` | on change |

Chart rules: shared tooltip; compact numbers (12.4k); every chart has
empty ("No queries yet — browse to generate traffic"), loading (skeleton) and
error states; server-side downsampling — never ship raw rows to charts;
`prefers-reduced-motion` disables streaming animation.

## 5. Page specs (summary)

- **Live Log** — filter (allowed/blocked/search), cursor pagination, per-row
  "Allow this domain" / "Block this domain", pause/resume stream, export P2.
- **Blocklists** — source cards (name, count, enabled toggle, last refresh),
  Refresh-all button with progress, "check domain" input showing would-block result.
- **Rules** — two lists (allow/block), add form, delete with undo toast.
- **History** — range selector 7/30/90d; grouped daily bars (blocked/total);
  24×7 hourly heatmap of blocked queries; top-blocked trend line.
- **Settings** — DNS state panel (detected backend, current upstream, Apply/Revert
  with confirmation), block mode (P2), retention, theme, autostart.

## 6. Key flows

**First run:** Welcome → detect resolver backend (result card) → explanation of
the single polkit prompt → *Enable protection* → authenticating spinner →
success (status pill turns ✓) or denial (retry + manual instructions).

**Pause:** status pill → 15m / 1h / until resume → pill shows ⏸ with countdown.

**Broken site:** Live Log row or Dashboard top-bar click → "Allow `domain`" →
toast + `rules:changed` → site works without disabling protection.

**DNS repair:** on start, if state file says we own DNS → banner
"Restoring your DNS…" (auto) or "DNS still points at adbl — Repair / Revert"
with exact manual commands shown.

## 7. Accessibility

- All controls keyboard reachable, visible focus rings; charts paired with a
  visually-hidden data table fallback.
- Live ticker is `aria-live="off"` (too chatty); status pill is `aria-live="polite"`.
- Contrast ≥4.5:1 text; status color always paired with icon + label.
- Reduced motion honored; large click targets (≥36px) in rows/buttons.

## 8. Responsive

Desktop-first (min 1024px). Below 900px sidebar collapses to icons; charts stack.
Window default 1200×800, min 900×600, geometry persisted.
