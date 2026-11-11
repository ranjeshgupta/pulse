import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api } from "../api";
import { ErrorNote, Field, timeAgo, useToast } from "../ui";

// The bounce mailbox: an IMAP mailbox Pulse reads every 5 minutes for bounce reports (RFC 3464
// delivery status notifications) and spam complaints (RFC 5965 feedback-loop reports).

export type MailboxTLS = "tls" | "starttls" | "none";
export interface BounceMailbox {
  enabled: boolean; host: string; port: number; tls: MailboxTLS; username: string; folder: string;
  has_password: boolean; last_check_at?: string; last_error: string; processed: number; skipped: number;
}
export interface MailboxForm { enabled: boolean; host: string; port: string; tls: MailboxTLS; username: string; folder: string; password: string }

export const DEFAULT_PORTS: Record<MailboxTLS, number> = { tls: 993, starttls: 143, none: 143 };

export function formFrom(m: BounceMailbox | null, maskedPassword: string): MailboxForm {
  if (!m) return { enabled: true, host: "", port: "993", tls: "tls", username: "", folder: "INBOX", password: "" };
  return { enabled: m.enabled, host: m.host, port: String(m.port), tls: m.tls, username: m.username, folder: m.folder, password: maskedPassword };
}

/** mailboxRequest turns the form into the API body; a masked or empty password keeps the saved one. */
export function mailboxRequest(f: MailboxForm) {
  const port = Number(f.port) || DEFAULT_PORTS[f.tls];
  return { enabled: f.enabled, host: f.host.trim(), port, tls: f.tls, username: f.username.trim(), folder: f.folder.trim() || "INBOX", password: f.password.startsWith("••") ? "" : f.password };
}

/** passwordNeeded: changing the server, port, encryption or user needs the password typed again. */
export function passwordNeeded(saved: BounceMailbox | null, f: MailboxForm): boolean {
  if (f.password && !f.password.startsWith("••")) return false;
  if (!saved?.has_password) return true;
  const r = mailboxRequest(f);
  return saved.host.toLowerCase() !== r.host.toLowerCase() || saved.port !== r.port || saved.tls !== r.tls || saved.username !== r.username;
}

type MailboxResp = { mailbox: BounceMailbox | null; password?: string };

export function BounceMailboxPanel() {
  const toast = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["bounce-mailbox"], queryFn: () => api.get<MailboxResp>("/v1/settings/bounce-mailbox"), refetchInterval: 15000 });
  const saved = q.data?.mailbox ?? null;
  const [f, setF] = useState<MailboxForm | null>(null);
  useEffect(() => { if (q.data && !f) setF(formFrom(q.data.mailbox, q.data.password ?? "")); }, [q.data, f]);
  const save = useMutation({
    mutationFn: () => api.put<MailboxResp>("/v1/settings/bounce-mailbox", mailboxRequest(f!)),
    onSuccess: (r) => { qc.setQueryData(["bounce-mailbox"], r); setF(formFrom(r.mailbox, r.password ?? "")); toast({ text: r.mailbox?.enabled ? "Saved. Pulse checks the mailbox now and every 5 minutes." : "Saved. Checking is off." }); },
  });
  const test = useMutation({
    mutationFn: () => api.post<{ ok: boolean; message?: string; error?: string }>("/v1/settings/bounce-mailbox/test", mailboxRequest(f!)),
    onSuccess: (r) => toast({ text: r.ok ? r.message ?? "Connected." : `Couldn't connect: ${r.error}`, error: !r.ok }),
  });
  const check = useMutation({
    mutationFn: () => api.post("/v1/settings/bounce-mailbox/check"),
    onSuccess: () => toast({ text: "Checking the mailbox now." }),
  });
  const remove = useMutation({
    mutationFn: () => api.del("/v1/settings/bounce-mailbox"),
    onSuccess: () => { qc.setQueryData(["bounce-mailbox"], { mailbox: null }); setF(formFrom(null, "")); toast({ text: "Bounce mailbox removed." }); },
  });
  const set = (p: Partial<MailboxForm>) => setF((cur) => (cur ? { ...cur, ...p } : cur));
  const needPw = f ? passwordNeeded(saved, f) : false;
  const ready = !!f && f.host.trim() !== "" && f.username.trim() !== "" && !needPw;

  return (
    <section className="panel section">
      <div className="panel-head"><h3>Bounce mailbox (IMAP)</h3><span className="xs muted">For SMTP relays without bounce webhooks</span></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 12 }}>
          If your SMTP server sends bounces and feedback-loop complaints to a mailbox (your Return-Path or abuse address), Pulse can read it every 5 minutes.
          It records delivery status notifications (RFC 3464) and spam complaints (RFC 5965 / ARF) like the webhook above, and marks every message it reads as read.
          Use a mailbox only for this: other unread messages there are marked read too.
        </p>
        <ErrorNote error={q.error ?? save.error ?? test.error ?? check.error ?? remove.error} />
        {f && (
          <>
            <div className="grid-2">
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
              <Field label="Folder"><input className="input" value={f.folder} placeholder="INBOX" onChange={(e) => set({ folder: e.target.value })} /></Field>
              <label className="row small" style={{ alignItems: "center", gap: 8, marginTop: 22 }}>
                <input type="checkbox" checked={f.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> Check this mailbox every 5 minutes
              </label>
            </div>
            <div className="row" style={{ gap: 8, marginTop: 6 }}>
              <button className="btn primary" disabled={!ready || save.isPending} onClick={() => save.mutate()}>Save</button>
              <button className="btn" disabled={!ready || test.isPending} onClick={() => test.mutate()}>{test.isPending ? "Connecting…" : "Test connection"}</button>
              {saved?.enabled && <button className="btn" disabled={check.isPending} onClick={() => check.mutate()}>Check now</button>}
              {saved && <button className="btn ghost" disabled={remove.isPending} onClick={() => { if (confirm("Remove the bounce mailbox settings?")) remove.mutate(); }}>Remove</button>}
            </div>
          </>
        )}
        {saved && (
          <p className="xs muted" style={{ marginTop: 12 }}>
            {saved.last_check_at ? <>Last checked {timeAgo(saved.last_check_at)}. </> : saved.enabled ? "Not checked yet. " : ""}
            {saved.processed} bounces and complaints recorded, {saved.skipped} other messages skipped.
            {saved.last_error && <span style={{ color: "var(--danger)" }}> Last error: {saved.last_error}</span>}
          </p>
        )}
      </div>
    </section>
  );
}
