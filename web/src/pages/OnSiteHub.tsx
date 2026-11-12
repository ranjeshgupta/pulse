import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type DragEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, ApiError, type DWCSlot, type FocusItem, type Overlap } from "../api";
import { useCan } from "../auth";
import { moveBy, moveVariant } from "../onsiteVariants";
import { describeWindow } from "../schedule";
import { Drawer, Empty, ErrorNote, Modal, PageHead, Status, useToast } from "../ui";
import type { InAppMessage } from "./InApp";
import { OverlapView } from "./OnSite";
import "./onsite-variants.css";
import "./segments.css";

// One place for everything shown inside a website or an app: focus items (pop-ups, banner bars,
// slide-ins) and dynamic web content on websites, in-app messages in mobile apps. Each keeps its
// own editor and API; this page lists them together with a channel badge and filters.

type Channel = "website" | "app";
type Kind = "focus" | "content" | "inapp";
type Row = { key: string; kind: Kind; channel: Channel; name: string; type: string; schedule: string; status: string; href: string; shown?: number; clicked?: number };

const CHANNEL_LABEL: Record<Channel, string> = { website: "Website", app: "Mobile app" };
const KIND_LABEL: Record<Kind, string> = { focus: "Pop-ups, bars and slide-ins", content: "Dynamic web content", inapp: "In-app messages" };
const FOCUS_TYPE = { modal: "Pop-up", bar: "Banner bar", slide: "Slide-in" };
const INAPP_TYPE = { modal: "Pop-up", banner: "Banner", fullscreen: "Full screen" };

/** Where each kind's editor lives. */
export const onsiteHref = (kind: Kind, id: number | "new") => `/on-site/${kind === "focus" ? "focus" : kind === "content" ? "content" : "in-app"}/${id}`;

export function ChannelBadge({ channel }: { channel: Channel }) {
  return <span className={`badge ${channel === "website" ? "info" : "warn"}`}>{CHANNEL_LABEL[channel]}</span>;
}

