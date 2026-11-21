// Campaign approvals (O6): pure helpers for the banner and launch buttons.
import { ApiError } from "./api";

export type ApprovalKind = "broadcast" | "journey";
export interface Approval {
  id: number; kind: ApprovalKind; campaign_id: number; requested_by: number; requester: string; note: string;
  status: "pending" | "approved" | "rejected" | "withdrawn"; decider?: string; decision_note?: string;
  created_at: string; decided_at?: string; campaign_name?: string;
}

/** needsApproval: the server refused a launch because the workspace needs a second person. */
export function needsApproval(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && /second person|Ask for approval/i.test(e.message);
}

/** approvalLine says where a request stands, in one sentence. */
export function approvalLine(a: Approval, me?: number): string {
  const who = a.requested_by && a.requested_by === me ? "You" : a.requester || "Someone";
  switch (a.status) {
    case "pending": return `${who} asked to launch this${a.note ? `: “${a.note}”` : "."}`;
    case "approved": return `Approved by ${a.decider || "someone"} and launched.`;
    case "rejected": return `Sent back by ${a.decider || "someone"}${a.decision_note ? `: “${a.decision_note}”` : "."}`;
    case "withdrawn": return a.decision_note ? `Request withdrawn: ${a.decision_note}.` : "Request withdrawn.";
  }
}
