import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, type Import, type Segment } from "../api";
import { useCan } from "../auth";
import { CORE_TARGETS, guessTarget, headerEnd, mappingProblems, parseRows, remapped } from "../importMapping";
import { Empty, ErrorNote, LiveNumber, PageHead, timeAgo, useToast } from "../ui";
import { useAttributes, useSegments } from "./segmentData";
import "./imports.css";

export const MAX_IMPORT_BYTES = 10 * 1024 ** 3;

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export type UploadProgress = { loaded: number; total: number };

/** uploadImport sends a CSV to a background import, reporting upload progress. The import keeps
 * running on the server once the upload finishes; abort() stops the upload itself. */
export function uploadImport(file: Blob, filename: string, listId: number | undefined, onProgress?: (p: UploadProgress) => void) {
  const xhr = new XMLHttpRequest();
  const promise = new Promise<{ id: number; status: string; bytes: number }>((resolve, reject) => {
    const qs = new URLSearchParams({ filename });
    if (listId) qs.set("list_id", String(listId));
    xhr.open("POST", `/v1/imports?${qs}`);
    xhr.withCredentials = true;
    xhr.setRequestHeader("X-Pulse-CSRF", "1");
    xhr.setRequestHeader("Content-Type", "text/csv");
    xhr.upload.onprogress = (e) => onProgress?.({ loaded: e.loaded, total: e.lengthComputable ? e.total : file.size });
    xhr.onload = () => {
      let data: { id?: number; status?: string; bytes?: number; error?: string } = {};
      try { data = JSON.parse(xhr.responseText || "{}"); } catch { /* not JSON */ }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as { id: number; status: string; bytes: number });
      else reject(new Error(data.error ?? `Upload failed (${xhr.status})`));
    };
    xhr.onerror = () => reject(new Error("The upload was interrupted. Check your connection and try again."));
    xhr.onabort = () => reject(new Error("Upload cancelled."));
    xhr.send(file);
  });
  return { promise, abort: () => xhr.abort() };
}

const ACTIVE: Import["status"][] = ["uploading", "queued", "running"];
const STATUS: Record<Import["status"], [string, string]> = {
  uploading: ["Uploading", "info"], queued: ["Waiting", "info"], running: ["Importing", "warn"],
  done: ["Done", "ok"], failed: ["Failed", "bad"], cancelled: ["Cancelled", ""],
};

function took(i: Import): string {
  if (!i.started_at || !i.finished_at) return "";
  const s = Math.max(1, Math.round((new Date(i.finished_at).getTime() - new Date(i.started_at).getTime()) / 1000));
  return s < 90 ? `${s} s` : s < 5400 ? `${Math.round(s / 60)} min` : `${(s / 3600).toFixed(1)} h`;
}

/** ImportPage is the one import flow: upload, map columns, choose a static segment, review, run.
 * Every import runs in the background, so all of them show in one history below. */
export function ImportPage() {
  const canImport = useCan("contacts", "edit");
  const [params] = useSearchParams();
  const [uploading, setUploading] = useState(false);
  const q = useQuery({
    queryKey: ["imports"],
    queryFn: () => api.get<{ imports: Import[] }>("/v1/imports"),
    refetchInterval: (query) => (uploading || query.state.data?.imports.some((i) => ACTIVE.includes(i.status)) ? 2000 : false),
  });
  const segs = useSegments(0).data?.segments ?? [];
  const statics = segs.filter((s) => s.kind === "static");
  const segName = new Map(segs.map((l) => [l.id, l.name]));
  const imports = q.data?.imports ?? [];

  // Contacts and segments change as imports finish.
  const qc = useQueryClient();
  const finished = imports.filter((i) => i.status === "done").length;
  const seen = useRef(finished);
  useEffect(() => {
    if (finished !== seen.current) {
      seen.current = finished;
      qc.invalidateQueries({ queryKey: ["contacts"] });
      qc.invalidateQueries({ queryKey: ["segments"] });
    }
  }, [finished, qc]);

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/contacts">Contacts</Link>}
        title="Import contacts"
        intro="Upload a CSV of up to 10 GB, choose what each column becomes and, if you like, a static segment to add everyone to. The import runs in the background: you can leave once the upload finishes."
      />
      {canImport && <ImportWizard statics={statics} initialSegment={Number(params.get("segment")) || 0} onUploading={setUploading} />}
      <section className="section">
        <h3>Import history</h3>
        <ErrorNote error={q.error} />
        <div className="panel">
          {imports.length === 0 && !q.isLoading ? (
            <Empty title="No imports yet">Files you import show up here with their progress and any rows that were skipped.</Empty>
          ) : (
            <ImportTable imports={imports} listName={segName} canCancel={canImport} />
          )}
        </div>
      </section>
    </div>
  );
}

