// Campaign reports over time (batch O5): a journey's or one-time campaign's activity by day or
// hour — who entered and finished, the trigger segment's joins and leaves, message outcomes — for
// the whole campaign or one step (its messages and its Yes / No / path outputs); and a table of
// every decision and condition with its Yes and No counts.
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, type JourneyGraph, type StepStats } from "../api";
import { ContactCount } from "../ContactCount";
import { daysAgo, rangeProblem, RangePicker, ymd, type Range } from "../insights";
import { groupOf, pathName, shapeOf, stepLabel } from "../journeyGraph";
import { pct, ReportPanel } from "../reports";
import { SeriesChart, type SeriesLine } from "../SeriesChart";
import type { BucketCount } from "../templateReport";
import { ErrorNote } from "../ui";
import "./template-report.css";

const ZONE = Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Kolkata";

export interface TimelineResponse {
  from: string; to: string; tz: string; bucket: "day" | "hour";
  timeline: { messages: BucketCount[]; lifecycle?: BucketCount[]; segment?: BucketCount[] };
  steps?: Record<string, BucketCount[]>;
}

const LINE: Record<string, SeriesLine> = {
  entered: { key: "entered", label: "Entered", color: "#3a5ccc" },
  completed: { key: "completed", label: "Finished", color: "#1d9a6c" },
  joined: { key: "joined", label: "Joined the segment", color: "#0f9bd7" },
  left: { key: "left", label: "Left the segment", color: "#8a8f99" },
  sent: { key: "sent", label: "Sent", color: "#2b2f8f" },
  delivered: { key: "delivered", label: "Delivered", color: "#4aa84a" },
  failed: { key: "failed", label: "Failed", color: "#d64545" },
  bounced: { key: "bounced", label: "Bounced", color: "#e8890c" },
  opened: { key: "opened", label: "Opened", color: "#9b3fb5" },
  clicked: { key: "clicked", label: "Clicked", color: "#c2185b" },
  read: { key: "read", label: "Read", color: "#00897b" },
};
const PATH_COLORS = ["#3a5ccc", "#e8890c", "#1d9a6c", "#9b3fb5", "#d64545", "#0f9bd7"];

/** stepLines names a step's outputs: Reached, Yes, No, or its split paths. */
export function stepLines(outputs: string[], node?: JourneyGraph["nodes"][string]): SeriesLine[] {
  return outputs.map((o, i) => {
    if (o === "entered") return { key: "s:entered", label: "Reached the step", color: "#2b2f8f" };
    if (o === "yes") return { key: "s:yes", label: "Yes", color: "#1d9a6c" };
    if (o === "no") return { key: "s:no", label: "No", color: "#d64545" };
    const m = /^v(\d+)$/.exec(o);
    const label = m && node ? `Path ${pathName(node, Number(m[1]))}` : o;
    return { key: `s:${o}`, label, color: PATH_COLORS[i % PATH_COLORS.length] };
  });
}

function rangeDays(r: Range) {
  return r.preset === "custom" ? { from: r.from, to: r.to } : { from: ymd(daysAgo(r.preset - 1)), to: ymd(new Date()) };
}

