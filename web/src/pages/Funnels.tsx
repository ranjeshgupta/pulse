import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Funnel, type FunnelResult, type FunnelStep, type PropFilter } from "../api";
import { NUMERIC_OPS, newWhere, type AttrOp, type Where } from "../segmentRules";
import { CompareFields, count, daysAgo, EVENT_RE, KEY_RE, MAX_FILTERS, pct, RangePicker, rangeBounds, rangeProblem, SegmentPicker, ymd, type Range } from "../insights";
import { Empty, ErrorNote, Field, LiveNumber, PageHead, timeAgo, useToast } from "../ui";
import { askDelete, useEditGuard } from "../guard";
import "./funnels.css";

const MIN_STEPS = 2;
const MAX_STEPS = 8;

const stepName = (s: { event: string; label?: string }) => s.label?.trim() || s.event;
const days = (n: number) => `${n} ${n === 1 ? "day" : "days"}`;

// ---- list ----

export function FunnelList() {
  const q = useQuery({ queryKey: ["funnels"], queryFn: () => api.get<{ funnels: Funnel[] }>("/v1/funnels") });
  const fs = q.data?.funnels ?? [];
  return (
    <div className="page">
      <PageHead
        title="Funnels"
        intro="See how many contacts go from one event to the next, and where they drop off."
        actions={<Link className="btn primary" to="/funnels/new">New funnel</Link>}
      />
      <ErrorNote error={q.error} />
      <div className="panel">
        {fs.length === 0 && !q.isLoading ? (
          <Empty title="No funnels yet" action={<Link className="btn primary" to="/funnels/new">Create a funnel</Link>}>
            Follow contacts through the steps that matter, like "Viewed pricing → Signed up → Purchased".
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th>Steps</th><th>Window</th><th>Updated</th></tr></thead>
            <tbody>{fs.map((f) => (
              <tr key={f.id}>
                <td><Link to={`/funnels/${f.id}`}>{f.name}</Link></td>
                <td className="muted" style={{ maxWidth: 520 }}>{f.steps.map(stepName).join(" → ")}</td>
                <td>{days(f.window_days)}</td>
                <td className="muted">{f.updated_at ? timeAgo(f.updated_at) : ""}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// ---- editing model ----

// EditStep is a funnel step in a form-friendly shape; key keeps React rows stable across reorders.
type EditStep = { key: number; event: string; label: string; where: Where[] };

let nextKey = 1;
const editStep = (event = "", label = "", where: Where[] = []): EditStep => ({ key: nextKey++, event, label, where });

function compareText(v: unknown) {
  return Array.isArray(v) ? v.join(", ") : v === undefined || v === null ? "" : String(v);
}

// Same conversion as segment event filters (segmentRules.ts).
function compare(op: AttrOp, value: string): { op: string; value?: unknown } {
  switch (op) {
    case "exists": return { op };
    case "in": return { op, value: value.split(",").map((s) => s.trim()).filter(Boolean) };
    case "gt": case "gte": case "lt": case "lte": return { op, value: Number(value) };
    default: return { op, value };
  }
}

function fromStep(s: FunnelStep): EditStep {
  return editStep(s.event, s.label ?? "", (s.where ?? []).map((f) => ({ prop: f.prop, op: f.op as AttrOp, value: compareText(f.value) })));
}

function toStep(s: EditStep): FunnelStep {
  const out: FunnelStep = { event: s.event.trim() };
  if (s.label.trim()) out.label = s.label.trim();
  const where: PropFilter[] = s.where.map((w) => ({ prop: w.prop.trim(), ...compare(w.op, w.value) }));
  if (where.length) out.where = where;
  return out;
}

function stepProblems(s: EditStep, i: number): string[] {
  const out: string[] = [];
  if (!EVENT_RE.test(s.event.trim())) out.push(`Step ${i + 1} needs an event, e.g. purchase.`);
  if (s.where.length > MAX_FILTERS) out.push(`Use at most ${MAX_FILTERS} property filters per step.`);
  for (const w of s.where) {
    if (!KEY_RE.test(w.prop.trim())) out.push(`Step ${i + 1}: enter a property name, e.g. amount.`);
    else if (NUMERIC_OPS.includes(w.op) && (w.value.trim() === "" || isNaN(Number(w.value)))) out.push(`Step ${i + 1}: "${w.prop}" needs a number.`);
    else if (w.op === "in" && w.value.split(",").every((v) => !v.trim())) out.push(`Step ${i + 1}: list at least one value, separated by commas.`);
  }
  return out;
}

// ---- editor ----

const NEW_STEPS = () => [editStep("page_view"), editStep("signup"), editStep("purchase")];

export function FunnelEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const existing = useQuery({ queryKey: ["funnel", id], queryFn: () => api.get<Funnel>(`/v1/funnels/${id}`), enabled: !isNew });
  const eventNames = useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names"), staleTime: 60_000 });

  const [name, setName] = useState("");
  const [steps, setStepsRaw] = useState<EditStep[]>(NEW_STEPS);
  const [windowDays, setWindowDays] = useState(7);
  const [loaded, setLoaded] = useState(isNew);
  const [range, setRange] = useState<Range>({ preset: 30, from: ymd(daysAgo(30)), to: ymd(new Date()) });
  const [segmentId, setSegmentId] = useState(0);
  const [breakdown, setBreakdown] = useState("");

  useEffect(() => {
    if (existing.data && !loaded) {
      setName(existing.data.name);
      setStepsRaw(existing.data.steps.map(fromStep));
      setWindowDays(existing.data.window_days);
      setLoaded(true);
    }
  }, [existing.data, loaded]);

  const issues = useMemo(() => {
    const out = steps.flatMap(stepProblems);
    if (steps.length < MIN_STEPS) out.push(`A funnel needs at least ${MIN_STEPS} steps.`);
    if (!(Number.isInteger(windowDays) && windowDays >= 1 && windowDays <= 90)) out.push("The conversion window is 1 to 90 days.");
    return [...new Set(out)];
  }, [steps, windowDays]);
  const rangeIssue = rangeProblem(range);

  // Run live: debounce the request body, and only run a funnel the server will accept.
  const runKey = issues.length || rangeIssue ? "" : JSON.stringify({
    steps: steps.map(toStep), window_days: windowDays, ...rangeBounds(range), ...(segmentId ? { segment_id: segmentId } : {}),
    ...(KEY_RE.test(breakdown.trim()) ? { breakdown: { attr: breakdown.trim() } } : {}),
  });
  const [debounced, setDebounced] = useState(runKey);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(runKey), 400);
    return () => clearTimeout(t);
  }, [runKey]);
  const run = useQuery({
    queryKey: ["funnel-run", debounced],
    queryFn: () => api.post<FunnelResult>("/v1/funnels/run", JSON.parse(debounced)),
    enabled: debounced !== "",
    placeholderData: (prev) => prev,
  });

  const body = () => ({ name: name.trim(), steps: steps.map(toStep), window_days: windowDays });
  const save = useMutation({
    mutationFn: () => (isNew ? api.post<{ id: number }>("/v1/funnels", body()) : api.put<{ id: number }>(`/v1/funnels/${id}`, body())),
    onSuccess: (r) => {
      guard.saved();
      qc.invalidateQueries({ queryKey: ["funnels"] });
      qc.invalidateQueries({ queryKey: ["funnel", String(r.id)] });
      toast({ text: isNew ? "Funnel created" : "Funnel saved" });
      if (isNew) nav(`/funnels/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/funnels/${id}`),
    onSuccess: () => { guard.saved(); qc.invalidateQueries({ queryKey: ["funnels"] }); toast({ text: `Deleted ${existing.data?.name || "the funnel"}` }); nav("/funnels"); },
  });
  // The name, steps and window are saved; the range, segment and breakdown only explore. Changing
  // what's saved makes leaving ask first; Ctrl/⌘+S saves.
  const canSave = !!name.trim() && issues.length === 0 && !save.isPending;
  const guard = useEditGuard(canSave ? () => save.mutate() : undefined);
  const edited = <T,>(fn: (v: T) => void) => (v: T) => { guard.markDirty(); fn(v); };
  const setSteps = edited(setStepsRaw);

  if (!loaded) return <div className="page"><ErrorNote error={existing.error} /></div>;

  const setStep = (i: number, s: EditStep) => setSteps(steps.map((x, j) => (j === i ? s : x)));
  const move = (i: number, d: -1 | 1) => {
    const next = [...steps];
    [next[i], next[i + d]] = [next[i + d], next[i]];
    setSteps(next);
  };
  const saveProblem = !name.trim() ? "Name the funnel." : issues[0];

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/funnels">Funnels</Link>}
        title={isNew ? "New funnel" : name || "Funnel"}
        actions={<>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => void askDelete(existing.data?.name || "this funnel", "The saved funnel goes; events and contacts aren't touched. This can't be undone.").then((ok) => ok && remove.mutate())}>Delete</button>}
          <button className="btn primary" disabled={!!saveProblem || save.isPending} title={saveProblem} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : isNew ? "Create funnel" : "Save funnel"}</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error ?? existing.error} />
      <div className="funnel-split">
        <div>
          <div className="grid-2">
            <Field label="Name"><input className="input" value={name} onChange={(e) => edited(setName)(e.target.value)} placeholder="Pricing to purchase" autoFocus={isNew} /></Field>
            <Field label="Conversion window (days)" hint={`Contacts must do every step, in order, within ${days(windowDays || 0)} of step 1. Other events in between are fine.`}>
              <input className="input" type="number" min={1} max={90} value={windowDays || ""} onChange={(e) => edited(setWindowDays)(Number(e.target.value))} />
            </Field>
          </div>

          <div className="panel">
            <div className="panel-head"><h3>Steps</h3><span className="xs muted">{steps.length} of {MAX_STEPS}</span></div>
            <div className="panel-body" style={{ paddingTop: 4, paddingBottom: 4 }}>
              {steps.map((s, i) => (
                <StepEditor
                  key={s.key}
                  step={s}
                  index={i}
                  last={i === steps.length - 1}
                  canRemove={steps.length > MIN_STEPS}
                  onChange={(n) => setStep(i, n)}
                  onMove={(d) => move(i, d)}
                  onRemove={() => setSteps(steps.filter((_, j) => j !== i))}
                />
              ))}
            </div>
          </div>
          <div className="row" style={{ marginTop: 10 }}>
            <button className="btn sm" disabled={steps.length >= MAX_STEPS} onClick={() => setSteps([...steps, editStep()])}>Add step</button>
            {steps.length >= MAX_STEPS && <span className="xs muted">Funnels have up to {MAX_STEPS} steps.</span>}
          </div>
          <datalist id="funnel-events">{eventNames.data?.names.map((n) => <option key={n} value={n} />)}</datalist>
          {issues.length > 0 && <ul className="small muted" style={{ marginTop: 10 }}>{issues.map((p) => <li key={p}>{p}</li>)}</ul>}
        </div>

        <aside className="panel funnel-results">
          <div className="panel-body">
            <div className="funnel-controls">
              <RangePicker range={range} onChange={setRange} label="Step 1 done in" />
              {rangeIssue && <p className="xs" style={{ color: "var(--danger)", margin: 0 }}>{rangeIssue}</p>}
              <SegmentPicker value={segmentId} onChange={setSegmentId} />
              <Field label="Break down by" hint="A profile attribute, e.g. geo_country, plan or stage: the 10 most common values, then everyone else.">
                <input className="input" list="funnel-breakdown-attrs" value={breakdown} placeholder="none" onChange={(e) => setBreakdown(e.target.value)} />
                <datalist id="funnel-breakdown-attrs">{["geo_country", "geo_city", "plan", "stage", "first_utm_source", "last_utm_campaign"].map((a) => <option key={a} value={a} />)}</datalist>
                {breakdown.trim() !== "" && !KEY_RE.test(breakdown.trim()) && <small style={{ color: "var(--danger)" }}>Attribute names use letters, digits and _.</small>}
              </Field>
            </div>
            <Results
              result={run.data}
              ready={debounced !== "" && runKey !== ""}
              stale={debounced !== runKey || run.isFetching}
              error={run.error}
              windowDays={windowDays}
            />
          </div>
        </aside>
      </div>
      {run.data?.breakdown && <BreakdownTable result={run.data} stale={debounced !== runKey || run.isFetching} />}
    </div>
  );
}

