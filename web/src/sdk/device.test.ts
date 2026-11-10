import { describe, expect, it } from "vitest";
import { brandFromModel, parseUserAgent, type DeviceInfo, type UAData } from "./device";

type Case = [name: string, ua: string, want: Omit<DeviceInfo, "platform">, uaData?: UAData, maxTouchPoints?: number];

const cases: Case[] = [
  ["Chrome on Windows 10", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
    { device_type: "desktop", os: "Windows", os_version: "10", browser: "Chrome" }],
  ["Chrome on Windows 11 (Client Hints)", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
    { device_type: "desktop", os: "Windows", os_version: "11", browser: "Chrome" },
    { mobile: false, platform: "Windows", platformVersion: "15.0.0", brands: [{ brand: "Chromium", version: "124" }, { brand: "Google Chrome", version: "124" }, { brand: "Not-A.Brand", version: "99" }] }],
  ["Edge on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36 Edg/124.0.2478.67",
    { device_type: "desktop", os: "Windows", os_version: "10", browser: "Edge" }],
  ["Firefox on Windows 7", "Mozilla/5.0 (Windows NT 6.1; Win64; x64; rv:115.0) Gecko/20100101 Firefox/115.0",
    { device_type: "desktop", os: "Windows", os_version: "7", browser: "Firefox" }],
  ["Safari on macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15",
    { device_type: "desktop", os: "macOS", os_version: "10.15", browser: "Safari", device_brand: "Apple", device_model: "Mac" }, undefined, 0],
  ["Chrome on macOS (Client Hints)", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
    { device_type: "desktop", os: "macOS", os_version: "14.4", browser: "Chrome", device_brand: "Apple", device_model: "Mac" },
    { mobile: false, platform: "macOS", platformVersion: "14.4.1", brands: [{ brand: "Google Chrome", version: "124" }] }],
  ["Firefox on macOS", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14.4; rv:125.0) Gecko/20100101 Firefox/125.0",
    { device_type: "desktop", os: "macOS", os_version: "14.4", browser: "Firefox", device_brand: "Apple", device_model: "Mac" }],
  ["Firefox on Linux", "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:125.0) Gecko/20100101 Firefox/125.0",
    { device_type: "desktop", os: "Linux", browser: "Firefox" }],
  ["Chrome on Chrome OS", "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
    { device_type: "desktop", os: "Chrome OS", browser: "Chrome" }],
  ["Safari on iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_4_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4.1 Mobile/15E148 Safari/604.1",
    { device_type: "mobile", os: "iOS", os_version: "17.4", browser: "Safari", device_brand: "Apple", device_model: "iPhone" }],
  ["Chrome on iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/124.0.6367.88 Mobile/15E148 Safari/604.1",
    { device_type: "mobile", os: "iOS", os_version: "16.6", browser: "Chrome", device_brand: "Apple", device_model: "iPhone" }],
  ["Safari on iPad (iPadOS 12 UA)", "Mozilla/5.0 (iPad; CPU OS 12_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/12.1 Mobile/15E148 Safari/604.1",
    { device_type: "tablet", os: "iOS", os_version: "12.2", browser: "Safari", device_brand: "Apple", device_model: "iPad" }],
  ["Safari on iPad, desktop mode (iPadOS 13+)", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15",
    { device_type: "tablet", os: "iPadOS", os_version: "17.4", browser: "Safari", device_brand: "Apple", device_model: "iPad" }, undefined, 5],
  ["Chrome on Android, full UA", "Mozilla/5.0 (Linux; Android 13; Pixel 7 Build/TQ3A.230805.001) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36",
    { device_type: "mobile", os: "Android", os_version: "13", browser: "Chrome", device_brand: "Google", device_model: "Pixel 7" }],
  ["Chrome on Android, reduced UA", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Mobile Safari/537.36",
    { device_type: "mobile", os: "Android", browser: "Chrome" }],
  ["Chrome on Android, reduced UA + high-entropy hints", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Mobile Safari/537.36",
    { device_type: "mobile", os: "Android", os_version: "14", browser: "Chrome", device_brand: "Samsung", device_model: "SM-S918B" },
    { mobile: true, platform: "Android", platformVersion: "14.0.0", model: "SM-S918B", brands: [{ brand: "Google Chrome", version: "124" }] }],
  ["Samsung Internet on a Galaxy", "Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/24.0 Chrome/117.0.0.0 Mobile Safari/537.36",
    { device_type: "mobile", os: "Android", os_version: "14", browser: "Samsung Internet", device_brand: "Samsung", device_model: "SM-S918B" }],
  ["Android tablet (no Mobile token)", "Mozilla/5.0 (Linux; Android 13; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
    { device_type: "tablet", os: "Android", os_version: "13", browser: "Chrome", device_brand: "Samsung", device_model: "SM-X710" }],
  ["Opera on Android, Xiaomi", "Mozilla/5.0 (Linux; Android 12; Redmi Note 11 Build/SKQ1.211103.001) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Mobile Safari/537.36 OPR/80.4.4244.77437",
    { device_type: "mobile", os: "Android", os_version: "12", browser: "Opera", device_brand: "Xiaomi", device_model: "Redmi Note 11" }],
  ["Opera on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36 OPR/109.0.0.0",
    { device_type: "desktop", os: "Windows", os_version: "10", browser: "Opera" }],
  ["Firefox on Android", "Mozilla/5.0 (Android 14; Mobile; rv:125.0) Gecko/125.0 Firefox/125.0",
    { device_type: "mobile", os: "Android", os_version: "14", browser: "Firefox" }],
  ["Android 8.1 WebView, OnePlus, locale token", "Mozilla/5.0 (Linux; U; Android 8.1.0; en-us; ONEPLUS A6003 Build/OPM1.171019.011; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/70.0.3538.110 Mobile Safari/537.36",
    { device_type: "mobile", os: "Android", os_version: "8.1", browser: "Chrome", device_brand: "OnePlus", device_model: "ONEPLUS A6003" }],
  ["Oppo phone", "Mozilla/5.0 (Linux; Android 13; CPH2381) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36",
    { device_type: "mobile", os: "Android", os_version: "13", browser: "Chrome", device_brand: "Oppo", device_model: "CPH2381" }],
];

