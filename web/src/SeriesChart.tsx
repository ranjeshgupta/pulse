// A line chart of counts by day or hour (template and campaign reports): one line per kind, a
// legend that turns lines on and off, and a hover readout. Buckets with no data count as zero.
import { useState } from "react";
import { buckets, type BucketCount } from "./templateReport";

export type SeriesLine = { key: string; label: string; color: string };

export function SeriesChart({ series, from, to, hourly, lines, startOff = [], label = "Counts over time" }: {
  series: BucketCount[]; from: string; to: string; hourly: boolean; lines: SeriesLine[]; startOff?: string[]; label?: string;
}) {
  const [off, setOff] = useState<Set<string>>(() => new Set(startOff));
  const [hover, setHover] = useState<number | null>(null);
  const rows = buckets(series, from, to, hourly);
  const shown = lines.filter((l) => !off.has(l.key));
  const max = Math.max(1, ...rows.flatMap((r) => shown.map((l) => r.values[l.key] ?? 0)));
  const W = 900, H = 220, P = 28;
  const x = (i: number) => P + (rows.length <= 1 ? 0 : (i / (rows.length - 1)) * (W - 2 * P));
  const y = (n: number) => H - P - (n / max) * (H - 2 * P);
  const tick = (t: string) => hourly ? t.slice(5) : new Date(`${t}T00:00:00Z`).toLocaleDateString("en-IN", { day: "numeric", month: "short", timeZone: "UTC" });
  const every = Math.max(1, Math.ceil(rows.length / 8));
  return (
    <div className="trep-chart">
      <div className="trep-legend" role="group" aria-label="Lines">
        {lines.map((l) => (
          <button key={l.key} type="button" aria-pressed={!off.has(l.key)} onClick={() => setOff((s) => { const n = new Set(s); if (n.has(l.key)) n.delete(l.key); else n.add(l.key); return n; })}>
            <i style={{ background: l.color }} />{l.label}
          </button>
        ))}
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label} onMouseLeave={() => setHover(null)}
        onMouseMove={(e) => {
          const box = (e.currentTarget as SVGSVGElement).getBoundingClientRect();
          const px = ((e.clientX - box.left) / box.width) * W;
          setHover(Math.max(0, Math.min(rows.length - 1, Math.round(((px - P) / (W - 2 * P)) * (rows.length - 1)))));
        }}>
        {[0, 0.5, 1].map((f) => <line key={f} x1={P} x2={W - P} y1={y(max * f)} y2={y(max * f)} className="trep-grid-line" />)}
        <text x={4} y={y(max) + 4} className="trep-axis">{max.toLocaleString("en-IN")}</text>
        {rows.map((r, i) => i % every === 0 && <text key={r.t} x={x(i)} y={H - 6} textAnchor="middle" className="trep-axis">{tick(r.t)}</text>)}
        {/* Earlier lines are drawn wider, so lines that coincide (7 entered, 7 sent) still show each colour. */}
        {shown.map((l, i) => (
          <polyline key={l.key} fill="none" stroke={l.color} strokeWidth={2 + (shown.length - 1 - i) * 2} strokeLinejoin="round" strokeLinecap="round"
            points={rows.map((r, j) => `${x(j)},${y(r.values[l.key] ?? 0)}`).join(" ")} />
        ))}
        {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={P / 2} y2={H - P} className="trep-hover" />}
      </svg>
      {hover !== null && rows[hover] && (
        <div className="trep-tip small">
          <strong>{hourly ? rows[hover].t : tick(rows[hover].t)}</strong>
          {shown.map((l) => <span key={l.key}><i style={{ background: l.color }} />{l.label}: {(rows[hover].values[l.key] ?? 0).toLocaleString("en-IN")}</span>)}
        </div>
      )}
    </div>
  );
}
