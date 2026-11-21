import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent } from "react";
import { api, can, type Asset, type AssetDetail, type AssetList as AssetListData, type Form } from "../api";
import { useMe } from "../auth";
import { Drawer, Empty, ErrorNote, Field, LiveNumber, PageHead, timeAgo, useToast, useCopy } from "../ui";
import { ask, askDelete, DISCARD } from "../guard";
import "./assets.css";

// ---------- pure helpers (tested in assets.test.ts) ----------

/** Human file size: "830 bytes", "830 KB", "2.4 MB", "1.2 GB". */
export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return `${bytes} ${bytes === 1 ? "byte" : "bytes"}`;
  const kb = bytes / 1024;
  if (kb < 1000) return `${Math.max(1, Math.round(kb))} KB`;
  const mb = kb / 1024;
  if (mb < 1000) return `${trim1(mb)} MB`;
  return `${trim1(mb / 1024)} GB`;
}
const trim1 = (n: number) => (n >= 100 ? String(Math.round(n)) : n.toFixed(1).replace(/\.0$/, ""));

/** Lower-cased extension with its dot (".pdf"), or "" when the name has none. */
export function fileExt(name: string): string {
  const i = name.lastIndexOf(".");
  return i > 0 && i < name.length - 1 ? name.slice(i).toLowerCase() : "";
}

/** The default title the server uses: the file name without its extension. */
export function titleFromFilename(name: string): string {
  const ext = fileExt(name);
  return ext ? name.slice(0, -ext.length) : name;
}

/** Client-side copy of the server's upload checks; null when the file looks acceptable. */
export function validateFile(file: { name: string; size: number }, maxSize: number, extensions: string[]): string | null {
  if (file.size === 0) return `${file.name} is empty.`;
  const ext = fileExt(file.name);
  const allowed = extensions.map((e) => e.toLowerCase());
  if (allowed.length && !allowed.includes(ext)) {
    return `${ext ? `${ext} files aren't` : "Files without an extension aren't"} allowed. Upload one of: ${extensions.join(", ")}.`;
  }
  if (maxSize > 0 && file.size > maxSize) {
    return `${file.name} is ${formatSize(file.size)}; the limit is ${formatSize(maxSize)}.`;
  }
  return null;
}

/** A short type label from the extension, e.g. "PDF", "PNG"; falls back to the MIME subtype. */
export function typeLabel(filename: string, contentType = ""): string {
  const ext = fileExt(filename).slice(1);
  if (ext) return ext.toUpperCase();
  const sub = contentType.split("/")[1]?.split(";")[0];
  return sub ? sub.toUpperCase() : "File";
}

const escapeHtml = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

/** The HTML link snippet offered in the drawer. */
export function downloadSnippet(url: string, title: string): string {
  return `<a href="${escapeHtml(url)}">Download ${escapeHtml(title)}</a>`;
}

// ---------- shared bits ----------


function useForms() {
  return useQuery({ queryKey: ["forms"], queryFn: () => api.get<{ forms: Form[]; base_url: string }>("/v1/forms") });
}

const DRAFT_NOTE = "(draft — downloads are blocked until it's active)";

