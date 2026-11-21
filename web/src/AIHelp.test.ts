import { describe, expect, it } from "vitest";
import type { Template } from "./api";
import { mainField } from "./AIHelp";

const base = { id: 1, name: "t", subject: "", html: "", updated_at: "" };

describe("AI help", () => {
  it("rewrites each channel's main field", () => {
    expect(mainField({ ...base, channel: "email", html: "<p>Hi</p>", content: {} } as Template)?.key).toBe("html");
    expect(mainField({ ...base, channel: "sms", content: { text: "Sale" } } as Template)?.value).toBe("Sale");
    expect(mainField({ ...base, channel: "push", content: { body: "Hey" } } as Template)?.key).toBe("body");
    expect(mainField({ ...base, channel: "whatsapp", content: {} } as Template)).toBeNull();
  });
});
