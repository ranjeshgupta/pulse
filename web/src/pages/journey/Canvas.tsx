// Canvas pieces for the journey builder: the step card (number badge, icon, category colour, label,
// summary, stats, and a "+" under every free output), the edge with its Yes / No / path label and a
// "+" at its midpoint, and the dashed edge from a Jump to step to its target.
import { describeWhere } from "../../segmentRules";
import {
  BaseEdge, EdgeLabelRenderer, getBezierPath, getSmoothStepPath, Handle, Position, type EdgeProps, type NodeProps,
} from "@xyflow/react";
import { createContext, useContext } from "react";
import type { JourneyGraph, MessageCounts, StepStats } from "../../api";
import { itemForNode } from "../../journeyCatalog";
import {
  emptyGraph, groupOf, nodeChannel, outputs, pathName, shapeOf, stepLabel, targetAt, TRIGGER_ID,
  type EdgeData, type FlowData, type InsertAt,
} from "../../journeyGraph";
import { JIcon } from "./icons";
import { summary, type Names } from "./nodeText";

export type CanvasCtxValue = {
  graph: JourneyGraph; names: Names; numbers: Record<string, number>; locked: boolean;
  /** Contacts at each step now. */
  counts: Record<string, number>;
  /** Per-step report counts and messages, when stats are shown. */
  showStats: boolean; steps?: Record<string, StepStats>; messages?: Record<string, MessageCounts>;
  onAdd: (at: InsertAt, opener: HTMLElement) => void;
  triggerText: string;
};
export const CanvasCtx = createContext<CanvasCtxValue>({
  graph: emptyGraph(), names: {}, numbers: {}, locked: false, counts: {}, showStats: false, onAdd: () => {}, triggerText: "",
});

/** stepName names a step for buttons and the chooser, e.g. "Send email (step 2)", or "Start". */
export function stepName(g: JourneyGraph, numbers: Record<string, number>, id: string): string {
  if (id === TRIGGER_ID) return "Start";
  const n = g.nodes[id];
  return n ? `${stepLabel(g, n)} (step ${numbers[id]})` : id;
}

/** addLabel is the accessible name of the "+" for an insertion point. */
export function addLabel(g: JourneyGraph, numbers: Record<string, number>, at: InsertAt): string {
  const name = stepName(g, numbers, at.source);
  if (at.handle === "yes") return `Add step on Yes path of ${name}`;
  if (at.handle === "no") return `Add step on No path of ${name}`;
  if (at.handle.startsWith("v")) return `Add step on path ${pathName(g.nodes[at.source], Number(at.handle.slice(1)))} of ${name}`;
  return `Add step after ${name}`;
}

/** whereText says where the chooser will put the step. */
export function whereText(g: JourneyGraph, numbers: Record<string, number>, at: InsertAt): string {
  const b = targetAt(g, at);
  const path = at.handle === "yes" ? "the Yes path of " : at.handle === "no" ? "the No path of " : at.handle.startsWith("v") ? `path ${pathName(g.nodes[at.source], Number(at.handle.slice(1)))} of ` : "";
  const from = `${path}${stepName(g, numbers, at.source)}`;
  return b ? `Between ${from} and ${stepName(g, numbers, b)}` : path ? `On ${from}` : `After ${from}`;
}

function Plus({ at }: { at: InsertAt }) {
  const c = useContext(CanvasCtx);
  if (c.locked) return null;
  return (
    <button
      type="button" className="j-plus nodrag nopan" aria-label={addLabel(c.graph, c.numbers, at)} title={addLabel(c.graph, c.numbers, at)}
      onClick={(e) => { e.stopPropagation(); c.onAdd(at, e.currentTarget); }}
    >
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M6 1v10M1 6h10" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg>
    </button>
  );
}

const kindOf = (h: string): EdgeData["kind"] => (h === "yes" ? "yes" : h === "no" ? "no" : h === "next" ? "next" : "path");

// A "+" stub hanging under a free output, labelled for branching steps.
function Stub({ at, left, label }: { at: InsertAt; left: string; label?: string }) {
  const c = useContext(CanvasCtx);
  if (c.locked && !label) return null;
  return (
    <div className="j-stub nodrag nopan" style={{ left }}>
      <span className={`j-stub-line je-${kindOf(at.handle)}`} />
      {label && <span className={`je-label je-${kindOf(at.handle)}`}>{label}</span>}
      <Plus at={at} />
    </div>
  );
}

function StatsRow({ id }: { id: string }) {
  const c = useContext(CanvasCtx);
  if (!c.showStats) return null;
  const n = c.graph.nodes[id];
  if (!n) return null;
  const st = c.steps?.[id] ?? {};
  const here = c.counts[id] ?? 0;
  const parts: string[] = [`Entered ${(st.entered ?? 0).toLocaleString("en-IN")}`];
  if (here || shapeOf(n.type) === "decision" || n.type.startsWith("wait")) parts.push(`${n.type === "exit" ? "Finished" : "Here now"} ${here.toLocaleString("en-IN")}`);
  const lines = [parts.join(" · ")];
  const shape = shapeOf(n.type);
  if (shape === "decision" || shape === "condition") lines.push(`Yes ${(st.yes ?? 0).toLocaleString("en-IN")} · No ${(st.no ?? 0).toLocaleString("en-IN")}`);
  if (shape === "split") lines.push((n.variants ?? []).map((_, i) => `${pathName(n, i)} ${(st[`v${i}`] ?? 0).toLocaleString("en-IN")}`).join(" · "));
  const m = c.messages?.[id];
  const ch = nodeChannel(n.type);
  if (m && (ch || n.type === "send_message")) {
    const tracked = ch === "email" || ch === "inbox" || n.type === "send_message";
    lines.push(`Sent ${m.sent.toLocaleString("en-IN")} · ${tracked ? `Opened ${m.opened.toLocaleString("en-IN")}` : ch === "whatsapp" || ch === "rcs" ? `Read ${m.read.toLocaleString("en-IN")}` : `Delivered ${m.delivered.toLocaleString("en-IN")}`}`);
  }
  return <div className="jnode-stat">{lines.map((l) => <div key={l}>{l}</div>)}</div>;
}

