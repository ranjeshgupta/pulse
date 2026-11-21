import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Segment } from "../api";
import { nowISO, ScheduleFields, scheduleProblem } from "../schedule";
import { ask, askDelete } from "../guard";
import { ErrorNote, Field, PageHead, timeAgo, useToast } from "../ui";
import { useChangeGuard } from "./OnSite";
import { PersonalizeScopeProvider, TemplatedInput, TemplatedText } from "../VarPicker";
import { fillSample, SAMPLE_CONTACT } from "../personalize";
import "./inapp.css";

// ---- API shapes (mirror model.InAppMessage) ----

type Layout = "modal" | "banner" | "fullscreen";
type Platform = "android" | "ios";
export interface InAppButton { text: string; action: "url" | "dismiss"; value?: string }
export interface InAppMessage {
  id: number; name: string; layout: Layout; priority: number; status: "draft" | "active";
  start_at?: string; end_at?: string | null; updated_at?: string;
  content: {
    title: string; body: string; image_url?: string; buttons?: InAppButton[];
    background?: string; text_color?: string; button_color?: string; position?: "top" | "bottom";
  };
  targeting: {
    segment_ids?: number[]; exclude_segment_ids?: number[]; platforms?: Platform[];
    trigger_event: string; screen?: string; delay_seconds?: number; frequency: "once" | "session" | "daily" | "always";
  };
}
export interface InboxMessage { id: number; message_id: string; title: string; body: string; image_url?: string; url?: string; category?: string; created_at: string; read_at?: string; expires_at: string }

const LAYOUT_LABEL: Record<Layout, string> = { modal: "Pop-up", banner: "Banner", fullscreen: "Full screen" };
const LAYOUT_HINT: Record<Layout, string> = { modal: "A card over the screen", banner: "A strip at the top or bottom", fullscreen: "Takes over the whole screen" };
const FREQ_LABEL: Record<InAppMessage["targeting"]["frequency"], string> = {
  once: "Once per contact", session: "Once per app session", daily: "At most once a day", always: "Every time it's triggered",
};
const PLATFORM_LABEL: Record<Platform, string> = { android: "Android", ios: "iOS" };
const COMMON_TRIGGERS = ["app_open", "screen_view", "add_to_cart"];

const n = (v: number) => v.toLocaleString("en-IN");

// A button link is a secure web address or an app deep link (myapp://offers). Plain http and
// script-like schemes are refused, as the server does.
function linkProblem(v?: string): string | null {
  const s = (v ?? "").trim();
  if (!s) return "Add where the button goes.";
  const m = /^([a-z][a-z0-9+.-]*):\/\/\S+$/i.exec(s);
  if (!m) return "Use https://… or a deep link like myapp://offers.";
  if (["http", "javascript", "data", "file", "vbscript"].includes(m[1].toLowerCase())) return "Use https:// or an app deep link.";
  return null;
}

function messageProblem(m: InAppMessage): string | null {
  if (!m.name.trim()) return "Give it a name.";
  if (!m.content.title.trim() && !m.content.body.trim()) return "Add a title or a message.";
  if (m.content.image_url && !/^https:\/\/\S+$/.test(m.content.image_url.trim())) return "The image address must start with https://.";
  for (const b of m.content.buttons ?? []) {
    if (!b.text.trim()) return "Every button needs a label.";
    if (b.action === "url") { const p = linkProblem(b.value); if (p) return `Button "${b.text}": ${p}`; }
  }
  if (!m.targeting.trigger_event.trim()) return "Choose the event that shows it.";
  const d = m.targeting.delay_seconds ?? 0;
  if (!Number.isInteger(d) || d < 0 || d > 600) return "The delay must be 0 to 600 seconds.";
  return scheduleProblem(m.start_at, m.end_at);
}

function useSegments() {
  return useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") }).data?.segments ?? [];
}

