// The optional AMP version of an email template: a visual block builder (polls, forms, carousels…)
// or AMP code, with a live check from the server (problems by line, size, a script-free preview and
// which email providers send AMP). Rendered by TemplateBody below the email body, so the template
// editor and the one-time campaign wizard both get it. Pure logic lives in ../ampBuilder.ts.
import { keepPreviousData, useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState, type Dispatch, type RefObject, type SetStateAction } from "react";
import { api, type AmpBlock, type AmpBlockType, type AmpCheck, type AmpDoc, type AmpFormField, type AmpImage, type AmpPollResults, type AmpProblem, type AttributeDef, type ChannelContent, type Template } from "../api";
import {
  addBlock, AMP_MAX_BLOCKS, AMP_MAX_BYTES, ampMode, BLOCK_TYPES, blockLabel, canSaveTo, checkDoc, cleanDoc, duplicateBlock, FIELD_TYPES, fieldKeyFrom,
  moveBlock, pollIds, removeBlock, toBlocks, toCode, turnOff, turnOn, updateBlock, type DocProblem,
} from "../ampBuilder";
import { useDebounced } from "../insights";
import { formatBytes } from "../messagePreview";
import { insertAt } from "../personalize";
import { ErrorNote, Field, useToast } from "../ui";
import { ask } from "../guard";
import { TemplatedInput, TemplatedText, VarPicker } from "../VarPicker";
import "./amp.css";
import { ContactCount } from "../ContactCount";

type View = "blocks" | "code";
const GMAIL_NOTE = "Gmail shows AMP emails only from senders Google has registered (your domain needs SPF, DKIM and DMARC, then a registration with Google). Everyone else sees the normal email.";
const FORMS_NOTE = "Forms and polls only work in the email for 60 days after it is sent. After that, people see a short note instead.";

/** AmpSection: "AMP version (optional)" for an email template. onEdit (the editor page's
 * guard.markDirty) hears every change, including the ones made with buttons. */
export function AmpSection({ t, setT, onEdit }: { t: Template; setT: Dispatch<SetStateAction<Template | null>>; onEdit?: () => void }) {
  const c = t.content ?? {};
  const mode = ampMode(c);
  const [view, setView] = useState<View | null>(mode === "off" ? null : mode);
  const toast = useToast();
  // A template loaded (or swapped, in the campaign wizard) with an AMP part opens on it.
  useEffect(() => { if (mode !== "off" && mode !== view) setView(mode); }, [mode]); // eslint-disable-line react-hooks/exhaustive-deps

  const patch = (p: Partial<ChannelContent>) => { onEdit?.(); setT((cur) => cur && { ...cur, content: { ...(cur.content ?? {}), ...p } }); };
  const doc = c.amp_blocks ?? null;
  const setDoc = (d: AmpDoc) => patch({ amp_blocks: d });

  const toCodeM = useMutation({
    mutationFn: () => api.post<AmpCheck>("/v1/templates/amp/check", { amp_blocks: cleanDoc(doc!) }),
    onSuccess: (r) => {
      if (!r.amp) { toast({ text: "Fix the problems in the blocks first, then switch to code.", error: true }); return; }
      patch(toCode(r.amp));
      setView("code");
    },
  });

  const start = (v: View) => { patch(turnOn(v)); setView(v); };
  const remove = async () => {
    if (!(await ask({ title: "Remove the AMP version?", body: "Everyone gets the normal email. Nothing changes until you save.", confirmText: "Remove AMP version", danger: true }))) return;
    patch(turnOff());
    setView(null);
  };
  const switchTo = async (v: View) => {
    if (v === view) return;
    if (v === "code") {
      if (doc && !(await ask({ title: "Switch to code?", body: "The code your blocks make is copied in for you to edit, and the blocks are cleared. You can't turn code back into blocks.", confirmText: "Switch to code" }))) return;
      if (doc) toCodeM.mutate(); else start("code");
    } else {
      if ((c.amp ?? "").trim() && !(await ask({ title: "Start again with blocks?", body: "Your AMP code is discarded.", confirmText: "Discard code", danger: true }))) return;
      patch(toBlocks());
      setView("blocks");
    }
  };

  if (!view) {
    return (
      <section className="panel amp-section" aria-label="AMP version">
        <div className="panel-head"><h3>AMP version <span className="muted">(optional)</span></h3></div>
        <div className="panel-body">
          <p className="small" style={{ marginTop: 0, maxWidth: 720 }}>
            An AMP version lets people vote in a poll, fill in a form or swipe through pictures right inside the email, in Gmail, Yahoo Mail and Mail.ru.
            Everyone else gets the normal email above, so it's always sent too.
          </p>
          <div className="row">
            <button type="button" className="btn primary" onClick={() => start("blocks")}>Add an interactive AMP version</button>
            <button type="button" className="btn ghost sm" onClick={() => start("code")}>I have AMP code</button>
          </div>
        </div>
      </section>
    );
  }

  return (
    <section className="panel amp-section" aria-label="AMP version">
      <div className="panel-head">
        <h3>AMP version <span className="muted">(optional)</span></h3>
        <button type="button" className="btn ghost sm" onClick={() => void remove()}>Remove AMP version</button>
      </div>
      <div className="panel-body">
        <div className="tabs" role="tablist" aria-label="How to make the AMP version">
          <button role="tab" aria-selected={view === "blocks"} onClick={() => void switchTo("blocks")}>Blocks</button>
          <button role="tab" aria-selected={view === "code"} onClick={() => void switchTo("code")} disabled={toCodeM.isPending}>{toCodeM.isPending ? "Copying code…" : "Code"}</button>
        </div>
        <ErrorNote error={toCodeM.error} />
        <AmpWorkspace view={view} doc={doc} setDoc={setDoc} code={c.amp ?? ""} setCode={(amp) => patch({ amp })} templateId={t.id} />
      </div>
    </section>
  );
}

