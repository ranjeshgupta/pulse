import { describe, expect, it } from "vitest";
import type { FormField } from "../api";
import { formPages, layoutProblems, uploadOf } from "./formPages";

const email: FormField = { key: "email", label: "Email", type: "email" };
const brk = (label = ""): FormField => ({ key: "", label, type: "page_break" });
const cv: FormField = { key: "cv", label: "CV", type: "file" };

describe("form builder pages and files", () => {
  it("splits pages", () => {
    expect(formPages([email, brk("About you"), cv]).map((p) => [p.title, p.fields.length])).toEqual([["", 1], ["About you", 1]]);
  });
  it("flags misplaced breaks and bad file fields", () => {
    expect(layoutProblems([brk(), email]).at.get(0)).toContain("not first or last");
    expect(layoutProblems([email, brk(), brk(), cv]).at.get(2)).toContain("in a row");
    expect(layoutProblems([email, { ...cv, max_mb: 50 }]).at.get(1)).toContain("1 to 10");
    expect(layoutProblems([email, ...Array.from({ length: 6 }, (_, i) => ({ ...cv, key: `f${i}` }))]).problems[0]).toContain("5 file fields");
    expect(layoutProblems([email, brk("x"), cv]).at.size).toBe(0);
  });
  it("reads upload values", () => {
    expect(uploadOf({ upload_id: 7, filename: "cv.pdf", size: 10 })).toEqual({ id: 7, filename: "cv.pdf", size: 10 });
    expect(uploadOf("text")).toBeNull();
  });
});
