import { describe, expect, it } from "vitest";
import type { AmpBlockType, AmpDoc } from "./api";
import {
  addBlock, ampMode, BLOCK_TYPES, canSaveTo, checkDoc, cleanDoc, duplicateBlock, fieldKeyFrom, moveBlock, newBlock, nextId, pollIds,
  removeBlock, starterDoc, STARTER_CODE, toBlocks, toCode, turnOff, turnOn, updateBlock,
} from "./ampBuilder";

const ids = (d: AmpDoc) => d.blocks.map((b) => b.id);
const unique = (d: AmpDoc) => new Set(ids(d)).size === d.blocks.length;

describe("block builder output", () => {
  it("gives new blocks unique ids per type", () => {
    expect(nextId("poll", [])).toBe("poll-1");
    expect(nextId("poll", ["poll-1", "poll-3"])).toBe("poll-2");
    let d: AmpDoc = { blocks: [], style: {} };
    d = addBlock(d, "poll");
    d = addBlock(d, "poll");
    d = addBlock(d, "form");
    expect(ids(d)).toEqual(["poll-1", "poll-2", "form-1"]);
  });

  it("starts every ready-to-use block type valid, and flags the ones that need content", () => {
    const needsContent: AmpBlockType[] = ["image", "button", "carousel", "gallery"];
    for (const { type } of BLOCK_TYPES) {
      const b = newBlock(type);
      expect(b.type).toBe(type);
      expect(b.id).toBe(`${type}-1`);
      const p = checkDoc({ blocks: [b], style: {} });
      if (needsContent.includes(type)) expect(p.length, type).toBeGreaterThan(0);
      else expect(p, type).toEqual([]);
    }
  });

  it("has a valid starter doc with required fields present", () => {
    const d = starterDoc();
    expect(checkDoc(d)).toEqual([]);
    expect(unique(d)).toBe(true);
    const poll = d.blocks.find((b) => b.type === "poll")!;
    expect(poll.text).toBeTruthy();
    expect(poll.options!.length).toBeGreaterThanOrEqual(2);
    expect(pollIds(d)).toEqual([poll.id]);
    // JSON round trip: what the UI sends is plain data.
    expect(JSON.parse(JSON.stringify(d))).toEqual(d);
  });

  it("moves blocks up and down within bounds", () => {
    const d = starterDoc();
    const [a, b, c] = ids(d);
    expect(ids(moveBlock(d, 0, 1))).toEqual([b, a, c]);
    expect(ids(moveBlock(d, 2, -1))).toEqual([a, c, b]);
    expect(moveBlock(d, 0, -1)).toBe(d);
    expect(moveBlock(d, 2, 1)).toBe(d);
    expect(ids(d)).toEqual([a, b, c]); // not mutated
  });

  it("removes and duplicates with a new id and a deep copy", () => {
    const d = starterDoc();
    const pi = d.blocks.findIndex((b) => b.type === "poll");
    const dup = duplicateBlock(d, pi);
    expect(dup.blocks).toHaveLength(4);
    expect(dup.blocks[pi + 1].id).toBe("poll-2");
    expect(dup.blocks[pi + 1].options).toEqual(d.blocks[pi].options);
    expect(unique(dup)).toBe(true);
    dup.blocks[pi + 1].options![0] = "Changed";
    expect(d.blocks[pi].options![0]).toBe("Great");
    expect(removeBlock(dup, pi + 1).blocks.map((b) => b.id)).toEqual(ids(d));
    expect(removeBlock(d, 9)).toBe(d);
  });

  it("adds blocks at a position", () => {
    const d = addBlock(starterDoc(), "button", 1);
    expect(d.blocks[1].type).toBe("button");
  });
});

