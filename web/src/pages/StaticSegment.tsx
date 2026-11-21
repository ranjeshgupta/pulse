import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Segment } from "../api";
import { useCan } from "../auth";
import { describe, segmentRefs } from "../segmentRules";
import { SegmentType, viewContactsHref } from "./Segments";
import { useSegments } from "./segmentData";
import { ask, askDelete, useEditGuard } from "../guard";
import { ErrorNote, Field, PageHead, useToast } from "../ui";
import { ContactCount } from "../ContactCount";

// Static segments (the API calls them lists) hold exactly the contacts put in them: by hand, by CSV
// import, by a journey step or the API, until they're removed. Dynamic segments can include or
// exclude a static segment like any other.

export function StaticSegmentDetail() {
  const { id } = useParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const seg = useQuery({ queryKey: ["segment", id], queryFn: () => api.get<Segment>(`/v1/segments/${id}`), refetchInterval: 3000 });
  const all = useSegments(0).data?.segments ?? [];
  const usedBy = all.filter((s) => segmentRefs(s.rule).includes(Number(id)));
  const names = Object.fromEntries(all.map((s) => [s.id, s.name])) as Record<number, string>;
  const [ids, setIds] = useState("");
  const canEdit = useCan("segments", "edit");
  const canImport = useCan("contacts", "edit");
  const [name, setName] = useState<string | null>(null);
  const parsed = [...new Set(ids.split(/[\s,;]+/).map((s) => s.trim()).filter(Boolean))];
  // Leaving with IDs typed but not added, a rename not saved, or preference changes not saved asks first.
  const [prefDirty, setPrefDirty] = useState(false);
  const dirty = ids.trim() !== "" || (name !== null && name !== (seg.data?.name ?? "")) || prefDirty;
  const guard = useEditGuard();
  const { markDirty, saved: markSaved } = guard;
  useEffect(() => { if (dirty) markDirty(); else markSaved(); }, [dirty, markDirty, markSaved]);

  const members = useMutation({
    mutationFn: (action: "add" | "remove") => api.post<{ accepted: number }>(`/v1/lists/${id}/members`, { external_ids: parsed, action }),
    onSuccess: (r, action) => { setIds(""); toast({ text: `${action === "add" ? "Adding" : "Removing"} ${r.accepted.toLocaleString("en-IN")} contacts` }); },
  });
  const rename = useMutation({
    mutationFn: () => api.put(`/v1/lists/${id}`, { name }),
    onSuccess: () => { setName(null); qc.invalidateQueries({ queryKey: ["segment", id] }); qc.invalidateQueries({ queryKey: ["segments"] }); toast({ text: "Segment renamed" }); },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/segments/${id}`),
    onSuccess: () => { markSaved(); qc.invalidateQueries({ queryKey: ["lists"] }); qc.invalidateQueries({ queryKey: ["segments"] }); toast({ text: "Segment deleted" }); nav("/segments?type=static"); },
  });
  const list = seg.data;

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/segments?type=static">Segments</Link>}
        title={name !== null ? (
          <form className="row" onSubmit={(e) => { e.preventDefault(); if (name.trim()) rename.mutate(); }}>
            <input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} aria-label="Segment name" />
            <button className="btn sm primary" disabled={!name.trim()}>Save</button>
            <button type="button" className="btn sm ghost" onClick={() => setName(null)}>Cancel</button>
          </form>
        ) : <>{list?.name ?? "Segment"} <SegmentType kind="static" /></>}
        actions={<>
          {list && <Link className="btn" to={viewContactsHref(list.id)}>View contacts</Link>}
          {canEdit && name === null && <button className="btn" onClick={() => setName(list?.name ?? "")}>Rename</button>}
          {canEdit && <button className="btn danger" disabled={usedBy.length > 0} title={usedBy.length ? "Other segments use this one" : undefined}
            onClick={() => void askDelete(list?.name ? `the static segment ${list.name}` : "this static segment",
              "Its contacts stay; journeys that start from it stop enrolling. This can't be undone.", list?.name || undefined).then((ok) => ok && remove.mutate())}>Delete</button>}
          {canImport && list && <Link className="btn primary" to={`/contacts/import?segment=${list.id}`}>Import CSV</Link>}
        </>}
      />
      <ErrorNote error={seg.error ?? rename.error ?? remove.error} />
      <div className="editor-split">
        <div className="panel"><div className="panel-body">
          <Field label="Add or remove contacts" hint="External IDs or emails used as IDs, one per line or separated by commas. Unknown IDs become new contacts.">
            <textarea className="input" rows={8} value={ids} onChange={(e) => setIds(e.target.value)} placeholder={"u-1042\nu-1077\npriya@example.com"} />
          </Field>
          <div className="row">
            <button className="btn primary" disabled={parsed.length === 0 || parsed.length > 10000 || members.isPending} onClick={() => members.mutate("add")}>
              Add {parsed.length > 0 ? parsed.length.toLocaleString("en-IN") : ""} to segment
            </button>
            <button className="btn" disabled={parsed.length === 0 || parsed.length > 10000 || members.isPending} onClick={() => void ask({
              title: `Remove ${parsed.length.toLocaleString("en-IN")} ${parsed.length === 1 ? "contact" : "contacts"} from this segment?`,
              body: "The contacts themselves stay; only their place in this segment goes.",
              confirmText: "Remove from segment", danger: true,
            }).then((ok) => ok && members.mutate("remove"))}>Remove from segment</button>
            {parsed.length > 10000 && <span className="small" style={{ color: "var(--danger)" }}>Up to 10,000 at a time; import a CSV for more.</span>}
          </div>
          <ErrorNote error={members.error} />
        </div></div>
        <aside className="panel" style={{ position: "sticky", top: 20 }}>
          <div className="panel-body">
            <p className="small muted">In this segment now</p>
            <div className="preview-count">{list ? <ContactCount live value={list.size} filters={[["in", list.id]]} /> : "…"}</div>
            <h3 style={{ margin: "18px 0 8px" }}>Used by</h3>
            {usedBy.length === 0 ? <p className="small muted">No dynamic segment uses this one yet. In the segment builder, add the filter “Segment membership”.</p> : (
              <ul className="small">{usedBy.map((s) => <li key={s.id}><Link to={`/segments/${s.id}`}>{s.name}</Link> <div className="xs muted">{describe(s.rule, names)}</div></li>)}</ul>
            )}
          </div>
        </aside>
      </div>
      {list?.kind === "static" && <PreferenceSettings list={list} onDirty={setPrefDirty} />}
    </div>
  );
}

/** PreferenceSettings offers a static segment on the preference center, where contacts join or leave it themselves. */
function PreferenceSettings({ list, onDirty }: { list: Segment; onDirty: (dirty: boolean) => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [pub, setPub] = useState<boolean | null>(null);
  const [desc, setDesc] = useState<string | null>(null);
  const isPublic = pub ?? list.public;
  const description = desc ?? list.description;
  const dirty = (pub !== null && pub !== list.public) || (desc !== null && desc !== list.description);
  useEffect(() => onDirty(dirty), [dirty, onDirty]);
  const save = useMutation({
    mutationFn: () => api.put(`/v1/lists/${list.id}`, { name: list.name, public: isPublic, description }),
    onSuccess: () => {
      setPub(null); setDesc(null);
      qc.invalidateQueries({ queryKey: ["segment", String(list.id)] }); qc.invalidateQueries({ queryKey: ["lists"] });
      toast({ text: isPublic ? "Contacts can now join or leave this segment from the preference center" : "Hidden from the preference center" });
    },
  });
  return (
    <section className="section">
      <h3>Preference center</h3>
      <div className="panel" style={{ marginTop: 10 }}><div className="panel-body">
        <label className="check">
          <input type="checkbox" checked={isPublic} onChange={(e) => setPub(e.target.checked)} />
          <span>Let contacts join or leave this segment from their preference center (shown to them as a subscription list)</span>
        </label>
        <p className="xs muted" style={{ margin: "6px 0 12px" }}>
          The preference center opens from <code>{"{{.PreferencesURL}}"}</code> in an email, the unsubscribe page, or a link from the API.
        </p>
        <Field label="What subscribers get" hint="Shown under the segment's name. Up to 500 characters.">
          <input className="input" value={description} disabled={!isPublic} maxLength={500} onChange={(e) => setDesc(e.target.value)} placeholder="Invites to our monthly live sessions" />
        </Field>
        <button className="btn primary" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>Save</button>
        <ErrorNote error={save.error} />
      </div></div>
    </section>
  );
}
