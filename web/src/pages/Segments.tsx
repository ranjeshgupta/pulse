import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Segment } from "../api";
import { describe, newGroup, problems, rootFromRule, toRule, type Group } from "../segmentRules";
import { Empty, ErrorNote, Field, LiveNumber, PageHead, Status, timeAgo, useToast } from "../ui";
import { GroupEditor } from "./SegmentBuilder";
import { useRefObjects, useSegmentNames, useSegments } from "./segmentData";
import "./segments.css";

export { GroupEditor } from "./SegmentBuilder";
export { useAttributes, useRefObjects, useSegmentNames, useSegments } from "./segmentData";

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
          <GroupEditor group={root} onChange={setRoot} self={isNew ? undefined : Number(id)} />
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