export function CampaignTimeline({ kind, id, graph, numbers, step, onStep }: {
  kind: "journey" | "broadcast"; id: number; graph?: JourneyGraph; numbers?: Record<string, number>;
  step?: string; onStep?: (s: string) => void;
}) {
  const [range, setRange] = useState<Range>({ preset: 30, from: ymd(daysAgo(30)), to: ymd(new Date()) });
  const [hourly, setHourly] = useState(false);
  const days = rangeDays(range);
  const span = Math.round((new Date(days.to).getTime() - new Date(days.from).getTime()) / 86_400_000) + 1;
  const byHour = hourly && span <= 14;
  const problem = rangeProblem(range) ?? (kind === "journey" && span > 120 ? "Pick 120 days or fewer." : null);
  const q = useQuery({
    queryKey: ["timeline", kind, id, days.from, days.to, byHour, step ?? ""],
    queryFn: () => api.get<TimelineResponse>(`/v1/${kind}s/${id}/timeline?from=${days.from}&to=${days.to}&bucket=${byHour ? "hour" : "day"}&tz=${encodeURIComponent(ZONE)}${step ? `&step=${encodeURIComponent(step)}` : ""}`),
    enabled: !problem, refetchInterval: 30_000,
  });
  const d = q.data;
  const node = step && graph ? graph.nodes[step] : undefined;
  let series: BucketCount[] = [];
  let lines: SeriesLine[] = [];
  if (d) {
    const msgKinds = [...new Set(d.timeline.messages.map((b) => b.kind))];
    if (step) {
      const out = d.steps?.[step] ?? [];
      const outputs = [...new Set(out.map((b) => b.kind))].sort((a, b) => (a === "entered" ? -1 : b === "entered" ? 1 : a.localeCompare(b)));
      series = [...out.map((b) => ({ ...b, kind: `s:${b.kind}` })), ...d.timeline.messages];
      lines = [...stepLines(outputs, node), ...msgKinds.map((k) => LINE[k]).filter(Boolean)];
    } else {
      series = [...(d.timeline.lifecycle ?? []), ...(d.timeline.segment ?? []), ...d.timeline.messages];
      const kinds = new Set(series.map((b) => b.kind));
      lines = Object.values(LINE).filter((l) => kinds.has(l.key) || (kind === "journey" && (l.key === "entered" || l.key === "completed")) || l.key === "sent");
    }
  }
  const stepIds = graph ? Object.keys(graph.nodes).sort((a, b) => (numbers?.[a] ?? 0) - (numbers?.[b] ?? 0)) : [];
  return (
    <ReportPanel title="Over time" aside={<span className="xs muted">Times in {ZONE}</span>} body>
      <div className="trep-controls">
        <RangePicker range={range} onChange={setRange} label="Period" />
        <div>
          <p className="small muted" style={{ margin: "0 0 6px" }}>Chart</p>
          <div className="funnel-range" role="group" aria-label="Chart buckets">
            <button type="button" aria-pressed={!byHour} onClick={() => setHourly(false)}>Day by day</button>
            <button type="button" aria-pressed={byHour} disabled={span > 14} title={span > 14 ? "Pick 14 days or fewer" : undefined} onClick={() => setHourly(true)}>Hour by hour</button>
          </div>
        </div>
        {graph && onStep && (
          <label className="field" style={{ minWidth: 220 }}>
            <span className="small muted">Show</span>
            <select className="select" value={step ?? ""} onChange={(e) => onStep(e.target.value)}>
              <option value="">The whole campaign</option>
              {stepIds.map((sid) => <option key={sid} value={sid}>{numbers?.[sid] ? `${numbers[sid]}. ` : ""}{stepLabel(graph, graph.nodes[sid])}</option>)}
            </select>
          </label>
        )}
      </div>
      {problem && <div className="alert error">{problem}</div>}
      <ErrorNote error={q.error} />
      {d && <SeriesChart key={`${step ?? ""}:${lines.map((l) => l.key).join()}`} series={series} from={d.from} to={d.to} hourly={d.bucket === "hour"} lines={lines}
        startOff={step ? [] : ["left", "read"]} label={step ? "This step over time" : "The campaign over time"} />}
      {d && kind === "journey" && !step && (d.timeline.segment?.length ?? 0) === 0 && graph?.trigger.type === "segment_entered" && (
        <p className="xs muted">Segment joins and leaves are recorded from batch O5 on.</p>
      )}
    </ReportPanel>
  );
}

/** DecisionTable: every decision, condition and split with how many reached it and went each way. */
export function DecisionTable({ graph, numbers, steps, onPick }: {
  graph: JourneyGraph; numbers: Record<string, number>; steps: Record<string, StepStats>; onPick: (step: string) => void;
}) {
  const rows = Object.entries(graph.nodes)
    .filter(([, n]) => ["decision", "condition", "split"].includes(shapeOf(n.type)) || groupOf(n.type) === "decision")
    .sort(([a], [b]) => (numbers[a] ?? 0) - (numbers[b] ?? 0));
  if (rows.length === 0) return null;
  return (
    <ReportPanel title="Decisions and conditions" aside={<span className="xs muted">Click a row to see it over time</span>}>
      <div className="table-scroll">
        <table className="table">
          <thead><tr><th>Step</th><th className="num">Reached</th><th className="num">Yes</th><th className="num">No</th><th>Paths</th></tr></thead>
          <tbody>{rows.map(([sid, n]) => {
            const s = steps[sid] ?? {};
            const reached = s.entered ?? ((s.yes ?? 0) + (s.no ?? 0));
            const paths = Object.entries(s).filter(([k]) => /^v\d+$/.test(k));
            return (
              <tr key={sid} className="clickable" onClick={() => onPick(sid)}>
                <td><span className="faint xs">{numbers[sid]}.</span> {stepLabel(graph, n)}</td>
                <td className="num">{reached ? <ContactCount value={reached} live filters={null} /> : <span className="faint">0</span>}</td>
                <td className="num">{s.yes != null ? <span className="report-rate"><span>{pct(s.yes, reached)}</span><small>{s.yes.toLocaleString("en-IN")}</small></span> : <span className="faint">–</span>}</td>
                <td className="num">{s.no != null ? <span className="report-rate"><span>{pct(s.no, reached)}</span><small>{s.no.toLocaleString("en-IN")}</small></span> : <span className="faint">–</span>}</td>
                <td className="small">{paths.length ? paths.map(([k, v]) => `${pathName(n, Number(k.slice(1)))}: ${(v ?? 0).toLocaleString("en-IN")}`).join(" · ") : <span className="faint">–</span>}</td>
              </tr>
            );
          })}</tbody>
        </table>
      </div>
    </ReportPanel>
  );
}