function SdkNote() {
  return (
    <p className="xs muted" style={{ marginTop: 10 }}>
      <strong>Show it in your app:</strong> with the Pulse Flutter SDK, give it your app's navigator key
      (<code>Pulse.instance.attachNavigator(navigatorKey)</code>) and handle links in <code>onInAppAction</code>. The SDK fetches live
      campaigns and shows each one when its trigger happens; call <code>track("app_open")</code> after start-up for app_open campaigns. On websites, use a <Link to="/on-site?channel=website">pop-up, bar or slide-in</Link> instead.
    </p>
  );
}

// ---------- editor ----------

const NEW_MESSAGE: InAppMessage = {
  id: 0, name: "", layout: "modal", priority: 0, status: "draft",
  content: { title: "", body: "", buttons: [{ text: "Got it", action: "dismiss" }], background: "#ffffff", text_color: "#152238", button_color: "#2747d6", position: "top" },
  targeting: { segment_ids: [], exclude_segment_ids: [], platforms: [], trigger_event: "app_open", frequency: "once" },
};

export function InAppEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const segments = useSegments();
  const events = useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names"), staleTime: 60_000 });
  const existing = useQuery({ queryKey: ["inapp", id], queryFn: () => api.get<InAppMessage>(`/v1/inapp/${id}`), enabled: !isNew });
  const [m, setM] = useState<InAppMessage | null>(isNew ? { ...NEW_MESSAGE, start_at: nowISO() } : null);
  useEffect(() => { if (existing.data && !m) setM(existing.data); }, [existing.data, m]);
  // Every edit replaces m: leaving with unsaved changes asks first.
  const changes = useChangeGuard(m);

  const save = useMutation({
    mutationFn: (msg: InAppMessage) => {
      const body: InAppMessage = {
        ...msg, end_at: msg.end_at ?? null,
        content: {
          ...msg.content,
          image_url: msg.content.image_url?.trim() || undefined,
          position: msg.layout === "banner" ? msg.content.position ?? "top" : undefined,
          buttons: (msg.content.buttons ?? []).map((b) => (b.action === "dismiss" ? { text: b.text, action: b.action } : { ...b, value: b.value?.trim() })),
        },
        targeting: { ...msg.targeting, screen: msg.targeting.trigger_event === "screen_view" ? msg.targeting.screen?.trim() || undefined : undefined },
      };
      return isNew ? api.post<{ id: number }>("/v1/inapp", body) : api.put<{ id: number }>(`/v1/inapp/${id}`, body);
    },
    onSuccess: (r, msg) => {
      qc.invalidateQueries({ queryKey: ["inapp"] });
      toast({ text: msg.status === "active" ? "Live. Apps pick it up the next time they fetch campaigns." : "Saved as draft" });
      // Keep the id the create call returned: the page stays mounted under its new address.
      const next = isNew ? { ...msg, id: r.id } : msg;
      changes.savedAs(next);
      setM(next);
      if (isNew) nav(`/on-site/in-app/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({ mutationFn: () => api.del(`/v1/inapp/${id}`), onSuccess: () => { changes.left(); qc.invalidateQueries({ queryKey: ["inapp"] }); toast({ text: "In-app message deleted" }); nav("/on-site?channel=app"); } });
  if (!m) return <div className="page"><ErrorNote error={existing.error} /></div>;

  const c = m.content, t = m.targeting;
  const setC = (p: Partial<InAppMessage["content"]>) => setM({ ...m, content: { ...c, ...p } });
  const setT = (p: Partial<InAppMessage["targeting"]>) => setM({ ...m, targeting: { ...t, ...p } });
  const buttons = c.buttons ?? [];
  const setButton = (i: number, p: Partial<InAppButton>) => setC({ buttons: buttons.map((b, j) => (j === i ? { ...b, ...p } : b)) });
  const problem = messageProblem(m);

  const platforms: Platform[] = t.platforms && t.platforms.length ? t.platforms : ["android", "ios"];
  const togglePlatform = (p: Platform, on: boolean) => {
    const next = on ? [...new Set([...platforms, p])] : platforms.filter((x) => x !== p);
    setT({ platforms: next.length === 2 ? [] : next });
  };
  const triggerSuggestions = [...new Set([...COMMON_TRIGGERS, ...(events.data?.names ?? [])])];

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/on-site?channel=app">Website &amp; app messages</Link>}
        title={isNew ? "New in-app message" : m.name || "In-app message"}
        actions={<>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => void askDelete(m.name ? `the in-app message ${m.name}` : "this in-app message",
            m.status === "active" ? "Apps stop showing it the next time they fetch campaigns. This can't be undone." : "This can't be undone.").then((ok) => ok && remove.mutate())}>Delete</button>}
          <button className="btn" disabled={save.isPending} onClick={() => {
            if (m.status !== "active") return save.mutate({ ...m, status: "draft" });
            void ask({ title: "Take this message back to draft?", body: "Apps stop showing it the next time they fetch campaigns. Your changes are kept in the draft.", confirmText: "Back to draft" })
              .then((ok) => ok && save.mutate({ ...m, status: "draft" }));
          }}>{m.status === "active" ? "Back to draft" : "Save draft"}</button>
          <button className="btn primary" disabled={save.isPending || !!problem} title={problem ?? undefined} onClick={() => save.mutate({ ...m, status: "active" })}>{m.status === "active" ? "Save live" : "Go live"}</button>
        </>}
      />
      <div className="ia-errors"><ErrorNote error={save.error ?? remove.error} /></div>
      {problem && <p className="xs ia-problem" style={{ marginTop: -6, marginBottom: 12 }}>To go live: {problem}</p>}
      <div className="editor-split" style={{ gridTemplateColumns: "minmax(0,1fr) 320px" }}>
        <div>
          <div className="grid-2">
            <Field label="Name"><input className="input" value={m.name} onChange={(e) => setM({ ...m, name: e.target.value })} placeholder="Welcome offer on first open" /></Field>
            <Field label="Layout" hint={LAYOUT_HINT[m.layout]}>
              <select className="select" value={m.layout} onChange={(e) => setM({ ...m, layout: e.target.value as Layout })}>
                {Object.entries(LAYOUT_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </Field>
          </div>

          <h3 style={{ margin: "6px 0 12px" }}>Message</h3>
          <PersonalizeScopeProvider value={{ channel: "inapp" }}>
          <Field label="Title" hint="Contact variables are filled for signed-in app users; anonymous visitors get the default."><TemplatedInput label="Title" value={c.title} onChange={(title) => setC({ title })} placeholder={'Hi {{contact.first_name | default:"there"}}, 20% off'} /></Field>
          <Field label="Message"><TemplatedText label="Message" value={c.body} onChange={(body) => setC({ body })} placeholder="Use code WELCOME20 at checkout." /></Field>
          </PersonalizeScopeProvider>
          <Field label="Image address (optional)" hint="A secure https:// link to a JPG, PNG or WebP.">
            <input className="input" value={c.image_url ?? ""} onChange={(e) => setC({ image_url: e.target.value })} placeholder="https://" />
          </Field>

          <div className="field">
            <span>Buttons (up to 2)</span>
            <div className="ia-buttons">
              {buttons.map((b, i) => (
                <div className="ia-button-row" key={i}>
                  <TemplatedInput value={b.text} onChange={(text) => setButton(i, { text })} placeholder="Label" aria-label={`Button ${i + 1} label`} />
                  <select className="select" value={b.action} onChange={(e) => setButton(i, { action: e.target.value as InAppButton["action"] })} aria-label={`Button ${i + 1} action`}>
                    <option value="url">Opens a link</option>
                    <option value="dismiss">Closes it</option>
                  </select>
                  {b.action === "url"
                    ? <input className="input" value={b.value ?? ""} onChange={(e) => setButton(i, { value: e.target.value })} placeholder="https://… or myapp://offers" aria-label={`Button ${i + 1} link`} />
                    : <span />}
                  <button type="button" className="btn ghost sm" onClick={() => setC({ buttons: buttons.filter((_, j) => j !== i) })}>Remove</button>
                </div>
              ))}
            </div>
            {buttons.length < 2 && (
              <div><button type="button" className="btn sm" onClick={() => setC({ buttons: [...buttons, { text: "", action: "url", value: "" }] })}>Add a button</button></div>
            )}
            <small>A link can be a web page (https://) or a deep link into your app (myapp://offers). Without buttons, people close it with the ×.</small>
          </div>

          <div className="row" style={{ marginBottom: 18 }}>
            {(["background", "text_color", "button_color"] as const).map((k) => (
              <label key={k} className="row small"><input type="color" value={c[k] || "#ffffff"} onChange={(e) => setC({ [k]: e.target.value })} /> {k === "background" ? "Background" : k === "text_color" ? "Text" : "Button"}</label>
            ))}
            {m.layout === "banner" && (
              <select className="select inline" value={c.position ?? "top"} onChange={(e) => setC({ position: e.target.value as "top" | "bottom" })} aria-label="Banner position">
                <option value="top">Top of the screen</option>
                <option value="bottom">Bottom of the screen</option>
              </select>
            )}
          </div>

          <h3 style={{ marginBottom: 12 }}>Who sees it, and when</h3>
          <SegmentPicker label="Show to members of any of" hint="Leave empty to show everyone using the app." segments={segments} value={t.segment_ids ?? []} onChange={(segment_ids) => setT({ segment_ids })} />
          <SegmentPicker label="Never show to members of" segments={segments} value={t.exclude_segment_ids ?? []} onChange={(exclude_segment_ids) => setT({ exclude_segment_ids })} />
          <div className="field">
            <span>Platforms</span>
            <div className="row">
              {(["android", "ios"] as const).map((p) => (
                <label key={p} className="check" style={{ marginBottom: 0 }}>
                  <input type="checkbox" checked={platforms.includes(p)} disabled={platforms.length === 1 && platforms[0] === p} onChange={(e) => togglePlatform(p, e.target.checked)} />
                  {PLATFORM_LABEL[p]}
                </label>
              ))}
            </div>
          </div>
          <div className="grid-2">
            <Field label="Show it when this happens" hint="An event your app sends, such as app_open or add_to_cart.">
              <input className="input code" list="inapp-event-names" value={t.trigger_event} onChange={(e) => setT({ trigger_event: e.target.value.trim() })} placeholder="app_open" />
            </Field>
            {t.trigger_event === "screen_view" ? (
              <Field label="On this screen (optional)" hint="The screen name your app reports. Leave empty for any screen.">
                <input className="input code" value={t.screen ?? ""} onChange={(e) => setT({ screen: e.target.value })} placeholder="cart" />
              </Field>
            ) : <div />}
          </div>
          <datalist id="inapp-event-names">{triggerSuggestions.map((name) => <option key={name} value={name} />)}</datalist>
          <div className="ia-suggest" style={{ marginTop: -8, marginBottom: 14 }}>
            {COMMON_TRIGGERS.filter((x) => x !== t.trigger_event).map((x) => <button type="button" key={x} onClick={() => setT({ trigger_event: x })}>{x}</button>)}
          </div>
          <div className="grid-2">
            <Field label="Wait before showing" hint="Seconds after the trigger, 0 to 600.">
              <input className="input" type="number" min={0} max={600} value={t.delay_seconds ?? 0} onChange={(e) => setT({ delay_seconds: e.target.value === "" ? 0 : Number(e.target.value) })} />
            </Field>
            <Field label="How often">
              <select className="select" value={t.frequency} onChange={(e) => setT({ frequency: e.target.value as InAppMessage["targeting"]["frequency"] })}>
                {Object.entries(FREQ_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </Field>
          </div>
          <Field label="Priority" hint="When several messages match at once, the highest number shows first.">
            <input className="input" type="number" style={{ maxWidth: 140 }} value={m.priority} onChange={(e) => setM({ ...m, priority: Math.trunc(Number(e.target.value) || 0) })} />
          </Field>

          <h3 style={{ margin: "6px 0 12px" }}>Schedule</h3>
          <ScheduleFields start={m.start_at} end={m.end_at} onChange={(w) => setM({ ...m, start_at: w.start_at, end_at: w.end_at ?? null })} startLabel="Show from" />
          <SdkNote />
          {!isNew && <TryIt id={Number(id)} />}
        </div>
        <aside style={{ position: "sticky", top: 20 }}>
          <p className="small muted" style={{ marginBottom: 8, textAlign: "center" }}>Preview: {LAYOUT_LABEL[m.layout].toLowerCase()}</p>
          <PhonePreview m={m} />
        </aside>
      </div>
    </div>
  );
}

// TryIt checks which live campaigns a contact would get, and sends this one to a contact's app as a test.
function TryIt({ id }: { id: number }) {
  const toast = useToast();
  const [ext, setExt] = useState("");
  const [platform, setPlatform] = useState<"android" | "ios">("android");
  const check = useMutation({
    mutationFn: () => api.get<{ messages: InAppMessage[] }>(`/v1/inapp/check?platform=${platform}&external_id=${encodeURIComponent(ext.trim())}`),
  });
  const test = useMutation({
    mutationFn: () => api.post(`/v1/inapp/${id}/test`, { external_id: ext.trim() }),
    onSuccess: () => toast({ text: "Queued: it shows the next time their app checks for messages (within 15 minutes)." }),
  });
  return (
    <section className="section">
      <h3>Try it with a contact</h3>
      <div className="row" style={{ alignItems: "flex-end", marginTop: 8 }}>
        <Field label="Contact external ID"><input className="input" value={ext} onChange={(e) => setExt(e.target.value)} placeholder="u-1042" /></Field>
        <Field label="Platform">
          <select className="select" value={platform} onChange={(e) => setPlatform(e.target.value as "android" | "ios")}><option value="android">Android</option><option value="ios">iOS</option></select>
        </Field>
        <button className="btn" style={{ marginBottom: 14 }} disabled={!ext.trim() || check.isPending} onClick={() => check.mutate()}>See what they get</button>
        <button className="btn" style={{ marginBottom: 14 }} disabled={!ext.trim() || test.isPending} onClick={() => test.mutate()}>Send a test</button>
      </div>
      <ErrorNote error={check.error ?? test.error} />
      {check.data && (
        <p className="small" style={{ marginTop: 6 }}>
          {check.data.messages.length === 0 ? "No live in-app message targets them right now." :
            <>They'd get, in order: {check.data.messages.map((x) => x.name).join(", ")}.{check.data.messages.some((x) => x.id === id) ? "" : " Not this one."}</>}
        </p>
      )}
      <p className="xs muted">A test shows once, even for a draft, whatever its targeting. Their app must be signed in as this contact with its user hash.</p>
    </section>
  );
}

function PhonePreview({ m }: { m: InAppMessage }) {
  // Variables shown with a sample contact (Asha), as an identified app user would see them.
  const fill = (x: string) => fillSample(x, { contact: SAMPLE_CONTACT });
  const c = { ...m.content, title: fill(m.content.title), body: fill(m.content.body), buttons: m.content.buttons?.map((b) => ({ ...b, text: fill(b.text) })) };
  const vars = { "--ia-bg": c.background || "#ffffff", "--ia-fg": c.text_color || "#152238", "--ia-btn": c.button_color || "#2747d6" } as React.CSSProperties;
  const buttons = (c.buttons ?? []).filter((b) => b.text.trim());
  const btns = buttons.length > 0 && (
    <div className="ia-btns">
      {buttons.map((b, i) => <span key={i} className={`ia-btn${buttons.length === 2 && i === 1 ? " secondary" : ""}`}>{b.text}</span>)}
    </div>
  );
  const img = c.image_url && /^https:\/\//.test(c.image_url) ? <img src={c.image_url} alt="" /> : null;
  const text = (
    <>
      <h4>{c.title || (c.body ? "" : "Your title")}</h4>
      {(c.body || !c.title) && <p>{c.body || "Your message"}</p>}
    </>
  );
  return (
    <div className="ia-phone" aria-label="Phone preview">
      <div className="ia-status"><span>9:41</span><span>●●● ▮</span></div>
      <div className="ia-app" aria-hidden>
        <i className="bar" /><i className="hero" /><i className="line" /><i className="line short" />
        <div className="tiles"><i /><i /><i /><i /></div>
      </div>
      {m.layout === "modal" && (
        <>
          <div className="ia-backdrop" />
          <div className="ia-msg ia-msg-modal" style={vars}>
            {img}
            <span className="ia-close" aria-hidden>×</span>
            <div className="ia-body">{text}{btns}</div>
          </div>
        </>
      )}
      {m.layout === "banner" && (
        <div className={`ia-msg ia-msg-banner ${c.position === "bottom" ? "ia-msg-bottom" : "ia-msg-top"}${img ? "" : " ia-msg-no-img"}`} style={vars}>
          {img}
          <div>{text}{btns}</div>
        </div>
      )}
      {m.layout === "fullscreen" && (
        <div className="ia-msg ia-msg-fullscreen" style={vars}>
          {img}
          <span className="ia-close" aria-hidden>×</span>
          <div className="ia-body">{text}{btns}</div>
        </div>
      )}
    </div>
  );
}

export function SegmentPicker({ label, hint, segments, value, onChange }: { label: string; hint?: string; segments: Segment[]; value: number[]; onChange: (v: number[]) => void }) {
  const rest = segments.filter((s) => !value.includes(s.id));
  return (
    <div className="field">
      <span>{label}</span>
      <div className="row">
        {value.map((id) => (
          <span key={id} className="badge info">{segments.find((s) => s.id === id)?.name ?? `Segment ${id}`}
            <button className="btn ghost sm" style={{ height: 18, padding: "0 2px" }} onClick={() => onChange(value.filter((v) => v !== id))} aria-label="Remove">×</button>
          </span>
        ))}
        {rest.length > 0 && (
          <select className="select inline" value="" onChange={(e) => e.target.value && onChange([...value, Number(e.target.value)])} aria-label={label}>
            <option value="">Add a segment</option>
            {rest.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        )}
      </div>
      {hint && <small>{hint}</small>}
    </div>
  );
}

// ---------- contact drawer: app inbox ----------

/** ContactInbox lists one contact's app inbox, newest first, for the contact drawer. */
export function ContactInbox({ externalId }: { externalId: string }) {
  const q = useQuery({
    queryKey: ["contact-inbox", externalId],
    queryFn: () => api.get<{ messages: InboxMessage[]; unread: number }>(`/v1/contacts/${encodeURIComponent(externalId)}/inbox`),
    refetchInterval: 10_000,
  });
  const ms = q.data?.messages ?? [];
  return (
    <section style={{ marginBottom: 22 }}>
      <h3 style={{ marginBottom: 10 }}>
        App inbox {q.data && q.data.unread > 0 && <span className="badge info" style={{ marginLeft: 6 }}>{n(q.data.unread)} unread</span>}
      </h3>
      <ErrorNote error={q.error} />
      {q.data && ms.length === 0 && <p className="muted small">Nothing in their app inbox.</p>}
      {ms.length > 0 && (
        <div>
          {ms.map((m) => (
            <div className="ia-inbox-item" key={m.id}>
              <span className={`ia-dot${m.read_at ? " read" : ""}`} aria-label={m.read_at ? "Read" : "Unread"} />
              <div style={{ minWidth: 0 }}>
                <strong>{m.title || "(no title)"}</strong>
                {m.body && <p>{m.body}</p>}
              </div>
              <span className="muted xs" style={{ whiteSpace: "nowrap", textAlign: "right" }}>
                {timeAgo(m.created_at)}<br />{m.read_at ? "Read" : "Unread"}
              </span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
