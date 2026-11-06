import { useState } from "react";
import { useApp } from "../state";
import { RuleService } from "../../bindings/github.com/ubl/adbl/internal/service";

function RuleList({ title, domains, allow, onRemove }: {
  title: string; domains: string[] | null | undefined; allow: boolean;
  onRemove: (d: string) => void;
}) {
  return (
    <div className="card">
      <h2>{title}</h2>
      <div className="list">
        {(domains ?? []).length === 0 && (
          <div className="empty">{allow ? "No allow rules — everything on the blocklists applies." : "No manual block rules."}</div>
        )}
        {(domains ?? []).map((d) => (
          <div className="list-row" key={d}>
            <span className={`badge ${allow ? "ok" : "blocked"}`}>{allow ? "✓ allow" : "✕ block"}</span>
            <span className="domain">{d}</span>
            <span className="spacer" />
            <button className="btn sm danger" onClick={() => onRemove(d)} aria-label={`Remove rule for ${d}`}>
              Remove
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}

export default function Rules() {
  const { rules, refreshRules, toast } = useApp();
  const [domain, setDomain] = useState("");
  const [kind, setKind] = useState<"allow" | "block">("allow");
  const [busy, setBusy] = useState(false);

  const add = async () => {
    const d = domain.trim().toLowerCase();
    if (!d) return;
    setBusy(true);
    try {
      await RuleService.Add(kind === "allow", d);
      setDomain("");
      refreshRules();
      toast(`${kind === "allow" ? "Allowed" : "Blocked"} ${d}`, {
        label: "Undo",
        run: async () => { await RuleService.Remove(kind === "allow", d); refreshRules(); },
      });
    } catch (e: any) {
      toast(`Invalid domain: ${e?.message ?? e}`);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (allow: boolean, d: string) => {
    try {
      await RuleService.Remove(allow, d);
      refreshRules();
      toast(`Removed ${d}`, {
        label: "Undo",
        run: async () => { await RuleService.Add(allow, d); refreshRules(); },
      });
    } catch (e: any) { toast(`Failed: ${e?.message ?? e}`); }
  };

  return (
    <div className="grid" style={{ gap: 14 }}>
      <div className="card">
        <h2>Add rule</h2>
        <div className="row">
          <select className="select" value={kind} onChange={(e) => setKind(e.target.value as any)} aria-label="Rule type">
            <option value="allow">Allow (never block)</option>
            <option value="block">Block (always block)</option>
          </select>
          <input
            className="input mono" style={{ flex: 1, maxWidth: 380 }}
            placeholder="example.com" value={domain}
            onChange={(e) => setDomain(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && add()}
            aria-label="Domain"
          />
          <button className="btn primary" disabled={busy || !domain.trim()} onClick={add}>Add</button>
        </div>
        <div className="kpi-sub" style={{ marginTop: 8 }}>
          Rules survive blocklist refreshes. Allow always wins over any blocklist.
        </div>
      </div>

      <div className="grid two">
        <RuleList title="Allowlist" domains={rules?.allow} allow onRemove={(d) => remove(true, d)} />
        <RuleList title="Blocklist (manual)" domains={rules?.block} allow={false} onRemove={(d) => remove(false, d)} />
      </div>
    </div>
  );
}
