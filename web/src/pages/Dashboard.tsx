import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { api, can, CHANNEL_NAMES, type Channel, type Overview } from "../api";
import { useMe } from "../auth";
import { ContactCount } from "../ContactCount";
import { period } from "../contactLinks";
import { money } from "../insights";
import { change, keyRate, overviewSections, percent, REPEAT_WORDS, whenText, type BroadcastBrief, type OverviewSummary } from "../overview";
import { PulseTrace } from "../PulseTrace";
import { ErrorNote, LiveNumber, PageHead } from "../ui";
import "./overview.css";

const MESSAGE_LABELS: Record<string, string> = { sent: "Sent", delivered: "Delivered", opened: "Opened", clicked: "Clicked", failed: "Failed", suppressed: "Opted out", fallback: "To SMS" };

// The Overview is two requests: the live numbers (small, every 5 s) and the summary (built once a
// minute per workspace on the server, polled every minute while the tab is visible). Each part
// shows as soon as its own answer arrives.
export default function Dashboard() {
  const perms = useMe().data?.permissions;
  const reports = can(perms, "reports", "view");
  const q = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.get<Overview>("/v1/stats/overview?days=14"),
    refetchInterval: 5000,
    enabled: reports,
  });
  const sq = useQuery({
    queryKey: ["overview-summary"],
    queryFn: () => api.get<OverviewSummary>("/v1/stats/summary"),
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
    staleTime: 30_000,
    enabled: perms !== undefined,
  });
  const sections = overviewSections(perms);
  const sum = sq.data;

  return (
    <div className="page">
      <PageHead title="Overview" intro="What needs your attention, what your contacts are doing right now, and how your campaigns are going." />
      <Attention summary={sum} />
      {reports && <Live overview={q.data} error={q.error} />}

      {(sections.audience || sections.revenue) && (
        <div className="ov-cards section">
          {sections.audience && <Audience summary={sum} loading={sq.isPending} />}
          {sections.revenue && (sq.isPending || sum?.revenue) && <Revenue summary={sum} loading={sq.isPending} />}
        </div>
      )}
      {sections.campaigns && <Campaigns summary={sum} loading={sq.isPending} recent={sections.recent} />}
      {sq.error && !sum && <p className="muted small ov-note">The summary couldn't be loaded just now; it tries again in a minute.</p>}

      {reports && <Activity overview={q.data} />}
    </div>
  );
}

// ---- needs attention (only when something's wrong) ----

function Attention({ summary }: { summary?: OverviewSummary }) {
  const items = summary?.attention ?? [];
  if (items.length === 0) return null;
  return (
    <section className="ov-attention" aria-label="Needs attention">
      <h3>Needs attention</h3>
      <ul>
        {items.map((a, i) => (
          <li key={i} className={a.level === "problem" ? "problem" : "warning"}>
            <span>{a.text}</span>
            {a.link && <Link to={a.link} className="small">{a.link_text ?? "Open"}</Link>}
          </li>
        ))}
      </ul>
    </section>
  );
}

// ---- live (GET /v1/stats/overview) ----

function Live({ overview: o, error }: { overview?: Overview; error: unknown }) {
  const days = o?.activity?.events_per_day ?? [];
  const todayKey = new Date().toISOString().slice(0, 10);
  const today = days.find((d) => d.day === todayKey)?.count ?? 0;
  const minutes = lastHour(o?.activity?.events_per_minute ?? []);
  const thisMinute = minutes.at(-1) ?? 0;
  const lastHourTotal = minutes.reduce((s, n) => s + n, 0);
  return (
    <>
      <ErrorNote error={error} />
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
    </>
  );
}

// ---- audience and revenue ----

function Card({ title, note, children }: { title: string; note?: ReactNode; children: ReactNode }) {
  return (
    <section className="panel ov-card">
      <div className="panel-head"><h3>{title}</h3>{note && <span className="xs muted">{note}</span>}</div>
      <div className="panel-body">{children}</div>
    </section>
  );
}

function Stat({ value, label, before, sub }: { value: ReactNode; label: string; before?: [number, number]; sub?: ReactNode }) {
  const c = before ? change(before[0], before[1]) : null;
  return (
    <div className="ov-stat">
      <strong>{value}</strong>
      <span>{label}</span>
      {c && <span className={`ov-change ${c.dir}`} title={c.title}>{c.text}</span>}
      {sub && <span className="xs muted">{sub}</span>}
    </div>
  );
}

function Skeleton({ rows = 2 }: { rows?: number }) {
  return (
    <div className="ov-skel" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => <span key={i} />)}
    </div>
  );
}

const missing = (s: OverviewSummary | undefined, part: string) => s?.unavailable?.includes(part);
const RETRY = "Couldn't be read just now; it tries again shortly.";