/** AmpWorkspace: the editor on the left, the live check on the right. */
function AmpWorkspace({ view, doc, setDoc, code, setCode, templateId }: {
  view: View; doc: AmpDoc | null; setDoc: (d: AmpDoc) => void; code: string; setCode: (s: string) => void; templateId: number;
}) {
  const docProblems = view === "blocks" && doc ? checkDoc(doc) : [];
  // Blocks with problems aren't sent to the server: the list shows them, and the preview keeps the last good version.
  const body = view === "blocks" ? (doc && !docProblems.length ? { amp_blocks: cleanDoc(doc) } : null) : { amp: code };
  const key = useDebounced(body ? JSON.stringify(body) : "", 600);
  const check = useQuery({
    queryKey: ["amp-check", key],
    queryFn: () => api.post<AmpCheck>("/v1/templates/amp/check", JSON.parse(key)),
    enabled: !!key, placeholderData: keepPreviousData, staleTime: 30_000,
  });
  const editor = useRef<HTMLTextAreaElement>(null);
  const jump = (line: number) => { if (view === "code" && editor.current && line > 0) jumpToLine(editor.current, line); };

  return (
    <div className="amp-layout">
      <div className="amp-edit">
        {view === "blocks" && doc
          ? <BlocksEditor doc={doc} setDoc={setDoc} problems={docProblems} />
          : <CodeEditor value={code} onChange={setCode} problems={check.data?.problems ?? []} textarea={editor} />}
        {view === "blocks" && doc && templateId > 0 && pollIds(doc).length > 0 && <PollResults templateId={templateId} />}
      </div>
      <aside className="amp-side">
        <CheckPanel check={check.data} loading={check.isFetching} error={check.error} view={view} pending={docProblems.length} onLine={jump} />
      </aside>
    </div>
  );
}

function jumpToLine(ta: HTMLTextAreaElement, line: number) {
  const lines = ta.value.split("\n");
  const n = Math.min(line, lines.length);
  let start = 0;
  for (let i = 0; i < n - 1; i++) start += lines[i].length + 1;
  ta.focus();
  ta.setSelectionRange(start, start + (lines[n - 1] ?? "").length);
  const lh = parseFloat(getComputedStyle(ta).lineHeight) || 19;
  ta.scrollTop = Math.max(0, (n - 4) * lh);
}

// ---------- check panel ----------

