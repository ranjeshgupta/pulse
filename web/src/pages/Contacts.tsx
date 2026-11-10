import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useDeferredValue, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, CHANNEL_NAMES, CHANNELS, type Channel, type Contact, type EventRow, type Segment } from "../api";
import { contactCompany, isAccountAttr } from "../accounts";
import { Drawer, Empty, ErrorNote, Field, Modal, PageHead, timeAgo, useToast } from "../ui";
import { useCompanyOptions } from "./Companies";
import { ContactInbox } from "./InApp";
import { useStages } from "./Stages";
import { formatBytes, MAX_IMPORT_BYTES, uploadImport, type UploadProgress } from "./Imports";

export default function Contacts() {
  const [q, setQ] = useState("");
  const query = useDeferredValue(q);
  const [params, setParams] = useSearchParams();
  const [open, setOpenState] = useState<string | null>(params.get("open"));
  const setOpen = (ext: string | null) => {
    setOpenState(ext);
    if (params.has("open")) setParams({}, { replace: true });
  };
  const [importing, setImporting] = useState(false);
  const list = useInfiniteQuery({
    queryKey: ["contacts", query],
    queryFn: ({ pageParam }) => api.get<{ contacts: Contact[]; next: string }>(`/v1/contacts?q=${encodeURIComponent(query)}&after=${pageParam}&limit=50`),
    initialPageParam: "",
    getNextPageParam: (last) => last.next || undefined,
  });
  const contacts = list.data?.pages.flatMap((p) => p.contacts) ?? [];

  return (
    <div className="page">
      <PageHead
        title="Contacts"
        intro="Everyone Pulse has seen an event or profile for. Contacts are created automatically by the first event with a new external ID."
        actions={<>
          <Link className="btn" to="/contacts/duplicates">Duplicates</Link>
          <a className="btn" href="/v1/contacts/export" download>Export CSV</a>
          <button className="btn primary" onClick={() => setImporting(true)}>Import CSV</button>
        </>}
      />
      {importing && <ImportModal onClose={() => setImporting(false)} />}
      <div className="row" style={{ marginBottom: 14 }}>
        <input className="input" style={{ maxWidth: 380 }} placeholder="Search by ID, email, name or phone" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search contacts" />
      </div>
      <ErrorNote error={list.error} />
      <div className="panel">
        {contacts.length === 0 && !list.isLoading ? (
          <Empty title={query ? "No contacts match" : "No contacts yet"}>
            {query ? "Try part of an email address or external ID." : "Send an identify event from your app or server, and the contact appears here immediately."}
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>External ID</th><th>Email</th><th>Name</th><th>City</th><th>Last updated</th></tr></thead>
            <tbody>
              {contacts.map((c) => (
                <tr key={c.id} className="clickable" onClick={() => setOpen(c.external_id)}>
                  <td>{c.external_id}</td>
                  <td>{c.email || <span className="faint">none</span>}</td>
                  <td>{String(c.attrs.name ?? c.attrs.first_name ?? "")}</td>
                  <td>{String(c.attrs.city ?? "")}</td>
                  <td className="muted">{timeAgo(c.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {list.hasNextPage && (
        <div style={{ marginTop: 14 }}><button className="btn" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>Load more</button></div>
      )}
      {open && <ContactDrawer externalId={open} onClose={() => setOpen(null)} />}
    </div>
  );
}

function ContactDrawer({ externalId, onClose }: { externalId: string; onClose: () => void }) {
  const id = encodeURIComponent(externalId);
  const c = useQuery({ queryKey: ["contact", externalId], queryFn: () => api.get<{ contact: Contact; segments: number[] }>(`/v1/contacts/${id}`), refetchInterval: 4000 });
  const evs = useQuery({ queryKey: ["contact-events", externalId], queryFn: () => api.get<{ events: EventRow[] }>(`/v1/contacts/${id}/events`), refetchInterval: 4000 });
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const names = new Map(segs.data?.segments.map((s) => [s.id, s.name]));
  const contact = c.data?.contact;
  const merged = contact?.attrs.merged_into != null;
  const [merging, setMerging] = useState(false);

  return (
    <Drawer title={externalId} subtitle={contact?.email} onClose={onClose}>
      <ErrorNote error={c.error} />
      {merged && (
        <div className="alert">This contact was merged into <strong>{String(contact!.attrs.merged_into)}</strong>. Their history now belongs to that contact; new events go there too.</div>
      )}
      {contact && !merged && (
        <div className="row" style={{ marginBottom: 18 }}>
          <button className="btn sm" onClick={() => setMerging(true)}>Merge into…</button>
        </div>
      )}
      {merging && contact && <MergeModal loser={contact} onClose={() => setMerging(false)} />}
      <h3 style={{ marginBottom: 10 }}>Segments and lists</h3>
      {c.data && c.data.segments.length === 0 && <p className="muted small">Not in any segment or list right now.</p>}
      <div className="row" style={{ marginBottom: 22 }}>
        {c.data?.segments.map((s) => <span className="badge info" key={s}>{names.get(s) ?? `Segment ${s}`}</span>)}
      </div>

      {contact && !merged && <StageAndCompany contact={contact} />}
      {contact && <TagsAndPoints contact={contact} />}
      {contact && <ConsentPanel contact={contact} />}
      {contact && <ContactInbox externalId={contact.external_id} />}

      <h3 style={{ marginBottom: 10 }}>Attributes</h3>
      <dl className="kv" style={{ marginBottom: 22 }}>
        {contact && Object.entries(contact.attrs).filter(([k]) => !k.startsWith("optout_") && k !== "tags" && k !== "points" && !isAccountAttr(k)).sort().map(([k, v]) => (
          <div key={k} style={{ display: "contents" }}><dt>{k}</dt><dd>{typeof v === "object" ? JSON.stringify(v) : String(v)}</dd></div>
        ))}
      </dl>

      <h3 style={{ marginBottom: 10 }}>Activity</h3>
      {evs.data?.events.length === 0 && <p className="muted small">No events recorded.</p>}
      <table className="table">
        <tbody>
          {evs.data?.events.map((e) => (
            <tr key={e.id}>
              <td style={{ width: 150 }}>{e.name}</td>
              <td className="muted xs">{e.props !== "{}" ? e.props : ""}</td>
              <td className="muted xs" style={{ whiteSpace: "nowrap" }}>{timeAgo(e.ts)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Drawer>
  );
}

// StageAndCompany shows and changes the contact's lifecycle stage and company. Both change through
// events (stage_changed, company_linked), so the activity list records every change.
function StageAndCompany({ contact }: { contact: Contact }) {
  const qc = useQueryClient();
  const toast = useToast();
  const stages = useStages().data ?? [];
  const companies = useCompanyOptions().data ?? [];
  const id = encodeURIComponent(contact.external_id);
  const stage = contact.attrs.stage != null ? String(contact.attrs.stage) : "";
  const company = contactCompany(contact);
  const copies = Object.entries(contact.attrs).filter(([k]) => k.startsWith("company_") && k !== "company_id" && k !== "company_name").sort();
  const refresh = () => window.setTimeout(() => qc.invalidateQueries({ queryKey: ["contact", contact.external_id] }), 800);
  const setStage = useMutation({
    mutationFn: (s: string) => api.put(`/v1/contacts/${id}/stage`, { stage: s }),
    onSuccess: (_, s) => { toast({ text: `Moved to ${s}` }); refresh(); },
  });
  const link = useMutation({
    mutationFn: (companyId: number | null) => api.put(`/v1/contacts/${id}/company`, { company_id: companyId }),
    onSuccess: (_, companyId) => { toast({ text: companyId ? `Linked to ${companies.find((c) => c.id === companyId)?.name ?? "the company"}` : "Unlinked from the company" }); refresh(); qc.invalidateQueries({ queryKey: ["companies"] }); },
  });
  return (
    <section style={{ marginBottom: 22 }}>
      <h3 style={{ marginBottom: 10 }}>Stage and company</h3>
      <dl className="kv" style={{ marginBottom: 10 }}>
        <dt>Stage</dt>
        <dd>
          <div className="row">
            <select className="select inline" value={stage} disabled={setStage.isPending || stages.length === 0} onChange={(e) => e.target.value && setStage.mutate(e.target.value)} aria-label="Stage">
              <option value="" disabled>{stages.length ? "No stage" : "No stages defined"}</option>
              {stage && !stages.some((s) => s.name === stage) && <option value={stage} disabled>{stage} (removed)</option>}
              {stages.map((s) => <option key={s.id} value={s.name}>{s.name}</option>)}
            </select>
            {contact.attrs.stage_changed_at != null && <span className="xs muted">since {timeAgo(String(contact.attrs.stage_changed_at))}</span>}
          </div>
        </dd>
        <dt>Company</dt>
        <dd>
          <div className="row">
            <select className="select inline" value={company ? String(company.id) : ""} disabled={link.isPending} onChange={(e) => link.mutate(e.target.value ? Number(e.target.value) : null)} aria-label="Company">
              <option value="">No company</option>
              {company && !companies.some((c) => c.id === company.id) && <option value={String(company.id)}>{company.name}</option>}
              {companies.map((c) => <option key={c.id} value={String(c.id)}>{c.name}</option>)}
            </select>
            {company && <Link className="small" to={`/companies/${company.id}`}>Open</Link>}
          </div>
        </dd>
        {copies.map(([k, v]) => (
          <div key={k} style={{ display: "contents" }}><dt className="muted">{k}</dt><dd>{String(v)}</dd></div>
        ))}
      </dl>
      <ErrorNote error={setStage.error ?? link.error} />
    </section>
  );
}

// MergeModal merges this contact (the loser) into another one the user finds by ID, email or name.
function MergeModal({ loser, onClose }: { loser: Contact; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [q, setQ] = useState(loser.email || "");
  const query = useDeferredValue(q.trim());
  const [winner, setWinner] = useState("");
  const found = useQuery({
    queryKey: ["contacts", "merge-search", query],
    queryFn: () => api.get<{ contacts: Contact[] }>(`/v1/contacts?q=${encodeURIComponent(query)}&limit=10`),
    enabled: query.length > 0,
  });
  const candidates = (found.data?.contacts ?? []).filter((c) => c.external_id !== loser.external_id && c.attrs.merged_into == null);
  const merge = useMutation({
    mutationFn: () => api.post("/v1/contacts/merge", { winner, loser: loser.external_id }),
    onSuccess: () => {
      toast({ text: `Merging ${loser.external_id} into ${winner}` });
      window.setTimeout(() => { qc.invalidateQueries({ queryKey: ["contact", loser.external_id] }); qc.invalidateQueries({ queryKey: ["contacts"] }); }, 1200);
      onClose();
    },
  });
  return (
    <Modal title={`Merge ${loser.external_id} into…`} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!winner || merge.isPending} onClick={() => merge.mutate()}>Merge</button>
    </>}>
      <p className="small muted" style={{ marginBottom: 12 }}>
        The contact you pick is kept: its values win, and this contact's other attributes, history, tags, points and lists move to it. This contact then leaves every segment and its later events go to the kept one. A merge can't be undone.
      </p>
      <Field label="Find the contact to keep"><input className="input" autoFocus value={q} onChange={(e) => { setQ(e.target.value); setWinner(""); }} placeholder="ID, email, name or phone" /></Field>
      {query && candidates.length === 0 && !found.isLoading && <p className="small muted">No other contact matches.</p>}
      {candidates.length > 0 && (
        <table className="table">
          <tbody>
            {candidates.map((c) => (
              <tr key={c.id} className="clickable" onClick={() => setWinner(c.external_id)}>
                <td style={{ width: 28 }}><input type="radio" name="merge-winner" checked={winner === c.external_id} onChange={() => setWinner(c.external_id)} aria-label={`Keep ${c.external_id}`} /></td>
                <td>{c.external_id}</td>
                <td className="muted">{c.email}</td>
                <td className="muted">{String(c.attrs.name ?? c.attrs.first_name ?? "")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <ErrorNote error={merge.error ?? found.error} />
    </Modal>
  );
}

// TagsAndPoints shows the contact's tags and score and changes them. Both change through events
// (tag_added, points_changed), so the activity list records every change.
function TagsAndPoints({ contact }: { contact: Contact }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [tag, setTag] = useState("");
  const [delta, setDelta] = useState("");
  const tags = Array.isArray(contact.attrs.tags) ? (contact.attrs.tags as string[]) : [];
  const points = Number(contact.attrs.points ?? 0) || 0;
  const id = encodeURIComponent(contact.external_id);
  const refresh = () => window.setTimeout(() => { qc.invalidateQueries({ queryKey: ["contact", contact.external_id] }); qc.invalidateQueries({ queryKey: ["tags"] }); }, 800);
  const tagMut = useMutation({
    mutationFn: (b: { add?: string[]; remove?: string[] }) => api.post(`/v1/contacts/${id}/tags`, b),
    onSuccess: (_, b) => { setTag(""); toast({ text: b.add ? `Tagged ${b.add.join(", ")}` : `Removed ${b.remove?.join(", ")}` }); refresh(); },
  });
  const pointsMut = useMutation({
    mutationFn: (n: number) => api.post(`/v1/contacts/${id}/points`, { delta: n, reason: "changed in Pulse" }),
    onSuccess: (_, n) => { setDelta(""); toast({ text: `${n > 0 ? "Added" : "Took away"} ${Math.abs(n)} points` }); refresh(); },
  });
  const validTag = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(tag.trim());
  const n = Number(delta);
  return (
    <section style={{ marginBottom: 22 }}>
      <h3 style={{ marginBottom: 10 }}>Tags and points</h3>
      <div className="row" style={{ marginBottom: 10 }}>
        {tags.length === 0 && <span className="muted small">No tags.</span>}
        {tags.map((t) => (
          <span className="tag-chip" key={t}>
            {t}
            <button aria-label={`Remove tag ${t}`} disabled={tagMut.isPending} onClick={() => tagMut.mutate({ remove: [t] })}>×</button>
          </span>
        ))}
      </div>
      <form className="row" style={{ marginBottom: 14 }} onSubmit={(e) => { e.preventDefault(); if (validTag) tagMut.mutate({ add: [tag.trim()] }); }}>
        <input className="input" style={{ maxWidth: 200 }} list="drawer-tags" placeholder="Add a tag" value={tag} onChange={(e) => setTag(e.target.value)} aria-label="New tag" />
        <TagOptions id="drawer-tags" />
        <button className="btn sm" disabled={!validTag || tagMut.isPending}>Add tag</button>
      </form>
      <div className="row">
        <span className="points-value" aria-label="Points">{points.toLocaleString("en-IN")}</span>
        <span className="small muted">points</span>
        <form className="row" onSubmit={(e) => { e.preventDefault(); if (Number.isInteger(n) && n !== 0) pointsMut.mutate(n); }}>
          <input className="input" style={{ width: 90 }} inputMode="numeric" placeholder="+10 or -5" value={delta} onChange={(e) => setDelta(e.target.value)} aria-label="Points to add" />
          <button className="btn sm" disabled={!Number.isInteger(n) || n === 0 || pointsMut.isPending}>Adjust</button>
        </form>
      </div>
      <ErrorNote error={tagMut.error ?? pointsMut.error} />
    </section>
  );
}

export function TagOptions({ id }: { id: string }) {
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/tags"), staleTime: 30_000 }).data?.tags ?? [];
  return <datalist id={id}>{tags.map((t) => <option key={t} value={t} />)}</datalist>;
}

const optedOut = (c: Contact, ch: Channel | "all") => ["true", "1", "yes"].includes(String(c.attrs[`optout_${ch}`]));

// ConsentPanel shows and changes what this contact has agreed to receive. Changes are recorded as
// events, so the contact's history shows who changed it and when.
function ConsentPanel({ contact }: { contact: Contact }) {
  const qc = useQueryClient();
  const toast = useToast();
  const change = useMutation({
    mutationFn: ({ ch, sub }: { ch: Channel | "all"; sub: boolean }) =>
      api.post("/v1/events", { external_id: contact.external_id, name: sub ? "resubscribed" : "unsubscribed", props: { channel: ch, source: "pulse_app" } }),
    onSuccess: (_, v) => {
      toast({ text: `${v.sub ? "Subscribed to" : "Unsubscribed from"} ${v.ch === "all" ? "all messages" : CHANNEL_NAMES[v.ch]}` });
      window.setTimeout(() => qc.invalidateQueries({ queryKey: ["contact", contact.external_id] }), 800);
    },
  });
  const all = optedOut(contact, "all");
  return (
    <section style={{ marginBottom: 22 }}>
      <h3 style={{ marginBottom: 10 }}>Messages they agreed to</h3>
      {all && <div className="alert error">Unsubscribed from everything. Nothing is sent on any channel.</div>}
      <table className="table">
        <tbody>
          {CHANNELS.map((ch) => {
            const out = all || optedOut(contact, ch);
            return (
              <tr key={ch}>
                <td><span className={`chan chan-${ch}`}>{CHANNEL_NAMES[ch]}</span></td>
                <td>{out ? <span className="badge bad">Unsubscribed</span> : <span className="badge ok">Subscribed</span>}</td>
                <td className="num">
                  <button className="btn sm" disabled={change.isPending} onClick={() => change.mutate({ ch, sub: out })}>{out ? "Subscribe" : "Unsubscribe"}</button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {!all && <button className="btn ghost sm danger" style={{ marginTop: 6 }} onClick={() => confirm("Stop all messages to this contact?") && change.mutate({ ch: "all", sub: false })}>Unsubscribe from everything</button>}
      <ErrorNote error={change.error} />
    </section>
  );
}

/** The quick import's limit; bigger files go to a background import. */
const QUICK_MAX_BYTES = 200 * 1024 * 1024;

type ImportResult = { rows: number; imported: number; skipped: number; errors: { row: number; error: string }[]; columns: string[] };

/** ImportModal uploads a CSV of contacts; with a list, every imported contact is also added to it. */
export function ImportModal({ onClose, list }: { onClose: () => void; list?: { id: number; name: string } }) {
  const qc = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<string[][]>([]);
  const [agreed, setAgreed] = useState(false);
  const nav = useNavigate();
  const toast = useToast();
  const [bgProgress, setBgProgress] = useState<UploadProgress | null>(null);
  const bgAbort = useRef<(() => void) | null>(null);
  useEffect(() => () => bgAbort.current?.(), []);
  const background = useMutation({
    mutationFn: async () => {
      setBgProgress({ loaded: 0, total: file!.size });
      const up = uploadImport(file!, list?.id, setBgProgress);
      bgAbort.current = up.abort;
      try { return await up.promise; } finally { bgAbort.current = null; setBgProgress(null); }
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["imports"] }); toast({ text: "Uploaded. The import runs in the background." }); nav("/imports"); },
  });
  const upload = useMutation({
    mutationFn: async () => {
      const fd = new FormData();
      fd.append("file", file!);
      const res = await fetch(`/v1/contacts/import${list ? `?list_id=${list.id}` : ""}`, { method: "POST", body: fd, credentials: "same-origin", headers: { "X-Pulse-CSRF": "1" } });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error);
      return data as ImportResult;
    },
    onSuccess: () => window.setTimeout(() => { qc.invalidateQueries({ queryKey: ["contacts"] }); qc.invalidateQueries({ queryKey: ["lists"] }); }, 1500),
  });
  const pick = async (f: File | null) => {
    setFile(f);
    upload.reset();
    background.reset();
    if (!f) return setPreview([]);
    const head = (await f.slice(0, 4096).text()).split(/\r?\n/).slice(0, 4);
    setPreview(head.filter(Boolean).map((l) => l.split(",").slice(0, 6)));
  };
  const r = upload.data;
  const big = !!file && file.size > QUICK_MAX_BYTES;
  const busy = upload.isPending || background.isPending;
  const pct = bgProgress && bgProgress.total ? Math.min(100, Math.round((bgProgress.loaded / bgProgress.total) * 100)) : 0;
  return (
    <Modal title={list ? `Import contacts into ${list.name}` : "Import contacts"} onClose={onClose} footer={r ? <button className="btn primary" onClick={onClose}>Done</button> : <>
      <button className="btn" onClick={() => { bgAbort.current?.(); onClose(); }}>Cancel</button>
      <button className={`btn${big ? " primary" : ""}`} disabled={!file || !agreed || busy || file.size > MAX_IMPORT_BYTES} onClick={() => background.mutate()}>{background.isPending ? `Uploading… ${pct}%` : "Run in the background"}</button>
      {!big && <button className="btn primary" disabled={!file || !agreed || busy} onClick={() => upload.mutate()}>{upload.isPending ? "Importing…" : "Import contacts"}</button>}
    </>}>
      {r ? (
        <>
          <div className="alert ok">{r.imported.toLocaleString("en-IN")} of {r.rows.toLocaleString("en-IN")} rows imported. They appear in {list ? "the list and in segments" : "segments"} within seconds.</div>
          {r.skipped > 0 && <p className="small" style={{ marginBottom: 8 }}>{r.skipped} rows were skipped{r.errors.length ? ":" : "."}</p>}
          {r.errors.length > 0 && <ul className="small muted">{r.errors.map((e) => <li key={e.row}>Row {e.row}: {e.error}</li>)}</ul>}
        </>
      ) : (
        <>
          <p className="small muted" style={{ marginBottom: 12 }}>
            The first row names the columns. Use <code>external_id</code> (or <code>email</code>, which then becomes the ID), and optionally <code>email</code>, <code>phone</code> in international format (+91…) and any attributes, such as <code>city</code> or <code>plan</code>. A <code>tags</code> column takes tags separated by semicolons. Attributes defined as numbers or yes/no are checked and converted. Existing contacts are updated; empty cells leave values as they are.
          </p>
          <p className="small muted" style={{ marginBottom: 12 }}>
            Very large file (over 200 MB or 2 million rows)? Choose <strong>Run in the background</strong>: it takes up to 10 GB and you can follow it on <Link to="/imports">Imports</Link>.
          </p>
          <input type="file" accept=".csv,text/csv" onChange={(e) => pick(e.target.files?.[0] ?? null)} aria-label="CSV file" />
          {big && <div className="alert info" style={{ marginTop: 12 }}>This file is {formatBytes(file!.size)}, too big for a quick import. Run it in the background instead.</div>}
          {bgProgress && <p className="xs muted" style={{ marginTop: 10 }}>Uploading {formatBytes(bgProgress.loaded)} of {formatBytes(bgProgress.total)}. Keep this window open until it finishes.</p>}
          {preview.length > 0 && (
            <div className="table-scroll" style={{ marginTop: 12 }}>
              <table className="table"><tbody>{preview.map((row, i) => <tr key={i}>{row.map((c, j) => <td key={j} className={i === 0 ? "" : "muted"} style={i === 0 ? { fontWeight: 600 } : undefined}>{c}</td>)}</tr>)}</tbody></table>
            </div>
          )}
          <label className="check" style={{ marginTop: 14 }}><input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} />These people agreed to hear from us. Unsubscribe columns like <code>optout_email</code> are respected.</label>
          <ErrorNote error={upload.error ?? background.error} />
        </>
      )}
    </Modal>
  );
}
