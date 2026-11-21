// RichTextEditor (loaded lazily through RichText.tsx): a CKEditor-style editor for HTML fields,
// built on TipTap (MIT). It edits an HTML string; "Source" switches to the HTML itself. HTML the
// visual editor can't keep (email layouts with inline styles, <style> blocks, …) opens in Source
// with a note, so nothing is changed behind the author's back. Content the author never edits is
// never rewritten: onChange only fires on an edit.
import { useQuery } from "@tanstack/react-query";
import type { Editor } from "@tiptap/core";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import { forwardRef, useEffect, useId, useImperativeHandle, useMemo, useRef, useState, type ReactNode } from "react";
import { api, type AssetList } from "./api";
import { ask } from "./guard";
import { richTextExtensions, roundTrip } from "./richTextExtensions";
import { EMAIL_LINK_TAGS, normalizeLink, richTextLoss } from "./richTextHtml";
import { useVariables } from "./VarPicker";
import "./richtext.css";
import type { RichTextHandle, RichTextProps } from "./RichText";

const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);
const key = (k: string) => (IS_MAC ? k.replace(/Ctrl\+/g, "⌘").replace(/Shift\+/g, "⇧").replace(/Alt\+/g, "⌥") : k);

const TEXT_COLORS = ["#152238", "#5b6474", "#b4232f", "#c2410c", "#b46d00", "#157347", "#0e7490", "#2747d6", "#6b4fd8", "#be185d"];
const HIGHLIGHTS = ["#fff4d6", "#fde68a", "#dcfce7", "#cffafe", "#e9edfc", "#ede9fe", "#fce7f3", "#fdecee", "#f1f5f9", "#e5e7eb"];

function insertInto(text: string, el: HTMLTextAreaElement | null, snippet: string) {
  const s = el ? el.selectionStart : text.length, e = el ? el.selectionEnd : text.length;
  return { text: text.slice(0, s) + snippet + text.slice(e), caret: s + snippet.length };
}

