// A template's Report tab (batch O4): how its messages did across every campaign that sent it —
// headline figures, a chart by day or hour, when people engage (weekday × hour), and splits by
// campaign, channel and provider, with failure reasons and top links. Every count opens its contacts.
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, CHANNEL_NAMES, type Channel, type Provider } from "../api";
import { ContactCount } from "../ContactCount";
import { templateFilters } from "../contactLinks";
import { daysAgo, rangeProblem, RangePicker, ymd, type Range } from "../insights";
import { COUNT_LABELS, CountsTable, ReportFigures, ReportPanel, TopLinks, type CountKey, type CountLink } from "../reports";
import {
  bestTime, buckets, heatLevel, hourLabel, SERIES_COLORS, seriesKinds, templateKeys, WEEKDAYS, type TemplateReportResponse,
} from "../templateReport";
import { Empty, ErrorNote } from "../ui";
import "./template-report.css";

const ZONE = Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Kolkata";

function rangeDays(r: Range): { from: string; to: string } {
  if (r.preset === "custom") return { from: r.from, to: r.to };
  return { from: ymd(daysAgo(r.preset - 1)), to: ymd(new Date()) };
}

export function TemplateReport({ id, channel }: { id: number; channel: Channel }) {
  const [range, setRange] = useState<Range>({ preset: 30, from: ymd(daysAgo(30)), to: ymd(new Date()) });
  const [hourly, setHourly] = useState(false);
  const days = rangeDays(range);
  const span = Math.round((new Date(days.to).getTime() - new Date(days.from).getTime()) / 86_400_000) + 1;
  const canHourly = span <= 14;
  const byHour = hourly && canHourly;
  const problem = rangeProblem(range);
  const q = useQuery({
    queryKey: ["template-report", id, days.from, days.to, byHour, ZONE],
    queryFn: () => api.get<TemplateReportResponse>(`/v1/templates/${id}/report?from=${days.from}&to=${days.to}&bucket=${byHour ? "hour" : "day"}&tz=${encodeURIComponent(ZONE)}`),
    enabled: !problem,
    refetchInterval: 30_000,
  });
  const camps = useQuery({ queryKey: ["campaigns", "names"], queryFn: () => api.get<{ campaigns: { kind: string; id: number; name: string }[] }>("/v1/campaigns"), staleTime: 60_000 });
  const provs = useQuery({ queryKey: ["providers"], queryFn: () => api.get<{ providers: Provider[] }>("/v1/channels/providers"), staleTime: 60_000 });
  const rep = q.data?.report;
  const keys = templateKeys(channel);
  const link: CountLink = (k) => templateFilters(id, outcomeOf(k), days);
  const names = useMemo(() => new Map((camps.data?.campaigns ?? []).map((c) => [`${c.kind}:${c.id}`, c.name])), [camps.data]);
  const provName = (pid: string) => pid === "0" ? "Unknown (sent before reports split by provider)" : provs.data?.providers.find((p) => String(p.id) === pid)?.name ?? `Provider ${pid}`;

  return (
    <div className="trep">
      <div className="trep-controls">
        <RangePicker range={range} onChange={setRange} label="Period" />
        <div>
          <p className="small muted" style={{ margin: "0 0 6px" }}>Chart</p>
          <div className="funnel-range" role="group" aria-label="Chart buckets">
            <button type="button" aria-pressed={!byHour} onClick={() => setHourly(false)}>Day by day</button>
            <button type="button" aria-pressed={byHour} disabled={!canHourly} title={canHourly ? undefined : "Pick 14 days or fewer"} onClick={() => setHourly(true)}>Hour by hour</button>
          </div>
        </div>
        <p className="xs muted trep-zone">Times in {ZONE}. Counts are people, updated every 30 seconds.</p>
      </div>
      {problem && <div className="alert error">{problem}</div>}
      <ErrorNote error={q.error} />
      {q.isLoading && <div className="panel"><div className="panel-body"><div className="skeleton" style={{ height: 120 }} /></div></div>}
      {rep && rep.totals.sent === 0 && rep.totals.failed === 0 ? (
        <div className="panel"><Empty title="Nothing sent in this period">Messages from this template — in one-time campaigns, journey steps, tests or API sends — show here once they go out.</Empty></div>
      ) : rep && (
        <>
          <ReportFigures totals={rep.totals} keys={keys} link={link} />
          <ReportPanel title="Over time" body>
            <SeriesChart data={q.data!} channel={channel} />
          </ReportPanel>
          <div className="grid-2 trep-grid">
            <ReportPanel title="When people engage" aside={<span className="xs muted">{channel === "email" ? "Opens and clicks" : "Reads and clicks"}</span>} body>
              <Heatmap heat={rep.heatmap} />
            </ReportPanel>
            <ReportPanel title="Why messages didn't arrive" body={rep.reasons.length === 0}>
              {rep.reasons.length === 0 ? <p className="small muted">No failures, bounces or complaints. 🎉</p> : (
                <table className="table">
                  <thead><tr><th>Reason</th><th>What</th><th className="num">People</th></tr></thead>
                  <tbody>{rep.reasons.map((r) => (
                    <tr key={r.kind + r.code}>
                      <td>{r.label}</td><td className="small muted">{COUNT_LABELS[r.kind as CountKey] ?? r.kind}</td>
                      <td className="num"><ContactCount value={r.contacts} live filters={templateFilters(id, r.kind, days, { reason: r.code || undefined })} /></td>
                    </tr>
                  ))}</tbody>
                </table>
              )}
            </ReportPanel>
          </div>
          <ReportPanel title="By campaign">
            <CountsTable lead={["Campaign"]} keys={keys} rows={rep.by_campaign
              .sort((a, b) => b.counts.sent - a.counts.sent)
              .map((c) => ({
                key: `${c.source}:${c.source_id}`,
                lead: [c.source === "other" ? <span className="muted">Tests and API sends</span>
                  : <Link to={c.source === "broadcast" ? `/campaigns/one-time/${c.source_id}` : `/journeys/${c.source_id}`}>{names.get(`${c.source}:${c.source_id}`) ?? `${c.source === "broadcast" ? "One-time" : "Journey"} #${c.source_id}`}</Link>],
                counts: c.counts,
                link: c.source === "other" ? undefined : (k: CountKey) => [["campaign", `${c.source}/${c.source_id}`], ["template", id], ["outcome", outcomeOf(k)], ["period", `${days.from}..${days.to}`]],
              }))} />
          </ReportPanel>
          <div className="grid-2 trep-grid">
            {Object.keys(rep.by_provider).length > 0 && (
              <ReportPanel title="By provider">
                <CountsTable lead={["Provider"]} keys={keys.filter((k) => k === "sent" || k === "delivered" || k === "failed" || k === "bounced")}
                  rows={Object.entries(rep.by_provider).sort(([, a], [, b]) => b.sent - a.sent).map(([pid, c]) => ({ key: pid, lead: [provName(pid)], counts: c }))} />
              </ReportPanel>
            )}
            {Object.keys(rep.by_channel).length > 1 && (
              <ReportPanel title="By channel" aside={<span className="xs muted">Includes SMS fallbacks</span>}>
                <CountsTable lead={["Channel"]} keys={["sent", "delivered", "failed"]}
                  rows={Object.entries(rep.by_channel).map(([ch, c]) => ({ key: ch, lead: [CHANNEL_NAMES[ch as Channel] ?? ch], counts: c,
                    link: (k: CountKey) => templateFilters(id, outcomeOf(k), days, { via: ch }) }))} />
              </ReportPanel>
            )}
          </div>
          {(channel === "email" || rep.links.length > 0) && <TopLinks links={rep.links} />}
        </>
      )}
    </div>
  );
}

