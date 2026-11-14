import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useDeferredValue, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { addFilters } from "../contactFilterMenu";
import { FilterMenu } from "./ContactFilterMenu";
import { api, CHANNEL_NAMES, CHANNELS, type Channel, type Contact, type EventRow, type Rule, type Segment, type User } from "../api";
import { contactCompany, isAccountAttr } from "../accounts";
import { useCan } from "../auth";
import { currentWord, freeText, isLinkFilter, joinQuery, splitQuery, suggest, takeChip, type Suggestion } from "../contactFilters";
import { Drawer, Empty, ErrorNote, Field, Modal, PageHead, timeAgo, useToast } from "../ui";
import { useCompanyOptions } from "./Companies";
import { ContactInbox } from "./InApp";
import { useStages } from "./Stages";
import { useAttributes, useSegments } from "./segmentData";
import { CustomFields, SegmentWhy } from "./ContactFields";
import "./contacts.css";

type ViewPage = { contacts: Contact[]; next: string; filters?: { token: string; label: string }[]; rule?: Rule; text?: string; count?: number; approximate?: boolean; note?: string };

/** Columns the list can show; External ID is always first. Attribute definitions are offered too. */
const BUILTIN_COLUMNS: { key: string; label: string }[] = [
  { key: "email", label: "Email" }, { key: "name", label: "Name" }, { key: "phone", label: "Phone" }, { key: "city", label: "City" },
  { key: "points", label: "Points" }, { key: "stage", label: "Stage" }, { key: "company_name", label: "Company" },
  { key: "owner_name", label: "Owner" }, { key: "tags", label: "Tags" }, { key: "last_seen", label: "Last seen" },
  { key: "date_added", label: "Date added" }, { key: "updated_at", label: "Last updated" },
];
const DEFAULT_COLUMNS = ["email", "name", "city", "updated_at"];
const COLUMNS_KEY = "pulse.contacts.columns";

function loadColumns(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(COLUMNS_KEY) ?? "null");
    if (Array.isArray(v) && v.every((x) => typeof x === "string")) return v;
  } catch { /* storage unavailable */ }
  return DEFAULT_COLUMNS;
}

