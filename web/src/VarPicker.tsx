// The variable picker on every templated field: contact fields, the trigger event's properties,
// campaign and link variables, with an optional default and format. TemplatedInput / TemplatedText
// wrap an input or textarea with it and show the field's variable problems underneath.
import { useQuery } from "@tanstack/react-query";
import { createContext, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { api, type AttributeDef, type ContentBlock, type EventSchema } from "./api";
import { buildTag, CAMPAIGN_VARS, contactVars, eventVars, insertAt, lint, LINK_VARS, PIPES, RECO_VARS, type Problem, type TagPipe, type VarItem } from "./personalize";
import "./personalize.css";
import { copyText, useToast } from "./ui";

/** What the fields being edited can use: the trigger event (journeys) and the channel (links are email-only). */
export type PersonalizeScope = { event?: string; channel?: string };
const ScopeCtx = createContext<PersonalizeScope>({});
export const PersonalizeScopeProvider = ({ value, children }: { value: PersonalizeScope; children: ReactNode }) => <ScopeCtx.Provider value={value}>{children}</ScopeCtx.Provider>;
export const usePersonalizeScope = () => useContext(ScopeCtx);

/** useVariables: the catalog for the current scope — contact fields, the event's properties, campaign, links. */
export function useVariables(scope: PersonalizeScope = usePersonalizeScope()) {
  const attrs = useQuery({ queryKey: ["attributes"], queryFn: () => api.get<{ attributes: AttributeDef[] }>("/v1/attributes"), staleTime: 60_000 });
  const schemas = useQuery({ queryKey: ["event-schemas"], queryFn: () => api.get<{ schemas: EventSchema[] }>("/v1/events/schemas"), staleTime: 60_000, retry: false });
  return useMemo(() => {
    const schema = scope.event ? schemas.data?.schemas.find((s) => s.name === scope.event) ?? null : null;
    const contact = contactVars(attrs.data?.attributes ?? []);
    const event = schema ? eventVars(schema) : null;
    const links = !scope.channel || scope.channel === "email" ? LINK_VARS : [];
    // In-app campaigns are filled per app user, outside any journey: contact fields only.
    const inapp = scope.channel === "inapp";
    return { contact, event: inapp ? null : event, schema, schemas: schemas.data?.schemas ?? [], campaign: inapp ? [] : CAMPAIGN_VARS, links,
      reco: inapp ? [] : RECO_VARS,
      channel: scope.channel, eventName: scope.event, contactOnly: inapp };
  }, [attrs.data, schemas.data, scope.event, scope.channel]);
}

const GROUP_NAMES = { contact: "Contact", event: "Trigger event", campaign: "Campaign", links: "Links", reco: "Recommended products" } as const;
const FORMATS: { label: string; pipe?: TagPipe }[] = [
  { label: "As it is" },
  { label: "Title Case", pipe: { name: "title" } },
  { label: "UPPER CASE", pipe: { name: "upper" } },
  { label: "lower case", pipe: { name: "lower" } },
  { label: "Date (2 Jan 2006)", pipe: { name: "date", arg: "2 Jan 2006" } },
  { label: "Money (₹)", pipe: { name: "money", arg: "INR" } },
  { label: "Money ($)", pipe: { name: "money", arg: "USD" } },
  { label: "Number (1,234)", pipe: { name: "number" } },
  { label: "Shorten to 40 characters", pipe: { name: "truncate", arg: "40" } },
  { label: "For a link (urlencode)", pipe: { name: "urlencode" } },
  { label: "JSON value", pipe: { name: "json" } },
];

/**
 * VarPicker: "{ } Variable" opens a list of variables; choosing one (with a default and a format)
 * calls onInsert with the tag. copyOnly copies the tag instead (for editors it can't type into).
 */
export function VarPicker({ onInsert, label = "Insert a variable", copyOnly }: { onInsert: (tag: string) => void; label?: string; copyOnly?: boolean }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [pick, setPick] = useState<VarItem | null>(null);
  const [def, setDef] = useState("");
  const [fmt, setFmt] = useState(0);
  const [custom, setCustom] = useState("");
  const toast = useToast();
  const vars = useVariables();
  const surveys = useQuery({ queryKey: ["surveys"], queryFn: () => api.get<{ surveys: { id: number; name: string; status: string }[] }>("/v1/surveys"), staleTime: 60_000, retry: false, enabled: open && !vars.contactOnly });
  const blocks = useQuery({ queryKey: ["content-blocks"], queryFn: () => api.get<{ blocks: ContentBlock[] }>("/v1/content-blocks"), staleTime: 60_000, retry: false, enabled: open && !vars.contactOnly });
  const box = useRef<HTMLDivElement>(null);
  const id = useId();
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => { if (box.current && !box.current.contains(e.target as Node)) setOpen(false); };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", esc);
    return () => { document.removeEventListener("mousedown", close); document.removeEventListener("keydown", esc); };
  }, [open]);
  const groups = ([
    ["contact", vars.contact], ["event", vars.event ?? []], ["campaign", vars.campaign], ["links", vars.links], ["reco", vars.reco],
  ] as [keyof typeof GROUP_NAMES, VarItem[]][]).filter(([g, items]) => g === "contact" || g === "event" ? !vars.contactOnly || g === "contact" : items.length > 0);
  const match = (v: VarItem) => !q || (v.var + " " + v.label).toLowerCase().includes(q.toLowerCase());
  const tag = (v: VarItem) => buildTag(v.var, [...(def ? [{ name: "default", arg: def }] : []), ...(FORMATS[fmt].pipe ? [FORMATS[fmt].pipe!] : [])]);
  const done = (t: string) => {
    // Copying can fail (no permission, a page not on https): then the tag is shown to copy by hand.
    if (copyOnly) void copyText(t).then(() => onInsert(t), () => toast({ text: `Couldn't copy. Select it and copy it yourself: ${t}`, error: true }));
    else onInsert(t);
    setOpen(false); setPick(null); setDef(""); setFmt(0); setQ("");
  };
  return (
    <div className="vp" ref={box}>
      <button type="button" className="btn ghost sm vp-btn" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(!open)} title={label}>
        <span aria-hidden="true" className="vp-braces">{"{ }"}</span> {copyOnly ? "Copy a variable" : "Variable"}
      </button>
      {open && (
        <div className="vp-pop" role="dialog" aria-label={label}>
          {!pick ? (
            <>
              <input className="input vp-search" autoFocus placeholder="Search variables" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search variables" />
              <div className="vp-list">
                {(blocks.data?.blocks ?? []).filter((b) => !q || (b.name + " " + b.key).toLowerCase().includes(q.toLowerCase())).length > 0 && (
                  <div className="vp-group" role="group" aria-labelledby={`${id}-blocks`}>
                    <div className="vp-group-name" id={`${id}-blocks`}>Content blocks</div>
                    {(blocks.data?.blocks ?? []).filter((b) => !q || (b.name + " " + b.key).toLowerCase().includes(q.toLowerCase())).map((b) => (
                      <button type="button" key={b.key} className="vp-item" onClick={() => done(`{{block.${b.key}}}`)}>
                        <span>🧩 {b.name}</span><code>block.{b.key}</code>
                      </button>
                    ))}
                  </div>
                )}
                {!vars.contactOnly && (!q || "recommended products recommendations".includes(q.toLowerCase())) && (
                  <div className="vp-group" role="group" aria-labelledby={`${id}-reco-block`}>
                    <div className="vp-group-name" id={`${id}-reco-block`}>Filled by Pulse</div>
                    <button type="button" className="vp-item" onClick={() => done("{{block.recommendations}}")}>
                      <span>🛍️ Recommended products (cards)</span><code>block.recommendations</code>
                    </button>
                    <button type="button" className="vp-item" onClick={() => done("{{block.feed}}")}>
                      <span>📰 New items from the campaign's RSS feed</span><code>block.feed</code>
                    </button>
                  </div>
                )}
                {(surveys.data?.surveys ?? []).filter((x) => x.status !== "closed" && (!q || x.name.toLowerCase().includes(q.toLowerCase()))).length > 0 && (
                  <div className="vp-group" role="group" aria-labelledby={`${id}-surveys`}>
                    <div className="vp-group-name" id={`${id}-surveys`}>Survey links (one per contact)</div>
                    {(surveys.data?.surveys ?? []).filter((x) => x.status !== "closed" && (!q || x.name.toLowerCase().includes(q.toLowerCase()))).map((x) => (
                      <button type="button" key={x.id} className="vp-item" onClick={() => done(`{{survey.${x.id}}}`)}>
                        <span>📋 {x.name}</span><code>survey.{x.id}</code>
                      </button>
                    ))}
                  </div>
                )}
                {groups.map(([g, items]) => (
                  <div key={g} className="vp-group" role="group" aria-labelledby={`${id}-${g}`}>
                    <div className="vp-group-name" id={`${id}-${g}`}>{GROUP_NAMES[g]}</div>
                    {g === "event" && !vars.eventName && <p className="vp-note">Properties of the event that starts the journey. Choose the event above the editor to list them; one-time campaigns leave them empty.</p>}
                    {g === "event" && vars.eventName && !vars.schema && <p className="vp-note">“{vars.eventName}” has no schema yet: type the property, e.g. {"{{event.product_name}}"}.</p>}
                    {items.filter(match).map((v) => (
                      <button type="button" key={v.var} className="vp-item" onClick={() => setPick(v)}>
                        <span>{v.label}</span><code>{v.var}</code>
                      </button>
                    ))}
                  </div>
                ))}
              </div>
              <div className="vp-custom">
                <input className="input" placeholder="or type one: event.items.0.price" value={custom} onChange={(e) => setCustom(e.target.value.trim())} aria-label="Variable name" />
                <button type="button" className="btn sm" disabled={!/^(contact|event|campaign|links|reco|survey)\.[A-Za-z0-9_.[\]]+$/.test(custom)} onClick={() => setPick({ var: custom, label: custom, group: custom.split(".")[0] as VarItem["group"] })}>Use</button>
              </div>
            </>
          ) : (
            <div className="vp-options">
              <p className="small" style={{ margin: "0 0 8px" }}><strong>{pick.label}</strong> <code className="xs">{pick.var}</code></p>
              <label className="field"><span>When there's no value, use</span>
                <input className="input" value={def} onChange={(e) => setDef(e.target.value)} placeholder="nothing (e.g. there, Customer)" autoFocus />
              </label>
              <label className="field"><span>Format</span>
                <select className="select" value={fmt} onChange={(e) => setFmt(Number(e.target.value))}>
                  {FORMATS.map((f, i) => <option key={f.label} value={i}>{f.label}</option>)}
                </select>
              </label>
              <p className="xs muted vp-preview" aria-live="polite">{tag(pick)}</p>
              <div className="row" style={{ justifyContent: "flex-end", gap: 6 }}>
                <button type="button" className="btn sm ghost" onClick={() => setPick(null)}>Back</button>
                <button type="button" className="btn sm primary" onClick={() => done(tag(pick))}>{copyOnly ? "Copy" : "Insert"}</button>
              </div>
            </div>
          )}
          <p className="xs muted vp-foot">Formats: {PIPES.map((p) => p.name).join(", ")}. Write {"{{contact.first_name | default:\"there\" | title}}"} by hand too.</p>
        </div>
      )}
    </div>
  );
}