export default forwardRef<RichTextHandle, RichTextProps>(function RichTextEditor(
  { value, onChange, placeholder = "", minHeight = 220, personalize, allowImages, allowTables, compact, label = "Content", readOnly, lossHint },
  ref,
) {
  // Decided once for the HTML the field opens with; later external changes are checked as they come.
  const [loss, setLoss] = useState<string | null>(() => richTextLoss(value, roundTrip));
  const [source, setSource] = useState(() => loss !== null);
  const [dialog, setDialog] = useState<null | "link" | "image">(null);
  const last = useRef(value); // the HTML the editor holds, as last emitted or loaded
  const changeRef = useRef(onChange);
  changeRef.current = onChange;
  const openLinkRef = useRef<() => void>(() => {});
  const ta = useRef<HTMLTextAreaElement>(null);
  const extensions = useMemo(() => richTextExtensions(placeholder), [placeholder]);

  const editor = useEditor({
    extensions,
    content: loss ? "" : value,
    editable: !readOnly,
    editorProps: {
      attributes: { "aria-label": label, "aria-multiline": "true", role: "textbox", class: "rt-doc" },
      handleKeyDown: (_view, e) => {
        if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "k") {
          e.preventDefault();
          openLinkRef.current();
          return true;
        }
        return false;
      },
    },
    onUpdate: ({ editor: ed }) => {
      const html = ed.isEmpty ? "" : ed.getHTML();
      last.current = html;
      changeRef.current(html);
    },
  });

  // No update event: making it read-only isn't an edit.
  useEffect(() => { if (editor.isEditable === !!readOnly) editor.setEditable(!readOnly, false); }, [editor, readOnly]);

  // A new value from outside (another tab, an insert from the page): load it, unless it's what we sent.
  useEffect(() => {
    if (source || value === last.current) return;
    last.current = value;
    const problem = richTextLoss(value, roundTrip);
    if (problem) {
      setLoss(problem);
      setSource(true);
      return;
    }
    editor.commands.setContent(value, { emitUpdate: false });
  }, [value, source, editor]);

  const toVisual = () => {
    const problem = richTextLoss(value, roundTrip);
    if (!problem) return switchToVisual(null);
    void ask({
      title: "Switch to the visual editor?",
      body: `The visual editor can't keep ${problem}. Switching rewrites the HTML to what it can show, and that changes the layout.`,
      confirmText: "Switch anyway", danger: true,
    }).then((ok) => ok && switchToVisual(problem));
  };
  const switchToVisual = (problem: string | null) => {
    editor.commands.setContent(value, { emitUpdate: false });
    last.current = value;
    if (problem) {
      const html = editor.isEmpty ? "" : editor.getHTML();
      last.current = html;
      changeRef.current(html);
    }
    setLoss(null);
    setSource(false);
    requestAnimationFrame(() => editor.commands.focus());
  };

  const insertSource = (snippet: string) => {
    const el = ta.current;
    const { text, caret } = insertInto(value, el, snippet);
    changeRef.current(text);
    if (el) requestAnimationFrame(() => { el.focus(); el.setSelectionRange(caret, caret); });
  };

  useImperativeHandle(ref, () => ({
    insertText: (text) => (source ? insertSource(text) : editor.chain().focus().insertContent({ type: "text", text }).run()),
    insertHtml: (html) => (source ? insertSource(html) : editor.chain().focus().insertContent(html).run()),
  }));

  openLinkRef.current = () => setDialog("link");

  return (
    <div className={`rt${compact ? " compact" : ""}${source ? " is-source" : ""}${readOnly ? " is-readonly" : ""}`}>
      {!readOnly && (
        <Toolbar editor={editor} source={source} compact={!!compact} label={label}
          allowImages={!!allowImages} allowTables={!!allowTables} personalize={personalize}
          onSource={() => (source ? toVisual() : setSource(true))}
          onDialog={setDialog}
          onTag={(t) => (source ? insertSource(t) : editor.chain().focus().insertContent({ type: "text", text: t }).run())} />
      )}
      {dialog === "link" && !source && <LinkDialog editor={editor} personalize={personalize} onClose={() => { setDialog(null); editor.commands.focus(); }} />}
      {dialog === "image" && !source && <ImageDialog editor={editor} onClose={() => { setDialog(null); editor.commands.focus(); }} />}
      {source && loss && (
        <p className="rt-note" role="note">
          This HTML has layout the visual editor can't keep ({loss}), so it's shown as source. Edit it here{lossHint ? <>, {lossHint}</> : null}.
        </p>
      )}
      <div className="rt-content" style={{ minHeight, display: source ? "none" : undefined }} onClick={(e) => { if (e.target === e.currentTarget) editor.commands.focus("end"); }}>
        <EditorContent editor={editor} />
      </div>
      {source && (
        <textarea ref={ta} className="rt-source" style={{ minHeight }} value={value} readOnly={readOnly} spellCheck={false}
          aria-label={`${label} (HTML source)`} onChange={(e) => changeRef.current(e.target.value)} />
      )}
    </div>
  );
});

// ---------- toolbar ----------

type ToolbarProps = {
  editor: Editor; source: boolean; compact: boolean; label: string; allowImages: boolean; allowTables: boolean;
  personalize?: boolean | "email"; onSource: () => void; onDialog: (d: "link" | "image") => void; onTag: (tag: string) => void;
};

