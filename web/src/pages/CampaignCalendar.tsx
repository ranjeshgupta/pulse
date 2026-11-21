// The marketing calendar (O6): every one-time and repeating send in a month, drafts included, and
// the triggered / automation campaigns live then; days with many sends are flagged.
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import { BUSY, monthGrid, sendsByDay, ymdLocal, type CalendarItem } from "../calendar";
import { ErrorNote, PageHead } from "../ui";
import "./calendar.css";

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
const DAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

export function CampaignCalendar() {
  const now = new Date();
  const [ym, setYm] = useState({ y: now.getFullYear(), m: now.getMonth() });
  const weeks = monthGrid(ym.y, ym.m);
  const from = ymdLocal(weeks[0][0]), to = ymdLocal(weeks[weeks.length - 1][6]);
  const q = useQuery({ queryKey: ["calendar", from, to], queryFn: () => api.get<{ items: CalendarItem[] }>(`/v1/campaigns/calendar?from=${from}&to=${to}`) });
  const items = q.data?.items ?? [];
  const byDay = sendsByDay(items);
  const live = items.filter((i) => i.kind === "journey");
  const move = (d: number) => setYm(({ y, m }) => { const t = new Date(y, m + d, 1); return { y: t.getFullYear(), m: t.getMonth() }; });
  const today = ymdLocal(now);
  return (
    <div className="page" style={{ maxWidth: "none" }}>
      <PageHead back={<Link className="back" to="/campaigns">Campaigns</Link>} title="Calendar"
        intro="Every one-time and repeating send, drafts included, and the triggered and automation campaigns running. Spot clashes before your audience does."
        actions={<div className="row">
          <button className="btn" onClick={() => move(-1)} aria-label="Previous month">←</button>
          <button className="btn" onClick={() => setYm({ y: now.getFullYear(), m: now.getMonth() })}>Today</button>
          <button className="btn" onClick={() => move(1)} aria-label="Next month">→</button>
        </div>} />
      <h2 className="cal-title">{MONTHS[ym.m]} {ym.y}</h2>
      <ErrorNote error={q.error} />
      {live.length > 0 && (
        <div className="cal-live">
          <span className="small muted">Running all month or part of it:</span>
          {live.map((j) => <Link key={j.id} to={`/journeys/${j.id}`} className={`cal-pill journey${j.status === "paused" ? " paused" : ""}`}>⚡ {j.name}{j.status === "paused" ? " (paused)" : ""}</Link>)}
        </div>
      )}
      <div className="cal-grid" role="grid" aria-label={`${MONTHS[ym.m]} ${ym.y}`}>
        {DAYS.map((d) => <div key={d} className="cal-head" role="columnheader">{d}</div>)}
        {weeks.flat().map((d) => {
          const k = ymdLocal(d);
          const sends = byDay.get(k) ?? [];
          const other = d.getMonth() !== ym.m;
          return (
            <div key={k} role="gridcell" className={`cal-day${other ? " other" : ""}${k === today ? " today" : ""}${sends.length >= BUSY ? " busy" : ""}`}>
              <div className="cal-date"><span>{d.getDate()}</span>{sends.length >= BUSY && <span className="cal-busy" title={`${sends.length} sends this day: check they don't reach the same people too often`}>🔥 {sends.length}</span>}</div>
              {sends.slice(0, 4).map(({ item, at }) => (
                <Link key={`${item.id}-${at.getTime()}`} to={`/broadcasts/${item.id}`} className={`cal-pill ch-${item.channel}${item.status === "draft" ? " draft" : ""}`}
                  title={`${item.name} · ${at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} · ${item.status}`}>
                  <span className="cal-time">{at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span> {item.name}
                </Link>
              ))}
              {sends.length > 4 && <span className="xs muted">+{sends.length - 4} more</span>}
            </div>
          );
        })}
      </div>
      <p className="xs muted">Dashed: drafts at their planned time. Colours: email, SMS, WhatsApp, RCS, push, app inbox. 🔥: {BUSY} or more sends in a day.</p>
    </div>
  );
}