/** Problems: a field's variable warnings and errors. */
export function VarProblems({ problems }: { problems: Problem[] }) {
  if (!problems.length) return null;
  return (
    <ul className="vp-problems" aria-label="Variable problems">
      {problems.map((p) => <li key={p.level + p.var + p.message} className={p.level}><code>{p.var}</code> {p.message}</li>)}
    </ul>
  );
}

/** useLint checks text with the scope's catalog. */
export function useLint(...fields: (string | undefined)[]): Problem[] {
  const v = useVariables();
  const key = fields.join("\u0000");
  return useMemo(() => fields.flatMap((f) => lint(f ?? "", { contact: v.contact, event: v.event, channel: v.channel })), [key, v]);
}

type FieldProps = { value: string; onChange: (v: string) => void; label?: string; placeholder?: string; className?: string; rows?: number; "aria-label"?: string };

/** TemplatedInput: a one-line input with the variable picker and its problems. */
export function TemplatedInput(props: FieldProps) {
  const ref = useRef<HTMLInputElement>(null);
  return <Templated {...props} el={ref} render={(onSel) => (
    <input ref={ref} className={props.className ?? "input"} value={props.value} placeholder={props.placeholder} aria-label={props["aria-label"] ?? props.label}
      onChange={(e) => props.onChange(e.target.value)} onSelect={onSel} />
  )} />;
}

