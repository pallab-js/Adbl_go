import { useState } from "react";
import { AppProvider, useApp } from "./state";
import { DNSService, SystemService } from "../bindings/github.com/ubl/adbl/internal/service";
import Dashboard from "./pages/Dashboard";
import LiveLog from "./pages/LiveLog";
import Blocklists from "./pages/Blocklists";
import Rules from "./pages/Rules";
import History from "./pages/History";
import Settings from "./pages/Settings";

const PAGES = [
  { id: "dashboard", label: "Dashboard", ico: "▦" },
  { id: "log", label: "Live Log", ico: "≡" },
  { id: "blocklists", label: "Blocklists", ico: "⛨" },
  { id: "rules", label: "Rules", ico: "✓" },
  { id: "history", label: "History", ico: "▤" },
  { id: "settings", label: "Settings", ico: "⚙" },
] as const;

type PageId = (typeof PAGES)[number]["id"];

function fmtRemaining(unix: number): string {
  const s = Math.max(0, unix - Math.floor(Date.now() / 1000));
  if (s >= 3600) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  if (s >= 60) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${s}s`;
}

function StatusPill() {
  const { dns, system, toast } = useApp();
  const [menu, setMenu] = useState(false);
  const [busy, setBusy] = useState(false);

  const paused = (dns?.pausedUntil ?? 0) > Math.floor(Date.now() / 1000);
  const applied = system?.applied ?? false;
  const state = paused ? "paused" : applied ? "on" : "off";
  const label = paused
    ? `⏸ Paused ${fmtRemaining(dns!.pausedUntil)}`
    : applied
      ? "✓ Protected"
      : dns?.running
        ? "○ Engine only"
        : "✕ Off";

  const pause = async (min: number) => {
    setMenu(false);
    setBusy(true);
    try { await DNSService.Pause(min); } finally { setBusy(false); }
  };

  const toggleProtect = async () => {
    setMenu(false);
    setBusy(true);
    try {
      if (applied) {
        await SystemService.Disable();
        toast("Protection off — system DNS restored");
      } else {
        await SystemService.Enable(); // one polkit prompt
        toast("Protection on — ads and trackers blocked system-wide");
      }
    } catch (e: any) {
      toast(`DNS change failed: ${e?.message ?? e}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <button
        className={`pill ${state}`}
        aria-live="polite"
        disabled={busy}
        onClick={() => setMenu((m) => !m)}
      >
        {busy ? "…" : label} ▾
      </button>
      {menu && (
        <div className="menu" role="menu">
          {paused
            ? <button role="menuitem" onClick={() => pause(0)}>▶ Resume now</button>
            : <>
                <button role="menuitem" disabled={busy || !dns?.running} onClick={() => pause(15)}>⏸ Pause 15 min</button>
                <button role="menuitem" disabled={busy || !dns?.running} onClick={() => pause(60)}>⏸ Pause 1 hour</button>
              </>}
          <button role="menuitem" disabled={busy} onClick={toggleProtect}>
            {applied ? "✕ Turn protection off" : "⛨ Turn protection on"}
          </button>
        </div>
      )}
    </>
  );
}

function Toasts() {
  const { toasts } = useApp();
  if (toasts.length === 0) return null;
  return (
    <div className="toasts">
      {toasts.map((t) => (
        <div key={t.id} className="toast" role="status">
          {t.text}
          {t.action && (
            <button onClick={t.action.run}>{t.action.label}</button>
          )}
        </div>
      ))}
    </div>
  );
}

function Shell() {
  const [page, setPage] = useState<PageId>("dashboard");
  const { system } = useApp();

  // First-run / repair banner (UI-UX §6).
  const banner = system && !system.supported ? (
    <div className="banner">
      <div className="txt">
        <strong>DNS backend not detected</strong>
        {system.message ?? "systemd-resolved (resolvectl) and pkexec are required."}
      </div>
    </div>
  ) : system && system.applied && !system.currentServers?.some((s) => s.includes("127.0.0.1")) ? (
    <div className="banner">
      <div className="txt">
        <strong>DNS still points at adbl</strong>
        Manual repair: <code>resolvectl revert</code>
      </div>
    </div>
  ) : null;

  return (
    <div className="shell">
      <nav className="sidebar" aria-label="Main">
        <div className="logo">adbl</div>
        {PAGES.map((p) => (
          <button
            key={p.id}
            className={`nav-btn ${page === p.id ? "active" : ""}`}
            aria-current={page === p.id ? "page" : undefined}
            onClick={() => setPage(p.id)}
          >
            <span className="ico" aria-hidden>{p.ico}</span>
            <span className="label">{p.label}</span>
          </button>
        ))}
      </nav>
      <div className="main">
        <header className="topbar">
          <h1>{PAGES.find((p) => p.id === page)?.label}</h1>
          <StatusPill />
        </header>
        <main className="page">
          {banner}
          {page === "dashboard" && <Dashboard />}
          {page === "log" && <LiveLog />}
          {page === "blocklists" && <Blocklists />}
          {page === "rules" && <Rules />}
          {page === "history" && <History />}
          {page === "settings" && <Settings />}
        </main>
      </div>
      <Toasts />
    </div>
  );
}

export default function App() {
  return (
    <AppProvider>
      <Shell />
    </AppProvider>
  );
}
