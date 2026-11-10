import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Background, Controls, MarkerType, MiniMap, Panel, ReactFlow, ReactFlowProvider, useReactFlow,
  type Connection, type Edge, type EdgeChange, type IsValidConnection, type Node as FlowNode, type NodeChange,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, CHANNEL_NAMES, type Channel, type Journey, type JourneyGraph, type JourneyReport, type Segment, type SplitReport, type Template } from "../api";
import { useCan } from "../auth";
import { money } from "../insights";
import { JOURNEY_CATALOG, catalogItem, reasonAt, searchItems, type CatalogCtx, type JourneyItem } from "../journeyCatalog";
import {
  addStep, connect, defaultInsertPoint, deleteStep, disconnect, emptyGraph, groupOf, insertStep, issues as graphIssues, overlapping, moveNode, nodeChannel,
  removeBranch, share, STEP_GROUPS, stepLabel, stepNumbers, tidy, toFlow, TRIGGER_ID, wouldCycle, type EdgeData, type FlowData, type InsertAt,
} from "../journeyGraph";
import { CountsTable, DailyBars, metricsFor, ReportPanel, TopLinks, type CountsRow } from "../reports";
import { describeWindow, nowISO, scheduleProblem } from "../schedule";
import { Empty, ErrorNote, LiveNumber, PageHead, Status, useToast } from "../ui";
import { CanvasCtx, edgeTypes, nodeTypes, triggerText, whereText, type CanvasCtxValue } from "./journey/Canvas";
import { usePickers } from "./journey/data";
import { record, redo, startHistory, undo, type History } from "./journey/history";
import { JIcon } from "./journey/icons";
import { Inspector, METRIC_WORDS, SplitDecisionNote } from "./journey/Inspector";
import { StepChooser } from "./journey/StepChooser";
import "./journeys.css";

export function JourneyList() {
  const q = useQuery({ queryKey: ["journeys"], queryFn: () => api.get<{ journeys: Journey[] }>("/v1/journeys"), refetchInterval: 4000 });
  const js = q.data?.journeys ?? [];
  const sum = (f: (st: Record<string, number>) => number) => js.reduce((a, j) => a + f(j.stats?.status ?? {}), 0);
  const entered = (st: Record<string, number>) => Object.values(st).reduce((a, b) => a + b, 0);
  const inProgress = (st: Record<string, number>) => (st.active ?? 0) + (st.waiting ?? 0);
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
            <thead><tr><th>Name</th><th>Status</th><th>Schedule</th><th className="num">Steps</th><th className="num">Entered</th><th className="num">In progress</th><th className="num">Completed</th></tr></thead>
            <tbody>
              {js.map((j) => {
                const st = j.stats?.status ?? {};
                return (
                  <tr key={j.id}>
                    <td><Link to={`/journeys/${j.id}`}>{j.name}</Link></td>
                    <td><Status value={j.status} /></td>
                    <td className="muted small">{describeWindow(j.start_at, j.end_at)}</td>
                    <td className="num">{Object.keys(j.graph?.nodes ?? {}).length}</td>
                    <td className="num"><LiveNumber value={entered(st)} /></td>
                    <td className="num"><LiveNumber value={inProgress(st)} /></td>
                    <td className="num"><LiveNumber value={st.completed ?? 0} /></td>
                  </tr>
                );
              })}
            </tbody>
            {js.length > 1 && (
              <tfoot><tr><th colSpan={4}>All journeys</th><td className="num"><LiveNumber value={sum(entered)} /></td><td className="num"><LiveNumber value={sum(inProgress)} /></td><td className="num"><LiveNumber value={sum((st) => st.completed ?? 0)} /></td></tr></tfoot>
            )}
          </table>
        )}
      </div>
    </div>
  );
}

export function JourneyEdit() {
  return <ReactFlowProvider><JourneyEditor /></ReactFlowProvider>;
}

const EDGE_COLORS: Record<EdgeData["kind"], string> = { next: "#8a93a3", yes: "#157347", no: "#c2410c", path: "#6b4fd8", jump: "#8a93a3" };
const PALETTE_MIME = "application/x-pulse-step";
const NO_SEGMENTS: Segment[] = [];

const isEditable = (el: EventTarget | null) =>
  el instanceof HTMLElement && (el.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName));

