// Custom reports: pick a source (events, messages, contacts), a metric, a grouping and filters, and see
// the result live as a figure, bars, a line or a table. The editing model lives in reportBuilder.ts.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, CHANNELS, CHANNEL_NAMES, type ReportGroup, type ReportMetric, type ReportResult, type ReportSpec, type SavedReport } from "../api";
import { useCan } from "../auth";
import { daysAgo, defaultRange, FilterRows, PROP_OPS, RangePicker, rangeBounds, rangeProblem, SegmentPicker, useDebounced, ymd, type Preset, type Range } from "../insights";
import {
  campaignKind, CONTACT_OPS, describe, EVENT_RE, formatValue, fromSpec, groupLabel, groupOption, GROUPS, isTimeGroup, KIND_NAMES,
  labelIndices, lineGeometry, MAX_EVENTS, MESSAGE_KINDS, metricLabel, metricOption, METRICS, newDraft, PRESETS, rangeDaysOf, reportRows,
  shortTimeLabel, SOURCES, specProblems, toSpec, withGroup, withMetric, withSource, type Chart, type Draft, type MessageSource,
} from "../reportBuilder";
import { csvFileName, downloadCsv, toCsv } from "../csv";
import { BUILTIN_ATTRS } from "../segmentRules";
import { askDelete, useEditGuard } from "../guard";
import { Empty, ErrorNote, Field, PageHead, timeAgo, useToast } from "../ui";
import "./reports-builder.css";

type Saved = SavedReport<ReportSpec>;

// ---- list ----

