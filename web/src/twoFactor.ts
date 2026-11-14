// Two-step sign-in helpers: reading codes people type, the recovery codes file, and how the
// workspace policy reads in plain words.
import qrcode from "qrcode-generator";
import type { TwoFactorPolicy } from "./api";

/** cleanCode drops the spaces and dashes people type or paste into a code. */
export function cleanCode(code: string): string {
  return code.replace(/[\s-]/g, "").toLowerCase();
}

/** isAppCode: six digits from the authenticator app (anything else is tried as a recovery code). */
export function isAppCode(code: string): boolean {
  return /^\d{6}$/.test(cleanCode(code));
}

/** codeReady: enough typed to send, an app code or a recovery code (10 characters). */
export function codeReady(code: string): boolean {
  const c = cleanCode(code);
  return isAppCode(c) || c.length === 10;
}

/** recoveryText is the file people download or copy: the codes, with what they're for. */
export function recoveryText(codes: string[], workspace: string, email: string): string {
  return [
    `Pulse recovery codes for ${email} (workspace ${workspace})`,
    "",
    "Each code works once, instead of a code from your authenticator app.",
    "Keep them somewhere safe, like a password manager.",
    "",
    ...codes,
    "",
  ].join("\n");
}

/** policySummary says in words who must use two-step sign-in. */
export function policySummary(p: TwoFactorPolicy, roleName: (r: string) => string, userName: (id: number) => string): string {
  if (p.mode === "everyone") return "Required for everyone who signs in with a password.";
  if (p.mode === "optional") return "Optional: each person decides.";
  const who = [...p.roles.map(roleName), ...p.users.map(userName)];
  if (who.length === 0) return "Required for nobody yet: choose roles or people.";
  return `Required for ${listWords(who)}.`;
}

function listWords(xs: string[]): string {
  if (xs.length <= 2) return xs.join(" and ");
  return `${xs.slice(0, -1).join(", ")} and ${xs[xs.length - 1]}`;
}

/** qrMatrix turns text into QR code modules (true = dark), for drawing as SVG. */
export function qrMatrix(text: string): boolean[][] {
  const qr = qrcode(0, "M");
  qr.addData(text);
  qr.make();
  const n = qr.getModuleCount();
  return Array.from({ length: n }, (_, r) => Array.from({ length: n }, (_, c) => qr.isDark(r, c)));
}