function CheckPanel({ check, loading, error, view, pending, onLine }: {
  check?: AmpCheck; loading: boolean; error: unknown; view: View; pending: number; onLine: (n: number) => void;
}) {
  const problems = check?.problems ?? [];
  const errors = problems.filter((p) => !p.warning);
  const warnings = problems.filter((p) => p.warning);
  const max = check?.max_size || AMP_MAX_BYTES;
  const size = check?.size ?? 0;
  return (
    <div className="amp-check" aria-live="polite">
      <div className="amp-check-head">
        <h4>Check</h4>
        {loading && <span className="xs muted">Checking…</span>}
      </div>
      <ErrorNote error={error} />
      {pending > 0 ? (
        <div className="alert info">Fix {pending === 1 ? "the problem" : `the ${pending} problems`} in the blocks to update the check and preview.</div>
      ) : check && (
        errors.length === 0
          ? <div className="alert ok">{warnings.length ? `Ready to save, with ${warnings.length === 1 ? "1 suggestion" : `${warnings.length} suggestions`}.` : "No problems: ready to save."}</div>
          : <div className="alert error">{errors.length === 1 ? "1 problem stops" : `${errors.length} problems stop`} the AMP version from saving.</div>
      )}
      {problems.length > 0 && <ProblemList problems={problems} onLine={view === "code" ? onLine : undefined} />}
      {check && (
        <div className="amp-size">
          <div className="row" style={{ justifyContent: "space-between" }}>
            <span className="xs muted">Size</span>
            <span className={`xs ${size > max ? "amp-bad" : "muted"}`}>{formatBytes(size)} of {formatBytes(max)}</span>
          </div>
          <div className="amp-size-bar"><div className={size > max ? "over" : ""} style={{ width: `${Math.min(100, (size / max) * 100)}%` }} /></div>
        </div>
      )}
      <div className="amp-preview">
        <p className="xs muted">Desktop preview — approximate; real AMP behaviour shows in Gmail.</p>
        {check?.preview
          ? <iframe className="amp-frame" title="AMP version preview, with a sample contact" sandbox="" srcDoc={check.preview} />
          : <div className="amp-frame amp-frame-empty small muted">{loading ? "Building the preview…" : "The preview shows here once the AMP version has no problems."}</div>}
      </div>
      <Providers list={check?.providers} />
      <div className="amp-notes">
        <p>{GMAIL_NOTE}</p>
        <p>{FORMS_NOTE} Answers are recorded as the events <code>amp_poll_answered</code> and <code>amp_form_submitted</code>, which you can use in segments and campaigns.</p>
        <p>"Send a test" includes the AMP version.</p>
      </div>
    </div>
  );
}

function ProblemList({ problems, onLine }: { problems: AmpProblem[]; onLine?: (n: number) => void }) {
  return (
    <ul className="amp-problems" aria-label="AMP problems">
      {problems.map((p, i) => (
        <li key={i} className={p.warning ? "warning" : "error"}>
          <span className="amp-tag">{p.warning ? "Suggestion" : "Problem"}</span>
          {p.line > 0
            ? onLine ? <button type="button" className="amp-line" onClick={() => onLine(p.line)}>Line {p.line}</button> : <span className="amp-line">Line {p.line}</span>
            : <span className="amp-line">Whole email</span>}
          <span>{p.message}</span>
        </li>
      ))}
    </ul>
  );
}

