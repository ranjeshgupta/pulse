import { describe, expect, it } from "vitest";
import type { CampaignDashRow, MessageCounts, ReasonCount } from "./api";
import {
  barWidth, campaignHref, csvRows, CSV_HEADER, dashQuery, deliveryReported, groupReasons, nextSort, rate, rates, sectionOf, sortRows, totals,
  totalsReported,
} from "./campaignDash";

const zero: MessageCounts = {
  sent: 0, delivered: 0, read: 0, failed: 0, fallback: 0, suppressed: 0, capped: 0, deferred: 0, opened: 0, clicked: 0, unsubscribed: 0,
  converted: 0, bounced: 0, complained: 0, downloaded: 0,
};

function row(id: number, name: string, counts: Partial<MessageCounts>, extra: Partial<CampaignDashRow> = {}): CampaignDashRow {
  return {
    kind: "broadcast", id, name, category: "one_time", channels: ["email"], status: "completed", tags: [], audience: null, sent: counts.sent ?? 0,
    counts: { ...zero, ...counts }, reasons: [], revenue: 0, buyers: 0, ...extra,
  };
}

describe("rates", () => {
  it("formats with a dash for zero denominators", () => {
    expect(rate(null)).toBe("–");
    expect(rate(0)).toBe("0%");
    expect(rate(0.035)).toBe("3.5%");
    expect(rate(0.244)).toBe("24%");
  });
  it("uses delivered for opens when deliveries are reported", () => {
    const c = { ...zero, sent: 200, delivered: 100, opened: 50, clicked: 20, bounced: 4, complained: 1, unsubscribed: 2 };
    const r = rates(c, 10, true);
    expect(r.delivery).toBe(0.5);
    expect(r.open).toBe(0.5);
    expect(r.openOf).toBe("delivered");
    expect(r.click).toBe(0.1);
    expect(r.bounce).toBe(0.02);
    expect(r.complaint).toBe(0.005);
    expect(r.unsubscribe).toBe(0.01);
    expect(r.conversion).toBe(0.05);
  });
  it("falls back to sent when deliveries aren't reported", () => {
    const r = rates({ ...zero, sent: 200, delivered: 0, opened: 50 }, 0, false);
    expect(r.delivery).toBeNull();
    expect(r.open).toBe(0.25);
    expect(r.openOf).toBe("sent");
    expect(r.deliveryReported).toBe(false);
  });
  it("returns null for every rate with nothing sent", () => {
    const r = rates(zero, 0, true);
    expect([r.delivery, r.open, r.click, r.bounce, r.complaint, r.unsubscribe, r.conversion].every((x) => x === null)).toBe(true);
    expect(rate(r.click)).toBe("–");
  });
  it("needs every channel to report deliveries", () => {
    expect(deliveryReported(["email", "sms"], { email: true, sms: false })).toBe(false);
    expect(deliveryReported(["email"], { email: true })).toBe(true);
    expect(deliveryReported(["push"], {})).toBe(true);
  });
});

describe("query", () => {
  it("leaves out empty filters", () => {
    expect(dashQuery({ from: "2026-10-01", to: "2026-10-31", category: "", channel: "", tag: "", q: "  " })).toBe("from=2026-10-01&to=2026-10-31");
    expect(dashQuery({ from: "a", to: "b", category: "trigger", channel: "sms", tag: "Diwali 2026", q: " sale " }))
      .toBe("from=a&to=b&category=trigger&channel=sms&tag=Diwali+2026&q=sale");
  });
});

describe("sorting", () => {
  const reports = { email: true, sms: false };
  const a = row(1, "Alpha", { sent: 100, delivered: 90, clicked: 10 });
  const b = row(2, "beta", { sent: 300, delivered: 150, clicked: 3 });
  const c = row(3, "Gamma", { sent: 50, clicked: 25 }, { channels: ["sms"] });
  it("sorts numbers and names", () => {
    expect(sortRows([a, b, c], { key: "sent", dir: "desc" }, reports).map((r) => r.id)).toEqual([2, 1, 3]);
    expect(sortRows([c, b, a], { key: "name", dir: "asc" }, reports).map((r) => r.id)).toEqual([1, 2, 3]);
    expect(sortRows([a, b, c], { key: "click", dir: "desc" }, reports).map((r) => r.id)).toEqual([3, 1, 2]);
  });
  it("puts unknown rates last both ways", () => {
    expect(sortRows([c, a, b], { key: "delivery", dir: "desc" }, reports).map((r) => r.id)).toEqual([1, 2, 3]);
    expect(sortRows([c, a, b], { key: "delivery", dir: "asc" }, reports).map((r) => r.id)).toEqual([2, 1, 3]);
  });
  it("flips the sorted column and starts others descending", () => {
    expect(nextSort({ key: "sent", dir: "desc" }, "sent")).toEqual({ key: "sent", dir: "asc" });
    expect(nextSort({ key: "sent", dir: "desc" }, "revenue")).toEqual({ key: "revenue", dir: "desc" });
    expect(nextSort({ key: "sent", dir: "desc" }, "name")).toEqual({ key: "name", dir: "asc" });
  });
});

