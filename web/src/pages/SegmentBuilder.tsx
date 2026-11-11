// The segment builder: nested "Match ALL / ANY of the following" boxes of filters. Each filter is
// one row: [field] [operator for its type] [value input for its type] [×]; behaviour filters read
// as sentences ("Opened an email [the email: Welcome ▾] [at least ▾] [1] times in the last [30]
// days"). Filters are added from FilterPicker. Edits go through treeReducer by path.
import { useQuery } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { api, CHANNEL_NAMES, CHANNELS } from "../api";
import { buildCatalog, loadRecent, pushRecent, saveRecent, typeWord, type CatalogItem } from "../segmentCatalog";
import {
  canNest, EVENT_OPS, eventProps, fieldInfo, fieldType, guessPropType, KNOWN_EVENTS, MAX_GROUP_LEVELS, newGroup, newWhere, oldRange, OP_LABELS, opsFor, PICK_PROPS,
  pickNoun, propFieldType, schemaProp, treeReducer, valueShape, type AttrCond, type AttrOp, type EventCond, type EventOp, type FieldType, type Group, type Match, type Node,
  type Path, type TreeAction, type Where,
} from "../segmentRules";
import { FilterPicker, Icon } from "./FilterPicker";
import { useAttributes, useEventNames, useEventSchemas, useKnownValues, useRefObjects, useSegments, type RefItem } from "./segmentData";
import { useStages } from "./Stages";
import "./segment-builder.css";

type Ctx = {
  dispatch: (a: TreeAction) => void;
  items: CatalogItem[];
  recent: string[];
  remember: (id: string) => void;
  /** which picker is open: "add:<group path>" or "replace:<node path>" */
  open: string | null;
  setOpen: (k: string | null) => void;
  self?: number;
};
const BuilderCtx = createContext<Ctx | null>(null);
const useB = () => useContext(BuilderCtx)!;
const pk = (p: Path) => p.join(".");

/** GroupEditor edits a whole rule (the top group). It keeps no copy of the rule: every edit calls onChange. */
export function GroupEditor({ group, onChange, self }: { group: Group; onChange: (g: Group) => void; depth?: number; onRemove?: () => void; self?: number }) {
  const attrs = useAttributes().data;
  const items = useMemo(() => buildCatalog({ defs: attrs?.attributes ?? [], seen: attrs?.undefined ?? [] }), [attrs]);
  const [recent, setRecent] = useState(loadRecent);
  const [open, setOpen] = useState<string | null>(null);
  const latest = useRef(group);
  latest.current = group;
  const dispatch = useCallback((a: TreeAction) => onChange(treeReducer(latest.current, a)), [onChange]);
  const remember = useCallback((id: string) => setRecent((r) => { const n = pushRecent(r, id); saveRecent(n); return n; }), []);
  const ctx = useMemo<Ctx>(() => ({ dispatch, items, recent, remember, open, setOpen, self }), [dispatch, items, recent, remember, open, self]);
  return (
    <BuilderCtx.Provider value={ctx}>
      <div className="fb">
        <GroupBox group={group} path={[]} />
        <SharedLists />
      </div>
    </BuilderCtx.Provider>
  );
}

function MatchSelect({ value, onChange, label, root }: { value: Match; onChange: (m: Match) => void; label: string; root?: boolean }) {
  return (
    <select className="select inline fb-match" value={value} onChange={(e) => onChange(e.target.value as Match)} aria-label={label}>
      <option value="all">ALL</option>
      <option value="any">ANY</option>
      {(!root || value === "none") && <option value="none">NONE</option>}
    </select>
  );
}

