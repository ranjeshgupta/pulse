import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, CHANNEL_NAMES, CHANNELS, type AttributeDef, type Broadcast, type Form, type Journey, type Segment } from "../api";
import {
  ATTR_OPS, BUILTIN_ATTRS, DATE_OPS, EVENT_OPS, KNOWN_EVENTS, PICK_ATTRS, PICK_PROPS, PRESETS, describe, eventProps, isDateAttr, isDateOp, newGroup, newWhere,
  problems, rootFromRule, toRule, type AttrOp, type Cond, type EventOp, type Group, type Node, type RefNames, type Where,
} from "../segmentRules";
import { Empty, ErrorNote, Field, LiveNumber, PageHead, Status, timeAgo, useToast } from "../ui";
import { useCompanyOptions } from "./Companies";
import { useStages } from "./Stages";
import "./segments.css";

/** Every segment and list of the workspace, polled so sizes stay live. */
export function useSegments(poll = 3000) {
  return useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments"), refetchInterval: poll });
}

/** Segment and list names by id, so rules can describe references in words. */
export function useSegmentNames() {
  const segs = useSegments(0).data?.segments ?? [];
  return useMemo(() => Object.fromEntries(segs.map((s) => [s.id, s.name])) as Record<number, string>, [segs]);
}

/** Forms, campaigns (broadcasts) and journeys rules can point at, with names by id for describe().
 * A list the user may not view just stays empty, and ids show as numbers. */
export function useRefObjects() {
  const opts = { staleTime: 30_000, retry: false } as const;
  const forms = useQuery({ queryKey: ["forms"], queryFn: () => api.get<{ forms: Form[] }>("/v1/forms"), ...opts }).data?.forms;
  const broadcasts = useQuery({ queryKey: ["broadcasts"], queryFn: () => api.get<{ broadcasts: Broadcast[] }>("/v1/broadcasts"), ...opts }).data?.broadcasts;
  const journeys = useQuery({ queryKey: ["journeys"], queryFn: () => api.get<{ journeys: Journey[] }>("/v1/journeys"), ...opts }).data?.journeys;
  const companies = useCompanyOptions().data;
  return useMemo(() => {
    const byId = (xs?: { id: number; name: string }[]) => Object.fromEntries((xs ?? []).map((x) => [x.id, x.name])) as Record<number, string>;
    const lists: Record<keyof RefNames, { id: number; name: string }[]> = { forms: forms ?? [], broadcasts: broadcasts ?? [], journeys: journeys ?? [], companies: companies ?? [] };
    const names: RefNames = { forms: byId(forms), broadcasts: byId(broadcasts), journeys: byId(journeys), companies: byId(companies) };
    return { lists, names };
  }, [forms, broadcasts, journeys, companies]);
}

/** Attribute definitions plus keys seen on contacts, for autocomplete and typed inputs. */
export function useAttributes() {
  return useQuery({ queryKey: ["attributes"], queryFn: () => api.get<{ attributes: AttributeDef[]; undefined: string[] }>("/v1/attributes"), staleTime: 30_000 });
}

