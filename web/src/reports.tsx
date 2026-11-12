// Message reports shared by the broadcast and journey pages: headline figures, counts tables,
// top links and daily bars. All counts are unique contacts; rates are relative to sent.
import { useState, type ReactNode } from "react";
import type { MessageCounts, MessageReport } from "./api";
import { LiveNumber } from "./ui";
import { ContactCount } from "./ContactCount";
import type { ContactFilter } from "./contactLinks";

/** The contact list behind a count (an outcome kind), or null when it has none. */
export type CountLink = (k: CountKey) => ContactFilter[] | null;

/** Outcome kinds a contact list can show (see outcome: in internal/api/contactsets.go). */
const LISTABLE = new Set<CountKey>(["sent", "delivered", "opened", "clicked", "read", "unsubscribed", "bounced", "complained", "downloaded", "fallback"]);
export const listable = (k: CountKey) => LISTABLE.has(k);
import "./reports.css";

export type CountKey = keyof MessageCounts;

export const COUNT_LABELS: Record<CountKey, string> = {
  sent: "Sent", delivered: "Delivered", read: "Read", opened: "Opened", clicked: "Clicked", converted: "Converted",
  unsubscribed: "Unsubscribed", failed: "Failed", fallback: "Sent as SMS", suppressed: "Opted out", capped: "Over limit", deferred: "Held for quiet hours",
  bounced: "Bounced", complained: "Spam complaints", downloaded: "Downloads",
};

/** Outcomes shown as a share of sent. */
const RATE_KEYS = new Set<CountKey>(["opened", "clicked", "read", "converted"]);

export const EMPTY_COUNTS: MessageCounts = {
  sent: 0, delivered: 0, read: 0, failed: 0, fallback: 0, suppressed: 0, capped: 0, deferred: 0, opened: 0, clicked: 0, unsubscribed: 0, converted: 0,
  bounced: 0, complained: 0, downloaded: 0,
};

/** pct formats n as a share of `of`: "24%", "3.5%", or "–" when there is nothing to divide by. */
export function pct(n: number, of: number): string {
  if (!of) return "–";
  const p = (n / of) * 100;
  if (p === 0) return "0%";
  return p < 10 ? `${p.toFixed(1)}%` : `${Math.round(p)}%`;
}

/** metricsFor picks the outcomes that make sense for the channels involved.
 * Opens and clicks exist only for email; read receipts only for WhatsApp and RCS. */
export function metricsFor(channels: string[], opts: { conversion?: boolean; detail?: boolean } = {}): CountKey[] {
  const has = (...cs: string[]) => channels.some((c) => cs.includes(c));
  const keys: CountKey[] = ["sent", "delivered"];
  if (has("email")) keys.push("opened", "clicked");
  if (has("whatsapp", "rcs")) keys.push("read");
  if (opts.conversion) keys.push("converted");
  if (has("email") && !opts.detail) keys.push("unsubscribed");
  if (opts.detail) {
    keys.push("failed");
    if (has("whatsapp", "rcs")) keys.push("fallback");
  }
  return keys;
}

/** Figures is the row of headline numbers: counts for sent and delivered, rates for outcomes. */
export function ReportFigures({ totals, keys, link }: { totals: MessageCounts; keys: CountKey[]; link?: CountLink }) {
  return (
    <div className="figures report-figures">
      {keys.map((k) => RATE_KEYS.has(k) ? (
        <div key={k}>
          <strong>{pct(totals[k], totals.sent)}</strong>
          <span>{COUNT_LABELS[k].toLowerCase()} <small><ContactCount value={totals[k]} live filters={listable(k) ? link?.(k) ?? null : null} /></small></span>
        </div>
      ) : (
        <div key={k}><strong><ContactCount value={totals[k]} live filters={listable(k) ? link?.(k) ?? null : null} /></strong><span>{COUNT_LABELS[k].toLowerCase()}</span></div>
      ))}
    </div>
  );
}

/** CountCell shows a count, or for outcome columns its rate with the count underneath. */
export function CountCell({ counts, k, link }: { counts: MessageCounts; k: CountKey; link?: CountLink }) {
  const f = listable(k) ? link?.(k) ?? null : null;
  const n = counts[k] ?? 0;
  if (RATE_KEYS.has(k)) {
    return <span className="report-rate"><span>{pct(n, counts.sent)}</span><small>{n ? <ContactCount value={n} live filters={f} /> : "0"}</small></span>;
  }
  return n ? <ContactCount value={n} live filters={f} /> : <span className="faint">0</span>;
}

export interface CountsRow { key: string; lead: ReactNode[]; counts?: MessageCounts; trail?: ReactNode[]; highlight?: boolean; onClick?: () => void; link?: CountLink }

