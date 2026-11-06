import { useEffect, useState } from "react";
import {
  ResponsiveContainer, AreaChart, Area, PieChart, Pie, Cell,
  BarChart, Bar, XAxis, YAxis, Tooltip, CartesianGrid, LineChart, Line,
} from "recharts";
import { useApp } from "../state";
import { StatsService, RuleService } from "../../bindings/github.com/ubl/adbl/internal/service";
import type { Bucket, TopEntry } from "../../bindings/github.com/ubl/adbl/internal/store";

const ACCENT = "#4f8cff";
const DANGER = "#ff5d73";

// compact numbers per UI-UX §4 ("12.4k")
export function compact(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, "") + "k";
  return String(n);
}

function hhmm(ts: number): string {
  const d = new Date(ts * 1000);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

// Fill bucket gaps so the area chart shows zeros, not jumps.
function fillBuckets(rows: Bucket[] | null | undefined, from: number, to: number, step: number): Bucket[] {
  const byTS = new Map((rows ?? []).map((b) => [b.ts, b]));
  const out: Bucket[] = [];
  for (let t = from; t <= to; t += step) {
    out.push(byTS.get(t) ?? { ts: t, total: 0, blocked: 0 });
  }
  return out;
}

function Tip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="chart-tip">
      <div className="label">{hhmm(label)}</div>
      {payload.map((p: any) => (
        <div key={p.dataKey}>
          <span className="legend-dot" style={{ background: p.color || p.fill }} />
          {p.name}: <strong>{p.value}</strong>
        </div>
      ))}
    </div>
  );
}

function Kpi({ label, value, sub, spark }: {
  label: string; value: string; sub: string; spark: Bucket[] | null;
}) {
  return (
    <div className="card">
      <h2>{label}</h2>
      <div className="kpi-row">
        <div>
          <div className="kpi-value">{value}</div>
          <div className="kpi-sub">{sub}</div>
        </div>
        <div style={{ width: 90, height: 34 }} aria-hidden>
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={spark ?? []}>
              <Line dataKey="total" stroke={ACCENT} strokeWidth={1.5} dot={false} isAnimationActive={false} />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </div>
      <table className="sr-only">
        <caption>{label}, last 24 hours</caption>
        <tbody>{(spark ?? []).filter((b) => b.total > 0).map((b) => (
          <tr key={b.ts}><td>{hhmm(b.ts)}</td><td>{b.total}</td></tr>
        ))}</tbody>
      </table>
    </div>
  );
}