export function SegmentList() {
  // Sizes come straight from the live bitmaps; polling shows contacts moving in and out.
  const q = useSegments();
  const all = q.data?.segments ?? [];
  const segs = all.filter((s) => s.kind !== "static");
  const names = Object.fromEntries(all.map((s) => [s.id, s.name])) as Record<number, string>;
  const refs = useRefObjects().names;
  return (
    <div className="page">
      <PageHead
        title="Segments"
        intro="Contacts join and leave a segment the moment an event or profile change makes them match. Sizes here update live."
        actions={<Link className="btn primary" to="/segments/new">New segment</Link>}
      />
      <ErrorNote error={q.error} />
      <div className="panel">
        {segs.length === 0 && !q.isLoading ? (
          <Empty title="No segments yet" action={<Link className="btn primary" to="/segments/new">Create a segment</Link>}>
            Describe who you want to reach, like "bought at least twice in 30 days and lives in Pune".
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th>Who's in it</th><th className="num">Contacts</th><th>State</th></tr></thead>
            <tbody>
              {segs.map((s) => (
                <tr key={s.id}>
                  <td><Link to={`/segments/${s.id}`}>{s.name}</Link></td>
                  <td className="muted" style={{ maxWidth: 520 }}>{describe(s.rule, names, refs)}</td>
                  <td className="num"><LiveNumber value={s.size} /></td>
                  <td><Status value={s.status} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

export function SegmentEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const existing = useQuery({ queryKey: ["segment", id], queryFn: () => api.get<Segment>(`/v1/segments/${id}`), enabled: !isNew, refetchInterval: 3000 });
  const [name, setName] = useState("");
  const [root, setRoot] = useState<Group>(newGroup());
  const [loaded, setLoaded] = useState(isNew);

  useEffect(() => {
    if (existing.data && !loaded) {
      setName(existing.data.name);
      setRoot(rootFromRule(existing.data.rule ?? undefined));
      setLoaded(true);
    }
  }, [existing.data, loaded]);

  const names = useSegmentNames();
  const refs = useRefObjects().names;
  const issues = problems(root);
  const rule = useMemo(() => (issues.length ? null : toRule(root)), [root, issues.length]);
  const ruleKey = JSON.stringify(rule);
  const [debounced, setDebounced] = useState(ruleKey);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(ruleKey), 400);
    return () => clearTimeout(t);
  }, [ruleKey]);
  const preview = useQuery({
    queryKey: ["segment-preview", debounced],
    queryFn: () => api.post<{ count: number }>("/v1/segments/preview", { rule: JSON.parse(debounced) }),
    enabled: debounced !== "null",
  });

  const save = useMutation({
    mutationFn: () => (isNew ? api.post<{ id: number }>("/v1/segments", { name, rule }) : api.put<{ id: number }>(`/v1/segments/${id}`, { name, rule })),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["segments"] });
      toast({ text: isNew ? "Segment created. Pulse is filling it now." : "Segment saved. Pulse is rebuilding it now." });
      nav(`/segments/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/segments/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["segments"] }); toast({ text: "Segment deleted" }); nav("/segments"); },
  });

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/segments">Segments</Link>}
        title={isNew ? "New segment" : name || "Segment"}
        actions={<>
          {!isNew && <button className="btn danger" onClick={() => confirm("Delete this segment? Journeys that use it stop enrolling.") && remove.mutate()}>Delete</button>}
          <button className="btn primary" disabled={!rule || !name || save.isPending} onClick={() => save.mutate()}>{isNew ? "Create segment" : "Save segment"}</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error ?? existing.error} />
      <div className="editor-split">
        <div>
          <Field label="Name"><input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Repeat buyers in Pune" /></Field>
          <div className="panel"><div className="panel-body">
            <GroupEditor group={root} onChange={setRoot} depth={0} self={isNew ? undefined : Number(id)} />
          </div></div>
          {issues.length > 0 && <ul className="small muted" style={{ marginTop: 10 }}>{[...new Set(issues)].map((p) => <li key={p}>{p}</li>)}</ul>}
        </div>
        <aside className="panel" style={{ position: "sticky", top: 20 }}>
          <div className="panel-body">
            <p className="small muted">Would match right now</p>
            {/* Never show 0 for "not counted yet": until a count for this exact rule arrives, show the
                previous count dimmed, or an ellipsis on first load. */}
            <div className="preview-count" style={{ opacity: debounced !== ruleKey || preview.isFetching ? 0.45 : 1 }} aria-busy={debounced !== ruleKey || preview.isFetching}>
              {!rule ? "–" : preview.data ? <LiveNumber value={preview.data.count} /> : "…"}
            </div>
            <p className="xs muted" style={{ marginTop: 4 }}>contacts, counted from all recorded events</p>
            {!isNew && existing.data && (
              <dl className="kv" style={{ marginTop: 18 }}>
                <dt>In segment now</dt><dd><LiveNumber value={existing.data.size} /></dd>
                <dt>State</dt><dd><Status value={existing.data.status} /></dd>
                <dt>Last rebuilt</dt><dd>{timeAgo(existing.data.built_at)}</dd>
              </dl>
            )}
            {rule && <p className="small" style={{ marginTop: 18 }}>{describe(rule, names, refs)}</p>}
            {preview.error && <p className="xs" style={{ color: "var(--danger)", marginTop: 8 }}>{(preview.error as Error).message}</p>}
          </div>
        </aside>
      </div>
    </div>
  );
}

export function GroupEditor({ group, onChange, depth, onRemove, self }: { group: Group; onChange: (g: Group) => void; depth: number; onRemove?: () => void; self?: number }) {
  const setChild = (i: number, n: Node) => onChange({ ...group, children: group.children.map((c, j) => (j === i ? n : c)) });
  const removeChild = (i: number) => onChange({ ...group, children: group.children.filter((_, j) => j !== i) });
  const add = (n: Node) => onChange({ ...group, children: [...group.children, n] });
  return (
    <div className={`rule-group ${group.match}`}>
      <div className="rule-group-head">
        Contacts who match
        <select className="select inline" value={group.match} onChange={(e) => onChange({ ...group, match: e.target.value as "all" | "any" })} aria-label="Match all or any">
          <option value="all">all</option>
          <option value="any">any</option>
        </select>
        of these
        {onRemove && <button className="btn ghost sm" onClick={onRemove}>Remove group</button>}
      </div>
      {group.children.map((c, i) =>
        c.kind === "group" ? (
          <GroupEditor key={i} group={c} depth={depth + 1} self={self} onChange={(g) => setChild(i, g)} onRemove={() => removeChild(i)} />
        ) : (
          <CondEditor key={i} cond={c} self={self} onChange={(n) => setChild(i, n)} onRemove={() => removeChild(i)} />
        ),
      )}
      <AddCondition onAdd={add} group={depth < 3 ? () => add(newGroup(group.match === "all" ? "any" : "all")) : undefined} />
      <SuggestionLists />
    </div>
  );
}

// AddCondition: one button that opens the grouped list of ready-made conditions (PRESETS).
// It opens in place rather than as a popover, so it also fits the journey inspector.
function AddCondition({ onAdd, group }: { onAdd: (n: Node) => void; group?: () => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    const click = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as globalThis.Node) && setOpen(false);
    window.addEventListener("keydown", key);
    document.addEventListener("mousedown", click);
    return () => { window.removeEventListener("keydown", key); document.removeEventListener("mousedown", click); };
  }, [open]);
  return (
    <div className="rule-add" ref={ref}>
      <div className="row">
        <button className="btn sm" aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "Close" : "Add condition"}</button>
        {group && <button className="btn ghost sm" onClick={group}>Add group</button>}
      </div>
      {open && (
        <div className="rule-menu" role="menu" aria-label="Choose a condition">
          {PRESETS.map((g) => (
            <div className="rule-menu-group" key={g.group}>
              <h5>{g.group}</h5>
              {g.items.map((p) => (
                <button key={p.id} role="menuitem" className="rule-menu-item" onClick={() => { onAdd(p.make()); setOpen(false); }}>
                  <span>{p.label}</span>
                  <small>{p.hint}</small>
                </button>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// Shared datalists for attribute names, tags and event names; rendered once per group, ids are stable.
function SuggestionLists() {
  const attrs = useAttributes().data;
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/tags"), staleTime: 30_000 }).data?.tags ?? [];
  const builtin = new Set(BUILTIN_ATTRS.map((a) => a.key));
  const keys = [...(attrs?.attributes.map((a) => a.key) ?? []), ...(attrs?.undefined ?? [])].filter((k) => !builtin.has(k));
  return (
    <>
      <datalist id="attr-keys">
        {keys.map((k) => <option key={k} value={k} />)}
        {BUILTIN_ATTRS.map((a) => <option key={a.key} value={a.key}>{a.label}</option>)}
      </datalist>
      <datalist id="tag-names">{tags.map((t) => <option key={t} value={t} />)}</datalist>
      <datalist id="event-names">{Object.entries(KNOWN_EVENTS).map(([k, e]) => <option key={k} value={k}>{e.label}</option>)}</datalist>
    </>
  );
}

function CondEditor({ cond, onChange, onRemove, self }: { cond: Cond; onChange: (c: Cond) => void; onRemove: () => void; self?: number }) {
  const segs = useSegments(0).data?.segments ?? [];
  const defs = useAttributes().data?.attributes ?? [];
  const remove = <button className="btn ghost sm" onClick={onRemove} aria-label="Remove condition">Remove</button>;
  if (cond.kind === "segment") {
    const rules = segs.filter((s) => s.kind !== "static" && s.id !== self);
    const lists = segs.filter((s) => s.kind === "static");
    return (
      <div className="rule-row">
        <select className="select" value={cond.op} onChange={(e) => onChange({ ...cond, op: e.target.value as "in" | "not_in" })} aria-label="In or not in">
          <option value="in">Is in</option>
          <option value="not_in">Is not in</option>
        </select>
        <select className="select" value={cond.segment || ""} onChange={(e) => onChange({ ...cond, segment: Number(e.target.value) })} aria-label="Segment or list">
          <option value="" disabled>Choose a segment or list</option>
          {lists.length > 0 && <optgroup label="Lists">{lists.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</optgroup>}
          {rules.length > 0 && <optgroup label="Segments">{rules.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</optgroup>}
        </select>
        {remove}
      </div>
    );
  }
  if (cond.kind === "tag") {
    return (
      <div className="rule-row">
        <select className="select" value={cond.op} onChange={(e) => onChange({ ...cond, op: e.target.value as "has" | "not_has" })} aria-label="Has tag or not">
          <option value="has">Is tagged</option>
          <option value="not_has">Is not tagged</option>
        </select>
        <input className="input" list="tag-names" placeholder="tag, e.g. vip" value={cond.tag} onChange={(e) => onChange({ ...cond, tag: e.target.value })} aria-label="Tag" />
        {remove}
      </div>
    );
  }
  if (cond.kind === "event") {
    const setWhere = (i: number, w: Where) => onChange({ ...cond, where: cond.where.map((x, j) => (j === i ? w : x)) });
    const known = KNOWN_EVENTS[cond.event.trim()];
    const propList = `props-${cond.event.trim() || "any"}`;
    return (
      <div className="rule-cond">
        <div className="rule-row">
          <span className="small">Did</span>
          <input className="input" list="event-names" placeholder="event, e.g. purchase" value={cond.event} onChange={(e) => onChange({ ...cond, event: e.target.value })} aria-label="Event name" />
          {known && <span className="xs muted">{known.label.toLowerCase()}</span>}
          <select className="select" value={cond.op} onChange={(e) => onChange({ ...cond, op: e.target.value as EventOp })} aria-label="How often">
            {EVENT_OPS.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
          </select>
          {cond.op !== "none" && <><input className="input" style={{ width: 76, minWidth: 0 }} type="number" min={0} value={cond.count} onChange={(e) => onChange({ ...cond, count: Number(e.target.value) })} aria-label="Times" /><span className="small">times</span></>}
          <span className="small">in the last</span>
          <input className="input" style={{ width: 76, minWidth: 0 }} type="number" min={1} max={365} value={cond.days} onChange={(e) => onChange({ ...cond, days: Number(e.target.value) })} aria-label="Days" />
          <span className="small">days</span>
          {remove}
        </div>
        <datalist id={propList}>{eventProps(cond.event.trim()).map((p) => <option key={p} value={p} />)}</datalist>
        {cond.where.map((w, i) => (
          <div className="rule-row rule-where" key={i}>
            <span className="small">{i === 0 ? "where" : "and"}</span>
            <WhereFields event={cond.event.trim()} w={w} list={propList} onChange={(x) => setWhere(i, x)} />
            <button className="btn ghost sm" onClick={() => onChange({ ...cond, where: cond.where.filter((_, j) => j !== i) })} aria-label="Remove filter">Remove</button>
          </div>
        ))}
        {cond.where.length < 5 && (
          <button className="btn ghost sm rule-where-add" onClick={() => onChange({ ...cond, where: [...cond.where, newWhere()] })}>Only count events where…</button>
        )}
      </div>
    );
  }
  const def = defs.find((d) => d.key === cond.attr.trim());
  const builtin = BUILTIN_ATTRS.find((a) => a.key === cond.attr.trim());
  const date = isDateAttr(cond.attr, defs);
  if (PICK_ATTRS[cond.attr.trim()] && (cond.op === "eq" || cond.op === "ne" || cond.op === "exists")) {
    return (
      <div className="rule-row">
        <AccountPicker cond={cond} onChange={onChange} />
        {remove}
      </div>
    );
  }
  return (
    <div className="rule-row">
      <input className="input" list="attr-keys" placeholder="attribute, e.g. city" value={cond.attr} onChange={(e) => onChange({ ...cond, attr: e.target.value })} aria-label="Attribute" />
      {builtin ? <span className="xs muted" title={builtin.hint}>{builtin.label}</span> : def && <span className="xs muted" title={def.description || undefined}>{def.type}</span>}
      <CompareFields op={cond.op} value={cond.value} type={date ? "date" : def?.type} dates={date ? "first" : "extra"} onChange={(op, value) => onChange({ ...cond, op, value })} />
      {remove}
    </div>
  );
}

// AccountPicker edits "in stage" and "works at company" conditions: is / is not / any, and a
// stage (by name) or company (by id) picked from the workspace's own.
function AccountPicker({ cond, onChange }: { cond: Extract<Cond, { kind: "attr" }>; onChange: (c: Cond) => void }) {
  const stage = cond.attr.trim() === "stage";
  const stages = useStages().data ?? [];
  const companies = useCompanyOptions().data ?? [];
  const items = stage ? stages.map((s) => ({ value: s.name, label: s.name })) : companies.map((c) => ({ value: String(c.id), label: c.name }));
  const noun = stage ? "stage" : "company";
  const known = items.some((x) => x.value === cond.value);
  return (
    <>
      <select className="select" value={cond.op} onChange={(e) => onChange({ ...cond, op: e.target.value as AttrOp, value: e.target.value === "exists" ? "" : cond.value })} aria-label="Comparison">
        <option value="eq">{stage ? "Is in stage" : "Works at"}</option>
        <option value="ne">{stage ? "Is not in stage" : "Doesn't work at"}</option>
        <option value="exists">{stage ? "Has any stage" : "Works at any company"}</option>
      </select>
      {cond.op !== "exists" && (
        <select className="select" value={cond.value} onChange={(e) => onChange({ ...cond, value: e.target.value })} aria-label={`Choose a ${noun}`}>
          <option value="" disabled>Choose a {noun}</option>
          {!known && cond.value && <option value={cond.value}>{stage ? cond.value : `company #${cond.value}`}</option>}
          {items.map((x) => <option key={x.value} value={x.value}>{x.label}</option>)}
        </select>
      )}
      {items.length === 0 && <span className="xs muted">{stage ? "No stages yet: add them under Workspace, Stages." : "No companies yet."}</span>}
    </>
  );
}

// WhereFields edits one property filter. Properties that hold the id of a form, campaign or journey
// get a picker by name; a message's source can be switched between campaign and journey.
function WhereFields({ event, w, list, onChange }: { event: string; w: Where; list: string; onChange: (w: Where) => void }) {
  const { lists } = useRefObjects();
  const prop = w.prop.trim();
  const pick = PICK_PROPS[prop];
  const message = !!KNOWN_EVENTS[event]?.message;
  const propField = message && (prop === "broadcast_id" || prop === "journey_id") ? (
    <select className="select" value={prop} onChange={(e) => onChange({ prop: e.target.value, op: w.op, value: "" })} aria-label="Sent by">
      <option value="broadcast_id">sent by campaign</option>
      <option value="journey_id">sent by journey</option>
    </select>
  ) : (
    <input className="input" list={list} placeholder="property, e.g. amount" value={w.prop} onChange={(e) => onChange({ ...w, prop: e.target.value })} aria-label="Event property" />
  );
  if (pick?.refs && (w.op === "eq" || w.op === "ne")) {
    const items = lists[pick.refs];
    const known = items.some((x) => String(x.id) === w.value);
    return (
      <>
        {propField}
        <select className="select" value={w.op} onChange={(e) => onChange({ ...w, op: e.target.value as AttrOp })} aria-label="Comparison">
          <option value="eq">is</option>
          <option value="ne">is not</option>
        </select>
        <select className="select" value={w.value} onChange={(e) => onChange({ ...w, value: e.target.value })} aria-label={`Choose a ${pick.noun}`}>
          <option value="" disabled>Choose a {pick.noun}</option>
          {!known && w.value && <option value={w.value}>{pick.noun} #{w.value}</option>}
          {items.map((x) => <option key={x.id} value={String(x.id)}>{x.name}</option>)}
        </select>
        {items.length === 0 && <span className="xs muted">No {pick.noun}s yet</span>}
      </>
    );
  }
  if (event === "unsubscribed" && prop === "channel" && w.op === "eq") {
    return (
      <>
        {propField}
        <span className="small">is</span>
        <select className="select" value={w.value} onChange={(e) => onChange({ ...w, value: e.target.value })} aria-label="Channel">
          <option value="" disabled>choose</option>
          {CHANNELS.map((c) => <option key={c} value={c}>{CHANNEL_NAMES[c]}</option>)}
        </select>
      </>
    );
  }
  return (
    <>
      {propField}
      <CompareFields op={w.op} value={w.value} dates="props" onChange={(op, value) => onChange({ ...w, op, value })} />
    </>
  );
}

// CompareFields edits an attribute-style comparison: the operator and, unless it's "is set", a value.
// A defined boolean attribute gets yes/no instead of free text. dates says where date comparisons go:
// first (a date attribute), extra (offered after the usual ones) or props (event properties, which
// can't compare with today).
function CompareFields({ op, value, type, dates = "extra", onChange }: {
  op: AttrOp; value: string; type?: AttributeDef["type"]; dates?: "first" | "extra" | "props"; onChange: (op: AttrOp, value: string) => void;
}) {
  const dateOps = dates === "props" ? DATE_OPS.filter((o) => o.op === "before" || o.op === "after") : DATE_OPS;
  const dateOptions = dateOps.map((o) => <option key={o.op} value={o.op}>{o.suffix ? `${o.label} … ${o.suffix}` : o.label}</option>);
  const basic = dates === "first" ? ATTR_OPS.filter((o) => ["eq", "ne", "exists"].includes(o.op)) : ATTR_OPS;
  const basicOptions = basic.map((o) => <option key={o.op} value={o.op}>{o.label}</option>);
  // Switching between a date and a day count (or away from dates) starts the value afresh.
  const kind = (o: string) => (o === "before" || o === "after" ? "date" : o === "within_days" || o === "older_than_days" ? "days" : "text");
  const setOp = (next: AttrOp) => onChange(next, kind(next) === kind(op) ? value : kind(next) === "days" ? "30" : "");
  const dateOp = DATE_OPS.find((o) => o.op === op);
  return (
    <>
      <select className="select" value={op} onChange={(e) => setOp(e.target.value as AttrOp)} aria-label="Comparison">
        {dates === "first" ? <>{dateOptions}{basicOptions}</> : <>{basicOptions}<optgroup label="As a date">{dateOptions}</optgroup></>}
        {/* A saved rule may use an op this list doesn't offer here; keep it visible. */}
        {![...basic, ...dateOps].some((o) => o.op === op) && <option value={op}>{isDateOp(op) ? dateOp?.label : ATTR_OPS.find((o) => o.op === op)?.label ?? op}</option>}
      </select>
      {op === "exists" ? null : op === "before" || op === "after" ? (
        <input className="input" type="date" value={value} onChange={(e) => onChange(op, e.target.value)} aria-label="Date" />
      ) : op === "within_days" || op === "older_than_days" ? (
        <>
          <input className="input" style={{ width: 80, minWidth: 0 }} type="number" min={1} max={3650} step={1} value={value} onChange={(e) => onChange(op, e.target.value)} aria-label="Days" />
          <span className="small">{dateOp?.suffix}</span>
        </>
      ) : type === "boolean" && (op === "eq" || op === "ne") ? (
        <select className="select" value={value} onChange={(e) => onChange(op, e.target.value)} aria-label="Value">
          <option value="" disabled>choose</option>
          <option value="true">yes</option>
          <option value="false">no</option>
        </select>
      ) : (
        <input className="input" inputMode={type === "number" ? "decimal" : undefined} placeholder={op === "in" ? "Pune, Mumbai" : type === "date" ? "2026-11-30" : "value"} value={value} onChange={(e) => onChange(op, e.target.value)} aria-label="Value" />
      )}
    </>
  );
}
