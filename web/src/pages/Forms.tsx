import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, CHANNEL_NAMES, CHANNELS, type Channel, type Form, type FormField, type FormFieldType, type FormSettings, type FormStats, type Segment } from "../api";
import { Empty, ErrorNote, Field, LiveNumber, PageHead, Status, timeAgo, useToast, useCopy } from "../ui";
import { DEFAULT_FILE_MB, formPages, layoutProblems, MAX_FILE_MB, UPLOAD_KINDS, uploadOf } from "./formPages";
import "./forms.css";
import { ContactCount } from "../ContactCount";

const TYPE_LABEL: Record<FormFieldType, string> = {
  text: "Short text", email: "Email", tel: "Phone", number: "Number", textarea: "Long text",
  select: "Dropdown", checkbox: "Checkbox", hidden: "Hidden", file: "File upload", page_break: "Page break",
};
const DEFAULT_ACCENT = "#2747d6";
const MAX_FIELDS = 30;
const KEY_RE = /^[a-zA-Z][a-zA-Z0-9_]{0,63}$/;
const TAG_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/;
const RESERVED = new Set(["tags", "points", "merged_into"]);

const fmtDay = (d: string) => new Date(d.slice(0, 10) + "T00:00:00").toLocaleDateString("en-IN", { day: "numeric", month: "short" });
const fmtTime = (s: string) => new Date(s).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" });

/** snake_case attribute key from a label: "First name" → first_name. */
function toKey(label: string): string {
  let k = label.trim().toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "");
  if (k && !/^[a-z]/.test(k)) k = "f_" + k;
  return k.slice(0, 64);
}

const splitList = (s: string, lines: boolean) => s.split(lines ? /\n|,/ : /,/).map((x) => x.trim()).filter(Boolean);


// ---------- list ----------