function Providers({ list }: { list?: AmpCheck["providers"] }) {
  if (!list) return null;
  return (
    <div className="amp-providers">
      <h4>Your email providers</h4>
      {list.length === 0 && <p className="xs muted">No email provider is set up yet.</p>}
      <ul>
        {list.map((p) => (
          <li key={p.name}>
            <div className="row" style={{ gap: 6 }}><strong className="small">{p.name}</strong><span className={`badge ${p.amp ? "ok" : "warn"}`}>{p.amp ? "Sends AMP" : "Can't send AMP"}</span></div>
            {!p.amp && <p className="xs amp-warn">This provider can't send AMP: the AMP part is left out and the normal email is sent.</p>}
            {p.note && <p className="xs muted">{p.note}</p>}
          </li>
        ))}
      </ul>
    </div>
  );
}

// ---------- poll results ----------

function PollResults({ templateId }: { templateId: number }) {
  const q = useQuery({ queryKey: ["amp-results", templateId], queryFn: () => api.get<AmpPollResults>(`/v1/templates/${templateId}/amp/results`) });
  const polls = Object.entries(q.data?.polls ?? {});
  return (
    <section className="amp-results" aria-label="Poll results">
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h4>Poll results</h4>
        <button type="button" className="btn ghost sm" onClick={() => q.refetch()} disabled={q.isFetching}>{q.isFetching ? "Refreshing…" : "Refresh"}</button>
      </div>
      <ErrorNote error={q.error} />
      {q.data && polls.length === 0 && <p className="xs muted">Results show here for the saved version's polls.</p>}
      {polls.map(([id, p]) => (
        <div key={id} className="amp-result">
          <p className="small"><strong>{p.question}</strong> <span className="xs muted">({id}, <ContactCount value={p.total} filters={[["amp_poll", `${templateId}/${id}`]]} /> {p.total === 1 ? "vote" : "votes"})</span></p>
          <table className="table">
            <thead><tr><th>Answer</th><th style={{ textAlign: "right" }}>Votes</th><th style={{ width: "40%" }}>Share</th></tr></thead>
            <tbody>
              {p.items.map((it) => (
                <tr key={it.label}>
                  <td>{it.label}</td>
                  <td style={{ textAlign: "right" }}><ContactCount value={it.count} filters={[["amp_poll", `${templateId}/${id}`], ["answer", it.label]]} /></td>
                  <td><div className="amp-share"><div style={{ width: `${it.percent}%` }} /></div><span className="xs muted">{it.percent}%</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
      <p className="xs muted">From emails sent with the saved template. Unsaved changes don't count yet.</p>
    </section>
  );
}

// ---------- code editor ----------

function CodeEditor({ value, onChange, problems, textarea }: { value: string; onChange: (s: string) => void; problems: AmpProblem[]; textarea: RefObject<HTMLTextAreaElement | null> }) {
  const gutter = useRef<HTMLDivElement>(null);
  const sel = useRef<[number, number] | null>(null);
  const lines = value.split("\n").length;
  const bad = new Map<number, boolean>();
  for (const p of problems) if (p.line > 0) bad.set(p.line, (bad.get(p.line) ?? false) || !p.warning);
  const insert = (tag: string) => {
    const [s, e] = sel.current ?? [value.length, value.length];
    const r = insertAt(value, tag, s, e);
    onChange(r.value);
    requestAnimationFrame(() => { const x = textarea.current; if (x) { x.focus(); x.setSelectionRange(r.cursor, r.cursor); } });
  };
  return (
    <div>
      <div className="row" style={{ justifyContent: "space-between", marginBottom: 6 }}>
        <span className="xs muted">AMP for Email code. Pulse fills variables per contact and connects forms with <code>action-xhr="pulse:submit/&lt;form id&gt;"</code>.</span>
        <VarPicker onInsert={insert} />
      </div>
      <div className="amp-code">
        <div className="amp-gutter" ref={gutter} aria-hidden="true">
          {Array.from({ length: lines }, (_, i) => <div key={i} className={bad.has(i + 1) ? (bad.get(i + 1) ? "error" : "warning") : undefined}>{i + 1}</div>)}
        </div>
        <textarea ref={textarea} className="amp-code-text" value={value} wrap="off" spellCheck={false} aria-label="AMP code"
          onChange={(e) => onChange(e.target.value)}
          onSelect={(e) => { const x = e.currentTarget; sel.current = [x.selectionStart, x.selectionEnd]; }}
          onScroll={(e) => { if (gutter.current) gutter.current.scrollTop = e.currentTarget.scrollTop; }} />
      </div>
    </div>
  );
}

// ---------- blocks editor ----------

const STYLE_DEFAULTS = { background: "#f4f6fa", text: "#152238", accent: "#2747d6" };
const fullHex = (c: string | undefined, def: string) => {
  if (!c) return def;
  return /^#[0-9a-fA-F]{3}$/.test(c) ? `#${c[1]}${c[1]}${c[2]}${c[2]}${c[3]}${c[3]}` : /^#[0-9a-fA-F]{6}$/.test(c) ? c : def;
};

function BlocksEditor({ doc, setDoc, problems }: { doc: AmpDoc; setDoc: (d: AmpDoc) => void; problems: DocProblem[] }) {
  const [openId, setOpenId] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const st = doc.style ?? {};
  const top = problems.filter((p) => p.block === null);
  const add = (type: AmpBlockType) => {
    const d = addBlock(doc, type);
    setDoc(d);
    setOpenId(d.blocks[d.blocks.length - 1].id);
    setAdding(false);
  };
  return (
    <div className="amp-blocks">
      <div className="amp-style" role="group" aria-label="Look">
        {(["background", "text", "accent"] as const).map((k) => (
          <label key={k} className="amp-color">
            <input type="color" value={fullHex(st[k], STYLE_DEFAULTS[k])} onChange={(e) => setDoc({ ...doc, style: { ...st, [k]: e.target.value } })} />
            <span className="xs">{k === "background" ? "Page colour" : k === "text" ? "Text colour" : "Buttons and bars"}</span>
          </label>
        ))}
        <label className="amp-color">
          <select className="select inline" value={st.font ?? "sans"} onChange={(e) => setDoc({ ...doc, style: { ...st, font: e.target.value as "sans" | "serif" } })} aria-label="Font">
            <option value="sans">Sans serif (Arial)</option>
            <option value="serif">Serif (Georgia)</option>
          </select>
        </label>
      </div>
      {top.length > 0 && <ul className="amp-problems">{top.map((p) => <li key={p.message} className="error"><span>{p.message}</span></li>)}</ul>}

      <ol className="amp-block-list">
        {doc.blocks.map((b, i) => {
          const mine = problems.filter((p) => p.block === i);
          const open = openId === b.id;
          return (
            <li key={i} className={`amp-card${open ? " open" : ""}${mine.length ? " has-problems" : ""}`}>
              <div className="amp-card-head">
                <button type="button" className="amp-card-title" aria-expanded={open} onClick={() => setOpenId(open ? null : b.id)}>
                  <span className="amp-n">{i + 1}</span>
                  <strong>{blockLabel(b.type)}</strong>
                  <span className="xs muted amp-summary">{summary(b)}</span>
                </button>
                <div className="amp-card-actions">
                  <button type="button" className="btn ghost sm" aria-label={`Move block ${i + 1} up`} title="Move up" disabled={i === 0} onClick={() => setDoc(moveBlock(doc, i, -1))}>↑</button>
                  <button type="button" className="btn ghost sm" aria-label={`Move block ${i + 1} down`} title="Move down" disabled={i === doc.blocks.length - 1} onClick={() => setDoc(moveBlock(doc, i, 1))}>↓</button>
                  <button type="button" className="btn ghost sm" disabled={doc.blocks.length >= AMP_MAX_BLOCKS} onClick={() => setDoc(duplicateBlock(doc, i))}>Duplicate</button>
                  <button type="button" className="btn ghost sm" onClick={() => void ask({ title: `Delete this ${blockLabel(b.type).toLowerCase()} block?`, body: "Nothing changes in the saved template until you save.", confirmText: "Delete block", danger: true })
                    .then((ok) => ok && setDoc(removeBlock(doc, i)))}>Delete</button>
                </div>
              </div>
              {mine.length > 0 && <ul className="amp-problems amp-card-problems">{mine.map((p) => <li key={p.message} className="error"><span>{p.message}</span></li>)}</ul>}
              {open && (
                <div className="amp-card-body">
                  <BlockForm key={b.id} b={b} set={(p) => setDoc(updateBlock(doc, i, p))} />
                  <Field label="Block name" hint={b.type === "poll" || b.type === "form" ? "Answers are recorded under this name (the event's block property), so keep it once the email is sent." : "Lowercase letters, digits, - and _."}>
                    <input className="input amp-id" value={b.id} onChange={(e) => { const id = e.target.value.toLowerCase(); setDoc(updateBlock(doc, i, { id })); setOpenId(id); }} />
                  </Field>
                </div>
              )}
            </li>
          );
        })}
      </ol>
      {doc.blocks.length === 0 && <p className="small muted">No blocks yet. Add one to start.</p>}

      <div className="amp-add">
        <button type="button" className="btn" aria-expanded={adding} disabled={doc.blocks.length >= AMP_MAX_BLOCKS} onClick={() => setAdding(!adding)}>+ Add a block</button>
        {doc.blocks.length >= AMP_MAX_BLOCKS && <span className="xs muted"> {AMP_MAX_BLOCKS} blocks at most.</span>}
        {adding && (
          <div className="amp-add-menu" role="menu">
            {BLOCK_TYPES.map((x) => (
              <button key={x.type} type="button" role="menuitem" onClick={() => add(x.type)}>
                <strong>{x.label}</strong><span className="xs muted">{x.hint}</span>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function summary(b: AmpBlock): string {
  switch (b.type) {
    case "image": return b.image?.alt || b.image?.src || "";
    case "carousel": case "gallery": return `${b.items?.length ?? 0} images`;
    case "accordion": return `${b.sections?.length ?? 0} sections`;
    case "poll": return `${b.text ?? ""} (${b.options?.length ?? 0} answers)`;
    case "form": return `${b.text ? b.text + ", " : ""}${b.fields?.length ?? 0} fields`;
    default: return b.text ?? "";
  }
}

function BlockForm({ b, set }: { b: AmpBlock; set: (p: Partial<AmpBlock>) => void }) {
  switch (b.type) {
    case "heading":
      return <Field label="Heading"><TemplatedInput label="Heading" value={b.text ?? ""} onChange={(text) => set({ text })} /></Field>;
    case "text":
      return <Field label="Text" hint="Leave a blank line between paragraphs."><TemplatedText label="Text" rows={5} value={b.text ?? ""} onChange={(text) => set({ text })} /></Field>;
    case "image":
      return <ImageFields im={b.image ?? { src: "" }} onChange={(image) => set({ image })} caption />;
    case "button":
      return (
        <div className="grid-2">
          <Field label="Button label"><TemplatedInput label="Button label" value={b.text ?? ""} onChange={(text) => set({ text })} /></Field>
          <Field label="Opens this web address"><TemplatedInput label="Opens this web address" placeholder="https://…" value={b.url ?? ""} onChange={(url) => set({ url })} /></Field>
        </div>
      );
    case "carousel": case "gallery":
      return (
        <>
          <ImageList items={b.items ?? []} onChange={(items) => set({ items })} caption={b.type === "carousel"} />
          <div className="grid-2">
            <Field label="Height (pixels)" hint={b.type === "carousel" ? "Slides are 600 pixels wide." : "Pictures keep this height and scroll sideways."}>
              <input className="input" type="number" min={50} max={2000} value={b.height ?? ""} onChange={(e) => set({ height: e.target.value ? Number(e.target.value) : undefined })} />
            </Field>
            {b.type === "carousel" && (
              <Field label="Move to the next slide every (seconds)" hint="0 = only when swiped (up to 30).">
                <input className="input" type="number" min={0} max={30} value={b.autoplay ?? 0} onChange={(e) => set({ autoplay: Number(e.target.value) || 0 })} />
              </Field>
            )}
          </div>
        </>
      );
    case "accordion": {
      const s = b.sections ?? [];
      return (
        <div className="amp-list">
          {s.map((x, j) => (
            <div key={j} className="amp-sub">
              <div className="row" style={{ justifyContent: "space-between" }}><span className="xs muted">Section {j + 1}</span>
                <button type="button" className="btn ghost sm" onClick={() => set({ sections: s.filter((_, k) => k !== j) })}>Remove</button></div>
              <Field label="Title"><TemplatedInput label="Section title" value={x.title} onChange={(title) => set({ sections: s.map((y, k) => (k === j ? { ...y, title } : y)) })} /></Field>
              <Field label="Shown when opened"><TemplatedText label="Section text" rows={3} value={x.body} onChange={(body) => set({ sections: s.map((y, k) => (k === j ? { ...y, body } : y)) })} /></Field>
            </div>
          ))}
          {s.length < 10 && <div><button type="button" className="btn sm" onClick={() => set({ sections: [...s, { title: "", body: "" }] })}>Add a section</button></div>}
        </div>
      );
    }
    case "poll": {
      const o = b.options ?? [];
      return (
        <>
          <Field label="Question"><TemplatedInput label="Question" value={b.text ?? ""} onChange={(text) => set({ text })} /></Field>
          <div className="field" role="group" aria-label="Answers">
            <span>Answers <span className="muted">(2 to 8)</span></span>
            {o.map((x, j) => (
              <div key={j} className="row" style={{ flexWrap: "nowrap" }}>
                <input className="input" value={x} aria-label={`Answer ${j + 1}`} maxLength={100} onChange={(e) => set({ options: o.map((y, k) => (k === j ? e.target.value : y)) })} />
                <button type="button" className="btn ghost sm" disabled={o.length <= 2} onClick={() => set({ options: o.filter((_, k) => k !== j) })}>Remove</button>
              </div>
            ))}
            {o.length < 8 && <div><button type="button" className="btn sm" onClick={() => set({ options: [...o, ""] })}>Add an answer</button></div>}
          </div>
          <label className="check"><input type="checkbox" checked={!!b.show_results} onChange={(e) => set({ show_results: e.target.checked || undefined })} />Show everyone's results after voting</label>
          <SubmitTexts b={b} set={set} submit="Vote" success="Thanks for voting!" />
          <p className="xs muted">Each vote is recorded as the event <code>amp_poll_answered</code> (with the answer), so you can build segments and campaigns on it.</p>
        </>
      );
    }
    case "form":
      return (
        <>
          <Field label="Title (optional)"><TemplatedInput label="Form title" value={b.text ?? ""} onChange={(text) => set({ text })} /></Field>
          <FormFields fields={b.fields ?? []} onChange={(fields) => set({ fields })} />
          <SubmitTexts b={b} set={set} submit="Send" success="Thanks! We got your answer." />
          <p className="xs muted">Each answer is recorded as the event <code>amp_form_submitted</code> with the fields' saved names, so you can build segments and campaigns on it.</p>
        </>
      );
  }
}

function SubmitTexts({ b, set, submit, success }: { b: AmpBlock; set: (p: Partial<AmpBlock>) => void; submit: string; success: string }) {
  return (
    <div className="grid-2">
      <Field label="Button label"><input className="input" value={b.submit_text ?? ""} placeholder={submit} onChange={(e) => set({ submit_text: e.target.value || undefined })} /></Field>
      <Field label="Thank-you message"><input className="input" value={b.success_text ?? ""} placeholder={success} onChange={(e) => set({ success_text: e.target.value || undefined })} /></Field>
    </div>
  );
}

function ImageFields({ im, onChange, caption }: { im: AmpImage; onChange: (im: AmpImage) => void; caption?: boolean }) {
  const up = (p: Partial<AmpImage>) => onChange({ ...im, ...p });
  return (
    <div className="grid-2">
      <Field label="Image address" hint="A link to the picture, starting with https://"><input className="input" value={im.src} placeholder="https://…/picture.jpg" onChange={(e) => up({ src: e.target.value.trim() })} /></Field>
      <Field label="Description (for screen readers)"><input className="input" value={im.alt ?? ""} onChange={(e) => up({ alt: e.target.value || undefined })} /></Field>
      <Field label="Opens this web address when tapped (optional)"><input className="input" value={im.link ?? ""} placeholder="https://…" onChange={(e) => up({ link: e.target.value.trim() || undefined })} /></Field>
      {caption && <Field label="Caption (optional)"><input className="input" value={im.caption ?? ""} onChange={(e) => up({ caption: e.target.value || undefined })} /></Field>}
    </div>
  );
}

function ImageList({ items, onChange, caption }: { items: AmpImage[]; onChange: (l: AmpImage[]) => void; caption?: boolean }) {
  return (
    <div className="amp-list">
      {items.map((im, j) => (
        <div key={j} className="amp-sub">
          <div className="row" style={{ justifyContent: "space-between" }}><span className="xs muted">Image {j + 1}</span>
            <button type="button" className="btn ghost sm" disabled={items.length <= 2} onClick={() => onChange(items.filter((_, k) => k !== j))}>Remove</button></div>
          <ImageFields im={im} caption={caption} onChange={(x) => onChange(items.map((y, k) => (k === j ? x : y)))} />
        </div>
      ))}
      {items.length < 10 && <div><button type="button" className="btn sm" onClick={() => onChange([...items, { src: "" }])}>Add an image</button></div>}
    </div>
  );
}

function FormFields({ fields, onChange }: { fields: AmpFormField[]; onChange: (f: AmpFormField[]) => void }) {
  const attrs = useQuery({ queryKey: ["attributes"], queryFn: () => api.get<{ attributes: AttributeDef[] }>("/v1/attributes"), staleTime: 60_000 });
  const targets = (attrs.data?.attributes ?? []).filter((a) => canSaveTo(a.key));
  const up = (j: number, p: Partial<AmpFormField>) => onChange(fields.map((f, k) => (k === j ? { ...f, ...p } : f)));
  return (
    <div className="amp-list" role="group" aria-label="Fields">
      <span className="small" style={{ fontWeight: 600 }}>Fields <span className="muted">(1 to 10)</span></span>
      {fields.map((f, j) => {
        const others = fields.filter((_, k) => k !== j).map((x) => x.key);
        const autoKey = f.key === fieldKeyFrom(f.label, others);
        return (
          <div key={j} className="amp-sub">
            <div className="row" style={{ justifyContent: "space-between" }}><span className="xs muted">Field {j + 1}</span>
              <button type="button" className="btn ghost sm" disabled={fields.length <= 1} onClick={() => onChange(fields.filter((_, k) => k !== j))}>Remove</button></div>
            <div className="grid-2">
              <Field label="Label"><input className="input" value={f.label} onChange={(e) => up(j, { label: e.target.value, ...(autoKey ? { key: fieldKeyFrom(e.target.value, others) } : {}) })} /></Field>
              <Field label="Type">
                <select className="select" value={f.type} onChange={(e) => { const type = e.target.value as AmpFormField["type"]; up(j, { type, options: type === "select" ? f.options ?? [] : undefined }); }}>
                  {FIELD_TYPES.map((x) => <option key={x.type} value={x.type}>{x.label}</option>)}
                </select>
              </Field>
              <Field label="Saved as" hint="The answer's name in the event (lowercase letters, digits and _).">
                <input className="input" value={f.key} onChange={(e) => up(j, { key: e.target.value.toLowerCase() })} />
              </Field>
              <Field label="Also save the answer to the contact (optional)" hint={targets.length ? "Only your own contact fields; email, phone, name and consent can't be changed this way." : "Add your own contact fields under Settings to save answers to them."}>
                <select className="select" value={f.save_to ?? ""} onChange={(e) => up(j, { save_to: e.target.value || undefined })}>
                  <option value="">Don't save to the contact</option>
                  {targets.map((a) => <option key={a.key} value={a.key}>{a.label || a.key}</option>)}
                  {f.save_to && !targets.some((a) => a.key === f.save_to) && <option value={f.save_to}>{f.save_to}</option>}
                </select>
              </Field>
            </div>
            {f.type === "select" && <OptionsInput options={f.options ?? []} onChange={(options) => up(j, { options })} />}
            <label className="check"><input type="checkbox" checked={!!f.required} onChange={(e) => up(j, { required: e.target.checked || undefined })} />Required</label>
          </div>
        );
      })}
      {fields.length < 10 && (
        <div><button type="button" className="btn sm" onClick={() => onChange([...fields, { key: fieldKeyFrom("New field", fields.map((f) => f.key)), label: "New field", type: "text" }])}>Add a field</button></div>
      )}
    </div>
  );
}

/** OptionsInput: a drop-down's choices, one per line (empty lines are dropped from what's stored). */
function OptionsInput({ options, onChange }: { options: string[]; onChange: (o: string[]) => void }) {
  const [text, setText] = useState(options.join("\n"));
  return (
    <Field label="Choices" hint="One per line, 1 to 20.">
      <textarea className="textarea" rows={4} value={text} onChange={(e) => { setText(e.target.value); onChange(e.target.value.split("\n").map((s) => s.trim()).filter(Boolean)); }} />
    </Field>
  );
}

