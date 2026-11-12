import { describe, expect, it } from "vitest";
import { barWidth, DELIVERY_HINT, funnelSteps, reasonRows, share, sideFigures, skippedRows, stepReasonGroups, type FunnelData } from "./funnelMath";

const base: FunnelData = {
  targeted: 0, skipped: { consent: 0, reachability: 0, cap: 0, missing_value: 0, deadline: 0 },
  sent: 0, delivered: 0, opened: 0, read: 0, clicked: 0, downloaded: 0, converted: 0,
  bounced: 0, hard_bounced: 0, soft_bounced: 0, complained: 0, failed: 0, unsubscribed: 0,
};

describe("share", () => {
  it("formats like the reports", () => {
    expect(share(1, 0)).toBe("–");
    expect(share(0, 10)).toBe("0%");
    expect(share(1, 40)).toBe("2.5%");
    expect(share(24, 100)).toBe("24%");
  });
});

describe("barWidth", () => {
  it("scales to the widest and keeps small counts visible", () => {
    expect(barWidth(50, 100)).toBe(50);
    expect(barWidth(1, 100000)).toBe(0.5);
    expect(barWidth(0, 100)).toBe(0);
    expect(barWidth(null, 100)).toBe(0);
    expect(barWidth(5, 0)).toBe(0);
  });
});

describe("funnelSteps", () => {
  it("builds a broadcast funnel from the audience at send time", () => {
    const f: FunnelData = {
      ...base, audience: { audience: 1000, after_exclude: 900, after_consent: 800, reachable: 700, runs: 2, ready: true },
      targeted: 700, sent: 600, delivered: 500, opened: 250, clicked: 50,
    };
    const steps = funnelSteps(f, ["email"]);
    expect(steps.map((s) => s.key)).toEqual(["audience", "after_exclude", "after_consent", "reachable", "targeted", "sent", "delivered", "opened", "clicked"]);
    expect(steps[0].label).toContain("2 sends");
    expect(steps[0].ofPrevious).toBe("");
    expect(steps[1].ofPrevious).toBe("90%");
    expect(steps[5].ofSent).toBe(""); // sent itself
    expect(steps[6].ofPrevious).toBe("83%");
    expect(steps[6].ofSent).toBe("83%");
    expect(steps[7].ofPrevious).toBe("50%"); // of delivered
    expect(steps[7].ofSent).toBe("42%");
    expect(steps[8].ofPrevious).toBe("20%"); // of opened
    expect(steps[8].ofSent).toBe("8.3%");
    expect(steps.slice(0, 5).every((s) => s.before && s.ofSent === "")).toBe(true);
  });

  it("shows delivery as not reported when nothing was delivered", () => {
    const steps = funnelSteps({ ...base, enrolled: 50, targeted: 40, sent: 40, opened: 10, clicked: 5 }, ["email"]);
    expect(steps[0]).toMatchObject({ key: "enrolled", count: 50 });
    const d = steps.find((s) => s.key === "delivered")!;
    expect(d.count).toBeNull();
    expect(d.hint).toBe(DELIVERY_HINT);
    expect(d.ofPrevious).toBe("");
    // Opens then compare with sent, the last step with a count.
    expect(steps.find((s) => s.key === "opened")!.ofPrevious).toBe("25%");
  });

  it("keeps a zero delivery when nothing was sent", () => {
    const d = funnelSteps(base, ["sms"]).find((s) => s.key === "delivered")!;
    expect(d.count).toBe(0);
    expect(d.hint).toBeUndefined();
  });

  it("picks opens and reads by channel; both compare with the step before them", () => {
    expect(funnelSteps({ ...base, sent: 10 }, ["sms"]).map((s) => s.key)).not.toContain("opened");
    const steps = funnelSteps({ ...base, targeted: 10, sent: 10, delivered: 10, opened: 4, read: 5, clicked: 2 }, ["email", "whatsapp"]);
    const by = Object.fromEntries(steps.map((s) => [s.key, s]));
    expect(by.opened.ofPrevious).toBe("40%");
    expect(by.read.ofPrevious).toBe("50%");
    expect(by.clicked.ofPrevious).toBe("50%"); // of opened
    // Without channels, opens show; reads only when there are any.
    expect(funnelSteps({ ...base, sent: 1 }).map((s) => s.key)).toEqual(["targeted", "sent", "delivered", "opened", "clicked"]);
  });

  it("compares clicks with sent or delivered when nothing was opened", () => {
    const c = funnelSteps({ ...base, targeted: 10, sent: 10, delivered: 8, clicked: 2 }, ["sms"]).find((s) => s.key === "clicked")!;
    expect(c.ofPrevious).toBe("25%");
  });
});

