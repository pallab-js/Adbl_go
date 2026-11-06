import { Fragment, useEffect, useMemo, useState } from "react";
import {
  ResponsiveContainer, BarChart, Bar, XAxis, YAxis, Tooltip, CartesianGrid, Legend,
} from "recharts";
import { StatsService } from "../../bindings/github.com/ubl/adbl/internal/service";
import type { Bucket, TopEntry } from "../../bindings/github.com/ubl/adbl/internal/store";

const ACCENT = "#4f8cff";
const DANGER = "#ff5d73";

function dayLabel(ts: number): string {
  return new Date(ts * 1000).toLocaleDateString([], { month: "short", day: "numeric" });
}

function Tip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="chart-tip">
      <div className="label">{label}</div>
      {payload.map((p: any) => (
        <div key={p.dataKey}><span className="legend-dot" style={{ background: p.color || p.fill }} />{p.name}: <strong>{p.value}</strong></div>
      ))}
    </div>
  );
}

const DAYS = [7, 30, 90] as const;

export default function History() {
  const [days, setDays] = useState<number>(30);
  const [daily, setDaily] = useState<Bucket[] | null>(null);
  const [hourly, setHourly] = useState<Bucket[] | null>(null);
  const [top, setTop] = useState<TopEntry[] | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    let dead = false;
    setErr("");
    Promise.all([
      StatsService.Daily(days),
      StatsService.Series(`${days}d`, "1h"),
      StatsService.TopBlocked(`${days}d`, 10),
    ]).then(([d, h, t]) => {
      if (dead) return;
      setDaily(d); setHourly(h); setTop(t);
    }).catch((e) => !dead && setErr(String(e?.message ?? e)));
    return () => { dead = true; };
  }, [days]);

  // 24×7 heatmap: weekday × hour-of-day, blocked queries only (UI-UX §5).
  const heat = useMemo(() => {
    const grid = Array.from({ length: 7 }, () => new Array<number>(24).fill(0));
    for (const b of hourly ?? []) {
      const d = new Date(b.ts * 1000);
      grid[(d.getDay() + 6) % 7][d.getHours()] += b.blocked; // Mon-first
    }
    return grid;
  }, [hourly]);
  const heatMax = Math.max(1, ...heat.flat());

  const chartData = (daily ?? []).map((b) => ({
    day: dayLabel(b.ts),
    blocked: b.blocked,
    allowed: b.total - b.blocked,
  }));

  const weekdays = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

  return (
    <div className="grid" style={{ gap: 14 }}>
      <div className="row">
        <div className="row" role="group" aria-label="Date range">
          {DAYS.map((d) => (
            <button key={d} className={`btn sm ${days === d ? "primary" : ""}`}
              aria-pressed={days === d} onClick={() => setDays(d)}>
              {d} days
            </button>
          ))}
        </div>
        <span className="spacer" />
        {err && <span className="error">{err}</span>}
      </div>

      <div className="card">
        <h2>Daily queries — last {days} days</h2>
        {!daily ? <div className="empty">Loading…</div> : chartData.length === 0 ? (
          <div className="empty">No history yet.</div>
        ) : (
          <div style={{ height: 260 }}>
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={chartData} margin={{ top: 6, right: 6, left: -18, bottom: 0 }}>
                <CartesianGrid stroke="var(--border)" vertical={false} />
                <XAxis dataKey="day" stroke="var(--fg-dim)" tick={{ fontSize: 11 }} minTickGap={30} />
                <YAxis stroke="var(--fg-dim)" tick={{ fontSize: 11 }} allowDecimals={false} />
                <Tooltip content={<Tip />} cursor={{ fill: "var(--bg-elev2)" }} />
                <Legend wrapperStyle={{ fontSize: 12 }} />
                <Bar dataKey="blocked" name="Blocked" stackId="a" fill={DANGER} isAnimationActive={false} />
                <Bar dataKey="allowed" name="Allowed" stackId="a" fill={ACCENT} fillOpacity={0.55} isAnimationActive={false} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        )}
        <table className="sr-only">
          <caption>Daily query totals</caption>
          <tbody>{chartData.map((d) => (
            <tr key={d.day}><td>{d.day}</td><td>{d.blocked} blocked</td><td>{d.allowed} allowed</td></tr>
          ))}</tbody>
        </table>
      </div>

      <div className="grid two">
        <div className="card">
          <h2>Blocked by hour — weekday × hour</h2>
          <div className="heatmap" role="img"
            aria-label={`Heatmap of blocked queries by weekday and hour, max ${heatMax} per cell`}>
            <span />
            {Array.from({ length: 24 }, (_, h) => (
              <span key={h} style={{ textAlign: "center" }}>{h % 3 === 0 ? h : ""}</span>
            ))}
            {heat.map((row, wi) => (
              <Fragment key={weekdays[wi]}>
                <span style={{ alignSelf: "center" }}>{weekdays[wi]}</span>
                {row.map((v, hi) => (
                  <div key={`${wi}-${hi}`} className="cell"
                    title={`${weekdays[wi]} ${hi}:00 — ${v} blocked`}
                    style={{
                      background: v === 0 ? undefined
                        : `color-mix(in srgb, var(--danger) ${Math.round(15 + (v / heatMax) * 85)}%, var(--bg-elev2))`,
                    }} />
                ))}
              </Fragment>
            ))}
          </div>
          <div className="kpi-sub" style={{ marginTop: 8 }}>Max {heatMax} blocked in an hour · hover a cell for detail.</div>
        </div>

        <div className="card">
          <h2>Top blocked — last {days} days</h2>
          {!top || top.length === 0 ? (
            <div className="empty">Nothing blocked in this range.</div>
          ) : (
            <div className="list">
              {top.map((e, i) => (
                <div className="list-row" key={e.domain}>
                  <span className="dim">{i + 1}.</span>
                  <span className="domain">{e.domain}</span>
                  <span className="spacer" />
                  <strong>{e.count.toLocaleString()}</strong>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