describe("checkDoc mirrors the server rules", () => {
  const one = (patch: Partial<AmpDoc["blocks"][number]>, type: AmpBlockType = "poll") =>
    checkDoc(updateBlock({ blocks: [newBlock(type)], style: {} }, 0, patch)).map((p) => p.message);

  it("needs blocks and valid colours", () => {
    expect(checkDoc({ blocks: [], style: {} })[0].message).toMatch(/at least one block/);
    expect(checkDoc({ ...starterDoc(), style: { accent: "blue" } })[0].message).toMatch(/Accent colour/);
    expect(checkDoc({ ...starterDoc(), style: { accent: "#abc", background: "#ffffff" } })).toEqual([]);
  });

  it("checks block names", () => {
    expect(one({ id: "Poll 1" })[0]).toMatch(/lowercase/);
    const d = starterDoc();
    d.blocks[1].id = d.blocks[0].id;
    expect(checkDoc(d)).toEqual([{ block: 1, message: expect.stringMatching(/used by another block/) }]);
  });

  it("checks polls", () => {
    expect(one({ text: " " })).toContain("Write the question.");
    expect(one({ options: ["Yes"] })).toContain("A poll has 2 to 8 answers.");
    expect(one({ options: ["Yes", "yes "] })[0]).toMatch(/twice/);
    expect(one({ options: ["Yes", ""] })[0]).toMatch(/every answer/);
  });

  it("checks images, buttons and accordions", () => {
    expect(one({ image: { src: "http://x.test/a.png" } }, "image")[0]).toMatch(/https/);
    expect(one({ image: { src: "https://x.test/a.png" } }, "image")).toEqual([]);
    expect(one({ image: { src: "{{contact.photo}}" } }, "image")).toEqual([]);
    expect(one({ items: [{ src: "https://x.test/a.png" }] }, "carousel")).toContain("Add 2 to 10 images.");
    expect(one({ items: [{ src: "https://a.test/1.png" }, { src: "https://a.test/2.png" }], autoplay: 31 }, "carousel")).toEqual(["Autoplay is 0 (off) to 30 seconds."]);
    expect(one({ url: "https://shop.test" }, "button")).toEqual([]);
    expect(one({ url: "javascript:alert(1)" }, "button")[0]).toMatch(/https/);
    expect(one({ sections: [{ title: "", body: "x" }] }, "accordion")).toEqual(["Every section needs a title."]);
  });

  it("checks form fields", () => {
    expect(one({}, "form")).toEqual([]);
    expect(one({ fields: [] }, "form")).toContain("A form has 1 to 10 fields.");
    expect(one({ fields: [{ key: "answer", label: "A", type: "text" }] }, "form")[0]).toMatch(/reserved/);
    expect(one({ fields: [{ key: "Bad key", label: "A", type: "text" }] }, "form")[0]).toMatch(/saved name/);
    expect(one({ fields: [{ key: "a", label: "A", type: "text" }, { key: "a", label: "B", type: "text" }] }, "form")[0]).toMatch(/twice/);
    expect(one({ fields: [{ key: "size", label: "Size", type: "select", options: [] }] }, "form")[0]).toMatch(/1 to 20 choices/);
    expect(one({ fields: [{ key: "city", label: "City", type: "text", save_to: "email" }] }, "form")[0]).toMatch(/can't be saved/);
    expect(one({ fields: [{ key: "city", label: "City", type: "text", save_to: "home_city" }] }, "form")).toEqual([]);
  });

  it("knows which contact fields answers may go into", () => {
    for (const k of ["email", "phone", "first_name", "optout_email", "company_name", "tags", "device_type"]) expect(canSaveTo(k), k).toBe(false);
    for (const k of ["city", "plan", "favourite_colour"]) expect(canSaveTo(k), k).toBe(true);
  });

  it("names fields from labels", () => {
    expect(fieldKeyFrom("Your city?")).toBe("your_city");
    expect(fieldKeyFrom("2 Favourite colour")).toBe("favourite_colour");
    expect(fieldKeyFrom("Answer")).toBe("answer_1");
    expect(fieldKeyFrom("City", ["city"])).toBe("city_2");
    expect(fieldKeyFrom("???")).toBe("field");
  });

  it("drops empty choices before sending", () => {
    const d = updateBlock({ blocks: [newBlock("form")], style: {} }, 0, {
      fields: [{ key: "size", label: "Size", type: "select", options: ["S", " ", "M "] }, { key: "x", label: "X", type: "text", options: ["stale"] }],
    });
    const f = cleanDoc(d).blocks[0].fields!;
    expect(f[0].options).toEqual(["S", "M"]);
    expect(f[1].options).toBeUndefined();
  });
});

describe("modes", () => {
  it("reads and switches the AMP part", () => {
    expect(ampMode({})).toBe("off");
    expect(ampMode({ amp: "  " })).toBe("off");
    expect(ampMode({ ...turnOn("blocks") })).toBe("blocks");
    expect(ampMode({ ...turnOn("code") })).toBe("code");
    expect(turnOn("code").amp).toBe(STARTER_CODE);
    expect(ampMode({ amp: "<html>", ...toBlocks() })).toBe("blocks");
    const code = { amp_blocks: starterDoc(), ...toCode("<!doctype html>…") };
    expect(ampMode(code)).toBe("code");
    expect(code.amp_blocks).toBeUndefined();
    const off = { amp: "x", amp_blocks: starterDoc(), ...turnOff() };
    expect(JSON.parse(JSON.stringify(off))).toEqual({});
  });
});
