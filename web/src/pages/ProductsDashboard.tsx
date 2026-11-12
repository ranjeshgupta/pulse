// Analyze → Products: product views, add-to-carts and purchases from events carrying product_code
// (and product_category), with daily trend, top products, categories and buyer segments.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type KeyboardEvent } from "react";
import { useNavigate } from "react-router-dom";
import { api, can, type ProductRow, type ProductsDashboard as ProductsAnswer } from "../api";
import { useMe } from "../auth";
import { csvFileName, downloadCsv, toCsv } from "../csv";
import { count, defaultRange, money, pct, RangePicker, rangeProblem, useDebounced, type Range } from "../insights";
import {
  buyersSegment, COLUMNS, conversion, DEFAULT_CART_EVENT, DEFAULT_VIEW_EVENT, eventNameProblem, fillDays, hasProductData, METRICS,
  nextSort, productsCsvRows, productsPath, sortProducts, type Metric, type ProductDay, type SortDir, type SortKey,
} from "../productsDash";
import { ErrorNote, Field, PageHead, useToast } from "../ui";
import { compact, labelIndexes, longDay, niceMax, rangeDays, retryUnlessLimited, shortDay, ticks, updatedAt } from "../usersDash";
import "./dashboards.css";
import { ContactCount } from "../ContactCount";
import { productFilters } from "../contactLinks";

/** pf: the contacts behind a products number (M7), with the page's events and period. */
const pf = (d: ProductsAnswer, what: "viewed" | "carted" | "bought" | "abandoned", item: { code?: string; category?: string }) =>
  productFilters(what, d.events, item, d.from.slice(0, 10), d.to.slice(0, 10));

export function ProductsDashboard() {
  const [range, setRange] = useState<Range>(() => defaultRange(30));
  const [viewEvent, setViewEvent] = useState(DEFAULT_VIEW_EVENT);
  const [cartEvent, setCartEvent] = useState(DEFAULT_CART_EVENT);

  const rangeIssue = rangeProblem(range);
  const path = rangeIssue ? "" : productsPath(rangeDays(range), viewEvent, cartEvent);
  const debounced = useDebounced(path);
  const q = useQuery({
    queryKey: ["analytics-products", debounced],
    queryFn: () => api.get<ProductsAnswer>(debounced),
    enabled: debounced !== "",
    placeholderData: (prev) => prev,
    retry: retryUnlessLimited,
  });
  const stale = debounced !== path || q.isFetching;
  const d = q.data;

  return (
    <div className="page">
      <PageHead title="Products" intro="Which products people look at, add to their cart and buy, and who leaves them in the cart." />

      <section className="panel">
        <div className="panel-body dash-controls">
          <div>
            <RangePicker range={range} onChange={setRange} label="Events in" />
            {rangeIssue && <p className="xs dash-problem">{rangeIssue}</p>}
          </div>
          <EventInput label="Product view event" value={viewEvent} onApply={setViewEvent} fallback={DEFAULT_VIEW_EVENT} />
          <EventInput label="Add to cart event" value={cartEvent} onApply={setCartEvent} fallback={DEFAULT_CART_EVENT} />
        </div>
      </section>

      <ErrorNote error={q.error && !stale ? q.error : null} />

      {!d ? (
        <p className="muted small dash-wait">{path ? (q.error ? "" : "Adding up product events…") : "Fix the dates above to see products."}</p>
      ) : (
        <div className={`dash-body${stale ? " stale" : ""}`} aria-busy={stale}>
          <Report d={d} />
        </div>
      )}
    </div>
  );
}