/** CountsTable lays out one row of counts per thing (variant, step, channel, send). */
export function CountsTable({ lead, rows, keys, trail = [] }: { lead: string[]; rows: CountsRow[]; keys: CountKey[]; trail?: string[] }) {
  return (
    <div className="table-scroll">
      <table className="table">
        <thead><tr>
          {lead.map((h) => <th key={h}>{h}</th>)}
          {keys.map((k) => <th key={k} className="num">{RATE_KEYS.has(k) ? `${COUNT_LABELS[k]} rate` : COUNT_LABELS[k]}</th>)}
          {trail.map((h, i) => <th key={`t${i}`}>{h}</th>)}
        </tr></thead>
        <tbody>{rows.map((r) => (
          <tr key={r.key} className={[r.highlight ? "report-winner" : "", r.onClick ? "clickable" : ""].join(" ").trim() || undefined} onClick={r.onClick}>
            {r.lead.map((c, i) => <td key={`l${i}`}>{c}</td>)}
            {keys.map((k) => <td key={k} className="num"><CountCell counts={r.counts ?? EMPTY_COUNTS} k={k} link={r.link} /></td>)}
            {trail.map((_, i) => <td key={`t${i}`}>{r.trail?.[i]}</td>)}
          </tr>
        ))}</tbody>
      </table>
    </div>
  );
}

/** ReportPanel is a titled panel; pass `body` for padded content, or a table as children. */
export function ReportPanel({ title, aside, children, body }: { title: ReactNode; aside?: ReactNode; children: ReactNode; body?: boolean }) {
  return (
    <section className="panel">
      <div className="panel-head"><h3>{title}</h3>{aside}</div>
      {body ? <div className="panel-body">{children}</div> : children}
    </section>
  );
}

export function TopLinks({ links }: { links: MessageReport["links"] }) {
  return (
    <ReportPanel title="Top links" body={links.length === 0}>
      {links.length === 0 ? <p className="muted small">No clicks yet.</p> : (
        <div className="table-scroll">
          <table className="table">
            <thead><tr><th>Link</th><th className="num">Contacts</th><th className="num">Clicks</th></tr></thead>
            <tbody>{links.map((l) => (
              <tr key={l.url}>
                <td><a className="report-url" href={l.url} target="_blank" rel="noopener noreferrer" title={l.url}>{l.url.replace(/^https?:\/\//, "")}</a></td>
                <td className="num"><LiveNumber value={l.contacts} /></td>
                <td className="num"><LiveNumber value={l.clicks} /></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </ReportPanel>
  );
}

type Kind = MessageReport["daily"][number]["kind"];
const KIND_LABELS: Record<Kind, string> = { sent: "Sent", opened: "Opened", clicked: "Clicked" };

/** DailyBars shows one bar per day for sent, opened or clicked. */
export function DailyBars({ daily, link }: { daily: MessageReport["daily"]; link?: (kind: string, day: string) => ContactFilter[] | null }) {
  const kinds = (Object.keys(KIND_LABELS) as Kind[]).filter((k) => daily.some((d) => d.kind === k && d.count > 0));
  const [pick, setPick] = useState<Kind>("sent");
  const kind = kinds.includes(pick) ? pick : kinds[0] ?? "sent";
  const byDay = new Map<string, number>();
  for (const d of daily) if (d.kind === kind) byDay.set(d.day.slice(0, 10), (byDay.get(d.day.slice(0, 10)) ?? 0) + d.count);
  const days = [...byDay.entries()].sort(([a], [b]) => a.localeCompare(b));
  const max = Math.max(1, ...days.map(([, n]) => n));
  return (
    <ReportPanel
      title="By day"
      body
      aside={kinds.length > 1 && (
        <div className="report-kinds">
          {kinds.map((k) => <button key={k} type="button" aria-pressed={k === kind} onClick={() => setPick(k)}>{KIND_LABELS[k]}</button>)}
        </div>
      )}
    >
      {days.length === 0 ? <p className="muted small">Nothing sent yet.</p> : (
        <div className="bars">
          {days.map(([day, n]) => (
            <div className="bar-row" key={day}>
              <span>{new Date(`${day}T00:00:00`).toLocaleDateString("en-IN", { day: "numeric", month: "short" })}</span>
              <div className="bar-track"><div className={`bar-fill kind-${kind}`} style={{ width: `${(n / max) * 100}%` }} /></div>
              <span className="num"><ContactCount value={n} live filters={link?.(kind, day) ?? null} /></span>
            </div>
          ))}
        </div>
      )}
    </ReportPanel>
  );
}