function cell(c: Contact, key: string): ReactNode {
  if (key === "email") return c.email || <span className="faint">none</span>;
  if (key === "updated_at") return <span className="muted">{timeAgo(c.updated_at)}</span>;
  if (key === "name") {
    const n = c.attrs.name ?? [c.attrs.first_name, c.attrs.last_name].filter(Boolean).join(" ");
    return String(n ?? "");
  }
  const v = c.attrs[key];
  if (v == null) return "";
  if (Array.isArray(v)) return v.join(", ");
  if (key === "last_seen" || key === "date_added") return <span className="muted">{timeAgo(String(v))}</span>;
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

export default function Contacts() {
  const [params, setParams] = useSearchParams();
  const initial = useMemo(() => splitQuery(params.get("q") ?? ""), []); // eslint-disable-line react-hooks/exhaustive-deps
  const [chips, setChips] = useState<string[]>(initial.chips);
  const [text, setText] = useState(initial.text);
  const query = joinQuery(chips, freeText(text));
  // Typing waits 300 ms before asking the server; adding or removing a chip applies at once.
  const [debounced, setDebounced] = useState(query);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(query), 300);
    return () => clearTimeout(t);
  }, [query]);
  const [open, setOpenState] = useState<string | null>(params.get("open"));
  useEffect(() => {
    // Keep the view in the address bar, so it can be bookmarked and shared.
    const next = new URLSearchParams(params);
    if (debounced) next.set("q", debounced); else next.delete("q");
    next.delete("open");
    if (next.toString() !== params.toString()) setParams(next, { replace: true });
  }, [debounced]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    // A link to another view while this page is open (the sidebar, "View contacts") replaces the search.
    const q = params.get("q") ?? "";
    if (q !== debounced) {
      const v = splitQuery(q);
      setChips(v.chips); setText(v.text); setDebounced(q);
    }
  }, [params]); // eslint-disable-line react-hooks/exhaustive-deps
  const setOpen = (ext: string | null) => setOpenState(ext);
  const applyNow = (c: string[], t: string) => { setChips(c); setText(t); setDebounced(joinQuery(c, freeText(t))); };

  const list = useInfiniteQuery({
    queryKey: ["contacts", debounced],
    queryFn: ({ pageParam }) => api.get<ViewPage>(`/v1/contacts?q=${encodeURIComponent(debounced)}&after=${pageParam}&limit=50`),
    initialPageParam: "",
    getNextPageParam: (last) => last.next || undefined,
    placeholderData: (prev) => prev,
  });
  const first = list.data?.pages[0];
  const contacts = list.data?.pages.flatMap((p) => p.contacts) ?? [];
  const labels = new Map(first?.filters?.map((f) => [f.token, f.label]));
  const [columns, setColumnsState] = useState<string[]>(loadColumns);
  const setColumns = (c: string[]) => { setColumnsState(c); try { localStorage.setItem(COLUMNS_KEY, JSON.stringify(c)); } catch { /* storage unavailable */ } };
  const attrs = useAttributes().data;
  const columnChoices = [...BUILTIN_COLUMNS, ...(attrs?.attributes ?? []).filter((d) => !BUILTIN_COLUMNS.some((b) => b.key === d.key)).map((d) => ({ key: d.key, label: d.label || d.key }))];
  const colLabel = (k: string) => columnChoices.find((c) => c.key === k)?.label ?? k;
  const [choosing, setChoosing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [snapshot, setSnapshot] = useState(false);
  const [filtering, setFiltering] = useState(false);
  // Lists opened from a number elsewhere (a funnel step, a journey step…) can't be a dynamic
  // segment's rule; they can become a static segment of who is in them now.
  const linkList = chips.some(isLinkFilter);
  const canExport = useCan("contacts", "edit");
  const canSave = useCan("segments", "edit");
  const filtered = !!debounced;
  const exportCols = columns.filter((c) => c !== "name" && c !== "updated_at" && c !== "email");
  if (columns.includes("name")) exportCols.unshift("name", "first_name", "last_name");
  const exportHref = `/v1/contacts/export?${new URLSearchParams({ ...(debounced ? { q: debounced } : {}), columns: exportCols.join(",") })}`;
  const countText = first?.count == null ? "" : first.approximate
    ? `About ${first.count.toLocaleString("en-IN")} contacts`
    : `${first.count.toLocaleString("en-IN")} ${first.count === 1 ? "contact" : "contacts"}`;

  return (
    <div className="page">
      <PageHead
        title="Contacts"
        intro="Everyone Pulse has seen an event or profile for. Use Add filter to narrow the list (who they are, what they did and when, which campaigns they got), or type to find someone."
        actions={<>
          <Link className="btn" to="/contacts/duplicates">Duplicates</Link>
          {canExport && <Link className="btn primary" to="/contacts/import">Import</Link>}
        </>}
      />
      <div className="cv-search-row">
        <ContactSearch chips={chips} text={text} labels={labels} onText={setText} onChips={(c, t) => applyNow(c, t)} />
        <button className="btn" onClick={() => setFiltering(true)}>Add filter</button>
      </div>
      {filtering && <FilterMenu onClose={() => setFiltering(false)} onAdd={(add) => { setFiltering(false); applyNow(addFilters(chips, add), text); }} />}
      <div className="cv-bar">
        <span className="small muted" aria-live="polite">
          {list.isFetching && !list.isFetchingNextPage ? "Searching…" : filtered ? countText : ""}
        </span>
        <div className="row">
          {filtered && canSave && !linkList && (
            <button className="btn sm" disabled={!first?.rule || !!first?.text} title={first?.text ? "Free text can't be saved; use email: or name: instead" : !first?.rule ? "Add a filter first" : "A segment with these filters: contacts join and leave it as they match"}
              onClick={() => setSaving(true)}>Save as segment</button>
          )}
          {filtered && canSave && linkList && (
            <button className="btn sm" disabled={!first || (first.count ?? 0) === 0} title="A static segment of the contacts in this list now"
              onClick={() => setSnapshot(true)}>Create segment from this list</button>
          )}
          <button className="btn sm" onClick={() => setChoosing(true)} aria-haspopup="dialog">Columns</button>
          {canExport && <a className="btn sm" href={exportHref} download>Export CSV</a>}
        </div>
      </div>
      {choosing && <ColumnChooser columns={columns} choices={columnChoices} onChange={setColumns} onClose={() => setChoosing(false)} />}
      {first?.note && filtered && <p className="xs muted cv-note">{first.note}</p>}
      {snapshot && first && <SnapshotSegmentModal q={debounced} count={first.count ?? 0} approximate={!!first.approximate} labels={first.filters?.map((f) => f.label) ?? []} onClose={() => setSnapshot(false)} />}
      {saving && first?.rule && <SaveSegmentModal rule={first.rule} labels={first.filters?.map((f) => f.label) ?? []} onClose={() => setSaving(false)} />}
      <ErrorNote error={list.error} />
      <div className="panel">
        {contacts.length === 0 && !list.isLoading ? (
          <Empty title={filtered ? "No contacts match" : "No contacts yet"}>
            {filtered ? "Remove a filter, or try part of an email address or external ID." : "Send an identify event from your app or server, or import a CSV, and contacts appear here."}
          </Empty>
        ) : (
          <div className="table-scroll">
            <table className="table">
              <thead><tr><th>External ID</th>{columns.map((k) => <th key={k}>{colLabel(k)}</th>)}</tr></thead>
              <tbody>
                {contacts.map((c) => (
                  <tr key={c.id} className="clickable" onClick={() => setOpen(c.external_id)}>
                    <td>{c.external_id}</td>
                    {columns.map((k) => <td key={k}>{cell(c, k)}</td>)}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {list.hasNextPage && (
        <div style={{ marginTop: 14 }}><button className="btn" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>Load more</button></div>
      )}
      {open && <ContactDrawer externalId={open} onClose={() => setOpen(null)} />}
    </div>
  );
}

/** ContactSearch is the search box: chips for filters, free text, and suggestions as you type. */
function ContactSearch({ chips, text, labels, onText, onChips }: {
  chips: string[]; text: string; labels: Map<string, string>; onText: (t: string) => void; onChips: (chips: string[], text: string) => void;
}) {
  const segs = useSegments(0).data?.segments ?? [];
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/tags"), staleTime: 30_000 }).data?.tags ?? [];
  const stages = useStages().data ?? [];
  const companies = useCompanyOptions().data ?? [];
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<{ users: User[] }>("/v1/users"), staleTime: 60_000, retry: false }).data?.users ?? [];
  const segName = new Map(segs.map((s) => [String(s.id), s.name]));
  const [focused, setFocused] = useState(false);
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const options = focused ? suggest(text, {
    segments: segs, tags, stages: stages.map((s) => s.name), companies: companies.map((c) => c.name), users,
  }) : [];
  const pick = (s: Suggestion) => {
    if (s.chip) onChips([...chips.filter((c) => c !== s.chip), s.chip], currentWord(text).before.trimEnd());
    else if (s.fill !== undefined) onText(s.fill);
    setActive(0);
    input.current?.focus();
  };
  const change = (t: string) => {
    const taken = takeChip(t);
    if (taken) onChips([...chips.filter((c) => c !== taken.chip), taken.chip], taken.rest);
    else onText(t);
    setActive(0);
  };
  const key = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" && options.length) { e.preventDefault(); setActive((active + 1) % options.length); }
    else if (e.key === "ArrowUp" && options.length) { e.preventDefault(); setActive((active - 1 + options.length) % options.length); }
    else if ((e.key === "Enter" || e.key === "Tab") && options[active] && currentWord(text).word) { e.preventDefault(); pick(options[active]); }
    else if (e.key === "Enter") {
      e.preventDefault();
      const taken = takeChip(text, true);
      if (taken) onChips([...chips.filter((c) => c !== taken.chip), taken.chip], taken.rest);
      else onChips(chips, text);
    } else if (e.key === "Backspace" && text === "" && chips.length) onChips(chips.slice(0, -1), "");
    else if (e.key === "Escape") setFocused(false);
  };
  const chipLabel = (c: string) => {
    if (labels.has(c)) return labels.get(c)!;
    const m = c.match(/^(segment|list):(\d+)$/);
    return m && segName.has(m[2]) ? `Segment: ${segName.get(m[2])}` : c;
  };
  return (
    <div className="cv-search-wrap">
      <div className={`cv-search${focused ? " focused" : ""}`} onClick={() => input.current?.focus()}>
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden><circle cx="7" cy="7" r="4.8" /><path d="m10.6 10.6 3.6 3.6" /></svg>
        {chips.map((c) => (
          <span className="tag-chip" key={c} title={c}>
            {chipLabel(c)}
            <button aria-label={`Remove filter ${chipLabel(c)}`} onClick={(e) => { e.stopPropagation(); onChips(chips.filter((x) => x !== c), text); }}>×</button>
          </span>
        ))}
        <input ref={input} value={text} onChange={(e) => change(e.target.value)} onKeyDown={key}
          onFocus={() => setFocused(true)} onBlur={() => window.setTimeout(() => setFocused(false), 150)}
          placeholder={chips.length ? "Add a filter or search" : "Search, or filter: is:anonymous, segment:, tag:, points:>50…"}
          aria-label="Search and filter contacts" role="combobox" aria-expanded={options.length > 0} aria-controls="cv-suggestions" aria-autocomplete="list"
          aria-activedescendant={options.length ? `cv-opt-${active}` : undefined} />
        {(chips.length > 0 || text) && <button className="btn ghost sm" onClick={(e) => { e.stopPropagation(); onChips([], ""); }}>Clear</button>}
      </div>
      {options.length > 0 && (
        <ul className="cv-suggest" id="cv-suggestions" role="listbox">
          {options.map((o, i) => (
            <li key={o.label + i} id={`cv-opt-${i}`} role="option" aria-selected={i === active} className={i === active ? "active" : ""}
              onMouseDown={(e) => { e.preventDefault(); pick(o); }} onMouseEnter={() => setActive(i)}>
              <strong>{o.label}</strong>{o.hint && <span className="muted xs">{o.hint}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ColumnChooser({ columns, choices, onChange, onClose }: { columns: string[]; choices: { key: string; label: string }[]; onChange: (c: string[]) => void; onClose: () => void }) {
  // Columns keep the chooser's order, whatever order they were ticked in.
  const toggle = (k: string, on: boolean) => onChange(choices.map((c) => c.key).filter((c) => (c === k ? on : columns.includes(c))));
  return (
    <Modal title="Columns" onClose={onClose} footer={<>
      <button className="btn" onClick={() => onChange(DEFAULT_COLUMNS)}>Reset</button>
      <button className="btn primary" onClick={onClose}>Done</button>
    </>}>
      <p className="small muted" style={{ marginBottom: 12 }}>External ID always shows. The export uses the same columns. Your choice is remembered in this browser.</p>
      <div className="cv-columns">
        {choices.map((c) => (
          <label className="check" key={c.key}>
            <input type="checkbox" checked={columns.includes(c.key)} onChange={(e) => toggle(c.key, e.target.checked)} />
            {c.label} {c.label !== c.key && <code className="xs muted">{c.key}</code>}
          </label>
        ))}
      </div>
    </Modal>
  );
}

/** SaveSegmentModal stores the view's filters as a dynamic segment with the same rule. */
function SaveSegmentModal({ rule, labels, onClose }: { rule: Rule; labels: string[]; onClose: () => void }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const toast = useToast();
  const [name, setName] = useState(labels.join(", ").slice(0, 80));
  const save = useMutation({
    mutationFn: () => api.post<{ id: number }>("/v1/segments", { name: name.trim(), rule }),
    onSuccess: (r) => { qc.invalidateQueries({ queryKey: ["segments"] }); toast({ text: "Segment created. Pulse is filling it now." }); nav(`/segments/${r.id}`); },
  });
  return (
    <Modal title="Save as segment" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!name.trim() || save.isPending} onClick={() => save.mutate()}>Create segment</button>
    </>}>
      <p className="small muted" style={{ marginBottom: 12 }}>
        Creates a dynamic segment with these filters. Contacts then join and leave it on their own as they start or stop matching.
      </p>
      <Field label="Name"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} /></Field>
      <ul className="small">{labels.map((l) => <li key={l}>{l}</li>)}</ul>
      <ErrorNote error={save.error} />
    </Modal>
  );
}

/** SnapshotSegmentModal makes a static segment of the contacts in a list now. */
function SnapshotSegmentModal({ q, count, approximate, labels, onClose }: { q: string; count: number; approximate: boolean; labels: string[]; onClose: () => void }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const toast = useToast();
  const [name, setName] = useState(labels.join(", ").slice(0, 80));
  const save = useMutation({
    mutationFn: () => api.post<{ id: number; contacts: number }>("/v1/contacts/view-segment", { q, name: name.trim() }),
    onSuccess: (r) => { qc.invalidateQueries({ queryKey: ["segments"] }); toast({ text: `Static segment created. Adding ${r.contacts.toLocaleString("en-IN")} contacts now.` }); nav(`/segments/${r.id}`); },
  });
  return (
    <Modal title="Create segment from this list" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!name.trim() || save.isPending} onClick={() => save.mutate()}>Create static segment</button>
    </>}>
      <p className="small muted" style={{ marginBottom: 12 }}>
        Makes a static segment of the {approximate ? "about " : ""}{count.toLocaleString("en-IN")} contacts in this list now, for example to send them a follow-up.
        It doesn't change afterwards: contacts who match later aren't added. Up to 500,000 contacts.
      </p>
      <Field label="Name"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} /></Field>
      <ul className="small">{labels.map((l) => <li key={l}>{l}</li>)}</ul>
      <ErrorNote error={save.error} />
    </Modal>
  );
}

function ContactDrawer({ externalId, onClose }: { externalId: string; onClose: () => void }) {
  const id = encodeURIComponent(externalId);
  const c = useQuery({ queryKey: ["contact", externalId], queryFn: () => api.get<{ contact: Contact; segments: number[] }>(`/v1/contacts/${id}`), refetchInterval: 4000 });
  const evs = useQuery({ queryKey: ["contact-events", externalId], queryFn: () => api.get<{ events: EventRow[] }>(`/v1/contacts/${id}/events`), refetchInterval: 4000 });
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const names = new Map(segs.data?.segments.map((s) => [s.id, s.name]));
  const contact = c.data?.contact;
  const merged = contact?.attrs.merged_into != null;
  const [merging, setMerging] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [why, setWhy] = useState<number | null>(null);
  const [askWhy, setAskWhy] = useState("");
  const canEdit = useCan("contacts", "edit");

  return (
    <Drawer title={externalId} subtitle={contact?.email} onClose={onClose}>
      <ErrorNote error={c.error} />
      {merged && (
        <div className="alert">This contact was merged into <strong>{String(contact!.attrs.merged_into)}</strong>. Their history now belongs to that contact; new events go there too.</div>
      )}
      {contact && (
        <div className="row" style={{ marginBottom: 18 }}>
          <Link className="btn sm primary" to={`/contacts/${id}`}>Open full page</Link>
          {!merged && <button className="btn sm" onClick={() => setMerging(true)}>Merge into…</button>}
          {canEdit && <a className="btn sm" href={`/v1/contacts/${id}/data`} download
            title="Everything Pulse keeps about this person (profile, consent, segments, events) as a JSON file, for a data access request">Download data</a>}
          {canEdit && <button className="btn sm danger" onClick={() => setDeleting(true)}>Delete contact</button>}
        </div>
      )}
      {merging && contact && <MergeModal loser={contact} onClose={() => setMerging(false)} />}
      {deleting && contact && <DeleteContactModal contact={contact} onClose={() => setDeleting(false)} onDeleted={onClose} />}
      <h3 style={{ marginBottom: 10 }}>Segments</h3>
      {c.data && c.data.segments.length === 0 && <p className="muted small">Not in any segment right now.</p>}
      <div className="row" style={{ marginBottom: 10 }}>
        {c.data?.segments.map((s) => (
          <button type="button" className={`badge info seg-badge${why === s ? " on" : ""}`} key={s} onClick={() => setWhy(why === s ? null : s)} title="Why is this contact in this segment?">
            {names.get(s) ?? `Segment ${s}`} <span className="xs">· why?</span>
          </button>
        ))}
      </div>
      {segs.data && (
        <div className="row" style={{ marginBottom: 12 }}>
          <select className="select sm" value={askWhy} onChange={(e) => { setAskWhy(e.target.value); if (e.target.value) setWhy(Number(e.target.value)); }} aria-label="Check another segment">
            <option value="">Why not in another segment?</option>
            {segs.data.segments.filter((s) => s.kind === "rule" && !c.data?.segments.includes(s.id)).map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </div>
      )}
      {why !== null && <SegmentWhy externalId={externalId} segment={why} onClose={() => { setWhy(null); setAskWhy(""); }} />}
      <div style={{ marginBottom: 22 }} />

      {contact && !merged && <StageAndCompany contact={contact} />}
      {contact && <TagsAndPoints contact={contact} />}
      {contact && <CustomFields contact={contact} />}
      {contact && <ConsentPanel contact={contact} />}
      {contact && <ContactInbox externalId={contact.external_id} />}

      <h3 style={{ marginBottom: 10 }}>Attributes</h3>
      <dl className="kv" style={{ marginBottom: 22 }}>
        {contact && Object.entries(contact.attrs).filter(([k]) => !k.startsWith("optout_") && k !== "tags" && k !== "points" && !isAccountAttr(k)).sort().map(([k, v]) => (
          <div key={k} style={{ display: "contents" }}><dt>{k}</dt><dd>{typeof v === "object" ? JSON.stringify(v) : String(v)}</dd></div>
        ))}
      </dl>

      <h3 style={{ marginBottom: 10 }}>Activity</h3>
      {evs.data?.events.length === 0 && <p className="muted small">No events recorded.</p>}
      <table className="table">
        <tbody>
          {evs.data?.events.map((e) => (
            <tr key={e.id}>
              <td style={{ width: 150 }}>{e.name}</td>
              <td className="muted xs event-props">{e.props !== "{}" ? e.props : ""}</td>
              <td className="muted xs" style={{ whiteSpace: "nowrap" }}>{timeAgo(e.ts)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Drawer>
  );
}

// DeleteContactModal permanently deletes a contact (an erasure request): the profile, events,
// segment memberships, devices, inbox, journey progress and form uploads. Typing DELETE confirms it.
export function DeleteContactModal({ contact, onClose, onDeleted }: { contact: Contact; onClose: () => void; onDeleted: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [typed, setTyped] = useState("");
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/contacts/${encodeURIComponent(contact.external_id)}`),
    onSuccess: () => {
      toast({ text: `Deleted ${contact.external_id}` });
      qc.removeQueries({ queryKey: ["contact", contact.external_id] });
      qc.invalidateQueries({ queryKey: ["contacts"] });
      onClose();
      onDeleted();
    },
  });
  return (
    <Modal title={`Delete ${contact.external_id}?`} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn danger" disabled={typed !== "DELETE" || remove.isPending} onClick={() => remove.mutate()}>Delete contact</button>
    </>}>
      <p className="small muted" style={{ marginBottom: 12 }}>
        This permanently deletes the contact with their attributes, event history, segment memberships, devices, inbox messages, journey progress and files they uploaded through forms. It can't be undone. Download their data first if you need a copy. If your website or servers send new events for this ID, a new contact is created.
      </p>
      <Field label="Type DELETE to confirm"><input className="input" autoFocus value={typed} onChange={(e) => setTyped(e.target.value)} aria-label="Type DELETE to confirm" /></Field>
      <ErrorNote error={remove.error} />
    </Modal>
  );
}

// StageAndCompany shows and changes the contact's lifecycle stage and company. Both change through
// events (stage_changed, company_linked), so the activity list records every change.
export function StageAndCompany({ contact }: { contact: Contact }) {
  const qc = useQueryClient();
  const toast = useToast();
  const stages = useStages().data ?? [];
  const companies = useCompanyOptions().data ?? [];
  const id = encodeURIComponent(contact.external_id);
  const stage = contact.attrs.stage != null ? String(contact.attrs.stage) : "";
  const company = contactCompany(contact);
  const copies = Object.entries(contact.attrs).filter(([k]) => k.startsWith("company_") && k !== "company_id" && k !== "company_name").sort();
  const refresh = () => window.setTimeout(() => qc.invalidateQueries({ queryKey: ["contact", contact.external_id] }), 800);
  const setStage = useMutation({
    mutationFn: (s: string) => api.put(`/v1/contacts/${id}/stage`, { stage: s }),
    onSuccess: (_, s) => { toast({ text: `Moved to ${s}` }); refresh(); },
  });
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<{ users: User[] }>("/v1/users"), staleTime: 60_000, retry: false });
  const ownerId = contact.attrs.owner_id != null ? String(contact.attrs.owner_id) : "";
  const ownerName = contact.attrs.owner_name != null ? String(contact.attrs.owner_name) : "";
  const setOwner = useMutation({
    mutationFn: (userId: number) => api.put(`/v1/contacts/${id}/owner`, { user_id: userId }),
    onSuccess: (r) => { const o = r as { owner_name?: string }; toast({ text: o.owner_name ? `Owner: ${o.owner_name}` : "Owner removed" }); refresh(); },
  });
  const link = useMutation({
    mutationFn: (companyId: number | null) => api.put(`/v1/contacts/${id}/company`, { company_id: companyId }),
    onSuccess: (_, companyId) => { toast({ text: companyId ? `Linked to ${companies.find((c) => c.id === companyId)?.name ?? "the company"}` : "Unlinked from the company" }); refresh(); qc.invalidateQueries({ queryKey: ["companies"] }); },
  });
  return (
    <section style={{ marginBottom: 22 }}>
      <h3 style={{ marginBottom: 10 }}>Owner, stage and company</h3>
      <dl className="kv" style={{ marginBottom: 10 }}>
        <dt>Owner</dt>
        <dd>
          <div className="row">
            <select className="select inline" value={ownerId} disabled={setOwner.isPending} onChange={(e) => setOwner.mutate(Number(e.target.value || 0))} aria-label="Owner">
              <option value="">No owner</option>
              {ownerId && !users.data?.users.some((u) => String(u.id) === ownerId) && <option value={ownerId}>{ownerName || `User #${ownerId}`}</option>}
              {users.data?.users.map((u) => <option key={u.id} value={String(u.id)}>{u.name || u.email}</option>)}
            </select>
            {users.isError && <span className="xs muted">You can't list teammates, so only the current owner shows.</span>}
          </div>
        </dd>
        <dt>Stage</dt>
        <dd>
          <div className="row">
            <select className="select inline" value={stage} disabled={setStage.isPending || stages.length === 0} onChange={(e) => e.target.value && setStage.mutate(e.target.value)} aria-label="Stage">
              <option value="" disabled>{stages.length ? "No stage" : "No stages defined"}</option>
              {stage && !stages.some((s) => s.name === stage) && <option value={stage} disabled>{stage} (removed)</option>}
              {stages.map((s) => <option key={s.id} value={s.name}>{s.name}</option>)}
            </select>
            {contact.attrs.stage_changed_at != null && <span className="xs muted">since {timeAgo(String(contact.attrs.stage_changed_at))}</span>}
          </div>
        </dd>
        <dt>Company</dt>
        <dd>
          <div className="row">
            <select className="select inline" value={company ? String(company.id) : ""} disabled={link.isPending} onChange={(e) => link.mutate(e.target.value ? Number(e.target.value) : null)} aria-label="Company">
              <option value="">No company</option>
              {company && !companies.some((c) => c.id === company.id) && <option value={String(company.id)}>{company.name}</option>}
              {companies.map((c) => <option key={c.id} value={String(c.id)}>{c.name}</option>)}
            </select>
            {company && <Link className="small" to={`/companies/${company.id}`}>Open</Link>}
          </div>
        </dd>
        {copies.map(([k, v]) => (
          <div key={k} style={{ display: "contents" }}><dt className="muted">{k}</dt><dd>{String(v)}</dd></div>
        ))}
      </dl>
      <ErrorNote error={setStage.error ?? link.error ?? setOwner.error} />
    </section>
  );
}

// MergeModal merges this contact (the loser) into another one the user finds by ID, email or name.
export function MergeModal({ loser, onClose }: { loser: Contact; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [q, setQ] = useState(loser.email || "");
  const query = useDeferredValue(q.trim());
  const [winner, setWinner] = useState("");
  const found = useQuery({
    queryKey: ["contacts", "merge-search", query],
    queryFn: () => api.get<{ contacts: Contact[] }>(`/v1/contacts?q=${encodeURIComponent(query)}&limit=10`),
    enabled: query.length > 0,
  });
  const candidates = (found.data?.contacts ?? []).filter((c) => c.external_id !== loser.external_id && c.attrs.merged_into == null);
  const merge = useMutation({
    mutationFn: () => api.post("/v1/contacts/merge", { winner, loser: loser.external_id }),
    onSuccess: () => {
      toast({ text: `Merging ${loser.external_id} into ${winner}` });
      window.setTimeout(() => { qc.invalidateQueries({ queryKey: ["contact", loser.external_id] }); qc.invalidateQueries({ queryKey: ["contacts"] }); }, 1200);
      onClose();
    },
  });
  return (
    <Modal title={`Merge ${loser.external_id} into…`} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!winner || merge.isPending} onClick={() => merge.mutate()}>Merge</button>
    </>}>
      <p className="small muted" style={{ marginBottom: 12 }}>
        The contact you pick is kept: its values win, and this contact's other attributes, history, tags, points and static segments move to it. This contact then leaves every segment and its later events go to the kept one. A merge can't be undone.
      </p>
      <Field label="Find the contact to keep"><input className="input" autoFocus value={q} onChange={(e) => { setQ(e.target.value); setWinner(""); }} placeholder="ID, email, name or phone" /></Field>
      {query && candidates.length === 0 && !found.isLoading && <p className="small muted">No other contact matches.</p>}
      {candidates.length > 0 && (
        <table className="table">
          <tbody>
            {candidates.map((c) => (
              <tr key={c.id} className="clickable" onClick={() => setWinner(c.external_id)}>
                <td style={{ width: 28 }}><input type="radio" name="merge-winner" checked={winner === c.external_id} onChange={() => setWinner(c.external_id)} aria-label={`Keep ${c.external_id}`} /></td>
                <td>{c.external_id}</td>
                <td className="muted">{c.email}</td>
                <td className="muted">{String(c.attrs.name ?? c.attrs.first_name ?? "")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <ErrorNote error={merge.error ?? found.error} />
    </Modal>
  );
}

// TagsAndPoints shows the contact's tags and score and changes them. Both change through events
// (tag_added, points_changed), so the activity list records every change.
export function TagsAndPoints({ contact }: { contact: Contact }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [tag, setTag] = useState("");
  const [delta, setDelta] = useState("");
  const tags = Array.isArray(contact.attrs.tags) ? (contact.attrs.tags as string[]) : [];
  const points = Number(contact.attrs.points ?? 0) || 0;
  const id = encodeURIComponent(contact.external_id);
  const refresh = () => window.setTimeout(() => { qc.invalidateQueries({ queryKey: ["contact", contact.external_id] }); qc.invalidateQueries({ queryKey: ["tags"] }); }, 800);
  const tagMut = useMutation({
    mutationFn: (b: { add?: string[]; remove?: string[] }) => api.post(`/v1/contacts/${id}/tags`, b),
    onSuccess: (_, b) => { setTag(""); toast({ text: b.add ? `Tagged ${b.add.join(", ")}` : `Removed ${b.remove?.join(", ")}` }); refresh(); },
  });
  const pointsMut = useMutation({
    mutationFn: (n: number) => api.post(`/v1/contacts/${id}/points`, { delta: n, reason: "changed in Pulse" }),
    onSuccess: (_, n) => { setDelta(""); toast({ text: `${n > 0 ? "Added" : "Took away"} ${Math.abs(n)} points` }); refresh(); },
  });
  const validTag = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(tag.trim());
  const n = Number(delta);
  return (
    <section style={{ marginBottom: 22 }}>
      <h3 style={{ marginBottom: 10 }}>Tags and points</h3>
      <div className="row" style={{ marginBottom: 10 }}>
        {tags.length === 0 && <span className="muted small">No tags.</span>}
        {tags.map((t) => (
          <span className="tag-chip" key={t}>
            {t}
            <button aria-label={`Remove tag ${t}`} disabled={tagMut.isPending} onClick={() => tagMut.mutate({ remove: [t] })}>×</button>
          </span>
        ))}
      </div>
      <form className="row" style={{ marginBottom: 14 }} onSubmit={(e) => { e.preventDefault(); if (validTag) tagMut.mutate({ add: [tag.trim()] }); }}>
        <input className="input" style={{ maxWidth: 200 }} list="drawer-tags" placeholder="Add a tag" value={tag} onChange={(e) => setTag(e.target.value)} aria-label="New tag" />
        <TagOptions id="drawer-tags" />
        <button className="btn sm" disabled={!validTag || tagMut.isPending}>Add tag</button>
      </form>
      <div className="row">
        <span className="points-value" aria-label="Points">{points.toLocaleString("en-IN")}</span>
        <span className="small muted">points</span>
        <form className="row" onSubmit={(e) => { e.preventDefault(); if (Number.isInteger(n) && n !== 0) pointsMut.mutate(n); }}>
          <input className="input" style={{ width: 90 }} inputMode="numeric" placeholder="+10 or -5" value={delta} onChange={(e) => setDelta(e.target.value)} aria-label="Points to add" />
          <button className="btn sm" disabled={!Number.isInteger(n) || n === 0 || pointsMut.isPending}>Adjust</button>
        </form>
      </div>
      <ErrorNote error={tagMut.error ?? pointsMut.error} />
    </section>
  );
}

export function TagOptions({ id }: { id: string }) {
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/tags"), staleTime: 30_000 }).data?.tags ?? [];
  return <datalist id={id}>{tags.map((t) => <option key={t} value={t} />)}</datalist>;
}

const optedOut = (c: Contact, ch: Channel | "all") => ["true", "1", "yes"].includes(String(c.attrs[`optout_${ch}`]));

// ConsentPanel shows and changes what this contact has agreed to receive. Changes are recorded as
// events, so the contact's history shows who changed it and when.
export function ConsentPanel({ contact }: { contact: Contact }) {
  const qc = useQueryClient();
  const toast = useToast();
  const change = useMutation({
    mutationFn: ({ ch, sub }: { ch: Channel | "all"; sub: boolean }) =>
      api.post("/v1/events", { external_id: contact.external_id, name: sub ? "resubscribed" : "unsubscribed", props: { channel: ch, source: "pulse_app" } }),
    onSuccess: (_, v) => {
      toast({ text: `${v.sub ? "Subscribed to" : "Unsubscribed from"} ${v.ch === "all" ? "all messages" : CHANNEL_NAMES[v.ch]}` });
      window.setTimeout(() => qc.invalidateQueries({ queryKey: ["contact", contact.external_id] }), 800);
    },
  });
  const all = optedOut(contact, "all");
  return (
    <section style={{ marginBottom: 22 }}>
      <h3 style={{ marginBottom: 10 }}>Messages they agreed to</h3>
      {all && <div className="alert error">Unsubscribed from everything. Nothing is sent on any channel.</div>}
      <table className="table">
        <tbody>
          {CHANNELS.map((ch) => {
            const out = all || optedOut(contact, ch);
            return (
              <tr key={ch}>
                <td><span className={`chan chan-${ch}`}>{CHANNEL_NAMES[ch]}</span></td>
                <td>{out ? <span className="badge bad">Unsubscribed</span> : <span className="badge ok">Subscribed</span>}</td>
                <td className="num">
                  <button className="btn sm" disabled={change.isPending} onClick={() => change.mutate({ ch, sub: out })}>{out ? "Subscribe" : "Unsubscribe"}</button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {!all && <button className="btn ghost sm danger" style={{ marginTop: 6 }} onClick={() => confirm("Stop all messages to this contact?") && change.mutate({ ch: "all", sub: false })}>Unsubscribe from everything</button>}
      <ErrorNote error={change.error} />
    </section>
  );
}
