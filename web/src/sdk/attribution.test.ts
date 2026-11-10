import { describe, expect, it } from "vitest";
import { pageProps, parseTouch, profileAttrs, referrerDomain, touchFromUrl } from "./attribution";

describe("UTM attribution", () => {
  it("reads utm parameters and the referring site", () => {
    expect(touchFromUrl("https://shop.test/sale?utm_source=google&utm_medium=cpc&utm_campaign=diwali&x=1", "https://www.google.com/search?q=a"))
      .toEqual({ source: "google", medium: "cpc", campaign: "diwali", referrer: "www.google.com" });
    expect(touchFromUrl("https://shop.test/?utm_campaign=%20spring%20", "https://shop.test/home")).toEqual({ campaign: "spring" });
  });
  it("ignores visits without utm parameters and bad URLs", () => {
    expect(touchFromUrl("https://shop.test/?ref=x", "https://news.test/")).toBeNull();
    expect(touchFromUrl("not a url")).toBeNull();
    expect(referrerDomain("", "shop.test")).toBe("");
  });
  it("caps long values", () => {
    expect(touchFromUrl(`https://shop.test/?utm_campaign=${"a".repeat(500)}`)!.campaign!.length).toBe(200);
  });
  it("maps touches to profile attributes and page view properties", () => {
    const first = { source: "newsletter", campaign: "launch", referrer: "mail.test" };
    const last = { source: "google", medium: "cpc", campaign: "diwali" };
    expect(profileAttrs(first, last)).toEqual({
      first_utm_source: "newsletter", first_utm_campaign: "launch", first_referrer: "mail.test",
      last_utm_source: "google", last_utm_medium: "cpc", last_utm_campaign: "diwali",
    });
    expect(profileAttrs(null, null)).toEqual({});
    expect(pageProps(first)).toEqual({ utm_source: "newsletter", utm_campaign: "launch", referrer_domain: "mail.test" });
  });
  it("parses stored touches defensively", () => {
    expect(parseTouch('{"campaign":"x","evil":{"a":1},"source":5}')).toEqual({ campaign: "x" });
    expect(parseTouch("{nope")).toBeNull();
    expect(parseTouch('"str"')).toBeNull();
  });
});