function FormPicker({ forms, value, onChange, disabled, id }: { forms: Form[]; value: string; onChange: (v: string) => void; disabled?: boolean; id?: string }) {
  const known = forms.some((f) => String(f.id) === value);
  return (
    <select id={id} className="select" value={value} onChange={(e) => onChange(e.target.value)} disabled={disabled}>
      <option value="">Anyone with the link</option>
      {forms.map((f) => (
        <option key={f.id} value={String(f.id)}>{f.name}{f.status === "draft" ? ` ${DRAFT_NOTE}` : ""}</option>
      ))}
      {value && !known && <option value={value}>Form #{value}</option>}
    </select>
  );
}

// ---------- list ----------

export function AssetList() {
  const me = useMe();
  const canEdit = can(me.data?.permissions, "content", "edit");
  const q = useQuery({ queryKey: ["assets"], queryFn: () => api.get<AssetListData>("/v1/assets"), refetchInterval: 30_000 });
  const formsQ = useForms();
  const copy = useCopy();
  const [openId, setOpenId] = useState<number | null>(null);
  const pickRef = useRef<() => void>(() => {});

  const assets = q.data?.assets ?? [];
  const base = q.data?.base_url ?? "";
  const forms = formsQ.data?.forms ?? [];
  const formById = useMemo(() => new Map(forms.map((f) => [f.id, f])), [forms]);
  const close = useCallback(() => setOpenId(null), []);

  return (
    <div className="page">
      <PageHead title="Assets" intro="Files people can download from a link, behind a form if you like, and attach to emails." />
      <ErrorNote error={q.error} />
      {canEdit && q.data && (
        <Uploader limits={q.data} forms={forms} registerPick={(fn) => { pickRef.current = fn; }} onUploaded={(id) => setOpenId(id)} />
      )}
      <div className="panel as-list">
        {assets.length === 0 && !q.isLoading ? (
          <Empty
            title="No files yet"
            action={canEdit ? <button className="btn primary" onClick={() => pickRef.current()}>Upload a file</button> : undefined}
          >
            Upload a brochure, price list or e-book. Share its link openly or behind a form, and attach it to emails.
          </Empty>
        ) : (
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr><th>Title</th><th>File</th><th>Access</th><th className="num">Downloads</th><th>Added</th><th><span className="as-sr">Link</span></th></tr>
              </thead>
              <tbody>
                {assets.map((a) => {
                  const url = `${base}/a/${a.public_key}`;
                  return (
                    <tr key={a.id}>
                      <td><button className="as-link" onClick={() => setOpenId(a.id)}>{a.title || a.filename}</button></td>
                      <td>
                        <div className="as-file">
                          <span className="as-name" title={a.filename}>{a.filename}</span>
                          <span className="as-meta"><span className="badge">{typeLabel(a.filename, a.content_type)}</span> {formatSize(a.size)}</span>
                        </div>
                      </td>
                      <td><Gate asset={a} formById={formById} /></td>
                      <td className="num"><LiveNumber value={a.download_count} /></td>
                      <td className="muted as-nowrap">{timeAgo(a.created_at)}</td>
                      <td><button className="btn ghost sm" onClick={() => copy(url, "Link copied")} aria-label={`Copy link to ${a.title || a.filename}`}>Copy link</button></td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {openId !== null && (
        <AssetDrawer id={openId} forms={forms} canEdit={canEdit} onClose={close} />
      )}
    </div>
  );
}

function Gate({ asset, formById }: { asset: Asset; formById: Map<number, Form> }) {
  if (!asset.form_id) return <span className="muted">Open link</span>;
  const f = formById.get(asset.form_id);
  return (
    <span className="as-gate">
      <span>{f ? f.name : `Form #${asset.form_id}`}</span>
      {f?.status === "draft" && <span className="badge warn">blocked: form is a draft</span>}
    </span>
  );
}

// ---------- upload ----------

function Uploader({ limits, forms, registerPick, onUploaded }: {
  limits: AssetListData; forms: Form[]; registerPick: (fn: () => void) => void; onUploaded: (id: number) => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const inputRef = useRef<HTMLInputElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [formId, setFormId] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [over, setOver] = useState(false);
  const uploading = progress !== null;

  const pick = useCallback(() => inputRef.current?.click(), []);
  useEffect(() => registerPick(pick), [registerPick, pick]);
  useEffect(() => () => abortRef.current?.abort(), []);

  const accept = (f: File | undefined) => {
    if (!f) return;
    setError(null);
    const p = validateFile(f, limits.max_size, limits.extensions);
    setProblem(p);
    setFile(p ? null : f);
  };

  const reset = () => {
    setFile(null); setTitle(""); setDescription(""); setFormId(""); setProblem(null);
    if (inputRef.current) inputRef.current.value = "";
  };

  const upload = async () => {
    if (!file) return;
    const fd = new FormData();
    fd.append("file", file);
    if (title.trim()) fd.append("title", title.trim());
    if (description.trim()) fd.append("description", description.trim());
    if (formId) fd.append("form_id", formId);
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setError(null);
    setProgress(0);
    try {
      const res = await api.upload<{ id: number; asset: Asset; url: string }>("/v1/assets", fd, (f) => setProgress(f), ctrl.signal);
      toast({ text: `Uploaded ${res.asset?.title || file.name}` });
      reset();
      await qc.invalidateQueries({ queryKey: ["assets"] });
      onUploaded(res.id ?? res.asset.id);
    } catch (e) {
      if (ctrl.signal.aborted) toast({ text: "Upload cancelled" });
      else setError(e);
    } finally {
      abortRef.current = null;
      setProgress(null);
    }
  };

  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setOver(false);
    if (!uploading) accept(e.dataTransfer.files?.[0]);
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); if (!uploading) pick(); }
  };
  const pct = Math.round((progress ?? 0) * 100);
  const maxLabel = formatSize(limits.max_size);
  const selectedForm = forms.find((f) => String(f.id) === formId);

  return (
    <section className="panel as-upload" aria-labelledby="as-upload-h">
      <div className="panel-head"><h3 id="as-upload-h">Upload a file</h3></div>
      <div className="panel-body as-upload-body">
        <div>
          <div
            role="button"
            tabIndex={uploading ? -1 : 0}
            aria-disabled={uploading}
            aria-describedby="as-limits"
            aria-label={file ? `Selected ${file.name}. Choose a different file` : "Choose a file to upload, or drop one here"}
            className={`as-drop${over ? " over" : ""}${file ? " has-file" : ""}${uploading ? " busy" : ""}`}
            onClick={() => !uploading && pick()}
            onKeyDown={onKey}
            onDragOver={(e) => { e.preventDefault(); if (!uploading) setOver(true); }}
            onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(false); }}
            onDrop={onDrop}
          >
            {file ? (
              <>
                <span className="as-drop-title">{file.name}</span>
                <span className="as-drop-sub"><span className="badge">{typeLabel(file.name, file.type)}</span> {formatSize(file.size)} · click to choose another</span>
              </>
            ) : (
              <>
                <span className="as-drop-title">Drop a file here</span>
                <span className="as-drop-sub">or <span className="as-fake-btn">Choose file</span></span>
              </>
            )}
          </div>
          <input
            ref={inputRef}
            type="file"
            hidden
            accept={limits.extensions.join(",")}
            onChange={(e) => accept(e.target.files?.[0])}
          />
          <p id="as-limits" className="as-limits">
            Up to {maxLabel}. Allowed: {limits.extensions.join(" ")}
          </p>
          {problem && <div className="alert error" role="alert">{problem}</div>}
          <ErrorNote error={error} />
          {uploading && (
            <div className="as-progress-row">
              <div className="as-progress" role="progressbar" aria-label="Upload progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
                <div style={{ width: `${pct}%` }} />
              </div>
              <span className="as-pct">{pct < 100 ? `${pct}%` : "Checking…"}</span>
              <button className="btn sm" onClick={() => abortRef.current?.abort()}>Cancel</button>
            </div>
          )}
        </div>
        <div>
          <Field label="Title" hint="Optional. Defaults to the file name.">
            <input className="input" value={title} disabled={uploading} placeholder={file ? titleFromFilename(file.name) : "e.g. 2026 price list"} onChange={(e) => setTitle(e.target.value)} maxLength={200} />
          </Field>
          <Field label="Description" hint="Optional. Shown above the form for gated downloads.">
            <textarea className="textarea as-short" value={description} disabled={uploading} onChange={(e) => setDescription(e.target.value)} maxLength={2000} />
          </Field>
          <Field label="Who can download" hint={selectedForm?.status === "draft" ? "This form is a draft: downloads are blocked until it's active." : "Pick a form to ask for details before the download."}>
            <FormPicker forms={forms} value={formId} onChange={setFormId} disabled={uploading} />
          </Field>
          <div className="row">
            <button className="btn primary" disabled={!file || uploading} onClick={upload}>{uploading ? "Uploading…" : "Upload"}</button>
            {file && !uploading && <button className="btn ghost" onClick={reset}>Clear</button>}
          </div>
        </div>
      </div>
    </section>
  );
}

// ---------- detail drawer ----------

type Draft = { title: string; description: string; formId: string };

function AssetDrawer({ id, forms, canEdit, onClose }: { id: number; forms: Form[]; canEdit: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const copy = useCopy();
  const q = useQuery({ queryKey: ["asset", id], queryFn: () => api.get<AssetDetail>(`/v1/assets/${id}`) });
  const [draft, setDraft] = useState<Draft | null>(null);
  const a = q.data?.asset;

  useEffect(() => {
    if (a) setDraft({ title: a.title, description: a.description ?? "", formId: a.form_id ? String(a.form_id) : "" });
  }, [a?.id, a?.updated_at]); // eslint-disable-line react-hooks/exhaustive-deps

  const save = useMutation({
    mutationFn: (d: Draft) => api.put<unknown>(`/v1/assets/${id}`, {
      title: d.title.trim(), description: d.description.trim(), form_id: d.formId ? Number(d.formId) : null,
    }),
    onSuccess: () => {
      toast({ text: "Saved" });
      qc.invalidateQueries({ queryKey: ["assets"] });
      qc.invalidateQueries({ queryKey: ["asset", id] });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/assets/${id}`),
    onSuccess: () => {
      toast({ text: `Deleted ${a?.title || a?.filename || "the file"}` });
      qc.invalidateQueries({ queryKey: ["assets"] });
      qc.removeQueries({ queryKey: ["asset", id] });
      onClose();
    },
  });

  const dirty = !!(a && draft) && (draft.title !== a.title || draft.description !== (a.description ?? "") || draft.formId !== (a.form_id ? String(a.form_id) : ""));
  const gateForm = draft?.formId ? forms.find((f) => String(f.id) === draft.formId) : undefined;
  const stats = q.data?.stats;
  const url = q.data?.url ?? "";
  const templates = q.data?.templates ?? [];

  const onDelete = () => {
    if (!a) return;
    const used = templates.length > 0 ? ` ${templates.length} email template${templates.length === 1 ? "" : "s"} attach it and lose the attachment.` : "";
    void askDelete(a.title || a.filename, `Its link stops working right away.${used} This can't be undone.`).then((ok) => ok && remove.mutate());
  };
  // The drawer knows exactly what's unsaved (dirty), so it asks itself instead of the Drawer's typing check.
  const close = () => { if (!dirty) return onClose(); void ask(DISCARD).then((ok) => ok && onClose()); };

  return (
    <Drawer
      title={a ? a.title || a.filename : "File"}
      subtitle={a ? `${a.filename} · ${typeLabel(a.filename, a.content_type)} · ${formatSize(a.size)} · added ${timeAgo(a.created_at)}` : undefined}
      onClose={close}
      guard={false}
    >
      <ErrorNote error={q.error} />
      {q.isLoading && <p className="muted small">Loading…</p>}
      {a && draft && (
        <>
          <section className="as-stats" aria-label="Downloads">
            <div><strong><LiveNumber value={stats?.downloads ?? a.download_count} /></strong><span>Downloads</span></div>
            {stats?.known_downloads !== undefined && <div><strong><LiveNumber value={stats.known_downloads} /></strong><span>By known contacts</span></div>}
            {stats?.contacts !== undefined && <div><strong><LiveNumber value={stats.contacts} /></strong><span>Unique contacts</span></div>}
          </section>
          {(stats?.known_downloads !== undefined || stats?.contacts !== undefined) && (
            <p className="muted xs as-note">Known-contact figures count form-gated downloads, where the person filled in the form. Open-link downloads are anonymous and only count toward the total.</p>
          )}

          <form className="as-edit" onSubmit={(e) => { e.preventDefault(); if (canEdit && dirty && draft.title.trim()) save.mutate(draft); }}>
            <Field label="Title">
              <input className="input" value={draft.title} readOnly={!canEdit} required maxLength={200} onChange={(e) => setDraft({ ...draft, title: e.target.value })} />
            </Field>
            <Field label="Description" hint="Shown above the form for gated downloads.">
              <textarea className="textarea as-short" value={draft.description} readOnly={!canEdit} maxLength={2000} onChange={(e) => setDraft({ ...draft, description: e.target.value })} />
            </Field>
            <Field
              label="Who can download"
              hint={gateForm?.status === "draft"
                ? "This form is a draft, so downloads are blocked until you make it active."
                : draft.formId ? "People fill in the form, then get the file through a link that works for 15 minutes." : "Anyone with the link downloads the file directly."}
            >
              <FormPicker forms={forms} value={draft.formId} onChange={(v) => setDraft({ ...draft, formId: v })} disabled={!canEdit} />
            </Field>
            <ErrorNote error={save.error} />
            {canEdit && (
              <div className="row">
                <button className="btn primary" type="submit" disabled={!dirty || !draft.title.trim() || save.isPending}>{save.isPending ? "Saving…" : "Save"}</button>
                {dirty && <button className="btn ghost" type="button" onClick={() => setDraft({ title: a.title, description: a.description ?? "", formId: a.form_id ? String(a.form_id) : "" })}>Discard changes</button>}
              </div>
            )}
            <p className="muted xs as-note">The file itself can't be replaced. To change it, upload a new file.</p>
          </form>

          <section className="panel section">
            <div className="panel-head"><h3>Use it</h3></div>
            <div className="panel-body">
              <div className="field">
                <span>Download link</span>
                <div className="row">
                  <input className="input code as-grow" readOnly value={url} onFocus={(e) => e.target.select()} aria-label="Download link" />
                  <button className="btn" onClick={() => copy(url, "Link copied")}>Copy</button>
                </div>
                <small>{draft.formId ? "Opens the form first; the file follows after submission." : "Downloads the file right away."}</small>
              </div>
              <div className="field">
                <span>HTML link</span>
                <div className="as-snippet">
                  <pre aria-label="HTML link code">{downloadSnippet(url, a.title || a.filename)}</pre>
                  <button className="btn sm" onClick={() => copy(downloadSnippet(url, a.title || a.filename))}>Copy</button>
                </div>
              </div>
              <ul className="as-notes">
                <li><a href={`/v1/assets/${a.id}/file`} download={a.filename}>Download a copy</a> <span className="muted">(not counted as a download)</span></li>
                <li>Attach it to emails in the template editor (Attachments).</li>
                <li>Files are always delivered as downloads, never opened in the browser.</li>
              </ul>
            </div>
          </section>

          <section className="section">
            <h3 className="as-h">Email templates attaching it</h3>
            {templates.length === 0
              ? <p className="muted small">None yet.</p>
              : <ul className="as-templates">{templates.map((t) => <li key={t}>{t}</li>)}</ul>}
          </section>

          {canEdit && (
            <section className="section as-danger">
              <ErrorNote error={remove.error} />
              <button className="btn danger" onClick={onDelete} disabled={remove.isPending}>{remove.isPending ? "Deleting…" : "Delete file"}</button>
            </section>
          )}
        </>
      )}
    </Drawer>
  );
}