function GroupBox({ group, path }: { group: Group; path: Path }) {
  const { dispatch } = useB();
  const root = path.length === 0;
  const join = group.match === "all" ? "AND" : "OR";
  const headId = useId();
  return (
    <section className={`fb-group ${group.match}${root ? " root" : ""}`} role="group" aria-labelledby={headId} style={{ ["--level" as string]: path.length }}>
      <header className="fb-group-head" id={headId}>
        <span>{root ? "Contacts who match" : group.match === "none" ? "Leave out contacts who match" : "Match"}</span>
        <MatchSelect root={root} value={group.match} onChange={(m) => dispatch({ type: "match", group: path, match: m })} label={root ? "Contacts must match all or any of the filters" : "This group matches all, any or none of its filters"} />
        <span>of the following{root ? ":" : ""}</span>
        {group.match === "none" && <span className="xs muted fb-none-hint">a contact matching any of these is excluded</span>}
        {!root && (
          <button type="button" className="btn ghost sm fb-remove-group" onClick={() => dispatch({ type: "remove", path })} aria-label="Remove this group and its filters">
            Remove group
          </button>
        )}
      </header>
      {group.children.length === 0 && (
        <p className="fb-empty small muted">{root ? "No filters yet. Add one to say who belongs in this segment." : "This group is empty. Add a filter to it, or remove it."}</p>
      )}
      <ol className="fb-list">
        {group.children.map((c, i) => (
          <li key={i} className="fb-item">
            {i > 0 && <div className={`fb-join ${group.match}`} aria-label={join === "AND" ? "and" : "or"}>{join}</div>}
            {c.kind === "group" ? <GroupBox group={c} path={[...path, i]} /> : <FilterRow node={c} path={[...path, i]} />}
          </li>
        ))}
      </ol>
      <AddControls group={group} path={path} />
    </section>
  );
}

function AddControls({ group, path }: { group: Group; path: Path }) {
  const { dispatch, items, recent, remember, open, setOpen } = useB();
  const key = `add:${pk(path)}`;
  const btn = useRef<HTMLButtonElement>(null);
  const nest = canNest(path);
  const close = () => { setOpen(null); btn.current?.focus(); };
  const add = (it: CatalogItem) => {
    remember(it.id);
    dispatch({ type: "add", group: path, node: it.make() });
    setOpen(null);
  };
  return (
    <div className="fb-add">
      <div className="row">
        <button ref={btn} type="button" className="btn sm" aria-expanded={open === key} aria-haspopup="dialog" onClick={() => setOpen(open === key ? null : key)}>
          + Add filter
        </button>
        {nest ? (
          <>
            <button type="button" className="btn ghost sm" onClick={() => {
              dispatch({ type: "add", group: path, node: newGroup(group.match === "all" ? "any" : "all") });
              setOpen(`add:${pk([...path, group.children.length])}`);
            }}>
              + Add group
            </button>
            {group.match !== "none" && (
              <button type="button" className="btn ghost sm" onClick={() => {
                dispatch({ type: "add", group: path, node: newGroup("none") });
                setOpen(`add:${pk([...path, group.children.length])}`);
              }}>
                + Exclude (none of)
              </button>
            )}
          </>
        ) : (
          <span className="xs muted">Groups can be nested {MAX_GROUP_LEVELS} levels deep, the most a segment can hold; this group is the deepest.</span>
        )}
      </div>
      {open === key && <FilterPicker items={items} recent={recent} onPick={add} onClose={close} nestingBlocked={!nest} />}
    </div>
  );
}

// ---- one filter ----

function FilterRow({ node, path }: { node: Exclude<Node, Group>; path: Path }) {
  const { dispatch, items, recent, remember, open, setOpen } = useB();
  const set = (n: Node) => dispatch({ type: "replace", path, node: n });
  const key = `replace:${pk(path)}`;
  const parentNest = canNest(path.slice(0, -1));
  const change = (it: CatalogItem) => {
    remember(it.id);
    set(it.make());
    setOpen(null);
  };
  const label = rowLabel(node, items);
  const remove = (
    <button type="button" className="fb-x" onClick={() => dispatch({ type: "remove", path })} aria-label={`Remove filter: ${label}`} title="Remove filter">×</button>
  );
  const fieldBtn = (text: ReactNode, icon: CatalogItem["icon"] = "text") => (
    <button type="button" className="fb-field" aria-expanded={open === key} aria-label={`Filter on ${label}. Change the filter`} onClick={() => setOpen(open === key ? null : key)}>
      <Icon name={icon} size={14} /> {text}
    </button>
  );
  let body: ReactNode;
  if (node.kind === "segment") body = <SegmentFields node={node} set={set} field={fieldBtn("Segment membership", "layers")} />;
  else if (node.kind === "tag") body = <TagFields node={node} set={set} field={fieldBtn("Tags", "tag")} />;
  else if (node.kind === "event") body = <EventFields node={node} set={set} field={fieldBtn} />;
  else body = <AttrFields node={node} set={set} field={fieldBtn} items={items} />;
  return (
    <div className="fb-row">
      <div className="fb-row-main">{body}{remove}</div>
      {open === key && <FilterPicker title="Change the filter" items={items} recent={recent} onPick={change} onClose={() => setOpen(null)} nestingBlocked={!parentNest} />}
    </div>
  );
}

