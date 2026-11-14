import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api";
import { ErrorNote, Field, timeAgo, useToast } from "../ui";

// Bounce mailboxes: IMAP mailboxes Pulse reads every 5 minutes for bounce reports (RFC 3464
// delivery status notifications) and spam complaints (RFC 5965 feedback-loop reports). A workspace
// can have several (one per sending domain…), and one mailbox can keep bounces and complaints in
// separate folders or labels: each folder says what it holds.

export type MailboxTLS = "tls" | "starttls" | "none";
export type FolderKind = "auto" | "bounces" | "complaints";
export interface MailboxFolder { name: string; kind: FolderKind }
export interface BounceMailbox {
  id: number; name: string; enabled: boolean; host: string; port: number; tls: MailboxTLS; username: string; folders: MailboxFolder[];
  has_password: boolean; last_check_at?: string; last_error: string; processed: number; skipped: number;
}
export interface MailboxForm {
  id: number; name: string; enabled: boolean; host: string; port: string; tls: MailboxTLS; username: string; folders: MailboxFolder[]; password: string;
}

export const DEFAULT_PORTS: Record<MailboxTLS, number> = { tls: 993, starttls: 143, none: 143 };

export const FOLDER_KINDS: { value: FolderKind; label: string }[] = [
  { value: "auto", label: "Bounces and complaints (tell them apart)" },
  { value: "bounces", label: "Bounces" },
  { value: "complaints", label: "Spam complaints" },
];

export function formFrom(m: BounceMailbox | null, maskedPassword: string): MailboxForm {
  if (!m) return { id: 0, name: "", enabled: true, host: "", port: "993", tls: "tls", username: "", folders: [{ name: "INBOX", kind: "auto" }], password: "" };
  return { id: m.id, name: m.name, enabled: m.enabled, host: m.host, port: String(m.port), tls: m.tls, username: m.username, folders: m.folders.map((f) => ({ ...f })), password: maskedPassword };
}

/** mailboxRequest turns the form into the API body; a masked or empty password keeps the saved one. */
export function mailboxRequest(f: MailboxForm) {
  const port = Number(f.port) || DEFAULT_PORTS[f.tls];
  const folders = f.folders.map((x) => ({ name: x.name.trim(), kind: x.kind })).filter((x) => x.name !== "");
  return {
    id: f.id || undefined, name: f.name.trim(), enabled: f.enabled, host: f.host.trim(), port, tls: f.tls, username: f.username.trim(),
    folders: folders.length ? folders : [{ name: "INBOX", kind: "auto" as FolderKind }], password: f.password.startsWith("••") ? "" : f.password,
  };
}

/** passwordNeeded: changing the server, port, encryption or user needs the password typed again. */
export function passwordNeeded(saved: BounceMailbox | null, f: MailboxForm): boolean {
  if (f.password && !f.password.startsWith("••")) return false;
  if (!saved?.has_password) return true;
  const r = mailboxRequest(f);
  return saved.host.toLowerCase() !== r.host.toLowerCase() || saved.port !== r.port || saved.tls !== r.tls || saved.username !== r.username;
}

