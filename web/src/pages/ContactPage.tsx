// The contact page (/contacts/:id): everything about one person on one page. The drawer on the
// contact list is the quick look; this is the full profile, message history and event timeline.
import { ConsentPrivacyPanel } from "./ConsentPrivacy";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Fragment, useEffect, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  api, ApiError, CHANNEL_NAMES, type Channel, type ChannelReach, type Contact, type ContactMessage, type ContactProfile, type EventRow, type Segment,
} from "../api";
import { isAccountAttr } from "../accounts";
import { useCan } from "../auth";
import {
  attrText, campaignLink, campaignName, contactName, distinctNames, eventsPath, formatProps, formatTime, messagesPath, messageStatus, nextBefore,
  parseNames, productList, propsPreview, stepTypeLabel, toggleName, uniqueBy, WINDOW_LABELS, WINDOWS, type EventKind,
} from "../contactPage";
import { money } from "../insights";
import { Empty, ErrorNote, PageHead, Status, timeAgo } from "../ui";
import { CustomFields, SegmentWhy } from "./ContactFields";
import { ConsentPanel, DeleteContactModal, MergeModal, StageAndCompany, TagsAndPoints } from "./Contacts";
import { ContactInbox } from "./InApp";
import "./contacts.css";
import "./contact-page.css";

const PAGE = 50;
type Tab = "overview" | "messages" | "events" | "consent";
const TABS: { key: Tab; label: string }[] = [
  { key: "overview", label: "Overview" }, { key: "messages", label: "Messages" }, { key: "events", label: "Events" },
  { key: "consent", label: "Consent & privacy" },
];

const notFound = (e: unknown) => e instanceof ApiError && e.status === 404;
const chanName = (c: string) => CHANNEL_NAMES[c as Channel] ?? c;