export function OnSiteList() {
  const [params, setParams] = useSearchParams();
  const channel = (["website", "app"].includes(params.get("channel") ?? "") ? params.get("channel") : "") as Channel | "";
  const kind = (["focus", "content", "inapp"].includes(params.get("type") ?? "") ? params.get("type") : "") as Kind | "";
  const set = (k: string, v: string) => { const p = new URLSearchParams(params); if (v) p.set(k, v); else p.delete(k); setParams(p, { replace: true }); };
  const [search, setSearch] = useState("");
  const [creating, setCreating] = useState(false);
  const [ordering, setOrdering] = useState(false);
  const canEdit = useCan("content", "edit");
  const focus = useQuery({ queryKey: ["focus"], queryFn: () => api.get<{ focus_items: FocusItem[] }>("/v1/focus-items") });
  const dwc = useQuery({ queryKey: ["dwc"], queryFn: () => api.get<{ slots: DWCSlot[] }>("/v1/dwc") });
  const inapp = useQuery({
    queryKey: ["inapp"], refetchInterval: 10_000,
    queryFn: () => api.get<{ messages: InAppMessage[]; stats: Record<string, { shown: number; clicked: number }> }>("/v1/inapp"),
  });
  const stats = inapp.data?.stats ?? {};
  const rows: Row[] = [
    ...(focus.data?.focus_items ?? []).map((f): Row => ({ key: `f${f.id}`, kind: "focus", channel: "website", name: f.name, type: FOCUS_TYPE[f.kind], schedule: describeWindow(f.start_at, f.end_at), status: f.status, href: onsiteHref("focus", f.id) })),
    ...(dwc.data?.slots ?? []).map((d): Row => ({ key: `d${d.id}`, kind: "content", channel: "website", name: d.name, type: `Content slot · ${d.slot}`, schedule: describeWindow(d.start_at, d.end_at), status: d.status, href: onsiteHref("content", d.id) })),
    ...(inapp.data?.messages ?? []).map((m): Row => ({
      key: `i${m.id}`, kind: "inapp", channel: "app", name: m.name, type: INAPP_TYPE[m.layout] ?? m.layout, schedule: describeWindow(m.start_at, m.end_at), status: m.status,
      href: onsiteHref("inapp", m.id), shown: stats[String(m.id)]?.shown ?? 0, clicked: stats[String(m.id)]?.clicked ?? 0,
    })),
  ];
  const shown = rows.filter((r) => (!channel || r.channel === channel) && (!kind || r.kind === kind) && r.name.toLowerCase().includes(search.trim().toLowerCase()));
  const loading = focus.isLoading || dwc.isLoading || inapp.isLoading;
  return (
    <div className="page">
      <PageHead
        title="On-site & in-app"
        intro="Messages shown inside your website (pop-ups, banner bars, slide-ins and dynamic content) and your mobile apps (in-app messages), targeted by segment, page or app event."
        actions={canEdit && <button className="btn primary" onClick={() => setCreating(true)}>New</button>}
      />
      {creating && <NewOnSiteModal onClose={() => setCreating(false)} />}
      {ordering && <FocusOrderDrawer items={focus.data?.focus_items ?? []} canEdit={canEdit} onClose={() => setOrdering(false)} />}
      <div className="row" style={{ marginBottom: 14, justifyContent: "space-between" }}>
        <div className="row">
          <div className="seg-toggle" role="group" aria-label="Channel">
            {(["", "website", "app"] as const).map((c) => <button key={c} aria-pressed={channel === c} onClick={() => { set("channel", c); if (c && kind && (c === "app") !== (kind === "inapp")) set("type", ""); }}>{c ? CHANNEL_LABEL[c] : "All"}</button>)}
          </div>
          <select className="select inline" value={kind} onChange={(e) => set("type", e.target.value)} aria-label="Type">
            <option value="">All types</option>
            {(Object.keys(KIND_LABEL) as Kind[]).filter((k) => !channel || (channel === "app") === (k === "inapp")).map((k) => <option key={k} value={k}>{KIND_LABEL[k]}</option>)}
          </select>
        </div>
        <div className="row">
          {channel !== "app" && (kind === "" || kind === "focus") && (focus.data?.focus_items.length ?? 0) > 1 && (
            <button className="btn" onClick={() => setOrdering(true)}>Order of pop-ups, bars and slide-ins</button>
          )}
          <input className="input" style={{ maxWidth: 260 }} placeholder="Find by name" value={search} onChange={(e) => setSearch(e.target.value)} aria-label="Find by name" />
        </div>
      </div>
      <ErrorNote error={focus.error ?? dwc.error ?? inapp.error} />
      <div className="panel">
        {shown.length === 0 && !loading ? (
          <Empty title={rows.length ? "Nothing matches" : "Nothing yet"} action={canEdit && !rows.length && <button className="btn primary" onClick={() => setCreating(true)}>Create one</button>}>
            {rows.length ? "Try another channel or type." : "An exit-intent offer on your website, or a welcome pop-up on the first app open, is a good start."}
          </Empty>
        ) : (
          <div className="table-scroll">
            <table className="table">
              <thead><tr><th>Name</th><th>Channel</th><th>Type</th><th>Schedule</th><th>Status</th><th className="num">Shown · CTR</th></tr></thead>
              <tbody>{shown.map((r) => (
                <tr key={r.key}>
                  <td><Link to={r.href}>{r.name}</Link></td>
                  <td><ChannelBadge channel={r.channel} /></td>
                  <td className="muted">{r.type}</td>
                  <td className="muted small">{r.schedule}</td>
                  <td><Status value={r.status} /></td>
                  <td className="num">{r.shown != null ? <>{r.shown.toLocaleString("en-IN")} · {r.shown ? `${((r.clicked! / r.shown) * 100).toFixed(1)}%` : "–"}</> : <span className="faint">–</span>}</td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        )}
      </div>
      <p className="xs muted" style={{ marginTop: 10 }}>
        Websites show these through the Pulse web SDK (<code>POST /v1/web/decide</code>); apps through the Android, iOS or Flutter SDK (<code>POST /v1/web/inapp</code>). In-app shown and clicked count unique contacts.
      </p>
    </div>
  );
}

/** NewOnSiteModal asks where it shows first: a website or a mobile app. */
function NewOnSiteModal({ onClose }: { onClose: () => void }) {
  const nav = useNavigate();
  const [channel, setChannel] = useState<Channel | null>(null);
  return (
    <Modal title="New on-site or in-app message" onClose={onClose} footer={<button className="btn" onClick={onClose}>Cancel</button>}>
      <p className="small muted" style={{ marginBottom: 10 }}>Where should it show?</p>
      <div className="seg-kind" role="radiogroup" aria-label="Channel">
        <button role="radio" aria-checked={channel === "website"} className={channel === "website" ? "on" : ""} onClick={() => setChannel("website")}>
          <strong>Website</strong><span>On your site's pages, through the web SDK.</span>
        </button>
        <button role="radio" aria-checked={channel === "app"} className={channel === "app" ? "on" : ""} onClick={() => nav(onsiteHref("inapp", "new"))}>
          <strong>Mobile app</strong><span>An in-app message in your Android or iOS app, when an app event happens.</span>
        </button>
      </div>
      {channel === "website" && (
        <>
          <p className="small muted" style={{ marginBottom: 10 }}>What kind?</p>
          <div className="seg-kind">
            <button onClick={() => nav(onsiteHref("focus", "new"))}>
              <strong>Pop-up, banner bar or slide-in</strong><span>A focus item over the page, shown on load, after a delay, on scroll or on exit intent.</span>
            </button>
            <button onClick={() => nav(onsiteHref("content", "new"))}>
              <strong>Dynamic web content</strong><span>A placeholder in your page that shows different content to different segments.</span>
            </button>
          </div>
        </>
      )}
    </Modal>
  );
}

// ---------- focus item order ----------

type FocusKind = FocusItem["kind"];
const FOCUS_KINDS: FocusKind[] = ["modal", "bar", "slide"];
const KIND_PLURAL: Record<FocusKind, string> = { modal: "Pop-ups", bar: "Banner bars", slide: "Slide-ins" };

/** The order the website tries items of one kind in: priority (lower first), then newest first. */
const byOrder = (a: FocusItem, b: FocusItem) => (a.targeting.priority ?? 0) - (b.targeting.priority ?? 0) || b.id - a.id;

/** FocusOrderDrawer orders focus items within each kind (drag or arrows) and counts their overlap. */
function FocusOrderDrawer({ items, canEdit, onClose }: { items: FocusItem[]; canEdit: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const initial = () => Object.fromEntries(FOCUS_KINDS.map((k) => [k, items.filter((f) => f.kind === k).sort(byOrder).map((f) => f.id)])) as Record<FocusKind, number[]>;
  const [order, setOrder] = useState<Record<FocusKind, number[]>>(initial);
  const [saved, setSaved] = useState<Record<FocusKind, number[]>>(initial);
  const byId = new Map(items.map((f) => [f.id, f]));
  const changed = FOCUS_KINDS.filter((k) => order[k].join() !== saved[k].join());
  const save = useMutation({
    mutationFn: async () => {
      for (const k of changed) await api.put("/v1/focus-items/order", { ids: order[k] });
    },
    onSuccess: () => {
      setSaved(order);
      qc.invalidateQueries({ queryKey: ["focus"] });
      toast({ text: "Order saved. Visitors see it within seconds." });
    },
  });
  return (
    <Drawer title="Order of pop-ups, bars and slide-ins" subtitle="Visitors see one pop-up, one bar and one slide-in at a time: the first in this order they can still see." onClose={onClose}>
      <p className="small muted" style={{ marginBottom: 14 }}>
        Put the most important item first. If a visitor isn't in its audience, has already seen it as often as it allows, or it's outside its schedule, the next one in the list is shown instead.
      </p>
      <p className="xs muted" style={{ marginBottom: 14 }}>New items go to the top of their list until you save an order again.</p>
      {FOCUS_KINDS.filter((k) => order[k].length > 0).map((k) => (
        <KindOrder key={k} kind={k} ids={order[k]} byId={byId} canEdit={canEdit} onChange={(ids) => setOrder({ ...order, [k]: ids })} />
      ))}
      <ErrorNote error={save.error} />
      {canEdit && (
        <div className="row" style={{ marginTop: 18 }}>
          <button className="btn primary" disabled={!changed.length || save.isPending} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : "Save order"}</button>
          {changed.length > 0 && <button className="btn ghost" onClick={() => setOrder(saved)}>Undo changes</button>}
        </div>
      )}
    </Drawer>
  );
}

function KindOrder({ kind, ids, byId, canEdit, onChange }: { kind: FocusKind; ids: number[]; byId: Map<number, FocusItem>; canEdit: boolean; onChange: (ids: number[]) => void }) {
  const [drag, setDrag] = useState<{ from: number; over: number } | null>(null);
  const live = ids.filter((id) => byId.get(id)?.status === "active");
  const overlap = useMutation({
    mutationFn: async (liveIds: number[]) => {
      try {
        return { o: await api.post<Overlap>("/v1/focus-items/overlap", { ids: liveIds }), key: liveIds.join() };
      } catch (e) {
        throw e instanceof ApiError && e.status === 429 ? new Error("Too many counts right now. Try again in a minute.") : e;
      }
    },
  });
  const over = (i: number) => (e: DragEvent) => {
    if (!drag) return;
    e.preventDefault();
    if (drag.over !== i) setDrag({ ...drag, over: i });
  };
  const drop = (i: number) => (e: DragEvent) => {
    if (!drag) return;
    e.preventDefault();
    onChange(moveVariant(ids, drag.from, i));
    setDrag(null);
  };
  return (
    <section className="ov-kind">
      <div className="ov-kind-head">
        <h3>{KIND_PLURAL[kind]}</h3>
        <button className="btn sm" disabled={live.length === 0 || overlap.isPending} title={live.length ? undefined : "Nothing published"} onClick={() => overlap.mutate(live)}>
          {overlap.isPending ? "Counting…" : "Check overlap"}
        </button>
      </div>
      <ol className="ov-order" aria-label={`${KIND_PLURAL[kind]} in order`}>
        {ids.map((id, i) => {
          const f = byId.get(id);
          const cls = [drag?.from === i ? "dragging" : "", drag && drag.over === i && drag.from !== i ? (drag.from < i ? "drop-after" : "drop-before") : ""].filter(Boolean).join(" ");
          return (
            <li key={id} className={cls} onDragOver={over(i)} onDrop={drop(i)} onDragEnd={() => setDrag(null)}>
              {canEdit && (
                <span className="ov-handle" draggable title="Drag to reorder" aria-hidden="true"
                  onDragStart={(e) => { e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("application/x-pulse-focus", String(id)); setDrag({ from: i, over: i }); }}>⋮⋮</span>
              )}
              <span className="ov-num">{i + 1}</span>
              <span className="ov-item-name small"><Link to={onsiteHref("focus", id)}>{f?.name || `Item ${id}`}</Link></span>
              {f && <Status value={f.status} />}
              {canEdit && (
                <span className="row" style={{ gap: 2, flexWrap: "nowrap" }}>
                  <button type="button" className="btn ghost sm" aria-label={`Move ${f?.name ?? id} up`} disabled={i === 0} onClick={() => onChange(moveBy(ids, i, -1))}>↑</button>
                  <button type="button" className="btn ghost sm" aria-label={`Move ${f?.name ?? id} down`} disabled={i === ids.length - 1} onClick={() => onChange(moveBy(ids, i, 1))}>↓</button>
                </span>
              )}
            </li>
          );
        })}
      </ol>
      <ErrorNote error={overlap.error} />
      {overlap.data && (
        <div style={{ marginTop: 10 }}>
          <p className="xs muted" style={{ marginBottom: 6 }}>Published {KIND_PLURAL[kind].toLowerCase()} only, in the order above.</p>
          <OverlapView o={overlap.data.o} noun="item" stale={overlap.data.key !== live.join()} />
        </div>
      )}
    </section>
  );
}
