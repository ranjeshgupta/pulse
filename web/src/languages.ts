// Multi-language templates (O6): the languages offered, and how a contact's locale picks one —
// the web side of internal/channel/locale.go. Pure for vitest.
import type { Translation } from "./api";

export const LANGUAGES: { code: string; name: string }[] = [
  { code: "hi", name: "Hindi" }, { code: "bn", name: "Bengali" }, { code: "ta", name: "Tamil" }, { code: "te", name: "Telugu" },
  { code: "mr", name: "Marathi" }, { code: "gu", name: "Gujarati" }, { code: "kn", name: "Kannada" }, { code: "ml", name: "Malayalam" },
  { code: "pa", name: "Punjabi" }, { code: "or", name: "Odia" }, { code: "ur", name: "Urdu" }, { code: "as", name: "Assamese" },
  { code: "en", name: "English" }, { code: "ar", name: "Arabic" }, { code: "es", name: "Spanish" }, { code: "fr", name: "French" },
  { code: "de", name: "German" }, { code: "pt", name: "Portuguese" }, { code: "id", name: "Indonesian" }, { code: "ja", name: "Japanese" },
  { code: "zh", name: "Chinese" },
];

/** languageName: "hi-IN" → "Hindi (hi-IN)", unknown codes as they are. */
export function languageName(locale: string): string {
  const base = locale.toLowerCase().split(/[-_]/)[0];
  const l = LANGUAGES.find((x) => x.code === base);
  return l ? (locale.toLowerCase() === base ? l.name : `${l.name} (${locale})`) : locale;
}

export const validLocale = (l: string) => /^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$/.test(l);

/** pickTranslation mirrors the server: the exact locale, else the same language. */
export function pickTranslation(trs: Translation[] | undefined, locale: string): Translation | undefined {
  if (!locale || !trs?.length) return undefined;
  const norm = locale.toLowerCase().replace(/_/g, "-");
  return trs.find((t) => t.locale.toLowerCase() === norm) ?? trs.find((t) => t.locale.toLowerCase().split("-")[0] === norm.split("-")[0]);
}