describe("totals", () => {
  it("adds counts, revenue and buyers", () => {
    const t = totals([row(1, "a", { sent: 10, failed: 2 }, { revenue: 99.5, buyers: 1 }), row(2, "b", { sent: 5, complained: 1 }, { revenue: 0.5, buyers: 2 })]);
    expect(t.counts.sent).toBe(15);
    expect(t.counts.failed).toBe(2);
    expect(t.counts.complained).toBe(1);
    expect(t.revenue).toBe(100);
    expect(t.buyers).toBe(3);
    expect(t.campaigns).toBe(2);
    expect(totals([]).counts.sent).toBe(0);
  });
  it("reports deliveries only when every row does", () => {
    expect(totalsReported([row(1, "a", {}), row(2, "b", {}, { channels: ["sms"] })], { sms: false })).toBe(false);
    expect(totalsReported([row(1, "a", {})], { sms: false })).toBe(true);
  });
});

describe("reasons", () => {
  const rs: ReasonCount[] = [
    { kind: "suppressed", code: "opted_out", label: "Opted out", group: "consent", contacts: 30 },
    { kind: "capped", code: "cap", label: "Over the frequency cap", contacts: 5 },
    { kind: "failed", code: "deadline", label: "Past the send deadline", group: "deadline", contacts: 2 },
    { kind: "bounced", code: "hard", label: "Mailbox doesn't exist", contacts: 12 },
    { kind: "complained", code: "spam", label: "Marked as spam", contacts: 1 },
    { kind: "failed", code: "provider_error", label: "Provider error", contacts: 60 },
    { kind: "failed", code: "zero", label: "Nothing", contacts: 0 },
  ];
  it("places each reason in a section", () => {
    expect(rs.map(sectionOf)).toEqual(["skipped", "skipped", "skipped", "bounced", "bounced", "failed", "failed"]);
  });
  it("groups, totals and sorts", () => {
    const { groups, max } = groupReasons(rs);
    expect(max).toBe(60);
    expect(groups.map((g) => [g.section, g.total])).toEqual([["skipped", 37], ["bounced", 13], ["failed", 60]]);
    expect(groups[0].reasons.map((r) => r.code)).toEqual(["opted_out", "cap", "deadline"]);
    expect(groupReasons([]).groups).toEqual([]);
  });
  it("scales bars", () => {
    expect(barWidth(60, 60)).toBe(100);
    expect(barWidth(30, 60)).toBe(50);
    expect(barWidth(1, 100000)).toBe(1);
    expect(barWidth(0, 60)).toBe(0);
    expect(barWidth(5, 0)).toBe(0);
  });
});

describe("links and CSV", () => {
  it("links to the broadcast or journey", () => {
    expect(campaignHref({ kind: "broadcast", id: 4 })).toBe("/broadcasts/4");
    expect(campaignHref({ kind: "journey", id: 9 })).toBe("/journeys/9");
  });
  it("exports one row per campaign with rates as shown", () => {
    const rows = csvRows([
      row(1, "=Sale", { sent: 100, delivered: 80, opened: 40, clicked: 10 }, { revenue: 500, buyers: 5, tags: ["a", "b"] }),
      row(2, "SMS", { sent: 10 }, { kind: "journey", category: "trigger", channels: ["sms"] }),
    ], { sms: false }, "INR");
    expect(rows[0]).toEqual(CSV_HEADER);
    const h = (name: string, r: (string | number)[]) => r[CSV_HEADER.indexOf(name)];
    expect(h("Campaign", rows[1])).toBe("=Sale");
    expect(h("Delivery rate", rows[1])).toBe("80%");
    expect(h("Open rate", rows[1])).toBe("50%");
    expect(h("Conversion rate", rows[1])).toBe("5.0%");
    expect(h("Tags", rows[1])).toBe("a | b");
    expect(h("Type", rows[2])).toBe("Trigger based");
    expect(h("Delivery rate", rows[2])).toBe("not reported");
    expect(h("Open rate of", rows[2])).toBe("sent");
    expect(h("Click rate", rows[2])).toBe("0%");
    expect(rows[1].length).toBe(CSV_HEADER.length);
  });
});