function Toolbar({ editor, source, compact, label, allowImages, allowTables, personalize, onSource, onDialog, onTag }: ToolbarProps) {
  const s = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      block: ([1, 2, 3, 4, 5, 6].find((level) => e.isActive("heading", { level })) ?? 0) as number,
      bold: e.isActive("bold"), italic: e.isActive("italic"), underline: e.isActive("underline"), strike: e.isActive("strike"),
      link: e.isActive("link"), bullet: e.isActive("bulletList"), ordered: e.isActive("orderedList"), quote: e.isActive("blockquote"),
      align: (["center", "right", "justify"] as const).find((a) => e.isActive({ textAlign: a })) ?? "left",
      color: (e.getAttributes("textStyle").color as string | undefined) ?? "",
      background: (e.getAttributes("textStyle").backgroundColor as string | undefined) ?? "",
      table: e.isActive("table"),
      canUndo: e.can().undo(), canRedo: e.can().redo(),
      canMerge: e.can().mergeOrSplit(),
    }),
  });
  const run = () => editor.chain().focus();
  const bar = useRef<HTMLDivElement>(null);

  // Arrow keys move along the toolbar, as in CKEditor; Tab still reaches every control.
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
    if ((e.target as HTMLElement).tagName === "SELECT" || (e.target as HTMLElement).tagName === "INPUT") return;
    const items = Array.from(bar.current?.querySelectorAll<HTMLElement>("button:not([disabled]), select:not([disabled])") ?? []);
    const i = items.indexOf(document.activeElement as HTMLElement);
    if (i < 0) return;
    e.preventDefault();
    items[(i + (e.key === "ArrowRight" ? 1 : -1) + items.length) % items.length].focus();
  };

  const dis = source; // formatting is off while editing the source
  return (
    <div className="rt-toolbar" role="toolbar" aria-label={`${label} formatting`} ref={bar} onKeyDown={onKeyDown}>
      {!compact && (
        <select className="rt-select" aria-label="Text style" disabled={dis} value={s?.block ?? 0} onChange={(e) => {
          const level = Number(e.target.value);
          if (level) run().setHeading({ level: level as 1 | 2 | 3 | 4 | 5 | 6 }).run(); else run().setParagraph().run();
        }}>
          <option value={0}>Paragraph</option>
          <option value={1}>Heading 1</option>
          <option value={2}>Heading 2</option>
          <option value={3}>Heading 3</option>
          {(s?.block ?? 0) > 3 && <option value={s!.block}>Heading {s!.block}</option>}
        </select>
      )}
      <Group>
        <Btn icon="bold" tip={`Bold (${key("Ctrl+B")})`} on={s?.bold} disabled={dis} onClick={() => run().toggleBold().run()} />
        <Btn icon="italic" tip={`Italic (${key("Ctrl+I")})`} on={s?.italic} disabled={dis} onClick={() => run().toggleItalic().run()} />
        <Btn icon="underline" tip={`Underline (${key("Ctrl+U")})`} on={s?.underline} disabled={dis} onClick={() => run().toggleUnderline().run()} />
        {!compact && <Btn icon="strike" tip={`Strikethrough (${key("Ctrl+Shift+S")})`} on={s?.strike} disabled={dis} onClick={() => run().toggleStrike().run()} />}
      </Group>
      <Group>
        <ColorMenu icon="color" tip="Text colour" current={s?.color ?? ""} colors={TEXT_COLORS} disabled={dis}
          onPick={(c) => run().setColor(c).run()} onClear={() => run().unsetColor().run()} clearLabel="Default colour" />
        <ColorMenu icon="highlight" tip="Highlight" current={s?.background ?? ""} colors={HIGHLIGHTS} disabled={dis}
          onPick={(c) => run().setBackgroundColor(c).run()} onClear={() => run().unsetBackgroundColor().run()} clearLabel="No highlight" />
      </Group>
      <Group>
        <Btn icon="link" tip={`Link (${key("Ctrl+K")})`} on={s?.link} disabled={dis} onClick={() => onDialog("link")} haspopup="dialog" />
        <Btn icon="bullets" tip={`Bulleted list (${key("Ctrl+Shift+8")})`} on={s?.bullet} disabled={dis} onClick={() => run().toggleBulletList().run()} />
        <Btn icon="numbers" tip={`Numbered list (${key("Ctrl+Shift+7")})`} on={s?.ordered} disabled={dis} onClick={() => run().toggleOrderedList().run()} />
        {!compact && <Btn icon="quote" tip={`Block quote (${key("Ctrl+Shift+B")})`} on={s?.quote} disabled={dis} onClick={() => run().toggleBlockquote().run()} />}
      </Group>
      {!compact && (
        <Group>
          {(["left", "center", "right", "justify"] as const).map((a) => (
            <Btn key={a} icon={`align-${a}`} tip={`Align ${a === "justify" ? "justified" : a} (${key(`Ctrl+Shift+${{ left: "L", center: "E", right: "R", justify: "J" }[a]}`)})`}
              on={s?.align === a} disabled={dis} onClick={() => run().setTextAlign(a).run()} />
          ))}
        </Group>
      )}
      {(!compact || allowImages || allowTables) && (
        <Group>
          {!compact && <Btn icon="rule" tip="Horizontal line" disabled={dis} onClick={() => run().setHorizontalRule().run()} />}
          {allowImages && <Btn icon="image" tip="Image" disabled={dis} onClick={() => onDialog("image")} haspopup="dialog" />}
          {allowTables && <Btn icon="table" tip="Insert table" disabled={dis || s?.table} onClick={() => run().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run()} />}
        </Group>
      )}
      <Group>
        <Btn icon="clear" tip="Remove formatting" disabled={dis} onClick={() => run().unsetAllMarks().clearNodes().run()} />
        <Btn icon="undo" tip={`Undo (${key("Ctrl+Z")})`} disabled={dis || !s?.canUndo} onClick={() => run().undo().run()} />
        <Btn icon="redo" tip={`Redo (${key(IS_MAC ? "Ctrl+Shift+Z" : "Ctrl+Y")})`} disabled={dis || !s?.canRedo} onClick={() => run().redo().run()} />
      </Group>
      {personalize && <TagPicker email={personalize === "email"} onTag={onTag} />}
      <span className="rt-spacer" />
      <button type="button" className="rt-btn rt-source-btn" aria-pressed={source} data-tip={source ? "Back to the visual editor" : "Edit the HTML source"} onClick={onSource}>
        <Icon name="source" /><span>Source</span>
      </button>
      {s?.table && !source && (
        <div className="rt-table-bar" role="group" aria-label="Table">
          <span className="rt-table-label">Table</span>
          <button type="button" className="rt-chip" onClick={() => run().addRowBefore().run()}>Row above</button>
          <button type="button" className="rt-chip" onClick={() => run().addRowAfter().run()}>Row below</button>
          <button type="button" className="rt-chip" onClick={() => run().deleteRow().run()}>Delete row</button>
          <button type="button" className="rt-chip" onClick={() => run().addColumnBefore().run()}>Column left</button>
          <button type="button" className="rt-chip" onClick={() => run().addColumnAfter().run()}>Column right</button>
          <button type="button" className="rt-chip" onClick={() => run().deleteColumn().run()}>Delete column</button>
          <button type="button" className="rt-chip" disabled={!s.canMerge} title="Select several cells to merge them, or a merged cell to split it" onClick={() => run().mergeOrSplit().run()}>Merge or split cells</button>
          <button type="button" className="rt-chip" onClick={() => run().toggleHeaderRow().run()}>Header row</button>
          <button type="button" className="rt-chip danger" onClick={() => run().deleteTable().run()}>Delete table</button>
        </div>
      )}
    </div>
  );
}

