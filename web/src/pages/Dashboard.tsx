import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, CHANNEL_NAMES, type Channel, type Overview } from "../api";
import { PulseTrace } from "../PulseTrace";
import { ErrorNote, LiveNumber, PageHead } from "../ui";
import { ContactCount } from "../ContactCount";

const MESSAGE_LABELS: Record<string, string> = { sent: "Sent", delivered: "Delivered", opened: "Opened", clicked: "Clicked", failed: "Failed", suppressed: "Opted out", fallback: "To SMS" };

export default function Dashboard() {
  const q = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.get<Overview>("/v1/stats/overview?days=14"),
    refetchInterval: 5000,
  });
  const o = q.data;
  const days = o?.activity?.events_per_day ?? [];
  const todayKey = new Date().toISOString().slice(0, 10);
  const today = days.find((d) => d.day === todayKey)?.count ?? 0;
  const minutes = lastHour(o?.activity?.events_per_minute ?? []);
  const thisMinute = minutes.at(-1) ?? 0;
  const lastHourTotal = minutes.reduce((s, n) => s + n, 0);
  const top = o?.activity?.top_events ?? [];
  const maxTop = Math.max(1, ...top.map((t) => t.count));

  // Messages grouped by channel: { email: { sent: 3, failed: 1 }, ... }
  const byChannel: Record<string, Record<string, number>> = {};
  for (const m of o?.activity?.messages ?? []) {
    const [ch, outcome] = m.name.split("_");
    (byChannel[ch] ??= {})[outcome] = m.count;
  }

  return (
    <div className="page">
      <PageHead title="Overview" intro="What your contacts are doing right now, and what Pulse sent in response." />
      <ErrorNote error={q.error} />

      <section className="pulse-panel" aria-label="Event volume">
        <div>
          <div className="pulse-live">Live</div>
          <div className="pulse-figure"><LiveNumber value={thisMinute} /></div>
          <div className="pulse-label">events this minute</div>
          <div className="pulse-label">{lastHourTotal.toLocaleString("en-IN")} in the last hour, {today.toLocaleString("en-IN")} today</div>
        </div>
        <div>
          <PulseTrace values={minutes} />
          <div className="pulse-axis">
            <span>60 min ago</span>
            <span>Now</span>
          </div>
        </div>
      </section>

      <div className="figures">
        <div><strong><ContactCount live value={o?.counts.contacts ?? 0} filters={[]} /></strong><span>contacts</span></div>
        <div><strong><LiveNumber value={o?.counts.segments ?? 0} /></strong><span>segments</span></div>
        <div><strong><LiveNumber value={o?.counts.active_journeys ?? 0} /></strong><span title="Triggered and automation campaigns that are live">campaigns running</span></div>
        <div><strong><LiveNumber value={o?.counts.templates ?? 0} /></strong><span>templates</span></div>
      </div>

      <div className="dash-cols section">
        <section className="panel">
          <div className="panel-head"><h3>Most frequent events</h3><span className="xs muted">14 days</span></div>
          <div className="panel-body">
            {top.length === 0 ? (
              <p className="muted small">No events yet. Send one with your server API key to <code>POST /v1/events</code>, and it shows up here within seconds.</p>
            ) : (
              <div className="bars">
                {top.map((t) => (
                  <div className="bar-row" key={t.name}>
                    <span>{t.name}</span>
                    <div className="bar-track"><div className="bar-fill" style={{ width: `${(t.count / maxTop) * 100}%` }} /></div>
                    <span className="num"><LiveNumber value={t.count} /></span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </section>

        <section className="panel">
          <div className="panel-head"><h3>Messages <span className="xs muted">14 days</span></h3><Link className="small" to="/settings/test-inbox">Test inbox</Link></div>
          {Object.keys(byChannel).length === 0 ? (
            <div className="panel-body"><p className="muted small">Nothing sent yet. Messages go out from <Link to="/campaigns">campaigns</Link> or a test send on a template.</p></div>
          ) : (
            <div className="table-scroll"><table className="table">
              <thead><tr><th>Channel</th>{Object.keys(MESSAGE_LABELS).map((k) => <th key={k} className="num">{MESSAGE_LABELS[k]}</th>)}</tr></thead>
              <tbody>
                {Object.entries(byChannel).map(([ch, counts]) => (
                  <tr key={ch}>
                    <td><span className={`chan chan-${ch}`}>{CHANNEL_NAMES[ch as Channel] ?? ch}</span></td>
                    {Object.keys(MESSAGE_LABELS).map((k) => <td key={k} className="num">{counts[k] ? <LiveNumber value={counts[k]} /> : <span className="faint">0</span>}</td>)}
                  </tr>
                ))}
              </tbody>
            </table></div>
          )}
        </section>
      </div>
    </div>
  );
}

// lastHour turns sparse per-minute counts into 60 values, oldest first, with quiet minutes as 0.
function lastHour(rows: { day: string; count: number }[]): number[] {
  const byMinute = new Map(rows.map((r) => [r.day, r.count]));
  const now = Date.now();
  return Array.from({ length: 60 }, (_, i) => {
    const key = new Date(now - (59 - i) * 60_000).toISOString().slice(0, 16);
    return byMinute.get(key) ?? 0;
  });
}
