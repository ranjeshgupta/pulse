import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Background, Controls, Handle, Position, ReactFlow, ReactFlowProvider, useReactFlow,
  type Connection, type Edge, type EdgeChange, type NodeChange, type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { createContext, useContext, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, CHANNEL_NAMES, type Channel, type Journey, type JourneyGraph, type JourneyNode, type JourneyReport, type Segment, type SplitDecision, type SplitReport, type SplitWinner, type Template } from "../api";
import { money } from "../insights";
import {
  addNode, connect, decisionText, disconnect, emptyGraph, formatWait, moveNode, nodeChannel, nodeLabel, NODE_TYPES, OUTCOME_WORDS, OUTCOMES, outputs,
  problems, removeNode, sendSteps, share, STEP_GROUPS, toFlow, TRIGGER_ID, upstream, type FlowData, type Outcome,
  winnerProblem,
} from "../journeyGraph";
import { describe, problems as ruleProblems, rootFromRule, toRule, type RefNames } from "../segmentRules";
import { GroupEditor, useRefObjects } from "./Segments";
import { useStages } from "./Stages";
import "./journeys.css";
import { TemplateActions } from "../messagePreview";
import { CountsTable, DailyBars, metricsFor, ReportPanel, TopLinks, type CountsRow } from "../reports";
import { describeWindow, fromLocalInput, nowISO, ScheduleFields, scheduleProblem, toLocalInput } from "../schedule";
import { Empty, ErrorNote, Field, LiveNumber, PageHead, Status, useToast } from "../ui";

