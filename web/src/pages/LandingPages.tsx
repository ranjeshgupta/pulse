// Landing pages: hosted pages at {base_url}{slug}, built with the drag-and-drop builder or raw
// HTML, with embedded forms, a server-rendered preview, and view/visitor/submission stats.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiError, can, type AssetList, type Form, type LandingPage, type LandingPageDetail, type LandingPageList as PageListResponse, type PageStats } from "../api";
import { useMe } from "../auth";
import { describeWindow, nowISO, ScheduleFields, scheduleProblem } from "../schedule";
import { Empty, ErrorNote, Field, LiveNumber, PageHead, timeAgo, useToast } from "../ui";
import type { EmailBuilderHandle } from "./EmailBuilder";
import {
  assetLink, assetURL, conversion, formToken, insertAt, lastDays, META_MAX, pageState, pageURL, slugify, slugProblem,
  STATE_LABEL, STATE_TONE, TITLE_MAX, type PageState,
} from "./landingState";
import "./landing.css";

const EmailBuilder = lazy(() => import("./EmailBuilder"));

// The builder's own starter is an email; a new landing page starts from a simple hero instead.
const STARTER = `<section style="padding:72px 24px;background:#152238;color:#ffffff;text-align:center;font-family:Arial,Helvetica,sans-serif">
  <h1 style="margin:0 0 16px;font-size:40px;line-height:1.15;color:#ffffff">Your headline goes here</h1>
  <p style="margin:0 auto 28px;max-width:560px;font-size:18px;line-height:1.6;color:#c9d2e0">One or two sentences on what visitors get and why it matters. Drag blocks from the right to add images, columns and buttons.</p>
  <a href="#details" style="display:inline-block;background:#2747d6;color:#ffffff;padding:14px 26px;border-radius:6px;text-decoration:none;font-weight:bold">Find out more</a>
</section>
<section id="details" style="padding:56px 24px;background:#ffffff;color:#152238;font-family:Arial,Helvetica,sans-serif">
  <div style="max-width:640px;margin:0 auto">
    <h2 style="margin:0 0 12px;font-size:26px">Why it's worth it</h2>
    <p style="margin:0;font-size:16px;line-height:1.7;color:#3a4a66">Explain the offer here. To collect sign-ups, add a form: insert one with "Insert form" above.</p>
  </div>
</section>`;

const fmtDay = (d: string) => new Date(d.slice(0, 10) + "T00:00:00Z").toLocaleDateString("en-IN", { day: "numeric", month: "short", timeZone: "UTC" });

function useCopy() {
  const toast = useToast();
  return (text: string, what = "Copied") =>
    navigator.clipboard.writeText(text).then(() => toast({ text: what }), () => toast({ text: `Couldn't copy. Select it and copy it yourself: ${text}`, error: true }));
}

function StateBadge({ state, title }: { state: PageState; title?: string }) {
  return <span className={`badge ${STATE_TONE[state]}`} title={title}>{STATE_LABEL[state]}</span>;
}

// ---------- list ----------

