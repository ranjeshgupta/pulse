import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, type Segment, type SegmentRefresh } from "../api";
import { useCan } from "../auth";
import { describe, newGroup, problems, rootFromRule, toRule, type Group } from "../segmentRules";
import { Empty, ErrorNote, Field, LiveNumber, Modal, PageHead, Status, timeAgo, useToast } from "../ui";
import { GroupEditor } from "./SegmentBuilder";
import { StaticSegmentDetail } from "./StaticSegment";
import { useRefObjects, useSegmentNames, useSegments } from "./segmentData";
import "./segments.css";

export { GroupEditor } from "./SegmentBuilder";
export { useAttributes, useRefObjects, useSegmentNames, useSegments } from "./segmentData";

type TypeFilter = "all" | "rule" | "static";
const TYPE_LABEL = { rule: "Dynamic", static: "Static" } as const;

/** The type badge: dynamic segments follow rules, static ones hold the contacts put in them. */
export function SegmentType({ kind }: { kind: Segment["kind"] }) {
  return <span className={`badge ${kind === "static" ? "" : "info"}`} title={kind === "static" ? "Contacts are added by hand, import, a journey step or the API" : "Contacts join and leave by its rules"}>{TYPE_LABEL[kind]}</span>;
}

/** The Contacts list filtered to one segment. */
export const viewContactsHref = (id: number) => `/contacts?q=${encodeURIComponent(`segment:${id}`)}`;

