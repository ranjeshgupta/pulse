// Device detection for the web SDK: platform, device type, OS, browser, brand and model from the
// User-Agent string and, where the browser has it, navigator.userAgentData (User-Agent Client Hints).
// Everything here is pure so it can be tested without a browser.

/** Device properties as the Pulse server reads them (flat strings, each at most 100 characters). */
export interface DeviceInfo {
  platform: "web";
  device_type?: "desktop" | "mobile" | "tablet";
  os?: string;
  os_version?: string;
  browser?: string;
  device_brand?: string;
  device_model?: string;
}

/** The parts of navigator.userAgentData we use; model and platformVersion are high-entropy values. */
export interface UAData {
  mobile?: boolean;
  platform?: string;
  brands?: { brand: string; version: string }[];
  model?: string;
  platformVersion?: string;
}

const MAX = 100;
const clip = (s: string | undefined) => {
  const t = s?.trim();
  return t ? t.slice(0, MAX) : undefined;
};

/** majorMinor turns "14.4.1" into "14.4" and "14.0.0" into "14". */
function majorMinor(v: string | undefined): string | undefined {
  const m = /^(\d+)(?:[._](\d+))?/.exec(v ?? "");
  if (!m) return undefined;
  return m[2] && m[2] !== "0" ? `${m[1]}.${m[2]}` : m[1];
}

const WINDOWS_NT: Record<string, string> = { "10.0": "10", "6.3": "8.1", "6.2": "8", "6.1": "7", "6.0": "Vista", "5.1": "XP", "5.2": "XP" };

/** brandFromModel guesses the manufacturer from a device model name (SM-S918B → Samsung). */
export function brandFromModel(model: string | undefined): string | undefined {
  if (!model) return undefined;
  const rules: [RegExp, string][] = [
    [/^(iPhone|iPad|iPod|Mac)/, "Apple"],
    [/^(SM-|GT-|SCH-|SGH-|SHV-|SAMSUNG|Galaxy)/i, "Samsung"],
    [/^(Pixel|Nexus)/i, "Google"],
    [/^(Redmi|POCO|Xiaomi|Mi[ \d]|MI[ \d]|M2\d{3}|2\d{5,}[A-Z])/i, "Xiaomi"],
    [/^(ONEPLUS|OnePlus|(GM|HD|IN|KB|LE|NE|PJ)\d{4}$)/i, "OnePlus"],
    [/^(CPH\d|OPPO)/i, "Oppo"],
    [/^RMX\d/i, "Realme"],
    [/^(vivo|V\d{4}[A-Z]?$)/i, "vivo"],
    [/^(moto|XT\d{4})/i, "Motorola"],
    [/^(Nokia|TA-\d{4})/i, "Nokia"],
    [/^(HUAWEI|HONOR)/i, "Huawei"],
    [/^(LM-|LG-)/i, "LG"],
    [/^(Infinix)/i, "Infinix"],
    [/^(TECNO)/i, "Tecno"],
  ];
  for (const [re, brand] of rules) if (re.test(model)) return brand;
  return undefined;
}

/** androidModel reads the model from "(Linux; Android 14; SM-S918B Build/UP1A…)"; the reduced
 * User-Agent's "K" placeholder means unknown. */
function androidModel(ua: string): string | undefined {
  const paren = /\(([^)]*Android[^)]*)\)/.exec(ua);
  if (!paren) return undefined;
  const parts = paren[1].split(";").map((p) => p.trim());
  const i = parts.findIndex((p) => /^Android\b/.test(p));
  if (i < 0) return undefined;
  for (const p of parts.slice(i + 1)) {
    if (!p || /^(K|U|wv|Mobile|Tablet|TV)$/i.test(p) || /^rv:/.test(p) || /^[a-z]{2}([-_][a-z]{2,4})?$/i.test(p)) continue;
    const model = p.replace(/\s*Build\/.*$/, "").trim();
    return model && model !== "K" ? model : undefined;
  }
  return undefined;
}