export function LandingPageList() {
  const q = useQuery({ queryKey: ["landing-pages"], queryFn: () => api.get<PageListResponse>("/v1/landing-pages"), refetchInterval: 60_000 });
  const canEdit = can(useMe().data?.permissions, "content", "edit");
  const pages = q.data?.pages ?? [];
  const base = q.data?.base_url ?? "";
  const newBtn = canEdit ? <Link className="btn primary" to="/landing-pages/new">New page</Link> : undefined;
  return (
    <div className="page">
      <PageHead title="Landing pages" intro="Hosted pages for campaigns and offers, with your forms built in. Pulse counts views, visitors and sign-ups for each." actions={newBtn} />
      <ErrorNote error={q.error} />
      <div className="panel">
        {pages.length === 0 && !q.isLoading ? (
          <Empty title="No landing pages yet" action={canEdit ? <Link className="btn primary" to="/landing-pages/new">Create a page</Link> : undefined}>
            A page for one offer, with a sign-up form, is a good start. Share its link from emails, ads and social posts.
          </Empty>
        ) : (
          <div className="table-scroll">
            <table className="table">
              <thead><tr><th>Title</th><th>Address</th><th>State</th><th>Updated</th></tr></thead>
              <tbody>{pages.map(({ page: p, live }) => {
                const url = pageURL(base, p.slug);
                const state = pageState(p, live);
                return (
                  <tr key={p.id}>
                    <td><Link to={`/landing-pages/${p.id}`}>{p.title || p.slug}</Link></td>
                    <td className="lp-url-cell">{live ? <a href={url} target="_blank" rel="noreferrer">{url}</a> : <span className="muted">{url}</span>}</td>
                    <td><StateBadge state={state} title={p.status === "published" ? describeWindow(p.start_at, p.end_at) : "Not published"} /></td>
                    <td className="muted" style={{ whiteSpace: "nowrap" }}>{timeAgo(p.updated_at)}</td>
                  </tr>
                );
              })}</tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

// ---------- editor ----------

type Draft = {
  title: string; slug: string; slugAuto: boolean; meta_description: string; status: LandingPage["status"];
  start_at?: string; end_at?: string | null; html: string; builder: unknown | null;
};
type Mode = "builder" | "html";
type Width = "desktop" | "mobile";

function draftFrom(p: LandingPage): Draft {
  return {
    title: p.title, slug: p.slug, slugAuto: false, meta_description: p.meta_description ?? "", status: p.status,
    start_at: p.start_at, end_at: p.end_at ?? null, html: p.html ?? "", builder: p.builder ?? null,
  };
}

const newDraft = (): Draft => ({
  title: "", slug: "", slugAuto: true, meta_description: "", status: "draft", start_at: nowISO(), end_at: null, html: STARTER, builder: null,
});

function saveError(err: unknown): { message: string; slugTaken: boolean } | null {
  if (!err) return null;
  if (err instanceof ApiError && err.status === 409) return { message: "Another page already uses this address. Choose a different one.", slugTaken: true };
  return { message: err instanceof Error ? err.message : String(err), slugTaken: false };
}

export function LandingPageEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const copy = useCopy();
  const me = useMe().data;
  const canEdit = can(me?.permissions, "content", "edit");

  const existing = useQuery({ queryKey: ["landing-page", id], queryFn: () => api.get<LandingPageDetail>(`/v1/landing-pages/${id}`), enabled: !isNew });
  // New pages take the address base from the list.
  const list = useQuery({ queryKey: ["landing-pages"], queryFn: () => api.get<PageListResponse>("/v1/landing-pages"), enabled: isNew });
  const forms = useQuery({ queryKey: ["forms"], queryFn: () => api.get<{ forms: Form[]; base_url: string }>("/v1/forms"), enabled: canEdit }).data?.forms ?? [];
  const assetsQ = useQuery({ queryKey: ["assets"], queryFn: () => api.get<AssetList>("/v1/assets"), enabled: canEdit }).data;

  const [d, setD] = useState<Draft | null>(isNew ? newDraft() : null);
  const [mode, setMode] = useState<Mode>("builder");
  const [showPreview, setShowPreview] = useState(false);
  const builder = useRef<EmailBuilderHandle>(null);
  const ta = useRef<HTMLTextAreaElement>(null);
  const sel = useRef<{ start: number; end: number } | null>(null);

  useEffect(() => {
    if (existing.data && !d) {
      const p = existing.data.page;
      setD(draftFrom(p));
      setMode(p.builder ? "builder" : "html");
    }
  }, [existing.data, d]);
  // Viewers can't change the page: open on the preview.
  const viewOnly = !!me && !canEdit;
  useEffect(() => { if (viewOnly && d) setShowPreview(true); }, [viewOnly, d === null]); // eslint-disable-line react-hooks/exhaustive-deps

  // ---- preview (rendered by the server exactly as the live page, minus scripts) ----
  const [preview, setPreview] = useState<{ html: string; error: unknown; loading: boolean }>({ html: "", error: null, loading: false });
  const [width, setWidth] = useState<Width>("desktop");
  const seq = useRef(0);
  const previewOn = !!d && (mode === "html" || showPreview);
  const refreshPreview = (p: { title: string; meta_description: string; html: string }) => {
    const n = ++seq.current;
    setPreview((s) => ({ ...s, loading: true }));
    api.postText("/v1/landing-pages/preview", { page: p }).then(
      (html) => { if (n === seq.current) setPreview({ html, error: null, loading: false }); },
      (error) => { if (n === seq.current) setPreview((s) => ({ ...s, error, loading: false })); },
    );
  };
  useEffect(() => {
    if (!previewOn || !d) return;
    const t = setTimeout(() => refreshPreview({ title: d.title, meta_description: d.meta_description, html: d.html }), 450);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewOn, d?.title, d?.meta_description, d?.html]);

  const save = useMutation({
    mutationFn: (body: Omit<LandingPage, "id" | "created_at" | "updated_at">) =>
      isNew ? api.post<{ id: number; status: string; live: boolean }>("/v1/landing-pages", body) : api.put<{ id: number; status: string; live: boolean }>(`/v1/landing-pages/${id}`, body),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["landing-pages"] });
      qc.invalidateQueries({ queryKey: ["landing-page", String(r.id)] });
      setD((x) => (x ? { ...x, slugAuto: false } : x));
      toast({ text: r.status !== "published" ? "Saved as draft" : r.live ? "Saved. The page is live." : "Saved. It's published, but outside its schedule right now." });
      if (isNew) nav(`/landing-pages/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/landing-pages/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["landing-pages"] }); toast({ text: "Page deleted" }); nav("/landing-pages"); },
  });

  if (!d) return <div className="page"><ErrorNote error={existing.error} /></div>;

  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const base = existing.data?.base_url ?? list.data?.base_url ?? "";
  const builderMounted = mode === "builder" && !showPreview && canEdit;

  /** Pulls the builder's current state into the draft (it keeps its own state while mounted). */
  const readBuilder = (): Draft => {
    if (!builderMounted || !builder.current) return d;
    const { html, project } = builder.current.read();
    const nd = { ...d, html, builder: project };
    setD(nd);
    return nd;
  };

  const switchTo = (next: Mode | "preview") => {
    readBuilder();
    if (next === "preview") { setShowPreview(true); return; }
    setShowPreview(false);
    setMode(next);
  };

  const onSave = () => {
    const cur = readBuilder();
    save.mutate({
      slug: cur.slug.trim(), title: cur.title.trim(), meta_description: cur.meta_description.trim(), html: cur.html,
      builder: mode === "builder" ? cur.builder : null, status: cur.status, start_at: cur.start_at ?? nowISO(), end_at: cur.end_at || null,
    });
  };

  /** Inserts a snippet at the HTML cursor, or copies it for the builder. */
  const insert = (snippet: string, forBuilder: { copy: string; toast: string }) => {
    if (mode === "html") {
      const el = ta.current;
      const r = el && !showPreview ? { start: el.selectionStart, end: el.selectionEnd } : sel.current ?? { start: d.html.length, end: d.html.length };
      const { text, caret } = insertAt(d.html, r.start, r.end, snippet);
      setD({ ...d, html: text, builder: null });
      sel.current = { start: caret, end: caret };
      if (el && !showPreview) requestAnimationFrame(() => { el.focus(); el.setSelectionRange(caret, caret); });
      else toast({ text: "Inserted in the HTML" });
    } else {
      copy(forBuilder.copy, forBuilder.toast);
    }
  };

  const err = saveError(save.error);
  const slugErr = slugProblem(d.slug.trim());
  const blocker = !d.title.trim() ? "Give the page a title."
    : d.title.trim().length > TITLE_MAX ? `The title can be up to ${TITLE_MAX} characters.`
    : slugErr ?? (d.meta_description.length > META_MAX ? `The meta description can be up to ${META_MAX} characters.` : null)
    ?? (mode === "html" && !d.html.trim() ? "Add the page's HTML." : null)
    ?? scheduleProblem(d.start_at, d.end_at);
  const url = pageURL(base, d.slug.trim() || "your-page");
  const saved = existing.data;
  const savedState = saved ? pageState(saved.page, saved.live) : null;
  const assets = assetsQ?.assets ?? [];

  return (
    <div className="page lp-page">
      <PageHead
        back={<Link className="back" to="/landing-pages">Landing pages</Link>}
        title={<span className="lp-title">{isNew ? "New landing page" : d.title || "Untitled page"}{savedState && <StateBadge state={savedState} />}</span>}
        actions={canEdit ? <>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => confirm("Delete this page? Its address stops working right away. Stats already recorded stay.") && remove.mutate()}>Delete</button>}
          <button className="btn primary" disabled={!!blocker || save.isPending} title={blocker ?? undefined} onClick={onSave}>{save.isPending ? "Saving…" : "Save"}</button>
        </> : undefined}
      />
      {viewOnly && <div className="alert info">You can view this page and its stats, but your role can't change it.</div>}
      {err && <div className="alert error" role="alert">{err.message}</div>}
      <ErrorNote error={remove.error} />
      {canEdit && blocker && !err && <p className="xs muted lp-blocker">To save: {blocker}</p>}

      <fieldset className="lp-settings panel" disabled={viewOnly}>
        <legend className="lp-sr">Page settings</legend>
        <div className="panel-body">
          <div className="grid-2">
            <Field label="Title" hint="Shown in the browser tab and when the link is shared.">
              <input className="input" value={d.title} maxLength={TITLE_MAX + 50} placeholder="Diwali offer: 20% off"
                onChange={(e) => set({ title: e.target.value, ...(d.slugAuto ? { slug: slugify(e.target.value) } : {}) })} />
            </Field>
            <Field label="Address" hint={<>Lowercase letters, digits and dashes. <span className="lp-url-preview">{url}</span></>}>
              <input className={`input code${err?.slugTaken || (d.slug && slugErr) ? " lp-invalid" : ""}`} value={d.slug} placeholder="diwali-offer" spellCheck={false}
                aria-invalid={!!(err?.slugTaken || (d.slug && slugErr))}
                onChange={(e) => set({ slug: e.target.value.toLowerCase().replace(/\s+/g, "-"), slugAuto: false })} />
            </Field>
          </div>
          {d.slug && slugErr && <p className="lp-field-err">{slugErr}</p>}
          {err?.slugTaken && <p className="lp-field-err">That address is taken by another page.</p>}
          <Field label="Meta description" hint={<span className={d.meta_description.length > META_MAX ? "lp-over" : undefined}>{d.meta_description.length}/{META_MAX} characters. Search engines and link previews show it under the title.</span>}>
            <textarea className="textarea" style={{ minHeight: 64 }} value={d.meta_description} onChange={(e) => set({ meta_description: e.target.value })}
              placeholder="Get 20% off everything until 5 November. Sign up to get your code." />
          </Field>
          <div className="field">
            <span id="lp-status-label">Status</span>
            <div>
              <div className="lp-toggle" role="group" aria-labelledby="lp-status-label">
                <button type="button" aria-pressed={d.status === "draft"} onClick={() => set({ status: "draft" })}>Draft</button>
                <button type="button" className="live" aria-pressed={d.status === "published"} onClick={() => set({ status: "published" })}>Published</button>
              </div>
            </div>
            <small>{d.status === "published" ? "Visitors see it during the schedule below, once you save." : "Nobody can open it. Publish and save to put it online."}</small>
          </div>
          <ScheduleFields start={d.start_at} end={d.end_at} disabled={viewOnly} startLabel="Online from"
            onChange={(v) => set({ start_at: v.start_at, end_at: v.end_at ?? null })} />
        </div>
      </fieldset>

      <section className="section">
        <div className="lp-content-head">
          <div className="tabs lp-tabs" role="tablist" aria-label="Page content">
            {canEdit && <button role="tab" aria-selected={mode === "builder" && !showPreview} onClick={() => switchTo("builder")}>Drag and drop</button>}
            <button role="tab" aria-selected={mode === "html" && !showPreview} onClick={() => switchTo("html")}>HTML</button>
            <button role="tab" aria-selected={showPreview} onClick={() => switchTo("preview")}>Preview</button>
          </div>
          {canEdit && (
            <div className="lp-inserts">
              <label className="lp-sr" htmlFor="lp-insert-form">Insert form</label>
              <select id="lp-insert-form" className="select inline" value="" onChange={(e) => {
                const f = forms.find((x) => x.public_key === e.target.value);
                if (!f) return;
                const tok = formToken(f.public_key);
                insert(tok, { copy: tok, toast: `Copied ${tok}. Paste it into a text block where the form should go.` });
              }}>
                <option value="">Insert form…</option>
                {forms.length === 0 && <option value="" disabled>No forms yet</option>}
                {forms.map((f) => <option key={f.id} value={f.public_key}>{f.name}{f.status !== "active" ? " (draft: hidden until active)" : ""}</option>)}
              </select>
              <label className="lp-sr" htmlFor="lp-insert-asset">Insert download link</label>
              <select id="lp-insert-asset" className="select inline" value="" onChange={(e) => {
                const a = assets.find((x) => x.public_key === e.target.value);
                if (!a || !assetsQ) return;
                insert(assetLink(assetsQ.base_url, a), { copy: assetURL(assetsQ.base_url, a.public_key), toast: "Download address copied. Select text or a button in the builder and link it to this address." });
              }}>
                <option value="">Insert download link…</option>
                {assets.length === 0 && <option value="" disabled>No files uploaded yet</option>}
                {assets.map((a) => <option key={a.id} value={a.public_key}>{a.title || a.filename}</option>)}
              </select>
            </div>
          )}
        </div>

        {showPreview ? (
          <PreviewPane preview={preview} width={width} setWidth={setWidth} big
            onRefresh={() => refreshPreview({ title: d.title, meta_description: d.meta_description, html: d.html })} />
        ) : mode === "builder" && canEdit ? (
          <>
            <Suspense fallback={<div className="gjs-host" />}>
              <EmailBuilder ref={builder} html={d.html || STARTER} project={d.builder ?? undefined} />
            </Suspense>
            <p className="xs muted lp-note">Add a form by pasting its <code>{"{form:KEY}"}</code> token (from “Insert form”) into a text block. Check the result in the Preview tab.</p>
          </>
        ) : (
          <div className="lp-html-split">
            <div>
              <textarea ref={ta} className="textarea code-editor lp-code" value={d.html} readOnly={viewOnly} spellCheck={false} aria-label="Page HTML"
                onSelect={(e) => { sel.current = { start: e.currentTarget.selectionStart, end: e.currentTarget.selectionEnd }; }}
                onChange={(e) => setD({ ...d, html: e.target.value, builder: null })} />
              <p className="xs muted lp-note">
                Embed a form with <code>{"{form:KEY}"}</code> or <code>{'<div data-pulse-form="KEY"></div>'}</code>. When the page is served,
                Pulse removes <code>&lt;script&gt;</code> elements, inline event handlers (<code>onclick=…</code>), <code>javascript:</code> links,
                <code> &lt;base&gt;</code> and <code>&lt;meta http-equiv&gt;</code>: only Pulse's web SDK runs on the page.
              </p>
              {canEdit && saved?.page.builder != null && mode === "html" && <p className="xs muted">Saving from the HTML tab replaces the drag-and-drop layout with this HTML.</p>}
            </div>
            <PreviewPane preview={preview} width={width} setWidth={setWidth}
              onRefresh={() => refreshPreview({ title: d.title, meta_description: d.meta_description, html: d.html })} />
          </div>
        )}
      </section>

      {saved && id && (
        <div className="lp-below">
          <UseIt detail={saved} state={savedState!} />
          <Stats id={id} />
        </div>
      )}
    </div>
  );
}

// ---------- preview ----------

function PreviewPane({ preview, width, setWidth, onRefresh, big }: {
  preview: { html: string; error: unknown; loading: boolean }; width: Width; setWidth: (w: Width) => void; onRefresh: () => void; big?: boolean;
}) {
  return (
    <div className="lp-preview">
      <div className="lp-preview-bar">
        <span className="xs muted" aria-live="polite">{preview.loading ? "Updating preview…" : "Preview: as visitors see it, without scripts"}</span>
        <div className="row" style={{ gap: 6 }}>
          <div className="lp-toggle sm" role="group" aria-label="Preview width">
            <button type="button" aria-pressed={width === "desktop"} onClick={() => setWidth("desktop")}>Desktop</button>
            <button type="button" aria-pressed={width === "mobile"} onClick={() => setWidth("mobile")}>Mobile</button>
          </div>
          <button type="button" className="btn ghost sm" onClick={onRefresh}>Refresh</button>
        </div>
      </div>
      {preview.error != null && <ErrorNote error={preview.error} />}
      <div className={`lp-frame-wrap ${width}`}>
        <iframe className={`lp-frame${big ? " big" : ""}`} sandbox="" srcDoc={preview.html} title="Preview" />
      </div>
    </div>
  );
}

// ---------- use it ----------

function UseIt({ detail, state }: { detail: LandingPageDetail; state: PageState }) {
  const copy = useCopy();
  const p = detail.page;
  const attr = (s: string) => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
  const embed = `<iframe src="${detail.url}" style="width:100%;border:0;min-height:900px" title="${attr(p.title)}"></iframe>`;
  const why: Record<PageState, string> = {
    live: "",
    draft: "This page is a draft. The address works once you publish and save it.",
    scheduled: `Published, not online yet. ${describeWindow(p.start_at, p.end_at)}.`,
    ended: `Published, but its schedule is over (${describeWindow(p.start_at, p.end_at).replace(/^Ended /, "ended ")}). Change the end date to bring it back.`,
  };
  return (
    <section className="panel">
      <div className="panel-head"><h3>Use it</h3><StateBadge state={state} /></div>
      <div className="panel-body">
        {state !== "live" && <div className="alert info">{why[state]}</div>}
        <div className="field">
          <span>Page address</span>
          <div className="row">
            <input className="input code" style={{ flex: 1, minWidth: 200 }} readOnly value={detail.url} onFocus={(e) => e.target.select()} aria-label="Page address" />
            <button className="btn" onClick={() => copy(detail.url, "Address copied")}>Copy</button>
            {detail.live && <a className="btn" href={detail.url} target="_blank" rel="noreferrer">Open</a>}
          </div>
          <small>Link to it from emails, ads and social posts. Add <code>?utm_source=…&amp;utm_campaign=…</code> to see where visitors came from.</small>
        </div>
        <div className="field">
          <span>Embed it on another site</span>
          <div className="lp-snippet">
            <pre aria-label="Embed code">{embed}</pre>
            <button className="btn sm" onClick={() => copy(embed)}>Copy</button>
          </div>
          <small>Any site may show the page in an iframe.</small>
        </div>
        <p className="xs muted" style={{ marginBottom: 0 }}>
          Pulse's web SDK records each visit as <code>landing_page_viewed</code> (with the page and any <code>utm_*</code> values) and a page view.
          Anonymous visitors are counted only when the workspace tracks them (<Link to="/settings/data">Settings → Data &amp; privacy</Link>).
          A visitor who submits an embedded form becomes a known contact, and their earlier views are merged in.
        </p>
      </div>
    </section>
  );
}

// ---------- stats ----------

function Stats({ id }: { id: string }) {
  const q = useQuery({
    queryKey: ["landing-page-stats", id],
    queryFn: () => api.get<PageStats>(`/v1/landing-pages/${id}/stats`),
    refetchInterval: 10_000,
    retry: (n, e) => !(e instanceof ApiError && e.status === 503) && n < 2,
  });
  const unavailable = q.error instanceof ApiError && q.error.status === 503;
  const st = q.data;
  const byDay = new Map((st?.per_day ?? []).map((x) => [x.day.slice(0, 10), x]));
  const days = lastDays(30).map((day) => byDay.get(day) ?? { day, views: 0, visitors: 0, submissions: 0 });
  const max = Math.max(1, ...days.map((x) => x.views));
  const active = days.filter((x) => x.views || x.submissions).reverse();
  return (
    <section className="panel">
      <div className="panel-head"><h3>Stats</h3><span className="xs muted">Last 30 days</span></div>
      <div className="panel-body">
        {unavailable ? (
          <p className="small muted">Stats are unavailable right now. They'll show here when analytics are back.</p>
        ) : (
          <>
            <ErrorNote error={q.error} />
            <div className="figures lp-figures">
              <div><strong><LiveNumber value={st?.views ?? 0} /></strong><span>views</span></div>
              <div><strong><LiveNumber value={st?.visitors ?? 0} /></strong><span>unique visitors</span></div>
              <div><strong><LiveNumber value={st?.submissions ?? 0} /></strong><span>form submissions</span></div>
              <div><strong className="live-num">{conversion(st?.submissions ?? 0, st?.visitors ?? 0)}</strong><span>conversion</span></div>
            </div>
            {st && active.length === 0 ? (
              <p className="small muted" style={{ marginTop: 16 }}>No visits yet. They show up here within seconds.</p>
            ) : st && (
              <>
                <div className="lp-spark" role="img" aria-label={`Views per day over the last 30 days, up to ${max.toLocaleString("en-IN")} a day`}>
                  {days.map((x) => (
                    <div key={x.day} className="lp-spark-col" title={`${fmtDay(x.day)}: ${x.views} views, ${x.visitors} visitors, ${x.submissions} submissions`}>
                      <div className="lp-spark-bar" style={{ height: `${(x.views / max) * 100}%` }} />
                      {x.submissions > 0 && <div className="lp-spark-dot" />}
                    </div>
                  ))}
                </div>
                <div className="lp-spark-axis xs muted"><span>{fmtDay(days[0].day)}</span><span>Bars: views · dots: days with submissions</span><span>Today</span></div>
                <details className="lp-days">
                  <summary className="small">By day</summary>
                  <div className="table-scroll">
                    <table className="table">
                      <thead><tr><th>Day</th><th className="num">Views</th><th className="num">Visitors</th><th className="num">Submissions</th></tr></thead>
                      <tbody>{active.map((x) => (
                        <tr key={x.day}>
                          <td>{fmtDay(x.day)}</td>
                          <td className="num"><LiveNumber value={x.views} /></td>
                          <td className="num"><LiveNumber value={x.visitors} /></td>
                          <td className="num"><LiveNumber value={x.submissions} /></td>
                        </tr>
                      ))}</tbody>
                    </table>
                  </div>
                </details>
              </>
            )}
          </>
        )}
      </div>
    </section>
  );
}
