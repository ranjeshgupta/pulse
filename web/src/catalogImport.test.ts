import { describe, expect, it } from "vitest";
import { importCloseQuestion } from "./pages/ProductCatalog";

// Code review: the import dialog closed without asking with a read file, and Esc during the
// upload closed it while the chunks kept going.
describe("importCloseQuestion", () => {
  it("closes at once with nothing read", () => {
    expect(importCloseQuestion({ uploading: false, ready: 0, done: 0 })).toBeNull();
  });
  it("asks before discarding a read file", () => {
    expect(importCloseQuestion({ uploading: false, ready: 12, done: 0 })?.title).toBe("Discard this file?");
  });
  it("asks to stop a running import, saying how far it got", () => {
    const q = importCloseQuestion({ uploading: true, ready: 5000, done: 1000 });
    expect(q?.title).toBe("Stop the import?");
    expect(String(q?.body)).toContain("1,000 of 5,000");
  });
});