const Group = ({ children }: { children: ReactNode }) => <span className="rt-group">{children}</span>;

function Btn({ icon, tip, on, disabled, onClick, haspopup }: { icon: string; tip: string; on?: boolean; disabled?: boolean; onClick: () => void; haspopup?: "dialog" | "menu" }) {
  return (
    <button type="button" className="rt-btn" aria-label={tip.replace(/ \(.*\)$/, "")} aria-pressed={haspopup ? undefined : on ?? undefined}
      aria-haspopup={haspopup} data-active={on || undefined} data-tip={tip} disabled={disabled}
      // Keep the editor's selection: a mousedown on the button would move focus first.
      onMouseDown={(e) => e.preventDefault()} onClick={onClick}>
      <Icon name={icon} />
    </button>
  );
}

/** usePopover closes a popover on Escape or a click outside it. */
function usePopover(open: boolean, close: () => void) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const down = (e: MouseEvent) => { if (box.current && !box.current.contains(e.target as Node)) close(); };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") { e.stopPropagation(); close(); } };
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", esc, true);
    return () => { document.removeEventListener("mousedown", down); document.removeEventListener("keydown", esc, true); };
  }, [open, close]);
  return box;
}

function ColorMenu({ icon, tip, current, colors, disabled, onPick, onClear, clearLabel }: {
  icon: string; tip: string; current: string; colors: string[]; disabled?: boolean; onPick: (c: string) => void; onClear: () => void; clearLabel: string;
}) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const close = useMemo(() => () => { setOpen(false); trigger.current?.focus(); }, []);
  const box = usePopover(open, close);
  const id = useId();
  const pick = (f: () => void) => { f(); setOpen(false); };
  return (
    <div className="rt-menu" ref={box}>
      <button ref={trigger} type="button" className="rt-btn" aria-label={tip} data-tip={tip} aria-haspopup="true" aria-expanded={open} aria-controls={id}
        disabled={disabled} onMouseDown={(e) => e.preventDefault()} onClick={() => setOpen(!open)}>
        <Icon name={icon} /><span className="rt-swatch-bar" style={{ background: current || (icon === "color" ? "var(--text)" : "transparent") }} />
      </button>
      {open && (
        <div className="rt-pop rt-colors" id={id} role="group" aria-label={tip}>
          <div className="rt-swatches">
            {colors.map((c) => (
              <button key={c} type="button" className="rt-swatch" style={{ background: c }} aria-label={c} title={c}
                aria-pressed={current.toLowerCase() === c} onMouseDown={(e) => e.preventDefault()} onClick={() => pick(() => onPick(c))} />
            ))}
          </div>
          <label className="rt-custom">
            <input type="color" defaultValue={/^#[0-9a-f]{6}$/i.test(current) ? current : "#2747d6"} onChange={(e) => onPick(e.target.value)} />
            Custom…
          </label>
          <button type="button" className="rt-chip" onMouseDown={(e) => e.preventDefault()} onClick={() => pick(onClear)}>{clearLabel}</button>
        </div>
      )}
    </div>
  );
}

