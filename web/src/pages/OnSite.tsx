import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { chosenNote, pickerOption } from "../publishing";
import { useEffect, useMemo, useRef, useState, type DragEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, ApiError, type AttributeDef, type Device, type DWCShadow, type DWCSlot, type DWCVariant, type FocusItem, type OnsiteAudience, type OnsiteCondition, type Overlap, type Segment } from "../api";
import { useMe } from "../auth";
import {
  cleanAudience, CONDITION_OPS, conditionOp, conditionValueText, describeAudience, DEVICES, firstEveryone, isEveryone, localShadows, MAX_CONDITIONS,
  MAX_VARIANTS, moveBy, moveVariant, normaliseVariant, overlapPairs, overlapRows, parseConditionValue, setConditionOp, splitList,
} from "../onsiteVariants";
import { AppContentEditor, cardProblem, emptyCard } from "./AppCardEditor";
import { RichText } from "../RichText";
import { nowISO, ScheduleFields, scheduleProblem } from "../schedule";
import { ask, askDelete, useEditGuard } from "../guard";
import { ErrorNote, Field, PageHead, timeAgo, useToast } from "../ui";
import "./onsite-variants.css";
import { ContactCount } from "../ContactCount";
import { audienceFilters, overlapPairFilters, overlapSeesFilters, type SegmentAudience } from "../contactLinks";

const KIND_LABEL = { modal: "Pop-up", bar: "Banner bar", slide: "Slide-in" };
const TRIGGER_LABEL: Record<string, string> = { load: "As the page loads", exit_intent: "When they're about to leave", scroll: "After scrolling", delay: "After a delay" };
const FREQ_LABEL: Record<string, string> = { once: "Once per visitor", session: "Once per visit", daily: "At most once a day", always: "On every matching page" };

/** useChangeGuard: an editor page whose every edit replaces one state value (value). Any value other
 * than the first one or the last saved one is an unsaved change, and leaving then asks first.
 * Call savedAs(v) with the value a save leaves the page showing. */
export function useChangeGuard<T>(value: T | null) {
  const base = useRef<T | null>(null);
  const guard = useEditGuard();
  const { markDirty, saved } = guard;
  useEffect(() => {
    if (value === null) return;
    if (base.current === null) base.current = value;
    else if (value !== base.current) markDirty();
  }, [value, markDirty]);
  return { savedAs: (v: T | null) => { base.current = v; saved(); }, left: saved };
}

function useSegments() {
  return useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") }).data?.segments ?? [];
}

function SdkNote() {
  const key = useMe().data?.workspace.site_key;
  return (
    <p className="xs muted" style={{ marginTop: 10 }}>
      Shown on your website by the Pulse web SDK, which asks <code>POST /v1/web/decide</code> with your site key <code>{key}</code>.
    </p>
  );
}

// ContactCheck shows what the public decision API returns for one visitor on one page: the on-site
// equivalent of a test send. It uses the same site key the web SDK uses.
function ContactCheck({ slot, focusId }: { slot?: string; focusId?: number }) {
  const key = useMe().data?.workspace.site_key;
  const [ext, setExt] = useState("");
  const [url, setUrl] = useState("https://");
  const check = useMutation({
    mutationFn: async () => {
      const res = await fetch("/v1/web/decide", {
        method: "POST", headers: { "Content-Type": "application/json", "X-Pulse-Site-Key": key ?? "" },
        body: JSON.stringify({ external_id: ext.trim(), url, slots: slot ? [slot] : [] }),
      });
      if (!res.ok) throw new Error((await res.json()).error);
      return (await res.json()) as { focus: FocusItem[]; content: Record<string, string> };
    },
  });
  const result = check.data;
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Check what a visitor sees</h3><span className="xs muted">Only published, in-schedule content shows</span></div>
      <div className="panel-body">
        <div className="grid-2">
          <Field label="Contact's external ID" hint="Leave empty for an anonymous visitor."><input className="input" value={ext} onChange={(e) => setExt(e.target.value)} /></Field>
          <Field label="Page address"><input className="input" value={url} onChange={(e) => setUrl(e.target.value)} /></Field>
        </div>
        <button className="btn" disabled={check.isPending} onClick={() => check.mutate()}>Check</button>
        <ErrorNote error={check.error} />
        {result && focusId !== undefined && (
          <p className="small" style={{ marginTop: 12 }}>{result.focus.some((f) => f.id === focusId) ? "✓ This visitor sees this focus item on that page." : "This visitor doesn't see this focus item there (targeting, schedule or status)."}</p>
        )}
        {result && slot && (
          result.content[slot] === undefined
            ? <p className="small" style={{ marginTop: 12 }}>The slot is empty for this visitor (not published or outside its schedule).</p>
            : <div style={{ marginTop: 12 }}><p className="small muted" style={{ marginBottom: 6 }}>They see:</p><iframe title="Slot content" sandbox="" className="preview-frame" style={{ height: 140 }} srcDoc={result.content[slot]} /></div>
        )}
      </div>
    </section>
  );
}

// ---------- focus items ----------

