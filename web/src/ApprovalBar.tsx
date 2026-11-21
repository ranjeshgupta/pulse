// The approval banner on a campaign (O6) and the "Ask for approval" dialog.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { api, can } from "./api";
import { useMe } from "./auth";
import { approvalLine, type Approval, type ApprovalKind } from "./approvals";
import { ask } from "./guard";
import { ErrorNote, Field, Modal, useToast } from "./ui";

const area = (k: ApprovalKind) => (k === "journey" ? "journeys" : "broadcasts");

export function useLatestApproval(kind: ApprovalKind, id?: number) {
  return useQuery({
    queryKey: ["approval", kind, id], enabled: !!id, refetchInterval: 15_000,
    queryFn: () => api.get<{ approval: Approval | null; changed_since?: boolean }>(`/v1/approvals/latest?kind=${kind}&id=${id}`),
  });
}

/** ApprovalBar: the campaign's latest request, with approve / send back / withdraw. */
export function ApprovalBar({ kind, id, onLaunched }: { kind: ApprovalKind; id: number; onLaunched?: () => void }) {
  const me = useMe().data;
  const qc = useQueryClient();
  const toast = useToast();
  const q = useLatestApproval(kind, id);
  const a = q.data?.approval;
  const [backOpen, setBackOpen] = useState(false);
  const [backNote, setBackNote] = useState("");
  const canPublish = can(me?.permissions, area(kind), "publish");
  const decide = useMutation({
    mutationFn: ({ action, note }: { action: "approve" | "reject" | "withdraw"; note?: string }) => api.post(`/v1/approvals/${a!.id}/${action}`, note ? { note } : undefined),
    onSuccess: (_, v) => {
      qc.invalidateQueries({ queryKey: ["approval", kind, id] });
      qc.invalidateQueries({ queryKey: ["approvals"] });
      qc.invalidateQueries({ queryKey: [kind, String(id)] });
      qc.invalidateQueries({ queryKey: [kind === "journey" ? "journeys" : "broadcasts"] });
      toast({ text: v.action === "approve" ? "Approved: it's launching." : v.action === "reject" ? "Sent back with your note." : "Request withdrawn." });
      if (v.action === "approve") onLaunched?.();
    },
  });
  if (!a || (a.status !== "pending" && a.status !== "rejected")) return null;
  if (a.status === "rejected" && a.decided_at && Date.now() - new Date(a.decided_at).getTime() > 14 * 86_400_000) return null;
  const mine = !!me?.user && a.requested_by === me.user.id;
  const sendBack = () => setBackOpen(true);
  return (
    <div className={`alert ${a.status === "pending" ? "info" : "error"} approval-bar`}>
      <div><strong>{a.status === "pending" ? "⏳ Waiting for approval" : "↩️ Sent back"}</strong> — {approvalLine(a, me?.user?.id)}</div>
      {a.status === "pending" && q.data?.changed_since && <div className="xs">It changed after the request, so approving won't launch it: ask again.</div>}
      <ErrorNote error={decide.error} />
      {a.status === "pending" && (
        <div className="row" style={{ marginTop: 8 }}>
          {canPublish && !mine && <button className="btn primary sm" disabled={decide.isPending || q.data?.changed_since}
            onClick={() => void ask({ title: `Approve and launch ${a.campaign_name || "this campaign"}?`, body: "It launches now, as it was when they asked.", confirmText: "Approve and launch" }).then((ok) => ok && decide.mutate({ action: "approve" }))}>✅ Approve and launch</button>}
          {canPublish && !mine && <button className="btn sm" disabled={decide.isPending} onClick={sendBack}>Send back</button>}
          {(mine || canPublish) && <button className="btn ghost sm" disabled={decide.isPending} onClick={() => decide.mutate({ action: "withdraw" })}>Withdraw</button>}
          {mine && !canPublish && <span className="xs muted">Someone who may publish campaigns gets it on their Campaigns page.</span>}
        </div>
      )}
      {backOpen && (
        <Modal title="Send it back" onClose={() => setBackOpen(false)} footer={<>
          <button className="btn" onClick={() => setBackOpen(false)}>Cancel</button>
          <button className="btn primary" disabled={!backNote.trim() || decide.isPending} onClick={() => { decide.mutate({ action: "reject", note: backNote.trim() }); setBackOpen(false); }}>Send back</button>
        </>}>
          <Field label="What should change? They'll see this."><textarea className="textarea" rows={3} autoFocus value={backNote} maxLength={1000} onChange={(e) => setBackNote(e.target.value)} /></Field>
        </Modal>
      )}
    </div>
  );
}

/** AskApprovalModal: a note, then the request. */
export function AskApprovalModal({ kind, id, reason, onClose }: { kind: ApprovalKind; id: number; reason?: string; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [note, setNote] = useState("");
  const send = useMutation({
    mutationFn: () => api.post("/v1/approvals", { kind, campaign_id: id, note }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["approval", kind, id] });
      qc.invalidateQueries({ queryKey: ["approvals"] });
      toast({ text: "Asked for approval. You'll see the answer here." });
      onClose();
    },
  });
  return (
    <Modal title="Ask for approval" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={send.isPending} onClick={() => send.mutate()}>{send.isPending ? "Asking…" : "Ask for approval"}</button>
    </>}>
      <p className="small" style={{ marginTop: 0 }}>{reason ?? "Someone who may publish campaigns approves it, and it launches exactly as it is now. If you change it before then, ask again."}</p>
      <ErrorNote error={send.error} />
      <Field label="Note for the approver (optional)"><textarea className="textarea" rows={3} autoFocus value={note} maxLength={1000} placeholder="Diwali send to all Pune customers, offer valid till Sunday." onChange={(e) => setNote(e.target.value)} /></Field>
    </Modal>
  );
}

/** PendingApprovals: the requests waiting for someone who may publish (Campaigns page). */
export function PendingApprovals() {
  const me = useMe().data;
  const q = useQuery({ queryKey: ["approvals", "pending"], queryFn: () => api.get<{ approvals: Approval[] }>("/v1/approvals?status=pending"), refetchInterval: 30_000 });
  const list = (q.data?.approvals ?? []).filter((a) => can(me?.permissions, area(a.kind), "publish") && a.requested_by !== me?.user?.id);
  if (list.length === 0) return null;
  return (
    <div className="alert info" style={{ marginBottom: 16 }}>
      <strong>⏳ {list.length} campaign{list.length === 1 ? "" : "s"} waiting for your approval:</strong>{" "}
      {list.slice(0, 5).map((a, i) => (
        <span key={a.id}>{i ? ", " : ""}<Link to={a.kind === "journey" ? `/journeys/${a.campaign_id}` : `/broadcasts/${a.campaign_id}`}>{a.campaign_name || `#${a.campaign_id}`}</Link> <span className="xs muted">by {a.requester}</span></span>
      ))}
      {list.length > 5 && <> and {list.length - 5} more</>}
    </div>
  );
}
