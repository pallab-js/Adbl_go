import { useEffect, useState } from "react";
import { useApp } from "../state";
import { QueryService, RuleService } from "../../bindings/github.com/ubl/adbl/internal/service";
import type { Query } from "../../bindings/github.com/ubl/adbl/internal/store";

const PAGE = 100;

function fmtTime(ts: number): string {
  const d = new Date(ts * 1000);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export default function LiveLog() {
  const { ticker, tickerPaused, setTickerPaused, toast, refreshRules } = useApp();
  const [filter, setFilter] = useState<"all" | "blocked" | "allowed">("all");
  const [search, setSearch] = useState("");
  const [rows, setRows] = useState<Query[]>([]);
  const [cursor, setCursor] = useState(0);
  const [loading, setLoading] = useState(false);
  const [streaming, setStreaming] = useState(true);

  const load = async (before: number) => {
    setLoading(true);
    try {
      const page = await QueryService.List({
        Blocked: filter === "all" ? null : filter === "blocked",
        Search: search,
        Limit: PAGE,
        BeforeID: before,
      }) ?? [];
      setRows((r) => (before === 0 ? page : [...r, ...page]));
      setCursor(page.length === PAGE ? page[page.length - 1].id : 0);
    } catch (e: any) {
      toast(`Load failed: ${e?.message ?? e}`);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { setRows([]); load(0); }, [filter, search]);

  const allow = async (domain: string) => {
    try {
      await RuleService.Add(true, domain);
      toast(`Allowed ${domain}`, { label: "Undo", run: async () => { await RuleService.Remove(true, domain); refreshRules(); } });
      refreshRules();
    } catch (e: any) { toast(`Failed: ${e?.message ?? e}`); }
  };

  const matches = (q: Query) =>
    (filter === "all" || (filter === "blocked") === q.blocked) &&
    (search === "" || q.domain.includes(search.toLowerCase()));

  // Live rows prepend on top of the fetched page while streaming.
  const live = streaming && !tickerPaused ? [...ticker].reverse().filter(matches) : [];
  const seen = new Set(live.map((q) => `${q.ts}:${q.domain}`));
  const older = rows.filter((q) => !seen.has(`${q.ts}:${q.domain}`));
  const shown = [...live, ...older];

  return (
    <div className="card">
      <div className="row" style={{ marginBottom: 10 }}>
        <div className="row" role="group" aria-label="Filter">
          {(["all", "blocked", "allowed"] as const).map((f) => (
            <button key={f} className={`btn sm ${filter === f ? "primary" : ""}`}
              aria-pressed={filter === f} onClick={() => setFilter(f)}>
              {f[0].toUpperCase() + f.slice(1)}
            </button>
          ))}
        </div>
        <input
          className="input mono" type="search" placeholder="Search domain…"
          value={search} onChange={(e) => setSearch(e.target.value)}
          aria-label="Search domain" style={{ flex: 1, maxWidth: 320 }}
        />
        <span className="spacer" />
        <button className="btn sm" onClick={() => { setStreaming(!streaming); setTickerPaused(!streaming); }}>
          {streaming ? "⏸ Pause stream" : "▶ Resume stream"}
        </button>
      </div>

      <div className="list" style={{ maxHeight: "calc(100vh - 240px)", overflowY: "auto" }}>
        {shown.length === 0 && !loading && (
          <div className="empty">No queries yet — browse to generate traffic.</div>
        )}
        {shown.map((q, i) => (
          <div className="list-row" key={`${q.id}-${q.ts}-${i}`}>
            <span className={`badge ${q.blocked ? "blocked" : "ok"}`}>{q.blocked ? "✕" : "✓"}</span>
            <span className="domain" title={q.domain}>{q.domain}</span>
            <span className="badge">{q.qtype}</span>
            {q.source && <span className="badge" title="Blocklist source">{q.source}</span>}
            <span className="spacer" />
            <span className="dim">{fmtTime(q.ts)}</span>
            <button className="btn sm" onClick={() => allow(q.domain)} title="Allow this domain">
              Allow
            </button>
          </div>
        ))}
        {loading && <div className="empty">Loading…</div>}
      </div>

      {cursor > 0 && (
        <div className="row" style={{ marginTop: 10, justifyContent: "center" }}>
          <button className="btn" disabled={loading} onClick={() => load(cursor)}>Load older</button>
        </div>
      )}
    </div>
  );
}