function rowLabel(n: Exclude<Node, Group>, items: CatalogItem[]): string {
  if (n.kind === "segment") return "segment membership";
  if (n.kind === "tag") return "tags";
  if (n.kind === "event") return KNOWN_EVENTS[n.event]?.label ?? (n.event || "an event");
  return items.find((it) => it.key === n.attr.trim())?.label ?? (n.attr || "attribute");
}

// ---- attribute filters ----

function AttrFields({ node, set, field, items }: { node: AttrCond; set: (n: Node) => void; field: (t: ReactNode, i?: CatalogItem["icon"]) => ReactNode; items: CatalogItem[] }) {
  const defs = useAttributes().data?.attributes ?? [];
  const key = node.attr.trim();
  const item = items.find((it) => it.key === key);
  const info = fieldInfo(key);
  const type = fieldType(key, defs);
  const needsKey = !item || key === "company_";
  const fieldLabel = item ? (
    <>{item.label}{item.custom && <span className="fp-type">{typeWord(type)}</span>}</>
  ) : key.startsWith("company_") ? "Company field" : "Attribute";
  return (
    <>
      {field(fieldLabel, item?.icon ?? "text")}
      {needsKey && (
        <input
          className="input fb-key" list="fb-attr-keys" value={node.attr} placeholder={key.startsWith("company_") ? "company_industry" : "attribute name"}
          onChange={(e) => set({ ...node, attr: e.target.value })} aria-label="Attribute name"
        />
      )}
      {info?.pick ? <PickFields node={node} set={set} pick={info.pick} /> : (
        <CompareFields cmp={node} type={type} onChange={(c) => set({ ...node, ...c })} attrKey={key} options={defs.find((d) => d.key === key)?.options} />
      )}
    </>
  );
}

type Cmp = { op: AttrOp; value: string; value2?: string; values?: string[] };

/** opText words an operator for its menu ("is in the last … days"). */
function opText(op: AttrOp, type: FieldType): string {
  if (op === "within_days") return "is in the last … days";
  if (op === "older_than_days") return "is more than … days ago";
  if (type === "date" && (op === "empty" || op === "not_empty")) return op === "empty" ? "is not set" : "is set";
  return OP_LABELS[op];
}

/** CompareFields: the operator (suited to the field's type) and the value input it needs. */
type Option = { value: string; label?: string };

function CompareFields({ cmp, type, onChange, attrKey, event, prop, options }: { cmp: Cmp; type: FieldType; onChange: (c: Cmp) => void; attrKey: string; event?: string; prop?: boolean; options?: Option[] }) {
  const ops = opsFor(type, cmp.op, prop);
  const setOp = (op: AttrOp) => {
    const before = valueShape(cmp.op), after = valueShape(op);
    if (before === after) return onChange({ ...cmp, op });
    const start = after === "days" ? "30" : type === "boolean" && after === "one" ? "true" : "";
    onChange({ op, value: after === "list" ? "" : start, values: after === "list" ? (cmp.value ? [cmp.value] : []) : undefined, value2: undefined });
  };
  return (
    <>
      <select className="select fb-op" value={cmp.op} onChange={(e) => setOp(e.target.value as AttrOp)} aria-label="Comparison">
        {ops.map((o) => <option key={o} value={o}>{opText(o, type)}</option>)}
      </select>
      <ValueInput cmp={cmp} type={type} onChange={onChange} attrKey={attrKey} event={event} options={options} />
    </>
  );
}

