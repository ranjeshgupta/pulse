// Content blocks (O6): a header, footer or offer written once and used in any template as
// {{block.key}}. Changing a block changes every template that uses it, from the next send.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { api, type ContentBlock } from "../api";
import { askDelete } from "../guard";
import { RichText } from "../RichText";
import { Empty, ErrorNote, Field, Modal, PageHead, timeAgo, useCopy, useToast } from "../ui";

/** keyFrom makes a block key from its name: "Festive footer" → festive_footer. */
export function keyFrom(name: string): string {
  return name.toLowerCase().normalize("NFKD").replace(/[\u0300-\u036f]/g, "").replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").slice(0, 64);
}

export function ContentBlocks() {
  const q = useQuery({ queryKey: ["content-blocks"], queryFn: () => api.get<{ blocks: ContentBlock[] }>("/v1/content-blocks") });
  const [editing, setEditing] = useState<ContentBlock | null>(null);
  const copy = useCopy();
  const blocks = q.data?.blocks ?? [];
  return (
    <div className="page">
      <PageHead back={<Link className="back" to="/templates">Message templates</Link>} title="Content blocks"
        intro="Write a header, footer, offer or signature once and use it in any template as {{block.key}}. Change it here and every template that uses it changes from the next send."
        actions={<button className="btn primary" onClick={() => setEditing({ id: 0, key: "", name: "", html: "", text: "" })}>New block</button>} />
      <ErrorNote error={q.error} />
      {blocks.length === 0 && !q.isLoading ? (
        <div className="panel"><Empty title="No content blocks yet">A shared footer (address, social links, legal line) is a good first one.</Empty></div>
      ) : (
        <div className="panel"><div className="table-scroll"><table className="table">
          <thead><tr><th>Name</th><th>Use it as</th><th>Changed</th><th /></tr></thead>
          <tbody>{blocks.map((b) => (
            <tr key={b.id} className="clickable" onClick={() => setEditing(b)}>
              <td><strong>🧩 {b.name}</strong></td>
              <td><button type="button" className="btn ghost sm" onClick={(e) => { e.stopPropagation(); copy(`{{block.${b.key}}}`, "Copied: paste it into a template"); }}><code>{`{{block.${b.key}}}`}</code> Copy</button></td>
              <td className="small muted">{timeAgo(b.updated_at)}</td>
              <td className="num"><button className="btn ghost sm" onClick={(e) => { e.stopPropagation(); setEditing(b); }}>Edit</button></td>
            </tr>
          ))}</tbody>
        </table></div></div>
      )}
      {editing && <BlockEditor block={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function BlockEditor({ block, onClose }: { block: ContentBlock; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [b, setB] = useState(block);
  const [keyTouched, setKeyTouched] = useState(block.id !== 0);
  const usage = useQuery({ queryKey: ["block-usage", block.id], queryFn: () => api.get<{ templates: { id: number; name: string; channel: string }[] }>(`/v1/content-blocks/${block.id}/usage`), enabled: block.id !== 0 });
  const used = usage.data?.templates ?? [];
  const save = useMutation({
    mutationFn: () => (b.id ? api.put(`/v1/content-blocks/${b.id}`, b) : api.post("/v1/content-blocks", b)),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["content-blocks"] }); toast({ text: `Saved ${b.name}${used.length ? `: ${used.length} template${used.length === 1 ? "" : "s"} use it from the next send` : ""}` }); onClose(); },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/content-blocks/${b.id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["content-blocks"] }); toast({ text: `Deleted ${b.name}` }); onClose(); },
  });
  return (
    <Modal wide title={b.id ? `Edit ${block.name}` : "New content block"} onClose={onClose} footer={<>
      {b.id !== 0 && <button className="btn danger" style={{ marginRight: "auto" }} disabled={remove.isPending}
        onClick={() => void askDelete(block.name, used.length ? `${used.length} template${used.length === 1 ? " uses" : "s use"} it; ${used.length === 1 ? "it" : "they"} will show nothing in its place.` : "No template uses it.").then((ok) => ok && remove.mutate())}>Delete</button>}
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={save.isPending || !b.name.trim() || !b.key} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : "Save block"}</button>
    </>}>
      <ErrorNote error={save.error ?? remove.error} />
      <div className="grid-2">
        <Field label="Name"><input className="input" autoFocus value={b.name} placeholder="Festive footer" onChange={(e) => setB({ ...b, name: e.target.value, key: keyTouched ? b.key : keyFrom(e.target.value) })} /></Field>
        <Field label="Key" hint={<>Templates use <code>{`{{block.${b.key || "key"}}}`}</code>{b.id ? ". Changing it breaks templates that use the old key." : ""}</>}>
          <input className="input code" value={b.key} onChange={(e) => { setKeyTouched(true); setB({ ...b, key: keyFrom(e.target.value) }); }} />
        </Field>
      </div>
      <RichText allowImages allowTables label="Content (emails)" minHeight={160} value={b.html} onChange={(html) => setB((x) => ({ ...x, html }))} />
      <Field label="Plain text (SMS, WhatsApp, push, subjects)" hint="Leave empty to use the content's words.">
        <textarea className="textarea" rows={3} value={b.text} onChange={(e) => setB({ ...b, text: e.target.value })} />
      </Field>
      {used.length > 0 && (
        <p className="small muted">Used by {used.slice(0, 6).map((t, i) => <span key={t.id}>{i ? ", " : ""}<Link to={`/templates/${t.id}`}>{t.name}</Link></span>)}{used.length > 6 ? ` and ${used.length - 6} more` : ""}.</p>
      )}
    </Modal>
  );
}
