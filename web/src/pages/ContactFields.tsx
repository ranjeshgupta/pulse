// Contact page panels: list and choice fields (shown and edited as chips or a dropdown), and
// "Why?" for each segment the contact is in or could be in.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, type AttributeDef, type Contact, type SegmentExplain } from "../api";
import { useCan } from "../auth";
import { explainRows, verdict } from "../explain";
import { ErrorNote, Modal } from "../ui";
import { ChipsInput } from "./SegmentBuilder";
import { useAttributes, useRefObjects, useSegmentNames } from "./segmentData";

/** listOf reads a list field's value (a JSON array, or its text). */
export function listOf(v: unknown): string[] {
  if (Array.isArray(v)) return v.map(String);
  if (typeof v === "string" && v.startsWith("[")) {
    try { const a = JSON.parse(v); return Array.isArray(a) ? a.map(String) : []; } catch { return []; }
  }
  return [];
}

/** CustomFields shows the contact's list and choice fields, editable by contacts editors (the change
 * is an identify event, so it is ordered with the contact's other events). */
export function CustomFields({ contact }: { contact: Contact }) {
  const defs = (useAttributes().data?.attributes ?? []).filter((d) => d.type === "list" || d.type === "enum");
  const canEdit = useCan("contacts", "edit");
  const [editing, setEditing] = useState<AttributeDef | null>(null);
  if (defs.length === 0) return null;
  return (
    <section className="contact-fields">
      <h3 style={{ marginBottom: 10 }}>Lists and choices</h3>
      <dl className="kv" style={{ marginBottom: 22 }}>
        {defs.map((d) => {
          const v = contact.attrs[d.key];
          return (
            <div key={d.key} style={{ display: "contents" }}>
              <dt title={d.key}>{d.label || d.key}</dt>
              <dd>
                {d.type === "list"
                  ? (listOf(v).length ? listOf(v).map((x) => <span className="badge" key={x} style={{ marginRight: 4 }}>{x}</span>) : <span className="faint">none</span>)
                  : v != null && v !== "" ? (d.options?.find((o) => o.value === String(v))?.label ?? String(v)) : <span className="faint">not set</span>}
                {canEdit && <button className="btn ghost sm" onClick={() => setEditing(d)} aria-label={`Edit ${d.label || d.key}`}>Edit</button>}
              </dd>
            </div>
          );
        })}
      </dl>
      {editing && <FieldModal contact={contact} def={editing} onClose={() => setEditing(null)} />}
    </section>
  );
}

function FieldModal({ contact, def, onClose }: { contact: Contact; def: AttributeDef; onClose: () => void }) {
  const qc = useQueryClient();
  const cur = contact.attrs[def.key];
  const [list, setList] = useState(listOf(cur));
  const [choice, setChoice] = useState(cur == null ? "" : String(cur));
  const save = useMutation({
    mutationFn: () => api.post("/v1/events", {
      external_id: contact.external_id, name: "identify",
      attrs: { [def.key]: def.type === "list" ? list : choice },
    }),
    onSuccess: () => { setTimeout(() => qc.invalidateQueries({ queryKey: ["contact", contact.external_id] }), 800); onClose(); },
  });
  return (
    <Modal title={`${def.label || def.key}`} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={save.isPending} onClick={() => save.mutate()}>Save</button>
    </>}>
      {def.type === "list" ? (
        <>
          <p className="small muted">Type a value and press Enter. Segments ask whether the list has a value.</p>
          <ChipsInput values={list} onChange={setList} />
        </>
      ) : (
        <select className="select" value={choice} onChange={(e) => setChoice(e.target.value)} aria-label={def.label || def.key}>
          <option value="">Not set</option>
          {(def.options ?? []).map((o) => <option key={o.value} value={o.value}>{o.label ? `${o.label} (${o.value})` : o.value}</option>)}
        </select>
      )}
      <ErrorNote error={save.error} />
    </Modal>
  );
}

/** SegmentWhy shows, filter by filter, why the contact is (or isn't) in a segment. */
export function SegmentWhy({ externalId, segment, onClose }: { externalId: string; segment: number; onClose: () => void }) {
  const names = useSegmentNames();
  const refs = useRefObjects().names;
  const q = useQuery({
    queryKey: ["explain", externalId, segment],
    queryFn: () => api.get<SegmentExplain>(`/v1/contacts/${encodeURIComponent(externalId)}/segments/${segment}/explain`),
  });
  const d = q.data;
  const rows = d?.explanation ? explainRows(d.explanation, names, refs) : [];
  return (
    <div className="seg-why panel" role="region" aria-label={`Why: ${d?.name ?? "segment"}`}>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <strong>{d?.name ?? "…"}</strong>
        <button className="btn ghost sm" onClick={onClose} aria-label="Close">×</button>
      </div>
      <ErrorNote error={q.error} />
      {d && <p className="small">{d.kind === "static" ? (d.member ? "In this static segment: added by hand, import, a journey step or the API." : "Not in this static segment.") : verdict(d.member, d.explanation?.match, d.refresh?.mode === "daily")}</p>}
      {rows.length > 0 && (
        <ul className="seg-why-rows">
          {rows.map((r, i) => (
            <li key={i} className={r.match ? "yes" : "no"} style={{ paddingLeft: r.depth * 18 }}>
              <span className="seg-why-mark" aria-label={r.match ? "matches" : "doesn't match"}>{r.sweep ? "?" : r.match ? "✓" : "✗"}</span>
              <span className={r.group ? "seg-why-group" : ""}>{r.text}</span>
              {r.detail && <span className="xs muted"> · {r.detail}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