/** outcomeOf: the contact-list outcome behind a figure (Failed counts every failure, skips included). */
function outcomeOf(k: CountKey): string {
  return k === "failed" ? "failed" : k;
}

/** SeriesChart draws one line per outcome, by day or hour; the legend turns lines on and off. */
function SeriesChart({ data, channel }: { data: TemplateReportResponse; channel: Channel }) {
  const kinds = seriesKinds(channel);
  const [off, setOff] = useState<Set<string>>(() => new Set(kinds.filter((k) => k === "complained" || k === "unsubscribed")));
  const [hover, setHover] = useState<number | null>(null);
  const rows = buckets(data.report.series, data.from, data.to, data.bucket === "hour");
  const shown = kinds.filter((k) => !off.has(k));
  const max = Math.max(1, ...rows.flatMap((r) => shown.map((k) => r.values[k] ?? 0)));
  const W = 900, H = 220, P = 28;
  const x = (i: number) => P + (rows.length <= 1 ? 0 : (i / (rows.length - 1)) * (W - 2 * P));
  const y = (n: number) => H - P - (n / max) * (H - 2 * P);
  const label = (t: string) => data.bucket === "hour" ? t.slice(5) : new Date(`${t}T00:00:00Z`).toLocaleDateString("en-IN", { day: "numeric", month: "short", timeZone: "UTC" });
  const every = Math.max(1, Math.ceil(rows.length / 8));
  return (
    <div className="trep-chart">
      <div className="trep-legend" role="group" aria-label="Lines">
        {kinds.map((k) => (
          <button key={k} type="button" aria-pressed={!off.has(k)} onClick={() => setOff((s) => { const n = new Set(s); if (n.has(k)) n.delete(k); else n.add(k); return n; })}>
            <i style={{ background: SERIES_COLORS[k] }} />{COUNT_LABELS[k as CountKey]}
          </button>
        ))}
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Outcomes over time" onMouseLeave={() => setHover(null)}
        onMouseMove={(e) => {
          const box = (e.currentTarget as SVGSVGElement).getBoundingClientRect();
          const px = ((e.clientX - box.left) / box.width) * W;
          setHover(Math.max(0, Math.min(rows.length - 1, Math.round(((px - P) / (W - 2 * P)) * (rows.length - 1)))));
        }}>
        {[0, 0.5, 1].map((f) => <line key={f} x1={P} x2={W - P} y1={y(max * f)} y2={y(max * f)} className="trep-grid-line" />)}
        <text x={4} y={y(max) + 4} className="trep-axis">{max.toLocaleString("en-IN")}</text>
        {rows.map((r, i) => i % every === 0 && <text key={r.t} x={x(i)} y={H - 6} textAnchor="middle" className="trep-axis">{label(r.t)}</text>)}
        {shown.map((k) => (
          <polyline key={k} fill="none" stroke={SERIES_COLORS[k]} strokeWidth={2} strokeLinejoin="round"
            points={rows.map((r, i) => `${x(i)},${y(r.values[k] ?? 0)}`).join(" ")} />
        ))}
        {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={P / 2} y2={H - P} className="trep-hover" />}
      </svg>
      {hover !== null && rows[hover] && (
        <div className="trep-tip small">
          <strong>{data.bucket === "hour" ? rows[hover].t : label(rows[hover].t)}</strong>
          {shown.map((k) => <span key={k}><i style={{ background: SERIES_COLORS[k] }} />{COUNT_LABELS[k as CountKey]}: {(rows[hover].values[k] ?? 0).toLocaleString("en-IN")}</span>)}
        </div>
      )}
    </div>
  );
}

function Heatmap({ heat }: { heat: number[][] }) {
  const max = Math.max(0, ...heat.flat());
  const best = bestTime(heat);
  if (!max) return <p className="small muted">No opens, reads or clicks yet.</p>;
  return (
    <>
      <div className="trep-heat" role="table" aria-label="Engagement by weekday and hour">
        <div role="row" className="trep-heat-row">
          <span />
          {Array.from({ length: 24 }, (_, h) => <span key={h} className="trep-heat-h" role="columnheader">{h % 3 === 0 ? hourLabel(h) : ""}</span>)}
        </div>
        {heat.map((row, d) => (
          <div role="row" key={d} className="trep-heat-row">
            <span role="rowheader" className="trep-heat-d">{WEEKDAYS[d]}</span>
            {row.map((n, h) => <span key={h} role="cell" className={`trep-cell l${heatLevel(n, max)}`} title={`${WEEKDAYS[d]} ${hourLabel(h)}: ${n.toLocaleString("en-IN")}`} />)}
          </div>
        ))}
      </div>
      {best && <p className="small" style={{ marginBottom: 0 }}>⏰ Busiest: <strong>{best.day}, {hourLabel(best.hour)}</strong>. Sending a little before that usually does best.</p>}
    </>
  );
}