/** folderProblem: why the folders can't be saved, or "". */
export function folderProblem(folders: MailboxFolder[]): string {
  const names = folders.map((f) => f.name.trim()).filter(Boolean);
  if (names.length > 10) return "At most 10 folders per mailbox.";
  if (new Set(names).size !== names.length) return "A folder is listed twice.";
  if (names.some((n) => /[\r\n"*%]/.test(n))) return "Folder names can't contain quotes, * or %.";
  return "";
}

type MailboxView = { mailbox: BounceMailbox; password?: string };

export function BounceMailboxPanel() {
  const toast = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["bounce-mailboxes"], queryFn: () => api.get<{ mailboxes: MailboxView[] }>("/v1/settings/bounce-mailboxes"), refetchInterval: 15000 });
  const [editing, setEditing] = useState<{ form: MailboxForm; saved: BounceMailbox | null } | null>(null);
  const check = useMutation({
    mutationFn: (id: number) => api.post(`/v1/settings/bounce-mailboxes/${id}/check`),
    onSuccess: () => toast({ text: "Checking the mailbox now." }),
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.del(`/v1/settings/bounce-mailboxes/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["bounce-mailboxes"] }); toast({ text: "Bounce mailbox removed." }); },
  });
  const boxes = q.data?.mailboxes ?? [];
  return (
    <section className="panel section">
      <div className="panel-head">
        <h3>Bounce mailboxes (IMAP)</h3>
        <button className="btn sm" onClick={() => setEditing({ form: formFrom(null, ""), saved: null })}>Add mailbox</button>
      </div>
      <div className="panel-body">
        <p className="small muted" style={{ marginTop: 0 }}>
          If your email server sends bounces and spam complaints to a mailbox (your Return-Path or abuse address), Pulse reads it every 5 minutes and
          records them like the webhook above. Use several mailboxes (one per sending domain), or one mailbox with a folder or label each for bounces and
          for complaints. Every message Pulse reads is marked read, so use these folders only for this.
        </p>
        <ErrorNote error={q.error ?? check.error ?? remove.error} />
        {boxes.length === 0 && !editing && <p className="small muted">No bounce mailbox yet.</p>}
        <ul className="mbx-list">
          {boxes.map(({ mailbox: m, password }) => (
            <li key={m.id} className="mbx">
              <div className="mbx-head">
                <strong>{m.name}</strong>
                <span className={`badge ${m.enabled ? "ok" : ""}`}>{m.enabled ? "Checked every 5 minutes" : "Off"}</span>
                <span style={{ flex: 1 }} />
                <button className="btn sm" onClick={() => setEditing({ form: formFrom(m, password ?? ""), saved: m })}>Edit</button>
                {m.enabled && <button className="btn sm" disabled={check.isPending} onClick={() => check.mutate(m.id)}>Check now</button>}
                <button className="btn sm ghost danger" onClick={() => { if (confirm(`Remove the mailbox "${m.name}"?`)) remove.mutate(m.id); }}>Remove</button>
              </div>
              <div className="xs muted">
                {m.username} on {m.host}:{m.port} · {m.folders.map((f) => `${f.name} (${FOLDER_KINDS.find((k) => k.value === f.kind)?.label.toLowerCase()})`).join(", ")}
              </div>
              <div className="xs muted">
                {m.last_check_at ? <>Last checked {timeAgo(m.last_check_at)}. </> : m.enabled ? "Not checked yet. " : ""}
                {m.processed} bounces and complaints recorded, {m.skipped} other messages skipped.
                {m.last_error && <span style={{ color: "var(--danger)" }}> Last error: {m.last_error}</span>}
              </div>
            </li>
          ))}
        </ul>
        {editing && <MailboxEditor start={editing.form} saved={editing.saved} onDone={() => setEditing(null)} />}
      </div>
    </section>
  );
}

function MailboxEditor({ start, saved, onDone }: { start: MailboxForm; saved: BounceMailbox | null; onDone: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [f, setF] = useState(start);
  const set = (p: Partial<MailboxForm>) => setF((cur) => ({ ...cur, ...p }));
  const setFolder = (i: number, p: Partial<MailboxFolder>) => set({ folders: f.folders.map((x, j) => (j === i ? { ...x, ...p } : x)) });
  const save = useMutation({
    mutationFn: () => (f.id ? api.put<MailboxView>(`/v1/settings/bounce-mailboxes/${f.id}`, mailboxRequest(f)) : api.post<MailboxView>("/v1/settings/bounce-mailboxes", mailboxRequest(f))),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["bounce-mailboxes"] });
      toast({ text: r.mailbox.enabled ? "Saved. Pulse checks the mailbox now and every 5 minutes." : "Saved. Checking is off." });
      onDone();
    },
  });
  const test = useMutation({
    mutationFn: () => api.post<{ ok: boolean; message?: string; error?: string }>("/v1/settings/bounce-mailboxes/test", mailboxRequest(f)),
    onSuccess: (r) => toast({ text: r.ok ? r.message ?? "Connected." : `Couldn't connect: ${r.error}`, error: !r.ok }),
  });
  const needPw = passwordNeeded(saved, f);
  const problem = folderProblem(f.folders);
  const ready = f.host.trim() !== "" && f.username.trim() !== "" && !needPw && !problem;
  return (
    <div className="mbx-editor">
      <h4>{f.id ? `Edit ${saved?.name}` : "Add a bounce mailbox"}</h4>
      <ErrorNote error={save.error ?? test.error} />
      <div className="grid-2">
        <Field label="Name"><input className="input" value={f.name} placeholder="Bounces for acme.com" onChange={(e) => set({ name: e.target.value })} /></Field>
        <Field label="IMAP server"><input className="input" value={f.host} placeholder="imap.example.com" onChange={(e) => set({ host: e.target.value })} /></Field>
        <div className="row" style={{ alignItems: "flex-end" }}>
          <Field label="Encryption">
            <select className="select" value={f.tls} onChange={(e) => { const tls = e.target.value as MailboxTLS; set({ tls, port: String(DEFAULT_PORTS[tls]) }); }}>
              <option value="tls">TLS (port 993)</option>
              <option value="starttls">STARTTLS (port 143)</option>
              <option value="none">None (development only)</option>
            </select>
          </Field>
          <Field label="Port"><input className="input" style={{ width: 90 }} inputMode="numeric" value={f.port} onChange={(e) => set({ port: e.target.value.replace(/\D/g, "") })} /></Field>
        </div>
        <Field label="Username"><input className="input" value={f.username} autoComplete="off" onChange={(e) => set({ username: e.target.value })} /></Field>
        <Field label="Password" hint={needPw && saved?.has_password ? "The server or user changed: type the password again." : saved?.has_password ? "Saved and encrypted. Leave as is to keep it." : undefined}>
          <input className="input" type="password" autoComplete="new-password" value={f.password} onFocus={() => { if (f.password.startsWith("••")) set({ password: "" }); }} onChange={(e) => set({ password: e.target.value })} />
        </Field>
      </div>
      <Field label="Folders to read" group hint="A folder or Gmail label per kind of report, or one folder with both: Pulse tells them apart.">
        <div className="mbx-folders">
          {f.folders.map((x, i) => (
            <div className="row" key={i} style={{ flexWrap: "nowrap", marginBottom: 6 }}>
              <input className="input" value={x.name} placeholder="INBOX, Bounces or [Gmail]/Spam reports" aria-label={`Folder ${i + 1}`} onChange={(e) => setFolder(i, { name: e.target.value })} />
              <select className="select" value={x.kind} aria-label={`Folder ${i + 1} holds`} onChange={(e) => setFolder(i, { kind: e.target.value as FolderKind })}>
                {FOLDER_KINDS.map((k) => <option key={k.value} value={k.value}>{k.label}</option>)}
              </select>
              <button className="btn ghost sm" disabled={f.folders.length === 1} aria-label={`Remove folder ${i + 1}`} onClick={() => set({ folders: f.folders.filter((_, j) => j !== i) })}>Remove</button>
            </div>
          ))}
          <button className="btn sm" disabled={f.folders.length >= 10} onClick={() => set({ folders: [...f.folders, { name: "", kind: "complaints" }] })}>Add folder</button>
        </div>
      </Field>
      {problem && <div className="alert error">{problem}</div>}
      <label className="check"><input type="checkbox" checked={f.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> Check this mailbox every 5 minutes</label>
      <div className="row" style={{ gap: 8 }}>
        <button className="btn primary" disabled={!ready || save.isPending} onClick={() => save.mutate()}>Save mailbox</button>
        <button className="btn" disabled={!ready || test.isPending} onClick={() => test.mutate()}>{test.isPending ? "Connecting…" : "Test connection"}</button>
        <button className="btn ghost" onClick={onDone}>Cancel</button>
      </div>
    </div>
  );
}
