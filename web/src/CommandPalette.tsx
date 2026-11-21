// ⌘K / Ctrl+K: jump to any page or start something new by typing, the way current tools work.
// "/" focuses the page's search box; "?" lists the keyboard shortcuts.
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import "./palette.css";

export type Command = { label: string; to: string; group: string; keywords?: string };

/** rank orders commands by how well they match the typed text (all words must match). */
export function rank(cmds: Command[], q: string): Command[] {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return cmds;
  const scored: { c: Command; s: number }[] = [];
  for (const c of cmds) {
    const label = c.label.toLowerCase();
    const hay = `${label} ${c.group.toLowerCase()} ${c.keywords ?? ""}`.toLowerCase();
    if (!words.every((w) => hay.includes(w))) continue;
    const s = label.startsWith(words[0]) ? 0 : label.split(/\s+/).some((p) => p.startsWith(words[0])) ? 1 : 2;
    scored.push({ c, s });
  }
  return scored.sort((a, b) => a.s - b.s).map((x) => x.c);
}

/** typing reports whether the key went to a text field (shortcuts stay out of the way there). */
const typing = (t: EventTarget | null) =>
  t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || t instanceof HTMLSelectElement || (t instanceof HTMLElement && t.isContentEditable);

export function CommandPalette({ commands }: { commands: Command[] }) {
  const [open, setOpen] = useState(false);
  const [help, setHelp] = useState(false);
  const [q, setQ] = useState("");
  const [at, setAt] = useState(0);
  const nav = useNavigate();
  const input = useRef<HTMLInputElement>(null);
  const extra: Command[] = q.trim() ? [{ label: `Search contacts for “${q.trim()}”`, to: `/contacts?q=${encodeURIComponent(q.trim())}`, group: "Contacts" }] : [];
  const list = useMemo(() => [...rank(commands, q), ...extra].slice(0, 12), [commands, q]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
        setQ("");
        setAt(0);
        return;
      }
      if (typing(e.target) || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === "/") {
        const box = document.querySelector<HTMLInputElement>("main input[type=search], main input.search, main input[placeholder^='Search']");
        if (box) { e.preventDefault(); box.focus(); }
      } else if (e.key === "?") {
        setHelp((h) => !h);
      }
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, []);
  useEffect(() => { if (open) window.setTimeout(() => input.current?.focus(), 0); }, [open]);

  const go = (c?: Command) => {
    if (!c) return;
    setOpen(false);
    nav(c.to);
  };
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setAt((a) => Math.min(a + 1, list.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setAt((a) => Math.max(a - 1, 0)); }
    else if (e.key === "Enter") { e.preventDefault(); go(list[at]); }
    else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); setOpen(false); }
  };

  return (
    <>
      {open && (
        <>
          <div className="modal-backdrop cmdk-backdrop" onClick={() => setOpen(false)} />
          <div className="cmdk" role="dialog" aria-modal="true" aria-label="Go to or create">
            <input ref={input} className="cmdk-input" placeholder="Go to a page or start something new…" value={q} data-no-dirty
              onChange={(e) => { setQ(e.target.value); setAt(0); }} onKeyDown={onKey} aria-controls="cmdk-list" aria-activedescendant={`cmdk-${at}`} />
            <ul className="cmdk-list" id="cmdk-list" role="listbox">
              {list.length === 0 && <li className="cmdk-empty">Nothing matches “{q}”.</li>}
              {list.map((c, i) => (
                <li key={c.to + c.label} id={`cmdk-${i}`} role="option" aria-selected={i === at} className={i === at ? "on" : ""}
                  onMouseEnter={() => setAt(i)} onClick={() => go(c)}>
                  <span>{c.label}</span><span className="cmdk-group">{c.group}</span>
                </li>
              ))}
            </ul>
            <div className="cmdk-foot xs muted">↑ ↓ to move · Enter to open · Esc to close · <kbd>?</kbd> all shortcuts</div>
          </div>
        </>
      )}
      {help && <ShortcutHelp onClose={() => setHelp(false)} />}
    </>
  );
}

function ShortcutHelp({ onClose }: { onClose: () => void }) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [onClose]);
  const mod = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘" : "Ctrl";
  const rows: [string, string][] = [
    [`${mod} K`, "Go to a page or start something new"],
    ["/", "Search on this page"],
    [`${mod} S`, "Save the editor you're in"],
    ["Esc", "Close a dialog (asks first if you changed something)"],
    ["?", "Show or hide this list"],
  ];
  return (
    <>
      <div className="modal-backdrop" onClick={onClose} />
      <div className="modal" role="dialog" aria-modal="true" aria-label="Keyboard shortcuts">
        <div className="panel-head"><h3>Keyboard shortcuts</h3><button className="btn ghost sm" onClick={onClose}>Close</button></div>
        <div className="panel-body">
          <table className="table"><tbody>{rows.map(([k, d]) => <tr key={k}><td style={{ width: 120 }}><kbd>{k}</kbd></td><td>{d}</td></tr>)}</tbody></table>
        </div>
      </div>
    </>
  );
}
