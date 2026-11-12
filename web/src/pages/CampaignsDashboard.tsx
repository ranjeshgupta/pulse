import { useQuery } from "@tanstack/react-query";
import { Fragment, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, CHANNEL_NAMES, CHANNELS, type CampaignDashRow, type CampaignsDashboard as Dash, type Channel } from "../api";
import {
  barWidth, campaignHref, categoryLabel, csvRows, DASH_CATEGORIES, dashQuery, DEFAULT_SORT, GROUP_LABELS, groupReasons, KIND_LABELS, nextSort,
  rate, rates, reasonLabel, rowRates, sortRows, totals, totalsReported, type Sort, type SortKey,
} from "../campaignDash";
import { csvFileName, downloadCsv, toCsv } from "../csv";
import { count, daysAgo, defaultRange, money, RangePicker, rangeProblem, useDebounced, ymd, type Range } from "../insights";
import { ChannelLabel, Empty, ErrorNote, PageHead } from "../ui";
import { ContactCount } from "../ContactCount";
import { outcomeFilters, period, type ContactFilter } from "../contactLinks";
import "./campaign-dash.css";

const NOT_REPORTED = "Not reported by this provider";
const OPEN_HINT = "Opens as a share of delivered messages; of sent messages where the provider doesn't report deliveries.";

function dates(r: Range): { from: string; to: string } {
  if (r.preset === "custom") return { from: r.from, to: r.to };
  return { from: ymd(daysAgo(r.preset)), to: ymd(new Date()) };
}

const fmtDate = (d: string) => new Date(`${d.slice(0, 10)}T00:00:00`).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" });