const STEPS = ["Upload", "Map columns", "Static segment", "Review"] as const;
type Head = { name: string; end: number; headings: string[]; samples: string[][] };

/** readHead reads the header row and a few sample rows from the start of the file. */
async function readHead(f: File): Promise<Head> {
  const bytes = new Uint8Array(await f.slice(0, 256 * 1024).arrayBuffer());
  const end = headerEnd(bytes);
  if (end < 0) throw new Error("Couldn't find the end of the first row. Is this a CSV file with a header row?");
  const rows = parseRows(new TextDecoder().decode(bytes), 6);
  if (!rows.length || rows[0].every((h) => !h.trim())) throw new Error("The first row should name the columns.");
  return { name: f.name, end, headings: rows[0], samples: rows.slice(1) };
}

function ImportWizard({ statics, initialSegment, onUploading }: { statics: Segment[]; initialSegment: number; onUploading: (b: boolean) => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const attrs = useAttributes().data;
  const [step, setStep] = useState(0);
  const [file, setFile] = useState<File | null>(null);
  const [head, setHead] = useState<Head | null>(null);
  const [targets, setTargets] = useState<string[]>([]);
  const [segMode, setSegMode] = useState<"none" | "existing" | "new">(initialSegment ? "existing" : "none");
  const [segId, setSegId] = useState(initialSegment);
  const [segNew, setSegNew] = useState("");
  const [agreed, setAgreed] = useState(false);
  const [over, setOver] = useState(false);
  const [progress, setProgress] = useState<UploadProgress | null>(null);
  const [error, setError] = useState<unknown>(null);
  const abort = useRef<(() => void) | null>(null);

  useEffect(() => {
    if (!progress) return;
    const warn = (e: BeforeUnloadEvent) => { e.preventDefault(); };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [progress]);
  useEffect(() => () => abort.current?.(), []);

  const reset = () => { setStep(0); setFile(null); setHead(null); setTargets([]); setAgreed(false); setSegNew(""); };
  const pick = async (f: File | null) => {
    setError(null);
    setFile(f);
    setHead(null);
    if (!f) return;
    if (f.size > MAX_IMPORT_BYTES) return setError(new Error(`This file is ${formatBytes(f.size)}. The limit is 10 GB; split it into smaller files.`));
    try {
      const h = await readHead(f);
      setHead(h);
      setTargets(h.headings.map(guessTarget));
    } catch (e) { setError(e); }
  };

  const known = new Set(CORE_TARGETS.map((t) => t.value));
  const defs = (attrs?.attributes ?? []).filter((d) => !known.has(d.key));
  const problems = mappingProblems(targets);
  const chosenSeg = segMode === "existing" ? statics.find((s) => s.id === segId) : undefined;
  const segOK = segMode === "none" || (segMode === "existing" && !!chosenSeg) || (segMode === "new" && !!segNew.trim());
  const canNext = [!!head, problems.length === 0, segOK, agreed][step];

  const start = async () => {
    if (!file || !head) return;
    setError(null);
    try {
      let listId = segMode === "existing" ? segId : 0;
      if (segMode === "new") {
        listId = (await api.post<{ id: number }>("/v1/lists", { name: segNew.trim() })).id;
        qc.invalidateQueries({ queryKey: ["segments"] });
      }
      setProgress({ loaded: 0, total: file.size });
      onUploading(true);
      const up = uploadImport(remapped(file, head.end, targets), file.name, listId || undefined, setProgress);
      abort.current = up.abort;
      window.setTimeout(() => qc.invalidateQueries({ queryKey: ["imports"] }), 800);
      await up.promise;
      toast({ text: "Uploaded. The import runs in the background now." });
      reset();
    } catch (e) {
      setError(e);
    } finally {
      abort.current = null;
      setProgress(null);
      onUploading(false);
      qc.invalidateQueries({ queryKey: ["imports"] });
    }
  };

  const pct = progress && progress.total ? Math.min(100, Math.round((progress.loaded / progress.total) * 100)) : 0;
  const mapped = targets.filter(Boolean).length;
  return (
    <section className="panel">
      <div className="panel-head">
        <ol className="imp-steps" aria-label="Import steps">
          {STEPS.map((s, i) => <li key={s} className={i === step ? "current" : i < step ? "done" : ""} aria-current={i === step ? "step" : undefined}><span>{i + 1}</span>{s}</li>)}
        </ol>
      </div>
      <div className="panel-body">
        {progress ? (
          <div style={{ marginBottom: 14 }}>
            <div className="row" style={{ justifyContent: "space-between" }}>
              <span className="small"><strong>Uploading {file?.name}</strong> <span className="muted">{formatBytes(progress.loaded)} of {formatBytes(progress.total)}</span></span>
              <span className="row small"><span className="live-num">{pct}%</span><button className="btn sm" onClick={() => abort.current?.()}>Cancel upload</button></span>
            </div>
            <div className="imp-bar"><div style={{ width: `${pct}%` }} /></div>
            <p className="xs muted" style={{ marginTop: 8 }}>Keep this page open until the upload finishes. After that the import runs on its own.</p>
          </div>
        ) : step === 0 ? (
          <>
            <p className="small muted" style={{ marginBottom: 12 }}>
              The first row names the columns; you choose what each one becomes next. Every contact needs an external ID or an email (which then becomes the ID). Existing contacts are updated; empty cells leave values as they are.
            </p>
            <div className={`imp-drop${over ? " over" : ""}`}
              onDragOver={(e) => { e.preventDefault(); setOver(true); }}
              onDragLeave={() => setOver(false)}
              onDrop={(e) => { e.preventDefault(); setOver(false); pick(e.dataTransfer.files?.[0] ?? null); }}>
              <p className="small">{file ? <><strong>{file.name}</strong> <span className="muted">({formatBytes(file.size)})</span></> : "Drop a CSV file here, or choose one. Up to 10 GB."}</p>
              <input type="file" accept=".csv,text/csv" onChange={(e) => pick(e.target.files?.[0] ?? null)} aria-label="CSV file" />
            </div>
          </>
        ) : step === 1 && head ? (
          <>
            <p className="small muted" style={{ marginBottom: 12 }}>Choose what each column becomes. New fields are created as text; define numbers, dates and yes/no fields in Settings → Contact fields first to have them checked and converted.</p>
            <div className="table-scroll">
              <table className="table">
                <thead><tr><th>Column in your file</th><th>Example values</th><th>Import as</th></tr></thead>
                <tbody>{head.headings.map((h, i) => {
                  const t = targets[i] ?? "";
                  const isNew = t && !known.has(t) && !defs.some((d) => d.key === t);
                  return (
                    <tr key={i}>
                      <td><strong>{h || <span className="faint">(no heading)</span>}</strong></td>
                      <td className="muted small" style={{ maxWidth: 260 }}>{head.samples.map((r) => r[i]).filter(Boolean).slice(0, 3).join(" · ")}</td>
                      <td>
                        <select className="select inline" value={t} aria-label={`Import ${h || `column ${i + 1}`} as`} onChange={(e) => setTargets(targets.map((x, j) => (j === i ? e.target.value : x)))}>
                          <option value="">Don't import</option>
                          <optgroup label="Contact">{CORE_TARGETS.map((c) => <option key={c.value} value={c.value}>{c.label}</option>)}</optgroup>
                          {defs.length > 0 && <optgroup label="Contact fields">{defs.map((d) => <option key={d.key} value={d.key}>{d.label || d.key}</option>)}</optgroup>}
                          {guessTarget(h) && !known.has(guessTarget(h)) && !defs.some((d) => d.key === guessTarget(h)) && <option value={guessTarget(h)}>New field: {guessTarget(h)}</option>}
                        </select>
                        {isNew && <span className="xs muted" style={{ marginLeft: 8 }}>new field</span>}
                      </td>
                    </tr>
                  );
                })}</tbody>
              </table>
            </div>
            {problems.length > 0 && <ul className="small" style={{ color: "var(--danger)", marginTop: 10 }}>{problems.map((p) => <li key={p}>{p}</li>)}</ul>}
          </>
        ) : step === 2 ? (
          <fieldset className="imp-seg">
            <legend className="small muted">Static segments hold exactly the contacts you put in them: event guests, a sales hand-off, a seed list.</legend>
            <label className="check"><input type="radio" name="imp-seg" checked={segMode === "none"} onChange={() => setSegMode("none")} />Don't add them to a segment</label>
            <label className="check"><input type="radio" name="imp-seg" checked={segMode === "existing"} disabled={statics.length === 0} onChange={() => setSegMode("existing")} />Add them to a static segment</label>
            {segMode === "existing" && (
              <select className="select" style={{ maxWidth: 360, margin: "0 0 12px 24px" }} value={segId || ""} onChange={(e) => setSegId(Number(e.target.value))} aria-label="Static segment">
                <option value="" disabled>Choose a static segment</option>
                {statics.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
              </select>
            )}
            <label className="check"><input type="radio" name="imp-seg" checked={segMode === "new"} onChange={() => setSegMode("new")} />Create a new static segment</label>
            {segMode === "new" && <input className="input" style={{ maxWidth: 360, margin: "0 0 12px 24px" }} autoFocus value={segNew} onChange={(e) => setSegNew(e.target.value)} placeholder="Diwali webinar guests" aria-label="New static segment name" />}
          </fieldset>
        ) : step === 3 && head && file ? (
          <>
            <dl className="kv" style={{ marginBottom: 14 }}>
              <dt>File</dt><dd>{file.name} <span className="muted">({formatBytes(file.size)})</span></dd>
              <dt>Columns</dt><dd>{mapped} imported{head.headings.length - mapped > 0 ? `, ${head.headings.length - mapped} skipped` : ""}: {targets.filter(Boolean).join(", ")}</dd>
              <dt>Static segment</dt><dd>{segMode === "none" ? <span className="muted">None</span> : segMode === "new" ? <>{segNew.trim()} <span className="badge info">new</span></> : chosenSeg?.name}</dd>
            </dl>
            <label className="check"><input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} />These people agreed to hear from us. Unsubscribe columns like <code>optout_email</code> are respected.</label>
          </>
        ) : null}
        <ErrorNote error={error} />
        {!progress && (
          <div className="row" style={{ marginTop: 14 }}>
            {step > 0 && <button className="btn" onClick={() => setStep(step - 1)}>Back</button>}
            {step < 3 && <button className="btn primary" disabled={!canNext} onClick={() => setStep(step + 1)}>Next</button>}
            {step === 3 && <button className="btn primary" disabled={!agreed} onClick={start}>Start import</button>}
            {step > 0 && <button className="btn ghost" onClick={reset}>Start over</button>}
          </div>
        )}
      </div>
    </section>
  );
}

function ImportTable({ imports, listName, canCancel }: { imports: Import[]; listName: Map<number, string>; canCancel: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState<number | null>(null);
  const cancel = useMutation({
    mutationFn: (id: number) => api.post(`/v1/imports/${id}/cancel`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["imports"] }); toast({ text: "Import cancelled. Rows already imported stay." }); },
  });
  return (
    <>
      <ErrorNote error={cancel.error} />
      <div className="table-scroll">
        <table className="table">
          <thead><tr><th>File</th><th>Static segment</th><th>Started</th><th>Status</th><th className="num">Imported</th><th className="num">Skipped</th><th /></tr></thead>
          <tbody>{imports.map((i) => {
            const [label, tone] = STATUS[i.status] ?? [i.status, ""];
            const active = ACTIVE.includes(i.status);
            const hasDetail = i.errors?.length > 0;
            const expanded = open === i.id;
            return (
              <Fragment key={i.id}>
                <tr>
                  <td><strong>{i.filename || "Untitled file"}</strong><div className="xs muted">{formatBytes(i.bytes)}</div></td>
                  <td className="small">{i.list_id ? listName.get(i.list_id) ?? `Segment ${i.list_id}` : <span className="faint">None</span>}</td>
                  <td className="muted" style={{ whiteSpace: "nowrap" }}>{timeAgo(i.created_at)}</td>
                  <td style={{ minWidth: 150 }}>
                    <span className={`badge ${tone}`}>{label}</span>
                    {active && <div className="imp-bar busy" aria-hidden="true"><div /></div>}
                    {i.status === "done" && took(i) && <div className="xs muted">Took {took(i)}</div>}
                    {i.message && !active && <div className="xs muted" style={{ maxWidth: 260 }}>{i.message}</div>}
                  </td>
                  <td className="num"><LiveNumber value={i.rows_done} /></td>
                  <td className="num">{i.rows_failed > 0 ? <span style={{ color: "var(--danger)" }}>{i.rows_failed.toLocaleString("en-IN")}</span> : <span className="faint">0</span>}</td>
                  <td className="num">
                    <div className="row" style={{ justifyContent: "flex-end", flexWrap: "nowrap" }}>
                      {canCancel && (i.status === "queued" || i.status === "running") && (
                        <button className="btn sm" disabled={cancel.isPending} onClick={() => confirm("Stop this import? Rows already imported stay.") && cancel.mutate(i.id)}>Cancel</button>
                      )}
                      {hasDetail && <button className="btn ghost sm" aria-expanded={expanded} onClick={() => setOpen(expanded ? null : i.id)}>{expanded ? "Hide errors" : "Show errors"}</button>}
                    </div>
                  </td>
                </tr>
                {expanded && (
                  <tr className="imp-detail">
                    <td colSpan={7}>
                      <p className="xs muted">{i.rows_failed > i.errors.length ? `The first ${i.errors.length} of ${i.rows_failed.toLocaleString("en-IN")} skipped rows:` : "Skipped rows:"}</p>
                      <ul className="imp-errors small muted">{i.errors.map((e, n) => <li key={n}>Row {e.row}: {e.error}</li>)}</ul>
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}</tbody>
        </table>
      </div>
    </>
  );
}
