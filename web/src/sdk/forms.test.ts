import { describe, expect, it } from "vitest";
import { fileProblem, formPages, type FormField } from "./forms";

const email: FormField = { key: "email", label: "Email", type: "email", required: true };
const cv: FormField = { key: "cv", label: "CV", type: "file", accept: [".pdf", ".jpg"], max_mb: 2 };

describe("SDK forms", () => {
  it("splits pages at page breaks", () => {
    expect(formPages([email])).toEqual([{ title: "", fields: [email] }]);
    const pages = formPages([email, { key: "", label: "Your CV", type: "page_break" }, cv]);
    expect(pages.map((p) => [p.title, p.fields.map((f) => f.key)])).toEqual([["", ["email"]], ["Your CV", ["cv"]]]);
  });
  it("checks file size and type before sending", () => {
    expect(fileProblem(cv, { name: "cv.PDF", size: 1000 })).toBe("");
    expect(fileProblem(cv, { name: "me.jpeg", size: 1000 })).toBe("");
    expect(fileProblem(cv, { name: "cv.pdf", size: 3 * 1048576 })).toContain("too big");
    expect(fileProblem(cv, { name: "cv.exe", size: 10 })).toContain("PDF, JPG");
    expect(fileProblem({ ...cv, accept: [], max_mb: undefined }, { name: "a.svg", size: 10 })).toContain("isn't accepted");
    expect(fileProblem({ ...cv, accept: [], max_mb: undefined }, { name: "a.docx", size: 5 * 1048576 })).toBe("");
  });
});
