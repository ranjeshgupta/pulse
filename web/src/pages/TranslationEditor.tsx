// Multi-language templates (O6): the language bar above the template editor, and the editor of one
// translation (only the words: subject, body, text; links, images and settings stay the default's).
import { useState, type Dispatch, type SetStateAction } from "react";
import type { ChannelContent, Template, Translation } from "../api";
import { askDelete } from "../guard";
import { LANGUAGES, languageName, validLocale } from "../languages";
import { RichText } from "../RichText";
import { TranslateWithAI } from "../AIHelp";
import { EMAIL_STYLE, unwrapRichText, wrapRichText } from "../richTextHtml";
import { Field } from "../ui";
import { TemplatedInput, TemplatedText } from "../VarPicker";

/** LanguageBar: Default plus each language; "+ Add language" adds one. lang "" is the default. */
export function LanguageBar({ t, lang, setLang, setT }: { t: Template; lang: string; setLang: (l: string) => void; setT: (t: Template) => void }) {
  const [adding, setAdding] = useState(false);
  const [custom, setCustom] = useState("");
  const trs = t.translations ?? [];
  const add = (code: string) => {
    if (!validLocale(code) || trs.some((x) => x.locale.toLowerCase() === code.toLowerCase())) return;
    setT({ ...t, translations: [...trs, { locale: code }] });
    setLang(code); setAdding(false); setCustom("");
  };
  return (
    <div className="lang-bar" role="tablist" aria-label="Template languages">
      <span className="small muted">🌐 Language</span>
      <button type="button" role="tab" aria-selected={lang === ""} onClick={() => setLang("")}>Default</button>
      {trs.map((x) => <button key={x.locale} type="button" role="tab" aria-selected={lang === x.locale} onClick={() => setLang(x.locale)}>{languageName(x.locale)}</button>)}
      {trs.length < 20 && (adding ? (
        <span className="lang-add">
          <select className="select sm" autoFocus value="" aria-label="Language to add" onChange={(e) => e.target.value && add(e.target.value)}>
            <option value="">Choose…</option>
            {LANGUAGES.filter((l) => !trs.some((x) => x.locale === l.code)).map((l) => <option key={l.code} value={l.code}>{l.name} ({l.code})</option>)}
          </select>
          <input className="input sm" style={{ width: 110 }} placeholder="or a code: hi-IN" value={custom} aria-label="Language code"
            onChange={(e) => setCustom(e.target.value.trim())} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(custom); } }} />
          <button type="button" className="btn sm" disabled={!validLocale(custom)} onClick={() => add(custom)}>Add</button>
          <button type="button" className="btn ghost sm" onClick={() => setAdding(false)}>Cancel</button>
        </span>
      ) : <button type="button" className="btn ghost sm" onClick={() => setAdding(true)}>+ Add language</button>)}
    </div>
  );
}

/** defaultFields are the default's words a translation covers. */
function defaultFields(t: Template): Record<string, string> {
  const d = t.content ?? {};
  const f: Record<string, string> = t.channel === "email" ? { subject: t.subject, html: t.html }
    : t.channel === "sms" || t.channel === "rcs" ? { text: d.text ?? "" }
    : t.channel === "push" || t.channel === "inbox" ? { title: d.title ?? "", body: d.body ?? "" } : {};
  return Object.fromEntries(Object.entries(f).filter(([, v]) => v.trim() !== ""));
}

function hasWords(tr: Translation): boolean {
  return !!(tr.subject?.trim() || tr.html?.trim() || Object.values(tr.content ?? {}).some((v) => typeof v === "string" && v.trim()));
}

/**
 * applyAITranslation puts an AI translation into the translation for locale, in the template as it
 * is now (other edits made while waiting stay). Nothing changes if that language was removed.
 */
export function applyAITranslation<T extends Template | null>(cur: T, locale: string, f: Record<string, string>): T {
  if (!cur) return cur;
  const trs = cur.translations ?? [];
  if (!trs.some((x) => x.locale === locale)) return cur;
  const words = Object.fromEntries(Object.entries(f).filter(([k]) => k !== "subject" && k !== "html"));
  return {
    ...cur,
    translations: trs.map((x) => x.locale !== locale ? x : cur.channel === "email"
      ? { ...x, subject: f.subject ?? x.subject, html: f.html ?? x.html }
      : { ...x, content: { ...(x.content ?? {}), ...words } }),
  };
}