function ValueInput({ cmp, type, onChange, attrKey, event, options }: { cmp: Cmp; type: FieldType; onChange: (c: Cmp) => void; attrKey: string; event?: string; options?: Option[] }) {
  const shape = valueShape(cmp.op);
  const [asked, setAsked] = useState(false);
  const listId = useId();
  const suggest = (type === "text" || type === "list" || (type === "enum" && !options?.length)) && (shape === "one" || shape === "list");
  const fetched = useKnownValues(attrKey, asked && suggest, event).data?.values ?? [];
  const known = options?.length ? options.map((o) => ({ value: o.value, count: -1, label: o.label })) : fetched.map((v) => ({ ...v, label: undefined as string | undefined }));
  if (shape === "none") return null;
  if (shape === "date") return <input className="input fb-date" type="date" value={cmp.value} onChange={(e) => onChange({ ...cmp, value: e.target.value })} aria-label="Date" />;
  if (shape === "days") {
    return (
      <span className="fb-inline">
        <input className="input fb-num" type="number" min={1} max={3650} step={1} value={cmp.value} onChange={(e) => onChange({ ...cmp, value: e.target.value })} aria-label="Number of days" />
        <span className="small">{cmp.op === "within_days" ? "days" : "days ago"}</span>
      </span>
    );
  }
  if (shape === "range" && type === "date") {
    return (
      <span className="fb-inline">
        <input className="input fb-date" type="date" value={cmp.value} onChange={(e) => onChange({ ...cmp, value: e.target.value })} aria-label="From" />
        <span className="small">and</span>
        <input className="input fb-date" type="date" value={cmp.value2 ?? ""} onChange={(e) => onChange({ ...cmp, value2: e.target.value })} aria-label="To" />
      </span>
    );
  }
  if (shape === "range") {
    return (
      <span className="fb-inline">
        <input className="input fb-num" inputMode="decimal" value={cmp.value} onChange={(e) => onChange({ ...cmp, value: e.target.value })} aria-label="From" placeholder="from" />
        <span className="small">and</span>
        <input className="input fb-num" inputMode="decimal" value={cmp.value2 ?? ""} onChange={(e) => onChange({ ...cmp, value2: e.target.value })} aria-label="To" placeholder="to" />
      </span>
    );
  }
  if (type === "boolean" && shape === "one") {
    return (
      <select className="select" value={cmp.value} onChange={(e) => onChange({ ...cmp, value: e.target.value })} aria-label="Value">
        <option value="true">yes</option>
        <option value="false">no</option>
      </select>
    );
  }
  if (type === "enum" && options?.length && shape === "one") {
    return (
      <select className="select" value={cmp.value} onChange={(e) => onChange({ ...cmp, value: e.target.value })} aria-label="Value">
        <option value="" disabled>choose</option>
        {!options.some((o) => o.value === cmp.value) && cmp.value && <option value={cmp.value}>{cmp.value}</option>}
        {options.map((o) => <option key={o.value} value={o.value}>{o.label ? `${o.label} (${o.value})` : o.value}</option>)}
      </select>
    );
  }
  const suggestions = <datalist id={listId}>{known.map((v) => <option key={v.value} value={v.value}>{v.label ?? (v.count >= 0 ? `${v.count} contacts` : "")}</option>)}</datalist>;
  if (shape === "list") {
    return <>{suggestions}<ChipsInput values={cmp.values ?? (cmp.value ? cmp.value.split(",").map((s) => s.trim()).filter(Boolean) : [])} list={listId} onFocus={() => setAsked(true)} onChange={(values) => onChange({ ...cmp, values, value: values.join(", ") })} /></>;
  }
  return (
    <>
      {suggestions}
      <input
        className="input fb-value" list={type === "text" ? listId : undefined} inputMode={type === "number" ? "decimal" : undefined}
        placeholder={type === "number" ? "number" : "value"} value={cmp.value} onFocus={() => setAsked(true)}
        onChange={(e) => onChange({ ...cmp, value: e.target.value })} aria-label="Value"
      />
    </>
  );
}

/** ChipsInput edits a list of values ("is one of"): Enter or comma adds the typed value, Backspace on
 * an empty box removes the last one. */
export function ChipsInput({ values, onChange, list, onFocus }: { values: string[]; onChange: (v: string[]) => void; list?: string; onFocus?: () => void }) {
  const [text, setText] = useState("");
  const add = (raw: string) => {
    const v = raw.trim();
    if (v && !values.includes(v)) onChange([...values, v]);
    setText("");
  };
  return (
    <span className="fb-chips" role="group" aria-label="Values">
      {values.map((v) => (
        <span className="fb-chip" key={v}>
          {v}
          <button type="button" onClick={() => onChange(values.filter((x) => x !== v))} aria-label={`Remove ${v}`}>×</button>
        </span>
      ))}
      <input
        className="fb-chips-input" list={list} value={text} placeholder={values.length ? "add another" : "type a value, Enter"} onFocus={onFocus}
        onChange={(e) => {
          const t = e.target.value;
          if (t.endsWith(",")) add(t.slice(0, -1));
          else setText(t);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") { e.preventDefault(); add(text); }
          else if (e.key === "Backspace" && !text && values.length) onChange(values.slice(0, -1));
        }}
        onBlur={() => text && add(text)}
        aria-label="Add a value"
      />
    </span>
  );
}

