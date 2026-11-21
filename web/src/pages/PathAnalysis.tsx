// Path analysis (O6): what people did after an event (or before one), step by step — a flow chart
// and the most common paths. Pulse's own events (messages, journeys, tags…) are left out.
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { api } from "../api";
import { useDebounced } from "../insights";
import { layout, STOPPED, type PathsAnswer } from "../paths";
import { Empty, ErrorNote, Field, PageHead } from "../ui";
import "./paths.css";

const W = 1000, H = 420;

export function PathAnalysis() {
  const names = useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names") }).data?.names ?? [];
  const [mode, setMode] = useState<"after" | "before">("after");
  const [anchor, setAnchor] = useState("");
  const [steps, setSteps] = useState(4);
  const [days, setDays] = useState(30);
  const [exclude, setExclude] = useState<string[]>(["page_view", "session_start"]);
  const body = useDebounced(anchor ? JSON.stringify({ [mode === "after" ? "start" : "end"]: anchor, steps, days, exclude }) : "");
  const q = useQuery({
    queryKey: ["paths", body], queryFn: () => api.post<PathsAnswer>("/v1/analytics/paths", JSON.parse(body)), enabled: body !== "",
    placeholderData: (p) => p,
  });
  const d = q.data;
  const flow = useMemo(() => (d ? layout(d.links, d.steps, d.start || d.end, W, H, !!d.end) : null), [d]);
  const [hover, setHover] = useState<string | null>(null);
  const total = d?.paths.reduce((a, p) => a + p.people, 0) ?? 0;
  // Backwards paths are drawn right to left; either way the column at the right edge labels leftwards.
  const labelLeft = (col: number) => (d?.end ? col !== d.steps : col === d?.steps);
  return (
    <div className="page">
      <PageHead title="Paths" intro="See what people do after an event — or what they did before one — step by step. Pulse's own events (messages sent, journey steps, tags) are left out, and repeats in a row count once." />
      <section className="panel"><div className="panel-body paths-controls">
        <div>
          <p className="small muted" style={{ margin: "0 0 6px" }}>Show what people did</p>
          <div className="seg-toggle" role="group" aria-label="Direction">
            <button type="button" aria-pressed={mode === "after"} onClick={() => setMode("after")}>After</button>
            <button type="button" aria-pressed={mode === "before"} onClick={() => setMode("before")}>Before</button>
          </div>
        </div>
        <Field label="Event">
          <input className="input" list="paths-events" placeholder="e.g. signup" value={anchor} onChange={(e) => setAnchor(e.target.value.trim())} data-no-dirty />
          <datalist id="paths-events">{names.map((n) => <option key={n} value={n} />)}</datalist>
        </Field>
        <Field label="Steps"><select className="select" value={steps} onChange={(e) => setSteps(Number(e.target.value))} data-no-dirty>{[2, 3, 4, 5, 6].map((n) => <option key={n}>{n}</option>)}</select></Field>
        <Field label="In the last"><select className="select" value={days} onChange={(e) => setDays(Number(e.target.value))} data-no-dirty>{[7, 30, 90].map((n) => <option key={n} value={n}>{n} days</option>)}</select></Field>
        <Field label="Leave out">
          <div className="paths-chips">
            {exclude.filter((x) => x !== anchor).map((x) => <button key={x} type="button" className="badge" title="Put it back" onClick={() => setExclude(exclude.filter((y) => y !== x))}>{x} ×</button>)}
            <select className="select sm" value="" aria-label="Leave out an event" onChange={(e) => e.target.value && setExclude([...exclude, e.target.value])} data-no-dirty>
              <option value="">+ event</option>
              {names.filter((n) => !exclude.includes(n) && n !== anchor).map((n) => <option key={n}>{n}</option>)}
            </select>
          </div>
        </Field>
      </div></section>
      <ErrorNote error={q.error} />
      {!anchor && <div className="panel" style={{ marginTop: 14 }}><Empty title="Pick an event">For example what new sign-ups do next, or what people did before they bought.</Empty></div>}
      {d && flow && anchor && (
        d.paths.length === 0 ? <div className="panel" style={{ marginTop: 14 }}><Empty title={`No one did ${d.start || d.end} in the last ${d.days} days`} /></div> : (
          <>
            <section className={`panel paths-flow${q.isFetching ? " stale" : ""}`} aria-busy={q.isFetching}>
              <div className="panel-head"><h3>{d.people.toLocaleString("en-IN")} people did <code>{d.start || d.end}</code></h3><span className="xs muted">Hover a band to see it</span></div>
              <div className="panel-body">
                {/* The chart is for the eye: hover-only, so screen readers get the table below, which says the same. */}
                <svg viewBox={`-4 -4 ${W + 8} ${H + 8}`} role="img" aria-label="Flow of events step by step; the same paths are in the Most common paths table" className="paths-svg">
                  {flow.bands.map((b, i) => {
                    const k = `${b.from}>${b.to}:${i}`;
                    return (
                      <path key={k} aria-hidden="true" d={b.d} strokeWidth={b.width} className={`paths-band${b.to === STOPPED ? " stop" : ""}${hover && hover !== k ? " dim" : ""}`}
                        onMouseEnter={() => setHover(k)} onMouseLeave={() => setHover(null)}>
                        <title>{`${b.from} → ${b.to}: ${b.value.toLocaleString("en-IN")} people`}</title>
                      </path>
                    );
                  })}
                  {flow.nodes.map((n) => (
                    <g key={n.key}>
                      <rect x={n.x} y={n.y} width={12} height={n.h} rx={3} className={n.name === STOPPED ? "paths-node stop" : "paths-node"} />
                      {/* Labels sit inside the chart: beside the box, but left of the outermost column. */}
                      <text x={labelLeft(n.col) ? n.x - 6 : n.x + 18} y={n.y + n.h / 2} dy="0.35em" textAnchor={labelLeft(n.col) ? "end" : "start"} className="paths-label">
                        {n.name} · {n.value.toLocaleString("en-IN")}
                      </text>
                    </g>
                  ))}
                </svg>
              </div>
            </section>
            <section className="panel" style={{ marginTop: 14 }}>
              <div className="panel-head"><h3 id="paths-table-title">Most common paths</h3>{d.paths.length === 500 && <span className="xs muted">The 500 most common</span>}</div>
              <div className="table-scroll"><table className="table" aria-labelledby="paths-table-title">
                <thead><tr><th scope="col">Path</th><th scope="col" className="num">People</th><th scope="col" className="num">Share</th></tr></thead>
                <tbody>{d.paths.slice(0, 25).map((p, i) => (
                  <tr key={i}>
                    <td className="paths-steps">{p.steps.map((s, j) => <span key={j}>{j > 0 && <span className="faint"> → </span>}<code>{s}</code></span>)}</td>
                    <td className="num">{p.people.toLocaleString("en-IN")}</td>
                    <td className="num small">{total ? `${Math.round((p.people / total) * 100)}%` : ""}</td>
                  </tr>
                ))}</tbody>
              </table></div>
            </section>
          </>
        )
      )}
    </div>
  );
}