function StepEditor({ step, index, last, canRemove, onChange, onMove, onRemove }: {
  step: EditStep; index: number; last: boolean; canRemove: boolean;
  onChange: (s: EditStep) => void; onMove: (d: -1 | 1) => void; onRemove: () => void;
}) {
  const setWhere = (i: number, w: Where) => onChange({ ...step, where: step.where.map((x, j) => (j === i ? w : x)) });
  return (
    <div className="funnel-step">
      <span className="funnel-step-num" aria-hidden>{index + 1}</span>
      <div>
        <div className="rule-row">
          <input className="input event" list="funnel-events" placeholder="event, e.g. signup" value={step.event} onChange={(e) => onChange({ ...step, event: e.target.value })} aria-label={`Step ${index + 1} event`} />
          <input className="input label" placeholder="label (optional)" value={step.label} onChange={(e) => onChange({ ...step, label: e.target.value })} aria-label={`Step ${index + 1} label`} />
          <div className="funnel-step-tools">
            <button className="btn ghost sm" disabled={index === 0} onClick={() => onMove(-1)} aria-label="Move step up" title="Move up">↑</button>
            <button className="btn ghost sm" disabled={last} onClick={() => onMove(1)} aria-label="Move step down" title="Move down">↓</button>
            <button className="btn ghost sm" disabled={!canRemove} onClick={onRemove} title={canRemove ? undefined : `A funnel needs at least ${MIN_STEPS} steps.`}>Remove</button>
          </div>
        </div>
        {step.where.map((w, i) => (
          <div className="rule-row rule-where" key={i}>
            <span className="small">{i === 0 ? "where" : "and"}</span>
            <input className="input" placeholder="property, e.g. amount" value={w.prop} onChange={(e) => setWhere(i, { ...w, prop: e.target.value })} aria-label="Event property" />
            <CompareFields op={w.op} value={w.value} onChange={(op, value) => setWhere(i, { ...w, op, value })} />
            <button className="btn ghost sm" onClick={() => onChange({ ...step, where: step.where.filter((_, j) => j !== i) })} aria-label="Remove filter">Remove</button>
          </div>
        ))}
        {step.where.length < MAX_FILTERS && (
          <button className="btn ghost sm rule-where-add" onClick={() => onChange({ ...step, where: [...step.where, newWhere()] })}>Only count events where…</button>
        )}
      </div>
    </div>
  );
}