/** PickFields: stage (by name), company and owner (by id), chosen from the workspace's own. */
function PickFields({ node, set, pick }: { node: AttrCond; set: (n: Node) => void; pick: "stage" | "company" | "owner" }) {
  const stages = useStages().data ?? [];
  const { lists } = useRefObjects();
  const items: { value: string; label: string }[] =
    pick === "stage" ? stages.map((s) => ({ value: s.name, label: s.name }))
      : (pick === "company" ? lists.companies : lists.users).map((x) => ({ value: String(x.id), label: x.name }));
  const noun = pick;
  const op = node.op === "exists" || node.op === "not_exists" || node.op === "ne" ? node.op : "eq";
  const known = items.some((x) => x.value === node.value);
  const words: Record<string, Record<string, string>> = {
    stage: { eq: "is", ne: "is not", exists: "is set (any stage)", not_exists: "is not set" },
    company: { eq: "is", ne: "is not", exists: "is set (any company)", not_exists: "is not set" },
    owner: { eq: "is", ne: "is not", exists: "is set (anyone)", not_exists: "is not set (unassigned)" },
  };
  return (
    <>
      <select className="select fb-op" value={op} onChange={(e) => set({ ...node, op: e.target.value as AttrOp, value: e.target.value === "exists" || e.target.value === "not_exists" ? "" : node.value })} aria-label="Comparison">
        {(["eq", "ne", "exists", "not_exists"] as const).map((o) => <option key={o} value={o}>{words[pick][o]}</option>)}
      </select>
      {(op === "eq" || op === "ne") && (
        <select className="select" value={node.value} onChange={(e) => set({ ...node, value: e.target.value })} aria-label={`Choose a ${noun}`}>
          <option value="" disabled>Choose {noun === "owner" ? "a teammate" : `a ${noun}`}</option>
          {!known && node.value && <option value={node.value}>{pick === "stage" ? node.value : `#${node.value}`}</option>}
          {items.map((x) => <option key={x.value} value={x.value}>{x.label}</option>)}
        </select>
      )}
      {items.length === 0 && <span className="xs muted">{pick === "stage" ? "No stages yet: add them under Settings → Stages." : pick === "company" ? "No companies yet." : "No teammates to list (or you can't view users)."}</span>}
    </>
  );
}