export function ContactPage() {
  const externalId = useParams().id ?? "";
  const id = encodeURIComponent(externalId);
  const nav = useNavigate();
  const [params, setParams] = useSearchParams();
  const tab: Tab = TABS.some((t) => t.key === params.get("tab")) ? (params.get("tab") as Tab) : "overview";
  const setTab = (t: Tab) => {
    const next = new URLSearchParams(params);
    if (t === "overview") next.delete("tab"); else next.set("tab", t);
    setParams(next, { replace: true });
  };
  const retry = (n: number, e: unknown) => !notFound(e) && n < 2;
  const c = useQuery({
    queryKey: ["contact", externalId],
    queryFn: () => api.get<{ contact: Contact; segments: number[] }>(`/v1/contacts/${id}`),
    refetchInterval: (q) => (notFound(q.state.error) ? false : 10_000), retry,
  });
  const profile = useQuery({
    queryKey: ["contact", externalId, "profile"],
    queryFn: () => api.get<ContactProfile>(`/v1/contacts/${id}/profile`),
    refetchInterval: 30_000, retry, enabled: !notFound(c.error),
  });
  const canEdit = useCan("contacts", "edit");
  const [merging, setMerging] = useState(false);
  const [deleting, setDeleting] = useState(false);

  const back = <Link className="back" to="/contacts">Contacts</Link>;
  if (notFound(c.error)) {
    return (
      <div className="page cp-page">
        <PageHead back={back} title={externalId} />
        <div className="panel">
          <Empty title="Contact not found" action={<Link className="btn" to="/contacts">Back to contacts</Link>}>
            No contact has the external ID “{externalId}”. They may have been deleted, or the link is wrong.
          </Empty>
        </div>
      </div>
    );
  }

  const contact = c.data?.contact ?? profile.data?.contact;
  const merged = contact?.attrs.merged_into != null;
  const name = contact ? contactName(contact) : "";
  const act = profile.data?.activity;

  return (
    <div className="page cp-page">
      <PageHead
        back={back}
        title={name || externalId}
        intro={name ? externalId : undefined}
        actions={contact && <>
          {!merged && <button className="btn" onClick={() => setMerging(true)}>Merge into…</button>}
          {canEdit && <a className="btn" href={`/v1/contacts/${id}/data`} download
            title="Everything Pulse keeps about this person (profile, consent, segments, events) as a JSON file, for a data access request">Download data</a>}
          {canEdit && <button className="btn danger" onClick={() => setDeleting(true)}>Delete</button>}
        </>}
      />
      <ErrorNote error={c.error ?? profile.error} />
      {merged && (
        <div className="alert">
          This contact was merged into{" "}
          <Link to={`/contacts/${encodeURIComponent(String(contact!.attrs.merged_into))}`}><strong>{String(contact!.attrs.merged_into)}</strong></Link>.
          Their history now belongs to that contact; new events go there too.
        </div>
      )}
      {contact && (
        <dl className="cp-meta">
          <Meta label="External ID"><code>{contact.external_id}</code></Meta>
          <Meta label="Email">{contact.email || <span className="faint">none</span>}</Meta>
          {name && <Meta label="Name">{name}</Meta>}
          <Meta label="Stage">{attrText(contact, "stage") || <span className="faint">none</span>}</Meta>
          <Meta label="Owner">{attrText(contact, "owner_name") || <span className="faint">none</span>}</Meta>
          <Meta label="Created"><span title={formatTime(contact.created_at)}>{timeAgo(contact.created_at)}</span></Meta>
          <Meta label="Updated"><span title={formatTime(contact.updated_at)}>{timeAgo(contact.updated_at)}</span></Meta>
          <Meta label="Last active">{act?.last_active ? <span title={formatTime(act.last_active)}>{timeAgo(act.last_active)}</span> : <span className="faint">{act ? "never" : "–"}</span>}</Meta>
        </dl>
      )}
      {merging && contact && <MergeModal loser={contact} onClose={() => setMerging(false)} />}
      {deleting && contact && <DeleteContactModal contact={contact} onClose={() => setDeleting(false)} onDeleted={() => nav("/contacts")} />}

      <div className="tabs" role="tablist" aria-label="Contact sections">
        {TABS.map((t) => (
          <button key={t.key} role="tab" id={`cp-tab-${t.key}`} aria-controls="cp-tabpanel" aria-selected={tab === t.key} onClick={() => setTab(t.key)}>{t.label}</button>
        ))}
      </div>
      <div className="cp-layout">
        <div className="cp-main" role="tabpanel" id="cp-tabpanel" aria-labelledby={`cp-tab-${tab}`}>
          {tab === "overview" && (
            <>
              <ReachPanel profile={profile.data} loading={profile.isLoading} />
              <ActivityPanel profile={profile.data} loading={profile.isLoading} />
              <PurchasesPanel profile={profile.data} />
              <JourneysPanel profile={profile.data} />
              <SegmentsPanel externalId={externalId} segments={c.data?.segments} />
            </>
          )}
          {tab === "messages" && <MessagesPanel externalId={externalId} />}
          {tab === "events" && <EventsPanel externalId={externalId} />}
          {tab === "consent" && <ConsentPrivacyPanel externalId={externalId} canEdit={canEdit} onDelete={() => setDeleting(true)} />}
        </div>
        {contact && (
          <aside className="cp-side" aria-label="Profile">
            {!merged && <StageAndCompany contact={contact} />}
            <TagsAndPoints contact={contact} />
            <ConsentPanel contact={contact} />
            <CustomFields contact={contact} />
            <DevicesPanel profile={profile.data} />
            <ContactInbox externalId={contact.external_id} />
            <AttributesPanel contact={contact} />
          </aside>
        )}
      </div>
    </div>
  );
}

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>;
}

function Card({ title, aside, children, flush }: { title: ReactNode; aside?: ReactNode; children: ReactNode; flush?: boolean }) {
  return (
    <section className="panel cp-card">
      <div className="panel-head"><h3>{title}</h3>{aside}</div>
      {flush ? children : <div className="panel-body">{children}</div>}
    </section>
  );
}

// ---- overview ----