/** EventInput edits an event name, applying it on blur or Enter when it's valid. */
function EventInput({ label, value, onApply, fallback }: { label: string; value: string; onApply: (v: string) => void; fallback: string }) {
  const [draft, setDraft] = useState(value);
  const problem = eventNameProblem(draft);
  const apply = () => {
    const v = draft.trim();
    if (v === "") { setDraft(fallback); onApply(fallback); return; }
    if (!problem) { setDraft(v); onApply(v); }
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") { e.preventDefault(); apply(); }
    if (e.key === "Escape") setDraft(value);
  };
  const id = `dash-ev-${fallback}`;
  return (
    <div className="dash-event">
      <Field label={label} hint={draft.trim() !== value && !problem ? "Press Enter to apply." : undefined}>
        <input
          className="input" value={draft} maxLength={64} spellCheck={false} placeholder={fallback}
          onChange={(e) => setDraft(e.target.value)} onBlur={apply} onKeyDown={onKey}
          aria-invalid={!!problem && draft.trim() !== ""} aria-describedby={problem ? id : undefined}
        />
      </Field>
      {problem && draft.trim() !== "" && <p className="xs dash-problem" id={id}>{problem}</p>}
    </div>
  );
}

function Report({ d }: { d: ProductsAnswer }) {
  const t = d.report.totals;
  const cur = d.currency;
  const ev = d.events;
  if (!hasProductData(d)) {
    return (
      <section className="panel section">
        <div className="empty">
          <h3>No product events in this period</h3>
          <p>
            Products are counted from <code>{ev.view}</code>, <code>{ev.cart}</code> and <code>{ev.purchase}</code> events that carry a{" "}
            <code>product_code</code> property (and optionally <code>product_category</code>). Send those properties with your events,
            pick the event names your site uses above, or try a longer date range.
          </p>
        </div>
      </section>
    );
  }
  const conv = conversion(t);
  return (
    <>
      <div className="figures dash-figures">
        <div><strong>{count(t.views)}</strong><span>product views</span></div>
        <div><strong><ContactCount value={t.viewers} filters={pf(d, "viewed", {})} /></strong><span>viewers</span></div>
        <div title={`${count(t.carts)} add-to-cart events`}><strong><ContactCount value={t.carters} filters={pf(d, "carted", {})} /></strong><span>added to cart</span></div>
        <div><strong><ContactCount value={t.buyers} filters={pf(d, "bought", {})} /></strong><span>buyers</span></div>
        <div><strong>{count(t.purchases)}</strong><span>purchases</span></div>
        <div><strong>{money(t.revenue, cur)}</strong><span>revenue</span></div>
        <div title="Added a product to their cart but didn't buy it in the range"><strong><ContactCount value={t.abandoned} filters={pf(d, "abandoned", {})} /></strong><span>abandoned carts</span></div>
        <div title="Buyers ÷ viewers"><strong>{conv === null ? "–" : pct(conv)}</strong><span>view → buy</span></div>
      </div>
      <p className="xs faint dash-period">
        {longDay(d.from)} to {longDay(d.to)} (UTC days) · Updated {updatedAt(d.computed_at)}
      </p>
      <p className="xs muted dash-note">
        Views from <code>{ev.view}</code>, carts from <code>{ev.cart}</code>, purchases from <code>{ev.purchase}</code> with the amount in{" "}
        <code>{ev.amount}</code>{cur ? `, in ${cur}` : ""} (the revenue settings). Products come from the events' <code>product_code</code> and{" "}
        <code>product_category</code> properties; people are counted once across products in the totals.
      </p>

      <DailyPanel days={fillDays(d.report.days, d.from, d.to)} currency={cur} />
      <ProductsTable d={d} />
      <CategoriesTable rows={d.report.categories} currency={cur} d={d} />
    </>
  );
}

// ---- daily trend ----

