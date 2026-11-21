import { afterEach, describe, expect, it, vi } from "vitest";
import { copyText } from "./ui";

// The UI crawler found "Copy a variable" throwing an unhandled rejection when the browser refused
// the clipboard write; copyText must turn every failure into a rejection the caller handles.
describe("copyText", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("rejects where there is no clipboard (a page not on https)", async () => {
    vi.stubGlobal("navigator", {});
    await expect(copyText("x")).rejects.toThrow();
  });

  it("rejects when the browser refuses or throws", async () => {
    vi.stubGlobal("navigator", { clipboard: { writeText: () => Promise.reject(new Error("denied")) } });
    await expect(copyText("x")).rejects.toThrow("denied");
    vi.stubGlobal("navigator", { clipboard: { writeText: () => { throw new Error("sync"); } } });
    await expect(copyText("x")).rejects.toThrow("sync");
  });

  it("resolves when the write works", async () => {
    const writeText = vi.fn(() => Promise.resolve());
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    await expect(copyText("{{contact.first_name}}")).resolves.toBeUndefined();
    expect(writeText).toHaveBeenCalledWith("{{contact.first_name}}");
  });
});

describe("errorText", () => {
  it("turns a malformed id in the address into not found", async () => {
    const { errorText } = await import("./ui");
    const { ApiError } = await import("./api");
    expect(errorText(new ApiError(400, "invalid id"))).toMatch(/^Not found/);
    expect(errorText(new ApiError(400, "name is required"))).toBe("name is required");
    expect(errorText(new ApiError(404, "template not found"))).toBe("template not found");
    expect(errorText("boom")).toBe("boom");
  });
});