export function JourneyList() {
  const q = useQuery({ queryKey: ["journeys"], queryFn: () => api.get<{ journeys: Journey[] }>("/v1/journeys"), refetchInterval: 4000 });
  const js = q.data?.journeys ?? [];
  return (
    <div className="page">
      <PageHead
        title="Journeys"
        intro="A journey starts when a contact enters a segment or does something, then waits, checks and sends across channels."
        actions={<Link className="btn primary" to="/journeys/new">New journey</Link>}
      />
      <ErrorNote error={q.error} />
      <div className="panel">
        {js.length === 0 && !q.isLoading ? (
          <Empty title="No journeys yet" action={<Link className="btn primary" to="/journeys/new">Build a journey</Link>}>
            For example: when someone enters "Abandoned cart", wait an hour, then send a WhatsApp reminder with an SMS fallback.
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th>Status</th><th>Schedule</th><th className="num">Entered</th><th className="num">In progress</th><th className="num">Completed</th></tr></thead>
            <tbody>
              {js.map((j) => {
                const st = j.stats?.status ?? {};
                const total = Object.values(st).reduce((a, b) => a + b, 0);
                return (
                  <tr key={j.id}>
                    <td><Link to={`/journeys/${j.id}`}>{j.name}</Link></td>
                    <td><Status value={j.status} /></td>
                    <td className="muted small">{describeWindow(j.start_at, j.end_at)}</td>
                    <td className="num"><LiveNumber value={total} /></td>
                    <td className="num"><LiveNumber value={(st.active ?? 0) + (st.waiting ?? 0)} /></td>
                    <td className="num"><LiveNumber value={st.completed ?? 0} /></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// Lookups the canvas nodes need to describe themselves.
type Ctx = { segments: Map<number, string>; templates: Map<number, Template>; counts: Record<string, number>; graph: JourneyGraph; refs: RefNames };
const CanvasCtx = createContext<Ctx>({ segments: new Map(), templates: new Map(), counts: {}, graph: emptyGraph(), refs: {} });

// TagListInput edits a list of tags as comma-separated text, keeping what's typed until it parses.
function TagListInput({ value, onChange }: { value: string[]; onChange: (tags: string[]) => void }) {
  const [text, setText] = useState(value.join(", "));
  return (
    <input className="input" value={text} placeholder="lead, webinar" onChange={(e) => {
      setText(e.target.value);
      onChange([...new Set(e.target.value.split(",").map((t) => t.trim()).filter(Boolean))]);
    }} />
  );
}

function PulseNode({ data, selected }: NodeProps) {
  const { segments, templates, counts, graph, refs } = useContext(CanvasCtx);
  const d = data as FlowData;
  if (d.isTrigger) {
    const t = graph.trigger;
    return (
      <div className={`jnode t-trigger${selected ? " selected" : ""}`}>
        <div className="jnode-head">Starts when</div>
        <div className="jnode-body">
          {t.type === "segment_entered" ? `Contact enters ${t.segment_id ? `"${segments.get(t.segment_id) ?? "segment " + t.segment_id}"` : "a segment"}` : `Contact does ${t.event || "an event"}`}
        </div>
        <Handle type="source" position={Position.Bottom} id="next" />
      </div>
    );
  }
  const n = d.node!;
  const tpl = n.template_id ? templates.get(n.template_id) : undefined;
  const fb = n.fallback_template_id ? templates.get(n.fallback_template_id) : undefined;
  const count = counts[d.id];
  let body = "";
  if (n.type === "wait") body = `for ${formatWait(n.seconds)}`;
  else if (n.type === "condition") body = n.segment_id ? `in "${segments.get(n.segment_id) ?? n.segment_id}"?` : "choose a segment";
  else if (n.type === "exit") body = "journey ends";
  else if (n.type === "wait_until") body = n.until ? `until ${new Date(n.until).toLocaleString("en-IN", { dateStyle: "medium", timeStyle: "short" })}` : n.time_of_day ? `until ${n.time_of_day} in their time zone` : "choose when";
  else if (n.type === "wait_for_event") body = n.event ? `for ${n.event}, up to ${formatWait(n.seconds)}` : "choose an event";
  else if (n.type === "check") body = n.rule ? describe(n.rule, Object.fromEntries(segments), refs) : "describe who matches";
  else if (n.type === "message_decision") body = decisionText(graph, n, (tid) => templates.get(tid)?.name);
  else if (n.type === "update_tags") body = [n.add_tags?.length ? `add ${n.add_tags.join(", ")}` : "", n.remove_tags?.length ? `remove ${n.remove_tags.join(", ")}` : ""].filter(Boolean).join("; ") || "choose tags";
  else if (n.type === "adjust_points") body = n.points ? `${n.points > 0 ? "+" : ""}${n.points} points` : "set points";
  else if (n.type === "change_stage") body = n.stage ? `move to “${n.stage}”` : "choose a stage";
  else if (n.type === "update_list") body = n.list_id ? `${n.list_action === "remove" ? "remove from" : "add to"} "${segments.get(n.list_id) ?? n.list_id}"` : "choose a list";
  else if (n.type === "split") body = `${n.variants?.length ?? 0} paths${n.winner ? " · picks a winner" : ""}`;
  else if (n.type === "update_attr") body = Object.entries(n.attrs ?? {}).map(([k, v]) => `${k} = ${String(v)}`).join(", ") || "set attributes";
  else if (n.type === "webhook") body = n.url ? new URL(n.url, "https://x").host : "enter a URL";
  else body = tpl ? tpl.name : "choose a template";
  const outs = outputs(n);
  return (
    <div className={`jnode t-${n.type}${selected ? " selected" : ""}`}>
      <Handle type="target" position={Position.Top} />
      <div className="jnode-head"><span>{nodeLabel(n.type)}</span><span className="faint xs">{d.id}</span></div>
      <div className="jnode-body">{body}{fb && <><br />SMS fallback: {fb.name}</>}</div>
      {count ? <div className="jnode-stat">{count.toLocaleString("en-IN")} {n.type === "exit" ? "finished here" : n.type === "wait" ? "waiting" : "here now"}</div> : null}
      {outs.map(([h], i) => (
        <Handle key={h} type="source" position={Position.Bottom} id={h} style={{ left: `${((i + 1) / (outs.length + 1)) * 100}%` }} />
      ))}
    </div>
  );
}

const nodeTypes = { pulse: PulseNode };

export function JourneyEdit() {
  return <ReactFlowProvider><JourneyEditor /></ReactFlowProvider>;
}

function JourneyEditor() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const flow = useReactFlow();

  const existing = useQuery({ queryKey: ["journey", id], queryFn: () => api.get<Journey>(`/v1/journeys/${id}`), enabled: !isNew });
  const stats = useQuery({ queryKey: ["journey-stats", id], queryFn: () => api.get<Journey["stats"]>(`/v1/journeys/${id}/stats`), enabled: !isNew, refetchInterval: 3000 });
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const tpls = useQuery({ queryKey: ["templates", ""], queryFn: () => api.get<{ templates: Template[] }>("/v1/templates") });

  const [name, setName] = useState("Untitled journey");
  const [graph, setGraph] = useState<JourneyGraph>(emptyGraph());
  const [win, setWin] = useState<{ start_at?: string; end_at?: string }>({ start_at: nowISO() });
  const [selected, setSelected] = useState<string | null>(TRIGGER_ID);
  const [loaded, setLoaded] = useState(isNew);
  const [view, setView] = useState<"canvas" | "report">("canvas");
  useEffect(() => {
    if (existing.data && !loaded) {
      setName(existing.data.name);
      setGraph(existing.data.graph);
      setWin({ start_at: existing.data.start_at, end_at: existing.data.end_at ?? undefined });
      setLoaded(true);
      // The canvas first mounted with a placeholder graph; frame the real one once it has rendered.
      window.setTimeout(() => flow.fitView({ padding: 0.3, maxZoom: 1 }), 60);
    }
  }, [existing.data, loaded, flow]);

  const status = existing.data?.status ?? "draft";
  const locked = status === "active";
  const { nodes, edges } = useMemo(() => toFlow(graph), [graph]);
  const flowNodes = nodes.map((n) => ({ ...n, selected: n.id === selected, draggable: !locked }));
  const winProblem = scheduleProblem(win.start_at, win.end_at);
  const issues = winProblem ? [...problems(graph), winProblem] : problems(graph);

  const refs = useRefObjects().names;
  const ctx: Ctx = {
    segments: new Map(segs.data?.segments.map((s) => [s.id, s.name])),
    templates: new Map(tpls.data?.templates.map((t) => [t.id, t])),
    counts: stats.data?.node ?? {},
    graph,
    refs,
  };

  const onNodesChange = (changes: NodeChange[]) => {
    let g = graph;
    for (const c of changes) {
      if (c.type === "position" && c.position && c.id !== TRIGGER_ID) g = moveNode(g, c.id, c.position.x, c.position.y);
      if (c.type === "remove" && c.id !== TRIGGER_ID && !locked) g = removeNode(g, c.id);
      if (c.type === "select" && c.selected) setSelected(c.id);
    }
    if (g !== graph) setGraph(g);
  };
  const onEdgesChange = (changes: EdgeChange[]) => {
    if (locked) return;
    let g = graph;
    for (const c of changes) if (c.type === "remove") g = disconnect(g, c.id);
    if (g !== graph) setGraph(g);
  };
  const onConnect = (c: Connection) => !locked && setGraph(connect(graph, c.source, c.sourceHandle, c.target));

  const add = (type: string) => {
    const center = flow.screenToFlowPosition({ x: window.innerWidth / 2, y: window.innerHeight / 2 });
    const { graph: g, id: nid } = addNode(graph, type, center.x - 95, center.y - 40);
    setGraph(g);
    setSelected(nid);
  };

  const save = useMutation({
    mutationFn: () => {
      const body = { name, graph, start_at: win.start_at, end_at: win.end_at ?? null };
      return isNew ? api.post<{ id: number }>("/v1/journeys", body) : api.put<{ id: number }>(`/v1/journeys/${id}`, body);
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["journeys"] });
      qc.invalidateQueries({ queryKey: ["journey", String(r.id)] });
      toast({ text: "Journey saved" });
      if (isNew) nav(`/journeys/${r.id}`, { replace: true });
    },
  });
  const setStatus = useMutation({
    mutationFn: (action: "activate" | "pause") => api.post(`/v1/journeys/${id}/${action}`),
    onSuccess: (_, action) => {
      qc.invalidateQueries({ queryKey: ["journey", id] });
      qc.invalidateQueries({ queryKey: ["journeys"] });
      toast({ text: action === "activate" ? "Journey is live. New matches enter it immediately." : "Journey paused" });
    },
  });

  return (
    <div className="page" style={{ maxWidth: "none" }}>
      <PageHead
        back={<Link className="back" to="/journeys">Journeys</Link>}
        title={<input className="input" style={{ fontSize: "var(--fs-xl)", fontWeight: 650, border: 0, padding: 0, background: "transparent" }} value={name} onChange={(e) => setName(e.target.value)} aria-label="Journey name" disabled={locked} />}
        actions={<>
          {!isNew && <Status value={status} />}
          {!locked && <button className="btn" disabled={save.isPending} onClick={() => save.mutate()}>Save draft</button>}
          {!isNew && status !== "active" && <button className="btn primary" disabled={issues.length > 0 || setStatus.isPending} onClick={async () => { await save.mutateAsync(); setStatus.mutate("activate"); }}>Save and go live</button>}
          {status === "active" && <button className="btn" onClick={() => setStatus.mutate("pause")}>Pause to edit</button>}
        </>}
      />
      <ErrorNote error={save.error ?? setStatus.error ?? existing.error} />
      {locked && <div className="alert info">This journey is live, so its steps are locked. Pause it to make changes; contacts already inside wait where they are.</div>}
      {!isNew && (
        <div className="tabs" role="tablist">
          <button role="tab" aria-selected={view === "canvas"} onClick={() => setView("canvas")}>Steps</button>
          <button role="tab" aria-selected={view === "report"} onClick={() => setView("report")}>Report</button>
        </div>
      )}
      {view === "report" && !isNew ? (
        <JourneyReportView id={id!} graph={graph} templates={ctx.templates} onStep={(sid) => { setSelected(sid); setView("canvas"); }} />
      ) : (
      <CanvasCtx.Provider value={ctx}>
        <div className="journey-layout">
          <aside className="palette">
            <h4>Add a step</h4>
            {STEP_GROUPS.map((grp) => (
              <section className={`palette-group g-${grp.group}`} key={grp.group}>
                <h5 title={grp.hint}>{grp.label}</h5>
                {NODE_TYPES.filter((t) => t.group === grp.group).map((t) => (
                  <button key={t.type} className="palette-item" disabled={locked} onClick={() => add(t.type)} title={t.hint}>
                    <span className="palette-label">{t.channel ? <span className={`chan chan-${t.channel}`}>{t.label}</span> : t.label}</span>
                    <small>{t.hint}</small>
                  </button>
                ))}
              </section>
            ))}
            <p className="xs muted" style={{ marginTop: 14 }}>Drag from a step's bottom dot to the next step to connect them. Select a line and press Backspace to remove it.</p>
          </aside>
          <ReactFlow
            nodes={flowNodes}
            edges={edges as Edge[]}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onPaneClick={() => setSelected(null)}
            fitView
            fitViewOptions={{ padding: 0.3, maxZoom: 1 }}
            nodesConnectable={!locked}
            deleteKeyCode={locked ? null : ["Backspace", "Delete"]}
            proOptions={{ hideAttribution: true }}
          >
            <Background gap={20} color="#dce1e8" />
            <Controls showInteractive={false} />
          </ReactFlow>
          <aside className="inspector">
            <Inspector graph={graph} setGraph={setGraph} win={win} setWin={setWin} selected={selected} locked={locked} decisions={existing.data?.split_winners ?? {}} segments={segs.data?.segments ?? []} templates={tpls.data?.templates ?? []} onDelete={(nid) => { setGraph(removeNode(graph, nid)); setSelected(null); }} />
            {issues.length > 0 && (
              <div style={{ marginTop: 20 }}>
                <h4 className="small" style={{ marginBottom: 6 }}>Before it can go live</h4>
                <ul className="xs muted" style={{ paddingLeft: 18, margin: 0 }}>{issues.map((p) => <li key={p}>{p}</li>)}</ul>
              </div>
            )}
          </aside>
        </div>
      </CanvasCtx.Provider>
      )}
    </div>
  );
}

// JourneyReportView: who entered and where they are, and what each send step delivered.
function JourneyReportView({ id, graph, templates, onStep }: { id: string; graph: JourneyGraph; templates: Map<number, Template>; onStep: (stepId: string) => void }) {
  const q = useQuery({ queryKey: ["journey-report", id], queryFn: () => api.get<JourneyReport>(`/v1/journeys/${id}/report`), refetchInterval: 5000 });
  const r = q.data;
  const st = r?.enrollments.status ?? {};
  const entered = Object.values(st).reduce((a, b) => a + b, 0);
  const m = r?.messages;
  const graphChannels = Object.values(graph.nodes).map((n) => nodeChannel(n.type)).filter((c): c is Channel => !!c);
  const channels = [...new Set([...graphChannels, ...Object.keys(m?.by_channel ?? {})])];
  const conversion = !!graph.goal || (m?.totals.converted ?? 0) > 0;
  const keys = metricsFor(channels, { conversion, detail: true });

  const stepRows: CountsRow[] = Object.entries(m?.by_node ?? {}).map(([sid, counts]) => {
    const n = graph.nodes[sid];
    const tpl = n?.template_id ? templates.get(n.template_id) : undefined;
    const ch = n ? nodeChannel(n.type) : undefined;
    return {
      key: sid,
      lead: [
        <span key="s">{n ? (ch ? <span className={`chan chan-${ch}`}>{nodeLabel(n.type)}</span> : nodeLabel(n.type)) : <span className="muted">Removed step</span>} <span className="faint xs">{sid}</span></span>,
        tpl ? tpl.name : <span className="faint">–</span>,
      ],
      counts,
      onClick: n ? () => onStep(sid) : undefined,
    };
  }).sort((a, b) => (b.counts?.sent ?? 0) - (a.counts?.sent ?? 0));
  const channelRows: CountsRow[] = Object.entries(m?.by_channel ?? {}).map(([c, counts]) => ({
    key: c, lead: [<span key="c" className={`chan chan-${c}`}>{CHANNEL_NAMES[c as Channel] ?? c}</span>], counts,
  }));

  return (
    <div>
      <ErrorNote error={q.error} />
      <div className="figures" style={{ margin: "4px 2px 22px" }}>
        <div><strong><LiveNumber value={entered} /></strong><span>entered</span></div>
        <div><strong><LiveNumber value={(st.active ?? 0) + (st.waiting ?? 0)} /></strong><span>in progress</span></div>
        <div><strong><LiveNumber value={st.completed ?? 0} /></strong><span>completed</span></div>
        {(graph.goal || (st.converted ?? 0) > 0) && <div><strong><LiveNumber value={st.converted ?? 0} /></strong><span>reached the goal</span></div>}
        {(st.exited ?? 0) > 0 && <div><strong><LiveNumber value={st.exited ?? 0} /></strong><span>left early</span></div>}
      </div>
      {m?.revenue && (
        <div className="figures" style={{ margin: "-8px 2px 22px" }}>
          <div><strong>{money(m.revenue.revenue, m.revenue.currency)}</strong><span>revenue from contacts in it <small>({m.revenue.event} within {m.revenue.days} days of a message)</small></span></div>
          <div><strong><LiveNumber value={m.revenue.orders} /></strong><span>orders</span></div>
          <div><strong><LiveNumber value={m.revenue.buyers} /></strong><span>buyers</span></div>
        </div>
      )}
      {Object.entries(r?.splits ?? {}).map(([sid, sp]) => (
        <div key={sid} style={{ marginBottom: 24 }}>
          <ReportPanel title={<>A/B split <span className="faint xs">{sid}</span></>} aside={<button className="btn ghost sm" onClick={() => onStep(sid)}>Open step</button>}>
            <SplitTable graph={graph} id={sid} split={sp} />
          </ReportPanel>
        </div>
      ))}
      {m && (
        <>
          <div style={{ marginBottom: 24 }}>
            <ReportPanel title="Messages by step" aside={<span className="xs muted">Each contact counts once. Rates are out of messages sent.</span>} body={stepRows.length === 0}>
              {stepRows.length === 0 ? <p className="muted small">No messages sent yet. Counts show up here as contacts reach send steps.</p> : (
                <CountsTable lead={["Step", "Template"]} rows={stepRows} keys={keys} />
              )}
            </ReportPanel>
          </div>
          {channelRows.length > 0 && (
            <div style={{ marginBottom: 24 }}>
              <ReportPanel title="By channel">
                <CountsTable lead={["Channel"]} rows={channelRows} keys={keys} />
              </ReportPanel>
            </div>
          )}
          <div className="report-cols" style={{ marginTop: 0 }}>
            {(channels.includes("email") || m.links.length > 0) && <TopLinks links={m.links} />}
            <DailyBars daily={m.daily} />
          </div>
        </>
      )}
    </div>
  );
}

// SplitTable: contacts per path of a split and, for A/B tests, the metric's rate; the winner highlighted.
function SplitTable({ graph, id, split }: { graph: JourneyGraph; id: string; split: SplitReport }) {
  const vs = graph.nodes[id]?.variants ?? [];
  const by = new Map(split.paths.map((p) => [p.path, p]));
  const metric = split.metric ? METRIC_WORDS[split.metric] ?? split.metric : "";
  return (
    <>
      {split.decision && <div className="panel-body" style={{ paddingBottom: 0 }}><SplitDecisionNote decision={split.decision} /></div>}
      <div className="table-scroll">
        <table className="table">
          <thead><tr><th scope="col">Path</th><th scope="col" className="num">Share</th><th scope="col" className="num">Contacts in the test</th>{metric && <><th scope="col" className="num">Hits</th><th scope="col" className="num">{metric[0].toUpperCase() + metric.slice(1)}</th></>}</tr></thead>
          <tbody>{vs.map((v, i) => {
            const p = by.get(i);
            return (
              <tr key={i} className={split.decision?.path === i ? "report-winner" : undefined}>
                <th scope="row" style={{ fontWeight: 500 }}>{v.label || String.fromCharCode(65 + i)}{split.decision?.path === i && <span className="xs" style={{ marginLeft: 6, color: "var(--ok)" }}>winner</span>}</th>
                <td className="num">{share(graph.nodes[id], i)}%</td>
                <td className="num">{p ? <LiveNumber value={p.entrants} /> : <span className="faint">0</span>}</td>
                {metric && <><td className="num">{p?.hits ?? 0}</td><td className="num">{p && p.entrants ? `${(p.rate * 100).toFixed(1)}%` : "–"}</td></>}
              </tr>
            );
          })}</tbody>
        </table>
      </div>
      <p className="xs muted" style={{ padding: "8px 14px" }}>Counts contacts the split sent down each path before a winner was picked.</p>
    </>
  );
}

function Inspector({ graph, setGraph, win, setWin, selected, locked, segments, templates, onDelete, decisions }: {
  graph: JourneyGraph; setGraph: (g: JourneyGraph) => void; selected: string | null; locked: boolean; decisions: Record<string, SplitDecision>;
  win: { start_at?: string; end_at?: string }; setWin: (w: { start_at?: string; end_at?: string }) => void;
  segments: Segment[]; templates: Template[]; onDelete: (id: string) => void;
}) {
  if (!selected) return <p className="small muted">Select a step to set it up.</p>;
  if (selected === TRIGGER_ID) {
    const t = graph.trigger;
    const setT = (patch: Partial<JourneyGraph["trigger"]>) => setGraph({ ...graph, trigger: { ...t, ...patch } });
    return (
      <fieldset disabled={locked} style={{ border: 0, padding: 0, margin: 0 }}>
        <h3 style={{ marginBottom: 12 }}>Start</h3>
        <Field label="Start this journey when a contact">
          <select className="select" value={t.type} onChange={(e) => setT({ type: e.target.value as "segment_entered" | "event" })}>
            <option value="segment_entered">enters a segment</option>
            <option value="event">does an event</option>
          </select>
        </Field>
        {t.type === "segment_entered" ? (
          <>
            <Field label="Segment">
              <select className="select" value={t.segment_id ?? ""} onChange={(e) => setT({ segment_id: Number(e.target.value) || undefined })}>
                <option value="">Choose a segment</option>
                {segments.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
              </select>
            </Field>
            <label className="check"><input type="checkbox" checked={!!t.include_backfill} onChange={(e) => setT({ include_backfill: e.target.checked })} />Also enroll contacts added when the segment is rebuilt</label>
            <p className="xs muted">Off: only people who newly match from now on enter. On: rebuilding a segment can enroll many contacts at once.</p>
          </>
        ) : (
          <Field label="Event name" hint="Exactly as your app sends it, e.g. signup or cart_abandoned.">
            <input className="input" value={t.event ?? ""} onChange={(e) => setT({ event: e.target.value.trim() })} />
          </Field>
        )}
        <h4 className="small" style={{ margin: "18px 0 8px" }}>Entering again</h4>
        <Field label="A contact who has been through this journey">
          <select className="select" value={t.reentry ?? "once"} onChange={(e) => setT({ reentry: e.target.value as "once" | "always" | "after_days", days: e.target.value === "after_days" ? t.days ?? 30 : undefined })}>
            <option value="once">never enters again</option>
            <option value="always">can enter again after finishing</option>
            <option value="after_days">can enter again after some days</option>
          </select>
        </Field>
        {t.reentry === "after_days" && (
          <Field label="Days since they last entered"><input className="input" type="number" min={1} value={t.days ?? 30} onChange={(e) => setT({ days: Number(e.target.value) })} /></Field>
        )}
        <h4 className="small" style={{ margin: "18px 0 8px" }}>Goal</h4>
        <label className="check"><input type="checkbox" checked={!!graph.goal} onChange={(e) => setGraph({ ...graph, goal: e.target.checked ? { event: "purchase" } : null })} />End the journey early for contacts who reach a goal</label>
        {graph.goal && (
          <Field label="Goal event" hint="When a contact in the journey does this, they leave it and count as converted.">
            <input className="input" value={graph.goal.event} onChange={(e) => setGraph({ ...graph, goal: { event: e.target.value.trim() } })} />
          </Field>
        )}
        <h4 className="small" style={{ margin: "18px 0 8px" }}>Schedule</h4>
        <ScheduleFields start={win.start_at} end={win.end_at} onChange={setWin} startLabel="Contacts can enter from" />
        <p className="xs muted">Contacts already inside keep going after the end.</p>
        <p className="xs muted" style={{ marginTop: 8 }}>Each contact goes through a journey once.</p>
      </fieldset>
    );
  }
  const n = graph.nodes[selected];
  if (!n) return null;
  const setN = (patch: Partial<JourneyNode>) => setGraph({ ...graph, nodes: { ...graph.nodes, [selected]: { ...n, ...patch } } });
  const ch = nodeChannel(n.type);
  const smsTemplates = templates.filter((t) => t.channel === "sms");
  return (
    <fieldset disabled={locked} style={{ border: 0, padding: 0, margin: 0 }}>
      <h3 style={{ marginBottom: 4 }}>{nodeLabel(n.type)}</h3>
      <p className="xs faint" style={{ marginBottom: 14 }}>Step ID {selected}</p>
      {n.type === "wait" && <WaitField seconds={n.seconds ?? 0} onChange={(seconds) => setN({ seconds })} />}
      {n.type === "wait_until" && <WaitUntilFields n={n} setN={setN} />}
      {n.type === "wait_for_event" && (
        <>
          <Field label="Wait until the contact does" hint={'The "did it" path continues as soon as they do.'}><input className="input" value={n.event ?? ""} placeholder="purchase" onChange={(e) => setN({ event: e.target.value.trim() })} /></Field>
          <WaitField label='Give up and take the "timed out" path after' seconds={n.seconds ?? 0} onChange={(seconds) => setN({ seconds })} />
        </>
      )}
      {n.type === "message_decision" && <DecisionFields id={selected} n={n} graph={graph} templates={templates} setN={setN} />}
      {n.type === "check" && <CheckFields key={selected} n={n} setN={setN} />}
      {n.type === "split" && <SplitFields id={selected} n={n} setN={setN} graph={graph} decision={decisions[selected]} />}
      {n.type === "update_attr" && <AttrFields attrs={n.attrs ?? {}} onChange={(attrs) => setN({ attrs })} />}
      {n.type === "update_tags" && (
        <>
          <Field label="Add tags" hint="Separate with commas, e.g. lead, webinar-2026."><TagListInput key={`${selected}-add`} value={n.add_tags ?? []} onChange={(add_tags) => setN({ add_tags })} /></Field>
          <Field label="Remove tags"><TagListInput key={`${selected}-rm`} value={n.remove_tags ?? []} onChange={(remove_tags) => setN({ remove_tags })} /></Field>
        </>
      )}
      {n.type === "adjust_points" && (
        <Field label="Points" hint="Use a negative number to take points away. Scoring rules add points for events on their own.">
          <input className="input" type="number" step={1} value={n.points ?? 0} onChange={(e) => setN({ points: Math.trunc(Number(e.target.value)) })} />
        </Field>
      )}
      {n.type === "update_list" && (
        <>
          <Field label="List">
            <select className="select" value={n.list_id ?? ""} onChange={(e) => setN({ list_id: Number(e.target.value) || undefined })}>
              <option value="">Choose a list</option>
              {segments.filter((s) => s.kind === "static").map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </Field>
          <Field label="Action">
            <select className="select" value={n.list_action ?? "add"} onChange={(e) => setN({ list_action: e.target.value as "add" | "remove" })}>
              <option value="add">Add the contact to the list</option>
              <option value="remove">Remove the contact from the list</option>
            </select>
          </Field>
          {segments.every((s) => s.kind !== "static") && <p className="xs muted">No lists yet. Create one under Audience, Lists.</p>}
        </>
      )}
      {n.type === "change_stage" && <StageField value={n.stage ?? ""} onChange={(stage) => setN({ stage: stage || undefined })} />}
      {n.type === "webhook" && (
        <>
          <Field label="POST to" hint="Must be a public https:// address. Pulse sends the contact as JSON and retries briefly; a failure is recorded and the journey continues."><input className="input" value={n.url ?? ""} placeholder="https://api.example.com/hooks/pulse" onChange={(e) => setN({ url: e.target.value.trim() })} /></Field>
          <Field label="Body (optional)" hint={'Leave empty to send {"contact": …}. Or write JSON using {{.Contact.ExternalID}}, {{.Attrs.city}}, {{json .Attrs}}.'}>
            <textarea className="textarea code" value={n.body ?? ""} onChange={(e) => setN({ body: e.target.value || undefined })} />
          </Field>
        </>
      )}
      {n.type === "condition" && (
        <Field label="Is the contact in this segment?" hint="Checked at the moment the contact reaches this step.">
          <select className="select" value={n.segment_id ?? ""} onChange={(e) => setN({ segment_id: Number(e.target.value) || undefined })}>
            <option value="">Choose a segment</option>
            {segments.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </Field>
      )}
      {ch && (
        <>
          <Field label={`${CHANNEL_NAMES[ch]} template`}>
            <select className="select" value={n.template_id ?? ""} onChange={(e) => setN({ template_id: Number(e.target.value) || undefined })}>
              <option value="">Choose a template</option>
              {templates.filter((t) => t.channel === ch).map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
            </select>
          </Field>
          <TemplateActions templateId={n.template_id} />
          {templates.filter((t) => t.channel === ch).length === 0 && <p className="xs muted" style={{ marginTop: -8, marginBottom: 12 }}>No {CHANNEL_NAMES[ch]} templates yet. <Link to={`/templates/new?channel=${ch}`}>Create one</Link></p>}
          {(ch === "whatsapp" || ch === "rcs") && (
            <Field label="If it can't be delivered, send this SMS instead" hint={`Used when the number isn't on ${CHANNEL_NAMES[ch]} or delivery fails. Sent at most once.`}>
              <select className="select" value={n.fallback_template_id ?? ""} onChange={(e) => setN({ fallback_template_id: Number(e.target.value) || undefined })}>
                <option value="">No fallback</option>
                {smsTemplates.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
              </select>
            </Field>
          )}
        </>
      )}
      {selected !== graph.entry || Object.keys(graph.nodes).length > 1 ? (
        <button className="btn danger sm" style={{ marginTop: 8 }} onClick={() => onDelete(selected)}>Remove step</button>
      ) : null}
    </fieldset>
  );
}

// DecisionFields sets up a message decision: which earlier message, what should happen to it,
// and how long to wait before taking the "no" path.
function DecisionFields({ id, n, graph, templates, setN }: { id: string; n: JourneyNode; graph: JourneyGraph; templates: Template[]; setN: (p: Partial<JourneyNode>) => void }) {
  const sends = sendSteps(graph);
  const before = upstream(graph, id);
  const name = (sid: string, s: JourneyNode, ch: Channel) => {
    const tpl = templates.find((t) => t.id === s.template_id);
    return `${CHANNEL_NAMES[ch]} ${tpl ? `‘${tpl.name}’` : "(no template yet)"} · ${sid}`;
  };
  const earlier = sends.filter(([sid]) => before.has(sid));
  const others = sends.filter(([sid]) => !before.has(sid));
  const step = n.step ? sends.find(([sid]) => sid === n.step) : undefined;
  const ch = step?.[2];
  const allowed = ch ? OUTCOMES[ch] : [];
  const pickStep = (sid: string) => {
    const c = sends.find(([x]) => x === sid)?.[2];
    const keep = c && n.outcome && OUTCOMES[c].includes(n.outcome);
    setN({ step: sid || undefined, outcome: keep ? n.outcome : c ? OUTCOMES[c][0] : undefined });
  };
  return (
    <>
      <p className="xs muted" style={{ marginBottom: 10 }}>Contacts wait here. They take “yes” the moment their message does what you choose below, or “no” once the time limit passes.</p>
      <Field label="Which message?">
        <select className="select" value={n.step ?? ""} onChange={(e) => pickStep(e.target.value)}>
          <option value="">Choose a send step</option>
          {earlier.length > 0 && <optgroup label="Earlier in this journey">{earlier.map(([sid, s, c]) => <option key={sid} value={sid}>{name(sid, s, c)}</option>)}</optgroup>}
          {others.length > 0 && <optgroup label={earlier.length ? "Other send steps" : "Send steps"}>{others.map(([sid, s, c]) => <option key={sid} value={sid}>{name(sid, s, c)}</option>)}</optgroup>}
        </select>
      </Field>
      {sends.length === 0 && <p className="xs muted" style={{ marginTop: -8, marginBottom: 12 }}>Add a send step first, then connect it to this decision.</p>}
      {step && !before.has(step[0]) && <p className="xs muted" style={{ marginTop: -8, marginBottom: 12 }}>Connect this decision after that step; until then contacts reach it without the message.</p>}
      <Field label="Take “yes” if the message">
        <select className="select" value={n.outcome ?? ""} disabled={!ch} onChange={(e) => setN({ outcome: (e.target.value || undefined) as Outcome | undefined })}>
          {!ch && <option value="">Choose a message first</option>}
          {allowed.map((o) => <option key={o} value={o}>{OUTCOME_WORDS[o].long}</option>)}
        </select>
      </Field>
      {ch === "email" && n.outcome === "opened" && <p className="xs muted" style={{ marginTop: -8, marginBottom: 12 }}>Some mail apps hide opens, so "a link is clicked" is the surer signal.</p>}
      <WaitField label="Take “no” if it hasn't happened within" seconds={n.seconds ?? 0} onChange={(seconds) => setN({ seconds })} />
    </>
  );
}

const UNITS = [{ s: 60, label: "minutes" }, { s: 3600, label: "hours" }, { s: 86400, label: "days" }];

function WaitField({ seconds, onChange, label = "Wait for" }: { seconds: number; onChange: (s: number) => void; label?: string }) {
  const unit = [...UNITS].reverse().find((u) => seconds > 0 && seconds % u.s === 0) ?? UNITS[1];
  return (
    <Field label={label}>
      <div className="row">
        <input className="input" style={{ width: 90 }} type="number" min={1} value={seconds / unit.s || ""} onChange={(e) => onChange(Math.round(Number(e.target.value) * unit.s))} />
        <select className="select inline" value={unit.s} onChange={(e) => onChange((seconds / unit.s || 1) * Number(e.target.value))}>
          {UNITS.map((u) => <option key={u.s} value={u.s}>{u.label}</option>)}
        </select>
      </div>
    </Field>
  );
}

const ZONES = ["Asia/Kolkata", "Asia/Dubai", "Asia/Singapore", "Europe/London", "America/New_York", "America/Los_Angeles", "UTC"];

function WaitUntilFields({ n, setN }: { n: JourneyNode; setN: (p: Partial<JourneyNode>) => void }) {
  const mode = n.until ? "date" : "time";
  return (
    <>
      <Field label="Continue">
        <select className="select" value={mode} onChange={(e) => setN(e.target.value === "date" ? { until: new Date(Date.now() + 86400000).toISOString(), time_of_day: undefined, timezone: undefined } : { until: undefined, time_of_day: "09:00", timezone: "Asia/Kolkata" })}>
          <option value="time">at the next time of day</option>
          <option value="date">at a specific date and time</option>
        </select>
      </Field>
      {mode === "time" ? (
        <>
          <Field label="Time of day"><input className="input" type="time" value={n.time_of_day ?? "09:00"} onChange={(e) => setN({ time_of_day: e.target.value })} /></Field>
          <Field label="In this time zone" hint={'A contact with a "timezone" attribute (e.g. Europe/London) gets their own local time.'}>
            <select className="select" value={n.timezone ?? "Asia/Kolkata"} onChange={(e) => setN({ timezone: e.target.value })}>{ZONES.map((z) => <option key={z}>{z}</option>)}</select>
          </Field>
        </>
      ) : (
        <Field label="Date and time" hint="Contacts who arrive after it continue straight away.">
          <input className="input" type="datetime-local" value={toLocalInput(n.until)} onChange={(e) => setN({ until: fromLocalInput(e.target.value) })} />
        </Field>
      )}
    </>
  );
}

// CheckFields keeps the half-typed rule as a draft; the step only stores it once it's valid.
// It is keyed by step id, so selecting another step starts from that step's rule.
function CheckFields({ n, setN }: { n: JourneyNode; setN: (p: Partial<JourneyNode>) => void }) {
  const [root, setRoot] = useState(() => rootFromRule(n.rule));
  const issues = ruleProblems(root);
  return (
    <>
      <p className="xs muted" style={{ marginBottom: 6 }}>Contacts who match take the "matches" path, checked the moment they reach this step. Event counts work too, e.g. did email_opened at least 1 time in 3 days.</p>
      <GroupEditor group={root} onChange={(g) => { setRoot(g); setN({ rule: ruleProblems(g).length ? undefined : toRule(g) }); }} />
      {issues.length > 0 && <p className="xs" style={{ color: "var(--danger)" }}>{issues[0]}</p>}
    </>
  );
}

function SplitFields({ id, n, setN, graph, decision }: { id: string; n: JourneyNode; setN: (p: Partial<JourneyNode>) => void; graph: JourneyGraph; decision?: SplitDecision }) {
  const vs = n.variants ?? [];
  const set = (i: number, p: Partial<(typeof vs)[number]>) => setN({ variants: vs.map((v, j) => (j === i ? { ...v, ...p } : v)) });
  const w = n.winner;
  const setW = (p: Partial<SplitWinner>) => setN({ winner: { ...(w ?? { metric: "clicked", after_seconds: 86400, min_per_variant: 100 }), ...p } });
  const problem = winnerProblem(graph, id);
  return (
    <>
      {decision && <SplitDecisionNote decision={decision} />}
      <p className="xs muted" style={{ marginBottom: 8 }}>Each contact takes one path, at random by share, and always the same one. Compare paths in the journey's report.</p>
      {vs.map((v, i) => (
        <div className="row" key={i} style={{ marginBottom: 6, flexWrap: "nowrap" }}>
          <input className="input" style={{ width: 70 }} value={v.label ?? ""} placeholder={String.fromCharCode(65 + i)} onChange={(e) => set(i, { label: e.target.value })} aria-label="Path name" />
          <input className="input" style={{ width: 70 }} type="number" min={1} value={v.weight} onChange={(e) => set(i, { weight: Number(e.target.value) })} aria-label="Share" />
          <span className="xs muted" style={{ width: 34 }}>{share(n, i)}%</span>
          {vs.length > 2 && <button className="btn ghost sm" onClick={() => setN({ variants: vs.filter((_, j) => j !== i) })}>Remove</button>}
        </div>
      ))}
      {vs.length < 5 && <button className="btn sm" onClick={() => setN({ variants: [...vs, { weight: 1, label: String.fromCharCode(65 + vs.length) }] })}>Add a path</button>}
      <h4 className="small" style={{ margin: "18px 0 8px" }}>Winner</h4>
      <label className="check"><input type="checkbox" checked={!!w} onChange={(e) => setN({ winner: e.target.checked ? { metric: graph.goal ? "converted" : "clicked", after_seconds: 86400, min_per_variant: 100 } : null })} />Pick a winner automatically and send everyone after down it</label>
      {w && (
        <>
          <Field label="Best path by">
            <select className="select" value={w.metric} onChange={(e) => setW({ metric: e.target.value as SplitWinner["metric"] })}>
              <option value="opened">open rate of the emails or inbox messages after the split</option>
              <option value="clicked">click rate of the emails or inbox messages after the split</option>
              <option value="converted">conversion rate (the journey's goal)</option>
            </select>
          </Field>
          <WaitField label="Test for at least" seconds={w.after_seconds} onChange={(after_seconds) => setW({ after_seconds })} />
          <Field label="And until every path has" hint="Contacts who went down the path. The test keeps going until each path has this many.">
            <div className="row"><input className="input" style={{ width: 110 }} type="number" min={1} value={w.min_per_variant || ""} onChange={(e) => setW({ min_per_variant: Math.trunc(Number(e.target.value)) })} /><span className="small muted">contacts</span></div>
          </Field>
          <p className="xs muted">Until then contacts are split by share. The best rate wins (ties: more hits, then the first path); the decision is final and shows here and in the report.</p>
          {problem && <p className="xs" style={{ color: "var(--danger)" }}>{problem}</p>}
        </>
      )}
    </>
  );
}

const METRIC_WORDS: Record<string, string> = { opened: "open rate", clicked: "click rate", converted: "conversion rate" };

function SplitDecisionNote({ decision }: { decision: SplitDecision }) {
  return (
    <div className="panel" style={{ padding: "10px 12px", marginBottom: 12, borderColor: "var(--ok)" }} role="status">
      <strong className="small">Winner: path {decision.label || String.fromCharCode(65 + decision.path)}</strong>
      <p className="xs muted" style={{ margin: "4px 0 0" }}>
        Picked by {METRIC_WORDS[decision.metric] ?? decision.metric} on {new Date(decision.decided_at).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}.
        {" "}Everyone reaching this step now takes it.
      </p>
      <ul className="xs" style={{ margin: "6px 0 0", paddingLeft: 16 }}>
        {decision.stats.map((st) => <li key={st.path}>Path {String.fromCharCode(65 + st.path)}: {st.hits} of {st.entrants} ({(st.rate * 100).toFixed(1)}%)</li>)}
      </ul>
    </div>
  );
}

// StageField picks the stage a change_stage step moves the contact to.
function StageField({ value, onChange }: { value: string; onChange: (stage: string) => void }) {
  const stages = useStages().data ?? [];
  return (
    <>
      <Field label="Move the contact to" hint="A contact is in one stage at a time; this replaces their current stage, forwards or back.">
        <select className="select" value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="">Choose a stage</option>
          {value && !stages.some((s) => s.name === value) && <option value={value}>{value} (no longer exists)</option>}
          {stages.map((s) => <option key={s.id} value={s.name}>{s.name}</option>)}
        </select>
      </Field>
      {stages.length === 0 && <p className="xs muted">No stages yet. Add them under Workspace, Stages.</p>}
    </>
  );
}

function AttrFields({ attrs, onChange }: { attrs: Record<string, unknown>; onChange: (a: Record<string, unknown>) => void }) {
  const rows = Object.entries(attrs);
  const rename = (i: number, k: string) => onChange(Object.fromEntries(rows.map(([kk, v], j) => (j === i ? [k, v] : [kk, v]))));
  return (
    <div className="field">
      <span>Set these attributes</span>
      {rows.map(([k, v], i) => (
        <div className="row" key={i} style={{ flexWrap: "nowrap" }}>
          <input className="input" placeholder="attribute" value={k} onChange={(e) => rename(i, e.target.value.trim())} aria-label="Attribute" />
          <input className="input" placeholder="value" value={String(v ?? "")} onChange={(e) => onChange({ ...attrs, [k]: e.target.value })} aria-label="Value" />
          <button className="btn ghost sm" onClick={() => onChange(Object.fromEntries(rows.filter((_, j) => j !== i)))}>×</button>
        </div>
      ))}
      <div><button className="btn sm" onClick={() => onChange({ ...attrs, [`attribute_${rows.length + 1}`]: "" })}>Add attribute</button></div>
      <small>Consent (optout_*), tags, points, stage and company attributes can't be set here: use their own steps, or the contact's company.</small>
    </div>
  );
}
