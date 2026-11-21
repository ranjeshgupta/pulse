// Sign-in and account security helpers: which sign-in step the login page opens on, the messages
// for sign-in errors, checking the change-password form, and when the SSO client secret must be
// typed again.
import { own } from "./safe";

export type LoginStep = "password" | "code" | "setup";

/** loginStep reads ?two_factor= (set by the single sign-on callback when a code or a setup is still
 * needed): "code" or "setup", anything else is the ordinary password step. */
export function loginStep(params: URLSearchParams): LoginStep {
  const v = params.get("two_factor");
  return v === "code" || v === "setup" ? v : "password";
}

export const SSO_ERRORS: Record<string, string> = {
  unknown_workspace: "That workspace doesn't exist. Check the workspace ID.",
  sso_unavailable: "Single sign-on isn't set up for this workspace yet. Sign in with your password, or ask an admin.",
  sso_expired: "The sign-in took too long or was interrupted. Start again.",
  sso_denied: "Sign-in was cancelled at your identity provider.",
  sso_failed: "Your identity provider's response couldn't be verified. Try again, or ask an admin to check the SSO settings.",
  sso_no_verified_email: "Your identity provider didn't share a verified email address.",
  sso_not_provisioned: "You don't have access to this workspace yet. Ask an admin to add you.",
  sso_link_needed: "This email already has a password account. Sign in with your password first, then use Single sign-on once to link it.",
};

/** loginError is the message for ?error= (null without one; a general message for unknown codes). */
export function loginError(params: URLSearchParams): string | null {
  const code = params.get("error");
  if (!code) return null;
  return own(SSO_ERRORS, code) ?? "Sign-in failed.";
}

/** MIN_PASSWORD matches the server's minimum length. */
export const MIN_PASSWORD = 10;

export interface PasswordForm { current: string; next: string; confirm: string }

/** passwordProblem says what stops the change-password form from being sent (null when it's ready). */
export function passwordProblem(f: PasswordForm): string | null {
  if (!f.current) return "Enter your current password.";
  if (!f.next) return "Choose a new password.";
  if (f.next.length < MIN_PASSWORD) return `The new password needs at least ${MIN_PASSWORD} characters.`;
  if (f.next === f.current) return "The new password is the same as the current one.";
  if (f.confirm !== f.next) return "The two new passwords don't match.";
  return null;
}

/** passwordChangeError turns the server's answer to POST /v1/account/password into words. */
export function passwordChangeError(status: number, message: string): string {
  if (status === 403) return "Your current password is wrong.";
  if (status === 429) return "Too many attempts. Try again in an hour.";
  if (status === 400 && message) return message.charAt(0).toUpperCase() + message.slice(1) + (/[.!?]$/.test(message) ? "" : ".");
  return message || "The password couldn't be changed. Try again.";
}

/** sameURL compares settings URLs as the server does: ignoring spaces and a trailing slash. */
export function sameURL(a: string, b: string): boolean {
  const n = (s: string) => s.trim().replace(/\/+$/, "");
  return n(a) === n(b);
}

interface SSOFields { issuer: string; discovery_url: string; client_id: string }

/** ssoSecretNeeded: the stored client secret was given to one identity provider, so the server only
 * keeps it while the issuer, discovery URL and client ID stay the same. When any of them changed
 * (or no secret is stored yet), the secret must be typed again. */
export function ssoSecretNeeded(saved: (SSOFields & { has_client_secret: boolean }) | undefined, draft: SSOFields): boolean {
  if (!saved?.has_client_secret) return true;
  return !sameURL(saved.issuer, draft.issuer) || !sameURL(saved.discovery_url, draft.discovery_url) || saved.client_id !== draft.client_id;
}