function TagPicker({ email, onTag }: { email: boolean; onTag: (tag: string) => void }) {
  // The same catalog as every other templated field (VarPicker): contact fields, the trigger event, campaign, links.
  const v = useVariables();
  const tags = v.contact.map((c) => ({ label: c.label, tag: c.var === "contact.first_name" ? '{{contact.first_name | default:"there"}}' : `{{${c.var}}}` }));
  return (
    <select className="rt-select rt-tags" aria-label="Insert personalization" value=""
      onChange={(e) => { if (e.target.value) onTag(e.target.value); }}>
      <option value="">Personalize…</option>
      <optgroup label="Contact">
        {tags.map((t) => <option key={t.tag} value={t.tag}>{t.label}</option>)}
      </optgroup>
      {v.event && v.event.length > 0 && (
        <optgroup label={`Trigger event (${v.eventName})`}>
          {v.event.map((t) => <option key={t.var} value={`{{${t.var}}}`}>{t.label}</option>)}
        </optgroup>
      )}
      <optgroup label="Campaign">
        {v.campaign.map((t) => <option key={t.var} value={`{{${t.var}}}`}>{t.label}</option>)}
      </optgroup>
      {email && (
        <optgroup label="Links (as text)">
          {EMAIL_LINK_TAGS.map((t) => <option key={t.tag} value={t.tag}>{t.label}</option>)}
        </optgroup>
      )}
    </select>
  );
}

// ---------- dialogs ----------

