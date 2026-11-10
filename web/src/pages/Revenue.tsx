import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { api, can, type Attribution, type AttributionModel, type Credit, type RevenueReport, type RevenueSettings } from "../api";
import { useMe } from "../auth";
import { count, defaultRange, money, pct, RangePicker, rangeBounds, rangeProblem, useDebounced, type Range } from "../insights";
import {
  averageOrder, creditKey, creditLabel, creditLink, DEFAULT_SETTINGS, DEFAULT_WINDOW, formatConversions, KIND_LABELS, MAX_WINDOW,
  MIN_WINDOW, modelDescription, MODELS, normalizeSettings, sameSettings, settingsProblems, share, sourceMedium, unattributed,
  unattributedOrders, windowProblem,
} from "../revenue";
import { ErrorNote, Field, PageHead, useToast } from "../ui";
import "./revenue.css";

const SETTINGS_KEY = ["revenue-settings"];
const REPORT_KEY = "revenue-report";

export function Revenue() {
  const settingsQ = useQuery({ queryKey: SETTINGS_KEY, queryFn: () => api.get<{ settings: RevenueSettings }>("/v1/revenue/settings") });
  const [range, setRange] = useState<Range>(() => defaultRange(30));
  const [model, setModel] = useState<AttributionModel>("last_touch");
  const [windowDays, setWindowDays] = useState(DEFAULT_WINDOW);

  const rangeIssue = rangeProblem(range);
  const windowIssue = windowProblem(windowDays);
  const runKey = rangeIssue || windowIssue ? "" : JSON.stringify({ ...rangeBounds(range), model, window_days: windowDays });
  const debounced = useDebounced(runKey);
  const run = useQuery({
    queryKey: [REPORT_KEY, debounced],
    queryFn: () => api.post<RevenueReport>("/v1/revenue/report", JSON.parse(debounced)),
    enabled: debounced !== "",
    placeholderData: (prev) => prev,
  });
  const stale = debounced !== runKey || run.isFetching;

  return (
    <div className="page">
      <PageHead
        title="Revenue"
        intro="See how much your purchases bring in, and which broadcasts, journeys and UTM campaigns led to them."
      />

      <section className="panel revenue-controls">
        <div className="panel-body">
          <div className="revenue-controls-row">
            <div>
              <RangePicker range={range} onChange={setRange} label="Purchases made in" />
              {rangeIssue && <p className="xs revenue-problem">{rangeIssue}</p>}
            </div>
            <div>
              <p className="small muted" style={{ margin: "0 0 6px" }} id="revenue-model-label">Attribution model</p>
              <div className="seg-toggle" role="group" aria-labelledby="revenue-model-label">
                {MODELS.map((m) => (
                  <button key={m.model} type="button" aria-pressed={model === m.model} title={modelDescription(m.model, windowDays || DEFAULT_WINDOW)} onClick={() => setModel(m.model)}>
                    {m.label}
                  </button>
                ))}
              </div>
              <p className="xs muted revenue-model-hint">{modelDescription(model, windowIssue ? DEFAULT_WINDOW : windowDays)}</p>
            </div>
            <div className="revenue-window">
              <Field label="Attribution window (days)" hint="How far back before a purchase a touch still counts.">
                <input
                  className="input" type="number" min={MIN_WINDOW} max={MAX_WINDOW} value={windowDays || ""}
                  onChange={(e) => setWindowDays(Number(e.target.value))} aria-invalid={!!windowIssue}
                />
              </Field>
              {windowIssue && <p className="xs revenue-problem">{windowIssue}</p>}
            </div>
          </div>
        </div>
      </section>

      <ErrorNote error={run.error && !stale ? run.error : null} />
      <Report report={run.data} ready={runKey !== ""} stale={stale || runKey === ""} />

      <SettingsPanel query={settingsQ} openByDefault={run.data?.totals.orders === 0} />
    </div>
  );
}

// ---- report ----

