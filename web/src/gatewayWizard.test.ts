import { describe, expect, it } from "vitest";
import { reportMode, stepDone } from "./pages/GatewayWizard";
import { newCustomRequest, serializeRequest, statusWordsIn, suggestStatuses } from "./providerAuth";

describe("gateway wizard", () => {
  it("suggests Pulse statuses for common gateway words", () => {
    expect(suggestStatuses(["DELIVRD", "UNDELIV", "REJECTD", "SUBMITTED", "read", "weird"])).toEqual({
      DELIVRD: "delivered", UNDELIV: "undeliverable", REJECTD: "failed", SUBMITTED: "pending", read: "read",
    });
  });
  it("finds status words in a pasted report", () => {
    expect(statusWordsIn('[{"d":{"s":"DELIVRD"}},{"d":{"s":"FAILED"}}]', "d.s")).toEqual(["DELIVRD", "FAILED"]);
    expect(statusWordsIn('{"items":[{"s":"A"},{"s":"B"}]}', "items.s")).toEqual(["A", "B"]);
    expect(statusWordsIn("not json", "s")).toEqual([]);
  });
  it("knows the report mode and finished steps", () => {
    const r = newCustomRequest("sms");
    expect(reportMode(r)).toBe("none");
    expect(reportMode({ ...r, receipt: { id_path: "id", status_path: "s", statuses: {}, verify: "query", verify_name: "token" } })).toBe("webhook");
    expect(reportMode({ ...r, poll: { method: "GET", url: "https://x.test" }, receipt: { id_path: "id", status_path: "s", statuses: {}, verify: "", verify_name: "" } })).toBe("poll");
    expect(stepDone(2, { ...r, url: "https://api.test/send" }, "none")).toBe(true);
    expect(stepDone(3, { ...r, id_path: "" }, "none")).toBe(false);
    expect(stepDone(3, { ...r, id_path: "", match_by: "pulse" }, "none")).toBe(true);
  });
  it("drops an empty failure rule and the default match", () => {
    const out = JSON.parse(serializeRequest({ ...newCustomRequest("sms"), failure: { json_path: "", values: [" "] }, match_by: "provider" }));
    expect(out.failure).toBeUndefined();
    expect(out.match_by).toBeUndefined();
    const kept = JSON.parse(serializeRequest({ ...newCustomRequest("sms"), failure: { json_path: "status", values: ["error", " failed "] } }));
    expect(kept.failure.values).toEqual(["error", "failed"]);
    // Half filled in (field typed first) stays while editing.
    expect(JSON.parse(serializeRequest({ ...newCustomRequest("sms"), failure: { json_path: "status", values: [] } })).failure.json_path).toBe("status");
  });
});