function DailyPanel({ days, currency }: { days: ProductDay[]; currency: string }) {
  const [metric, setMetric] = useState<Metric>("views");
  const label = METRICS.find((m) => m.key === metric)!.label;
  const fmt = (n: number) => (metric === "revenue" ? money(n, currency) : count(n));
  const max = niceMax(Math.max(0, ...days.map((x) => x[metric])));
  const total = days.reduce((s, x) => s + x[metric], 0);
  const n = days.length;
  return (
    <section className="panel section">
      <div className="panel-head dash-head">
        <h3>By day</h3>
        <div className="seg-toggle sm dash-wrap" role="group" aria-label="Measure">
          {METRICS.map((m) => <button key={m.key} type="button" aria-pressed={metric === m.key} onClick={() => setMetric(m.key)}>{m.label}</button>)}
        </div>
      </div>
      <div className="panel-body">
        {n === 0 ? <p className="small muted" style={{ margin: 0 }}>No days in this range.</p> : (
          <div className="dash-chart">
            <div className="dash-y" aria-hidden="true">
              {ticks(max).map((t) => <span key={t} style={{ bottom: `${(t / max) * 100}%` }}>{compact(t)}</span>)}
            </div>
            <div className="dash-plot dash-barcols" style={{ gap: n > 120 ? 0 : 2 }} role="img" aria-label={`${label} per day, ${fmt(total)} in total.`}>
              {days.map((x) => (
                <div key={x.day} className="dash-col" title={`${longDay(x.day)}: ${fmt(x[metric])}`}>
                  <div className={`bar-${metric}`} style={{ height: `${(x[metric] / max) * 100}%` }} />
                </div>
              ))}
            </div>
            <div className="dash-x" aria-hidden="true">
              {labelIndexes(n, 5).map((i) => (
                <span key={i} className={i === 0 ? "first" : i === n - 1 ? "last" : ""} style={{ left: `${n === 1 ? 50 : (i / (n - 1)) * 100}%` }}>{shortDay(days[i].day)}</span>
              ))}
            </div>
          </div>
        )}
        <p className="xs muted" style={{ margin: "10px 0 0" }}>{label}: {fmt(total)} in the range.</p>
      </div>
    </section>
  );
}

// ---- tables ----

