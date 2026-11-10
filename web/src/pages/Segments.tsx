import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type AttributeDef, type Segment } from "../api";
import {
  ATTR_OPS, EVENT_OPS, describe, newAttr, newEvent, newGroup, newSegmentRef, newTag, newWhere, problems, rootFromRule, toRule,
  type AttrOp, type Cond, type EventOp, type Group, type Node, type Where,
} from "../segmentRules";
import { Empty, ErrorNote, Field, LiveNumber, PageHead, Status, timeAgo, useToast } from "../ui";

/** Every segment and list of the workspace, polled so sizes stay live. */
export function useSegments(poll = 3000) {
  return useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments"), refetchInterval: poll });
}

/** Segment and list names by id, so rules can describe references in words. */
export function useSegmentNames() {
  const segs = useSegments(0).data?.segments ?? [];
  return useMemo(() => Object.fromEntries(segs.map((s) => [s.id, s.name])) as Record<number, string>, [segs]);
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
                  <td className="muted" style={{ maxWidth: 520 }}>{describe(s.rule, names)}</td>
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
            {rule && <p className="small" style={{ marginTop: 18 }}>{describe(rule, names)}</p>}
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
      <div className="row" style={{ marginTop: 6 }}>
        <button className="btn sm" onClick={() => add(newAttr())}>Add attribute condition</button>
        <button className="btn sm" onClick={() => add(newEvent())}>Add event condition</button>
        <button className="btn sm" onClick={() => add(newTag())}>Add tag condition</button>
        <button className="btn sm" onClick={() => add(newSegmentRef())}>Add segment or list</button>
        {depth < 3 && <button className="btn ghost sm" onClick={() => add(newGroup(group.match === "all" ? "any" : "all"))}>Add group</button>}
      </div>
      <SuggestionLists />
    </div>
  );
}

// Shared datalists for attribute names and tags; rendered once per group, ids are stable.
function SuggestionLists() {
  const attrs = useAttributes().data;
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/tags"), staleTime: 30_000 }).data?.tags ?? [];
  const keys = [...(attrs?.attributes.map((a) => a.key) ?? []), ...(attrs?.undefined ?? [])];
  return (
    <>
      <datalist id="attr-keys">{keys.map((k) => <option key={k} value={k} />)}</datalist>
      <datalist id="tag-names">{tags.map((t) => <option key={t} value={t} />)}</datalist>
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
    return (
      <div className="rule-cond">
        <div className="rule-row">
          <span className="small">Did</span>
          <input className="input" placeholder="event, e.g. purchase" value={cond.event} onChange={(e) => onChange({ ...cond, event: e.target.value })} aria-label="Event name" />
          <select className="select" value={cond.op} onChange={(e) => onChange({ ...cond, op: e.target.value as EventOp })} aria-label="How often">
            {EVENT_OPS.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
          </select>
          {cond.op !== "none" && <><input className="input" style={{ width: 76, minWidth: 0 }} type="number" min={0} value={cond.count} onChange={(e) => onChange({ ...cond, count: Number(e.target.value) })} aria-label="Times" /><span className="small">times</span></>}
          <span className="small">in the last</span>
          <input className="input" style={{ width: 76, minWidth: 0 }} type="number" min={1} max={365} value={cond.days} onChange={(e) => onChange({ ...cond, days: Number(e.target.value) })} aria-label="Days" />
          <span className="small">days</span>
          {remove}
        </div>
        {cond.where.map((w, i) => (
          <div className="rule-row rule-where" key={i}>
            <span className="small">{i === 0 ? "where" : "and"}</span>
            <input className="input" placeholder="property, e.g. amount" value={w.prop} onChange={(e) => setWhere(i, { ...w, prop: e.target.value })} aria-label="Event property" />
            <CompareFields op={w.op} value={w.value} onChange={(op, value) => setWhere(i, { ...w, op, value })} />
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
  return (
    <div className="rule-row">
      <input className="input" list="attr-keys" placeholder="attribute, e.g. city" value={cond.attr} onChange={(e) => onChange({ ...cond, attr: e.target.value })} aria-label="Attribute" />
      {def && <span className="xs muted" title={def.description || undefined}>{def.type}</span>}
      <CompareFields op={cond.op} value={cond.value} type={def?.type} onChange={(op, value) => onChange({ ...cond, op, value })} />
      {remove}
    </div>
  );
}

// CompareFields edits an attribute-style comparison: the operator and, unless it's "is set", a value.
// A defined boolean attribute gets yes/no instead of free text.
function CompareFields({ op, value, type, onChange }: { op: AttrOp; value: string; type?: AttributeDef["type"]; onChange: (op: AttrOp, value: string) => void }) {
  return (
    <>
      <select className="select" value={op} onChange={(e) => onChange(e.target.value as AttrOp, value)} aria-label="Comparison">
        {ATTR_OPS.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
      </select>
      {op !== "exists" && (type === "boolean" && (op === "eq" || op === "ne") ? (
        <select className="select" value={value} onChange={(e) => onChange(op, e.target.value)} aria-label="Value">
          <option value="" disabled>choose</option>
          <option value="true">yes</option>
          <option value="false">no</option>
        </select>
      ) : (
        <input className="input" inputMode={type === "number" ? "decimal" : undefined} placeholder={op === "in" ? "Pune, Mumbai" : "value"} value={value} onChange={(e) => onChange(op, e.target.value)} aria-label="Value" />
      ))}
    </>
  );
}
