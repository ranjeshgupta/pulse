import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useDeferredValue, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Company, type Contact } from "../api";
import { attrsFromRows, companyProblems, rowsFromAttrs, type AttrRow } from "../accounts";
import { Empty, ErrorNote, Field, LiveNumber, Modal, PageHead, timeAgo, useToast } from "../ui";
import "./companies.css";

/** Companies for pickers (segment builder, contact drawer): the first 200 by name. */
export function useCompanyOptions() {
  return useQuery({
    queryKey: ["companies", "options"],
    queryFn: () => api.get<{ companies: Company[] }>("/v1/companies?limit=200"),
    select: (d) => d.companies,
    staleTime: 30_000,
    retry: false,
  });
}

// Companies are B2B accounts. A contact links to one; the company's name and attributes are copied
// to the contact (company_name, company_<key>), so segments and journeys use them like any attribute.
export function CompanyList() {
  const [q, setQ] = useState("");
  const query = useDeferredValue(q);
  const [creating, setCreating] = useState(false);
  const list = useInfiniteQuery({
    queryKey: ["companies", "list", query],
    queryFn: ({ pageParam }) => api.get<{ companies: Company[]; next: string }>(`/v1/companies?q=${encodeURIComponent(query)}&offset=${pageParam}&limit=50`),
    initialPageParam: "0",
    getNextPageParam: (last) => last.next || undefined,
    refetchInterval: 5000,
  });
  const companies = list.data?.pages.flatMap((p) => p.companies) ?? [];
  return (
    <div className="page">
      <PageHead
        title="Companies"
        intro="The accounts your contacts work for. Link a contact by hand, or send an identify with a company attribute from your server and Pulse links it (creating the company when it's new). Segments can use any company attribute, like industry or plan."
        actions={<button className="btn primary" onClick={() => setCreating(true)}>New company</button>}
      />
      {creating && <NewCompanyModal onClose={() => setCreating(false)} />}
      <div className="row" style={{ marginBottom: 14 }}>
        <input className="input" style={{ maxWidth: 380 }} placeholder="Search by name or domain" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search companies" />
      </div>
      <ErrorNote error={list.error} />
      <div className="panel">
        {companies.length === 0 && !list.isLoading ? (
          <Empty title={query ? "No companies match" : "No companies yet"} action={!query && <button className="btn primary" onClick={() => setCreating(true)}>Add a company</button>}>
            {query ? "Try part of the name or domain." : "Add one here, or send identify events with attrs.company from your server."}
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th>Domain</th><th>Attributes</th><th className="num">Contacts</th><th>Updated</th></tr></thead>
            <tbody>
              {companies.map((c) => (
                <tr key={c.id}>
                  <td><Link to={`/companies/${c.id}`}>{c.name}</Link></td>
                  <td>{c.domain || <span className="faint">none</span>}</td>
                  <td className="muted small company-attr-summary">{Object.entries(c.attrs).map(([k, v]) => `${k}: ${String(v)}`).join(", ")}</td>
                  <td className="num"><LiveNumber value={c.members} /></td>
                  <td className="muted">{timeAgo(c.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {list.hasNextPage && (
        <div style={{ marginTop: 14 }}><button className="btn" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>Load more</button></div>
      )}
    </div>
  );
}

function NewCompanyModal({ onClose }: { onClose: () => void }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [domain, setDomain] = useState("");
  const issues = companyProblems(name, domain, []);
  const create = useMutation({
    mutationFn: () => api.post<{ id: number }>("/v1/companies", { name: name.trim(), domain: domain.trim(), attrs: {} }),
    onSuccess: (r) => { qc.invalidateQueries({ queryKey: ["companies"] }); nav(`/companies/${r.id}`); },
  });
  return (
    <Modal title="New company" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={issues.length > 0 || create.isPending} onClick={() => create.mutate()}>Create company</button>
    </>}>
      <Field label="Name"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Acme Retail" /></Field>
      <Field label="Domain (optional)"><input className="input" value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="acme.com" /></Field>
      <ErrorNote error={create.error} />
    </Modal>
  );
}

export function CompanyDetail() {
  const { id } = useParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["company", id], queryFn: () => api.get<{ company: Company; members: Contact[] }>(`/v1/companies/${id}`), refetchInterval: 5000 });
  const company = q.data?.company;
  const [form, setForm] = useState<{ name: string; domain: string; rows: AttrRow[] } | null>(null);
  useEffect(() => {
    if (company && !form) setForm({ name: company.name, domain: company.domain, rows: rowsFromAttrs(company.attrs) });
  }, [company, form]);
  const issues = form ? companyProblems(form.name, form.domain, form.rows) : [];
  const dirty = !!form && !!company && (form.name !== company.name || form.domain !== company.domain || JSON.stringify(attrsFromRows(form.rows)) !== JSON.stringify(company.attrs));
  const save = useMutation({
    mutationFn: () => api.put(`/v1/companies/${id}`, { name: form!.name.trim(), domain: form!.domain.trim(), attrs: attrsFromRows(form!.rows) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["companies"] });
      qc.invalidateQueries({ queryKey: ["company", id] }).then(() => setForm(null));
      toast({ text: `Saved. ${company?.members ? `Its ${company.members.toLocaleString("en-IN")} contacts update within seconds.` : ""}` });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/companies/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["companies"] }); toast({ text: "Company deleted; its contacts are being unlinked" }); nav("/companies"); },
  });
  const setRow = (i: number, r: AttrRow) => form && setForm({ ...form, rows: form.rows.map((x, j) => (j === i ? r : x)) });
  const members = q.data?.members ?? [];
  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/companies">Companies</Link>}
        title={company?.name ?? "Company"}
        intro={company?.domain || undefined}
        actions={<button className="btn danger" onClick={() => confirm(`Delete ${company?.name}? Its contacts stay, unlinked from it.`) && remove.mutate()}>Delete</button>}
      />
      <ErrorNote error={q.error ?? remove.error} />
      {form && (
        <div className="editor-split">
          <div className="panel"><div className="panel-body">
            <div className="grid-2">
              <Field label="Name"><input className="input" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
              <Field label="Domain"><input className="input" value={form.domain} onChange={(e) => setForm({ ...form, domain: e.target.value })} placeholder="acme.com" /></Field>
            </div>
            <div className="field">
              <span>Attributes</span>
              <small>Each one is copied to the company's contacts as <code>company_&lt;name&gt;</code>, e.g. <code>company_industry</code>, for segments and personalisation. Numbers and true/false keep their type.</small>
              {form.rows.map((r, i) => (
                <div className="row company-attr-row" key={i}>
                  <input className="input" placeholder="industry" value={r.key} onChange={(e) => setRow(i, { ...r, key: e.target.value })} aria-label="Attribute name" />
                  <input className="input" placeholder="Retail" value={r.value} onChange={(e) => setRow(i, { ...r, value: e.target.value })} aria-label="Value" />
                  <button className="btn ghost sm" aria-label="Remove attribute" onClick={() => setForm({ ...form, rows: form.rows.filter((_, j) => j !== i) })}>×</button>
                </div>
              ))}
              <div><button className="btn sm" onClick={() => setForm({ ...form, rows: [...form.rows, { key: "", value: "" }] })}>Add attribute</button></div>
            </div>
            {issues.length > 0 && dirty && <ul className="small company-issues">{issues.map((p) => <li key={p}>{p}</li>)}</ul>}
            <div className="row">
              <button className="btn primary" disabled={!dirty || issues.length > 0 || save.isPending} onClick={() => save.mutate()}>Save</button>
              {dirty && <button className="btn ghost" onClick={() => setForm(null)}>Discard changes</button>}
            </div>
            <ErrorNote error={save.error} />
          </div></div>
          <aside className="panel" style={{ position: "sticky", top: 20 }}>
            <div className="panel-body">
              <p className="small muted">Contacts at this company</p>
              <div className="preview-count">{company ? <LiveNumber value={company.members} /> : "…"}</div>
              <p className="xs muted" style={{ marginTop: 10 }}>Segment on it with the “Company” filter, or on its fields with “Company name” and “Other company field”.</p>
            </div>
          </aside>
        </div>
      )}
      <section className="section">
        <h3>Contacts</h3>
        <div className="panel" style={{ marginTop: 10 }}>
          {members.length === 0 ? (
            <Empty title="No contacts linked yet">Link a contact from its drawer under Contacts, or send an identify naming this company.</Empty>
          ) : (
            <table className="table">
              <thead><tr><th>External ID</th><th>Email</th><th>Name</th><th>Stage</th></tr></thead>
              <tbody>
                {members.map((c) => (
                  <tr key={c.id} className="clickable" onClick={() => nav(`/contacts?open=${encodeURIComponent(c.external_id)}`)}>
                    <td>{c.external_id}</td>
                    <td>{c.email || <span className="faint">none</span>}</td>
                    <td>{String(c.attrs.name ?? c.attrs.first_name ?? "")}</td>
                    <td>{String(c.attrs.stage ?? "")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        {company && company.members > members.length && <p className="xs muted" style={{ marginTop: 8 }}>Showing the first {members.length} of {company.members.toLocaleString("en-IN")}. Segment on the company to see them all.</p>}
      </section>
    </div>
  );
}
