import { describe, expect, it } from "vitest";
import { buildHeader, guessTarget, headerEnd, mappingProblems, parseRows, remapped } from "./importMapping";

describe("import column mapping", () => {
  it("guesses targets like the importer", () => {
    expect(["User ID", "Email", "First Name", "2nd phone", "﻿id"].map(guessTarget)).toEqual(["external_id", "email", "first_name", "", "external_id"]);
  });
  it("finds the end of the header row outside quotes", () => {
    const b = new TextEncoder().encode('"a\nb",c\r\nx,y\n');
    expect(headerEnd(b)).toBe(9);
    expect(headerEnd(new TextEncoder().encode("no newline"))).toBe(-1);
  });
  it("parses preview rows and drops a cut-off record", () => {
    expect(parseRows('id,note\n1,"say ""hi"", ok"\r\n2,cut', 5)).toEqual([["id", "note"], ["1", 'say "hi", ok']]);
  });
  it("checks the mapping", () => {
    expect(mappingProblems(["", "city"])[0]).toMatch(/External ID or Email/);
    expect(mappingProblems(["email", "city", "city"])[0]).toMatch(/Two columns/);
    expect(mappingProblems(["external_id", "", "city"])).toEqual([]);
  });
  it("swaps the header and keeps the rest of the file", async () => {
    const file = new Blob(["Customer,Town,Secret\nu1,Pune,x\n"]);
    const out = remapped(file, 21, ["external_id", "city", ""]);
    expect(await out.text()).toBe("external_id,city,\nu1,Pune,x\n");
    expect(buildHeader(["a,b"])).toBe('"a,b"\n');
  });
});