export function FormList() {
  const q = useQuery({ queryKey: ["forms"], queryFn: () => api.get<{ forms: Form[]; base_url: string }>("/v1/forms") });
  const copy = useCopy();
  const forms = q.data?.forms ?? [];
  const base = q.data?.base_url ?? "";
  return (
    <div className="page">
      <PageHead title="Forms" intro="Sign-up and contact forms that create or update contacts. Share a link, embed one on your website, or both." actions={<Link className="btn primary" to="/forms/new">New form</Link>} />
      <ErrorNote error={q.error} />
      <div className="panel">
        {forms.length === 0 && !q.isLoading ? (
          <Empty title="No forms yet" action={<Link className="btn primary" to="/forms/new">Create a form</Link>}>A newsletter sign-up with name and email is a good start. New submitters become contacts right away.</Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th className="num">Fields</th><th>Link</th><th>Updated</th><th>Status</th></tr></thead>
            <tbody>{forms.map((f) => {
              const url = `${base}/f/${f.public_key}`;
              return (
                <tr key={f.id}>
                  <td><Link to={`/forms/${f.id}`}>{f.name}</Link></td>
                  <td className="num">{f.fields.filter((x) => x.type !== "page_break").length}</td>
                  <td>{f.status === "active" ? (
                    <div className="row" style={{ gap: 4 }}>
                      <a className="btn ghost sm" href={url} target="_blank" rel="noreferrer">Open</a>
                      <button className="btn ghost sm" onClick={() => copy(url, "Link copied")}>Copy link</button>
                    </div>
                  ) : <span className="faint small">Live forms only</span>}</td>
                  <td className="muted">{timeAgo(f.updated_at)}</td>
                  <td><Status value={f.status === "active" ? "ready" : f.status} /></td>
                </tr>
              );
            })}</tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// ---------- editor ----------

/** A field as edited: auto means the key still follows the label; optText keeps what was typed for dropdown options. */
type Row = { uid: number; auto: boolean; optText: string; f: FormField };
type Draft = { name: string; status: Form["status"]; settings: FormSettings; rows: Row[]; tagText: string; redirect: boolean };

let uidSeq = 0;
const row = (f: FormField, auto = false): Row => ({ uid: ++uidSeq, auto, optText: (f.options ?? []).join("\n"), f });

function draftFrom(f: Form): Draft {
  return {
    name: f.name, status: f.status, settings: { ...f.settings }, rows: f.fields.map((x) => row(x)),
    tagText: (f.settings.tags ?? []).join(", "), redirect: !!f.settings.redirect_url,
  };
}

function newDraft(): Draft {
  return {
    name: "", status: "draft", tagText: "", redirect: false,
    settings: { submit_text: "Sign up", success_message: "Thanks! You're signed up.", list_ids: [], tags: [], resubscribe: [] },
    rows: [
      row({ key: "email", label: "Email", type: "email", required: true }),
      row({ key: "first_name", label: "First name", type: "text" }, true),
    ],
  };
}

/** The Form body the API takes. */
function toForm(d: Draft): Omit<Form, "id" | "public_key"> {
  const fields = d.rows.map(({ f, optText }) => {
    if (f.type === "page_break") return { key: "", label: f.label.trim(), type: f.type } as FormField;
    const out: FormField = { key: f.key.trim(), label: f.label.trim(), type: f.type };
    if (f.type === "file") {
      if (f.accept?.length) out.accept = f.accept;
      if (f.max_mb) out.max_mb = f.max_mb;
    }
    if (f.type !== "hidden" && f.required) out.required = true;
    if (f.type === "select") out.options = splitList(optText, true);
    if (f.type === "hidden" && f.default) out.default = f.default;
    if (f.type !== "hidden" && f.type !== "file" && f.progressive) out.progressive = true;
    return out;
  });
  const s = d.settings;
  const settings: FormSettings = {
    description: s.description?.trim() || undefined,
    submit_text: s.submit_text?.trim() || undefined,
    success_message: d.redirect ? undefined : s.success_message?.trim() || undefined,
    redirect_url: d.redirect ? s.redirect_url?.trim() || undefined : undefined,
    list_ids: s.list_ids ?? [],
    tags: splitList(d.tagText, false),
    consent_text: s.consent_text?.trim() || undefined,
    resubscribe: s.consent_text?.trim() ? s.resubscribe ?? [] : [],
    theme: s.theme || undefined,
  };
  return { name: d.name.trim(), status: d.status, fields, settings };
}

/** Client-side copy of the server's checks, so most mistakes show while typing. */
function check(d: Draft): { rowErr: Map<number, string>; problems: string[] } {
  const rowErr = new Map<number, string>();
  const problems: string[] = [];
  const seen = new Set<string>();
  let identifies = false;
  const layout = layoutProblems(d.rows.map((r) => r.f));
  d.rows.forEach(({ uid, f, optText }, i) => {
    if (f.type === "page_break") {
      if (layout.at.get(i)) rowErr.set(uid, layout.at.get(i)!);
      return;
    }
    const k = f.key.trim();
    let err = layout.at.get(i) ?? "";
    if (!k) err = "Give it a key.";
    else if (!KEY_RE.test(k)) err = "Keys use letters, digits and _ only, and start with a letter.";
    else if (k.startsWith("optout_") || RESERVED.has(k)) err = `"${k}" is managed by Pulse; pick another key.`;
    else if (seen.has(k)) err = `"${k}" is used by another field.`;
    else if (!err && f.type !== "hidden" && !f.label.trim()) err = "Add a label.";
    else if (f.type === "select" && splitList(optText, true).length === 0) err = "Add the dropdown's options.";
    else if (k === "email" && f.type !== "email") err = "The email key needs the Email type.";
    else if (k === "phone" && f.type !== "tel") err = "The phone key needs the Phone type.";
    seen.add(k);
    if ((k === "email" && f.type === "email") || (k === "phone" && f.type === "tel")) identifies = true;
    if (err) rowErr.set(uid, err);
  });
  problems.push(...layout.problems);
  if (d.rows.length === 0) problems.push("Add at least one field.");
  if (d.rows.length > MAX_FIELDS) problems.push(`A form can have up to ${MAX_FIELDS} fields.`);
  if (d.rows.length && !identifies) problems.push("Add an Email field (key email) or a Phone field (key phone). It's how Pulse knows who filled in the form.");
  const r = d.settings.redirect_url?.trim() ?? "";
  if (d.redirect && !/^https?:\/\/\S+/.test(r)) problems.push("The page to send people to must start with https:// or http://.");
  const badTag = splitList(d.tagText, false).find((t) => !TAG_RE.test(t));
  if (badTag) problems.push(`"${badTag}" can't be a tag: use letters, digits, - and _ only.`);
  return { rowErr, problems };
}

export function FormEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const existing = useQuery({ queryKey: ["form", id], queryFn: () => api.get<{ form: Form; hosted_url: string }>(`/v1/forms/${id}`), enabled: !isNew });
  const lists = useQuery({ queryKey: ["lists"], queryFn: () => api.get<{ lists: Segment[] }>("/v1/lists") }).data?.lists ?? [];
  const allTags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/tags"), staleTime: 30_000 }).data?.tags ?? [];
  const [d, setD] = useState<Draft | null>(isNew ? newDraft() : null);
  const saved = useRef<string>("");
  useEffect(() => {
    if (existing.data && !d) {
      const nd = draftFrom(existing.data.form);
      saved.current = JSON.stringify(toForm(nd));
      setD(nd);
    }
  }, [existing.data, d]);

  const save = useMutation({
    mutationFn: (body: ReturnType<typeof toForm>) => (isNew ? api.post<{ id: number }>("/v1/forms", body) : api.put<{ id: number }>(`/v1/forms/${id}`, body)),
    onSuccess: (r, body) => {
      saved.current = JSON.stringify(body);
      qc.invalidateQueries({ queryKey: ["forms"] });
      toast({ text: body.status === "active" ? "Saved. The form is live." : "Saved as draft" });
      if (isNew) nav(`/forms/${r.id}`, { replace: true });
      else qc.invalidateQueries({ queryKey: ["form", id] });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/forms/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["forms"] }); toast({ text: "Form deleted" }); nav("/forms"); },
  });

  const body = useMemo(() => (d ? toForm(d) : null), [d]);
  if (!d || !body) return <div className="page"><ErrorNote error={existing.error} /></div>;

  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const setS = (p: Partial<FormSettings>) => setD({ ...d, settings: { ...d.settings, ...p } });
  const setRow = (uid: number, p: Partial<Row>, fp?: Partial<FormField>) =>
    setD({ ...d, rows: d.rows.map((r) => (r.uid === uid ? { ...r, ...p, f: { ...r.f, ...fp } } : r)) });
  const move = (i: number, dir: -1 | 1) => {
    const rows = [...d.rows];
    [rows[i], rows[i + dir]] = [rows[i + dir], rows[i]];
    set({ rows });
  };
  const addField = () => {
    set({ rows: [...d.rows, row({ key: d.rows.length ? "" : "email", label: "", type: d.rows.length ? "text" : "email" }, true)] });
  };
  const addPageBreak = () => set({ rows: [...d.rows, row({ key: "", label: "", type: "page_break" })] });

  const { rowErr, problems } = check(d);
  const blocker = !body.name ? "Name the form." : rowErr.size ? "Fix the fields marked in red." : problems[0];
  const dirty = !isNew && saved.current !== "" && JSON.stringify(body) !== saved.current;
  const consentOn = !!d.settings.consent_text?.trim();
  const tags = splitList(d.tagText, false);
  const suggestions = allTags.filter((t) => !tags.includes(t)).slice(0, 12);
  const hosted = existing.data?.hosted_url;
  const publicKey = existing.data?.form.public_key;
  const savedLive = existing.data?.form.status === "active";

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/forms">Forms</Link>}
        title={isNew ? "New form" : d.name || "Untitled form"}
        actions={<>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => confirm("Delete this form? Its link and embeds stop working. Contacts it created stay.") && remove.mutate()}>Delete</button>}
          <button className="btn primary" disabled={!!blocker || save.isPending} title={blocker} onClick={() => save.mutate(body)}>{save.isPending ? "Saving…" : "Save"}</button>
        </>}
      />
      <div className="pre-line"><ErrorNote error={save.error ?? remove.error} /></div>
      {dirty && <div className="alert info">You have unsaved changes{savedLive ? ". Visitors see the saved version until you save." : "."}</div>}

      <div className="editor-split" style={{ gridTemplateColumns: "minmax(0,1fr) 400px" }}>
        <div>
          <div className="grid-2">
            <Field label="Name" hint="Shown as the heading on the form.">
              <input className="input" value={d.name} onChange={(e) => set({ name: e.target.value })} placeholder="Newsletter sign-up" />
            </Field>
            <div className="field">
              <span>Status</span>
              <div>
                <div className="seg-toggle" role="group" aria-label="Status">
                  <button type="button" aria-pressed={d.status === "draft"} onClick={() => set({ status: "draft" })}>Draft</button>
                  <button type="button" className="live" aria-pressed={d.status === "active"} onClick={() => set({ status: "active" })}>Live</button>
                </div>
              </div>
              <small>{d.status === "active" ? "Anyone with the link can fill it in once you save." : "Only you can see it. Switch to Live and save to start taking sign-ups."}</small>
            </div>
          </div>

          <section className="section" style={{ marginTop: 10 }}>
            <h3 style={{ marginBottom: 4 }}>Fields</h3>
            <p className="small muted" style={{ marginBottom: 12 }}>Each field saves to the contact attribute named by its key (file uploads are kept with the submission instead). You need an Email or Phone field: it says who filled in the form. Page breaks split long forms into steps with a progress bar.</p>
            {problems.length > 0 && (
              <div className="alert info">{problems.length === 1 ? problems[0] : <ul style={{ margin: 0, paddingLeft: 18 }}>{problems.map((p) => <li key={p}>{p}</li>)}</ul>}</div>
            )}
            {d.rows.map((r, i) => {
              const f = r.f;
              const identifies = f.key === "email" || f.key === "phone";
              if (f.type === "page_break") {
                const pageNo = d.rows.slice(0, i + 1).filter((x) => x.f.type === "page_break").length + 1;
                return (
                  <div className="ff ff-break" key={r.uid}>
                    <div className="ff-break-head"><span>Page {pageNo} starts here</span></div>
                    <Field label="Page title (optional)" hint="Shown at the top of the page, e.g. “About your company”.">
                      <input className="input" value={f.label} placeholder="Next page" onChange={(e) => setRow(r.uid, {}, { label: e.target.value })} />
                    </Field>
                    {rowErr.get(r.uid) && <p className="ff-err">{rowErr.get(r.uid)}</p>}
                    <div className="ff-foot">
                      <span className="ff-num">{i + 1}</span>
                      <div className="row" style={{ gap: 2 }}>
                        <button className="btn ghost sm" disabled={i === 0} onClick={() => move(i, -1)}>Move up</button>
                        <button className="btn ghost sm" disabled={i === d.rows.length - 1} onClick={() => move(i, 1)}>Move down</button>
                        <button className="btn ghost sm" onClick={() => set({ rows: d.rows.filter((x) => x.uid !== r.uid) })}>Remove</button>
                      </div>
                    </div>
                  </div>
                );
              }
              return (
                <div className="ff" key={r.uid}>
                  <div className="ff-main">
                    <Field label={f.type === "hidden" ? "Label (optional)" : "Label"}>
                      <input className="input" value={f.label} placeholder={f.type === "hidden" ? "Only you see it" : "What should they fill in?"}
                        onChange={(e) => setRow(r.uid, {}, r.auto ? { label: e.target.value, key: toKey(e.target.value) } : { label: e.target.value })} />
                    </Field>
                    <Field label="Saves to">
                      <input className="input code" value={f.key} placeholder="attribute_key" aria-label="Attribute key"
                        onChange={(e) => setRow(r.uid, { auto: false }, { key: e.target.value.replace(/\s+/g, "_") })} />
                    </Field>
                    <Field label="Type">
                      <select className="select" value={f.type} onChange={(e) => setRow(r.uid, {}, { type: e.target.value as FormFieldType })}>
                        {Object.entries(TYPE_LABEL).filter(([k]) => k !== "page_break").map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                      </select>
                    </Field>
                  </div>
                  {f.type === "select" && (
                    <Field label="Options" hint="One per line, or separated by commas.">
                      <textarea className="textarea" style={{ minHeight: 70 }} value={r.optText} onChange={(e) => setRow(r.uid, { optText: e.target.value })} placeholder={"Bengaluru\nMumbai\nDelhi"} />
                    </Field>
                  )}
                  {f.type === "hidden" && (
                    <Field label="Value sent" hint={<>On the hosted link, <code>?{f.key || "key"}=…</code> in the address replaces it, e.g. <code>?utm_source=newsletter</code>.</>}>
                      <input className="input" value={f.default ?? ""} onChange={(e) => setRow(r.uid, {}, { default: e.target.value })} placeholder="website" />
                    </Field>
                  )}
                  {f.type === "file" && (
                    <div className="grid-2">
                      <div className="field">
                        <span>Accepted files</span>
                        <div className="checks-box">
                          {UPLOAD_KINDS.map((k) => {
                            const on = !f.accept?.length || k.exts.every((x) => f.accept!.includes(x));
                            const toggle = () => {
                              const cur = f.accept?.length ? f.accept : UPLOAD_KINDS.flatMap((x) => x.exts);
                              const next = on ? cur.filter((x) => !k.exts.includes(x)) : [...cur, ...k.exts];
                              const all = UPLOAD_KINDS.every((x) => x.exts.every((e) => next.includes(e)));
                              setRow(r.uid, {}, { accept: all ? undefined : next.length ? next : cur });
                            };
                            return <label className="check" key={k.label}><input type="checkbox" checked={on} onChange={toggle} /> {k.label}</label>;
                          })}
                        </div>
                        <small>Checked against the file's content, not just its name. Files are private: only people with access to contacts can download them.</small>
                      </div>
                      <Field label="Size limit (MB)" hint={`1 to ${MAX_FILE_MB} MB.`}>
                        <input className="input" type="number" min={1} max={MAX_FILE_MB} value={f.max_mb ?? DEFAULT_FILE_MB}
                          onChange={(e) => setRow(r.uid, {}, { max_mb: Math.max(0, Math.round(Number(e.target.value))) || undefined })} />
                      </Field>
                    </div>
                  )}
                  {rowErr.get(r.uid) && <p className="ff-err">{rowErr.get(r.uid)}</p>}
                  <div className="ff-foot">
                    <div className="row" style={{ gap: 16 }}>
                      <span className="ff-num">{i + 1}</span>
                      {f.type !== "hidden" && (
                        <>
                          <label className="check"><input type="checkbox" checked={!!f.required} onChange={(e) => setRow(r.uid, {}, { required: e.target.checked })} /> Required</label>
                          {f.type !== "file" && (
                            <label className="check" title={identifies ? "Email and phone always show: they identify the submitter." : "Works when the form is embedded with the web SDK and Pulse knows the visitor."}>
                              <input type="checkbox" disabled={identifies} checked={!!f.progressive && !identifies} onChange={(e) => setRow(r.uid, {}, { progressive: e.target.checked })} /> Hide when already known
                            </label>
                          )}
                        </>
                      )}
                    </div>
                    <div className="row" style={{ gap: 2 }}>
                      <button className="btn ghost sm" disabled={i === 0} onClick={() => move(i, -1)}>Move up</button>
                      <button className="btn ghost sm" disabled={i === d.rows.length - 1} onClick={() => move(i, 1)}>Move down</button>
                      <button className="btn ghost sm" onClick={() => set({ rows: d.rows.filter((x) => x.uid !== r.uid) })}>Remove</button>
                    </div>
                  </div>
                </div>
              );
            })}
            <div className="row" style={{ gap: 8 }}>
              <button className="btn sm" disabled={d.rows.length >= MAX_FIELDS} onClick={addField}>Add field</button>
              <button className="btn sm" disabled={d.rows.length >= MAX_FIELDS || d.rows.length === 0} onClick={addPageBreak} title="Split the form into pages: each page is checked before the next one shows">Add page break</button>
            </div>
          </section>

          <section className="section">
            <h3>Form text</h3>
            <Field label="Description" hint="Shown above the fields. Optional.">
              <textarea className="textarea" style={{ minHeight: 70 }} value={d.settings.description ?? ""} onChange={(e) => setS({ description: e.target.value })} placeholder="One email a week with our best offers. Unsubscribe any time." />
            </Field>
            <div className="grid-2">
              <Field label="Button text"><input className="input" value={d.settings.submit_text ?? ""} onChange={(e) => setS({ submit_text: e.target.value })} placeholder="Submit" /></Field>
              <Field label="Accent color" hint="Used for the button and checkboxes.">
                <div className="row">
                  <input type="color" value={d.settings.theme || DEFAULT_ACCENT} onChange={(e) => setS({ theme: e.target.value })} aria-label="Accent color" />
                  <code className="small">{d.settings.theme || DEFAULT_ACCENT}</code>
                  {d.settings.theme && <button className="btn ghost sm" onClick={() => setS({ theme: "" })}>Reset</button>}
                </div>
              </Field>
            </div>
          </section>

          <section className="section">
            <h3>After they submit</h3>
            <div className="row" style={{ gap: 18, marginBottom: 12 }}>
              <label className="check" style={{ marginBottom: 0 }}><input type="radio" name="after" checked={!d.redirect} onChange={() => set({ redirect: false })} /> Show a message</label>
              <label className="check" style={{ marginBottom: 0 }}><input type="radio" name="after" checked={d.redirect} onChange={() => set({ redirect: true })} /> Send them to a page</label>
            </div>
            {d.redirect ? (
              <Field label="Page address"><input className="input" value={d.settings.redirect_url ?? ""} onChange={(e) => setS({ redirect_url: e.target.value })} placeholder="https://example.com/thank-you" /></Field>
            ) : (
              <Field label="Message"><input className="input" value={d.settings.success_message ?? ""} onChange={(e) => setS({ success_message: e.target.value })} placeholder="Thanks! We got it." /></Field>
            )}
            <div className="field">
              <span>Add them to these static segments</span>
              {lists.length === 0 ? (
                <small>You have no static segments yet. <Link to="/segments?type=static">Create one</Link> to collect sign-ups in it.</small>
              ) : (
                <div className="checks-box">
                  {lists.map((l) => {
                    const on = (d.settings.list_ids ?? []).includes(l.id);
                    return (
                      <label className="check" key={l.id}>
                        <input type="checkbox" checked={on} onChange={() => setS({ list_ids: on ? (d.settings.list_ids ?? []).filter((x) => x !== l.id) : [...(d.settings.list_ids ?? []), l.id] })} />
                        {l.name} <span className="faint xs">{l.size.toLocaleString("en-IN")} contacts</span>
                      </label>
                    );
                  })}
                </div>
              )}
            </div>
            <Field label="Add these tags" hint="Comma-separated, e.g. newsletter, webinar-2026.">
              <input className="input" value={d.tagText} onChange={(e) => set({ tagText: e.target.value })} placeholder="newsletter" />
            </Field>
            {suggestions.length > 0 && (
              <div className="tag-input-row" style={{ marginTop: -6, marginBottom: 14 }}>
                <span className="xs muted">Existing tags:</span>
                {suggestions.map((t) => <button key={t} type="button" className="tag-suggest" onClick={() => set({ tagText: [...tags, t].join(", ") })}>+ {t}</button>)}
              </div>
            )}
          </section>

          <section className="section">
            <h3>Consent</h3>
            <Field label="Consent checkbox" hint="If set, they must tick a box with this text to submit. Leave empty for no checkbox.">
              <textarea className="textarea" style={{ minHeight: 60 }} value={d.settings.consent_text ?? ""} onChange={(e) => setS({ consent_text: e.target.value })} placeholder="Yes, send me offers by email and WhatsApp. I can opt out any time." />
            </Field>
            <div className="field">
              <span>Opt them back in to</span>
              <div className="row" style={{ gap: 16 }}>
                {CHANNELS.map((ch) => {
                  const on = (d.settings.resubscribe ?? []).includes(ch);
                  return (
                    <label className="check" key={ch} style={{ marginBottom: 0 }}>
                      <input type="checkbox" disabled={!consentOn} checked={on && consentOn}
                        onChange={() => setS({ resubscribe: on ? (d.settings.resubscribe ?? []).filter((x) => x !== ch) : [...(d.settings.resubscribe ?? []), ch as Channel] })} />
                      {CHANNEL_NAMES[ch]}
                    </label>
                  );
                })}
              </div>
              <small>{consentOn ? "People who opted out of these channels get them again after ticking the box." : "Add consent text first: you can only opt people back in when they agree to it."}</small>
            </div>
          </section>

          {!isNew && hosted && publicKey && <UseIt hosted={hosted} publicKey={publicKey} name={body.name} live={savedLive} />}
          {!isNew && id && <Submissions id={id} />}
        </div>

        <aside style={{ position: "sticky", top: 20 }}>
          <Preview form={body} />
        </aside>
      </div>
    </div>
  );
}

