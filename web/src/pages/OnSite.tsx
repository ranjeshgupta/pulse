import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type DWCSlot, type FocusItem, type Segment } from "../api";
import { useMe } from "../auth";
import { RichText } from "../RichText";
import { nowISO, ScheduleFields, scheduleProblem } from "../schedule";
import { ErrorNote, Field, PageHead, useToast } from "../ui";

const KIND_LABEL = { modal: "Pop-up", bar: "Banner bar", slide: "Slide-in" };
const TRIGGER_LABEL: Record<string, string> = { load: "As the page loads", exit_intent: "When they're about to leave", scroll: "After scrolling", delay: "After a delay" };
const FREQ_LABEL: Record<string, string> = { once: "Once per visitor", session: "Once per visit", daily: "At most once a day", always: "On every matching page" };

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

  const save = useMutation({
    mutationFn: (item: FocusItem) => (isNew ? api.post<{ id: number }>("/v1/focus-items", item) : api.put<{ id: number }>(`/v1/focus-items/${id}`, item)),
    onSuccess: (r, item) => {
      qc.invalidateQueries({ queryKey: ["focus"] });
      toast({ text: item.status === "active" ? "Published. Visitors see it within seconds." : "Saved as draft" });
      if (isNew) nav(`/on-site/focus/${r.id}`, { replace: true });
      setF(item);
    },
  });
  const remove = useMutation({ mutationFn: () => api.del(`/v1/focus-items/${id}`), onSuccess: () => { qc.invalidateQueries({ queryKey: ["focus"] }); nav("/on-site?channel=website"); } });
  if (!f) return <div className="page"><ErrorNote error={existing.error} /></div>;
  const c = f.content, t = f.targeting;
  const setC = (p: Partial<FocusItem["content"]>) => setF({ ...f, content: { ...c, ...p } });
  const setT = (p: Partial<FocusItem["targeting"]>) => setF({ ...f, targeting: { ...t, ...p } });

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/on-site?channel=website">On-site &amp; in-app</Link>}
        title={isNew ? "New focus item" : f.name}
        actions={<>
          {!isNew && <button className="btn danger" onClick={() => confirm("Delete this focus item?") && remove.mutate()}>Delete</button>}
          <button className="btn" disabled={save.isPending} onClick={() => save.mutate({ ...f, status: "draft" })}>{f.status === "active" ? "Unpublish" : "Save draft"}</button>
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
          <SegmentPicker label="Show to members of any of" hint="Leave empty to show everyone, including anonymous visitors." segments={segments} value={t.segment_ids ?? []} onChange={(segment_ids) => setT({ segment_ids })} />
          <SegmentPicker label="Never show to members of" segments={segments} value={t.exclude_segment_ids ?? []} onChange={(exclude_segment_ids) => setT({ exclude_segment_ids })} />
          <Field label="Only on pages whose address contains" hint="Comma-separated, e.g. /pricing, /checkout. Leave empty for every page.">
            <input className="input" value={(t.url_contains ?? []).join(", ")} onChange={(e) => setT({ url_contains: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })} />
          </Field>
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

function SegmentPicker({ label, hint, segments, value, onChange }: { label: string; hint?: string; segments: Segment[]; value: number[]; onChange: (v: number[]) => void }) {
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

// ---------- dynamic web content ----------

export function DWCEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const segments = useSegments();
  const existing = useQuery({ queryKey: ["dwc", id], queryFn: () => api.get<DWCSlot>(`/v1/dwc/${id}`), enabled: !isNew });
  const [d, setD] = useState<DWCSlot | null>(isNew ? { id: 0, slot: "", name: "", default_html: "", variants: [], status: "draft", start_at: nowISO() } : null);
  useEffect(() => { if (existing.data && !d) setD(existing.data); }, [existing.data, d]);
  const save = useMutation({
    mutationFn: (s: DWCSlot) => (isNew ? api.post<{ id: number }>("/v1/dwc", s) : api.put<{ id: number }>(`/v1/dwc/${id}`, s)),
    onSuccess: (r, s) => {
      qc.invalidateQueries({ queryKey: ["dwc"] });
      toast({ text: s.status === "active" ? "Published" : "Saved as draft" });
      setD(s);
      if (isNew) nav(`/on-site/content/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({ mutationFn: () => api.del(`/v1/dwc/${id}`), onSuccess: () => { qc.invalidateQueries({ queryKey: ["dwc"] }); nav("/on-site?channel=website"); } });
  if (!d) return <div className="page"><ErrorNote error={existing.error} /></div>;
  const setVar = (i: number, p: Partial<DWCSlot["variants"][number]>) => setD({ ...d, variants: d.variants.map((v, j) => (j === i ? { ...v, ...p } : v)) });
  const move = (i: number, dir: -1 | 1) => {
    const vs = [...d.variants];
    [vs[i], vs[i + dir]] = [vs[i + dir], vs[i]];
    setD({ ...d, variants: vs });
  };

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/on-site?channel=website">On-site &amp; in-app</Link>}
        title={isNew ? "New content slot" : d.name}
        actions={<>
          {!isNew && <button className="btn danger" onClick={() => confirm("Delete this slot? The placeholder on your pages will stay empty.") && remove.mutate()}>Delete</button>}
          <button className="btn" onClick={() => save.mutate({ ...d, status: "draft" })}>{d.status === "active" ? "Unpublish" : "Save draft"}</button>
          <button className="btn primary" disabled={!!scheduleProblem(d.start_at, d.end_at)} onClick={() => save.mutate({ ...d, status: "active" })}>Publish</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error} />
      <div className="grid-2">
        <Field label="Name"><input className="input" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} placeholder="Homepage hero" /></Field>
        <Field label="Slot key" hint={<>Matches <code>{`data-pulse-slot="${d.slot || "home-hero"}"`}</code> on your page.</>}>
          <input className="input code" value={d.slot} onChange={(e) => setD({ ...d, slot: e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, "-") })} placeholder="home-hero" />
        </Field>
      </div>
      <h3 style={{ margin: "8px 0 4px" }}>Variants</h3>
      <p className="small muted" style={{ marginBottom: 12 }}>Checked top to bottom: a visitor sees the first variant whose segment they're in.</p>
      {d.variants.map((v, i) => (
        <div className="panel" key={i} style={{ marginBottom: 12 }}>
          <div className="panel-head">
            <select className="select inline" value={v.segment_id || ""} onChange={(e) => setVar(i, { segment_id: Number(e.target.value) })} aria-label="Segment">
              <option value="">Choose a segment</option>
              {segments.map((s) => <option key={s.id} value={s.id}>For members of "{s.name}"</option>)}
            </select>
            <div className="row">
              <button className="btn ghost sm" disabled={i === 0} onClick={() => move(i, -1)}>Move up</button>
              <button className="btn ghost sm" disabled={i === d.variants.length - 1} onClick={() => move(i, 1)}>Move down</button>
              <button className="btn ghost sm" onClick={() => setD({ ...d, variants: d.variants.filter((_, j) => j !== i) })}>Remove</button>
            </div>
          </div>
          <div className="panel-body">
            <RichText allowImages allowTables label={`Variant ${i + 1} content`} minHeight={140} value={v.html}
              onChange={(html) => setD((x) => x && { ...x, variants: x.variants.map((w, j) => (j === i ? { ...w, html } : w)) })} />
          </div>
        </div>
      ))}
      <button className="btn sm" onClick={() => setD({ ...d, variants: [...d.variants, { segment_id: 0, html: "" }] })}>Add a variant</button>
      <div className="section">
        <div className="field">
          <span>Everyone else sees</span>
          <RichText allowImages allowTables label="Everyone else sees" minHeight={140} value={d.default_html} placeholder="Leave empty to show nothing."
            onChange={(default_html) => setD((x) => x && { ...x, default_html })} />
          <small>Also shown to anonymous visitors. Leave empty to show nothing.</small>
        </div>
      </div>
      <h3 style={{ margin: "6px 0 12px" }}>Schedule</h3>
      <ScheduleFields start={d.start_at} end={d.end_at} onChange={(w) => setD({ ...d, start_at: w.start_at, end_at: w.end_at ?? null })} startLabel="Show from" />
      {!isNew && d.slot && <ContactCheck slot={d.slot} />}
      <SdkNote />
    </div>
  );
}