export function SegmentList() {
  // Sizes come straight from the live bitmaps; polling shows contacts moving in and out.
  const q = useSegments();
  const [params, setParams] = useSearchParams();
  const type = (["rule", "static"].includes(params.get("type") ?? "") ? params.get("type") : "all") as TypeFilter;
  const setType = (t: TypeFilter) => setParams(t === "all" ? {} : { type: t }, { replace: true });
  const [search, setSearch] = useState("");
  const [creating, setCreating] = useState(false);
  const canEdit = useCan("segments", "edit");
  const all = q.data?.segments ?? [];
  const segs = all.filter((s) => (type === "all" || s.kind === type) && s.name.toLowerCase().includes(search.trim().toLowerCase()));
  const names = Object.fromEntries(all.map((s) => [s.id, s.name])) as Record<number, string>;
  const refs = useRefObjects().names;
  const counts = { all: all.length, rule: all.filter((s) => s.kind !== "static").length, static: all.filter((s) => s.kind === "static").length };
  return (
    <div className="page">
      <PageHead
        title="Segments"
        intro="Dynamic segments follow rules: contacts join and leave the moment an event or profile change makes them match. Static segments hold exactly the contacts you add by hand, import, a journey step or the API. Sizes update live."
        actions={canEdit && <button className="btn primary" onClick={() => setCreating(true)}>New segment</button>}
      />
      {creating && <NewSegmentModal onClose={() => setCreating(false)} />}
      <div className="row" style={{ marginBottom: 14, justifyContent: "space-between" }}>
        <div className="seg-toggle" role="group" aria-label="Segment type">
          {(["all", "rule", "static"] as const).map((t) => (
            <button key={t} aria-pressed={type === t} onClick={() => setType(t)}>{t === "all" ? "All" : TYPE_LABEL[t]} <span className="faint">{counts[t]}</span></button>
          ))}
        </div>
        <input className="input" style={{ maxWidth: 280 }} placeholder="Find a segment" value={search} onChange={(e) => setSearch(e.target.value)} aria-label="Find a segment" />
      </div>
      <ErrorNote error={q.error} />
      <div className="panel">
        {segs.length === 0 && !q.isLoading ? (
          all.length === 0 ? (
            <Empty title="No segments yet" action={canEdit && <button className="btn primary" onClick={() => setCreating(true)}>Create a segment</button>}>
              Describe who you want to reach, like "bought at least twice in 30 days and lives in Pune", or import a list of webinar guests.
            </Empty>
          ) : <Empty title="No segments match">Try another type or name.</Empty>
        ) : (
          <div className="table-scroll">
            <table className="table">
              <thead><tr><th>Name</th><th>Type</th><th>Who's in it</th><th className="num">Contacts</th><th>State</th><th /></tr></thead>
              <tbody>
                {segs.map((s) => (
                  <tr key={s.id}>
                    <td><Link to={`/segments/${s.id}`}>{s.name}</Link></td>
                    <td><SegmentType kind={s.kind} /></td>
                    <td className="muted" style={{ maxWidth: 480 }}>
                      {s.kind === "static" ? "Added by hand, import, a journey step or the API" : describe(s.rule, names, refs)}
                      {s.refresh?.mode === "daily" && <div className="xs">Refreshed daily at {s.refresh.at} ({s.refresh.tz})</div>}
                    </td>
                    <td className="num"><LiveNumber value={s.size} /></td>
                    <td>{s.kind === "static" ? <Status value="ready" /> : <Status value={s.status} />}</td>
                    <td className="num"><Link className="btn sm" to={viewContactsHref(s.id)}>View contacts</Link></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

/** NewSegmentModal asks Dynamic or Static first; a static segment only needs a name. */
function NewSegmentModal({ onClose }: { onClose: () => void }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [kind, setKind] = useState<"rule" | "static" | null>(null);
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: () => api.post<{ id: number }>("/v1/lists", { name: name.trim() }),
    onSuccess: (r) => { qc.invalidateQueries({ queryKey: ["segments"] }); nav(`/segments/${r.id}`); },
  });
  return (
    <Modal title="New segment" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      {kind === "rule" && <button className="btn primary" onClick={() => nav("/segments/new")}>Continue</button>}
      {kind === "static" && <button className="btn primary" disabled={!name.trim() || create.isPending} onClick={() => create.mutate()}>Create static segment</button>}
    </>}>
      <div className="seg-kind" role="radiogroup" aria-label="Segment type">
        <button role="radio" aria-checked={kind === "rule"} className={kind === "rule" ? "on" : ""} onClick={() => setKind("rule")}>
          <strong>Dynamic</strong>
          <span>Rules decide who's in it: attributes, events, tags, consent, other segments. Contacts join and leave on their own, in real time.</span>
        </button>
        <button role="radio" aria-checked={kind === "static"} className={kind === "static" ? "on" : ""} onClick={() => setKind("static")}>
          <strong>Static</strong>
          <span>Holds exactly the contacts you put in it, by import, by hand, a journey step or the API. For event guests, sales hand-offs or a seed list.</span>
        </button>
      </div>
      {kind === "static" && <Field label="Name"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Diwali webinar guests" /></Field>}
      <ErrorNote error={create.error} />
    </Modal>
  );
}

export function SegmentEdit() {
  const { id } = useParams();
  const kind = useQuery({ queryKey: ["segment", id], queryFn: () => api.get<Segment>(`/v1/segments/${id}`), enabled: id !== "new", refetchInterval: 3000 }).data?.kind;
  if (kind === "static") return <StaticSegmentDetail />;
  return <DynamicSegmentEdit />;
}

function DynamicSegmentEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const existing = useQuery({ queryKey: ["segment", id], queryFn: () => api.get<Segment>(`/v1/segments/${id}`), enabled: !isNew, refetchInterval: 3000 });
  const [name, setName] = useState("");
  const [root, setRoot] = useState<Group>(newGroup());
  const [refresh, setRefresh] = useState<SegmentRefresh>({ mode: "live" });
  const [loaded, setLoaded] = useState(isNew);

  useEffect(() => {
    if (existing.data && !loaded) {
      setName(existing.data.name);
      setRoot(rootFromRule(existing.data.rule ?? undefined));
      setRefresh(existing.data.refresh ?? { mode: "live" });
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
    mutationFn: () => (isNew ? api.post<{ id: number }>("/v1/segments", { name, rule, refresh }) : api.put<{ id: number }>(`/v1/segments/${id}`, { name, rule, refresh })),
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
        title={<>{isNew ? "New dynamic segment" : name || "Segment"} {!isNew && <SegmentType kind="rule" />}</>}
        actions={<>
          {!isNew && <Link className="btn" to={viewContactsHref(Number(id))}>View contacts</Link>}
          {!isNew && <button className="btn danger" onClick={() => confirm("Delete this segment? Journeys that use it stop enrolling.") && remove.mutate()}>Delete</button>}
          <button className="btn primary" disabled={!rule || !name || save.isPending} onClick={() => save.mutate()}>{isNew ? "Create segment" : "Save segment"}</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error ?? existing.error} />
      <div className="editor-split">
        <div>
          <Field label="Name"><input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Repeat buyers in Pune" /></Field>
          <GroupEditor group={root} onChange={setRoot} self={isNew ? undefined : Number(id)} />
          <RefreshField value={refresh} onChange={setRefresh} />
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
                {existing.data.refresh?.mode === "daily" && existing.data.next_refresh_at && (
                  <><dt>Next refresh</dt><dd>{new Date(existing.data.next_refresh_at).toLocaleString()}</dd></>
                )}
              </dl>
            )}
            {rule && (
              <div style={{ marginTop: 18 }}>
                <p className="xs muted" style={{ marginBottom: 4 }}>In words</p>
                <p className="small" aria-live="polite">{describe(rule, names, refs)}</p>
              </div>
            )}
            {preview.error && <p className="xs" style={{ color: "var(--danger)", marginTop: 8 }}>{(preview.error as Error).message}</p>}
          </div>
        </aside>
      </div>
    </div>
  );
}


const ZONES = ["UTC", "Asia/Kolkata", "Asia/Dubai", "Asia/Singapore", "Europe/London", "Europe/Berlin", "America/New_York", "America/Los_Angeles", "Australia/Sydney"];

/** RefreshField: Live (members follow every event) or Daily at a time (rebuilt once a day). */
function RefreshField({ value, onChange }: { value: SegmentRefresh; onChange: (r: SegmentRefresh) => void }) {
  const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const zones = [...new Set([value.tz || browserZone, browserZone, ...ZONES].filter(Boolean))];
  return (
    <fieldset className="seg-refresh panel">
      <legend className="small">Refresh</legend>
      <div className="row" role="radiogroup" aria-label="Refresh">
        <label className="small"><input type="radio" checked={value.mode !== "daily"} onChange={() => onChange({ mode: "live" })} /> Live: contacts join and leave as events arrive (default)</label>
        <label className="small"><input type="radio" checked={value.mode === "daily"} onChange={() => onChange({ mode: "daily", at: value.at || "06:00", tz: value.tz || browserZone })} /> Daily at a set time</label>
      </div>
      {value.mode === "daily" && (
        <div className="row" style={{ marginTop: 8 }}>
          <input className="input" type="time" value={value.at ?? "06:00"} onChange={(e) => onChange({ ...value, at: e.target.value })} aria-label="Refresh time" style={{ width: 120 }} />
          <select className="select" value={value.tz ?? browserZone} onChange={(e) => onChange({ ...value, tz: e.target.value })} aria-label="Time zone">
            {zones.map((z) => <option key={z} value={z}>{z}</option>)}
          </select>
          <span className="xs muted">Members change only at this time each day: good for daily sends that must not shift during the day.</span>
        </div>
      )}
    </fieldset>
  );
}
