import { useState } from "react";
import { useApp } from "../state";
import { SystemService, DNSService } from "../../bindings/github.com/ubl/adbl/internal/service";

export default function Settings() {
  const { system, dns, toast } = useApp();
  const [busy, setBusy] = useState<"on" | "off" | "revert" | null>(null);
  const [theme, setThemeState] = useState<"dark" | "light">(
    document.documentElement.dataset.theme === "light" ? "light" : "dark",
  );

  const setTheme = (t: "dark" | "light") => {
    document.documentElement.dataset.theme = t;
    localStorage.setItem("adbl.theme", t);
    setThemeState(t);
  };

  const apply = async () => {
    setBusy("on");
    try {
      await SystemService.Enable(); // one polkit prompt
      toast("Protection on — system DNS now points at adbl");
    } catch (e: any) {
      toast(`Enable failed: ${e?.message ?? e}`);
    } finally { setBusy(null); }
  };

  const revert = async () => {
    if (!confirm("Turn protection off? Your original DNS servers will be restored (one polkit prompt).")) return;
    setBusy("revert");
    try {
      await SystemService.Disable();
      toast("Protection off — original DNS restored");
    } catch (e: any) {
      toast(`Restore failed: ${e?.message ?? e} — run manually: resolvectl revert`);
    } finally { setBusy(null); }
  };

  return (
    <div className="grid" style={{ gap: 14, maxWidth: 760 }}>
      <div className="card">
        <h2>DNS protection</h2>
        {!system ? <div className="empty">Detecting…</div> : (
          <div className="list">
            <div className="list-row">
              <span className="dim" style={{ width: 170 }}>Backend</span>
              <span>{system.supported ? "systemd-resolved" : "not detected"}</span>
              {!system.supported && system.message && <span className="error">{system.message}</span>}
            </div>
            <div className="list-row">
              <span className="dim" style={{ width: 170 }}>Interface</span>
              <span>{system.link ?? "—"}</span>
            </div>
            <div className="list-row">
              <span className="dim" style={{ width: 170 }}>Current DNS</span>
              <span className="domain">{system.currentServers?.join("  ") || "—"}</span>
            </div>
            <div className="list-row">
              <span className="dim" style={{ width: 170 }}>Sinkhole</span>
              <span className="domain">{system.listen ?? dns?.addr ?? "127.0.0.1:5453"}</span>
            </div>
            <div className="list-row">
              <span className="dim" style={{ width: 170 }}>Engine</span>
              <span>{dns?.running ? `running on ${dns.addr}` : "stopped"}</span>
              {dns && dns.pausedUntil > Math.floor(Date.now() / 1000) && (
                <span className="badge">paused</span>
              )}
            </div>
            <div className="row" style={{ marginTop: 12 }}>
              {system.applied ? (
                <button className="btn danger" disabled={busy !== null} onClick={revert}>
                  {busy === "revert" ? "Restoring…" : "✕ Turn protection off"}
                </button>
              ) : (
                <button className="btn primary" disabled={busy !== null || !system.supported} onClick={apply}>
                  {busy === "on" ? "Authenticating…" : "⛨ Turn protection on"}
                </button>
              )}
              {dns?.running && !system.applied && (
                <button className="btn" disabled={busy !== null}
                  onClick={async () => { await DNSService.Stop(); toast("Engine stopped"); }}>
                  Stop engine
                </button>
              )}
            </div>
            <div className="kpi-sub" style={{ marginTop: 8 }}>
              Turning protection on shows a single polkit password prompt. Your original
              DNS is saved before any change and restored on exit — if the app is ever
              force-killed, run <code>resolvectl revert</code> or start adbl again.
            </div>
          </div>
        )}
      </div>

      <div className="card">
        <h2>Appearance</h2>
        <div className="row">
          <span className="dim">Theme</span>
          <div className="row" role="group" aria-label="Theme">
            <button className={`btn sm ${theme === "dark" ? "primary" : ""}`}
              aria-pressed={theme === "dark"} onClick={() => setTheme("dark")}>
              Dark
            </button>
            <button className={`btn sm ${theme === "light" ? "primary" : ""}`}
              aria-pressed={theme === "light"} onClick={() => setTheme("light")}>
              Light
            </button>
          </div>
        </div>
      </div>

      <div className="card">
        <h2>Data</h2>
        <div className="list">
          <div className="list-row">
            <span className="dim" style={{ width: 170 }}>Query log</span>
            <span>raw rows kept 7 days, daily rollups 180 days</span>
          </div>
          <div className="list-row">
            <span className="dim" style={{ width: 170 }}>Location</span>
            <span className="domain">~/.local/share/adbl/adbl.db</span>
          </div>
          <div className="list-row">
            <span className="dim" style={{ width: 170 }}>Network</span>
            <span>only blocklist downloads leave this machine</span>
          </div>
        </div>
      </div>
    </div>
  );
}
