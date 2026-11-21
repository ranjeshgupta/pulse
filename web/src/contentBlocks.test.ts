import { describe, expect, it } from "vitest";
import { keyFrom } from "./pages/ContentBlocks";
import { fillSample } from "./personalize";

describe("content blocks", () => {
  it("makes keys from names", () => {
    expect(keyFrom("Festive footer!")).toBe("festive_footer");
    expect(keyFrom("  Ñame 2026 ")).toBe("name_2026");
    expect(keyFrom("Header")).toBe("header");
  });
  it("shows blocks as placeholders in the quick preview", () => {
    expect(fillSample("Hi {{block.footer}}")).toBe("Hi [block: footer]");
  });
});