describe("parseUserAgent", () => {
  it.each(cases)("%s", (_name, ua, want, uaData, touch) => {
    expect(parseUserAgent(ua, uaData, touch)).toEqual({ platform: "web", ...want });
  });

  it("knows Android tablets from Client Hints and keeps values short", () => {
    const ua = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36";
    expect(parseUserAgent(ua, { mobile: false, platform: "Android" }).device_type).toBe("tablet");
    const long = parseUserAgent(ua, { mobile: true, platform: "Android", model: "X".repeat(300) });
    expect(long.device_model).toHaveLength(100);
  });

  it("returns only the platform for an empty User-Agent", () => {
    expect(parseUserAgent("")).toEqual({ platform: "web" });
  });
});

describe("brandFromModel", () => {
  it.each([
    ["SM-A546E", "Samsung"], ["Pixel 8", "Google"], ["Redmi Note 12", "Xiaomi"], ["POCO F5", "Xiaomi"],
    ["M2101K6G", "Xiaomi"], ["2201116SG", "Xiaomi"], ["Mi 9T", "Xiaomi"], ["ONEPLUS A6003", "OnePlus"],
    ["KB2003", "OnePlus"], ["CPH2449", "Oppo"], ["RMX3630", "Realme"], ["iPhone", "Apple"], ["iPad", "Apple"],
    ["moto g54 5G", "Motorola"], ["Milestone", undefined], ["Unknown 1", undefined],
  ] as const)("%s → %s", (model, brand) => {
    expect(brandFromModel(model)).toBe(brand);
  });
});
