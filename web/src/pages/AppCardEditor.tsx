// App content slots: the card an app slot shows (title, text, image, button, link, custom values),
// with a live phone-sized preview, or HTML for apps that show the slot in a web view.
import { useState } from "react";
import type { AppCard } from "../api";
import { ask } from "../guard";
import { RichText } from "../RichText";
import { Field } from "../ui";
import "./app-card.css";

export type AppContentMode = "card" | "html";

/** cardProblem says what's wrong with a card, or "" (mirrors the server's checks). */
export function cardProblem(c: AppCard | null | undefined): string {
  if (!c) return "";
  if (c.image_url && !c.image_url.startsWith("https://")) return "The image must be an https:// address.";
  if (c.deep_link) {
    const i = c.deep_link.indexOf("://");
    const scheme = i > 0 ? c.deep_link.slice(0, i).toLowerCase() : "";
    if (!scheme || ["javascript", "data", "http", "file"].includes(scheme) || !/^[a-z][a-z0-9+.-]{0,31}$/.test(scheme)) {
      return "The link must be https://… or an app link like myapp://offers.";
    }
  }
  for (const k of Object.keys(c.data ?? {})) {
    if (!/^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$/.test(k)) return `Custom value name "${k}": use letters, digits, _ . or -.`;
  }
  return "";
}

/** emptyCard reports whether a card has nothing in it. */
export const emptyCard = (c: AppCard | null | undefined) =>
  !c || (!c.title && !c.body && !c.image_url && !c.button_text && !c.deep_link && Object.keys(c.data ?? {}).length === 0);

/** AppContentEditor edits one app slot content: a card (default) or HTML. */
export function AppContentEditor({ label, card, html, onChange }: {
  label: string; card: AppCard | null | undefined; html: string;
  onChange: (p: { card?: AppCard | null; html?: string }) => void;
}) {
  const [mode, setMode] = useState<AppContentMode>(html.trim() && emptyCard(card) ? "html" : "card");
  const c = card ?? {};
  const set = (p: Partial<AppCard>) => onChange({ card: { ...c, ...p } });
  const rows = Object.entries(c.data ?? {});
  const setRows = (r: [string, string][]) => set({ data: r.length ? Object.fromEntries(r) : undefined });
  const problem = cardProblem(card);
  // Switching type clears the other one: ask first when it has something in it.
  const switchTo = (to: AppContentMode) => {
    if (to === mode) return;
    const go = () => { setMode(to); onChange(to === "card" ? { html: "" } : { card: null }); };
    const loses = to === "card" ? html.trim() !== "" : !emptyCard(card);
    if (!loses) return go();
    void ask({
      title: to === "card" ? "Switch to a card?" : "Switch to HTML?",
      body: to === "card" ? "The HTML written for this content is cleared." : "The card's title, text, image, button and custom values are cleared.",
      confirmText: "Switch", danger: true,
    }).then((ok) => ok && go());
  };
  return (
    <div className="appc">
      <div className="seg-toggle sm" role="tablist" aria-label={`${label}: content type`} style={{ marginBottom: 10 }}>
        <button role="tab" aria-pressed={mode === "card"} aria-selected={mode === "card"} onClick={() => switchTo("card")}>Card</button>
        <button role="tab" aria-pressed={mode === "html"} aria-selected={mode === "html"} onClick={() => switchTo("html")}>HTML (web view)</button>
      </div>
      {mode === "html" ? (
        <RichText allowImages allowTables label={label} minHeight={140} value={html} onChange={(v) => onChange({ html: v })} />
      ) : (
        <div className="appc-grid">
          <div>
            <Field label="Title"><input className="input" value={c.title ?? ""} maxLength={200} placeholder="Weekend sale: 20% off" onChange={(e) => set({ title: e.target.value })} /></Field>
            <Field label="Text"><textarea className="input" rows={3} value={c.body ?? ""} maxLength={2000} placeholder="Hi {{contact.first_name | default: 'there'}}, your favourites are on sale." onChange={(e) => set({ body: e.target.value })} /></Field>
            <Field label="Image address" hint="https:// only"><input className="input" value={c.image_url ?? ""} placeholder="https://cdn.example.com/sale.png" onChange={(e) => set({ image_url: e.target.value.trim() })} /></Field>
            <div className="grid-2">
              <Field label="Button text"><input className="input" value={c.button_text ?? ""} maxLength={60} placeholder="Shop now" onChange={(e) => set({ button_text: e.target.value })} /></Field>
              <Field label="Opens" hint="https://… or an app link"><input className="input code" value={c.deep_link ?? ""} placeholder="myapp://sale" onChange={(e) => set({ deep_link: e.target.value.trim() })} /></Field>
            </div>
            <details className="appc-data">
              <summary className="small">Custom values for your app ({rows.length})</summary>
              <p className="xs muted">Key-values your app reads when it draws the slot itself, e.g. <code>theme = dark</code>, <code>offer_id = W20</code>.</p>
              {rows.map(([k, v], i) => (
                <div className="row" key={i} style={{ marginBottom: 6 }}>
                  <input className="input code" aria-label="Name" value={k} placeholder="name" onChange={(e) => setRows(rows.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))} />
                  <input className="input" aria-label="Value" value={v} placeholder="value" onChange={(e) => setRows(rows.map((r, j) => (j === i ? [r[0], e.target.value] : r)))} />
                  <button type="button" className="btn ghost sm" onClick={() => setRows(rows.filter((_, j) => j !== i))}>Remove</button>
                </div>
              ))}
              <button className="btn sm" onClick={() => setRows([...rows, [`key${rows.length + 1}`, ""]])}>Add a value</button>
            </details>
            {problem && <div className="alert error" style={{ marginTop: 8 }}>{problem}</div>}
          </div>
          <AppCardPreview card={c} />
        </div>
      )}
    </div>
  );
}

/** AppCardPreview draws a card the way the SDKs' default view does, in a phone frame. */
export function AppCardPreview({ card }: { card: AppCard }) {
  const empty = emptyCard(card);
  return (
    <div className="appc-phone" aria-label="Preview">
      <div className="appc-notch" />
      <div className="appc-screen">
        {empty ? <p className="xs muted" style={{ textAlign: "center", marginTop: 40 }}>Nothing shows: the slot stays empty.</p> : (
          <div className="appc-card">
            {card.image_url?.startsWith("https://") && <img src={card.image_url} alt="" />}
            {card.title && <div className="appc-title">{card.title}</div>}
            {card.body && <div className="appc-body">{card.body}</div>}
            {card.button_text && <div className="appc-btn">{card.button_text}</div>}
          </div>
        )}
      </div>
      <p className="xs muted" style={{ textAlign: "center", margin: "6px 0 0" }}>Preview of the SDK's default look</p>
    </div>
  );
}