// Breakdown: one row per attribute value, with contacts and conversion from step 1 at each step.
function BreakdownTable({ result, stale }: { result: FunnelResult; stale: boolean }) {
  const bd = result.breakdown;
  if (!bd) return null;
  const rows = [...bd.rows.map((r) => ({ label: r.value === "" ? "(not set)" : r.value, reached: r.reached })), ...(bd.other ? [{ label: "Everyone else", reached: bd.other }] : [])];
  return (
    <section className="panel" style={{ marginTop: 20, opacity: stale ? 0.45 : 1 }} aria-busy={stale}>
      <div className="panel-head"><h3>By {bd.attr}</h3><span className="xs muted">Contacts at each step, and the share of the value's step 1</span></div>
      {rows.length === 0 ? <div className="panel-body"><p className="muted small">No contacts did step 1 in this period.</p></div> : (
        <div className="table-scroll">
          <table className="table">
            <caption className="xs muted" style={{ textAlign: "left", padding: "8px 14px" }}>Funnel broken down by the profile attribute {bd.attr}</caption>
            <thead><tr><th scope="col">{bd.attr}</th>{result.steps.map((s, i) => <th key={i} scope="col" className="num">{i + 1}. {stepName(s)}</th>)}<th scope="col" className="num">Conversion</th></tr></thead>
            <tbody>{rows.map((r) => (
              <tr key={r.label}>
                <th scope="row" style={{ fontWeight: 500 }}>{r.label}</th>
                {r.reached.map((n, i) => (
                  <td key={i} className="num">{count(n)}{i > 0 && r.reached[0] > 0 && <small className="faint" style={{ display: "block" }}>{pct(n / r.reached[0])}</small>}</td>
                ))}
                <td className="num"><strong>{r.reached[0] ? pct(r.reached[r.reached.length - 1] / r.reached[0]) : "–"}</strong></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function Results({ result, ready, stale, error, windowDays }: { result?: FunnelResult; ready: boolean; stale: boolean; error: unknown; windowDays: number }) {
  // The query keeps the previous result as placeholder data, so it stays on screen (dimmed) while a
  // new one is counted or while the funnel has problems.
  const r = result;

  if (error && !stale) return <p className="xs" style={{ color: "var(--danger)" }}>{error instanceof Error ? error.message : String(error)}</p>;
  if (!r) return <p className="muted small">{ready ? "Counting…" : "Give every step an event to see results."}</p>;

  const first = r.steps[0];
  const last = r.steps[r.steps.length - 1];
  const busy = stale || !ready;
  if (!first || first.contacts === 0) {
    return (
      <div style={{ opacity: busy ? 0.45 : 1 }} aria-busy={busy}>
        <p className="small" style={{ margin: 0 }}><strong>No contacts did step 1 in this period.</strong></p>
        <p className="small muted" style={{ marginTop: 4 }}>Try a longer date range, or check the event name for "{stepName(first ?? { event: "step 1" })}".</p>
      </div>
    );
  }
  return (
    <div style={{ opacity: busy ? 0.45 : 1 }} aria-busy={busy}>
      <p className="small muted" style={{ margin: "0 0 4px" }}>Overall conversion</p>
      <div className="funnel-headline">{pct(last.of_first)}</div>
      <p className="xs muted" style={{ marginTop: 6 }}>
        <LiveNumber value={last.contacts} /> of <LiveNumber value={first.contacts} /> contacts went from {stepName(first)} to {stepName(last)} within {days(windowDays)}.
      </p>
      <div className="funnel-viz">
        {r.steps.map((s, i) => (
          <div key={i}>
            {i > 0 && (
              <div className={`funnel-drop${s.dropped === 0 ? " none" : ""}`}>
                {s.dropped === 0 ? "No one dropped" : `–${count(s.dropped)} (${pct(1 - s.of_previous)}) dropped`}
              </div>
            )}
            <div className="funnel-bar-head">
              <strong title={s.label ? s.event : undefined}>{i + 1}. {stepName(s)}</strong>
              <span className="num"><LiveNumber value={s.contacts} /></span>
            </div>
            <div className="funnel-bar-track"><div className={`funnel-bar-fill${s.contacts === 0 ? " zero" : ""}`} style={{ width: `${Math.min(1, s.of_first) * 100}%` }} /></div>
            <div className="funnel-bar-meta">{i === 0 ? "100% · everyone who started" : i === 1 ? `${pct(s.of_first)} of step 1` : `${pct(s.of_first)} of step 1 · ${pct(s.of_previous)} of step ${i}`}</div>
          </div>
        ))}
      </div>
      <p className="xs faint" style={{ marginTop: 14 }}>
        {new Date(r.from).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" })} to {new Date(r.to).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" })}
      </p>
    </div>
  );
}
