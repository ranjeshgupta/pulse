// AI help (O6): subject line ideas and rewrites in the template editor, translation of a
// template's language version, and a segment from a sentence. Hidden when the server has no AI
// key. Nothing is applied without a click, and replacing what's there asks first.
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, type Rule, type Template } from "./api";
import { ask } from "./guard";
import { ErrorNote, Field, Modal } from "./ui";

export function useAI() {
  return useQuery({ queryKey: ["ai-status"], queryFn: () => api.get<{ enabled: boolean; model: string }>("/v1/ai/status"), staleTime: 300_000, retry: false }).data?.enabled ?? false;
}

export const STYLES: { key: string; label: string }[] = [
  { key: "shorter", label: "Shorter" }, { key: "friendlier", label: "Friendlier" }, { key: "simpler", label: "Simpler words" },
  { key: "urgent", label: "More urgent" }, { key: "formal", label: "More formal" }, { key: "fix", label: "Fix spelling and grammar" },
];

/** mainField is the part of a template the AI rewrites: email HTML, SMS / RCS text, or push / inbox body. */
export function mainField(t: Template): { key: "html" | "text" | "body"; label: string; value: string } | null {
  if (t.channel === "email") return { key: "html", label: "Email body", value: t.html };
  if (t.channel === "sms" || t.channel === "rcs") return { key: "text", label: "Message", value: t.content?.text ?? "" };
  if (t.channel === "push" || t.channel === "inbox") return { key: "body", label: "Body", value: t.content?.body ?? "" };
  return null;
}

/** TemplateAI: the template editor's ✨ AI button and its panel. builderLocked: the email is in the drag-and-drop builder. */
export function TemplateAI({ t, setT, builderLocked }: { t: Template; setT: (t: Template) => void; builderLocked: boolean }) {
  const enabled = useAI();
  const [open, setOpen] = useState(false);
  if (!enabled || t.channel === "whatsapp") return null;
  return (
    <>
      <button type="button" className="btn" onClick={() => setOpen(true)}>✨ AI</button>
      {open && <TemplateAIPanel t={t} setT={setT} builderLocked={builderLocked} onClose={() => setOpen(false)} />}
    </>
  );
}

function TemplateAIPanel({ t, setT, builderLocked, onClose }: { t: Template; setT: (t: Template) => void; builderLocked: boolean; onClose: () => void }) {
  const field = mainField(t);
  const titleKey = t.channel === "email" ? "subject" : t.channel === "push" || t.channel === "inbox" ? "title" : null;
  const currentTitle = t.channel === "email" ? t.subject : t.content?.title ?? "";
  const [style, setStyle] = useState("shorter");
  const ideas = useMutation({ mutationFn: () => api.post<{ subjects: string[] }>("/v1/ai/subject-lines", { content: field?.value ?? "", subject: currentTitle, n: 5 }) });
  const rewrite = useMutation({ mutationFn: () => api.post<{ text: string }>("/v1/ai/rewrite", { text: field!.value, style, channel: t.channel }) });
  const useTitle = (s: string) => {
    setT(t.channel === "email" ? { ...t, subject: s } : { ...t, content: { ...t.content, title: s } });
    onClose();
  };
  const useRewrite = (text: string) => {
    if (!field) return;
    if (field.key === "html") setT({ ...t, html: text, builder: undefined });
    else setT({ ...t, content: { ...t.content, [field.key]: text } });
    onClose();
  };
  return (
    <Modal wide title="✨ AI help" onClose={onClose} guard={false}>
      <p className="small muted" style={{ marginTop: 0 }}>Suggestions only: nothing changes until you pick one, and you can still edit it (or leave without saving). {"{{…}}"} variables are kept as they are.</p>
      {titleKey && (
        <section style={{ marginBottom: 18 }}>
          <div className="row" style={{ justifyContent: "space-between" }}>
            <h3 style={{ margin: 0 }}>{t.channel === "email" ? "Subject line ideas" : "Title ideas"}</h3>
            <button type="button" className="btn sm" disabled={ideas.isPending || !(field?.value || currentTitle)} onClick={() => ideas.mutate()}>{ideas.isPending ? "Thinking…" : ideas.data ? "More ideas" : "Suggest"}</button>
          </div>
          <ErrorNote error={ideas.error} />
          {ideas.data && <ul className="ai-list">{ideas.data.subjects.map((x) => (
            <li key={x}><span>{x}</span><button type="button" className="btn ghost sm" onClick={() => useTitle(x)}>Use</button></li>
          ))}</ul>}
        </section>
      )}
      {field && (
        <section>
          <h3 style={{ margin: "0 0 8px" }}>Rewrite the {field.label.toLowerCase()}</h3>
          {builderLocked ? <p className="small muted">This email is built with the drag-and-drop builder. Switch to Rich text or HTML to rewrite its words (the layout becomes HTML).</p> : (
            <>
              <div className="row" style={{ gap: 8 }}>
                <select className="select" style={{ maxWidth: 260 }} value={style} aria-label="How to rewrite it" onChange={(e) => { setStyle(e.target.value); rewrite.reset(); }}>
                  {STYLES.map((s) => <option key={s.key} value={s.key}>{s.label}</option>)}
                </select>
                <button type="button" className="btn sm" disabled={rewrite.isPending || !field.value.trim()} onClick={() => rewrite.mutate()}>{rewrite.isPending ? "Rewriting…" : "Rewrite"}</button>
              </div>
              <ErrorNote error={rewrite.error} />
              {rewrite.data && (
                <div className="ai-compare">
                  <Field label="Now">{field.key === "html" ? <div className="ai-html" dangerouslySetInnerHTML={{ __html: sanitizeForPreview(field.value) }} /> : <p className="ai-text">{field.value}</p>}</Field>
                  <Field label="Suggested">{field.key === "html" ? <div className="ai-html" dangerouslySetInnerHTML={{ __html: sanitizeForPreview(rewrite.data.text) }} /> : <p className="ai-text">{rewrite.data.text}</p>}</Field>
                  <div className="row" style={{ gridColumn: "1 / -1", justifyContent: "flex-end" }}>
                    <button type="button" className="btn primary" onClick={() => useRewrite(rewrite.data!.text)}>Use the suggestion</button>
                  </div>
                </div>
              )}
            </>
          )}
        </section>
      )}
    </Modal>
  );
}