function Report({ report: r, ready, stale }: { report?: RevenueReport; ready: boolean; stale: boolean }) {
  if (!r) return <p className="muted small revenue-wait">{ready ? "Adding up revenue…" : "Fix the options above to see revenue."}</p>;
  const cur = r.settings.currency;
  const total = r.totals.revenue;

  if (r.totals.orders === 0) {
    return (
      <div className={`revenue-body${stale ? " stale" : ""}`} aria-busy={stale}>
        <section className="panel">
          <div className="empty">
            <h3>No <code>{r.settings.event}</code> events in this period</h3>
            <p>
              Revenue is counted from <code>{r.settings.event}</code> events, with the amount taken from their <code>{r.settings.amount_prop}</code> property.
              Try a longer date range, or check the revenue settings below if your purchases use another event or property.
            </p>
          </div>
        </section>
      </div>
    );
  }

  return (
    <div className={`revenue-body${stale ? " stale" : ""}`} aria-busy={stale}>
      <div className="figures revenue-figures">
        <div><strong>{money(total, cur)}</strong><span>revenue</span></div>
        <div><strong>{count(r.totals.orders)}</strong><span>orders</span></div>
        <div><strong>{count(r.totals.buyers)}</strong><span>buyers</span></div>
        <div><strong>{money(averageOrder(total, r.totals.orders), cur)}</strong><span>average order</span></div>
        <div>
          <strong>{money(r.messages.revenue, cur)}</strong>
          <span>from messages <small>{pct(share(r.messages.revenue, total))} of revenue</small></span>
        </div>
        <div>
          <strong>{money(r.campaigns.revenue, cur)}</strong>
          <span>from UTM campaigns <small>{pct(share(r.campaigns.revenue, total))} of revenue</small></span>
        </div>
      </div>
      <p className="xs faint revenue-period">
        {fmtDate(r.from)} to {fmtDate(r.to)} · {MODELS.find((m) => m.model === r.model)?.label.toLowerCase()} · {r.window_days} day window
        {!cur && " · amounts are summed as sent, without a currency"}
      </p>

      <CreditTable
        title="Broadcasts and journeys"
        hint={`A touch is the contact opening or clicking one of their messages in the ${days(r.window_days)} before the purchase.`}
        empty="No purchases in this period came after the buyer opened or clicked a broadcast or journey message within the window."
        attribution={r.messages}
        report={r}
        lead={["Broadcast or journey"]}
        cells={(c) => [
          <>
            {creditLink(c) ? <Link to={creditLink(c)!}>{creditLabel(c)}</Link> : creditLabel(c)}
            {" "}<span className={`revenue-kind kind-${c.kind}`}>{KIND_LABELS[c.kind]}</span>
          </>,
        ]}
      />

      <CreditTable
        title="UTM campaigns"
        hint={<>A touch is a page view (or landing page view) from a link tagged with <code>utm_*</code> parameters. The Pulse web SDK records them automatically, plus <code>first_utm_*</code> and <code>last_utm_*</code> attributes on the profile.</>}
        empty="No purchases in this period came after a visit from a UTM-tagged link within the window."
        attribution={r.campaigns}
        report={r}
        lead={["Campaign", "Source / medium"]}
        cells={(c) => [creditLabel(c), <span className="muted">{sourceMedium(c)}</span>]}
      />

      <p className="xs muted revenue-note">
        Messages and UTM campaigns are attributed separately, so the same purchase can count in both tables. Revenue with no touch in the window is unattributed.
        Amounts are not converted between currencies; a non-numeric amount counts as 0.
      </p>
    </div>
  );
}

function CreditTable({ title, hint, empty, attribution: a, report: r, lead, cells }: {
  title: string; hint: ReactNode; empty: string; attribution: Attribution; report: RevenueReport; lead: string[]; cells: (c: Credit) => ReactNode[];
}) {
  const cur = r.settings.currency;
  const total = r.totals.revenue;
  const rest = unattributed(total, a.revenue);
  const restOrders = unattributedOrders(r.totals.orders, a.conversions);
  return (
    <section className="panel section">
      <div className="panel-head"><h3>{title}</h3><span className="xs muted">{money(a.revenue, cur)} attributed</span></div>
      <p className="xs muted revenue-hint">{hint}</p>
      {a.credits.length === 0 ? (
        <div className="panel-body"><p className="small muted" style={{ margin: 0 }}>{empty}</p></div>
      ) : (
        <div className="table-scroll">
          <table className="table revenue-table">
            <caption className="revenue-sr">{title}: revenue attributed with the {r.model.replace("_", " ")} model</caption>
            <thead><tr>
              {lead.map((h) => <th key={h} scope="col">{h}</th>)}
              <th scope="col" className="num">Revenue</th>
              <th scope="col" className="revenue-share-col">Share of revenue</th>
              <th scope="col" className="num">Conversions</th>
            </tr></thead>
            <tbody>{a.credits.map((c) => {
              const [first, ...others] = cells(c);
              return (
                <tr key={creditKey(c)}>
                  <th scope="row" className="revenue-name">{first}</th>
                  {others.map((x, i) => <td key={i}>{x}</td>)}
                  <td className="num">{money(c.revenue, cur)}</td>
                  <td><ShareCell value={share(c.revenue, total)} /></td>
                  <td className="num">{formatConversions(c.conversions)}</td>
                </tr>
              );
            })}</tbody>
            <tfoot>
              <tr className="revenue-unattributed">
                <th scope="row" colSpan={lead.length}>Unattributed</th>
                <td className="num">{money(rest, cur)}</td>
                <td><ShareCell value={share(rest, total)} muted /></td>
                <td className="num">{formatConversions(restOrders)}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      )}
    </section>
  );
}

