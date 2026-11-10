import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Segment } from "../api";
import { describe, segmentRefs } from "../segmentRules";
import { ImportModal } from "./Contacts";
import { useSegments } from "./Segments";
import { Empty, ErrorNote, Field, LiveNumber, Modal, PageHead, useToast } from "../ui";

// Lists are static audiences: contacts are added by hand, by CSV import, by a journey step or the
// API, and stay until they're removed. Segments can include or exclude a list like any segment.

export function ListIndex() {
  const q = useQuery({ queryKey: ["lists"], queryFn: () => api.get<{ lists: Segment[] }>("/v1/lists"), refetchInterval: 3000 });
  const [creating, setCreating] = useState(false);
  const lists = q.data?.lists ?? [];
  return (
    <div className="page">
      <PageHead
        title="Lists"
        intro="Lists hold exactly the contacts you put in them, from an import, by hand, a journey step or the API. Use them for event guests, sales hand-offs or a seed list, and in segments like any other audience."
        actions={<button className="btn primary" onClick={() => setCreating(true)}>New list</button>}
      />
      {creating && <NewListModal onClose={() => setCreating(false)} />}
      <ErrorNote error={q.error} />
      <div className="panel">
        {lists.length === 0 && !q.isLoading ? (
          <Empty title="No lists yet" action={<button className="btn primary" onClick={() => setCreating(true)}>Create a list</button>}>
            Import a spreadsheet of webinar sign-ups, or paste the IDs of accounts sales is working.
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th className="num">Contacts</th></tr></thead>
            <tbody>
              {lists.map((l) => (
                <tr key={l.id}>
                  <td><Link to={`/lists/${l.id}`}>{l.name}</Link></td>
                  <td className="num"><LiveNumber value={l.size} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function NewListModal({ onClose }: { onClose: () => void }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: () => api.post<{ id: number }>("/v1/lists", { name }),
    onSuccess: (r) => { qc.invalidateQueries({ queryKey: ["lists"] }); qc.invalidateQueries({ queryKey: ["segments"] }); nav(`/lists/${r.id}`); },
  });
  return (
    <Modal title="New list" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!name.trim() || create.isPending} onClick={() => create.mutate()}>Create list</button>
    </>}>
      <Field label="Name"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Diwali webinar guests" /></Field>
      <ErrorNote error={create.error} />
    </Modal>
  );
}

export function ListDetail() {
  const { id } = useParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const seg = useQuery({ queryKey: ["segment", id], queryFn: () => api.get<Segment>(`/v1/segments/${id}`), refetchInterval: 3000 });
  const all = useSegments(0).data?.segments ?? [];
  const usedBy = all.filter((s) => segmentRefs(s.rule).includes(Number(id)));
  const names = Object.fromEntries(all.map((s) => [s.id, s.name])) as Record<number, string>;
  const [ids, setIds] = useState("");
  const [importing, setImporting] = useState(false);
  const [name, setName] = useState<string | null>(null);
  const parsed = [...new Set(ids.split(/[\s,;]+/).map((s) => s.trim()).filter(Boolean))];

  const members = useMutation({
    mutationFn: (action: "add" | "remove") => api.post<{ accepted: number }>(`/v1/lists/${id}/members`, { external_ids: parsed, action }),
    onSuccess: (r, action) => { setIds(""); toast({ text: `${action === "add" ? "Adding" : "Removing"} ${r.accepted.toLocaleString("en-IN")} contacts` }); },
  });
  const rename = useMutation({
    mutationFn: () => api.put(`/v1/lists/${id}`, { name }),
    onSuccess: () => { setName(null); qc.invalidateQueries({ queryKey: ["segment", id] }); qc.invalidateQueries({ queryKey: ["segments"] }); toast({ text: "List renamed" }); },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/segments/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["lists"] }); qc.invalidateQueries({ queryKey: ["segments"] }); toast({ text: "List deleted" }); nav("/lists"); },
  });
  const list = seg.data;

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/lists">Lists</Link>}
        title={name !== null ? (
          <form className="row" onSubmit={(e) => { e.preventDefault(); if (name.trim()) rename.mutate(); }}>
            <input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} aria-label="List name" />
            <button className="btn sm primary" disabled={!name.trim()}>Save</button>
            <button type="button" className="btn sm ghost" onClick={() => setName(null)}>Cancel</button>
          </form>
        ) : list?.name ?? "List"}
        actions={<>
          {name === null && <button className="btn" onClick={() => setName(list?.name ?? "")}>Rename</button>}
          <button className="btn danger" disabled={usedBy.length > 0} title={usedBy.length ? "Segments use this list" : undefined}
            onClick={() => confirm("Delete this list? Its contacts stay; journeys that start from it stop enrolling.") && remove.mutate()}>Delete</button>
          <button className="btn primary" onClick={() => setImporting(true)}>Import CSV</button>
        </>}
      />
      {importing && list && <ImportModal list={{ id: list.id, name: list.name }} onClose={() => setImporting(false)} />}
      <ErrorNote error={seg.error ?? rename.error ?? remove.error} />
      {list && list.kind !== "static" && <div className="alert">This is a rule-based segment. <Link to={`/segments/${list.id}`}>Open it in the segment builder</Link>.</div>}
      <div className="editor-split">
        <div className="panel"><div className="panel-body">
          <Field label="Add or remove contacts" hint="External IDs or emails used as IDs, one per line or separated by commas. Unknown IDs become new contacts.">
            <textarea className="input" rows={8} value={ids} onChange={(e) => setIds(e.target.value)} placeholder={"u-1042\nu-1077\npriya@example.com"} />
          </Field>
          <div className="row">
            <button className="btn primary" disabled={parsed.length === 0 || parsed.length > 10000 || members.isPending} onClick={() => members.mutate("add")}>
              Add {parsed.length > 0 ? parsed.length.toLocaleString("en-IN") : ""} to list
            </button>
            <button className="btn" disabled={parsed.length === 0 || parsed.length > 10000 || members.isPending} onClick={() => members.mutate("remove")}>Remove from list</button>
            {parsed.length > 10000 && <span className="small" style={{ color: "var(--danger)" }}>Up to 10,000 at a time; import a CSV for more.</span>}
          </div>
          <ErrorNote error={members.error} />
        </div></div>
        <aside className="panel" style={{ position: "sticky", top: 20 }}>
          <div className="panel-body">
            <p className="small muted">In this list now</p>
            <div className="preview-count">{list ? <LiveNumber value={list.size} /> : "…"}</div>
            <h3 style={{ margin: "18px 0 8px" }}>Used by</h3>
            {usedBy.length === 0 ? <p className="small muted">No segment uses this list yet. In the segment builder, choose “Add segment or list”.</p> : (
              <ul className="small">{usedBy.map((s) => <li key={s.id}><Link to={`/segments/${s.id}`}>{s.name}</Link> <div className="xs muted">{describe(s.rule, names)}</div></li>)}</ul>
            )}
          </div>
        </aside>
      </div>
      {list?.kind === "static" && <PreferenceSettings list={list} />}
    </div>
  );
}