function browserFromUA(ua: string): string | undefined {
  if (/SamsungBrowser\//.test(ua)) return "Samsung Internet";
  if (/\bOPR\/|\bOPT\/|Opera|\bOPX\//.test(ua)) return "Opera";
  if (/\bEdg(e|A|iOS)?\//.test(ua)) return "Edge";
  if (/YaBrowser\//.test(ua)) return "Yandex";
  if (/\b(Firefox|FxiOS)\//.test(ua)) return "Firefox";
  if (/\b(CriOS|Chrome|Chromium)\//.test(ua)) return "Chrome";
  if (/Version\/[\d.]+.*Safari\//.test(ua)) return "Safari";
  return undefined;
}

function browserFromBrands(brands: UAData["brands"]): string | undefined {
  const names = (brands ?? []).map((b) => b.brand);
  if (names.includes("Microsoft Edge")) return "Edge";
  if (names.some((n) => /^Opera/.test(n))) return "Opera";
  if (names.includes("Samsung Internet")) return "Samsung Internet";
  if (names.includes("Google Chrome")) return "Chrome";
  return undefined;
}

/**
 * parseUserAgent works out the device properties from a User-Agent string, refined by
 * navigator.userAgentData when given (uaData), and maxTouchPoints, which tells an iPad in desktop
 * mode (iPadOS 13+ reports itself as a Mac) from a real Mac. Unknown values are left out.
 */
export function parseUserAgent(ua: string, uaData?: UAData, maxTouchPoints?: number): DeviceInfo {
  ua = ua || "";
  let device_type: DeviceInfo["device_type"];
  let os: string | undefined;
  let os_version: string | undefined;
  let model: string | undefined;
  let reduced = false; // Chrome's reduced User-Agent: the Android version and model are frozen

  let m: RegExpExecArray | null;
  if ((m = /\b(iPhone|iPod)\b/.exec(ua))) {
    os = "iOS"; device_type = "mobile"; model = m[1];
    os_version = majorMinor(/\bOS (\d+[_.]\d+)/.exec(ua)?.[1]);
  } else if (/\biPad\b/.test(ua)) {
    os_version = majorMinor(/\bOS (\d+[_.]\d+)/.exec(ua)?.[1]);
    os = os_version && parseInt(os_version, 10) < 13 ? "iOS" : "iPadOS";
    device_type = "tablet"; model = "iPad";
  } else if (/Macintosh/.test(ua) && (maxTouchPoints ?? 0) > 1) {
    // iPadOS 13+ Safari asks for desktop sites with a Mac User-Agent; Safari's version tracks iPadOS's.
    os = "iPadOS"; device_type = "tablet"; model = "iPad";
    os_version = majorMinor(/Version\/(\d+\.\d+)/.exec(ua)?.[1]);
  } else if (/Android/.test(ua)) {
    os = "Android";
    device_type = /Mobile/.test(ua) ? "mobile" : "tablet";
    os_version = majorMinor(/Android (\d+(?:\.\d+)?)/.exec(ua)?.[1]);
    reduced = /Android [\d.]+; K\)/.test(ua);
    if (reduced) os_version = undefined; // "Android 10; K" is the same for every device
    model = androidModel(ua);
  } else if ((m = /Windows NT (\d+\.\d+)/.exec(ua))) {
    os = "Windows"; device_type = "desktop"; os_version = WINDOWS_NT[m[1]];
  } else if (/CrOS/.test(ua)) {
    os = "Chrome OS"; device_type = "desktop";
  } else if ((m = /Mac OS X (\d+[_.]\d+)/.exec(ua)) || /Macintosh/.test(ua)) {
    os = "macOS"; device_type = "desktop"; model = "Mac";
    os_version = majorMinor(m?.[1]);
  } else if (/Linux|X11/.test(ua)) {
    os = "Linux"; device_type = "desktop";
  } else if (ua) {
    device_type = /Mobi/.test(ua) ? "mobile" : "desktop";
  }

  // Client Hints, where the browser has them, are more accurate than the (frozen) User-Agent.
  if (uaData) {
    const p = uaData.platform;
    const pv = uaData.platformVersion;
    if (p === "Windows") {
      os = "Windows";
      const major = parseInt(pv ?? "", 10);
      if (major >= 13) os_version = "11";
      else if (major >= 1) os_version = "10";
    } else if (p === "macOS") {
      if (os !== "iPadOS") { os = "macOS"; os_version = majorMinor(pv) ?? os_version; }
    } else if (p === "Android") {
      os = "Android";
      if (pv) os_version = majorMinor(pv);
    } else if (p === "Chrome OS" || p === "Chromium OS") {
      os = "Chrome OS";
    } else if (p === "Linux") {
      os = "Linux";
    }
    if (typeof uaData.mobile === "boolean" && os !== "iPadOS") {
      if (uaData.mobile) device_type = "mobile";
      else device_type = os === "Android" ? "tablet" : "desktop";
    }
    if (uaData.model?.trim()) model = uaData.model.trim();
  }

  const browser = browserFromBrands(uaData?.brands) ?? browserFromUA(ua);
  const out: DeviceInfo = { platform: "web" };
  const set = (k: Exclude<keyof DeviceInfo, "platform" | "device_type">, v: string | undefined) => {
    const c = clip(v);
    if (c) out[k] = c;
  };
  if (device_type) out.device_type = device_type;
  set("os", os);
  set("os_version", os_version);
  set("browser", browser);
  set("device_brand", brandFromModel(model));
  set("device_model", model);
  return out;
}