function Audience({ summary: s, loading }: { summary?: OverviewSummary; loading: boolean }) {
  const a = s?.audience;
  return (
    <Card title="Audience" note="last 7 days, against the 7 before">
      {loading ? <Skeleton /> : (
        <div className="ov-stats">
          {a?.new ? (
            <Stat value={a.new.week.toLocaleString("en-IN")} label="contacts added" before={[a.new.week, a.new.prev_week]} sub={`${a.new.today.toLocaleString("en-IN")} today`} />
          ) : missing(s, "audience") ? <p className="muted small">Contacts added: {RETRY}</p> : null}
          {a?.unsubscribed && s && (
            <Stat
              value={<ContactCount value={a.unsubscribed.week} filters={[["event", "unsubscribed"], period(s.week_from, s.today)]} label="contacts who unsubscribed" />}
              label="unsubscribed"
              before={[a.unsubscribed.week, a.unsubscribed.prev]}
            />
          )}
          {missing(s, "unsubscribes") && <p className="muted small">Unsubscribes: {RETRY}</p>}
        </div>
      )}
    </Card>
  );
}

function Revenue({ summary: s, loading }: { summary?: OverviewSummary; loading: boolean }) {
  const r = s?.revenue;
  return (
    <Card title="Revenue" note={<Link to="/revenue">Revenue report</Link>}>
      {loading || !r ? <Skeleton /> : (
        <div className="ov-stats">
          <Stat value={money(r.week.revenue, r.currency)} label="revenue" before={[r.week.revenue, r.prev.revenue]} sub={<>from <code>{r.event}</code> events</>} />
          <Stat value={r.week.orders.toLocaleString("en-IN")} label="orders" before={[r.week.orders, r.prev.orders]} sub={`${r.week.buyers.toLocaleString("en-IN")} buyers`} />
        </div>
      )}
    </Card>
  );
}

// ---- one-time campaigns ----

function Campaigns({ summary: s, loading, recent }: { summary?: OverviewSummary; loading: boolean; recent: boolean }) {
  const c = s?.campaigns;
  return (
    <section className="panel section ov-campaigns" aria-label="One-time campaigns">
      <div className="panel-head">
        <h3>One-time campaigns</h3>
        <span className="small ov-head-links">
          <Link to="/campaigns?tab=one_time">All campaigns</Link>
          {recent && <Link to="/analytics/campaigns">Campaign performance</Link>}
        </span>
      </div>
      <div className={`ov-lists${recent ? " three" : ""}`}>
        <CampaignList title="Sending now" loading={loading} items={c?.sending} empty="Nothing is sending right now." show={(b) => <Sending b={b} />} />
        <CampaignList title="Coming up" loading={loading} items={c?.next} empty={<>Nothing scheduled. <Link to="/campaigns?new=1">Create a campaign</Link></>}
          show={(b) => <>{whenText(b.at)}{b.repeat && b.repeat !== "none" ? `, then ${REPEAT_WORDS[b.repeat] ?? b.repeat}` : ""}</>} />
        {recent && <CampaignList title="Recently sent" loading={loading} items={c?.recent} empty="Nothing sent in the last 30 days." show={(b) => <Result b={b} />} />}
      </div>
      {missing(s, "campaigns") && <p className="muted small ov-note">Campaigns: {RETRY}</p>}
    </section>
  );
}

function CampaignList({ title, loading, items, empty, show }: {
  title: string; loading: boolean; items?: BroadcastBrief[]; empty: ReactNode; show: (b: BroadcastBrief) => ReactNode;
}) {
  return (
    <div className="ov-list">
      <h4>{title}</h4>
      {loading ? <Skeleton rows={3} /> : !items?.length ? <p className="muted small">{empty}</p> : (
        <ul>
          {items.map((b) => (
            <li key={b.id}>
              <Link to={`/broadcasts/${b.id}`} className="ov-name">{b.name}</Link>
              <span className="ov-meta">
                <span className={`chan chan-${b.channel}`}>{CHANNEL_NAMES[b.channel as Channel] ?? b.channel}</span>
                <span>{show(b)}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Sending({ b }: { b: BroadcastBrief }) {
  if (b.status === "testing") return <>A/B test sent, waiting to pick the winner</>;
  return <>started {whenText(b.at)}{b.counts ? `, ${b.counts.sent.toLocaleString("en-IN")} sent so far` : ""}</>;
}

function Result({ b }: { b: BroadcastBrief }) {
  const r = keyRate(b.channel, b.counts);
  return (
    <>
      {whenText(b.at)}
      {b.counts && <>, {b.counts.sent.toLocaleString("en-IN")} sent</>}
      {r && <strong className="ov-rate" title={`Share of messages sent that were ${r.label}`}> · {percent(r.value)} {r.label}</strong>}
    </>
  );
}

// ---- events and messages of the last 14 days (GET /v1/stats/overview) ----

function Activity({ overview: o }: { overview?: Overview }) {
  const top = o?.activity?.top_events ?? [];
  const maxTop = Math.max(1, ...top.map((t) => t.count));
  // Messages grouped by channel: { email: { sent: 3, failed: 1 }, ... }
  const byChannel: Record<string, Record<string, number>> = {};
  for (const m of o?.activity?.messages ?? []) {
    const [ch, outcome] = m.name.split("_");
    (byChannel[ch] ??= {})[outcome] = m.count;
  }
  return (
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