export default function Dashboard() {
  const { stats, ticker, tickerPaused, setTickerPaused, sources, rules, toast, refreshRules } = useApp();
  const [top, setTop] = useState<TopEntry[] | null>(null);
  const [spark, setSpark] = useState<Bucket[] | null>(null);
  const [allowBusy, setAllowBusy] = useState("");

  // Top-10 + 24h sparkline: refresh every 10s (aggregates only, no raw rows).
  useEffect(() => {
    let dead = false;
    const load = () => {
      StatsService.TopBlocked("today", 10).then((r) => !dead && setTop(r)).catch(() => {});
      StatsService.Series("24h", "15m").then((r) => !dead && setSpark(r)).catch(() => {});
    };
    load();
    const t = setInterval(load, 10_000);
    return () => { dead = true; clearInterval(t); };
  }, []);

  const today = stats?.today;
  const pct = today && today.total > 0 ? Math.round((today.blocked / today.total) * 100) : 0;
  const activeSources = (sources ?? []).filter((s) => s.enabled).length;

  // Rolling 60 min, 1-min buckets (stats:tick series60).
  const nowSec = Math.floor(Date.now() / 1000);
  const areaData = fillBuckets(stats?.series60, nowSec - 3540, nowSec - 60, 60)
    .map((b) => ({ ...b, allowed: b.total - b.blocked }));

  const allowDomain = async (domain: string) => {
    setAllowBusy(domain);
    try {
      await RuleService.Add(true, domain);
      toast(`Allowed ${domain}`, { label: "Undo", run: async () => { await RuleService.Remove(true, domain); refreshRules(); } });
      setTop((t) => t?.filter((e) => e.domain !== domain) ?? null);
      refreshRules();
    } catch (e: any) {
      toast(`Failed: ${e?.message ?? e}`);
    } finally {
      setAllowBusy("");
    }
  };

  if (!stats) return <div className="empty">Loading statistics…</div>;

  return (
    <div className="grid" style={{ gap: 14 }}>
      <div className="grid kpi">
        <Kpi label="Queries today" value={compact(today?.total ?? 0)} sub="since local midnight" spark={spark} />
        <Kpi label="Blocked today" value={compact(today?.blocked ?? 0)} sub="ads & trackers" spark={(spark ?? []).map((b) => ({ ...b, total: b.blocked }))} />
        <Kpi label="Block rate" value={`${pct}%`} sub="of today's queries" spark={(spark ?? []).map((b) => ({ ...b, total: b.total > 0 ? Math.round((b.blocked / b.total) * 100) : 0 }))} />
        <Kpi label="Sources active" value={`${activeSources}`} sub={`of ${(sources ?? []).length} configured`} spark={null} />
      </div>

      <div className="grid dash">
        <div className="card">
          <h2>Live throughput — last 60 min</h2>
          <div style={{ height: 220 }}>
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={areaData} margin={{ top: 6, right: 6, left: -18, bottom: 0 }}>
                <CartesianGrid stroke="var(--border)" vertical={false} />
                <XAxis dataKey="ts" tickFormatter={hhmm} stroke="var(--fg-dim)" tick={{ fontSize: 11 }} minTickGap={40} />
                <YAxis stroke="var(--fg-dim)" tick={{ fontSize: 11 }} allowDecimals={false} />
                <Tooltip content={<Tip />} />
                <Area type="monotone" dataKey="blocked" name="Blocked" stackId="1" stroke={DANGER} fill={DANGER} isAnimationActive={false} />
                <Area type="monotone" dataKey="allowed" name="Allowed" stackId="1" stroke={ACCENT} fill={ACCENT} fillOpacity={0.55} isAnimationActive={false} />
              </AreaChart>
            </ResponsiveContainer>
          </div>
          <table className="sr-only">
            <caption>Queries per minute, last 60 minutes</caption>
            <tbody>{areaData.filter((b) => b.total > 0).map((b) => (
              <tr key={b.ts}><td>{hhmm(b.ts)}</td><td>{b.total - b.blocked} allowed</td><td>{b.blocked} blocked</td></tr>
            ))}</tbody>
          </table>
        </div>

        <div className="card">
          <h2>Blocked share — today</h2>
          <div style={{ height: 220, position: "relative" }}>
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie
                  data={[{ name: "Blocked", value: today?.blocked ?? 0 }, { name: "Allowed", value: Math.max(0, (today?.total ?? 0) - (today?.blocked ?? 0)) }]}
                  dataKey="value" innerRadius="62%" outerRadius="90%" startAngle={90} endAngle={-270}
                  stroke="none" isAnimationActive={false}
                >
                  <Cell fill={DANGER} />
                  <Cell fill={ACCENT} fillOpacity={0.4} />
                </Pie>
                <Tooltip content={<Tip />} />
              </PieChart>
            </ResponsiveContainer>
            <div style={{
              position: "absolute", inset: 0, display: "flex", flexDirection: "column",
              alignItems: "center", justifyContent: "center", pointerEvents: "none",
            }}>
              <strong style={{ fontSize: 30 }}>{pct}%</strong>
              <span className="kpi-sub">blocked</span>
            </div>
          </div>
        </div>
      </div>

      <div className="grid dash">
        <div className="card">
          <h2>Top blocked — today</h2>
          {!top || top.length === 0 ? (
            <div className="empty">No blocked queries yet — browse to generate traffic.</div>
          ) : (
            <div style={{ height: Math.max(180, top.length * 32) }}>
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={top} layout="vertical" margin={{ top: 0, right: 60, left: 4, bottom: 0 }}>
                  <CartesianGrid stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--fg-dim)" tick={{ fontSize: 11 }} allowDecimals={false} />
                  <YAxis type="category" dataKey="domain" width={170} stroke="var(--fg-dim)" tick={{ fontSize: 11, fontFamily: "monospace" }} />
                  <Tooltip content={<Tip />} cursor={{ fill: "var(--bg-elev2)" }} />
                  <Bar dataKey="count" name="Blocked" fill={DANGER} isAnimationActive={false} label={{ position: "right", fontSize: 11, fill: "var(--fg-dim)" }}>
                    {top.map((e) => (
                      <Cell
                        key={e.domain}
                        style={{ cursor: "pointer" }}
                        onClick={(ev: any) => { ev?.stopPropagation?.(); allowDomain(e.domain); }}
                      />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}
          <div className="kpi-sub" style={{ marginTop: 6 }}>Click a bar to allow that domain.</div>
          {allowBusy && <div className="kpi-sub">Allowing {allowBusy}…</div>}
        </div>

        <div className="card" style={{ display: "flex", flexDirection: "column" }}>
          <h2>
            Live ticker
            <span className="spacer" />
            <button className="btn sm" onClick={() => setTickerPaused(!tickerPaused)}>
              {tickerPaused ? "▶ Resume" : "⏸ Pause"}
            </button>
          </h2>
          <div className="ticker-scroll list" aria-live="off">
            {ticker.length === 0 && <div className="empty">Waiting for queries…</div>}
            {[...ticker].reverse().map((q, i) => (
              <div className="list-row" key={`${q.ts}-${i}`}>
                <span className={`badge ${q.blocked ? "blocked" : "ok"}`}>{q.blocked ? "✕" : "✓"}</span>
                <span className="domain" title={q.domain}>{q.domain}</span>
                <span className="spacer" />
                <span className="badge">{q.qtype}</span>
                <span className="dim">{hhmm(q.ts)}</span>
              </div>
            ))}
          </div>
          <div className="kpi-sub" style={{ marginTop: 6 }}>
            {rules && (rules.allow?.length ?? 0) + (rules.block?.length ?? 0) > 0
              ? `${rules.allow?.length ?? 0} allow · ${rules.block?.length ?? 0} block rules`
              : "No manual rules yet"}
          </div>
        </div>
      </div>
    </div>
  );
}