export function PulseNode({ data, selected }: NodeProps) {
  const c = useContext(CanvasCtx);
  const d = data as FlowData;
  if (d.isTrigger) {
    const free = !c.graph.nodes[c.graph.entry];
    return (
      <div className={`jnode c-start${selected ? " selected" : ""}`}>
        <div className="jnode-head"><span className="jnode-icon"><JIcon name="play" /></span><span className="jnode-label">Start</span></div>
        <div className="jnode-body">{c.triggerText}</div>
        <Handle type="source" position={Position.Bottom} id="next" />
        {free ? <Stub at={{ source: TRIGGER_ID, handle: "next" }} left="50%" /> : null}
      </div>
    );
  }
  // React Flow can render a card once more after its step left the graph (e.g. the placeholder's
  // Exit when a journey without one loads): draw nothing for it.
  const n = c.graph.nodes[d.id];
  if (!n) return null;
  const g = c.graph;
  const group = groupOf(n.type);
  const item = itemForNode(g, n);
  const outs = outputs(n);
  const fb = n.fallback_template_id ? c.names.templates?.[n.fallback_template_id] : undefined;
  return (
    <div className={`jnode c-${group} t-${n.type}${selected ? " selected" : ""}`} data-step={c.numbers[d.id]}>
      <Handle type="target" position={Position.Top} />
      <div className="jnode-head">
        <span className="jnode-num" title={`Step ${c.numbers[d.id]}`}>{c.numbers[d.id]}</span>
        {item && <span className="jnode-icon"><JIcon name={item.icon} /></span>}
        <span className="jnode-label">{stepLabel(g, n)}</span>
      </div>
      <div className="jnode-body">
        {summary(g, d.id, c.names)}
        {fb && <div className="faint">SMS fallback: {fb}</div>}
      </div>
      <StatsRow id={d.id} />
      {outs.map(([h, label], i) => {
        const left = `${((i + 1) / (outs.length + 1)) * 100}%`;
        const free = !targetAt(g, { source: d.id, handle: h });
        return (
          <span key={h}>
            <Handle type="source" position={Position.Bottom} id={h} className={`h-${kindOf(h)}`} style={{ left }} />
            {free && <Stub at={{ source: d.id, handle: h }} left={left} label={h === "no" && shapeOf(n.type) === "decision" ? "No" : label} />}
          </span>
        );
      })}
      {n.type === "jump" && <Handle type="source" position={Position.Right} id="jump" className="h-jump" isConnectable={false} />}
    </div>
  );
}

export function AddEdge(props: EdgeProps) {
  const { id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd, style } = props;
  const data = props.data as EdgeData;
  const [path, lx, ly] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 10 });
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} style={style} className={`je je-${data.kind}`} interactionWidth={18} />
      <EdgeLabelRenderer>
        <div className="je-mid nodrag nopan" style={{ transform: `translate(-50%, -50%) translate(${lx}px, ${ly}px)` }}>
          {data.label && <span className={`je-label je-${data.kind}`}>{data.label}</span>}
          <Plus at={{ source: data.source, handle: data.handle }} />
        </div>
      </EdgeLabelRenderer>
    </>
  );
}

export function JumpEdge(props: EdgeProps) {
  const { id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd } = props;
  const c = useContext(CanvasCtx);
  const [path, lx, ly] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, curvature: 0.5 });
  const target = c.graph.nodes[props.target] ? c.numbers[props.target] : undefined;
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} className="je je-jump" />
      <EdgeLabelRenderer>
        <div className="je-mid nodrag nopan" style={{ transform: `translate(-50%, -50%) translate(${lx}px, ${ly}px)`, pointerEvents: "none" }}>
          <span className="je-label je-jump">Jump to {target ?? "?"}</span>
        </div>
      </EdgeLabelRenderer>
    </>
  );
}

export const nodeTypes = { pulse: PulseNode };
export const edgeTypes = { add: AddEdge, jump: JumpEdge };

/** The trigger's line on the Start card. */
export function triggerText(g: JourneyGraph, segments: Record<number, string>, forms: Record<number, string> = {}): string {
  const t = g.trigger;
  if (t.type === "form_submitted") return `Contact submits ${t.form_id ? `‘${forms[t.form_id] ?? `form ${t.form_id}`}’` : "any form"}`;
  return t.type === "segment_entered"
    ? `Contact enters ${t.segment_id ? `‘${segments[t.segment_id] ?? `segment ${t.segment_id}`}’` : "a segment"}`
    : `Contact does ${t.event ? `‘${t.event}’` : "an event"}${t.where?.length ? ` where ${describeWhere(t.where)}` : ""}`;
}