function LinkDialog({ editor, personalize, onClose }: { editor: Editor; personalize?: boolean | "email"; onClose: () => void }) {
  const attrs = editor.getAttributes("link") as { href?: string; target?: string };
  const [url, setUrl] = useState(attrs.href ?? "");
  const [newTab, setNewTab] = useState(attrs.target === "_blank");
  const [problem, setProblem] = useState<string | null>(null);
  const box = usePopover(true, onClose);
  const id = useId();
  const apply = (e?: React.FormEvent) => {
    e?.preventDefault();
    const r = normalizeLink(url);
    if ("problem" in r) { setProblem(r.problem); return; }
    const link = { href: r.url, target: newTab ? "_blank" : null, rel: newTab ? "noopener noreferrer" : null };
    const chain = editor.chain().focus();
    if (editor.state.selection.empty && !editor.isActive("link")) {
      chain.insertContent({ type: "text", text: r.url, marks: [{ type: "link", attrs: link }] }).run();
    } else {
      chain.extendMarkRange("link").setLink(link).run();
    }
    onClose();
  };
  return (
    <div className="rt-pop rt-dialog" role="dialog" aria-label="Link" ref={box}>
      <form onSubmit={apply}>
        <label className="rt-field" htmlFor={`${id}-url`}>Link address</label>
        <div className="rt-row">
          <input id={`${id}-url`} className="input" autoFocus value={url} placeholder="https://example.com" spellCheck={false}
            aria-invalid={!!problem} aria-describedby={problem ? `${id}-err` : undefined}
            onChange={(e) => { setUrl(e.target.value); setProblem(null); }} />
          {personalize === "email" && (
            <select className="select inline" aria-label="Link to a personal link" value="" onChange={(e) => e.target.value && (setUrl(e.target.value), setProblem(null))}>
              <option value="">Personal links…</option>
              {EMAIL_LINK_TAGS.map((t) => <option key={t.tag} value={t.tag}>{t.label}</option>)}
            </select>
          )}
        </div>
        {problem && <p className="rt-err" id={`${id}-err`} role="alert">{problem}</p>}
        <label className="check rt-check"><input type="checkbox" checked={newTab} onChange={(e) => setNewTab(e.target.checked)} /> Open in a new tab</label>
        <div className="rt-row rt-actions">
          {attrs.href !== undefined && <button type="button" className="btn sm ghost" onClick={() => { editor.chain().focus().extendMarkRange("link").unsetLink().run(); onClose(); }}>Remove link</button>}
          <span className="rt-spacer" />
          <button type="button" className="btn sm" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn sm primary">{attrs.href !== undefined ? "Update" : "Add link"}</button>
        </div>
      </form>
    </div>
  );
}

