import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useEffect, useRef, useState } from "react";
import { api, type Import, type Segment } from "../api";
import { useCan } from "../auth";
import { Empty, ErrorNote, Field, LiveNumber, PageHead, timeAgo, useToast } from "../ui";
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
export function uploadImport(file: File, listId: number | undefined, onProgress?: (p: UploadProgress) => void) {
  const xhr = new XMLHttpRequest();
  const promise = new Promise<{ id: number; status: string; bytes: number }>((resolve, reject) => {
    const qs = new URLSearchParams({ filename: file.name });
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

export function Imports() {
  const canImport = useCan("contacts", "edit");
  const [uploading, setUploading] = useState(false);
  const q = useQuery({
    queryKey: ["imports"],
    queryFn: () => api.get<{ imports: Import[] }>("/v1/imports"),
    refetchInterval: (query) => (uploading || query.state.data?.imports.some((i) => ACTIVE.includes(i.status)) ? 2000 : false),
  });
  const lists = useQuery({ queryKey: ["lists"], queryFn: () => api.get<{ lists: Segment[] }>("/v1/lists") });
  const listName = new Map(lists.data?.lists.map((l) => [l.id, l.name]));
  const imports = q.data?.imports ?? [];

  // Contacts and lists change as imports finish.
  const qc = useQueryClient();
  const finished = imports.filter((i) => i.status === "done").length;
  const seen = useRef(finished);
  useEffect(() => {
    if (finished !== seen.current) {
      seen.current = finished;
      qc.invalidateQueries({ queryKey: ["contacts"] });
      qc.invalidateQueries({ queryKey: ["lists"] });
    }
  }, [finished, qc]);

  return (
    <div className="page">
      <PageHead title="Imports" intro="Import big contact files in the background, up to 10 GB. You can leave this page once the upload finishes; the import keeps going." />
      {canImport && <UploadPanel lists={lists.data?.lists ?? []} onUploading={setUploading} />}
      <section className="section">
        <h3>Recent imports</h3>
        <ErrorNote error={q.error} />
        <div className="panel">
          {imports.length === 0 && !q.isLoading ? (
            <Empty title="No imports yet">Files you import here show up with their progress. The quick import on Contacts doesn't appear here.</Empty>
          ) : (
            <ImportTable imports={imports} listName={listName} canCancel={canImport} />
          )}
        </div>
      </section>
    </div>
  );
}

function UploadPanel({ lists, onUploading }: { lists: Segment[]; onUploading: (b: boolean) => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [file, setFile] = useState<File | null>(null);
  const [listId, setListId] = useState(0);
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

  const pick = (f: File | null) => { setFile(f); setError(null); };
  const tooBig = !!file && file.size > MAX_IMPORT_BYTES;

  const start = async () => {
    if (!file) return;
    setError(null);
    setProgress({ loaded: 0, total: file.size });
    onUploading(true);
    const up = uploadImport(file, listId || undefined, setProgress);
    abort.current = up.abort;
    window.setTimeout(() => qc.invalidateQueries({ queryKey: ["imports"] }), 800);
    try {
      await up.promise;
      toast({ text: "Uploaded. The import runs in the background now." });
      setFile(null);
      setAgreed(false);
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
  return (
    <section className="panel">
      <div className="panel-head"><h3>Import a CSV</h3><span className="xs muted">Up to 10 GB</span></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 12 }}>
          The same format as the quick import: the first row names the columns, with <code>external_id</code> or <code>email</code>, and optionally <code>phone</code>, <code>tags</code> and any attributes. Existing contacts are updated.
        </p>
        {progress ? (
          <div style={{ marginBottom: 14 }}>
            <div className="row" style={{ justifyContent: "space-between" }}>
              <span className="small"><strong>Uploading {file?.name}</strong> <span className="muted">{formatBytes(progress.loaded)} of {formatBytes(progress.total)}</span></span>
              <span className="row small"><span className="live-num">{pct}%</span><button className="btn sm" onClick={() => abort.current?.()}>Cancel upload</button></span>
            </div>
            <div className="imp-bar"><div style={{ width: `${pct}%` }} /></div>
            <p className="xs muted" style={{ marginTop: 8 }}>Keep this page open until the upload finishes. After that the import runs on its own.</p>
          </div>
        ) : (
          <>
            <div className={`imp-drop${over ? " over" : ""}`}
              onDragOver={(e) => { e.preventDefault(); setOver(true); }}
              onDragLeave={() => setOver(false)}
              onDrop={(e) => { e.preventDefault(); setOver(false); pick(e.dataTransfer.files?.[0] ?? null); }}>
              <p className="small">{file ? <><strong>{file.name}</strong> <span className="muted">({formatBytes(file.size)})</span></> : "Drop a CSV file here, or choose one."}</p>
              <input type="file" accept=".csv,text/csv" onChange={(e) => pick(e.target.files?.[0] ?? null)} aria-label="CSV file" />
            </div>
            {tooBig && <div className="alert error">This file is {formatBytes(file.size)}. The limit is 10 GB; split it into smaller files.</div>}
            <div className="grid-2">
              <Field label="Also add them to a list (optional)">
                <select className="select" value={listId || ""} onChange={(e) => setListId(Number(e.target.value) || 0)}>
                  <option value="">No list</option>
                  {lists.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
                </select>
              </Field>
            </div>
            <label className="check"><input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} />These people agreed to hear from us. Unsubscribe columns like <code>optout_email</code> are respected.</label>
            <button className="btn primary" disabled={!file || !agreed || tooBig} onClick={start}>Start import</button>
          </>
        )}
        <ErrorNote error={error} />
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
          <thead><tr><th>File</th><th>List</th><th>Started</th><th>Status</th><th className="num">Imported</th><th className="num">Skipped</th><th /></tr></thead>
          <tbody>{imports.map((i) => {
            const [label, tone] = STATUS[i.status] ?? [i.status, ""];
            const active = ACTIVE.includes(i.status);
            const hasDetail = i.errors?.length > 0;
            const expanded = open === i.id;
            return (
              <Fragment key={i.id}>
                <tr>
                  <td><strong>{i.filename || "Untitled file"}</strong><div className="xs muted">{formatBytes(i.bytes)}</div></td>
                  <td className="small">{i.list_id ? listName.get(i.list_id) ?? `List ${i.list_id}` : <span className="faint">None</span>}</td>
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
