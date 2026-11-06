// Shared frontend state: one subscription per event, context for all pages.
import { createContext, useContext, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { DNSService, SystemService, RuleService, BlocklistService } from "../bindings/github.com/ubl/adbl/internal/service";
import type { DNSStatus, Rules, StatsTick } from "../bindings/github.com/ubl/adbl/internal/service";
import type { Query } from "../bindings/github.com/ubl/adbl/internal/store";
import type { Status as SourceStatus } from "../bindings/github.com/ubl/adbl/internal/blocklist";
import type { Status as SystemStatus } from "../bindings/github.com/ubl/adbl/internal/systemdns";

const TICKER_CAP = 200; // UI-UX §4: never let the ticker grow unbounded

export interface Toast { id: number; text: string; action?: { label: string; run: () => void } }

interface AppState {
  dns: DNSStatus | null;
  system: SystemStatus | null;
  stats: StatsTick | null;
  ticker: Query[];
  tickerPaused: boolean;
  setTickerPaused: (p: boolean) => void;
  sources: SourceStatus[] | null;
  rules: Rules | null;
  toasts: Toast[];
  toast: (text: string, action?: Toast["action"]) => void;
  refreshRules: () => void;
  refreshSources: () => void;
}

const Ctx = createContext<AppState | null>(null);
export const useApp = () => {
  const v = useContext(Ctx);
  if (!v) throw new Error("useApp outside provider");
  return v;
};

// on binds a Wails event to a state setter for the component lifetime.
function on<T>(name: string, set: (v: T) => void) {
  return Events.On(name as any, (ev: any) => { if (ev?.data !== undefined) set(ev.data as T); });
}

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [dns, setDns] = useState<DNSStatus | null>(null);
  const [system, setSystem] = useState<SystemStatus | null>(null);
  const [stats, setStats] = useState<StatsTick | null>(null);
  const [ticker, setTicker] = useState<Query[]>([]);
  const [tickerPaused, setTickerPaused] = useState(false);
  const [sources, setSources] = useState<SourceStatus[] | null>(null);
  const [rules, setRules] = useState<Rules | null>(null);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const pausedRef = useRef(tickerPaused);
  pausedRef.current = tickerPaused;
  const toastId = useRef(0);

  const toast: AppState["toast"] = (text, action) => {
    const id = ++toastId.current;
    setToasts((t) => [...t, { id, text, action }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), action ? 8000 : 4000);
  };

  const refreshRules = () => { RuleService.List().then(setRules).catch(() => {}); };
  const refreshSources = () => { BlocklistService.Sources().then(setSources).catch(() => {}); };

  useEffect(() => {
    // Initial fetch (events then keep these fresh).
    DNSService.Status().then(setDns).catch(() => {});
    SystemService.Status().then(setSystem).catch(() => {});
    refreshRules();
    refreshSources();

    const offs = [
      on<DNSStatus>("dns:state", setDns),
      on<SystemStatus>("system:state", setSystem),
      on<StatsTick>("stats:tick", setStats),
      on<SourceStatus[]>("blocklist:status", setSources),
      on<Rules>("rules:changed", (r) => { setRules(r); }),
      on<Query[]>("dns:batch", (batch) => {
        if (pausedRef.current) return;
        setTicker((t) => [...t, ...batch].slice(-TICKER_CAP));
      }),
    ];
    return () => offs.forEach((f) => f());
  }, []);

  return (
    <Ctx.Provider value={{
      dns, system, stats, ticker, tickerPaused, setTickerPaused,
      sources, rules, toasts, toast, refreshRules, refreshSources,
    }}>
      {children}
    </Ctx.Provider>
  );
}