function ProductsTable({ d }: { d: ProductsAnswer }) {
  const [sort, setSort] = useState<{ key: SortKey; dir: SortDir }>({ key: "revenue", dir: "desc" });
  const canSegment = can(useMe().data?.permissions, "segments", "edit");
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const cur = d.currency;
  const rows = sortProducts(d.report.products, sort.key, sort.dir);

  const segment = useMutation({
    mutationFn: (code: string) => api.post<{ id: number }>("/v1/segments", buyersSegment(d.events.purchase, code, d.from, d.to)),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["segments"] });
      navigate(`/segments/${r.id}`);
    },
    onError: (e) => toast({ text: e instanceof Error ? e.message : "Couldn't create the segment.", error: true }),
  });

  const exportCsv = () => downloadCsv(csvFileName(`Products ${d.from} to ${d.to}`), toCsv(productsCsvRows(rows, cur)));

  return (
    <section className="panel section">
      <div className="panel-head dash-head">
        <div>
          <h3>Top products</h3>
          <p className="xs muted" style={{ margin: "4px 0 0" }}>The top {count(d.report.products.length)} by revenue, then purchases. Click a column to sort.</p>
        </div>
        <button type="button" className="btn sm" onClick={exportCsv} disabled={rows.length === 0}>Export CSV</button>
      </div>
      {rows.length === 0 ? (
        <div className="panel-body"><p className="small muted" style={{ margin: 0 }}>No events with a product_code in this period.</p></div>
      ) : (
        <div className="table-scroll">
          <table className="table dash-table dash-products">
            <caption className="dash-sr">Top products, sorted by {COLUMNS.find((c) => c.key === sort.key)!.label} ({sort.dir === "asc" ? "ascending" : "descending"})</caption>
            <thead><tr>
              {COLUMNS.map((c) => (
                <th key={c.key} scope="col" className={c.num ? "num" : ""} title={c.title}
                  aria-sort={sort.key === c.key ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}>
                  <button type="button" className="dash-sort" onClick={() => setSort(nextSort(sort, c.key))}>
                    {c.label}<span aria-hidden="true" className="dash-arrow">{sort.key === c.key ? (sort.dir === "asc" ? "▲" : "▼") : ""}</span>
                  </button>
                </th>
              ))}
              {canSegment && <th scope="col"><span className="dash-sr">Actions</span></th>}
            </tr></thead>
            <tbody>{rows.map((r) => {
              const conv = conversion(r);
              return (
                <tr key={r.code}>
                  <th scope="row" className="dash-code"><code>{r.code}</code></th>
                  <td className={r.category ? "" : "muted"}>{r.category || "–"}</td>
                  <td className="num" title={`${count(r.viewers)} people viewed`}>{count(r.views)} <small className="faint">(<ContactCount value={r.viewers} filters={pf(d, "viewed", { code: r.code })} />)</small></td>
                  <td className="num" title={`${count(r.carts)} add-to-cart events`}><ContactCount value={r.carters} filters={pf(d, "carted", { code: r.code })} /></td>
                  <td className="num">{count(r.purchases)}</td>
                  <td className="num"><ContactCount value={r.buyers} filters={pf(d, "bought", { code: r.code })} /></td>
                  <td className="num">{money(r.revenue, cur)}</td>
                  <td className="num"><ContactCount value={r.abandoned} filters={pf(d, "abandoned", { code: r.code })} /></td>
                  <td className="num">{conv === null ? "–" : pct(conv)}</td>
                  {canSegment && (
                    <td className="dash-action">
                      <button
                        type="button" className="btn ghost sm" disabled={r.buyers === 0 || segment.isPending}
                        title={r.buyers === 0 ? "No one bought it in this period." : `Make a segment of people who bought ${r.code} in this period`}
                        aria-label={`Create segment from buyers of ${r.code}`}
                        onClick={() => segment.mutate(r.code)}
                      >
                        {segment.isPending && segment.variables === r.code ? "Creating…" : "Segment of buyers"}
                      </button>
                    </td>
                  )}
                </tr>
              );
            })}</tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function CategoriesTable({ rows, currency, d }: { rows: ProductRow[]; currency: string; d: ProductsAnswer }) {
  if (rows.length === 0) return null;
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Categories</h3><span className="xs muted">From the product_category property</span></div>
      <div className="table-scroll">
        <table className="table dash-table">
          <caption className="dash-sr">Product categories</caption>
          <thead><tr>
            <th scope="col">Category</th>
            <th scope="col" className="num">Views</th>
            <th scope="col" className="num">Viewers</th>
            <th scope="col" className="num">Added to cart</th>
            <th scope="col" className="num">Purchases</th>
            <th scope="col" className="num">Buyers</th>
            <th scope="col" className="num">Revenue</th>
            <th scope="col" className="num">Abandoned</th>
            <th scope="col" className="num">Conversion</th>
          </tr></thead>
          <tbody>{rows.map((r) => {
            const conv = conversion(r);
            return (
              <tr key={r.code}>
                <th scope="row" className={r.code ? "" : "muted"}>{r.code || "(none)"}</th>
                <td className="num">{count(r.views)}</td>
                <td className="num"><ContactCount value={r.viewers} filters={pf(d, "viewed", { category: r.code })} /></td>
                <td className="num"><ContactCount value={r.carters} filters={pf(d, "carted", { category: r.code })} /></td>
                <td className="num">{count(r.purchases)}</td>
                <td className="num"><ContactCount value={r.buyers} filters={pf(d, "bought", { category: r.code })} /></td>
                <td className="num">{money(r.revenue, currency)}</td>
                <td className="num"><ContactCount value={r.abandoned} filters={pf(d, "abandoned", { category: r.code })} /></td>
                <td className="num">{conv === null ? "–" : pct(conv)}</td>
              </tr>
            );
          })}</tbody>
        </table>
      </div>
    </section>
  );
}