// ---------- preview ----------

function Preview({ form }: { form: ReturnType<typeof toForm> }) {
  const [picked, setView] = useState<"new" | "known" | "done">("new");
  const [page, setPage] = useState(0);
  const s = form.settings;
  const progressive = form.fields.some((f) => f.progressive);
  const view = picked === "known" && !progressive ? "new" : picked;
  const shown = form.fields.filter((f) => f.type !== "hidden" && !(view === "known" && f.progressive && f.key !== "email" && f.key !== "phone"));
  const pages = formPages(shown).filter((p, i) => i === 0 || p.fields.length > 0);
  const cur = Math.min(page, pages.length - 1);
  const lastPage = cur === pages.length - 1;
  const hidden = form.fields.filter((f) => f.type === "hidden");
  return (
    <div>
      <div className="row" style={{ justifyContent: "space-between", marginBottom: 8 }}>
        <p className="small muted">Preview</p>
        <div className="seg-toggle" role="group" aria-label="Preview">
          <button type="button" aria-pressed={view === "new"} onClick={() => setView("new")}>Form</button>
          {progressive && <button type="button" aria-pressed={view === "known"} onClick={() => setView("known")}>Known visitor</button>}
          <button type="button" aria-pressed={view === "done"} onClick={() => setView("done")}>After submit</button>
        </div>
      </div>
      <div className="form-preview" style={{ "--accent": s.theme || DEFAULT_ACCENT } as CSSProperties}>
        {view === "done" ? (
          s.redirect_url
            ? <div className="done"><p style={{ color: "#5b6474" }}>They go to<br /><code style={{ wordBreak: "break-all" }}>{s.redirect_url}</code></p></div>
            : <div className="done" role="status"><h1>{s.success_message || "Thanks! We got it."}</h1></div>
        ) : (
          <form onSubmit={(e) => e.preventDefault()} noValidate>
            <h1>{form.name || "Your form"}</h1>
            {s.description && <p className="desc">{s.description}</p>}
            {pages.length > 1 && (
              <div className="steps">
                <span>Page {cur + 1} of {pages.length}</span>
                <div className="bar"><i style={{ width: `${Math.round(((cur + 1) * 100) / pages.length)}%` }} /></div>
              </div>
            )}
            {pages[cur].title && <p className="page-title">{pages[cur].title}</p>}
            {pages[cur].fields.map((f, i) =>
              f.type === "checkbox" ? (
                <label key={i} className="pcheck"><input type="checkbox" /> <span>{f.label}{f.required && <span className="req"> *</span>}</span></label>
              ) : (
                <label key={i}>
                  {f.label || <span className="faint">No label</span>}{f.required && <span className="req" aria-hidden="true"> *</span>}
                  {f.type === "textarea" ? <textarea /> : f.type === "select" ? (
                    <select defaultValue=""><option value="">Choose…</option>{(f.options ?? []).map((o) => <option key={o}>{o}</option>)}</select>
                  ) : f.type === "file" ? (
                    <>
                      <input type="file" accept={(f.accept ?? []).join(",") || undefined} />
                      <span className="hint">Up to {f.max_mb || DEFAULT_FILE_MB} MB.</span>
                    </>
                  ) : <input type={f.type} placeholder={f.type === "tel" ? "+91 98123 45678" : undefined} />}
                </label>
              ),
            )}
            {lastPage && s.consent_text && <label className="pcheck"><input type="checkbox" /> <span>{s.consent_text}</span></label>}
            <div className="nav">
              {cur > 0 && <button type="button" className="back" onClick={() => setPage(cur - 1)}>Back</button>}
              {lastPage ? <button type="submit">{s.submit_text || "Submit"}</button> : <button type="button" onClick={() => setPage(cur + 1)}>Next</button>}
            </div>
          </form>
        )}
      </div>
      {view === "known" && <p className="xs muted" style={{ marginTop: 8 }}>A visitor Pulse already knows, with values for every “Hide when already known” field. Works when the form is embedded with the web SDK.</p>}
      {view !== "done" && pages.length > 1 && <p className="xs muted" style={{ marginTop: 8 }}>Visitors move on only when the page's fields are filled in correctly; everything is checked again on submit.</p>}
      {view !== "done" && hidden.length > 0 && (
        <p className="xs muted" style={{ marginTop: 8 }}>Also sent, not shown: {hidden.map((f, i) => <span key={i}>{i > 0 && ", "}<code>{f.key || "?"}</code>{f.default ? ` = ${f.default}` : ""}</span>)}</p>
      )}
    </div>
  );
}

// ---------- use it ----------

function Snippet({ text, label }: { text: string; label: string }) {
  const copy = useCopy();
  return (
    <div className="snippet">
      <pre aria-label={label}>{text}</pre>
      <button className="btn sm" onClick={() => copy(text)}>Copy</button>
    </div>
  );
}

function UseIt({ hosted, publicKey, name, live }: { hosted: string; publicKey: string; name: string; live: boolean }) {
  const copy = useCopy();
  const attr = (s: string) => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Use it</h3>{live ? <Status value="ready" /> : <Status value="draft" />}</div>
      <div className="panel-body">
        {!live && <div className="alert info">This form is a draft. The link and embeds work once you switch it to Live and save.</div>}
        <div className="field">
          <span>Link to the form</span>
          <div className="row">
            <input className="input code" style={{ flex: 1, minWidth: 220 }} readOnly value={hosted} onFocus={(e) => e.target.select()} aria-label="Form link" />
            <a className="btn" href={hosted} target="_blank" rel="noreferrer">Open</a>
            <button className="btn" onClick={() => copy(hosted, "Link copied")}>Copy</button>
          </div>
          <small>Share it anywhere: emails, WhatsApp, social posts.</small>
        </div>
        <div className="field" style={{ marginBottom: 0 }}>
          <span>Embed on a web page</span>
          <Snippet label="Embed code" text={`<iframe src="${hosted}" style="width:100%;border:0;min-height:520px" title="${attr(name)}"></iframe>`} />
        </div>
        <div className="field" style={{ marginBottom: 0 }}>
          <span>Embed with the web SDK</span>
          <Snippet label="Web SDK code" text={`<div data-pulse-form="${publicKey}"></div>`} />
          <small>Needs the Pulse web SDK on the page (install it from <Link to="/settings/access">Sign-in &amp; keys</Link> → Your website). This way, “Hide when already known” works for visitors Pulse recognises.</small>
        </div>
      </div>
    </section>
  );
}

