// Analyze → Users: daily, weekly and monthly active users, stickiness, new vs returning, and who is
// active by platform and UTM source. Charts are plain SVG/HTML, as elsewhere in the app.
import { useQuery } from "@tanstack/react-query";
import { useState, type MouseEvent } from "react";
import { api, type UsersDashboard as UsersAnswer, type UsersDay } from "../api";
import { toCsv, csvFileName, downloadCsv } from "../csv";
import { count, defaultRange, pct, RangePicker, rangeProblem, useDebounced, type Range } from "../insights";
import { ErrorNote, Field, PageHead } from "../ui";
import {
  breakdown, compact, retryUnlessLimited, updatedAt, labelIndexes, linePath, linesMax, longDay, newReturningMax, PLATFORMS, platformLabel, rangeDays, shortDay,
  sourceLabel, sourceOptions, ticks, usersCsvName, usersCsvRows, usersPath, type BreakdownRow, type Platform,
} from "../usersDash";
import "./dashboards.css";

type Line = "dau" | "wau" | "mau";
const LINES: { key: Line; label: string; title: string }[] = [
  { key: "dau", label: "DAU", title: "Daily active users" },
  { key: "wau", label: "WAU", title: "Weekly active users (the 7 days ending that day)" },
  { key: "mau", label: "MAU", title: "Monthly active users (the 30 days ending that day)" },
];
/** ALL_SOURCES is the source filter's "All" (no filter); "" is a real value (no UTM source). */
const ALL_SOURCES = "\u0000all";

export function UsersDashboard() {
  const [range, setRange] = useState<Range>(() => defaultRange(90));
  const [platform, setPlatform] = useState<Platform>("");
  const [source, setSource] = useState<string | null>(null);

  const rangeIssue = rangeProblem(range);
  const path = rangeIssue ? "" : usersPath(rangeDays(range), platform, source);
  const debounced = useDebounced(path);
  const q = useQuery({
    queryKey: ["analytics-users", debounced],
    queryFn: () => api.get<UsersAnswer>(debounced),
    enabled: debounced !== "",
    placeholderData: (prev) => prev,
    retry: retryUnlessLimited,
  });
  const stale = debounced !== path || q.isFetching;
  const d = q.data;

  const exportCsv = () => {
    if (!d) return;
    downloadCsv(csvFileName(usersCsvName(d.from, d.to, platform, source)), toCsv(usersCsvRows(d.report.days)));
  };

  return (
    <div className="page">
      <PageHead
        title="Users"
        intro="How many people use your site and apps each day, week and month, how many come back, and where they come from."
        actions={<button type="button" className="btn" onClick={exportCsv} disabled={!d || d.report.days.length === 0}>Export CSV</button>}
      />

      <section className="panel">
        <div className="panel-body dash-controls">
          <div>
            <RangePicker range={range} onChange={setRange} label="Active in" />
            {rangeIssue && <p className="xs dash-problem">{rangeIssue}</p>}
          </div>
          <div>
            <p className="small muted dash-label" id="users-platform-label">Platform</p>
            <div className="seg-toggle sm dash-wrap" role="group" aria-labelledby="users-platform-label">
              {PLATFORMS.map((p) => (
                <button key={p.value} type="button" aria-pressed={platform === p.value} onClick={() => setPlatform(p.value)}>{p.label}</button>
              ))}
            </div>
          </div>
          <div className="dash-select">
            <Field label="UTM source">
              <select className="select" value={source ?? ALL_SOURCES} onChange={(e) => setSource(e.target.value === ALL_SOURCES ? null : e.target.value)}>
                <option value={ALL_SOURCES}>All sources</option>
                {sourceOptions(d?.report.by_source ?? [], source).map((k) => <option key={k} value={k}>{sourceLabel(k)}</option>)}
              </select>
            </Field>
          </div>
        </div>
      </section>

      <ErrorNote error={q.error && !stale ? q.error : null} />

      {!d ? (
        <p className="muted small dash-wait">{path ? (q.error ? "" : "Counting active users…") : "Fix the dates above to see users."}</p>
      ) : (
        <div className={`dash-body${stale ? " stale" : ""}`} aria-busy={stale}>
          <Report d={d} filtered={platform !== "" || source !== null} />
        </div>
      )}
    </div>
  );
}

