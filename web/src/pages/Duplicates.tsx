import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { api, type DuplicateGroup } from "../api";
import { mergePlan } from "../accounts";
import { Empty, ErrorNote, PageHead, timeAgo, useToast } from "../ui";
import "./duplicates.css";

type By = "email" | "phone";

// Duplicates: contacts that share an email address or phone number, with a merge that keeps the
// contact you pick and folds the others into it (their attributes fill gaps, history, tags, points
// and static segments carry over).
export function Duplicates() {
  const [by, setBy] = useState<By>("email");
  const q = useQuery({
    queryKey: ["duplicates", by],
    queryFn: () => api.get<{ groups: DuplicateGroup[]; more: boolean }>(`/v1/contacts/duplicates?by=${by}&limit=50`),
  });
  const groups = q.data?.groups ?? [];
  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/contacts">Contacts</Link>}
        title="Duplicates"
        intro="Contacts that share an email address or phone number. Pick the one to keep and merge the others into it: the kept contact's values win, the others fill in what it lacks, and their history, tags, points and static segments move to it. Merged contacts leave every segment; their later events go to the kept contact."
      />
      <div className="tabs" role="tablist" style={{ marginBottom: 14 }}>
        {(["email", "phone"] as By[]).map((b) => (
          <button key={b} role="tab" aria-selected={by === b} className={by === b ? "active" : ""} onClick={() => setBy(b)}>
            Same {b === "email" ? "email address" : "phone number"}
          </button>
        ))}
      </div>
      <ErrorNote error={q.error} />
      {groups.length === 0 && !q.isLoading ? (
        <div className="panel"><Empty title="No duplicates">No two contacts share {by === "email" ? "an email address" : "a phone number"}.</Empty></div>
      ) : (
        groups.map((g) => <GroupCard key={g.value} group={g} onMerged={() => q.refetch()} />)
      )}
      {q.data?.more && <p className="xs muted">Showing the first 50 groups. Merge these and the next ones appear.</p>}
    </div>
  );
}

function GroupCard({ group, onMerged }: { group: DuplicateGroup; onMerged: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [winner, setWinner] = useState(group.contacts[0]?.external_id ?? "");
  const merge = useMutation({
    mutationFn: async () => {
      // One at a time: each merge into the same contact is applied in order.
      for (const m of mergePlan(group.contacts, winner)) await api.post("/v1/contacts/merge", m);
    },
    onSuccess: () => {
      toast({ text: `Merged ${group.contacts.length - 1} into ${winner}` });
      window.setTimeout(() => { onMerged(); qc.invalidateQueries({ queryKey: ["contacts"] }); }, 1500);
    },
    onError: () => onMerged(),
  });
  return (
    <section className="panel dup-group">
      <div className="panel-head">
        <h3>{group.value}</h3>
        <span className="small muted">{group.total} contacts{group.total > group.contacts.length ? `, showing ${group.contacts.length}` : ""}</span>
      </div>
      <table className="table">
        <thead><tr><th>Keep</th><th>External ID</th><th>Email</th><th>Name</th><th>Stage</th><th>Created</th></tr></thead>
        <tbody>
          {group.contacts.map((c) => (
            <tr key={c.id} className={c.external_id === winner ? "dup-winner" : ""}>
              <td><input type="radio" name={`keep-${group.value}`} checked={c.external_id === winner} onChange={() => setWinner(c.external_id)} aria-label={`Keep ${c.external_id}`} /></td>
              <td>{c.external_id}</td>
              <td>{c.email || <span className="faint">none</span>}</td>
              <td>{String(c.attrs.name ?? c.attrs.first_name ?? "")}</td>
              <td>{String(c.attrs.stage ?? "")}</td>
              <td className="muted">{timeAgo(c.created_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="panel-body row">
        <button className="btn primary" disabled={!winner || merge.isPending || merge.isSuccess}
          onClick={() => confirm(`Merge ${group.contacts.length - 1} contact(s) into ${winner}? This can't be undone.`) && merge.mutate()}>
          {merge.isPending ? "Merging…" : merge.isSuccess ? "Merged" : `Merge into ${winner}`}
        </button>
        <ErrorNote error={merge.error} />
      </div>
    </section>
  );
}
