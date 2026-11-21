import { describe, expect, it } from "vitest";
import { commentSource } from "./pages/Surveys";

// Code review: with the contact hidden (a viewer who may not see contacts) every comment said
// "public link", even answers from a message.
describe("commentSource", () => {
  const at = "2026-11-21T10:00:00Z";
  it("links the contact when the server names one", () => {
    expect(commentSource({ at, text: "x", external_id: "u1" }, true)).toEqual({ contact: "u1" });
  });
  it("says public link only when it is one", () => {
    expect(commentSource({ at, text: "x", public: true }, false)).toBe("public link");
    expect(commentSource({ at, text: "x" }, true)).toBe("public link"); // the viewer would have seen an ID
  });
  it("doesn't call a hidden contact a public link", () => {
    expect(commentSource({ at, text: "x", public: false }, false)).toBe("a contact");
    expect(commentSource({ at, text: "x" }, false)).toBeNull();
  });
});