describe("skippedRows", () => {
  it("lists groups with contacts, largest first", () => {
    const { rows, total } = skippedRows({ consent: 3, reachability: 0, cap: 5, missing_value: 3, deadline: 0 });
    expect(rows.map((r) => r.group)).toEqual(["cap", "consent", "missing_value"]);
    expect(total).toBe(11);
    expect(skippedRows(undefined)).toEqual({ rows: [], total: 0 });
  });
});

describe("sideFigures", () => {
  it("breaks bounces down and rates failures against targeted", () => {
    const s = Object.fromEntries(sideFigures({ ...base, targeted: 200, sent: 100, bounced: 6, hard_bounced: 4, soft_bounced: 2, failed: 20 }).map((x) => [x.key, x]));
    expect(s.bounced.detail).toBe("4 hard · 2 soft");
    expect(s.bounced.rate).toBe("6.0%");
    expect(s.bounced.tone).toBe("bad");
    expect(s.failed).toMatchObject({ rate: "10%", rateOf: "targeted", tone: "warn" });
    expect(s.complained.tone).toBeUndefined();
  });
});

describe("reasonRows", () => {
  it("sorts by contacts and adds shares", () => {
    const { rows, total } = reasonRows([
      { kind: "failed", code: "a", label: "B reason", contacts: 1 },
      { kind: "bounced", code: "b", label: "A reason", contacts: 1 },
      { kind: "suppressed", code: "c", label: "Opted out", contacts: 2, group: "consent" },
      { kind: "capped", code: "d", label: "Zero", contacts: 0 },
    ]);
    expect(total).toBe(4);
    expect(rows.map((r) => r.code)).toEqual(["c", "b", "a"]);
    expect(rows[0].share).toBe(0.5);
    expect(rows[0].shareText).toBe("50%");
    expect(reasonRows(undefined).rows).toEqual([]);
  });
});

describe("stepReasonGroups", () => {
  it("orders steps by contacts, then step number", () => {
    const groups = stepReasonGroups({
      s2: [{ kind: "failed", code: "x", label: "X", contacts: 2 }],
      s1: [{ kind: "failed", code: "x", label: "X", contacts: 2 }],
      s3: [{ kind: "failed", code: "y", label: "Y", contacts: 5 }],
      s4: [],
    }, { s1: 1, s2: 2, s3: 3 });
    expect(groups.map((g) => g.id)).toEqual(["s3", "s1", "s2"]);
    expect(groups[0].total).toBe(5);
  });
});

describe("funnelSteps targeted", () => {
  it("compares Targeted with the audience after exclusions, which it can't exceed, not with Reachable", () => {
    const f: FunnelData = {
      ...base, audience: { audience: 220, after_exclude: 210, after_consent: 200, reachable: 192, runs: 1, ready: true },
      targeted: 199, sent: 190, delivered: 178,
    };
    const t = funnelSteps(f, ["email"]).find((s) => s.key === "targeted")!;
    expect(t.ofPrevious).toBe("95%");
    expect(t.ofLabel).toBe("of after exclusions");
    expect(funnelSteps(f, ["email"]).find((s) => s.key === "sent")!.ofLabel).toBeUndefined();
  });
});
