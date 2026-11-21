import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import {
  api, can, type AttributeDef, type CatalogEntry, type DiscoveredProp, type EventSchema, type EventSummary, type PropType,
  type SchemaChecks, type SchemaMode, type SchemaProp,
} from "../api";
import { useMe } from "../auth";
import {
  cleanProps, copyProps, dependsSentence, newEventProblem, parseValues, valuesText, exampleProps, fillDays, formatShare, keyFromPath, mergeProperties, MODE_HELP, modeLabel, moveProp,
  pathOf, problemWords, PROP_TYPES, propFromDiscovered, TYPE_NAMES, validateSchema,
} from "../eventSchemas";
import { Empty, ErrorNote, Field, LiveNumber, Modal, PageHead, timeAgo, useToast } from "../ui";
import { askDelete, useEditGuard } from "../guard";
import "./events.css";
import { ContactCount } from "../ContactCount";
import { addDays, period, todayUTC } from "../contactLinks";

type Suggested = EventSchema & { exists: boolean };
type Draft = Pick<EventSchema, "label" | "description" | "mode" | "props">;

/** Who may change schemas: contacts editors; turning on enforce also needs settings edit. */
function useEventPerms() {
  const perms = useMe().data?.permissions;
  return { canEdit: can(perms, "contacts", "edit"), canEnforce: can(perms, "settings", "edit") };
}

/** ModeBadge: how an event's schema is used, or that the event isn't described. */
export function ModeBadge({ mode, schema = true }: { mode?: SchemaMode; schema?: boolean }) {
  const m = schema ? mode ?? "warn" : undefined;
  return <span className={`badge ev-mode ev-mode-${m ?? "none"}`}>{modeLabel(m)}</span>;
}

// ---- list ----