function ShareCell({ value, muted }: { value: number; muted?: boolean }) {
  return (
    <div className="revenue-share">
      <span className="num">{pct(value)}</span>
      <div className="bar-track" aria-hidden="true"><div className={`bar-fill${muted ? " muted" : ""}`} style={{ width: `${value * 100}%` }} /></div>
    </div>
  );
}

const days = (n: number) => `${n} ${n === 1 ? "day" : "days"}`;
const fmtDate = (iso: string) => new Date(iso).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" });

// ---- settings ----

function SettingsPanel({ query, openByDefault }: { query: { data?: { settings: RevenueSettings }; error: unknown; isLoading: boolean }; openByDefault: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const canEdit = can(useMe().data?.permissions, "settings", "edit");
  const saved = query.data?.settings;
  const [form, setForm] = useState<RevenueSettings>(DEFAULT_SETTINGS);
  const [open, setOpen] = useState(false);
  const [touched, setTouched] = useState(false);
  const eventNames = useQuery({
    queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names"), staleTime: 60_000, enabled: open && canEdit,
  });

  useEffect(() => { if (saved && !touched) setForm(saved); }, [saved, touched]);
  useEffect(() => { if (openByDefault) setOpen(true); }, [openByDefault]);

  const save = useMutation({
    mutationFn: () => api.put<{ settings: RevenueSettings }>("/v1/revenue/settings", normalizeSettings(form)),
    onSuccess: (r) => {
      qc.setQueryData(SETTINGS_KEY, r);
      setForm(r.settings);
      setTouched(false);
      qc.invalidateQueries({ queryKey: [REPORT_KEY] });
      toast({ text: "Revenue settings saved" });
    },
  });

  const set = (patch: Partial<RevenueSettings>) => { setTouched(true); setForm({ ...form, ...patch }); };
  const problems = settingsProblems(form);
  const unchanged = saved ? sameSettings(form, saved) : true;
  const s = saved ?? DEFAULT_SETTINGS;

  return (
    <section className="panel section revenue-settings">
      <div className="panel-head">
        <div>
          <h3>Revenue settings</h3>
          <p className="xs muted" style={{ margin: "4px 0 0" }}>
            Revenue is the sum of the <code>{s.amount_prop}</code> property on <code>{s.event}</code> events{s.currency ? `, in ${s.currency}` : ""}.
          </p>
        </div>
        <button type="button" className="btn sm" aria-expanded={open} aria-controls="revenue-settings-body" onClick={() => setOpen(!open)}>
          {open ? "Hide" : canEdit ? "Change" : "Show"}
        </button>
      </div>
      {open && (
        <div className="panel-body" id="revenue-settings-body">
          <ErrorNote error={query.error ?? save.error} />
          <fieldset disabled={!canEdit || query.isLoading} className="revenue-settings-form">
            <div className="revenue-settings-grid">
              <Field label="Revenue event" hint="Each of these events is one order.">
                <input className="input" list="revenue-events" value={form.event} onChange={(e) => set({ event: e.target.value })} placeholder="purchase" />
              </Field>
              <Field label="Amount property" hint="The event property holding the order amount. Non-numeric values count as 0.">
                <input className="input" value={form.amount_prop} onChange={(e) => set({ amount_prop: e.target.value })} placeholder="amount" />
              </Field>
              <Field label="Currency code" hint="Used to format amounts, e.g. INR. Amounts are not converted, so send them all in one currency.">
                <input className="input" value={form.currency} maxLength={3} onChange={(e) => set({ currency: e.target.value })} placeholder="INR" style={{ textTransform: "uppercase" }} />
              </Field>
            </div>
            <datalist id="revenue-events">{eventNames.data?.names.map((n) => <option key={n} value={n} />)}</datalist>
          </fieldset>
          {canEdit ? (
            <>
              {touched && problems.length > 0 && <ul className="small revenue-problem" style={{ margin: "10px 0 0" }}>{problems.map((p) => <li key={p}>{p}</li>)}</ul>}
              <div className="row" style={{ marginTop: 14 }}>
                <button
                  type="button" className="btn primary" disabled={problems.length > 0 || unchanged || save.isPending}
                  title={problems[0] ?? (unchanged ? "Nothing to save." : undefined)} onClick={() => save.mutate()}
                >
                  {save.isPending ? "Saving…" : "Save settings"}
                </button>
                {!unchanged && saved && (
                  <button type="button" className="btn ghost" onClick={() => { setForm(saved); setTouched(false); save.reset(); }}>Discard changes</button>
                )}
              </div>
            </>
          ) : (
            <p className="xs muted" style={{ marginTop: 10 }}>Only people who can edit workspace settings can change these.</p>
          )}
        </div>
      )}
    </section>
  );
}