/** PreferenceSettings offers a list on the preference center, where contacts join or leave it themselves. */
function PreferenceSettings({ list }: { list: Segment }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [pub, setPub] = useState<boolean | null>(null);
  const [desc, setDesc] = useState<string | null>(null);
  const isPublic = pub ?? list.public;
  const description = desc ?? list.description;
  const dirty = (pub !== null && pub !== list.public) || (desc !== null && desc !== list.description);
  const save = useMutation({
    mutationFn: () => api.put(`/v1/lists/${list.id}`, { name: list.name, public: isPublic, description }),
    onSuccess: () => {
      setPub(null); setDesc(null);
      qc.invalidateQueries({ queryKey: ["segment", String(list.id)] }); qc.invalidateQueries({ queryKey: ["lists"] });
      toast({ text: isPublic ? "Contacts can now join or leave this list from the preference center" : "Hidden from the preference center" });
    },
  });
  return (
    <section className="section">
      <h3>Preference center</h3>
      <div className="panel" style={{ marginTop: 10 }}><div className="panel-body">
        <label className="check">
          <input type="checkbox" checked={isPublic} onChange={(e) => setPub(e.target.checked)} />
          <span>Let contacts join or leave this list from their preference center</span>
        </label>
        <p className="xs muted" style={{ margin: "6px 0 12px" }}>
          The preference center opens from <code>{"{{.PreferencesURL}}"}</code> in an email, the unsubscribe page, or a link from the API.
        </p>
        <Field label="What subscribers get" hint="Shown under the list's name. Up to 500 characters.">
          <input className="input" value={description} disabled={!isPublic} maxLength={500} onChange={(e) => setDesc(e.target.value)} placeholder="Invites to our monthly live sessions" />
        </Field>
        <button className="btn primary" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>Save</button>
        <ErrorNote error={save.error} />
      </div></div>
    </section>
  );
}