function SegmentFields({ node, set, field }: { node: Extract<Node, { kind: "segment" }>; set: (n: Node) => void; field: ReactNode }) {
  const { self } = useB();
  const segs = useSegments(0).data?.segments ?? [];
  const rules = segs.filter((s) => s.kind !== "static" && s.id !== self);
  const lists = segs.filter((s) => s.kind === "static");
  return (
    <>
      {field}
      <select className="select fb-op" value={node.op} onChange={(e) => set({ ...node, op: e.target.value as "in" | "not_in" })} aria-label="In or not in">
        <option value="in">is in</option>
        <option value="not_in">is not in</option>
      </select>
      <select className="select" value={node.segment || ""} onChange={(e) => set({ ...node, segment: Number(e.target.value) })} aria-label="Segment">
        <option value="" disabled>Choose a segment</option>
        {lists.length > 0 && <optgroup label="Static lists">{lists.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</optgroup>}
        {rules.length > 0 && <optgroup label="Segments">{rules.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</optgroup>}
      </select>
    </>
  );
}

function TagFields({ node, set, field }: { node: Extract<Node, { kind: "tag" }>; set: (n: Node) => void; field: ReactNode }) {
  return (
    <>
      {field}
      <select className="select fb-op" value={node.op} onChange={(e) => set({ ...node, op: e.target.value as "has" | "not_has" })} aria-label="Has the tag or not">
        <option value="has">include</option>
        <option value="not_has">don't include</option>
      </select>
      <input className="input fb-value" list="fb-tag-names" placeholder="tag, e.g. vip" value={node.tag} onChange={(e) => set({ ...node, tag: e.target.value })} aria-label="Tag" />
    </>
  );
}

// ---- behaviour (event) filters ----

const EVENT_GROUPS: { label: string; events: string[] }[] = [
  { label: "Email", events: ["email_sent", "email_opened", "email_clicked", "email_bounced"] },
  { label: "SMS, WhatsApp, RCS", events: ["sms_sent", "sms_delivered", "sms_clicked", "sms_failed", "whatsapp_delivered", "whatsapp_read", "rcs_read", "rcs_clicked"] },
  { label: "Push, inbox and in-app", events: ["push_sent", "inbox_opened", "inbox_clicked", "inapp_shown", "inapp_clicked"] },
  { label: "Web", events: ["page_view", "session_start", "landing_page_viewed"] },
  { label: "Forms, assets, journeys, consent", events: ["form_submitted", "asset_downloaded", "journey_entered", "journey_completed", "unsubscribed"] },
];
const OTHER = "__other";

function EventFields({ node, set, field }: { node: EventCond; set: (n: Node) => void; field: (t: ReactNode, i?: CatalogItem["icon"]) => ReactNode }) {
  const ev = node.event.trim();
  const known = KNOWN_EVENTS[ev];
  const [otherChosen, setOther] = useState(false);
  const other = otherChosen || !known;
  const setWhere = (i: number, w: Where) => set({ ...node, where: node.where.map((x, j) => (j === i ? w : x)) });
  const removeWhere = (i: number) => set({ ...node, where: node.where.filter((_, j) => j !== i) });
  const inline = node.where.map((w, i) => ({ w, i })).filter(({ w }) => PICK_PROPS[w.prop.trim()] && (w.op === "eq" || w.op === "ne"));
  const below = node.where.map((w, i) => ({ w, i })).filter(({ w }) => !(PICK_PROPS[w.prop.trim()] && (w.op === "eq" || w.op === "ne")));
  const propList = useId();
  const schemaProps = useEventSchemas().data?.schemas.find((x) => x.name === ev)?.props ?? [];
  return (
    <div className="fb-sentence">
      <div className="fb-sentence-line">
        {field(<span className="sr-only">Behaviour</span>, "bolt")}
        <select
          className="select fb-event" value={other ? OTHER : ev}
          onChange={(e) => {
            if (e.target.value === OTHER) { setOther(true); set({ ...node, event: "", where: [] }); return; }
            setOther(false);
            // Keep the source filter when switching between message events; drop the rest.
            const keep = KNOWN_EVENTS[e.target.value]?.message && known?.message ? node.where.filter((w) => ["broadcast_id", "journey_id", "template_id"].includes(w.prop)) : [];
            set({ ...node, event: e.target.value, where: keep });
          }}
          aria-label="Event"
        >
          {EVENT_GROUPS.map((g) => (
            <optgroup key={g.label} label={g.label}>{g.events.map((e) => <option key={e} value={e}>{KNOWN_EVENTS[e].label}</option>)}</optgroup>
          ))}
          <option value={OTHER}>Another event…</option>
        </select>
        {other && <input className="input fb-key" list="fb-event-names" placeholder="event name, e.g. purchase" value={node.event} onChange={(e) => set({ ...node, event: e.target.value })} aria-label="Event name" />}
        {inline.map(({ w, i }) => <SourcePick key={i} event={ev} w={w} onChange={(x) => setWhere(i, x)} onRemove={() => removeWhere(i)} />)}
        <select className="select fb-op" value={node.op} onChange={(e) => set({ ...node, op: e.target.value as EventOp })} aria-label="How often">
          {EVENT_OPS.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
        </select>
        {node.op !== "none" && (
          <span className="fb-inline">
            <input className="input fb-num" type="number" min={0} value={node.count} onChange={(e) => set({ ...node, count: Number(e.target.value) })} aria-label="Number of times" />
            <span className="small">{node.count === 1 ? "time" : "times"}</span>
          </span>
        )}
        <WindowFields node={node} set={set} />
      </div>
      {oldRange(node.from) && (
        <p className="xs muted fb-sweep-note">This range starts more than a year ago, so it is checked by the periodic sweep (every few minutes) rather than the moment an event arrives.</p>
      )}
      <datalist id={propList}>
        {schemaProps.map((p) => <option key={p.path || p.key} value={p.path || p.key}>{p.label || p.key}</option>)}
        {eventProps(ev).filter((p) => !schemaProps.some((x) => (x.path || x.key) === p)).map((p) => <option key={p} value={p} />)}
      </datalist>
      {below.map(({ w, i }, n) => (
        <div className="fb-where" key={i}>
          <span className="small fb-where-word">{n === 0 ? "where" : "and"}</span>
          <WhereFields event={ev} w={w} list={propList} onChange={(x) => setWhere(i, x)} schema={schemaProps} />
          <button type="button" className="fb-x" onClick={() => removeWhere(i)} aria-label={`Remove property filter ${w.prop || ""}`}>×</button>
        </div>
      ))}
      {node.where.length < 5 && (
        <div className="fb-where-add">
          <button type="button" className="btn ghost sm" onClick={() => set({ ...node, where: [...node.where, newWhere()] })}>+ Only count events where…</button>
          {known?.message && !node.where.some((w) => PICK_PROPS[w.prop]) && (
            <button type="button" className="btn ghost sm" onClick={() => set({ ...node, where: [...node.where, { prop: "template_id", op: "eq", value: "" }] })}>+ A specific message</button>
          )}
        </div>
      )}
    </div>
  );
}

/** SourcePick: an inline "[the email ▾] [Welcome ▾]" choice for a property that names an object. */
function SourcePick({ event, w, onChange, onRemove }: { event: string; w: Where; onChange: (w: Where) => void; onRemove: () => void }) {
  const { lists } = useRefObjects();
  const prop = w.prop.trim();
  const pick = PICK_PROPS[prop];
  const message = !!KNOWN_EVENTS[event]?.message;
  const channel = KNOWN_EVENTS[event]?.channel;
  let items: RefItem[] = pick.refs ? lists[pick.refs] : [];
  if (prop === "template_id" && channel) items = items.filter((t) => !t.channel || t.channel === channel);
  const has = items.some((x) => String(x.id) === w.value);
  const noun = pickNoun(prop, event);
  return (
    <span className="fb-inline fb-pick">
      {message ? (
        <select className="select" value={`${w.op}:${prop}`} onChange={(e) => { const [op, p] = e.target.value.split(":"); onChange({ prop: p, op: op as AttrOp, value: p === prop ? w.value : "" }); }} aria-label="Which messages">
          <option value="eq:template_id">{`the ${pickNoun("template_id", event)}`}</option>
          <option value="eq:broadcast_id">from campaign</option>
          <option value="ne:broadcast_id">not from campaign</option>
          <option value="eq:journey_id">from journey</option>
          <option value="ne:journey_id">not from journey</option>
        </select>
      ) : (
        <select className="select" value={w.op} onChange={(e) => onChange({ ...w, op: e.target.value as AttrOp })} aria-label="Comparison">
          <option value="eq">{`the ${noun}`}</option>
          <option value="ne">{`not the ${noun}`}</option>
        </select>
      )}
      <select className="select" value={w.value} onChange={(e) => onChange({ ...w, value: e.target.value })} aria-label={`Choose ${noun}`}>
        <option value="" disabled>{`Choose ${/^[aeiou]/i.test(noun) ? "an" : "a"} ${noun}`}</option>
        {!has && w.value && <option value={w.value}>#{w.value}</option>}
        {items.map((x) => <option key={x.id} value={String(x.id)}>{x.name}</option>)}
      </select>
      {items.length === 0 && <span className="xs muted">{`No ${noun}s yet`}</span>}
      <button type="button" className="fb-x sm" onClick={onRemove} aria-label={`Any ${noun}`} title={`Any ${noun}`}>×</button>
    </span>
  );
}

/** PropertyFilters edits a list of property filters on one event outside a segment (a journey's
 * event trigger), typed by the event's schema like the builder's own. */
export function PropertyFilters({ event, where, onChange }: { event: string; where: Where[]; onChange: (w: Where[]) => void }) {
  const list = useId();
  const schemaProps = useEventSchemas().data?.schemas.find((x) => x.name === event.trim())?.props ?? [];
  return (
    <div className="fb">
      <datalist id={list}>
        {schemaProps.map((p) => <option key={p.path || p.key} value={p.path || p.key}>{p.label || p.key}</option>)}
        {eventProps(event).filter((p) => !schemaProps.some((x) => (x.path || x.key) === p)).map((p) => <option key={p} value={p} />)}
      </datalist>
      {where.map((w, i) => (
        <div className="fb-where" key={i}>
          <span className="small fb-where-word">{i === 0 ? "where" : "and"}</span>
          <WhereFields event={event.trim()} w={w} list={list} schema={schemaProps} onChange={(x) => onChange(where.map((y, j) => (j === i ? x : y)))} />
          <button type="button" className="fb-x" onClick={() => onChange(where.filter((_, j) => j !== i))} aria-label={`Remove property filter ${w.prop || ""}`}>×</button>
        </div>
      ))}
      {where.length < 5 && <button type="button" className="btn ghost sm" onClick={() => onChange([...where, newWhere()])}>+ Only when the event's properties match…</button>}
    </div>
  );
}

/** WindowFields: "in the last N days", "between" two dates, or "since" a date. */
function WindowFields({ node, set }: { node: EventCond; set: (n: Node) => void }) {
  const mode = node.from === undefined ? "days" : node.to ? "between" : "since";
  const today = new Date().toISOString().slice(0, 10);
  const monthAgo = new Date(Date.now() - 30 * 86_400_000).toISOString().slice(0, 10);
  const setMode = (m: string) => {
    if (m === "days") set({ ...node, from: undefined, to: undefined });
    else if (m === "since") set({ ...node, from: node.from || monthAgo, to: "" });
    else set({ ...node, from: node.from || monthAgo, to: node.to || today });
  };
  return (
    <span className="fb-inline">
      <select className="select fb-op" value={mode} onChange={(e) => setMode(e.target.value)} aria-label="When">
        <option value="days">in the last</option>
        <option value="between">between</option>
        <option value="since">since</option>
      </select>
      {mode === "days" ? (
        <>
          <input className="input fb-num" type="number" min={1} max={365} value={node.days} onChange={(e) => set({ ...node, days: Number(e.target.value) })} aria-label="Days" />
          <span className="small">days</span>
        </>
      ) : (
        <>
          <input className="input fb-date" type="date" min="2000-01-01" value={node.from ?? ""} onChange={(e) => set({ ...node, from: e.target.value })} aria-label="First day" />
          {mode === "between" && (
            <>
              <span className="small">and</span>
              <input className="input fb-date" type="date" min={node.from} value={node.to ?? ""} onChange={(e) => set({ ...node, to: e.target.value })} aria-label="Last day" />
            </>
          )}
        </>
      )}
    </span>
  );
}

/** WhereFields edits one property filter: the property (a name, product.code, or items[].code for
 * any item) and a comparison typed by the event's schema when it has one. */
function WhereFields({ event, w, list, onChange, schema }: { event: string; w: Where; list: string; onChange: (w: Where) => void; schema?: { key: string; path?: string; label?: string; type: string; values?: Option[] }[] }) {
  const prop = w.prop.trim();
  const propInput = <input className="input fb-key" list={list} placeholder="property, e.g. amount or items[].code" value={w.prop} onChange={(e) => onChange({ ...w, prop: e.target.value })} aria-label="Event property" />;
  if (event === "unsubscribed" && (prop === "channel" || prop === "method") && w.op === "eq") {
    return (
      <>
        {propInput}
        <span className="small">is</span>
        <select className="select" value={w.value} onChange={(e) => onChange({ ...w, value: e.target.value })} aria-label={prop === "channel" ? "Channel" : "How"}>
          <option value="" disabled>choose</option>
          {prop === "channel"
            ? [...CHANNELS.map((c) => <option key={c} value={c}>{CHANNEL_NAMES[c]}</option>), <option key="all" value="all">Everything</option>]
            : [<option key="m" value="manual">manually (by your team or the API)</option>, <option key="s" value="self">by themselves (link, keyword, preference center)</option>]}
        </select>
      </>
    );
  }
  const sp = schemaProp(schema, prop);
  const type: FieldType = sp ? propFieldType(sp.type) : guessPropType(prop);
  return (
    <>
      {propInput}
      <CompareFields cmp={w} type={type} prop onChange={(c) => onChange({ ...w, ...c })} attrKey={prop} event={event || undefined} options={sp?.values} />
    </>
  );
}

// Shared datalists for attribute keys, tags and event names; rendered once per builder.
function SharedLists() {
  const attrs = useAttributes().data;
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/tags"), staleTime: 30_000 }).data?.tags ?? [];
  const names = useEventNames().data?.names ?? [];
  const keys = [...new Set([...(attrs?.attributes.map((a) => a.key) ?? []), ...(attrs?.undefined ?? [])])];
  return (
    <>
      <datalist id="fb-attr-keys">{keys.map((k) => <option key={k} value={k} />)}</datalist>
      <datalist id="fb-tag-names">{tags.map((t) => <option key={t} value={t} />)}</datalist>
      <datalist id="fb-event-names">{names.filter((n) => !KNOWN_EVENTS[n]).map((n) => <option key={n} value={n} />)}</datalist>
    </>
  );
}