/** sanitizeForPreview strips scripts, event handlers and javascript: links before showing HTML. */
export function sanitizeForPreview(html: string): string {
  const doc = new DOMParser().parseFromString(html, "text/html");
  doc.querySelectorAll("script,style,iframe,object,embed,form,meta,link,base").forEach((n) => n.remove());
  doc.querySelectorAll("*").forEach((el) => {
    for (const a of [...el.attributes]) {
      if (/^on/i.test(a.name) || (/^(href|src|xlink:href)$/i.test(a.name) && /^\s*javascript:/i.test(a.value))) el.removeAttribute(a.name);
    }
  });
  return doc.body.innerHTML;
}

/** TranslateWithAI fills a language version from the default's words. */
export function TranslateWithAI({ fields, locale, hasContent, onDone }: {
  fields: Record<string, string>; locale: string; hasContent: boolean; onDone: (out: Record<string, string>) => void;
}) {
  const enabled = useAI();
  const m = useMutation({
    mutationFn: () => api.post<{ fields: Record<string, string> }>("/v1/ai/translate", { fields, locale }),
    onSuccess: (r) => onDone(r.fields),
  });
  if (!enabled) return null;
  const go = async () => {
    if (hasContent && !(await ask({ title: "Replace this translation?", body: "The AI's translation replaces what's written here (you can still edit it, or leave without saving).", confirmText: "Translate" }))) return;
    m.mutate();
  };
  return (
    <>
      <button type="button" className="btn sm" disabled={m.isPending} onClick={() => void go()}>{m.isPending ? "Translating…" : "✨ Translate from Default"}</button>
      {m.error && <span className="small" style={{ color: "var(--danger)" }}>{(m.error as Error).message}</span>}
    </>
  );
}

/** SegmentFromWords: describe an audience, get a rule to check and edit. */
export function SegmentFromWords({ hasRule, onRule }: { hasRule: boolean; onRule: (r: Rule) => void }) {
  const enabled = useAI();
  const [text, setText] = useState("");
  const m = useMutation({ mutationFn: () => api.post<{ rule: Rule; note: string }>("/v1/ai/segment", { description: text.trim() }) });
  if (!enabled) return null;
  const go = async () => {
    if (hasRule && !(await ask({ title: "Replace the conditions?", body: "The conditions below are replaced by the AI's (you can still edit them, or leave without saving).", confirmText: "Replace" }))) return;
    m.mutate(undefined, { onSuccess: (r) => onRule(r.rule) });
  };
  return (
    <div className="panel ai-segment">
      <form className="panel-body" onSubmit={(e) => { e.preventDefault(); if (text.trim()) void go(); }}>
        <label className="small" htmlFor="ai-seg">✨ Describe who should be in it</label>
        <div className="row" style={{ flexWrap: "nowrap", gap: 8, marginTop: 6 }}>
          <input id="ai-seg" className="input" placeholder="Bought twice in the last 90 days, live in Pune, not unsubscribed" value={text} onChange={(e) => setText(e.target.value)} data-no-dirty />
          <button className="btn" disabled={!text.trim() || m.isPending}>{m.isPending ? "Building…" : "Build"}</button>
        </div>
        <ErrorNote error={m.error} />
        {m.data && <p className="small muted" style={{ margin: "8px 0 0" }}>{m.data.note} Check the conditions below before saving.</p>}
      </form>
    </div>
  );
}