const NEW_FOCUS: FocusItem = {
  id: 0, name: "", kind: "modal", status: "draft",
  content: { title: "", body_html: "", cta_text: "", cta_url: "", background: "#ffffff", text_color: "#152238", button_color: "#2747d6", position: "bottom" },
  targeting: { segment_ids: [], exclude_segment_ids: [], url_contains: [], trigger: "exit_intent", frequency: "once" },
};

export function FocusEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const segments = useSegments();
  const existing = useQuery({ queryKey: ["focus", id], queryFn: () => api.get<FocusItem>(`/v1/focus-items/${id}`), enabled: !isNew });
  const [f, setF] = useState<FocusItem | null>(isNew ? { ...NEW_FOCUS, start_at: nowISO() } : null);
  useEffect(() => { if (existing.data && !f) setF(existing.data); }, [existing.data, f]);
  const changes = useChangeGuard(f);

  const save = useMutation({
    mutationFn: (item: FocusItem) => (isNew ? api.post<{ id: number }>("/v1/focus-items", item) : api.put<{ id: number }>(`/v1/focus-items/${id}`, item)),
    onSuccess: (r, item) => {
      qc.invalidateQueries({ queryKey: ["focus"] });
      toast({ text: item.status === "active" ? "Published. Visitors see it within seconds." : "Saved as draft" });
      // Keep the id the create call returned: the page stays mounted under its new address.
      const next = isNew ? { ...item, id: r.id } : item;
      changes.savedAs(next);
      setF(next);
      if (isNew) nav(`/on-site/focus/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({ mutationFn: () => api.del(`/v1/focus-items/${id}`), onSuccess: () => { changes.left(); qc.invalidateQueries({ queryKey: ["focus"] }); toast({ text: "Focus item deleted" }); nav("/on-site?channel=website"); } });
  if (!f) return <div className="page"><ErrorNote error={existing.error} /></div>;
  const c = f.content, t = f.targeting;
  const setC = (p: Partial<FocusItem["content"]>) => setF({ ...f, content: { ...c, ...p } });
  const setT = (p: Partial<FocusItem["targeting"]>) => setF({ ...f, targeting: { ...t, ...p } });

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/on-site?channel=website">Website &amp; app messages</Link>}
        title={isNew ? "New focus item" : f.name}
        actions={<>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => void askDelete(f.name ? `the focus item ${f.name}` : "this focus item",
            f.status === "active" ? "Visitors stop seeing it right away, and its stats go too. This can't be undone." : "This can't be undone.").then((ok) => ok && remove.mutate())}>Delete</button>}
          <button className="btn" disabled={save.isPending} onClick={() => {
            if (f.status !== "active") return save.mutate({ ...f, status: "draft" });
            void ask({ title: "Unpublish this focus item?", body: "Visitors stop seeing it within seconds. It's kept as a draft, with your changes.", confirmText: "Unpublish" })
              .then((ok) => ok && save.mutate({ ...f, status: "draft" }));
          }}>{f.status === "active" ? "Unpublish" : "Save draft"}</button>
          <button className="btn primary" disabled={save.isPending || !!scheduleProblem(f.start_at, f.end_at)} onClick={() => save.mutate({ ...f, status: "active" })}>Publish</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error} />
      <div className="editor-split" style={{ gridTemplateColumns: "minmax(0,1fr) 440px" }}>
        <div>
          <div className="grid-2">
            <Field label="Name"><input className="input" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="Exit offer for new visitors" /></Field>
            <Field label="Type">
              <select className="select" value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value as FocusItem["kind"] })}>
                {Object.entries(KIND_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </Field>
          </div>
          <Field label="Headline"><input className="input" value={c.title} onChange={(e) => setC({ title: e.target.value })} /></Field>
          <div className="field">
            <span>Body</span>
            <RichText compact allowImages label="Body" minHeight={110} value={c.body_html} placeholder="Get 20% off your first order."
              onChange={(body_html) => setF((x) => x && { ...x, content: { ...x.content, body_html } })} />
            <small>Shown on your website as-is. Use Source for your own HTML.</small>
          </div>
          <div className="grid-2">
            <Field label="Button text"><input className="input" value={c.cta_text ?? ""} onChange={(e) => setC({ cta_text: e.target.value })} /></Field>
            <Field label="Button link"><input className="input" value={c.cta_url ?? ""} onChange={(e) => setC({ cta_url: e.target.value })} placeholder="https://" /></Field>
          </div>
          <div className="row" style={{ marginBottom: 18 }}>
            {(["background", "text_color", "button_color"] as const).map((k) => (
              <label key={k} className="row small"><input type="color" value={c[k] ?? "#ffffff"} onChange={(e) => setC({ [k]: e.target.value })} /> {k === "background" ? "Background" : k === "text_color" ? "Text" : "Button"}</label>
            ))}
            {f.kind === "bar" && <select className="select inline" value={c.position ?? "top"} onChange={(e) => setC({ position: e.target.value })} aria-label="Bar position"><option value="top">Top of page</option><option value="bottom">Bottom of page</option></select>}
          </div>

          <h3 style={{ marginBottom: 12 }}>Who sees it, and when</h3>
          <AudienceEditor value={t} onChange={setT} segments={segments} />
          <p className="xs muted" style={{ margin: "-6px 0 14px" }}>
            Your website shows one pop-up, one banner bar and one slide-in at a time: the first in the <Link to="/on-site?channel=website&amp;type=focus">order on the list</Link> that the visitor can still see.
          </p>
          <div className="grid-2">
            <Field label="Show it">
              <select className="select" value={t.trigger} onChange={(e) => setT({ trigger: e.target.value, trigger_value: e.target.value === "scroll" ? 50 : e.target.value === "delay" ? 5 : undefined })}>
                {Object.entries(TRIGGER_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </Field>
            {(t.trigger === "scroll" || t.trigger === "delay") && (
              <Field label={t.trigger === "scroll" ? "Percent of page scrolled" : "Seconds on page"}>
                <input className="input" type="number" min={1} max={t.trigger === "scroll" ? 100 : 600} value={t.trigger_value ?? ""} onChange={(e) => setT({ trigger_value: Number(e.target.value) })} />
              </Field>
            )}
          </div>
          <Field label="How often">
            <select className="select" value={t.frequency} onChange={(e) => setT({ frequency: e.target.value })}>
              {Object.entries(FREQ_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
          <h3 style={{ margin: "6px 0 12px" }}>Schedule</h3>
          <ScheduleFields start={f.start_at} end={f.end_at} onChange={(w) => setF({ ...f, start_at: w.start_at, end_at: w.end_at ?? null })} startLabel="Show from" />
          {!isNew && <ContactCheck focusId={f.id} />}
          <SdkNote />
        </div>
        <aside style={{ position: "sticky", top: 20 }}>
          <p className="small muted" style={{ marginBottom: 8 }}>Preview on a page</p>
          <div className="site-preview">
            {f.kind === "modal" && <div className="site-preview-backdrop" />}
            <div
              className={`fi ${f.kind}-kind`}
              style={{
                ...({ "--fi-bg": c.background, "--fi-fg": c.text_color, "--fi-btn": c.button_color } as Record<string, string | undefined>),
                ...(f.kind === "bar" ? (c.position === "bottom" ? { bottom: 0 } : { top: 0 }) : {}),
              }}
            >
              <div>
                <strong>{c.title || "Your headline"}</strong>
                {/* Authored HTML is previewed in a sandboxed frame so its scripts never run inside Pulse. */}
                {c.body_html && <iframe title="Body preview" sandbox="" srcDoc={`<style>p,ul,ol,h1,h2,h3{margin:0 0 4px}</style><body style="margin:0;font:14px system-ui;color:${c.text_color}">${c.body_html}</body>`} style={{ border: 0, width: "100%", height: f.kind === "bar" ? 22 : 60, display: "block", marginTop: 4 }} />}
              </div>
              {c.cta_text && <span className="cta">{c.cta_text}</span>}
            </div>
          </div>
        </aside>
      </div>
    </div>
  );
}

function SegmentPicker({ label, hint, segments, value, taken = [], onChange }: { label: string; hint?: string; segments: Segment[]; value: number[]; taken?: number[]; onChange: (v: number[]) => void }) {
  const rest = segments.filter((s) => !value.includes(s.id) && !taken.includes(s.id));
  return (
    <div className="field">
      <span>{label}</span>
      <div className="row">
        {value.map((id) => (
          <span key={id} className={`badge ${chosenNote(segments, id) ? "warn" : "info"}`} title={chosenNote(segments, id) ? `This segment is ${chosenNote(segments, id)}: Pulse isn't updating who's in it` : undefined}>{segments.find((s) => s.id === id)?.name ?? `Segment ${id}`}{chosenNote(segments, id) ? ` (${chosenNote(segments, id)})` : ""}
            <button className="btn ghost sm" style={{ height: 18, padding: "0 2px" }} onClick={() => onChange(value.filter((v) => v !== id))} aria-label="Remove">×</button>
          </span>
        ))}
        {rest.length > 0 && (
          <select className="select inline" value="" onChange={(e) => e.target.value && onChange([...value, Number(e.target.value)])} aria-label={label}>
            <option value="">Add a segment</option>
            {rest.map((s) => { const o = pickerOption(s); return <option key={s.id} value={s.id} disabled={o.disabled}>{o.label}</option>; })}
          </select>
        )}
      </div>
      {hint && <small>{hint}</small>}
    </div>
  );
}

// ---------- audience rules (shared by focus items and content variants) ----------

/** PageRules edits a comma-separated list of page-address fragments, keeping what's typed as typed. */
function PageRules({ label, hint, value, onChange }: { label: string; hint: string; value: string[]; onChange: (v: string[]) => void }) {
  const [text, setText] = useState(value.join(", "));
  useEffect(() => {
    if (splitList(text).join("\u0000") !== value.join("\u0000")) setText(value.join(", "));
  }, [value]); // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <Field label={label} hint={hint}>
      <input className="input" value={text} placeholder="/sale, /pricing" onChange={(e) => { setText(e.target.value); onChange(splitList(e.target.value)); }} />
    </Field>
  );
}

function DeviceChecks({ value, onChange }: { value: Device[]; onChange: (v: Device[]) => void }) {
  return (
    <div className="field" role="group" aria-label="Devices">
      <span>On these devices</span>
      <div className="ov-devices">
        {DEVICES.map((d) => (
          <label key={d.value} className="row small">
            <input type="checkbox" checked={value.includes(d.value)}
              onChange={(e) => onChange(e.target.checked ? DEVICES.map((x) => x.value).filter((x) => x === d.value || value.includes(x)) : value.filter((x) => x !== d.value))} />
            {d.label}
          </label>
        ))}
      </div>
      <small>Leave all unticked for any device.</small>
    </div>
  );
}

function useFieldKeys() {
  const q = useQuery({ queryKey: ["attributes"], queryFn: () => api.get<{ attributes: AttributeDef[]; undefined: string[] }>("/v1/attributes"), staleTime: 60_000, retry: false });
  return useMemo(() => [...(q.data?.attributes ?? []).map((a) => a.key), ...(q.data?.undefined ?? [])].filter((k, i, a) => a.indexOf(k) === i), [q.data]);
}

/** ConditionRows edits contact-field rules (all must match), with the ops of segment attribute rules. */
function ConditionRows({ value, onChange }: { value: OnsiteCondition[]; onChange: (v: OnsiteCondition[]) => void }) {
  const keys = useFieldKeys();
  const setAt = (i: number, c: OnsiteCondition) => onChange(value.map((x, j) => (j === i ? c : x)));
  return (
    <div className="field" role="group" aria-label="Contact fields">
      <span>Whose contact fields match</span>
      <datalist id="ov-field-keys">{keys.map((k) => <option key={k} value={k} />)}</datalist>
      {value.map((c, i) => {
        const op = conditionOp(c.op);
        return (
          <div className="ov-cond" key={i}>
            {i > 0 && <span className="xs muted">and</span>}
            <input className="input" list="ov-field-keys" value={c.prop} placeholder="city" aria-label={`Rule ${i + 1} field`} onChange={(e) => setAt(i, { ...c, prop: e.target.value.trim() })} />
            <select className="select" value={c.op} aria-label={`Rule ${i + 1} comparison`} onChange={(e) => setAt(i, setConditionOp(c, e.target.value))}>
              {CONDITION_OPS.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
            </select>
            {op.value !== "none" && (
              <input className="input" value={conditionValueText(c)} aria-label={`Rule ${i + 1} value`} placeholder={op.value === "list" ? "Pune, Mumbai" : op.value === "number" ? "100" : "Pune"}
                onChange={(e) => setAt(i, { ...c, value: op.value === "list" ? e.target.value.split(",").map((x) => x.trimStart()) : parseConditionValue(c.op, e.target.value) })}
                onBlur={() => op.value === "list" && setAt(i, { ...c, value: parseConditionValue(c.op, conditionValueText(c)) })} />
            )}
            <button type="button" className="btn ghost sm" aria-label={`Remove rule ${i + 1}`} onClick={() => onChange(value.filter((_, j) => j !== i))}>×</button>
          </div>
        );
      })}
      {value.length < MAX_CONDITIONS && (
        <div><button type="button" className="btn ghost sm" onClick={() => onChange([...value, { prop: "", op: "eq", value: "" }])}>+ Contact field rule</button></div>
      )}
      <small>Only for visitors your site identifies with a verified user hash. Anonymous or unverified visitors never match these rules.</small>
    </div>
  );
}

/** AudienceEditor edits who sees something: segments in and out, pages, devices and contact fields. */
function AudienceEditor({ value, onChange, segments, segmentsHint = "Leave empty to show everyone, including anonymous visitors.", app = false }: {
  value: OnsiteAudience; onChange: (p: Partial<OnsiteAudience>) => void; segments: Segment[]; segmentsHint?: string;
  /** app: an app slot, where page addresses and device types don't apply. */
  app?: boolean;
}) {
  return (
    <div className="ov-audience">
      <SegmentPicker label="Show to members of any of" hint={segmentsHint} segments={segments} value={value.segment_ids ?? []} taken={value.exclude_segment_ids} onChange={(segment_ids) => onChange({ segment_ids })} />
      <SegmentPicker label="Never show to members of" segments={segments} value={value.exclude_segment_ids ?? []} taken={value.segment_ids} onChange={(exclude_segment_ids) => onChange({ exclude_segment_ids })} />
      {!app && <>
        <div className="grid-2">
          <PageRules label="Only on pages whose address contains" hint="Comma-separated, e.g. /pricing, /checkout. Leave empty for every page." value={value.url_contains ?? []} onChange={(url_contains) => onChange({ url_contains })} />
          <PageRules label="Not on pages whose address contains" hint="Comma-separated. These pages never show it." value={value.url_not_contains ?? []} onChange={(url_not_contains) => onChange({ url_not_contains })} />
        </div>
        <DeviceChecks value={value.devices ?? []} onChange={(devices) => onChange({ devices })} />
      </>}
      <ConditionRows value={value.conditions ?? []} onChange={(conditions) => onChange({ conditions })} />
    </div>
  );
}

// ---------- overlap ----------

const overlapError = (e: unknown) => (e instanceof ApiError && e.status === 429 ? new Error("Too many counts right now. Try again in a minute.") : e);
const n = (x: number) => x.toLocaleString("en-IN");

/** OverlapView shows how an ordered set of audiences splits your known contacts. */
export function OverlapView({ o, fallbackNames = [], noun = "variant", stale, audiences }: {
  o: Overlap; fallbackNames?: string[]; noun?: "variant" | "item"; stale?: boolean;
  /** The audiences counted, in order (M7: numbers then open their contacts; not while stale). */
  audiences?: SegmentAudience[];
}) {
  const rows = overlapRows(o, fallbackNames);
  const pairs = overlapPairs(o, fallbackNames);
  const auds = stale ? undefined : audiences;
  return (
    <div className="ov-result">
      {stale && <div className="alert info">You've changed the {noun}s since this count. Check again to update it.</div>}
      <div className="table-scroll">
        <table className="table">
          <thead><tr><th>{noun === "variant" ? "Variant (in order)" : "Item (in order)"}</th><th className="num">Matching contacts</th><th className="num">Would see this</th></tr></thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.index}>
                <td>{r.label}{r.approximate && <span className="faint" title="Page, device and contact-field rules aren't counted"> *</span>}
                  {r.hidden && <span className="badge warn" style={{ marginLeft: 6 }}>Never shown</span>}</td>
                <td className="num"><ContactCount value={r.matching} filters={auds?.[r.index] ? audienceFilters(auds[r.index]) : null} /></td>
                <td className="num"><ContactCount value={r.sees} filters={auds?.[r.index] ? overlapSeesFilters(auds, r.index) : null} /></td>
              </tr>
            ))}
            {noun === "variant" && <tr><td className="muted">Everyone else (default content)</td><td className="num muted">–</td><td className="num">{o.default_never_shown ? 0 : <ContactCount value={o.default} filters={auds ? overlapSeesFilters([...auds, {}], auds.length) : null} />}</td></tr>}
          </tbody>
        </table>
      </div>
      <ul className="ov-facts small">
        <li><strong>{n(o.multi)}</strong> contacts match more than one {noun} — they see the first one in the order.</li>
        <li><strong>{n(o.default_never_shown ? 0 : o.default)}</strong> {noun === "variant" ? "see the default content" : "match none of these"}{o.default_never_shown && noun === "variant" ? " (a variant above takes everyone)" : ""}.</li>
        <li>Out of <strong>{n(o.contacts)}</strong> known contacts.</li>
      </ul>
      {pairs.length > 0 && (
        <>
          <p className="small" style={{ margin: "12px 0 6px" }}><strong>Shared contacts</strong></p>
          <ul className="ov-pairs small">
            {pairs.map((p) => <li key={`${p.a}|${p.b}`}><span>{p.a}</span> and <span>{p.b}</span>: <strong><ContactCount value={p.both} filters={auds?.[p.ai] && auds[p.bi] ? overlapPairFilters(auds[p.ai], auds[p.bi]) : null} /></strong></li>)}
          </ul>
        </>
      )}
      {o.approximate && (
        <p className="xs muted" style={{ marginTop: 10 }}>
          * Approximate: counts use segments only. Page, device and contact-field rules depend on the visit, so they aren't counted.
        </p>
      )}
      <p className="xs muted" style={{ marginTop: 6 }}>Counted {timeAgo(o.at)}{o.cached ? " (from the last minute's count)" : ""}. Counts are of known contacts; anonymous visitors aren't included.</p>
    </div>
  );
}

// ---------- dynamic web content ----------

/** Collapsible: a <details> whose first state is chosen once (later prop changes don't fold it). */
function Collapsible({ title, startOpen, children }: { title: string; startOpen: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(startOpen);
  return (
    <details className="ov-rules" open={open} onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary>{title}</summary>
      {open && children}
    </details>
  );
}

let keySeq = 0;
const newKey = () => `k${++keySeq}`;

type SaveResp = { id: number; variants: DWCVariant[]; shadowed: DWCShadow[] | null; default_never_shown: boolean };

const toPayload = (vs: DWCVariant[]): DWCVariant[] =>
  vs.map((v) => ({ ...(v.id ? { id: v.id } : {}), ...(v.name?.trim() ? { name: v.name.trim() } : {}), audience: cleanAudience(v.audience), html: v.html,
    ...(v.card && !emptyCard(v.card) ? { card: v.card } : {}) }));

export function DWCEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const segments = useSegments();
  const segNames = useMemo(() => new Map(segments.map((s) => [s.id, s.name])), [segments]);
  const existing = useQuery({ queryKey: ["dwc", id], queryFn: () => api.get<DWCSlot>(`/v1/dwc/${id}`), enabled: !isNew });
  const [search] = useSearchParams();
  const [d, setD] = useState<DWCSlot | null>(isNew ? { id: 0, slot: "", name: "", platform: search.get("platform") === "app" ? "app" : "web", default_html: "", variants: [], status: "draft", start_at: nowISO() } : null);
  // Stable keys keep each card's editor with its variant while the list is reordered.
  const [keys, setKeys] = useState<string[]>([]);
  // The server's shadow report (from loading or saving) holds until the variants change.
  const [server, setServer] = useState<{ shadowed: DWCShadow[]; defaultNeverShown: boolean } | null>(null);
  const [rev, setRev] = useState(0);
  const [drag, setDrag] = useState<{ from: number; over: number } | null>(null);
  useEffect(() => {
    if (existing.data && !d) {
      const vs = (existing.data.variants ?? []).map(normaliseVariant);
      setD({ ...existing.data, variants: vs });
      setKeys(vs.map(newKey));
      setServer({ shadowed: existing.data.shadowed ?? [], defaultNeverShown: !!existing.data.default_never_shown });
    }
  }, [existing.data, d]);
  // Every edit (typing, adding, removing or reordering variants) replaces d.
  const changes = useChangeGuard(d);

  const save = useMutation({
    mutationFn: (s: DWCSlot) => {
      const body = { ...s, variants: toPayload(s.variants), default_card: s.default_card && !emptyCard(s.default_card) ? s.default_card : null };
      delete body.shadowed;
      delete body.default_never_shown;
      return isNew ? api.post<SaveResp>("/v1/dwc", body) : api.put<SaveResp>(`/v1/dwc/${id}`, body);
    },
    onSuccess: (r, s) => {
      qc.invalidateQueries({ queryKey: ["dwc"] });
      toast({ text: s.status === "active" ? "Published" : "Saved as draft" });
      const vs = (r.variants ?? s.variants).map(normaliseVariant);
      // Keep the id the create call returned: the page stays mounted under its new address.
      const next = { ...s, id: isNew ? r.id : s.id, variants: vs };
      changes.savedAs(next);
      setD(next);
      setServer({ shadowed: r.shadowed ?? [], defaultNeverShown: !!r.default_never_shown });
      if (isNew) nav(`/on-site/content/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({ mutationFn: () => api.del(`/v1/dwc/${id}`), onSuccess: () => { changes.left(); qc.invalidateQueries({ queryKey: ["dwc"] }); toast({ text: "Content slot deleted" }); nav(`/on-site?channel=${d?.platform === "app" ? "app" : "website"}`); } });
  const overlap = useMutation({
    mutationFn: async ({ vs, at }: { vs: DWCVariant[]; at: number }) => {
      try {
        return { o: await api.post<Overlap>("/v1/dwc/overlap", { variants: toPayload(vs) }), rev: at };
      } catch (e) {
        throw overlapError(e);
      }
    },
  });

  const auds = useMemo(() => (d?.variants ?? []).map((v) => cleanAudience(v.audience)), [d?.variants]);
  if (!d) return <div className="page"><ErrorNote error={existing.error} /></div>;
  const app = d.platform === "app";
  const cardIssue = [d.default_card, ...d.variants.map((v) => v.card)].map(cardProblem).find(Boolean) ?? "";
  const hasDefault = d.default_html.trim() !== "" || !emptyCard(d.default_card);

  // Every change to the variants goes through here: the server's shadow report no longer applies.
  const setVariants = (vs: DWCVariant[], ks: string[] = keys) => {
    setD((x) => x && { ...x, variants: vs });
    setKeys(ks);
    setServer(null);
    setRev((r) => r + 1);
  };
  const setVar = (i: number, p: Partial<DWCVariant>) => setVariants(d.variants.map((v, j) => (j === i ? { ...v, ...p } : v)));
  const setAud = (i: number, p: Partial<OnsiteAudience>) => setVar(i, { audience: { ...d.variants[i].audience, ...p } });
  const move = (from: number, to: number) => setVariants(moveVariant(d.variants, from, to), moveVariant(keys, from, to));
  const step = (i: number, dir: -1 | 1) => setVariants(moveBy(d.variants, i, dir), moveBy(keys, i, dir));
  const removeAt = (i: number) => setVariants(d.variants.filter((_, j) => j !== i), keys.filter((_, j) => j !== i));
  const add = () => setVariants([...d.variants, { name: "", audience: {}, html: "" }], [...keys, newKey()]);

  const local = localShadows(auds);
  const report = server ?? local;
  const shadowOf = new Map(report.shadowed.map((s) => [s.index, s]));
  const everyoneAt = firstEveryone(auds);

  const onDragStart = (i: number) => (e: DragEvent) => {
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("application/x-pulse-variant", String(i)); // not text: the editors would paste it
    const card = (e.currentTarget as HTMLElement).closest(".ov-card");
    if (card) e.dataTransfer.setDragImage(card, 20, 20);
    setDrag({ from: i, over: i });
  };
  const onDragOver = (i: number) => (e: DragEvent) => {
    if (!drag) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    if (drag.over !== i) setDrag({ ...drag, over: i });
  };
  const onDrop = (i: number) => (e: DragEvent) => {
    if (!drag) return;
    e.preventDefault();
    e.stopPropagation(); // capture phase: the card's rich text editor never sees the drop
    move(drag.from, i);
    setDrag(null);
  };

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to={`/on-site?channel=${app ? "app" : "website"}`}>Website &amp; app messages</Link>}
        title={isNew ? (app ? "New app content slot" : "New content slot") : d.name}
        actions={<>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => void askDelete(d.name ? `the slot ${d.name}` : "this slot",
            `${app ? "The place in your app stays empty." : "The placeholder on your pages stays empty."} This can't be undone.`,
            d.status === "active" ? d.name || undefined : undefined).then((ok) => ok && remove.mutate())}>Delete</button>}
          <button className="btn" disabled={save.isPending} onClick={() => {
            if (d.status !== "active") return save.mutate({ ...d, status: "draft" });
            void ask({ title: "Unpublish this slot?", body: app ? "The place in your app shows nothing from Pulse within seconds. It's kept as a draft, with your changes." : "The placeholder on your pages stays empty within seconds. It's kept as a draft, with your changes.", confirmText: "Unpublish" })
              .then((ok) => ok && save.mutate({ ...d, status: "draft" }));
          }}>{d.status === "active" ? "Unpublish" : "Save draft"}</button>
          <button className="btn primary" disabled={save.isPending || !!scheduleProblem(d.start_at, d.end_at) || !!cardIssue} onClick={() => save.mutate({ ...d, status: "active" })}>Publish</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error} />
      <div className="grid-2">
        <Field label="Name"><input className="input" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} placeholder="Homepage hero" /></Field>
        <Field label="Slot key" hint={app
          ? <>Your app shows it with <code>{`PulseSlot(id: '${d.slot || "home_banner"}')`}</code> (Flutter), <code>PulseSlotView</code> (Android, iOS).</>
          : <>Matches <code>{`data-pulse-slot="${d.slot || "home-hero"}"`}</code> on your page.</>}>
          <input className="input code" value={d.slot} onChange={(e) => setD({ ...d, slot: e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, "-") })} placeholder={app ? "home_banner" : "home-hero"} />
        </Field>
      </div>
      <Field label="Where">
        <div className="seg-toggle sm" role="radiogroup" aria-label="Where" style={{ width: "fit-content" }}>
          <button role="radio" aria-checked={!app} aria-pressed={!app} disabled={!isNew && app} onClick={() => setD({ ...d, platform: "web", default_card: null, variants: d.variants.map((v) => ({ ...v, card: null })) })}>Website</button>
          <button role="radio" aria-checked={app} aria-pressed={app} disabled={!isNew && !app} onClick={() => setD({ ...d, platform: "app" })}>App</button>
        </div>
      </Field>
      {cardIssue && <div className="alert error">{cardIssue}</div>}
      <h3 style={{ margin: "8px 0 4px" }}>Variants</h3>
      <p className="small muted" style={{ marginBottom: 12 }}>
        Checked top to bottom: a visitor sees the first variant they match, and everyone else sees the default content at the bottom. Drag a variant by its handle, or use the arrows, to change the order.
      </p>
      <ol className="ov-list" aria-label="Variants in order">
        {d.variants.map((v, i) => {
          const shadow = shadowOf.get(i);
          const cls = ["ov-card", "panel", drag?.from === i ? "dragging" : "", drag && drag.over === i && drag.from !== i ? (drag.from < i ? "drop-after" : "drop-before") : "", shadow ? "shadowed" : ""].filter(Boolean).join(" ");
          return (
            <li key={keys[i] ?? i} className={cls} onDragOverCapture={onDragOver(i)} onDropCapture={onDrop(i)} onDragEnd={() => setDrag(null)}>
              <div className="panel-head ov-card-head">
                <span className="ov-handle" draggable onDragStart={onDragStart(i)} title="Drag to reorder" aria-hidden="true">⋮⋮</span>
                <span className="ov-num" aria-label={`Priority ${i + 1}`}>{i + 1}</span>
                <input className="input ov-name" value={v.name ?? ""} placeholder={`Variant ${i + 1}`} aria-label={`Variant ${i + 1} name`} maxLength={80} onChange={(e) => setVar(i, { name: e.target.value })} />
                <div className="row" style={{ gap: 2, flexWrap: "nowrap" }}>
                  <button type="button" className="btn ghost sm" aria-label={`Move variant ${i + 1} up`} disabled={i === 0} onClick={() => step(i, -1)}>↑</button>
                  <button type="button" className="btn ghost sm" aria-label={`Move variant ${i + 1} down`} disabled={i === d.variants.length - 1} onClick={() => step(i, 1)}>↓</button>
                  <button type="button" className="btn ghost sm" onClick={() => removeAt(i)}>Remove</button>
                </div>
              </div>
              <div className="panel-body">
                <p className="small ov-who"><strong>Who sees it:</strong> {describeAudience(auds[i], segNames)}{i > 0 && !shadow ? ", unless they match a variant above" : ""}.</p>
                {shadow && <div className="alert error" role="alert">{shadow.note}</div>}
                {everyoneAt === i && (i < d.variants.length - 1 || hasDefault) && (
                  <div className="alert info">This variant shows to everyone, so {i < d.variants.length - 1 ? "the variants below it and " : ""}the default content are never shown. Add a rule, or move it to the bottom.</div>
                )}
                <Collapsible title="Audience rules" startOpen={!v.id || isEveryone(v.audience)}>
                  <AudienceEditor app={app} value={v.audience} onChange={(p) => setAud(i, p)} segments={segments} segmentsHint="Leave empty for everyone who matches the other rules." />
                </Collapsible>
                {app ? (
                  <AppContentEditor label={`Variant ${i + 1} content`} card={v.card} html={v.html}
                    onChange={(p) => setD((x) => x && { ...x, variants: x.variants.map((w, j) => (j === i ? { ...w, ...p } : w)) })} />
                ) : (
                  <RichText allowImages allowTables label={`Variant ${i + 1} content`} minHeight={140} value={v.html}
                    onChange={(html) => setD((x) => x && { ...x, variants: x.variants.map((w, j) => (j === i ? { ...w, html } : w)) })} />
                )}
              </div>
            </li>
          );
        })}
      </ol>
      <div className="row">
        <button className="btn sm" disabled={d.variants.length >= MAX_VARIANTS} onClick={add}>Add a variant</button>
        {d.variants.length >= MAX_VARIANTS && <span className="xs muted">A slot can have up to {MAX_VARIANTS} variants.</span>}
      </div>
      <div className={`panel ov-card ov-default${report.defaultNeverShown ? " shadowed" : ""}`} style={{ marginTop: 14 }}>
        <div className="panel-head ov-card-head"><span className="ov-num">∗</span><strong>Everyone else</strong></div>
        <div className="panel-body">
          {report.defaultNeverShown && d.variants.length > 0 && (
            hasDefault
              ? <div className="alert error" role="alert">Never shown: {everyoneAt >= 0 ? `variant ${everyoneAt + 1}` : "a variant above"} shows to everyone. Add a rule to it to show this content to the rest.</div>
              : <p className="xs muted" style={{ marginBottom: 8 }}>Not used: {everyoneAt >= 0 ? `variant ${everyoneAt + 1}` : "a variant above"} shows to everyone.</p>
          )}
          {app ? (
            <AppContentEditor label="Everyone else sees" card={d.default_card} html={d.default_html}
              onChange={(p) => setD((x) => x && { ...x, ...(p.card !== undefined ? { default_card: p.card } : {}), ...(p.html !== undefined ? { default_html: p.html } : {}) })} />
          ) : (
            <RichText allowImages allowTables label="Everyone else sees" minHeight={140} value={d.default_html} placeholder="Leave empty to show nothing."
              onChange={(default_html) => setD((x) => x && { ...x, default_html })} />
          )}
          <small className="muted">{app ? "App users" : "Visitors"} who match no variant, including anonymous ones. Leave empty to show nothing.</small>
        </div>
      </div>

      <section className="panel section ov-overlap">
        <div className="panel-head">
          <h3>Overlap</h3>
          <button className="btn sm" disabled={overlap.isPending || d.variants.length === 0} onClick={() => overlap.mutate({ vs: d.variants, at: rev })}>{overlap.isPending ? "Counting…" : "Check overlap"}</button>
        </div>
        <div className="panel-body">
          {!overlap.data && !overlap.error && (
            <p className="small muted">{d.variants.length === 0 ? "Add a variant to see who would see it." : "See how many of your contacts match each variant, how many match more than one, and who ends up with the default — before you publish."}</p>
          )}
          <ErrorNote error={overlap.error} />
          {overlap.data && <OverlapView o={overlap.data.o} fallbackNames={d.variants.map((v) => v.name ?? "")} stale={overlap.data.rev !== rev} audiences={d.variants.map((v) => v.audience ?? {})} />}
        </div>
      </section>

      <h3 style={{ margin: "6px 0 12px" }}>Schedule</h3>
      <ScheduleFields start={d.start_at} end={d.end_at} onChange={(w) => setD({ ...d, start_at: w.start_at, end_at: w.end_at ?? null })} startLabel="Show from" />
      {!isNew && d.slot && !app && <ContactCheck slot={d.slot} />}
      {app ? <AppSdkNote slot={d.slot || "home_banner"} /> : <SdkNote />}
    </div>
  );
}

/** AppSdkNote: how an app shows a slot, in each SDK. */
function AppSdkNote({ slot }: { slot: string }) {
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Show it in your app</h3></div>
      <div className="panel-body small">
        <p style={{ marginTop: 0 }}>Put the slot where it should appear. The SDK fetches it, draws the card (or hands it to your own view), and records <code>app_content_viewed</code> and <code>app_content_clicked</code>.</p>
        <pre className="code">{`// Flutter
PulseSlot(id: '${slot}')

// Android (Kotlin)
findViewById<PulseSlotView>(R.id.banner).slotId = "${slot}"

// iOS (SwiftUI)
PulseSlot(id: "${slot}")`}</pre>
      </div>
    </section>
  );
}