/** TranslationEditor edits one translation; empty fields fall back to the default's. Mount it with
 * key={locale}, so nothing of one language's editor carries over to another's. */
export function TranslationEditor({ t, locale, setT, onRemoved }: { t: Template; locale: string; setT: Dispatch<SetStateAction<Template | null>>; onRemoved: () => void }) {
  const trs = t.translations ?? [];
  const tr = trs.find((x) => x.locale === locale);
  if (!tr) return null;
  const set = (p: Partial<Translation>) => setT({ ...t, translations: trs.map((x) => (x.locale === locale ? { ...x, ...p } : x)) });
  const c = tr.content ?? {};
  const setC = (p: Partial<ChannelContent>) => set({ content: { ...c, ...p } });
  const d = t.content ?? {};
  const name = languageName(locale);
  const copyDefault = () => {
    if (t.channel === "email") set({ subject: t.subject, html: t.html });
    else set({ content: { text: d.text, title: d.title, body: d.body, template_name: d.template_name, language: d.language } });
  };
  return (
    <section className="panel translation">
      <div className="panel-head">
        <h3>{name}</h3>
        <span className="row" style={{ gap: 6 }}>
          <TranslateWithAI locale={locale} fields={defaultFields(t)} hasContent={hasWords(tr)} onDone={(f, asked) => setT((cur) => applyAITranslation(cur, asked, f))} />
          <button type="button" className="btn sm" onClick={copyDefault} title="Start from the default's words, then translate them">Copy from Default</button>
          <button type="button" className="btn ghost sm danger" onClick={() => void askDelete(`the ${name} version`, "Contacts in that language get the default.").then((ok) => { if (ok) { setT({ ...t, translations: trs.filter((x) => x.locale !== locale) }); onRemoved(); } })}>Remove language</button>
        </span>
      </div>
      <div className="panel-body">
        <p className="small muted" style={{ marginTop: 0 }}>
          Contacts whose <code>locale</code> is <code>{locale}</code>{locale.includes("-") ? "" : <> (or {locale}-anything, like {locale}-IN)</>} get this version. Leave a field empty to use the default's. Variables work the same.
        </p>
        {t.channel === "email" && <>
          <Field label="Subject"><TemplatedInput label="Subject" value={tr.subject ?? ""} placeholder={t.subject} onChange={(subject) => set({ subject })} /></Field>
          <RichText value={unwrapRichText(tr.html ?? "") ?? tr.html ?? ""} label={`Email body (${name})`} minHeight={320} personalize="email" allowImages allowTables
            onChange={(inner) => set({ html: inner.trim() ? wrapRichText(inner, EMAIL_STYLE) : "" })} />
          {t.builder != null && <p className="xs muted">The default is built with the drag-and-drop builder; this version is written as rich text (Copy from Default brings its HTML in).</p>}
        </>}
        {(t.channel === "sms" || t.channel === "rcs") && (
          <Field label="Message"><TemplatedText label="Message" value={c.text ?? ""} placeholder={d.text} onChange={(text) => setC({ text })} /></Field>
        )}
        {(t.channel === "push" || t.channel === "inbox") && <>
          <Field label="Title"><TemplatedInput label="Title" value={c.title ?? ""} placeholder={d.title} onChange={(title) => setC({ title })} /></Field>
          <Field label="Body"><TemplatedText label="Body" value={c.body ?? ""} placeholder={d.body} onChange={(body) => setC({ body })} /></Field>
        </>}
        {t.channel === "whatsapp" && (
          <div className="grid-2">
            <Field label="Approved template name" hint="WhatsApp approves each language separately."><input className="input" value={c.template_name ?? ""} placeholder={d.template_name} onChange={(e) => setC({ template_name: e.target.value })} /></Field>
            <Field label="Template language code" hint="As approved, e.g. hi or en_US."><input className="input" value={c.language ?? ""} placeholder={locale} onChange={(e) => setC({ language: e.target.value })} /></Field>
          </div>
        )}
      </div>
    </section>
  );
}