export function ReportList() {
  const q = useQuery({ queryKey: ["reports"], queryFn: () => api.get<{ reports: Saved[] }>("/v1/reports") });
  // Cohort reports are saved alongside; they open on their own page.
  const rs = (q.data?.reports ?? []).filter((r) => r.kind !== "cohort");
  return (
    <div className="page">
      <PageHead
        title="Reports"
        intro="Count events, messages and contacts your way: pick what to measure, how to group it and how to chart it."
        actions={<Link className="btn primary" to="/reports/new">New report</Link>}
      />
      <ErrorNote error={q.error} />
      <div className="panel">
        {rs.length === 0 && !q.isLoading ? (
          <Empty title="No reports yet" action={<Link className="btn primary" to="/reports/new">Create a report</Link>}>
            Build reports like "Revenue by product", "Purchases per day" or "Email outcomes by campaign".
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th>Shows</th><th>Updated</th></tr></thead>
            <tbody>{rs.map((r) => (
              <tr key={r.id}>
                <td><Link to={`/reports/${r.id}`}>{r.name}</Link></td>
                <td className="muted" style={{ maxWidth: 560 }}>{describe(r.spec)}</td>
                <td className="muted">{r.updated_at ? timeAgo(r.updated_at) : ""}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// ---- editor ----

const PRESET_DAYS: Preset[] = [7, 30, 90];

/** rangeFor opens a saved report on its default range (a preset when it is one). */
function rangeFor(days?: number): Range {
  const n = days ?? 30;
  if ((PRESET_DAYS as number[]).includes(n)) return defaultRange(n as Preset);
  return { preset: "custom", from: ymd(daysAgo(n)), to: ymd(new Date()) };
}

type Run = { spec: ReportSpec; result: ReportResult };

export function ReportEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const canEdit = useCan("reports", "edit");
  const existing = useQuery({ queryKey: ["report", id], queryFn: () => api.get<Saved>(`/v1/reports/${id}`), enabled: !isNew });
  const eventNames = useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names"), staleTime: 60_000 });

  const [name, setName] = useState("");
  const [draft, setDraft] = useState<Draft>(() => newDraft("events"));
  const [range, setRange] = useState<Range>(() => defaultRange(30));
  const [loaded, setLoaded] = useState(isNew);

  // Unsaved changes (for people who can save): the name or the report's definition differs from
  // what was loaded or last saved. The date range alone is exploring, not editing.
  const base = useRef<{ name: string; draft: Draft } | null>(isNew ? { name, draft } : null);
  useEffect(() => {
    if (existing.data && !loaded) {
      const d = fromSpec(existing.data.spec);
      base.current = { name: existing.data.name, draft: d };
      setName(existing.data.name);
      setDraft(d);
      setRange(rangeFor(existing.data.spec.range_days));
      setLoaded(true);
    }
  }, [existing.data, loaded]);
  const unsaved = canEdit && loaded && !!base.current && (name !== base.current.name || draft !== base.current.draft);
  const guard = useEditGuard(() => saveRef.current?.());
  const saveRef = useRef<(() => void) | null>(null);
  const { markDirty, saved: markSaved } = guard;
  useEffect(() => { if (unsaved) markDirty(); else markSaved(); }, [unsaved, markDirty, markSaved]);

  // The saved default range: the picked preset; custom dates keep the previous default.
  const rangeDays = range.preset === "custom" ? draft.range_days : range.preset;
  const spec = useMemo(() => toSpec({ ...draft, range_days: rangeDays }), [draft, rangeDays]);
  const issues = useMemo(() => specProblems(draft, rangeDaysOf(range)), [draft, range]);
  const rangeIssue = rangeProblem(range);

  // Run live: debounce the request body, and only run specs the server will accept.
  const runKey = !loaded || issues.length || rangeIssue ? "" : JSON.stringify({ spec, ...rangeBounds(range) });
  const debounced = useDebounced(runKey, 400);
  const run = useQuery({
    queryKey: ["report-run", debounced],
    queryFn: async (): Promise<Run> => {
      const body = JSON.parse(debounced) as { spec: ReportSpec };
      return { spec: body.spec, result: await api.post<ReportResult>("/v1/reports/run", body) };
    },
    enabled: debounced !== "",
    placeholderData: (prev) => prev,
  });

  const body = () => ({ name: name.trim(), spec });
  const save = useMutation({
    mutationFn: () => (isNew ? api.post<{ id: number }>("/v1/reports", body()) : api.put<unknown>(`/v1/reports/${id}`, body()).then(() => ({ id: Number(id) }))),
    onSuccess: (r) => {
      base.current = { name, draft };
      markSaved();
      qc.invalidateQueries({ queryKey: ["reports"] });
      qc.invalidateQueries({ queryKey: ["report", String(r.id)] });
      toast({ text: isNew ? "Report created" : "Report saved" });
      if (isNew) nav(`/reports/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/reports/${id}`),
    onSuccess: () => { base.current = null; markSaved(); qc.invalidateQueries({ queryKey: ["reports"] }); toast({ text: "Report deleted" }); nav("/reports"); },
  });

  if (!loaded) return <div className="page"><ErrorNote error={existing.error} /></div>;

  const saveProblem = !name.trim() ? "Name the report." : issues[0];
  saveRef.current = canEdit && !saveProblem ? () => { if (!save.isPending) save.mutate(); } : null;
  const startFrom = (p: (typeof PRESETS)[number]) => {
    setDraft(fromSpec(p.spec));
    if (!name.trim() || PRESETS.some((x) => x.name === name)) setName(p.name);
    setRange(rangeFor(p.spec.range_days));
  };

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/reports">Reports</Link>}
        title={isNew ? "New report" : name || "Report"}
        intro={canEdit ? undefined : "You can run reports. Saving them needs edit access to reports."}
        actions={canEdit && <>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => void askDelete(existing.data?.name ? `the report ${existing.data.name}` : "this report").then((ok) => ok && remove.mutate())}>Delete</button>}
          <button className="btn primary" disabled={!!saveProblem || save.isPending} title={saveProblem} onClick={() => save.mutate()}>{isNew ? "Create report" : "Save report"}</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error ?? existing.error} />

      {isNew && (
        <div className="rb-presets" role="group" aria-label="Start from">
          <span className="small muted">Start from</span>
          {PRESETS.map((p) => <button key={p.id} type="button" className="btn sm" title={p.hint} onClick={() => startFrom(p)}>{p.name}</button>)}
        </div>
      )}

      <div className="rb-split">
        <div>
          {canEdit && <Field label="Name"><input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Revenue by product" /></Field>}
          <SpecEditor draft={draft} onChange={setDraft} eventNames={eventNames.data?.names ?? []} />
          {issues.length > 0 && <ul className="small muted rb-issues">{issues.map((p) => <li key={p}>{p}</li>)}</ul>}
        </div>

        <section className="panel rb-results" aria-label="Results">
          <div className="panel-body">
            <div className="rb-controls">
              <RangePicker range={range} onChange={setRange} label={draft.source === "contacts" ? "First seen in" : "Date range"} />
              {rangeIssue && <p className="xs rb-danger">{rangeIssue}</p>}
              {draft.source === "contacts" && !isTimeGroup(draft.group.type) && (
                <p className="xs muted rb-note">Counts all contacts: the date range only applies when grouped by day, week or month (by when contacts were first seen).</p>
              )}
              {canEdit && range.preset === "custom" && (
                <p className="xs muted rb-note">Custom dates aren't saved: the report opens on the last {draft.range_days ?? 30} days.</p>
              )}
              <SegmentPicker value={draft.segment_id} onChange={(segment_id) => setDraft({ ...draft, segment_id })} />
            </div>
            <Results
              name={name.trim() || "Report"}
              run={run.data}
              ready={debounced !== "" && runKey !== ""}
              stale={debounced !== runKey || run.isFetching}
              error={run.error}
            />
          </div>
        </section>
      </div>
    </div>
  );
}

// ---- spec form ----

function SpecEditor({ draft, onChange, eventNames }: { draft: Draft; onChange: (d: Draft) => void; eventNames: string[] }) {
  const m = metricOption(draft.source, draft.metric.type);
  const g = groupOption(draft.source, draft.group.type);
  const timed = isTimeGroup(draft.group.type);
  const keyList = (attr: boolean) => (attr ? "rb-attrs" : undefined);
  return (
    <>
      <div className="panel rb-panel">
        <div className="panel-head"><h3>What to count</h3></div>
        <div className="panel-body">
          <p className="small muted rb-label" id="rb-source">Source</p>
          <div className="seg-toggle" role="group" aria-labelledby="rb-source">
            {SOURCES.map((s) => (
              <button key={s.type} type="button" aria-pressed={draft.source === s.type} onClick={() => onChange(withSource(draft, s.type))}>{s.label}</button>
            ))}
          </div>
          <p className="xs muted rb-hint">{SOURCES.find((s) => s.type === draft.source)?.hint}</p>

          <div className="grid-2 rb-fields">
            <Field label="Measure">
              <select className="select" value={draft.metric.type} onChange={(e) => onChange(withMetric(draft, e.target.value as ReportMetric))}>
                {METRICS[draft.source].map((o) => <option key={o.type} value={o.type}>{o.label}</option>)}
              </select>
            </Field>
            {m?.needsKey ? (
              <Field label={draft.source === "contacts" ? "Attribute" : "Property"}>
                <input className="input" list={keyList(draft.source === "contacts")} placeholder={m.keyHint} value={draft.metric.key} onChange={(e) => onChange(withMetric(draft, draft.metric.type, e.target.value))} />
              </Field>
            ) : <div />}
            <Field label="Group by">
              <select className="select" value={draft.group.type} onChange={(e) => onChange(withGroup(draft, e.target.value as ReportGroup))}>
                {GROUPS[draft.source].map((o) => <option key={o.type} value={o.type}>{o.label}</option>)}
              </select>
            </Field>
            {g?.needsKey ? (
              <Field label={draft.group.type === "attr" ? "Attribute" : "Property"}>
                <input className="input" list={keyList(draft.group.type === "attr")} placeholder={g.keyHint} value={draft.group.key} onChange={(e) => onChange(withGroup(draft, draft.group.type, e.target.value))} />
              </Field>
            ) : <div />}
          </div>

          <p className="small muted rb-label" id="rb-chart">Show as</p>
          {draft.group.type === "none" ? (
            <p className="xs muted rb-hint">One total. Group by something to chart it.</p>
          ) : (
            <>
              <div className="seg-toggle sm" role="group" aria-labelledby="rb-chart">
                {(["table", "bars", "line"] as Chart[]).map((c) => (
                  <button key={c} type="button" aria-pressed={draft.chart === c} disabled={c === "line" && !timed}
                    title={c === "line" && !timed ? "Line charts need grouping by day, week or month." : undefined}
                    onClick={() => onChange({ ...draft, chart: c })}>
                    {c === "table" ? "Table" : c === "bars" ? "Bars" : "Line"}
                  </button>
                ))}
              </div>
              {!timed && <p className="xs muted rb-hint">Group by day, week or month for a line chart.</p>}
            </>
          )}
        </div>
      </div>

      <div className="panel rb-panel">
        <div className="panel-head"><h3>Filters</h3></div>
        <div className="panel-body">
          {draft.source === "events" && <>
            <EventPicker events={draft.events} onChange={(events) => onChange({ ...draft, events })} />
            <div className="rb-where">
              <FilterRows where={draft.where} onChange={(where) => onChange({ ...draft, where })} ops={PROP_OPS} />
            </div>
          </>}
          {draft.source === "messages" && <MessageFilters draft={draft} onChange={onChange} />}
          {draft.source === "contacts" && (
            <div className="rb-where">
              <FilterRows where={draft.where} onChange={(where) => onChange({ ...draft, where })} ops={CONTACT_OPS} noun="attribute" addLabel="Only count contacts where…" />
            </div>
          )}
        </div>
      </div>

      <datalist id="rb-events">{eventNames.map((n) => <option key={n} value={n} />)}</datalist>
      <datalist id="rb-attrs">{BUILTIN_ATTRS.map((a) => <option key={a.key} value={a.key}>{a.label}</option>)}</datalist>
    </>
  );
}

function EventPicker({ events, onChange }: { events: string[]; onChange: (e: string[]) => void }) {
  const [text, setText] = useState("");
  const name = text.trim();
  const valid = EVENT_RE.test(name) && !events.includes(name) && events.length < MAX_EVENTS;
  const add = () => { if (valid) { onChange([...events, name]); setText(""); } };
  return (
    <div>
      <p className="small muted rb-label">{events.length ? "Only these events" : "All events"}</p>
      {events.length > 0 && (
        <ul className="rb-tags">
          {events.map((e) => (
            <li key={e}>{e}<button type="button" aria-label={`Remove ${e}`} onClick={() => onChange(events.filter((x) => x !== e))}>×</button></li>
          ))}
        </ul>
      )}
      {events.length < MAX_EVENTS ? (
        <div className="row rb-add">
          <input className="input" list="rb-events" placeholder="event, e.g. purchase" value={text} aria-label="Add an event"
            onChange={(e) => setText(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }} />
          <button type="button" className="btn sm" disabled={!valid} onClick={add}>Add event</button>
        </div>
      ) : <p className="xs muted">Up to {MAX_EVENTS} events.</p>}
    </div>
  );
}

function MessageFilters({ draft, onChange }: { draft: Draft; onChange: (d: Draft) => void }) {
  const toggle = <T,>(xs: T[], x: T) => (xs.includes(x) ? xs.filter((y) => y !== x) : [...xs, x]);
  return (
    <>
      <Field label="From">
        <select className="select" value={draft.message_source} onChange={(e) => onChange({ ...draft, message_source: e.target.value as MessageSource })}>
          <option value="">Every campaign</option>
          <option value="broadcast">One-time campaigns</option>
          <option value="journey">Triggered and automation campaigns (journeys)</option>
        </select>
      </Field>
      <fieldset className="rb-fieldset">
        <legend className="small muted">Channels {draft.channels.length === 0 && <span className="faint">(all)</span>}</legend>
        <div className="rb-checks">
          {CHANNELS.map((c) => (
            <label className="check" key={c}><input type="checkbox" checked={draft.channels.includes(c)} onChange={() => onChange({ ...draft, channels: toggle(draft.channels, c) })} />{CHANNEL_NAMES[c]}</label>
          ))}
        </div>
      </fieldset>
      <fieldset className="rb-fieldset">
        <legend className="small muted">Outcomes {draft.kinds.length === 0 && <span className="faint">(all)</span>}</legend>
        <div className="rb-checks">
          {MESSAGE_KINDS.map((k) => (
            <label className="check" key={k}><input type="checkbox" checked={draft.kinds.includes(k)} onChange={() => onChange({ ...draft, kinds: toggle(draft.kinds, k) })} />{KIND_NAMES[k]}</label>
          ))}
        </div>
      </fieldset>
    </>
  );
}

// ---- results ----

const fmtDate = (iso: string) => new Date(iso).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" });

function Results({ name, run, ready, stale, error }: { name: string; run?: Run; ready: boolean; stale: boolean; error: unknown }) {
  // The query keeps the previous result as placeholder data, so it stays on screen (dimmed) while a
  // new one is counted or while the report has problems.
  if (error && !stale) return <p className="xs rb-danger" role="alert">{error instanceof Error ? error.message : String(error)}</p>;
  if (!run) return <p className="muted small">{ready ? "Counting…" : "Fix the report's settings to see results."}</p>;

  const { spec, result: r } = run;
  const busy = stale || !ready;
  const g = spec.group_by.type;
  let body: ReactNode;
  if (g === "none") {
    const v = r.rows[0]?.value ?? 0;
    body = (
      <div className="rb-figure">
        <strong>{formatValue(v, spec.metric.type)}</strong>
        <span>{metricLabel(spec)}</span>
      </div>
    );
  } else if (r.rows.length === 0) {
    body = <p className="small muted">Nothing to count in this period. Try a longer date range or fewer filters.</p>;
  } else {
    body = (
      <>
        {spec.chart === "bars" && <Bars run={run} />}
        {spec.chart === "line" && isTimeGroup(g) && <LineChart run={run} />}
        <DataTable run={run} asChart={spec.chart === "table"} />
        {r.rows.length >= r.max_groups && <p className="xs muted rb-note">Top {r.max_groups} groups, largest first.</p>}
      </>
    );
  }
  return (
    <div className="rb-out" style={{ opacity: busy ? 0.45 : 1 }} aria-busy={busy}>
      <div className="rb-out-head">
        <p className="small muted rb-desc">{describe(spec)}</p>
        <button type="button" className="btn sm" disabled={busy} title={busy ? "Wait for the new result" : "Download the result shown as a CSV file"}
          onClick={() => downloadCsv(csvFileName(name), toCsv(reportRows(spec, r)))}>Export CSV</button>
      </div>
      {body}
      <p className="xs faint rb-period">{fmtDate(r.from)} to {fmtDate(r.to)}</p>
    </div>
  );
}

function Bars({ run }: { run: Run }) {
  const { spec, result: r } = run;
  const max = Math.max(0, ...r.rows.map((x) => x.value)) || 1;
  return (
    <div className="bars rb-bars" aria-hidden>
      {r.rows.map((x) => {
        const label = groupLabel(x.group, spec.group_by.type, r.labels);
        return (
          <div className="rb-bar-row" key={x.group}>
            <span className="rb-bar-label" title={label}>{label}</span>
            <div className="bar-track"><div className="bar-fill rb-fill" style={{ width: `${(Math.max(0, x.value) / max) * 100}%` }} /></div>
            <span className="num">{formatValue(x.value, spec.metric.type)}</span>
          </div>
        );
      })}
    </div>
  );
}

const BOX = { width: 640, height: 240, left: 56, right: 16, top: 12, bottom: 30 };

function LineChart({ run }: { run: Run }) {
  const { spec, result: r } = run;
  const g = spec.group_by.type;
  const geo = lineGeometry(r.rows.map((x) => x.value), BOX);
  const xs = labelIndices(r.rows.length, 6);
  const last = r.rows.length - 1;
  return (
    <svg className="rb-line" viewBox={`0 0 ${BOX.width} ${BOX.height}`} role="img"
      aria-label={`Line chart of ${metricLabel(spec).toLowerCase()} per ${g}, ${r.rows.length} points. The data is in the table below.`}>
      {geo.ticks.map((t) => (
        <g key={t.value}>
          <line className="rb-grid" x1={BOX.left} x2={BOX.width - BOX.right} y1={t.y} y2={t.y} />
          <text className="rb-axis" x={BOX.left - 8} y={t.y} textAnchor="end" dominantBaseline="middle">{formatValue(t.value, "avg")}</text>
        </g>
      ))}
      {xs.map((i) => (
        <text key={i} className="rb-axis" x={geo.points[i].x} y={BOX.height - 8} textAnchor={last > 0 && i === 0 ? "start" : last > 0 && i === last ? "end" : "middle"}>
          {shortTimeLabel(r.rows[i].group, g)}
        </text>
      ))}
      <path className="rb-path" d={geo.path} />
      {geo.points.map((p, i) => (
        <circle key={i} className="rb-point" cx={p.x} cy={p.y} r={r.rows.length > 60 ? 2 : 3.5}>
          <title>{groupLabel(r.rows[i].group, g, r.labels)}: {formatValue(p.value, spec.metric.type)}</title>
        </circle>
      ))}
    </svg>
  );
}

function DataTable({ run, asChart }: { run: Run; asChart: boolean }) {
  const { spec, result: r } = run;
  const g = spec.group_by.type;
  const head = groupOption(spec.source, g)?.label ?? "Group";
  const keyed = spec.group_by.key ? `${head}: ${spec.group_by.key}` : head;
  return (
    <div className={`table-scroll rb-table${asChart ? " as-chart" : ""}`}>
      <table className="table">
        <caption className="rb-caption">{describe(spec)}</caption>
        <thead><tr><th scope="col">{keyed}</th><th scope="col" className="num">{metricLabel(spec)}</th></tr></thead>
        <tbody>{r.rows.map((x) => (
          <tr key={x.group}>
            <th scope="row" className={x.group === "" ? "faint" : undefined}>
              {groupLabel(x.group, g, r.labels)}
              {g === "campaign" && campaignKind(x.group) && <span className="xs faint"> · {campaignKind(x.group)}</span>}
            </th>
            <td className="num">{formatValue(x.value, spec.metric.type)}</td>
          </tr>
        ))}</tbody>
      </table>
    </div>
  );
}