function JourneyEditor() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const flow = useReactFlow();
  const canEditContacts = useCan("contacts", "edit");

  const existing = useQuery({ queryKey: ["journey", id], queryFn: () => api.get<Journey>(`/v1/journeys/${id}`), enabled: !isNew });
  const stats = useQuery({ queryKey: ["journey-stats", id], queryFn: () => api.get<Journey["stats"]>(`/v1/journeys/${id}/stats`), enabled: !isNew, refetchInterval: 3000 });
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const tpls = useQuery({ queryKey: ["templates", ""], queryFn: () => api.get<{ templates: Template[] }>("/v1/templates") });

  const [name, setName] = useState("Untitled journey");
  const [hist, setHist] = useState<History<JourneyGraph>>(() => startHistory(emptyGraph()));
  const graph = hist.present;
  const [win, setWin] = useState<{ start_at?: string; end_at?: string }>({ start_at: nowISO() });
  const [selected, setSelected] = useState<string | null>(TRIGGER_ID);
  const [loaded, setLoaded] = useState(isNew);
  const [view, setView] = useState<"canvas" | "report">("canvas");
  const [showStats, setShowStats] = useState(true);
  const [chooser, setChooser] = useState<{ at: InsertAt; opener: HTMLElement | null } | null>(null);
  const [paletteQ, setPaletteQ] = useState("");
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [, bump] = useState(0);

  const status = existing.data?.status ?? "draft";
  const locked = status === "active";
  const journeyId = isNew ? undefined : Number(id);
  const statsOn = showStats && !isNew;
  const report = useQuery({ queryKey: ["journey-report", id], queryFn: () => api.get<JourneyReport>(`/v1/journeys/${id}/report`), enabled: statsOn && view === "canvas", refetchInterval: 5000 });

  const fit = useCallback(() => flow.fitView({ padding: 0.25, maxZoom: 1, duration: 250 }), [flow]);
  useEffect(() => {
    if (existing.data && !loaded) {
      setName(existing.data.name);
      setHist(startHistory(existing.data.graph));
      setWin({ start_at: existing.data.start_at, end_at: existing.data.end_at ?? undefined });
      setLoaded(true);
      // The canvas first mounted with a placeholder graph; frame the real one once it has rendered.
      window.setTimeout(() => flow.fitView({ padding: 0.25, maxZoom: 1 }), 60);
    }
  }, [existing.data, loaded, flow]);

  /** setGraph records an edit; edits sharing a key within a second (typing, dragging) undo together. */
  const setGraph = useCallback((g: JourneyGraph, key?: string) => setHist((h) => record(h, g, key)), []);
  const doUndo = useCallback(() => !locked && setHist(undo), [locked]);
  const doRedo = useCallback(() => !locked && setHist(redo), [locked]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || isEditable(e.target) || view !== "canvas") return;
      const k = e.key.toLowerCase();
      if (k === "z" && !e.shiftKey) { e.preventDefault(); doUndo(); }
      else if ((k === "z" && e.shiftKey) || k === "y") { e.preventDefault(); doRedo(); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [doUndo, doRedo, view]);

  const pickers = usePickers(segs.data?.segments ?? NO_SEGMENTS);
  const numbers = useMemo(() => stepNumbers(graph), [graph]);
  const { nodes, edges } = useMemo(() => toFlow(graph), [graph]);
  // Nodes are rebuilt from the graph on every change, so keep the sizes React Flow measured (the
  // minimap and fit-view need them).
  const measured = useRef(new Map<string, { width: number; height: number }>());
  const flowNodes: FlowNode<FlowData>[] = nodes.map((n) => ({ ...n, measured: measured.current.get(n.id), selected: n.id === selected, draggable: !locked && n.id !== TRIGGER_ID }));
  const flowEdges = edges.map((e) => {
    const kind = (e.data as EdgeData).kind;
    return { ...e, selected: e.id === selectedEdge, markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16, color: EDGE_COLORS[kind] }, style: { stroke: EDGE_COLORS[kind] }, deletable: kind !== "jump" && !locked };
  }) as Edge[];
  const winProblem = scheduleProblem(win.start_at, win.end_at);
  const issueList = graphIssues(graph, { journeyId });
  const allIssues = winProblem ? [...issueList, { text: winProblem }] : issueList;
  const catCtx: CatalogCtx = useMemo(() => ({ canEditContacts }), [canEditContacts]);

  const openChooser = useCallback((at: InsertAt, opener: HTMLElement) => setChooser({ at, opener }), []);
  const closeChooser = () => {
    const opener = chooser?.opener;
    setChooser(null);
    window.setTimeout(() => { if (opener?.isConnected) opener.focus(); }, 0);
  };
  const focusInspector = () => window.setTimeout(() => document.getElementById("inspector-title")?.focus(), 30);
  const insert = (item: JourneyItem, at: InsertAt): boolean => {
    const why = reasonAt(item, graph, at, catCtx);
    if (why) { toast({ text: why, error: true }); return false; }
    const r = insertStep(graph, at, item.make(graph, at));
    if (!r) return false;
    // Keep the canvas readable: a step that lands on another one tidies the layout.
    setGraph(overlapping(r.graph) ? tidy(r.graph) : r.graph);
    setSelected(r.id);
    focusInspector();
    return true;
  };
  const pickFromChooser = (item: JourneyItem) => {
    if (!chooser) return;
    if (insert(item, chooser.at)) setChooser(null);
  };
  const paletteAt = defaultInsertPoint(graph, selected);
  const addFromPalette = (item: JourneyItem) => insert(item, paletteAt);

  const onNodesChange = (changes: NodeChange[]) => {
    let g = graph;
    let key: string | undefined;
    let resized = false;
    for (const c of changes) {
      if (c.type === "dimensions" && c.dimensions) { measured.current.set(c.id, c.dimensions); resized = true; }
      if (c.type === "position" && c.position && c.id !== TRIGGER_ID && !locked) { g = moveNode(g, c.id, c.position.x, c.position.y); key = `move:${c.id}`; }
      if (c.type === "select" && c.selected) setSelected(c.id);
    }
    if (g !== graph) setGraph(g, key);
    else if (resized) bump((x) => x + 1);
  };
  // Removals arrive here (Backspace / Delete) rather than as changes, so deleting a step and its
  // edges in one go heals over the step instead of first cutting the path into it.
  const onDelete = ({ nodes: ns, edges: es }: { nodes: FlowNode[]; edges: Edge[] }) => {
    if (locked) return;
    const gone = new Set(ns.map((n) => n.id));
    setHist((h) => {
      let g = h.present;
      for (const n of ns) if (n.id !== TRIGGER_ID) g = deleteStep(g, n.id);
      for (const e of es) if (!gone.has(e.source) && !gone.has(e.target) && !e.id.startsWith("jump:")) g = disconnect(g, e.id);
      return record(h, g);
    });
    if (selected && gone.has(selected)) setSelected(null);
  };
  // Edges are derived from the graph: only their selection is kept here (removals go through onDelete).
  const onEdgesChange = (changes: EdgeChange[]) => {
    for (const c of changes) if (c.type === "select") setSelectedEdge((cur) => (c.selected ? c.id : cur === c.id ? null : cur));
  };
  const isValidConnection: IsValidConnection = (c) =>
    !locked && !!c.source && !!c.target && c.target !== TRIGGER_ID && c.sourceHandle !== "jump" && !wouldCycle(graph, c.source, c.target);
  const onConnect = (c: Connection) => { if (!locked && !wouldCycle(graph, c.source, c.target)) setGraph(connect(graph, c.source, c.sourceHandle, c.target)); };

  const onDrop = (e: React.DragEvent) => {
    const item = catalogItem(e.dataTransfer.getData(PALETTE_MIME));
    if (!item || locked) return;
    e.preventDefault();
    if (item.id === "push_integration") return;
    const p = flow.screenToFlowPosition({ x: e.clientX, y: e.clientY });
    const r = addStep(graph, item.make(graph), p.x - 120, p.y - 30);
    setGraph(r.graph);
    setSelected(r.id);
    toast({ text: "Step added. Connect it: drag from the bottom dot of the step before it onto it." });
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

  const ctx: CanvasCtxValue = {
    graph, names: pickers.names, numbers, locked,
    counts: report.data?.enrollments.node ?? stats.data?.node ?? {},
    showStats: statsOn, steps: report.data?.steps, messages: report.data?.messages.by_node,
    onAdd: openChooser, triggerText: triggerText(graph, pickers.names.segments ?? {}),
  };
  const templateMap = useMemo(() => new Map((tpls.data?.templates ?? []).map((t) => [t.id, t])), [tpls.data]);

  const selectStep = (sid: string) => {
    setSelected(sid);
    const n = flow.getNode(sid);
    if (n) flow.setCenter(n.position.x + 120, n.position.y + 60, { zoom: flow.getZoom(), duration: 250 });
  };

  return (
    <div className="page" style={{ maxWidth: "none" }}>
      <PageHead
        back={<Link className="back" to="/journeys">Journeys</Link>}
        title={<input className="input" style={{ fontSize: "var(--fs-xl)", fontWeight: 650, border: 0, padding: 0, background: "transparent" }} value={name} onChange={(e) => setName(e.target.value)} aria-label="Journey name" disabled={locked} />}
        actions={<>
          {!isNew && <Status value={status} />}
          {!locked && <button className="btn" disabled={save.isPending} onClick={() => save.mutate()}>Save draft</button>}
          {!isNew && status !== "active" && <button className="btn primary" disabled={allIssues.length > 0 || setStatus.isPending} onClick={async () => { await save.mutateAsync(); setStatus.mutate("activate"); }}>Save and go live</button>}
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
        <JourneyReportView id={id!} graph={graph} templates={templateMap} numbers={numbers} onStep={(sid) => { setSelected(sid); setView("canvas"); }} />
      ) : (
      <CanvasCtx.Provider value={ctx}>
        <div className="journey-layout">
          <Palette q={paletteQ} setQ={setPaletteQ} locked={locked} graph={graph} at={paletteAt} ctx={catCtx} onAdd={addFromPalette}
            atText={whereText(graph, numbers, paletteAt)} />
          <div className="journey-canvas" onDragOver={(e) => { if (e.dataTransfer.types.includes(PALETTE_MIME)) { e.preventDefault(); e.dataTransfer.dropEffect = "copy"; } }} onDrop={onDrop}>
            <ReactFlow
              nodes={flowNodes}
              edges={flowEdges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onDelete={onDelete}
              onConnect={onConnect}
              isValidConnection={isValidConnection}
              onPaneClick={() => setSelected(null)}
              fitView
              fitViewOptions={{ padding: 0.25, maxZoom: 1 }}
              minZoom={0.2}
              nodesConnectable={!locked}
              deleteKeyCode={locked ? null : ["Backspace", "Delete"]}
              proOptions={{ hideAttribution: true }}
            >
              <Background gap={20} color="#dce1e8" />
              <Controls showInteractive={false} showFitView={false} />
              <MiniMap pannable zoomable ariaLabel="Journey overview" nodeStrokeWidth={2} nodeColor={(n) => {
                const d = n.data as FlowData;
                return d.isTrigger ? "#f0a202" : ({ action: "#2747d6", decision: "#c0496b", condition: "#6b4fd8", wait: "#8a93a3", split: "#0e7490" } as const)[groupOf(d.node!.type)];
              }} />
              <Panel position="top-left" className="j-toolbar" aria-label="Canvas tools">
                <button type="button" className="btn sm" onClick={doUndo} disabled={locked || hist.past.length === 0} title="Undo (Ctrl+Z)" aria-keyshortcuts="Control+Z Meta+Z">Undo</button>
                <button type="button" className="btn sm" onClick={doRedo} disabled={locked || hist.future.length === 0} title="Redo (Ctrl+Shift+Z)" aria-keyshortcuts="Control+Shift+Z Meta+Shift+Z">Redo</button>
                <button type="button" className="btn sm" disabled={locked} onClick={() => { setGraph(tidy(graph)); window.setTimeout(fit, 50); }}>Tidy up</button>
                <button type="button" className="btn sm" onClick={fit}>Zoom to fit</button>
                {!isNew && <label className="check j-toolbar-check"><input type="checkbox" checked={showStats} onChange={(e) => setShowStats(e.target.checked)} />Show stats</label>}
              </Panel>
              <Panel position="bottom-center" className="j-legend" aria-hidden="true">
                <span className="lg lg-yes">Yes</span><span className="lg lg-no">No</span><span className="lg lg-path">Split path</span><span className="lg lg-jump">Jump</span>
              </Panel>
            </ReactFlow>
          </div>
          <aside className="inspector" aria-label="Step settings">
            <Inspector
              graph={graph} setGraph={setGraph} win={win} setWin={setWin} selected={selected} locked={locked}
              decisions={existing.data?.split_winners ?? {}} pickers={pickers} templates={tpls.data?.templates ?? []}
              journeyId={journeyId} numbers={numbers} canEditContacts={canEditContacts}
              onDelete={(nid) => { setGraph(deleteStep(graph, nid)); setSelected(null); }}
              onDeleteBranch={(nid) => { setGraph(removeBranch(graph, nid)); setSelected(null); }}
            />
            {allIssues.length > 0 && (
              <div className="j-issues">
                <h4 className="small">Before it can go live</h4>
                <ul className="xs">
                  {allIssues.map((p) => (
                    <li key={p.text}>
                      {"step" in p && p.step ? <button type="button" className="linklike" onClick={() => selectStep(p.step!)}>{p.text}</button> : p.text}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </aside>
        </div>
        {chooser && (
          <StepChooser graph={graph} at={chooser.at} where={whereText(graph, numbers, chooser.at)} ctx={catCtx} onPick={pickFromChooser} onClose={closeChooser} />
        )}
      </CanvasCtx.Provider>
      )}
    </div>
  );
}

// Palette: every step by category, searchable. Click or Enter adds the step after the selected
// step (its first free output); drag one onto the canvas to place it yourself.
function Palette({ q, setQ, locked, graph, at, atText, ctx, onAdd }: {
  q: string; setQ: (q: string) => void; locked: boolean; graph: JourneyGraph; at: InsertAt; atText: string; ctx: CatalogCtx; onAdd: (it: JourneyItem) => void;
}) {
  const found = searchItems(JOURNEY_CATALOG, q);
  const listRef = useRef<HTMLDivElement>(null);
  return (
    <aside className="palette" aria-label="Steps">
      <h4>Add a step</h4>
      <input className="input palette-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search steps" aria-label="Search the step palette" />
      <p className="xs faint palette-at">Click adds it: {atText.charAt(0).toLowerCase() + atText.slice(1)}.</p>
      <div ref={listRef}>
        {STEP_GROUPS.map((grp) => {
          const items = found.filter((it) => it.category === grp.group);
          if (items.length === 0) return null;
          return (
            <section className={`palette-group c-${grp.group}`} key={grp.group} aria-label={`${grp.label} steps`}>
              <h5 title={grp.hint}>{grp.label}</h5>
              {items.map((it) => {
                const why = locked ? "The journey is live." : reasonAt(it, graph, at, ctx);
                const hardOff = it.id === "push_integration" || locked;
                return (
                  <button
                    key={it.id} type="button" className={`palette-item${why ? " off" : ""}`} disabled={hardOff}
                    aria-disabled={why ? true : undefined} draggable={!hardOff}
                    onDragStart={(e) => { e.dataTransfer.setData(PALETTE_MIME, it.id); e.dataTransfer.effectAllowed = "copy"; }}
                    onClick={() => onAdd(it)} title={why ?? it.description}
                    aria-label={it.label} aria-describedby={`pal-${it.id}`}
                  >
                    <span className="palette-label"><JIcon name={it.icon} size={14} /> {it.label}</span>
                    <small id={`pal-${it.id}`}>{why ?? it.description}</small>
                  </button>
                );
              })}
            </section>
          );
        })}
        {found.length === 0 && <p className="xs muted">No step matches “{q}”.</p>}
      </div>
      <p className="xs muted" style={{ marginTop: 14 }}>Or drag a step onto the canvas, then drag from its bottom dot to connect it. Select a line and press Backspace to remove it.</p>
    </aside>
  );
}

// JourneyReportView: who entered and where they are, and what each send step delivered.
function JourneyReportView({ id, graph, templates, numbers, onStep }: { id: string; graph: JourneyGraph; templates: Map<number, Template>; numbers: Record<string, number>; onStep: (stepId: string) => void }) {
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
        <span key="s">{n ? <><span className="faint xs">{numbers[sid]}.</span> {ch ? <span className={`chan chan-${ch}`}>{stepLabel(graph, n)}</span> : stepLabel(graph, n)}</> : <span className="muted">Removed step</span>}</span>,
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
          <ReportPanel title={<>{graph.nodes[sid] ? stepLabel(graph, graph.nodes[sid]) : "Split"} <span className="faint xs">step {numbers[sid] ?? sid}</span></>} aside={<button className="btn ghost sm" onClick={() => onStep(sid)}>Open step</button>}>
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
