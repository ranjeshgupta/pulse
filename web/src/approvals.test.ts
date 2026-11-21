import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import { approvalLine, needsApproval, type Approval } from "./approvals";

const base: Approval = { id: 1, kind: "broadcast", campaign_id: 2, requested_by: 5, requester: "asha@acme.test", note: "Diwali", status: "pending", created_at: "" };
describe("approvals", () => {
  it("recognises the four-eyes refusal", () => {
    expect(needsApproval(new ApiError(409, "this workspace needs a second person to approve every launch: use Ask for approval"))).toBe(true);
    expect(needsApproval(new ApiError(409, "this broadcast is already sending"))).toBe(false);
  });
  it("says where a request stands", () => {
    expect(approvalLine(base, 5)).toBe("You asked to launch this: “Diwali”");
    expect(approvalLine(base, 9)).toBe("asha@acme.test asked to launch this: “Diwali”");
    expect(approvalLine({ ...base, status: "rejected", decider: "Dev", decision_note: "Fix the date" })).toBe("Sent back by Dev: “Fix the date”");
  });
});