function ImageDialog({ editor, onClose }: { editor: Editor; onClose: () => void }) {
  const [url, setUrl] = useState("");
  const [alt, setAlt] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  const box = usePopover(true, onClose);
  const id = useId();
  const q = useQuery({ queryKey: ["assets"], queryFn: () => api.get<AssetList>("/v1/assets") });
  // Form-gated files show a form instead of the file, so they can't be images on a page.
  const images = (q.data?.assets ?? []).filter((a) => a.content_type.startsWith("image/") && !a.form_id);
  const base = (q.data?.base_url ?? "").replace(/\/+$/, "");
  const apply = (e: React.FormEvent) => {
    e.preventDefault();
    const r = normalizeLink(url);
    if ("problem" in r || !/^(https?:\/\/|\/|\{\{)/i.test(r.url)) { setProblem("Enter the image's address, starting with https://."); return; }
    editor.chain().focus().setImage({ src: r.url, alt: alt.trim() || undefined }).run();
    onClose();
  };
  return (
    <div className="rt-pop rt-dialog" role="dialog" aria-label="Image" ref={box}>
      <form onSubmit={apply}>
        <label className="rt-field" htmlFor={`${id}-src`}>Image address</label>
        <input id={`${id}-src`} className="input" autoFocus value={url} placeholder="https://example.com/banner.png" spellCheck={false}
          aria-invalid={!!problem} onChange={(e) => { setUrl(e.target.value); setProblem(null); }} />
        {images.length > 0 && (
          <div className="rt-assets">
            <span className="rt-field">Or pick an uploaded image</span>
            <div className="rt-asset-list">
              {images.map((a) => {
                const src = `${base}/a/${a.public_key}`;
                return (
                  <button key={a.id} type="button" className="rt-chip" aria-pressed={url === src}
                    onClick={() => { setUrl(src); if (!alt) setAlt(a.title); setProblem(null); }}>{a.title || a.filename}</button>
                );
              })}
            </div>
            <small className="rt-help">Each time the image is shown it counts as a download of the file.</small>
          </div>
        )}
        {problem && <p className="rt-err" role="alert">{problem}</p>}
        <label className="rt-field" htmlFor={`${id}-alt`}>Alternative text</label>
        <input id={`${id}-alt`} className="input" value={alt} placeholder="What the image shows, for screen readers and blocked images" onChange={(e) => setAlt(e.target.value)} />
        <div className="rt-row rt-actions">
          <span className="rt-spacer" />
          <button type="button" className="btn sm" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn sm primary">Insert image</button>
        </div>
      </form>
    </div>
  );
}

// ---------- icons (24px grid, stroked) ----------

const PATHS: Record<string, ReactNode> = {
  bold: <path d="M7 5h6a3.5 3.5 0 0 1 0 7H7zM7 12h7a3.5 3.5 0 0 1 0 7H7z" />,
  italic: <path d="M18 5h-7M13 19H6M14.5 5l-5 14" />,
  underline: <path d="M7 4v7a5 5 0 0 0 10 0V4M5 20h14" />,
  strike: <path d="M16 6.5A3.8 3.8 0 0 0 12.5 4h-1A3.5 3.5 0 0 0 8 7.5c0 1.6 1 2.6 2.5 3.1M5 12h14M14.5 13.4c1.5.6 2.5 1.6 2.5 3.1a3.5 3.5 0 0 1-3.5 3.5h-2A4 4 0 0 1 7.6 17.5" />,
  color: <path d="M6.5 16 12 4l5.5 12M8.6 11.5h6.8" />,
  highlight: <path d="m14 4 6 6-8 8H6v-6zM4 21h7" />,
  link: <path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1.2 1.2M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1.2-1.2" />,
  bullets: <path d="M10 6h10M10 12h10M10 18h10M4.5 6h.01M4.5 12h.01M4.5 18h.01" strokeWidth="2.6" />,
  numbers: <path d="M10 6h10M10 12h10M10 18h10M4 5l1.5-1v5M3.8 13.6c.3-.6.9-.9 1.5-.8.8.1 1.2.8.9 1.5L4 17h2.5" />,
  quote: <path d="M5 11h4v6H5v-6c0-3 1-5 4-6M15 11h4v6h-4v-6c0-3 1-5 4-6" />,
  "align-left": <path d="M4 6h16M4 10h10M4 14h16M4 18h10" />,
  "align-center": <path d="M4 6h16M7 10h10M4 14h16M7 18h10" />,
  "align-right": <path d="M4 6h16M10 10h10M4 14h16M10 18h10" />,
  "align-justify": <path d="M4 6h16M4 10h16M4 14h16M4 18h16" />,
  rule: <path d="M3 12h18M7 7h10M7 17h10" strokeOpacity={1} />,
  image: <path d="M4 5h16v14H4zM4 16l5-5 4 4 2.5-2.5L20 17M15.5 9h.01" />,
  table: <path d="M4 5h16v14H4zM4 10h16M4 14.5h16M10 5v14M15 5v14" />,
  clear: <path d="M5 7V5h13v2M11.5 5 9 19M7 19h4M15 14l5 5M20 14l-5 5" />,
  undo: <path d="M9 14 4 9l5-5M4 9h10a6 6 0 0 1 0 12h-3" />,
  redo: <path d="m15 14 5-5-5-5M20 9H10a6 6 0 0 0 0 12h3" />,
  source: <path d="m9 7-5 5 5 5M15 7l5 5-5 5" />,
};

function Icon({ name }: { name: string }) {
  return (
    <svg className="rt-icon" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
      {PATHS[name]}
    </svg>
  );
}
