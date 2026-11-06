import { useState } from "react";
import { useApp } from "../state";
import { BlocklistService } from "../../bindings/github.com/ubl/adbl/internal/service";

function ago(iso: string): string {
  if (!iso || iso.startsWith("0001")) return "never";
  const s = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export default function Blocklists() {
  const { sources, refreshSources, toast } = useApp();
  const [refreshing, setRefreshing] = useState(false);
  const [check, setCheck] = useState("");
  const [checkResult, setCheckResult] = useState<{ blocked: boolean; source: string } | null>(null);
  const [checkErr, setCheckErr] = useState("");

  const maxDomains = Math.max(1, ...(sources ?? []).map((s) => s.domains));

  const refresh = async () => {
    setRefreshing(true);
    try {
      await BlocklistService.Refresh();
      refreshSources();
      toast("Blocklists refreshed");
    } catch (e: any) {
      refreshSources();
      toast(`Refresh finished with errors: ${e?.message ?? e}`);
    } finally {
      setRefreshing(false);
    }
  };

  const toggle = async (id: string, on: boolean) => {
    try {
      await BlocklistService.Toggle(id, on);
      refreshSources();
    } catch (e: any) { toast(`Failed: ${e?.message ?? e}`); }
  };

  const doCheck = async () => {
    setCheckErr("");
    setCheckResult(null);
    try {
      const r = await BlocklistService.Check(check.trim());
      setCheckResult({ blocked: r.blocked, source: r.source });
    } catch (e: any) { setCheckErr(String(e?.message ?? e)); }
  };

  return (
    <div className="grid" style={{ gap: 14 }}>
      <div className="card">
        <div className="row">
          <h2 style={{ margin: 0 }}>Sources</h2>
          <span className="spacer" />
          <button className="btn primary" disabled={refreshing} onClick={refresh}>
            {refreshing ? "Refreshing…" : "↻ Refresh all"}
          </button>
        </div>
        <div className="list" style={{ marginTop: 8 }}>
          {(sources ?? []).length === 0 && <div className="empty">No sources loaded yet…</div>}
          {(sources ?? []).map((s) => (
            <div className="list-row" key={s.id}>
              <button
                className={`toggle ${s.enabled ? "on" : ""}`}
                role="switch" aria-checked={s.enabled}
                aria-label={`Enable ${s.name}`}
                onClick={() => toggle(s.id, !s.enabled)}
              />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 600 }}>{s.name}</div>
                <div className="kpi-sub">
                  {s.domains.toLocaleString()} domains · updated {ago(s.lastFetch)}
                  {s.lastError && <span style={{ color: "var(--danger)" }}> · error: {s.lastError}</span>}
                </div>
                {/* coverage bar */}
                <div style={{ height: 5, background: "var(--bg-elev2)", borderRadius: 3, marginTop: 5 }}>
                  <div style={{
                    height: "100%", borderRadius: 3, width: `${(s.domains / maxDomains) * 100}%`,
                    background: s.enabled ? "var(--accent)" : "var(--border)",
                  }} />
                </div>
              </div>
              <span className={`badge ${s.enabled ? "ok" : ""}`}>{s.enabled ? "active" : "off"}</span>
            </div>
          ))}
        </div>
      </div>

      <div className="card">
        <h2>Check a domain</h2>
        <div className="row">
          <input
            className="input mono" style={{ flex: 1, maxWidth: 380 }}
            placeholder="ads.example.com" value={check}
            onChange={(e) => setCheck(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && doCheck()}
            aria-label="Domain to check"
          />
          <button className="btn" disabled={!check.trim()} onClick={doCheck}>Check</button>
        </div>
        {checkErr && <div className="error">{checkErr}</div>}
        {checkResult && (
          <div className="list-row" style={{ marginTop: 8 }}>
            <span className={`badge ${checkResult.blocked ? "blocked" : "ok"}`}>
              {checkResult.blocked ? "✕ would be blocked" : "✓ would be allowed"}
            </span>
            <span className="domain">{check.trim().toLowerCase()}</span>
            {checkResult.source && <span className="badge">{checkResult.source}</span>}
          </div>
        )}
      </div>
    </div>
  );
}