function ReachPanel({ profile, loading }: { profile?: ContactProfile; loading: boolean }) {
  const rows = profile?.reach ?? [];
  return (
    <Card title="Reachability" aside={<span className="xs muted">Can a message get to them on each channel?</span>} flush={rows.length > 0}>
      {rows.length === 0 ? <p className="muted small">{loading ? "Loading…" : "No channels to show."}</p> : (
        <div className="table-scroll">
          <table className="table cp-reach">
            <thead><tr><th scope="col">Channel</th><th scope="col">Address</th><th scope="col">Reachable</th><th scope="col">Why</th></tr></thead>
            <tbody>{rows.map((r) => <ReachRow key={r.channel} r={r} />)}</tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function ReachRow({ r }: { r: ChannelReach }) {
  return (
    <tr>
      <th scope="row"><span className={`chan chan-${r.channel}`}>{chanName(r.channel)}</span></th>
      <td className="cp-addr">{r.has_address ? (r.address || <span className="muted">yes</span>) : <span className="faint">none</span>}</td>
      <td>
        <span className={`cp-mark ${r.reachable ? "yes" : "no"}`} aria-label={r.reachable ? "Reachable" : "Not reachable"}>{r.reachable ? "✓" : "✗"}</span>
        {r.opted_out && <span className="badge bad">Opted out</span>}
        {r.bounced && <span className="badge bad">Bounced</span>}
      </td>
      <td className="small muted">{r.why}</td>
    </tr>
  );
}

const ACTIVITY_ROWS = [
  { key: "events", label: "Events" }, { key: "messages", label: "Messages" }, { key: "opens", label: "Opens" }, { key: "clicks", label: "Clicks" },
] as const;

function ActivityPanel({ profile, loading }: { profile?: ContactProfile; loading: boolean }) {
  const act = profile?.activity;
  return (
    <Card title="Activity">
      {!act ? <p className="muted small">{loading ? "Loading…" : "Activity counts need the analytics database."}</p> : (
        <>
          <div className="cp-activity">
            {ACTIVITY_ROWS.map((row) => (
              <div className="cp-stat" key={row.key}>
                <h4>{row.label}</h4>
                <dl>
                  {WINDOWS.map((w) => (
                    <div key={w}><dt>{WINDOW_LABELS[w]}</dt><dd>{(act[row.key]?.[w] ?? 0).toLocaleString("en-IN")}</dd></div>
                  ))}
                </dl>
              </div>
            ))}
          </div>
          <h4 className="small" style={{ margin: "18px 0 8px" }}>Top events <span className="muted xs">last 30 days</span></h4>
          {act.top_events.length === 0 ? <p className="muted small">No events in the last 30 days.</p> : (
            <ul className="cp-top">
              {act.top_events.map((e) => {
                const max = Math.max(1, ...act.top_events.map((x) => x.count));
                return (
                  <li key={e.name}>
                    <span className="cp-top-name">{e.name}</span>
                    <span className="cp-top-bar" aria-hidden><span style={{ width: `${(e.count / max) * 100}%` }} /></span>
                    <span className="num">{e.count.toLocaleString("en-IN")}</span>
                  </li>
                );
              })}
            </ul>
          )}
        </>
      )}
    </Card>
  );
}

function PurchasesPanel({ profile }: { profile?: ContactProfile }) {
  if (!profile) return null;
  const p = profile.purchases;
  const products = productList(profile.products);
  if (!p && products.length === 0) return null;
  return (
    <Card title="Purchases and products" aside={p ? <span className="xs muted">{p.event} events</span> : undefined} flush>
      {p && (
        <div className="figures cp-figures">
          <div><strong>{p.count.toLocaleString("en-IN")}</strong><span>{p.count === 1 ? "purchase" : "purchases"}</span></div>
          <div><strong>{money(p.total, p.currency)}</strong><span>spent in total</span></div>
        </div>
      )}
      {products.length > 0 && (
        <div className="panel-body cp-products">
          <h4 className="small">Products <span className="muted xs">the products list field</span></h4>
          <div className="row">{products.map((x) => <span className="tag-chip" key={x}>{x}</span>)}</div>
        </div>
      )}
      {p && (p.items.length === 0 ? <div className="panel-body"><p className="muted small">No purchases yet.</p></div> : (
        <div className="table-scroll">
          <table className="table">
            <thead><tr><th scope="col">When</th><th scope="col">Product</th><th scope="col">Category</th><th scope="col" className="num">Amount</th></tr></thead>
            <tbody>{p.items.map((it) => (
              <tr key={it.id}>
                <td title={formatTime(it.ts)}>{timeAgo(it.ts)}</td>
                <td>{it.product_code || <span className="faint">–</span>}</td>
                <td>{it.product_category || <span className="faint">–</span>}</td>
                <td className="num">{money(it.amount, p.currency)}</td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      ))}
    </Card>
  );
}

function JourneysPanel({ profile }: { profile?: ContactProfile }) {
  if (!profile) return null;
  const js = [...profile.journeys].sort((a, b) => Number(b.current) - Number(a.current) || b.updated_at.localeCompare(a.updated_at));
  return (
    <Card title="Journeys" aside={<span className="xs muted">{js.filter((j) => j.current).length} in progress</span>} flush={js.length > 0}>
      {js.length === 0 ? <p className="muted small">Not in any journey.</p> : (
        <div className="table-scroll">
          <table className="table">
            <thead><tr><th scope="col">Journey</th><th scope="col">Status</th><th scope="col">Step</th><th scope="col">Entered</th><th scope="col">Updated</th></tr></thead>
            <tbody>{js.map((j) => (
              <tr key={`${j.journey_id}:${j.entered_at}`}>
                <td><Link to={`/journeys/${j.journey_id}`}>{j.name || `Journey ${j.journey_id}`}</Link>{j.current && <span className="badge info" style={{ marginLeft: 6 }}>Current</span>}</td>
                <td>{j.status ? <Status value={j.status} /> : null}</td>
                <td>{j.step ? <><code className="xs">{j.step}</code> <span className="muted small">{stepTypeLabel(j.step_type)}</span></> : <span className="faint">–</span>}</td>
                <td title={formatTime(j.entered_at)}>{timeAgo(j.entered_at)}</td>
                <td title={formatTime(j.updated_at)}>{timeAgo(j.updated_at)}</td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function SegmentsPanel({ externalId, segments }: { externalId: string; segments?: number[] }) {
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const names = new Map(segs.data?.segments.map((s) => [s.id, s.name]));
  const [why, setWhy] = useState<number | null>(null);
  const [askWhy, setAskWhy] = useState("");
  return (
    <Card title="Segments" aside={<span className="xs muted">Select one to see why they're in it</span>}>
      {segments && segments.length === 0 && <p className="muted small">Not in any segment right now.</p>}
      <div className="row" style={{ marginBottom: 10 }}>
        {segments?.map((s) => (
          <button type="button" className={`badge info seg-badge${why === s ? " on" : ""}`} key={s} aria-pressed={why === s} onClick={() => setWhy(why === s ? null : s)} title="Why is this contact in this segment?">
            {names.get(s) ?? `Segment ${s}`} <span className="xs">· why?</span>
          </button>
        ))}
      </div>
      {segs.data && (
        <div className="row" style={{ marginBottom: 12 }}>
          <select className="select sm" value={askWhy} onChange={(e) => { setAskWhy(e.target.value); if (e.target.value) setWhy(Number(e.target.value)); }} aria-label="Check another segment">
            <option value="">Why not in another segment?</option>
            {segs.data.segments.filter((s) => s.kind === "rule" && !segments?.includes(s.id)).map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </div>
      )}
      {why !== null && <SegmentWhy externalId={externalId} segment={why} onClose={() => { setWhy(null); setAskWhy(""); }} />}
    </Card>
  );
}

// ---- side ----

function DevicesPanel({ profile }: { profile?: ContactProfile }) {
  if (!profile) return null;
  return (
    <section>
      <h3 style={{ marginBottom: 10 }}>Devices</h3>
      {profile.devices.length === 0 ? <p className="muted small">No push devices.</p> : (
        <ul className="cp-devices">
          {profile.devices.map((d, i) => <li key={`${d.token}:${i}`}><span className="badge">{d.platform || "unknown"}</span> <code className="xs">{d.token}</code></li>)}
        </ul>
      )}
    </section>
  );
}

function AttributesPanel({ contact }: { contact: Contact }) {
  const rows = Object.entries(contact.attrs).filter(([k]) => !k.startsWith("optout_") && k !== "tags" && k !== "points" && !isAccountAttr(k)).sort(([a], [b]) => a.localeCompare(b));
  return (
    <section>
      <h3 style={{ marginBottom: 10 }}>Attributes</h3>
      {rows.length === 0 ? <p className="muted small">No attributes.</p> : (
        <dl className="kv">
          {rows.map(([k, v]) => <Fragment key={k}><dt>{k}</dt><dd>{typeof v === "object" ? JSON.stringify(v) : String(v)}</dd></Fragment>)}
        </dl>
      )}
    </section>
  );
}

// ---- messages ----

function MessagesPanel({ externalId }: { externalId: string }) {
  const q = useInfiniteQuery({
    queryKey: ["contact", externalId, "messages"],
    queryFn: ({ pageParam }) => api.get<{ messages: ContactMessage[] }>(messagesPath(externalId, pageParam || undefined, PAGE)),
    initialPageParam: "",
    getNextPageParam: (last) => nextBefore(last.messages, PAGE, (m) => m.at),
  });
  const rows = uniqueBy(q.data?.pages.flatMap((p) => p.messages) ?? [], (m) => m.message_id);
  return (
    <Card title="Messages" aside={<span className="xs muted">Newest first; the status is the furthest it got</span>} flush={rows.length > 0}>
      <ErrorNote error={q.error} />
      {rows.length === 0 ? <p className="muted small">{q.isLoading ? "Loading…" : "No messages sent to this contact yet."}</p> : (
        <>
          <div className="table-scroll">
            <table className="table cp-messages">
              <thead><tr><th scope="col">Sent</th><th scope="col">Channel</th><th scope="col">Campaign</th><th scope="col">Status</th><th scope="col">Reason</th><th scope="col">Opened</th><th scope="col">Clicked</th></tr></thead>
              <tbody>{rows.map((m) => <MessageRow key={m.message_id} m={m} />)}</tbody>
            </table>
          </div>
          {q.hasNextPage && (
            <div className="panel-body"><button className="btn" onClick={() => q.fetchNextPage()} disabled={q.isFetchingNextPage}>{q.isFetchingNextPage ? "Loading…" : "Load more"}</button></div>
          )}
        </>
      )}
    </Card>
  );
}

function MessageRow({ m }: { m: ContactMessage }) {
  const st = messageStatus(m.status);
  const to = campaignLink(m);
  const name = campaignName(m);
  const others = m.kinds.filter((k) => k !== m.status).map((k) => messageStatus(k).label);
  return (
    <tr>
      <td title={formatTime(m.at)} className="cp-nowrap">{timeAgo(m.at)}</td>
      <td><span className={`chan chan-${m.channel}`}>{chanName(m.channel)}</span></td>
      <td>
        {to ? <Link to={to}>{name}</Link> : <span className="muted">{name}</span>}
        {m.node && <span className="muted xs"> · step <code>{m.node}</code></span>}
        {m.variant && <span className="muted xs"> · variant {m.variant}</span>}
      </td>
      <td>
        <span className={`badge ${st.tone}`} title={others.length ? `Also: ${others.join(", ")}` : undefined}>{st.label}</span>
        {m.last && m.last !== m.at && <div className="xs muted" title={formatTime(m.last)}>{timeAgo(m.last)}</div>}
      </td>
      <td className="small">
        {m.reason_label || m.reason ? (
          m.reason && m.reason !== m.reason_label ? (
            <details className="cp-reason">
              <summary>{m.reason_label || "Provider said"}</summary>
              <span className="xs muted">{m.reason}</span>
            </details>
          ) : (m.reason_label || m.reason)
        ) : <span className="faint">–</span>}
      </td>
      <td>{m.opened ? <span className="cp-mark yes" aria-label="Opened">✓</span> : <span className="faint" aria-label="Not opened">–</span>}</td>
      <td>{m.clicked ? <span className="cp-mark yes" aria-label={`Clicked ${m.clicks} times`}>✓{m.clicks > 1 && <span className="xs muted"> ×{m.clicks}</span>}</span> : <span className="faint" aria-label="Not clicked">–</span>}</td>
    </tr>
  );
}

// ---- events ----

const KINDS: { key: EventKind; label: string }[] = [
  { key: "", label: "All" }, { key: "activity", label: "Their activity" }, { key: "pulse", label: "Pulse events" },
];

function EventsPanel({ externalId }: { externalId: string }) {
  const [kind, setKind] = useState<EventKind>("");
  const [namesText, setNamesText] = useState("");
  const [applied, setApplied] = useState<string[]>([]);
  const q = useInfiniteQuery({
    queryKey: ["contact", externalId, "events", kind, applied.join(",")],
    queryFn: ({ pageParam }) => api.get<{ events: EventRow[] }>(eventsPath(externalId, { kind, names: applied, before: pageParam || undefined, limit: PAGE })),
    initialPageParam: "",
    getNextPageParam: (last) => nextBefore(last.events, PAGE, (e) => e.ts),
  });
  const rows = uniqueBy(q.data?.pages.flatMap((p) => p.events) ?? [], (e) => e.id);
  // Names to pick from: those loaded, plus the ones filtered on (so they can be removed).
  const [seen, setSeen] = useState<string[]>([]);
  const present = distinctNames(rows);
  const presentKey = present.join(",");
  useEffect(() => {
    setSeen((s) => { const add = present.filter((n) => !s.includes(n)); return add.length ? [...s, ...add].slice(0, 20) : s; });
  }, [presentKey]); // eslint-disable-line react-hooks/exhaustive-deps
  const choices = [...new Set([...applied, ...seen])].slice(0, 24);
  const apply = (text: string) => { setNamesText(text); setApplied(parseNames(text)); };
  const [open, setOpen] = useState<Set<string>>(new Set());
  const toggle = (rid: string) => setOpen((o) => { const n = new Set(o); if (n.has(rid)) n.delete(rid); else n.add(rid); return n; });

  return (
    <Card title="Events" aside={<span className="xs muted">Newest first</span>} flush>
      <div className="panel-body cp-filters">
        <div className="cp-seg" role="group" aria-label="Which events">
          {KINDS.map((k) => (
            <button key={k.key || "all"} type="button" aria-pressed={kind === k.key} onClick={() => setKind(k.key)}>{k.label}</button>
          ))}
        </div>
        <form className="cp-names" onSubmit={(e) => { e.preventDefault(); apply(namesText); }}>
          <input className="input" value={namesText} onChange={(e) => setNamesText(e.target.value)} placeholder="Event names, separated by commas" aria-label="Filter by event names" />
          <button className="btn sm">Filter</button>
          {applied.length > 0 && <button type="button" className="btn ghost sm" onClick={() => apply("")}>Clear</button>}
        </form>
        {choices.length > 0 && (
          <div className="cp-name-chips" aria-label="Event names">
            {choices.map((n) => (
              <button key={n} type="button" className={`badge seg-badge${applied.includes(n) ? " info on" : ""}`} aria-pressed={applied.includes(n)} onClick={() => apply(toggleName(namesText, n))}>{n}</button>
            ))}
          </div>
        )}
      </div>
      <ErrorNote error={q.error} />
      {rows.length === 0 ? <div className="panel-body"><p className="muted small">{q.isLoading ? "Loading…" : "No events match."}</p></div> : (
        <ol className="cp-timeline">
          {rows.map((e) => {
            const pretty = formatProps(e.props);
            const isOpen = open.has(e.id);
            return (
              <li key={e.id}>
                <button type="button" className="cp-ev" aria-expanded={pretty ? isOpen : undefined} disabled={!pretty} onClick={() => toggle(e.id)}>
                  <span className="cp-ev-time" title={formatTime(e.ts)}>{timeAgo(e.ts)}</span>
                  <strong className="cp-ev-name">{e.name}</strong>
                  <span className="cp-ev-props muted xs">{isOpen ? "" : propsPreview(e.props)}</span>
                </button>
                {isOpen && pretty && <pre className="cp-json">{pretty}</pre>}
              </li>
            );
          })}
        </ol>
      )}
      {q.hasNextPage && (
        <div className="panel-body"><button className="btn" onClick={() => q.fetchNextPage()} disabled={q.isFetchingNextPage}>{q.isFetchingNextPage ? "Loading…" : "Load more"}</button></div>
      )}
    </Card>
  );
}