// EventList: every event sent in the last 30 days or described by a schema, with volume, how it's
// described and how many recent events didn't fit their schema.
export function EventList() {
  const { canEdit } = useEventPerms();
  const [suggesting, setSuggesting] = useState(false);
  const [creating, setCreating] = useState(false);
  const q = useQuery({
    queryKey: ["event-catalog"],
    queryFn: () => api.get<{ events: CatalogEntry[]; days: number }>("/v1/events/catalog"),
    refetchInterval: 15_000,
  });
  const events = q.data?.events ?? [];
  const suggestBtn = canEdit && <>
    <button className="btn" onClick={() => setSuggesting(true)}>Add suggested events</button>
    <button className="btn primary" onClick={() => setCreating(true)}>New event</button>
  </>;
  return (
    <div className="page">
      <PageHead
        title="Events"
        intro="The events your website, apps and servers send, with their properties. Describe an event with a schema (a list of the details it carries) so segments and campaigns can offer typed filters for them, and events that don't fit are caught and shown here."
        actions={suggestBtn}
      />
      <ErrorNote error={q.error} />
      <div className="panel">
        {events.length === 0 && !q.isLoading ? (
          <Empty title="No events yet" action={suggestBtn}>
            Events appear here as soon as your site, app or server sends them. You can describe one before it arrives: pick common ones (purchase, sign-up, login, cart), or add your own with New event.
          </Empty>
        ) : (
          <div className="table-scroll">
            <table className="table ev-table">
              <thead><tr><th>Event</th><th className="num">Last 30 days</th><th>Last seen</th><th>Schema</th><th>Problems (7 days)</th></tr></thead>
              <tbody>
                {events.map((e) => (
                  <tr key={e.name}>
                    <td>
                      <Link to={`/events/${encodeURIComponent(e.name)}`} className="ev-name"><code>{e.name}</code></Link>
                      {e.label && <div className="xs muted">{e.label}</div>}
                    </td>
                    <td className="num">
                      {/* The whole cell opens the contacts who sent it in these 30 days. */}
                      <ContactCount value={e.contacts} filters={[["event", e.name], period(addDays(todayUTC(), -29), todayUTC())]} label={`contacts who sent ${e.name}`}>
                        <span className="ev-count">
                          <LiveNumber value={e.count} />
                          <span className="xs muted">{e.contacts.toLocaleString("en-IN")} {e.contacts === 1 ? "contact" : "contacts"}</span>
                        </span>
                      </ContactCount>
                    </td>
                    <td className="small">{e.last_seen ? timeAgo(e.last_seen) : <span className="faint">not in 30 days</span>}</td>
                    <td>
                      <ModeBadge mode={e.mode} schema={e.schema} />
                      {e.schema && <div className="xs muted">{e.props} {e.props === 1 ? "property" : "properties"}</div>}
                    </td>
                    <td className="small"><ProblemCell e={e} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {suggesting && <SuggestedModal onClose={() => setSuggesting(false)} />}
      {creating && <NewEventModal known={events} onClose={() => setCreating(false)} />}
    </div>
  );
}

function ProblemCell({ e }: { e: CatalogEntry }) {
  if (!e.schema) return <span className="faint">—</span>;
  if (e.mode === "off") return <span className="faint">Not checked</span>;
  if (e.checked === 0) return <span className="muted">No events checked</span>;
  const text = `${e.problems.toLocaleString("en-IN")} of ${e.checked.toLocaleString("en-IN")} events`;
  return e.problems > 0 ? <span className="ev-warn">{text}</span> : <span className="ev-ok">{text}</span>;
}

// NewEventModal describes an event your site, app or server will send, before the first one arrives,
// so segments and campaigns can offer it straight away. Its properties can start from a described or
// suggested event (cart_abandoned, …); allowed values' labels, lists and conditions are edited on the
// Schema tab, which opens after saving. The schema starts in Warn mode.
function NewEventModal({ known, onClose }: { known: CatalogEntry[]; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();
  const [name, setName] = useState("");
  const [label, setLabel] = useState("");
  const [description, setDescription] = useState("");
  const [props, setProps] = useState<SchemaProp[]>([]);
  const described = useQuery({ queryKey: ["event-schemas"], queryFn: () => api.get<{ schemas: EventSchema[] }>("/v1/events/schemas"), staleTime: 60_000 });
  const suggested = useQuery({ queryKey: ["event-schemas", "suggested"], queryFn: () => api.get<{ schemas: Suggested[] }>("/v1/events/schemas/suggested") });
  const sources = [
    ...(described.data?.schemas ?? []).filter((x) => x.props.length > 0),
    ...(suggested.data?.schemas ?? []).filter((x) => !x.exists && x.props.length > 0),
  ];
  const trimmed = name.trim();
  const nameProblem = newEventProblem(trimmed, known);
  const problems = useMemo(() => validateSchema({ label, description, mode: "warn", props }), [label, description, props]);
  const setProp = (i: number, patch: Partial<SchemaProp>) => setProps(props.map((p, j) => (j === i ? { ...p, ...patch } : p)));
  const copyFrom = (from: string) => {
    const src = sources.find((x) => x.name === from);
    if (src) setProps(copyProps(props, src.props));
  };
  const save = useMutation({
    mutationFn: () => api.put<EventSchema>(`/v1/events/${encodeURIComponent(trimmed)}/schema`, {
      label: label.trim(), description: description.trim(), mode: "warn", props: cleanProps(props),
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["event-catalog"] });
      qc.invalidateQueries({ queryKey: ["event-schemas"] });
      qc.invalidateQueries({ queryKey: ["event-names"] });
      qc.invalidateQueries({ queryKey: ["attributes"] });
      toast({ text: `Added ${trimmed}` });
      nav(`/events/${encodeURIComponent(trimmed)}`, { state: { tab: "schema" } });
    },
  });
  const showProblems = props.some((p) => p.key) && problems.length > 0;
  return (
    <Modal wide title="New event" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!trimmed || !!nameProblem || problems.length > 0 || save.isPending} onClick={() => save.mutate()}>{save.isPending ? "Adding…" : "Add event"}</button>
    </>}>
      <p className="small muted" style={{ marginTop: 0, marginBottom: 16 }}>
        Describe an event before your site, app or server first sends it, so you can build segments and campaigns on it now.
      </p>
      <ErrorNote error={save.error} />
      <Field label="Event name" hint={nameProblem ? <span className="ev-warn">{nameProblem}</span> : <>The exact name your code sends, for example <code>webinar_joined</code>. It can't be changed later.</>}>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="webinar_joined" autoFocus spellCheck={false} aria-invalid={!!nameProblem} />
      </Field>
      <div className="grid-2">
        <Field label="Label (optional)" hint="How the event is named in segments, campaigns and reports.">
          <input className="input" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Joined a webinar" maxLength={120} />
        </Field>
        <Field label="Description (optional)">
          <input className="input" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Someone joined a live webinar." maxLength={500} />
        </Field>
      </div>

      <div className="ev-new-props-head">
        <h4>Properties</h4>
        <select className="select" value="" aria-label="Copy properties from another event" disabled={sources.length === 0} onChange={(e) => copyFrom(e.target.value)}>
          <option value="">Copy properties from…</option>
          {sources.map((x) => <option key={x.name} value={x.name}>{x.label || x.name} ({x.props.length})</option>)}
        </select>
        <button className="btn sm" disabled={props.length >= 100} onClick={() => setProps([...props, { key: "", type: "string" }])}>Add property</button>
      </div>
      {props.length === 0 ? (
        <p className="small muted ev-new-empty">The details this event carries, such as <code>topic</code> or <code>duration</code>. Optional: you can add them later on the Schema tab.</p>
      ) : (
        <table className="table ev-new-props">
          <thead><tr><th>Key</th><th>Type</th><th>Required</th><th /></tr></thead>
          <tbody>
            {props.map((p, i) => {
              const n = p.key || `property ${i + 1}`;
              return (
                <tr key={i}>
                  <td>
                    <input className="input" value={p.key} maxLength={64} placeholder="topic" spellCheck={false} aria-label={`Key of property ${i + 1}`}
                      onChange={(e) => setProp(i, { key: e.target.value })} />
                    {p.path && p.path !== p.key && <div className="xs muted">from <code>{p.path}</code></div>}
                    {p.type === "enum" && (
                      <input className="input ev-new-values" value={valuesText(p.values)} placeholder="Allowed values, separated by commas" aria-label={`Allowed values of ${n}`}
                        onChange={(e) => setProp(i, { values: parseValues(e.target.value, p.values) })} />
                    )}
                  </td>
                  <td>
                    <select className="select" value={p.type} aria-label={`Type of ${n}`} onChange={(e) => {
                      const type = e.target.value as PropType;
                      setProp(i, { type, values: type === "enum" ? p.values ?? [] : undefined, add_to_list: type === "list" || type === "object" ? undefined : p.add_to_list });
                    }}>
                      {PROP_TYPES.map((t) => <option key={t} value={t}>{TYPE_NAMES[t]}</option>)}
                    </select>
                  </td>
                  <td><input type="checkbox" checked={!!p.required} aria-label={`${n} is required`} onChange={(e) => setProp(i, { required: e.target.checked })} /></td>
                  <td className="num">
                    <button className="btn ghost sm danger" aria-label={`Remove ${n}`}
                      onClick={() => setProps(props.filter((_, j) => j !== i).map((x) => (x.depends_on?.prop === p.key ? { ...x, depends_on: null } : x)))}>Remove</button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {showProblems && (
        <div className="alert error" role="alert">
          <strong>Fix these first:</strong>
          <ul className="ev-problems-list">{problems.map((x) => <li key={x}>{x}</li>)}</ul>
        </div>
      )}
      {props.length > 0 && <p className="xs muted" style={{ marginBottom: 0 }}>After adding, the Schema tab opens for the rest: labels, personal data, lists on the contact and when a property applies.</p>}
    </Modal>
  );
}

// SuggestedModal offers the starter schemas (purchase, sign-up, …); ones already added are shown but can't be picked.
function SuggestedModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({
    queryKey: ["event-schemas", "suggested"],
    queryFn: () => api.get<{ schemas: Suggested[] }>("/v1/events/schemas/suggested"),
  });
  const schemas = q.data?.schemas ?? [];
  const [picked, setPicked] = useState<Set<string> | null>(null);
  const chosen = picked ?? new Set(schemas.filter((s) => !s.exists).map((s) => s.name));
  const toggle = (name: string) => {
    const next = new Set(chosen);
    if (next.has(name)) next.delete(name); else next.add(name);
    setPicked(next);
  };
  const add = useMutation({
    mutationFn: () => api.post<{ added: string[] }>("/v1/events/schemas/suggested", { names: [...chosen] }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["event-catalog"] });
      qc.invalidateQueries({ queryKey: ["event-schemas"] });
      qc.invalidateQueries({ queryKey: ["attributes"] });
      toast({ text: r.added.length ? `Added ${r.added.join(", ")}` : "Nothing to add: those events are already described" });
      onClose();
    },
  });
  return (
    <Modal title="Add suggested events" onClose={onClose} guard={false} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={chosen.size === 0 || add.isPending} onClick={() => add.mutate()}>
        {chosen.size ? `Add ${chosen.size} ${chosen.size === 1 ? "event" : "events"}` : "Add"}
      </button>
    </>}>
      <p className="small muted" style={{ marginTop: 0 }}>
        Schemas for common events, in Warn mode: nothing is refused. Edit them afterwards to match what you send. A purchase also adds its product codes to the contact's list field “products”.
      </p>
      <ErrorNote error={q.error ?? add.error} />
      <ul className="ev-suggested">
        {schemas.map((s) => (
          <li key={s.name}>
            <label className={s.exists ? "disabled" : ""}>
              <input type="checkbox" disabled={s.exists} checked={s.exists || chosen.has(s.name)} onChange={() => toggle(s.name)} />
              <span>
                <strong>{s.label || s.name}</strong> <code className="xs">{s.name}</code>
                {s.exists && <span className="badge ok">added</span>}
                <span className="xs muted ev-suggested-desc">{s.description} {s.props.length} properties.</span>
              </span>
            </label>
          </li>
        ))}
      </ul>
    </Modal>
  );
}

// ---- detail ----

type Tab = "overview" | "schema" | "problems" | "sending";
const TABS: { id: Tab; label: string }[] = [
  { id: "overview", label: "Volume and properties" },
  { id: "schema", label: "Schema" },
  { id: "problems", label: "Problems" },
  { id: "sending", label: "Sending this event" },
];

const draftOf = (s?: EventSchema | null): Draft | null =>
  s ? { label: s.label, description: s.description, mode: s.mode, props: s.props.map((p) => ({ ...p })) } : null;

// EventDetail: one event's volume, the properties seen in recent events next to its schema, the
// schema editor, the problems the schema caught and how to send it.
export function EventDetail() {
  const name = useParams().name ?? "";
  const { canEdit } = useEventPerms();
  // A new event (New event on the list) opens on its Schema tab.
  const opened = useLocation().state as { tab?: Tab } | null;
  const [tab, setTab] = useState<Tab>(opened?.tab ?? "overview");
  const q = useQuery({
    queryKey: ["event-summary", name],
    queryFn: () => api.get<EventSummary>(`/v1/events/${encodeURIComponent(name)}/summary`),
    refetchInterval: 30_000,
  });
  const summary = q.data;
  const saved = summary?.schema ?? null;
  // The editor's working copy; reset when the saved schema changes (after a save, or by someone else
  // when nothing is being edited).
  const [draft, setDraft] = useState<Draft | null>(null);
  const [dirty, setDirty] = useState(false);
  useEffect(() => {
    if (!dirty) setDraft(draftOf(saved));
  }, [saved?.updated_at, saved == null]); // only when the saved version changes
  const edit = (d: Draft | null) => { setDraft(d); setDirty(true); };
  // An unsaved schema makes leaving the page ask first.
  const { markDirty, saved: clean } = useEditGuard();
  useEffect(() => { if (dirty) markDirty(); else clean(); }, [dirty, markDirty, clean]);
  const addToSchema = (d: DiscoveredProp) => {
    const base = draft ?? { label: "", description: "", mode: "warn" as SchemaMode, props: [] };
    let p = propFromDiscovered(d);
    if (base.props.some((x) => x.key === p.key)) p = { ...p, key: `${p.key}_2`, path: d.path };
    edit({ ...base, props: [...base.props, p] });
    setTab("schema");
  };
  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/events">Events</Link>}
        title={<span className="ev-title"><code>{name}</code> <ModeBadge mode={saved?.mode} schema={!!saved} /></span>}
        intro={saved?.label ? <>{saved.label}{saved.description && <> · {saved.description}</>}</> : saved?.description || undefined}
      />
      <ErrorNote error={q.error} />
      <div className="tabs" role="tablist" aria-label="Event sections">
        {TABS.map((t) => (
          <button key={t.id} role="tab" id={`ev-tab-${t.id}`} aria-controls={`ev-panel-${t.id}`} aria-selected={tab === t.id} onClick={() => setTab(t.id)}>
            {t.label}
            {t.id === "schema" && dirty && <span className="ev-dot" aria-label="unsaved changes" />}
            {t.id === "problems" && (summary?.checks?.bad ?? 0) > 0 && <span className="badge warn ev-tab-count">{summary!.checks!.bad.toLocaleString("en-IN")}</span>}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`ev-panel-${tab}`} aria-labelledby={`ev-tab-${tab}`}>
        {tab === "overview" && (
          <>
            <VolumePanel volume={summary?.volume ?? []} days={summary?.days || 30} loading={q.isLoading} />
            <PropertiesPanel summary={summary} schemaProps={draft?.props ?? saved?.props ?? []} canEdit={canEdit} onAdd={addToSchema} />
          </>
        )}
        {tab === "schema" && (
          <SchemaPanel
            name={name} saved={saved} draft={draft} dirty={dirty} canEdit={canEdit} discovered={summary?.properties ?? []}
            onChange={edit}
            onSaved={(s) => { setDirty(false); setDraft(draftOf(s)); }}
            onDiscard={() => { setDirty(false); setDraft(draftOf(saved)); }}
          />
        )}
        {tab === "problems" && <ProblemsPanel checks={summary?.checks} schema={saved} onDescribe={() => setTab("schema")} />}
        {tab === "sending" && <SendingPanel name={name} schema={draft ?? saved} />}
      </div>
    </div>
  );
}

// VolumePanel: a bar per day for the last 30 days (days without events show as empty).
function VolumePanel({ volume, days, loading }: { volume: { day: string; count: number }[]; days: number; loading: boolean }) {
  const filled = fillDays(volume, days, new Date());
  const total = filled.reduce((s, d) => s + d.count, 0);
  const max = Math.max(1, ...filled.map((d) => d.count));
  const W = 600, H = 120, gap = 2;
  const bw = W / filled.length - gap;
  return (
    <section className="panel ev-section">
      <div className="panel-head">
        <h3>Volume</h3>
        <span className="small muted"><strong className="ev-total">{total.toLocaleString("en-IN")}</strong> events in the last {days} days</span>
      </div>
      <div className="panel-body">
        {loading ? <p className="muted small">Loading…</p> : (
          <>
            <svg className="ev-chart" viewBox={`0 0 ${W} ${H + 18}`} role="img" aria-label={`Events per day for the last ${days} days, ${total} in all`} preserveAspectRatio="none">
              <line x1={0} x2={W} y1={H} y2={H} className="ev-axis" />
              {filled.map((d, i) => {
                const h = d.count ? Math.max(2, (d.count / max) * (H - 4)) : 0;
                return (
                  <rect key={d.day} x={i * (bw + gap)} y={H - h} width={bw} height={h} rx={1.5} className="ev-bar">
                    <title>{`${d.day}: ${d.count.toLocaleString("en-IN")}`}</title>
                  </rect>
                );
              })}
              <text x={0} y={H + 14} className="ev-tick">{filled[0]?.day}</text>
              <text x={W} y={H + 14} textAnchor="end" className="ev-tick">Today</text>
            </svg>
            <p className="xs muted" style={{ margin: "6px 0 0" }}>Busiest day: {max.toLocaleString("en-IN")} events. Days are in UTC.</p>
          </>
        )}
      </div>
    </section>
  );
}

// PropertiesPanel: properties seen in recent events next to the ones the schema describes.
function PropertiesPanel({ summary, schemaProps, canEdit, onAdd }: {
  summary?: EventSummary; schemaProps: SchemaProp[]; canEdit: boolean; onAdd: (d: DiscoveredProp) => void;
}) {
  const rows = mergeProperties(summary?.properties ?? [], schemaProps);
  return (
    <section className="panel ev-section">
      <div className="panel-head">
        <h3>Properties</h3>
        <span className="small muted">From the latest {(summary?.sampled ?? 0).toLocaleString("en-IN")} events</span>
      </div>
      {rows.length === 0 ? (
        <Empty title="No properties seen">Events with this name carried no properties in the sample, and the schema describes none.</Empty>
      ) : (
        <div className="table-scroll">
          <table className="table">
            <thead><tr><th>Property</th><th>Type</th><th className="num">Events having it</th><th>Examples</th><th>In the schema</th></tr></thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.path}>
                  <td>
                    <code>{r.path}</code>
                    {r.prop && (r.prop.label || r.prop.key !== r.path) && <div className="xs muted">{r.prop.label || r.prop.key}</div>}
                  </td>
                  <td className="small">
                    {r.prop ? TYPE_NAMES[r.prop.type] : (r.discovered ? (TYPE_NAMES[r.discovered.type as PropType] ?? r.discovered.type) : "")}
                    {r.prop && r.discovered && r.discovered.type !== "mixed" && !typeFits(r.prop.type, r.discovered.type) && (
                      <div className="xs ev-warn">Seen as {(TYPE_NAMES[r.discovered.type as PropType] ?? r.discovered.type).toLowerCase()}</div>
                    )}
                    {r.discovered?.type === "mixed" && <div className="xs ev-warn">Mixed types seen</div>}
                  </td>
                  <td className="num">{r.discovered ? formatShare(r.discovered.share) : <span className="faint">not seen</span>}</td>
                  <td className="small ev-examples">
                    {r.prop?.personal ? <span className="faint">Personal data, hidden</span> : (r.discovered?.examples ?? []).map((x) => <code key={x}>{x}</code>)}
                  </td>
                  <td>
                    {r.described ? (
                      <span className="badge ok">Described{r.prop?.required ? ", required" : ""}</span>
                    ) : canEdit ? (
                      <button className="btn sm" onClick={() => onAdd(r.discovered!)} aria-label={`Add ${r.path} to the schema`}>Add to schema</button>
                    ) : (
                      <span className="badge">Not described</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

/** typeFits: the type seen in events matches the schema's type (enums are text). */
function typeFits(schemaType: PropType, seen: string) {
  return schemaType === seen || (schemaType === "enum" && seen === "string") || (schemaType === "date" && seen === "string");
}

// SchemaPanel: the schema editor for editors, a read-only view for everyone else.
function SchemaPanel({ name, saved, draft, dirty, canEdit, discovered, onChange, onSaved, onDiscard }: {
  name: string; saved: EventSchema | null; draft: Draft | null; dirty: boolean; canEdit: boolean; discovered: DiscoveredProp[];
  onChange: (d: Draft | null) => void; onSaved: (s: EventSchema | null) => void; onDiscard: () => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const { canEnforce } = useEventPerms();
  const [confirming, setConfirming] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const attrs = useQuery({
    queryKey: ["attributes"],
    queryFn: () => api.get<{ attributes: AttributeDef[]; undefined: string[] }>("/v1/attributes"),
    staleTime: 30_000,
    enabled: canEdit,
  });
  const listFields = (attrs.data?.attributes ?? []).filter((a) => a.type === "list" && a.key !== "tags").map((a) => a.key);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["event-catalog"] });
    qc.invalidateQueries({ queryKey: ["event-summary", name] });
    qc.invalidateQueries({ queryKey: ["event-schemas"] });
    qc.invalidateQueries({ queryKey: ["attributes"] });
  };
  const problems = useMemo(() => (draft ? validateSchema(draft) : []), [draft]);
  const needsConfirm = draft?.mode === "enforce" && saved?.mode !== "enforce";
  const save = useMutation({
    mutationFn: () => {
      const body = { label: draft!.label.trim(), description: draft!.description.trim(), mode: draft!.mode, props: cleanProps(draft!.props), ...(needsConfirm ? { confirm: "ENFORCE" } : {}) };
      return api.put<EventSchema>(`/v1/events/${encodeURIComponent(name)}/schema`, body);
    },
    onSuccess: (s) => { setConfirmed(false); onSaved(s); refresh(); toast({ text: `Saved the schema of ${name}` }); },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/events/${encodeURIComponent(name)}/schema`),
    onSuccess: () => { onSaved(null); refresh(); toast({ text: `Deleted the schema of ${name}` }); },
  });

  if (!draft) {
    return (
      <section className="panel ev-section">
        <Empty
          title="This event isn't described yet"
          action={canEdit && (
            <div className="row" style={{ justifyContent: "center" }}>
              <button className="btn primary" onClick={() => onChange({ label: "", description: "", mode: "warn", props: [] })}>Describe this event</button>
              {discovered.length > 0 && (
                <button className="btn" onClick={() => onChange({ label: "", description: "", mode: "warn", props: discovered.map(propFromDiscovered) })}>
                  Start from the {discovered.length} properties seen
                </button>
              )}
            </div>
          )}
        >
          A schema says which properties this event carries and their types. Segments and journeys then offer typed filters for them, and events that don't fit are counted under Problems.
        </Empty>
      </section>
    );
  }
  if (!canEdit) return <SchemaView schema={draft} />;

  const set = (patch: Partial<Draft>) => onChange({ ...draft, ...patch });
  const setProp = (i: number, p: SchemaProp) => set({ props: draft.props.map((x, j) => (j === i ? p : x)) });
  const pickMode = (m: SchemaMode) => {
    if (m === "enforce" && saved?.mode !== "enforce" && !confirmed) { setConfirming(true); return; }
    set({ mode: m });
  };
  const blocked = problems.length > 0 || save.isPending || (needsConfirm && !confirmed);
  return (
    <>
      <section className="panel ev-section">
        <div className="panel-head"><h3>About this event</h3></div>
        <div className="panel-body">
          <div className="grid-2">
            <Field label="Label" hint="How the event is named in segments, campaigns and reports.">
              <input className="input" value={draft.label} maxLength={120} onChange={(e) => set({ label: e.target.value })} placeholder="Purchase" />
            </Field>
            <Field label="Description">
              <input className="input" value={draft.description} maxLength={500} onChange={(e) => set({ description: e.target.value })} placeholder="An order was paid for." />
            </Field>
          </div>
          <Field label="Checking" group hint={MODE_HELP[draft.mode]}>
            <div className="seg-toggle" role="radiogroup" aria-label="Checking mode">
              {(["off", "warn", "enforce"] as SchemaMode[]).map((m) => (
                <button key={m} role="radio" aria-checked={draft.mode === m} aria-pressed={draft.mode === m}
                  disabled={m === "enforce" && !canEnforce && saved?.mode !== "enforce"}
                  title={m === "enforce" && !canEnforce ? "Turning on Enforce needs permission to change settings" : MODE_HELP[m]}
                  onClick={() => pickMode(m)}>
                  {modeLabel(m)}
                </button>
              ))}
            </div>
          </Field>
          {!canEnforce && saved?.mode !== "enforce" && <p className="xs muted" style={{ marginTop: -8 }}>Enforce can be turned on by someone who can change workspace settings.</p>}
        </div>
      </section>

      <section className="panel ev-section">
        <div className="panel-head">
          <h3>Properties</h3>
          <button className="btn sm" disabled={draft.props.length >= 100} onClick={() => set({ props: [...draft.props, { key: "", type: "string" }] })}>Add property</button>
        </div>
        <datalist id="ev-list-fields">{listFields.map((k) => <option key={k} value={k} />)}</datalist>
        {draft.props.length === 0 ? (
          <Empty title="No properties yet">Add the properties this event carries, or add them from what's been seen under “Volume and properties”.</Empty>
        ) : (
          <ol className="ev-props">
            {draft.props.map((p, i) => (
              <PropEditor
                key={i} index={i} prop={p} props={draft.props}
                onChange={(np) => setProp(i, np)}
                onMove={(d) => set({ props: moveProp(draft.props, i, d) })}
                onRemove={() => set({ props: draft.props.filter((_, j) => j !== i).map((x) => (x.depends_on?.prop === p.key ? { ...x, depends_on: null } : x)) })}
              />
            ))}
          </ol>
        )}
      </section>

      {problems.length > 0 && (
        <div className="alert error" role="alert">
          <strong>Fix these before saving:</strong>
          <ul className="ev-problems-list">{problems.map((x) => <li key={x}>{x}</li>)}</ul>
        </div>
      )}
      <ErrorNote error={save.error ?? remove.error} />
      <div className="row ev-save-bar">
        <button className="btn primary" disabled={blocked || !dirty} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : saved ? "Save schema" : "Create schema"}</button>
        {dirty && <button className="btn ghost" onClick={onDiscard}>Discard changes</button>}
        <span style={{ flex: 1 }} />
        {saved && (
          <button className="btn danger" disabled={remove.isPending}
            onClick={() => void askDelete(`the schema of ${name}`, "Its events are kept; they're no longer checked, and segments and journeys lose its typed filters. This can't be undone.", name)
              .then((ok) => ok && remove.mutate())}>
            Delete schema
          </button>
        )}
      </div>
      {confirming && (
        <EnforceModal
          name={name}
          onClose={() => setConfirming(false)}
          onConfirm={() => { setConfirmed(true); setConfirming(false); set({ mode: "enforce" }); }}
        />
      )}
    </>
  );
}

// EnforceModal: turning on Enforce loses events that don't fit, so the user types ENFORCE first.
function EnforceModal({ name, onClose, onConfirm }: { name: string; onClose: () => void; onConfirm: () => void }) {
  const [text, setText] = useState("");
  return (
    <Modal title={`Enforce the schema of ${name}?`} onClose={onClose} guard={false} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={text !== "ENFORCE"} onClick={onConfirm}>Turn on Enforce</button>
    </>}>
      <p className="small" style={{ marginTop: 0 }}>
        The server API (<code>POST /v1/events</code>) will refuse <strong>{name}</strong> events that don't fit this schema, with a 400 saying what's wrong.
        Refused events are <strong>lost</strong> unless the sender retries them after fixing the problem.
      </p>
      <p className="small">Events from the browser and app SDKs are never refused; their problems are still counted.</p>
      <p className="small">Check the Problems tab first: if events are failing in Warn mode now, they'll be refused.</p>
      <Field label="Type ENFORCE to confirm">
        <input className="input" autoFocus value={text} onChange={(e) => setText(e.target.value)} autoComplete="off" spellCheck={false} />
      </Field>
      <p className="xs muted">The change takes effect when you save the schema.</p>
    </Modal>
  );
}

// PropEditor: one property of the schema being edited.
function PropEditor({ index, prop: p, props, onChange, onMove, onRemove }: {
  index: number; prop: SchemaProp; props: SchemaProp[]; onChange: (p: SchemaProp) => void; onMove: (d: -1 | 1) => void; onRemove: () => void;
}) {
  const id = `ev-prop-${index}`;
  const name = p.key || `property ${index + 1}`;
  const set = (patch: Partial<SchemaProp>) => onChange({ ...p, ...patch });
  // Properties this one may depend on: others without a dependency of their own, not inside items[].
  const parents = props.filter((x) => x.key && x.key !== p.key && !x.depends_on && !pathOf(x).includes("[]"));
  const parent = props.find((x) => x.key === p.depends_on?.prop);
  const single = p.type !== "list" && p.type !== "object";
  return (
    <li className="ev-prop">
      <div className="ev-prop-head">
        <span className="ev-prop-num">{index + 1}</span>
        <strong><code>{p.key || "new property"}</code></strong>
        <span className="xs muted">{TYPE_NAMES[p.type]}{p.required ? ", required" : ""}</span>
        <span style={{ flex: 1 }} />
        <button className="btn ghost sm" aria-label={`Move ${name} up`} disabled={index === 0} onClick={() => onMove(-1)}>↑</button>
        <button className="btn ghost sm" aria-label={`Move ${name} down`} disabled={index === props.length - 1} onClick={() => onMove(1)}>↓</button>
        <button className="btn ghost sm danger" aria-label={`Remove ${name}`} onClick={onRemove}>Remove</button>
      </div>
      <div className="ev-prop-grid">
        <Field label="Key">
          <input className="input" value={p.key} maxLength={64} onChange={(e) => set({ key: e.target.value })} placeholder="product_code" spellCheck={false} />
        </Field>
        <Field label="Path in the event (optional)" hint="product.code or items[].code">
          <input className="input" value={p.path ?? ""} maxLength={128} placeholder="same as key" spellCheck={false}
            onChange={(e) => set({ path: e.target.value })}
            onBlur={() => { if (!p.key && p.path) set({ key: keyFromPath(p.path) }); }} />
        </Field>
        <Field label="Label">
          <input className="input" value={p.label ?? ""} maxLength={120} onChange={(e) => set({ label: e.target.value })} placeholder="Product code" />
        </Field>
        <Field label="Type">
          <select className="select" value={p.type} onChange={(e) => {
            const type = e.target.value as PropType;
            set({ type, values: type === "enum" ? (p.values?.length ? p.values : [{ value: "" }]) : undefined, add_to_list: type === "list" || type === "object" ? undefined : p.add_to_list });
          }}>
            {PROP_TYPES.map((t) => <option key={t} value={t}>{TYPE_NAMES[t]}</option>)}
          </select>
        </Field>
      </div>
      <div className="ev-prop-checks">
        <label><input type="checkbox" checked={!!p.required} onChange={(e) => set({ required: e.target.checked })} /> Required</label>
        <label><input type="checkbox" checked={!!p.personal} onChange={(e) => set({ personal: e.target.checked })} /> Personal data: hidden in samples, never copied into messages</label>
      </div>
      {p.type === "enum" && <EnumEditor id={id} values={p.values ?? []} onChange={(values) => set({ values })} />}
      <div className="ev-prop-grid">
        {single && (
          <Field label="Add to list field (optional)" hint="Each event adds this value to the contact's list field, e.g. products. A new name creates the field.">
            <input className="input" list="ev-list-fields" value={p.add_to_list ?? ""} maxLength={64} placeholder="none" spellCheck={false}
              onChange={(e) => set({ add_to_list: e.target.value })} />
          </Field>
        )}
        <Field label="Applies" group>
          <DependsEditor prop={p} parents={parents} parent={parent} props={props} onChange={(depends_on) => set({ depends_on })} />
        </Field>
      </div>
    </li>
  );
}

// EnumEditor: the allowed values of an enum, each with an optional label shown in the app.
function EnumEditor({ id, values, onChange }: { id: string; values: { value: string; label?: string }[]; onChange: (v: { value: string; label?: string }[]) => void }) {
  const setAt = (i: number, patch: Partial<{ value: string; label: string }>) => onChange(values.map((v, j) => (j === i ? { ...v, ...patch } : v)));
  return (
    <fieldset className="ev-enum">
      <legend className="small">Allowed values</legend>
      {values.map((v, i) => (
        <div className="row ev-enum-row" key={i}>
          <input className="input" aria-label={`Value ${i + 1}`} id={i === 0 ? `${id}-v0` : undefined} value={v.value} maxLength={200} placeholder="value sent, e.g. upi" onChange={(e) => setAt(i, { value: e.target.value })} />
          <input className="input" aria-label={`Label of value ${i + 1}`} value={v.label ?? ""} maxLength={120} placeholder="label (optional), e.g. UPI" onChange={(e) => setAt(i, { label: e.target.value })} />
          <button className="btn ghost sm danger" aria-label={`Remove value ${v.value || i + 1}`} onClick={() => onChange(values.filter((_, j) => j !== i))}>Remove</button>
        </div>
      ))}
      <button className="btn sm" disabled={values.length >= 200} onClick={() => onChange([...values, { value: "" }])}>Add value</button>
    </fieldset>
  );
}

// DependsEditor: “Always applies” or “Only when <property> is <values>”.
function DependsEditor({ prop, parents, parent, props, onChange }: {
  prop: SchemaProp; parents: SchemaProp[]; parent?: SchemaProp; props: SchemaProp[]; onChange: (d: SchemaProp["depends_on"]) => void;
}) {
  const d = prop.depends_on;
  const listPath = pathOf(prop).includes("[]");
  if (!d) {
    return (
      <div className="ev-depends">
        <span className="small">Always applies</span>
        {!listPath && parents.length > 0 && (
          <button className="btn sm" onClick={() => {
            const first = parents.find((x) => x.type === "enum") ?? parents[0];
            onChange({ prop: first.key, values: [] });
          }}>Only when…</button>
        )}
      </div>
    );
  }
  const toggle = (v: string) => onChange({ ...d, values: d.values.includes(v) ? d.values.filter((x) => x !== v) : [...d.values, v] });
  return (
    <div className="ev-depends">
      <div className="row">
        <span className="small">Only when</span>
        <select className="select ev-depends-prop" aria-label="Property it depends on" value={d.prop} onChange={(e) => onChange({ prop: e.target.value, values: [] })}>
          {!parent && <option value={d.prop}>{d.prop || "choose…"}</option>}
          {parents.map((x) => <option key={x.key} value={x.key}>{x.label || x.key}</option>)}
        </select>
        <span className="small">is</span>
        <button className="btn ghost sm" onClick={() => onChange(null)}>Always applies</button>
      </div>
      {parent?.type === "enum" ? (
        <div className="ev-depends-values" role="group" aria-label={`Values of ${parent.key}`}>
          {(parent.values ?? []).filter((v) => v.value.trim()).map((v) => (
            <label key={v.value}><input type="checkbox" checked={d.values.includes(v.value)} onChange={() => toggle(v.value)} /> {v.label || v.value}</label>
          ))}
        </div>
      ) : parent?.type === "bool" ? (
        <div className="ev-depends-values" role="group" aria-label={`Values of ${parent.key}`}>
          {["true", "false"].map((v) => <label key={v}><input type="checkbox" checked={d.values.includes(v)} onChange={() => toggle(v)} /> {v === "true" ? "yes" : "no"}</label>)}
        </div>
      ) : (
        <input className="input" aria-label="Values, separated by commas" placeholder="values, separated by commas"
          value={d.values.join(", ")} onChange={(e) => onChange({ ...d, values: e.target.value.split(",").map((x) => x.trim()).filter(Boolean) })} />
      )}
      <p className="xs muted ev-depends-sentence">{dependsSentence(prop, props)}. Sent otherwise, it's a problem; required only then.</p>
    </div>
  );
}

// SchemaView: the schema, read-only (for people who can't edit it).
function SchemaView({ schema }: { schema: Draft }) {
  return (
    <section className="panel ev-section">
      <div className="panel-head">
        <h3>Schema</h3>
        <span className="small muted">{MODE_HELP[schema.mode]}</span>
      </div>
      {schema.props.length === 0 ? <Empty title="No properties described" /> : (
        <div className="table-scroll">
          <table className="table">
            <thead><tr><th>Property</th><th>Type</th><th>Rules</th></tr></thead>
            <tbody>
              {schema.props.map((p) => (
                <tr key={p.key}>
                  <td>
                    <code>{p.key}</code>{p.label && <span className="muted small"> · {p.label}</span>}
                    {p.path && p.path !== p.key && <div className="xs muted">at <code>{p.path}</code></div>}
                  </td>
                  <td className="small">
                    {TYPE_NAMES[p.type]}
                    {p.type === "enum" && <div className="xs muted">{(p.values ?? []).map((v) => v.label || v.value).join(", ")}</div>}
                  </td>
                  <td className="small">
                    {[p.required && "Required", p.personal && "Personal data", p.add_to_list && `Added to ${p.add_to_list}`, p.depends_on && dependsSentence(p, schema.props)]
                      .filter(Boolean).join(" · ") || <span className="faint">—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

// ProblemsPanel: what the schema caught in the last 14 days.
function ProblemsPanel({ checks, schema, onDescribe }: { checks?: SchemaChecks; schema: EventSchema | null; onDescribe: () => void }) {
  if (!schema) {
    return (
      <section className="panel ev-section">
        <Empty title="Not checked" action={<button className="btn" onClick={onDescribe}>Go to Schema</button>}>
          Describing this event with a schema turns on checks: events missing a required property, with a wrong type or a value that isn't allowed are counted here.
        </Empty>
      </section>
    );
  }
  if (schema.mode === "off") {
    return <section className="panel ev-section"><Empty title="Checks are off">This schema documents the event only. Switch it to Warn to count events that don't fit.</Empty></section>;
  }
  const c = checks ?? { days: [], checked: 0, bad: 0, refused: 0, problems: [], samples: [] };
  const maxDay = Math.max(1, ...c.days.map((d) => d.checked));
  return (
    <>
      <div className="ev-stats">
        <div className="panel ev-stat"><span className="xs muted">Checked, 14 days</span><strong>{c.checked.toLocaleString("en-IN")}</strong></div>
        <div className={`panel ev-stat${c.bad ? " warn" : ""}`}><span className="xs muted">With problems</span><strong>{c.bad.toLocaleString("en-IN")}</strong>
          {c.checked > 0 && <span className="xs muted">{formatShare(c.bad / c.checked)} of checked</span>}</div>
        <div className={`panel ev-stat${c.refused ? " bad" : ""}`}><span className="xs muted">Refused</span><strong>{c.refused.toLocaleString("en-IN")}</strong>
          <span className="xs muted">{schema.mode === "enforce" ? "by the server API" : "only in Enforce mode"}</span></div>
      </div>
      {c.days.length > 0 && (
        <section className="panel ev-section">
          <div className="panel-head"><h3>By day</h3></div>
          <div className="table-scroll">
            <table className="table ev-days">
              <thead><tr><th>Day</th><th className="num">Checked</th><th className="num">With problems</th><th className="num">Refused</th><th aria-hidden="true" /></tr></thead>
              <tbody>
                {c.days.map((d) => (
                  <tr key={d.day}>
                    <td className="small">{d.day}</td>
                    <td className="num">{d.checked.toLocaleString("en-IN")}</td>
                    <td className={`num${d.bad ? " ev-warn" : ""}`}>{d.bad.toLocaleString("en-IN")}</td>
                    <td className={`num${d.refused ? " ev-bad" : ""}`}>{d.refused.toLocaleString("en-IN")}</td>
                    <td className="ev-day-bar" aria-hidden="true">
                      <span className="ev-day-ok" style={{ width: `${((d.checked - d.bad) / maxDay) * 100}%` }} />
                      <span className="ev-day-bad" style={{ width: `${(d.bad / maxDay) * 100}%` }} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
      <section className="panel ev-section">
        <div className="panel-head"><h3>Most common problems</h3></div>
        {c.problems.length === 0 ? <Empty title="No problems">Every checked event fit the schema.</Empty> : (
          <table className="table">
            <thead><tr><th>Property</th><th>What's wrong</th><th className="num">Events</th></tr></thead>
            <tbody>
              {c.problems.map((p) => (
                <tr key={`${p.prop}:${p.code}`}><td><code>{p.prop}</code></td><td className="small">{problemWords(p.code)}</td><td className="num">{p.count.toLocaleString("en-IN")}</td></tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
      {c.samples.length > 0 && (
        <section className="panel ev-section">
          <div className="panel-head"><h3>Latest events with problems</h3><span className="xs muted">Personal properties are removed</span></div>
          <ul className="ev-samples">
            {c.samples.map((s) => (
              <li key={s.event_id}>
                <div className="row">
                  <span className="small">{timeAgo(s.at)}</span>
                  <span className="small">{s.external_id ? <>Contact <code>{s.external_id}</code></> : <span className="faint">anonymous</span>}</span>
                </div>
                <ul className="ev-violations">{s.violations.map((v, i) => <li key={i} className="small"><code>{v.prop}</code> {problemWords(v.code)}: {v.message}</li>)}</ul>
                <details>
                  <summary className="xs">Properties sent</summary>
                  <pre className="ev-pre">{prettyJSON(s.props)}</pre>
                </details>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

function prettyJSON(s: string): string {
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return s; }
}

// SendingPanel: how to send this event from the web SDK and the server API, with props built from the schema.
function SendingPanel({ name, schema }: { name: string; schema: Draft | null }) {
  const props = exampleProps({ props: schema?.props ?? [] });
  const json = JSON.stringify(props, null, 2);
  const web = `Pulse.track(${JSON.stringify(name)}, ${json});`;
  const server = JSON.stringify({ external_id: "user-123", name, props }, null, 2);
  return (
    <section className="panel ev-section">
      <div className="panel-head"><h3>Sending this event</h3></div>
      <div className="panel-body">
        {!schema?.props.length && <p className="small muted" style={{ marginTop: 0 }}>Describe the event's properties and these examples will include them.</p>}
        <h4 className="ev-code-title">From your website (web SDK)</h4>
        <pre className="ev-pre">{web}</pre>
        <h4 className="ev-code-title">From your server: <code>POST /v1/events</code></h4>
        <pre className="ev-pre">{server}</pre>
        <p className="xs muted">
          Example values are placeholders: send your own.
          {schema?.mode === "enforce" ? " This event is enforced: the server API refuses events that don't fit, with a 400 explaining why." : " Events that don't fit the schema are kept and counted under Problems."}
        </p>
      </div>
    </section>
  );
}
