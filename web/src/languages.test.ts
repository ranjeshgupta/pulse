import { describe, expect, it } from "vitest";
import { languageName, pickTranslation, validLocale } from "./languages";

describe("languages", () => {
  it("names and checks locales", () => {
    expect(languageName("hi")).toBe("Hindi");
    expect(languageName("ta-IN")).toBe("Tamil (ta-IN)");
    expect(languageName("xx")).toBe("xx");
    expect([validLocale("hi"), validLocale("hi-IN"), validLocale("Hindi")]).toEqual([true, true, false]);
  });
  it("picks like the server", () => {
    const trs = [{ locale: "hi" }, { locale: "ta-IN" }];
    expect(pickTranslation(trs, "hi_IN")?.locale).toBe("hi");
    expect(pickTranslation(trs, "ta-in")?.locale).toBe("ta-IN");
    expect(pickTranslation(trs, "fr")).toBeUndefined();
  });
});