// ---------- submissions ----------

type Sub = { fields: Record<string, unknown>; source?: string; page_url?: string; consent?: boolean };

function parseProps(p: string): Sub {
  try {
    const o = JSON.parse(p) as Record<string, unknown>;
    return { fields: (o.fields as Record<string, unknown>) ?? {}, source: o.source as string | undefined, page_url: o.page_url as string | undefined, consent: o.consent === true };
  } catch {
    return { fields: {} };
  }
}

const SOURCE_LABEL: Record<string, string> = { hosted: "Link", sdk: "Web SDK" };

function Submissions({ id }: { id: string }) {
  const q = useQuery({ queryKey: ["form-submissions", id], queryFn: () => api.get<FormStats>(`/v1/forms/${id}/submissions`), refetchInterval: 5000 });
  const st = q.data;
  const days = [...(st?.per_day ?? [])].filter((x) => x.count > 0).sort((a, b) => b.day.localeCompare(a.day)).slice(0, 10);
  const max = Math.max(1, ...days.map((x) => x.count));
  const recent = st?.recent ?? [];
  return (
    <section className="section">
      <h3>Submissions</h3>
      <ErrorNote error={q.error} />
      <div className="figures" style={{ marginTop: 0 }}>
        <div><strong><LiveNumber value={st?.total ?? 0} /></strong><span>in total</span></div>
        <div><strong><LiveNumber value={st?.last_30_days ?? 0} /></strong><span>last 30 days</span></div>
        <div><strong><ContactCount live value={st?.contacts ?? 0} filters={[["form", id]]} /></strong><span>unique contacts</span></div>
      </div>
      {st && st.total === 0 ? (
        <p className="muted small" style={{ marginTop: 14 }}>No submissions yet. They show up here within seconds.</p>
      ) : (
        <>
          {days.length > 0 && (
            <div className="panel" style={{ marginTop: 18 }}>
              <div className="panel-head"><h3 className="small" style={{ fontSize: "var(--fs-md)" }}>By day</h3><span className="xs muted">Days with submissions, newest first</span></div>
              <div className="panel-body">
                <div className="bars">
                  {days.map((x) => (
                    <div className="bar-row" key={x.day}>
                      <span>{fmtDay(x.day)}</span>
                      <div className="bar-track"><div className="bar-fill" style={{ width: `${(x.count / max) * 100}%` }} /></div>
                      <span className="num"><LiveNumber value={x.count} /></span>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          )}
          {recent.length > 0 && (
            <div className="panel" style={{ marginTop: 18 }}>
              <div className="panel-head"><h3 style={{ fontSize: "var(--fs-md)" }}>Recent</h3></div>
              <div className="table-scroll">
                <table className="table">
                  <thead><tr><th>When</th><th>Contact</th><th>Answers</th><th>From</th></tr></thead>
                  <tbody>{recent.map((r, i) => {
                    const p = parseProps(r.props);
                    const entries = Object.entries(p.fields);
                    return (
                      <tr key={`${r.ts}-${r.external_id}-${i}`}>
                        <td className="muted" title={fmtTime(r.ts)} style={{ whiteSpace: "nowrap" }}>{timeAgo(r.ts)}</td>
                        <td><code>{r.external_id}</code></td>
                        <td className="sub-fields">
                          {entries.length === 0 ? <span className="faint">—</span> : entries.map(([k, v], j) => {
                            const up = uploadOf(v);
                            return <span key={k}>{j > 0 && " · "}<b>{k}:</b> {up ? <a href={`/v1/forms/${id}/uploads/${up.id}`} download title="Download (needs access to contacts)">{up.filename}</a> : String(v)}</span>;
                          })}
                          {p.consent && <span> · <b>consent:</b> yes</span>}
                        </td>
                        <td className="muted small" title={p.page_url}>{SOURCE_LABEL[p.source ?? ""] ?? p.source ?? ""}</td>
                      </tr>
                    );
                  })}</tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}