function Report({ d, filtered }: { d: UsersAnswer; filtered: boolean }) {
  const r = d.report;
  return (
    <>
      <div className="figures dash-figures">
        <div title="Active on the last day of the range"><strong>{count(r.dau)}</strong><span>DAU</span></div>
        <div title="Active in the 7 days ending on the last day"><strong>{count(r.wau)}</strong><span>WAU</span></div>
        <div title="Active in the 30 days ending on the last day"><strong>{count(r.mau)}</strong><span>MAU</span></div>
        <div title="Average DAU over the last 30 days ÷ MAU"><strong>{pct(r.stickiness)}</strong><span>stickiness</span></div>
        <div><strong>{count(r.active)}</strong><span>active in range</span></div>
        {!filtered && <div title="First active in the range"><strong>{count(r.new)}</strong><span>new</span></div>}
        {!filtered && <div title="Active in the range and seen before it"><strong>{count(r.returning)}</strong><span>returning</span></div>}
      </div>
      <p className="xs faint dash-period">
        {longDay(d.from)} to {longDay(d.to)} (UTC days) · Updated {updatedAt(d.computed_at)}
      </p>
      {filtered && <p className="small dash-note">New vs returning isn't split by platform or source.</p>}
      <p className="xs muted dash-note">
        Active means the person did something themselves: viewed a page or screen, bought, filled in a form, or an event your server sent about them.
        Messages Pulse sent, Pulse's own bookkeeping events and profile-only (identify) updates don't count.
      </p>

      {r.days.length === 0 ? (
        <section className="panel section"><div className="empty"><h3>No activity in this period</h3><p>Try a longer date range or remove the filters.</p></div></section>
      ) : (
        <>
          <TrendPanel days={r.days} />
          {!filtered && <NewReturningPanel days={r.days} />}
        </>
      )}

      <div className="dash-grid section">
        <BreakdownPanel title="By platform" first="Platform" rows={breakdown(r.by_platform, r.active, platformLabel)} />
        <BreakdownPanel title="By UTM source" first="Source" rows={breakdown(r.by_source, r.active, sourceLabel)} />
      </div>
      <p className="xs muted dash-note">
        Counts people active in the range. Someone who used several platforms, or came from several sources, counts once in each, so shares can add up to more than 100%.
      </p>

      <DailyTable days={r.days} />
    </>
  );
}

// ---- charts ----

const W = 1000;
const H = 220;

function TrendPanel({ days }: { days: UsersDay[] }) {
  const [shown, setShown] = useState<Line[]>(["dau", "wau", "mau"]);
  const [hover, setHover] = useState<number | null>(null);
  const lines = LINES.filter((l) => shown.includes(l.key));
  const max = linesMax(days, lines.map((l) => l.key));
  const toggle = (k: Line) => setShown(shown.includes(k) ? shown.filter((x) => x !== k) : [...shown, k]);
  const n = days.length;
  const at = hover !== null ? days[hover] : null;
  const last = days[n - 1];

  const onMove = (e: MouseEvent<HTMLDivElement>) => {
    const box = e.currentTarget.getBoundingClientRect();
    const f = (e.clientX - box.left) / box.width;
    setHover(n === 1 ? 0 : Math.max(0, Math.min(n - 1, Math.round(f * (n - 1)))));
  };

  return (
    <section className="panel section">
      <div className="panel-head dash-head">
        <h3>Active users</h3>
        <div className="dash-legend" role="group" aria-label="Lines to show">
          {LINES.map((l) => (
            <button key={l.key} type="button" aria-pressed={shown.includes(l.key)} title={l.title} onClick={() => toggle(l.key)}>
              <i className={`dash-swatch line-${l.key}`} aria-hidden="true" />{l.label}
            </button>
          ))}
        </div>
      </div>
      <div className="panel-body">
        {lines.length === 0 ? <p className="small muted" style={{ margin: 0 }}>Pick at least one line to show.</p> : (
          <div className="dash-chart">
            <YAxis max={max} />
            <div
              className="dash-plot"
              onMouseMove={onMove}
              onMouseLeave={() => setHover(null)}
              role="img"
              aria-label={`Active users by day from ${longDay(days[0].day)} to ${longDay(last.day)}. On the last day: ${lines.map((l) => `${l.label} ${count(last[l.key])}`).join(", ")}.`}
            >
              <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true">
                {ticks(max).map((t) => <line key={t} className="dash-grid-line" x1={0} x2={W} y1={H - (t / max) * H} y2={H - (t / max) * H} />)}
                {lines.map((l) => (
                  <path key={l.key} className={`dash-line line-${l.key}`} d={linePath(days.map((x) => x[l.key]), max, W, H)} />
                ))}
                {n === 1 && lines.map((l) => <circle key={l.key} className={`dash-dot line-${l.key}`} cx={W / 2} cy={H - (days[0][l.key] / max) * H} r={4} />)}
                {hover !== null && <line className="dash-cursor" x1={n === 1 ? W / 2 : (hover / (n - 1)) * W} x2={n === 1 ? W / 2 : (hover / (n - 1)) * W} y1={0} y2={H} />}
              </svg>
              {at && hover !== null && (
                <div className={`dash-tip${hover > n / 2 ? " left" : ""}`} style={{ left: `${n === 1 ? 50 : (hover / (n - 1)) * 100}%` }}>
                  <strong>{longDay(at.day)}</strong>
                  {lines.map((l) => <span key={l.key}><i className={`dash-swatch line-${l.key}`} />{l.label} {count(at[l.key])}</span>)}
                  <span className="muted">Stickiness {pct(at.stickiness)}</span>
                </div>
              )}
            </div>
            <XAxis days={days} />
          </div>
        )}
      </div>
    </section>
  );
}

function NewReturningPanel({ days }: { days: UsersDay[] }) {
  const max = newReturningMax(days);
  const n = days.length;
  const totNew = days.reduce((s, d) => s + d.new, 0);
  return (
    <section className="panel section">
      <div className="panel-head dash-head">
        <h3>New vs returning, by day</h3>
        <div className="dash-key xs">
          <span><i className="dash-swatch bar-new" aria-hidden="true" />New</span>
          <span><i className="dash-swatch bar-returning" aria-hidden="true" />Returning</span>
        </div>
      </div>
      <div className="panel-body">
        <div className="dash-chart">
          <YAxis max={max} />
          <div
            className="dash-plot dash-barcols"
            style={{ gap: n > 120 ? 0 : 2 }}
            role="img"
            aria-label={`New and returning active users per day; ${count(totNew)} new-user days in total.`}
          >
            {days.map((d) => (
              <div key={d.day} className="dash-col" title={`${longDay(d.day)}: ${count(d.new)} new, ${count(d.returning)} returning`}>
                <div className="bar-returning" style={{ height: `${(d.returning / max) * 100}%` }} />
                <div className="bar-new" style={{ height: `${(d.new / max) * 100}%` }} />
              </div>
            ))}
          </div>
          <XAxis days={days} />
        </div>
        <p className="xs muted" style={{ margin: "10px 0 0" }}>New: active for the first time that day. Returning: active that day and seen on an earlier day.</p>
      </div>
    </section>
  );
}

function YAxis({ max }: { max: number }) {
  return (
    <div className="dash-y" aria-hidden="true">
      {ticks(max).map((t) => <span key={t} style={{ bottom: `${(t / max) * 100}%` }}>{compact(t)}</span>)}
    </div>
  );
}

function XAxis({ days }: { days: { day: string }[] }) {
  const n = days.length;
  return (
    <div className="dash-x" aria-hidden="true">
      {labelIndexes(n, 5).map((i) => (
        <span key={i} className={i === 0 ? "first" : i === n - 1 ? "last" : ""} style={{ left: `${n === 1 ? 50 : (i / (n - 1)) * 100}%` }}>{shortDay(days[i].day)}</span>
      ))}
    </div>
  );
}

// ---- tables ----

function BreakdownPanel({ title, first, rows }: { title: string; first: string; rows: BreakdownRow[] }) {
  return (
    <section className="panel">
      <div className="panel-head"><h3>{title}</h3></div>
      {rows.length === 0 ? (
        <div className="panel-body"><p className="small muted" style={{ margin: 0 }}>No one active in this period.</p></div>
      ) : (
        <div className="table-scroll">
          <table className="table dash-table">
            <caption className="dash-sr">{title}: people active in the range</caption>
            <thead><tr>
              <th scope="col">{first}</th>
              <th scope="col" className="num">Active users</th>
              <th scope="col" className="dash-share-col">Share of active</th>
            </tr></thead>
            <tbody>{rows.map((r) => (
              <tr key={r.key}>
                <th scope="row" className={r.key === "" ? "muted" : ""}>{r.label}</th>
                <td className="num">{count(r.count)}</td>
                <td><ShareBar value={r.share} /></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function ShareBar({ value }: { value: number }) {
  return (
    <div className="dash-share">
      <span className="num">{pct(value)}</span>
      <div className="bar-track" aria-hidden="true"><div className="bar-fill" style={{ width: `${Math.min(1, value) * 100}%` }} /></div>
    </div>
  );
}

function DailyTable({ days }: { days: UsersDay[] }) {
  if (days.length === 0) return null;
  return (
    <details className="panel section dash-details">
      <summary>Daily numbers</summary>
      <div className="table-scroll dash-daily">
        <table className="table">
          <thead><tr>
            <th scope="col">Day</th><th scope="col" className="num">DAU</th><th scope="col" className="num">WAU</th><th scope="col" className="num">MAU</th>
            <th scope="col" className="num">Stickiness</th><th scope="col" className="num">New</th><th scope="col" className="num">Returning</th>
          </tr></thead>
          <tbody>{[...days].reverse().map((d) => (
            <tr key={d.day}>
              <th scope="row">{longDay(d.day)}</th>
              <td className="num">{count(d.dau)}</td><td className="num">{count(d.wau)}</td><td className="num">{count(d.mau)}</td>
              <td className="num">{pct(d.stickiness)}</td><td className="num">{count(d.new)}</td><td className="num">{count(d.returning)}</td>
            </tr>
          ))}</tbody>
        </table>
      </div>
    </details>
  );
}
