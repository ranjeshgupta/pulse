import { describe, expect, it } from "vitest";
import { rank, type Command } from "./CommandPalette";
import { isCloseButton } from "./guard";

const cmds: Command[] = [
  { label: "Segments", to: "/segments", group: "Audience" },
  { label: "New segment", to: "/segments/new", group: "Create", keywords: "audience list" },
  { label: "Identity", to: "/settings/identity", group: "Settings", keywords: "Which details identify a person" },
];

describe("command palette", () => {
  it("ranks label starts first and needs every word", () => {
    expect(rank(cmds, "seg").map((c) => c.to)).toEqual(["/segments", "/segments/new"]);
    expect(rank(cmds, "new seg").map((c) => c.to)).toEqual(["/segments/new"]);
    expect(rank(cmds, "identify person").map((c) => c.to)).toEqual(["/settings/identity"]);
    expect(rank(cmds, "")).toHaveLength(3);
  });
});

describe("guard", () => {
  it("knows which footer buttons close without saving", () => {
    expect(isCloseButton(" Cancel ", false)).toBe(true);
    expect(isCloseButton("Not now", false)).toBe(true);
    expect(isCloseButton("Save", false)).toBe(false);
    expect(isCloseButton("Done", true)).toBe(true);
  });
});