/** TemplatedText: a textarea with the variable picker and its problems. */
export function TemplatedText(props: FieldProps) {
  const ref = useRef<HTMLTextAreaElement>(null);
  return <Templated {...props} el={ref} render={(onSel) => (
    <textarea ref={ref} className={props.className ?? "textarea"} value={props.value} placeholder={props.placeholder} rows={props.rows} aria-label={props["aria-label"] ?? props.label}
      onChange={(e) => props.onChange(e.target.value)} onSelect={onSel} spellCheck />
  )} />;
}

function Templated({ value, onChange, el, render }: FieldProps & { el: React.RefObject<HTMLInputElement | HTMLTextAreaElement | null>; render: (onSelect: () => void) => ReactNode }) {
  const sel = useRef<[number, number] | null>(null);
  const problems = useLint(value);
  const remember = () => { const e = el.current; if (e) sel.current = [e.selectionStart ?? e.value.length, e.selectionEnd ?? e.value.length]; };
  const insert = (tag: string) => {
    const [s, e] = sel.current ?? [value.length, value.length];
    const r = insertAt(value, tag, s, e);
    onChange(r.value);
    requestAnimationFrame(() => { const x = el.current; if (x) { x.focus(); x.setSelectionRange(r.cursor, r.cursor); } });
  };
  return (
    <div className="vp-field">
      <div className="vp-wrap">{render(remember)}<VarPicker onInsert={insert} /></div>
      <VarProblems problems={problems} />
    </div>
  );
}

/** EventScopePicker: which event's properties {{event.x}} offers in the template editor (templates
 * don't know which journey sends them). */
export function EventScopePicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const v = useVariables({});
  return (
    <label className="vp-scope">
      <span className="xs muted">Trigger event for {"{{event.…}}"}</span>
      <select className="select inline" value={value} onChange={(e) => onChange(e.target.value)} aria-label="Trigger event for event variables">
        <option value="">None (one-time campaigns)</option>
        {v.schemas.map((s) => <option key={s.name} value={s.name}>{s.label || s.name}</option>)}
        {value && !v.schemas.some((s) => s.name === value) && <option value={value}>{value}</option>}
      </select>
    </label>
  );
}