export function CampaignsDashboard() {
  const [range, setRange] = useState<Range>(() => defaultRange(30));
  const [category, setCategory] = useState("");
  const [channel, setChannel] = useState("");
  const [tag, setTag] = useState("");
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<Sort>(DEFAULT_SORT);

  const rangeIssue = rangeProblem(range);
  const qs = rangeIssue ? "" : dashQuery({ ...dates(range), category, channel, tag, q: search });
  const debounced = useDebounced(qs, 350);
  const run = useQuery({
    queryKey: ["campaigns-dashboard", debounced],
    queryFn: () => api.get<Dash>(`/v1/analytics/campaigns?${debounced}`),
    enabled: debounced !== "",
    placeholderData: (prev) => prev,
    retry: (n, err) => (err as { status?: number }).status !== 429 && n < 2,
  });
  const tags = useQuery({ queryKey: ["campaign-tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/campaigns/tags") });
  const stale = debounced !== qs || run.isFetching;
  const filtered = !!(channel || tag || search.trim());

  return (
    <div className="page">
      <PageHead
        title="Campaign performance"
        intro="Compare every campaign that sent messages in a period: delivery, engagement, revenue and why messages didn't go out. Click a number to see those contacts."
      />

      <section className="panel cdash-controls">
        <div className="panel-body">
          <RangePicker range={range} onChange={setRange} label="Messages sent in" />
          {rangeIssue && <p className="xs cdash-problem">{rangeIssue}</p>}
        </div>
      </section>

      <div className="tabs cdash-tabs" role="tablist" aria-label="Campaign type">
        {DASH_CATEGORIES.map((c) => (
          <button key={c.key} type="button" role="tab" aria-selected={category === c.key} onClick={() => setCategory(c.key)}>{c.label}</button>
        ))}
      </div>
      <div className="row hub-filters cdash-filters">
        <input
          className="input cdash-search" type="search" placeholder="Search campaign names" value={search}
          onChange={(e) => setSearch(e.target.value)} aria-label="Search campaign names"
        />
        <select className="select inline" value={channel} onChange={(e) => setChannel(e.target.value)} aria-label="Channel">
          <option value="">Any channel</option>
          {CHANNELS.map((c) => <option key={c} value={c}>{CHANNEL_NAMES[c]}</option>)}
        </select>
        <select className="select inline" value={tag} onChange={(e) => setTag(e.target.value)} aria-label="Tag">
          <option value="">Any tag</option>
          {(tags.data?.tags ?? []).map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
        {filtered && <button type="button" className="btn ghost sm" onClick={() => { setChannel(""); setTag(""); setSearch(""); }}>Clear filters</button>}
      </div>

      <ErrorNote error={run.error && !stale ? run.error : null} />
      {!run.data ? (
        <p className="muted small cdash-wait">{qs ? (run.isError ? "" : "Adding up campaigns…") : "Fix the date range to see campaigns."}</p>
      ) : (
        <Body data={run.data} sort={sort} setSort={setSort} stale={stale} filtered={filtered || category !== ""} />
      )}
    </div>
  );
}

function Body({ data: d, sort, setSort, stale, filtered }: { data: Dash; sort: Sort; setSort: (s: Sort) => void; stale: boolean; filtered: boolean }) {
  // M7: each campaign's numbers open the contacts behind them, for this period. The totals add up
  // each campaign's contacts (someone in two campaigns counts twice), so they aren't one list of
  // people and stay plain; the reasons below are people across campaigns and do open.
  const p = period(d.from.slice(0, 10), d.to.slice(0, 10));
  const reports = useMemo(() => d.delivery_reports ?? {}, [d]);
  const rows = useMemo(() => sortRows(d.campaigns ?? [], sort, reports), [d, sort, reports]);
  const t = useMemo(() => totals(rows), [rows]);

  if (rows.length === 0) {
    return (
      <div className={`cdash-body${stale ? " stale" : ""}`} aria-busy={stale}>
        <section className="panel">
          <Empty title={filtered ? "No campaigns match these filters" : "No campaign messages in this period"}>
            {filtered
              ? "Try another type, channel or tag, or clear the search."
              : "Campaigns show up here once they have sent messages in the date range. Try a longer range."}
          </Empty>
        </section>
      </div>
    );
  }

  const reported = totalsReported(rows, reports);
  const tr = rates(t.counts, t.buyers, reported);
  const exportCsv = () => downloadCsv(csvFileName(`Campaign performance ${d.from.slice(0, 10)} to ${d.to.slice(0, 10)}`), toCsv(csvRows(rows, reports, d.currency)));

  return (
    <div className={`cdash-body${stale ? " stale" : ""}`} aria-busy={stale}>
      <div className="figures cdash-figures" title="Totals add up each campaign's contacts. Open a campaign's number in the table to see its contacts.">
        <div><strong><ContactCount value={t.counts.sent} filters={null} /></strong><span>sent <small>{count(t.campaigns)} {t.campaigns === 1 ? "campaign" : "campaigns"}</small></span></div>
        <div title={reported ? undefined : "Some providers in these campaigns don't report deliveries."}>
          <strong><ContactCount value={t.counts.delivered} filters={null} /></strong><span>delivered <small>{reported ? rate(tr.delivery) : "partly reported"}</small></span>
        </div>
        <div><strong><ContactCount value={t.counts.opened} filters={null} /></strong><span>opened <small>{rate(tr.open)}</small></span></div>
        <div><strong><ContactCount value={t.counts.clicked} filters={null} /></strong><span>clicked <small>{rate(tr.click)}</small></span></div>
        <div><strong>{money(t.revenue, d.currency)}</strong><span>revenue <small>{count(t.buyers)} {t.buyers === 1 ? "buyer" : "buyers"}</small></span></div>
        <div><strong><ContactCount value={t.counts.complained} filters={null} /></strong><span>complaints <small>{rate(tr.complaint)}</small></span></div>
        <div title="Messages that failed when sent; skips and bounces are in the reasons below"><strong><ContactCount value={t.counts.failed} filters={null} /></strong><span>failures <small>{rate(t.counts.sent ? t.counts.failed / t.counts.sent : null)}</small></span></div>
      </div>
      <p className="xs faint cdash-period">
        {fmtDate(d.from)} to {fmtDate(d.to)} · counts are unique contacts per campaign · revenue is <code>{d.revenue_event || "purchase"}</code> events
        by recipients within {d.window_days} {d.window_days === 1 ? "day" : "days"} of their first message
      </p>

      <section className="panel section">
        <div className="panel-head">
          <h3>Campaigns</h3>
          <button type="button" className="btn sm" onClick={exportCsv}>Export CSV</button>
        </div>
        <div className="table-scroll">
          <table className="table cdash-table">
            <caption className="cdash-sr">Campaign performance, sorted by {SORT_LABEL[sort.key]} {sort.dir === "asc" ? "ascending" : "descending"}. Expand a row to see failures by reason.</caption>
            <thead>
              <tr>
                <SortHead k="name" label="Campaign" sort={sort} setSort={setSort} />
                <SortHead k="category" label="Type" sort={sort} setSort={setSort} />
                <th scope="col">Channels</th>
                <SortHead k="sent" label="Sent" sort={sort} setSort={setSort} num />
                <SortHead k="delivery" label="Delivery %" sort={sort} setSort={setSort} num title="Delivered as a share of sent" />
                <SortHead k="open" label="Open %" sort={sort} setSort={setSort} num title={OPEN_HINT} />
                <SortHead k="click" label="Click %" sort={sort} setSort={setSort} num title="Contacts who clicked as a share of sent" />
                <SortHead k="downloaded" label="Downloads" sort={sort} setSort={setSort} num title="Clicks on file links (also counted as clicks)" />
                <SortHead k="buyers" label="Conversions" sort={sort} setSort={setSort} num title="Recipients with a revenue event within the window" />
                <SortHead k="revenue" label="Revenue" sort={sort} setSort={setSort} num />
                <SortHead k="unsubscribed" label="Unsubscribes" sort={sort} setSort={setSort} num />
                <SortHead k="complained" label="Complaints" sort={sort} setSort={setSort} num />
                <SortHead k="bounced" label="Bounces" sort={sort} setSort={setSort} num />
                <SortHead k="failed" label="Failures" sort={sort} setSort={setSort} num title="Failed when sent (skipped contacts are in the reasons)" />
              </tr>
            </thead>
            <tbody>{rows.map((r) => <CampaignRow key={`${r.kind}${r.id}`} r={r} reports={reports} currency={d.currency} p={p} />)}</tbody>
          </table>
        </div>
      </section>

      <ReasonsPanel reasons={d.reasons ?? []} p={p} />
    </div>
  );
}

const SORT_LABEL: Record<SortKey, string> = {
  name: "campaign", category: "type", sent: "sent", delivery: "delivery rate", open: "open rate", click: "click rate", downloaded: "downloads",
  buyers: "conversions", revenue: "revenue", unsubscribed: "unsubscribes", complained: "complaints", bounced: "bounces", failed: "failures",
};

function SortHead({ k, label, sort, setSort, num, title }: { k: SortKey; label: string; sort: Sort; setSort: (s: Sort) => void; num?: boolean; title?: string }) {
  const active = sort.key === k;
  return (
    <th scope="col" className={num ? "num" : undefined} aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"} title={title}>
      <button type="button" className={`cdash-sort${active ? " active" : ""}`} onClick={() => setSort(nextSort(sort, k))}>
        {label}
        <span className="cdash-arrow" aria-hidden="true">{active ? (sort.dir === "asc" ? "▲" : "▼") : "↕"}</span>
      </button>
    </th>
  );
}

function CampaignRow({ r, reports, currency, p }: { r: CampaignDashRow; reports: Record<string, boolean>; currency: string; p: ContactFilter }) {
  const of = (o: string, reason?: string): ContactFilter[] => [...outcomeFilters(r.kind as "broadcast" | "journey", r.id, o, { reason }), p];
  const [open, setOpen] = useState(false);
  const x = rowRates(r, reports);
  const c = r.counts;
  const detailId = `cdash-reasons-${r.kind}-${r.id}`;
  const reasons = [...(r.reasons ?? [])].filter((z) => z.contacts > 0).sort((a, b) => b.contacts - a.contacts);
  return (
    <Fragment>
      <tr className={open ? "cdash-open" : undefined}>
        <th scope="row" className="cdash-name">
          <button
            type="button" className="cdash-expand" aria-expanded={open} aria-controls={detailId} onClick={() => setOpen(!open)}
            aria-label={`${open ? "Hide" : "Show"} failures by reason for ${r.name}`}
          >
            <span aria-hidden="true">{open ? "▾" : "▸"}</span>
          </button>
          <span className="cdash-name-text">
            <Link to={campaignHref(r)}>{r.name}</Link>
            {r.tags.length > 0 && <span className="cdash-tags">{r.tags.map((t) => <span key={t} className="tag-chip">{t}</span>)}</span>}
          </span>
        </th>
        <td className="small">{categoryLabel(r.category)}</td>
        <td><div className="row cdash-chans">{r.channels.length ? r.channels.map((ch) => <ChannelLabel key={ch} channel={ch as Channel} />) : <span className="muted small">–</span>}</div></td>
        <td className="num"><ContactCount value={c.sent} filters={of("sent")} /></td>
        <td className="num">
          {x.deliveryReported ? rate(x.delivery) : <span className="muted" title={NOT_REPORTED} aria-label={NOT_REPORTED}>—</span>}
        </td>
        <td className="num" title={`Of ${x.openOf}`}>{rate(x.open)}</td>
        <td className="num">{rate(x.click)}</td>
        <td className="num"><ContactCount value={c.downloaded} filters={of("downloaded")} /></td>
        <td className="num" title={`${rate(x.conversion)} of sent`}>{count(r.buyers)}</td>
        <td className="num">{money(r.revenue, currency)}</td>
        <td className="num"><ContactCount value={c.unsubscribed} filters={of("unsubscribed")} /></td>
        <td className="num"><ContactCount value={c.complained} filters={of("complained")} /></td>
        <td className="num"><ContactCount value={c.bounced} filters={of("bounced")} /></td>
        <td className="num"><ContactCount value={c.failed} filters={of("failed_all")} /></td>
      </tr>
      {open && (
        <tr className="cdash-detail" id={detailId}>
          <td colSpan={14}>
            {reasons.length === 0 ? (
              <p className="small muted cdash-none">No failures, skips or bounces for this campaign in the period.</p>
            ) : (
              <table className="cdash-reason-table">
                <caption className="cdash-sr">Failures by reason for {r.name}</caption>
                <thead><tr><th scope="col">Reason</th><th scope="col">Kind</th><th scope="col" className="num">Contacts</th></tr></thead>
                <tbody>{reasons.map((z) => (
                  <tr key={`${z.kind}-${z.code}`}>
                    <td>{reasonLabel(z)}{z.group && <span className="xs muted"> · {GROUP_LABELS[z.group]}</span>}</td>
                    <td><span className={`badge cdash-kind kind-${z.kind}`}>{KIND_LABELS[z.kind] ?? z.kind}</span></td>
                    <td className="num"><ContactCount value={z.contacts} filters={of(z.kind, z.code)} /></td>
                  </tr>
                ))}</tbody>
              </table>
            )}
          </td>
        </tr>
      )}
    </Fragment>
  );
}

function ReasonsPanel({ reasons, p }: { reasons: Dash["reasons"]; p: ContactFilter }) {
  const { groups, max } = groupReasons(reasons);
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Failures by reason</h3><span className="xs muted">contacts, across the campaigns above</span></div>
      {groups.length === 0 ? (
        <div className="panel-body"><p className="small muted" style={{ margin: 0 }}>Every message in this period went out: nothing was skipped, bounced or failed.</p></div>
      ) : (
        <div className="panel-body cdash-reasons">
          {groups.map((g) => (
            <div key={g.section} className={`cdash-group sec-${g.section}`}>
              <h4>{g.label} <span className="muted small">{count(g.total)}</span></h4>
              <p className="xs muted">{g.hint}</p>
              <ul>
                {g.reasons.map((z) => (
                  <li key={`${z.kind}-${z.code}`}>
                    <span className="cdash-reason-label">
                      {reasonLabel(z)}
                      <span className="xs muted"> {z.group ? GROUP_LABELS[z.group] : KIND_LABELS[z.kind] ?? z.kind}</span>
                    </span>
                    <span className="bar-track cdash-bar" aria-hidden="true"><span className="bar-fill" style={{ width: `${barWidth(z.contacts, max)}%` }} /></span>
                    <span className="num"><ContactCount value={z.contacts} filters={[["outcome", z.kind], ["reason", z.code], p]} /></span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
