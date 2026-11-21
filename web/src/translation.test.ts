import { describe, expect, it } from "vitest";
import type { Template } from "./api";
import { applyAITranslation } from "./pages/TranslationEditor";

const base: Template = {
  id: 1, name: "t", channel: "push", subject: "", html: "", updated_at: "",
  content: { title: "Sale", body: "Today" },
  translations: [{ locale: "hi", content: { title: "पुराना" } }, { locale: "fr", content: { title: "Soldes" } }],
} as Template;

// Code review: switching language while "✨ Translate from Default" was pending wrote the answer
// into the newly opened language. The answer goes to the locale it was asked for.
describe("applyAITranslation", () => {
  it("fills only the language it was asked for, keeping other edits", () => {
    const edited = { ...base, name: "edited meanwhile" };
    const out = applyAITranslation(edited, "hi", { title: "बिक्री", body: "आज" });
    expect(out.name).toBe("edited meanwhile");
    expect(out.translations?.find((x) => x.locale === "hi")?.content).toEqual({ title: "बिक्री", body: "आज" });
    expect(out.translations?.find((x) => x.locale === "fr")?.content).toEqual({ title: "Soldes" });
  });
  it("puts subject and body into an email translation", () => {
    const email = { ...base, channel: "email", translations: [{ locale: "hi", subject: "old" }] } as Template;
    const out = applyAITranslation(email, "hi", { subject: "नया", html: "<p>x</p>" });
    expect(out.translations?.[0]).toMatchObject({ subject: "नया", html: "<p>x</p>" });
  });
  it("changes nothing when the language was removed meanwhile, or there's no template", () => {
    expect(applyAITranslation(base, "de", { title: "x" })).toBe(base);
    expect(applyAITranslation(null, "hi", { title: "x" })).toBeNull();
  });
});
