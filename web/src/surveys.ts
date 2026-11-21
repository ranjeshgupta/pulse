// Surveys and NPS (O6): the editor's helpers and the email snippet. Pure for vitest.

export type QuestionType = "nps" | "rating" | "choice" | "text";
export interface SurveyQuestion { id: string; type: QuestionType; text: string; options?: string[]; required?: boolean }
export interface Survey {
  id: number; name: string; status: "draft" | "live" | "closed"; questions: SurveyQuestion[];
  intro?: string; thank_you?: string; public_key?: string; updated_at?: string;
}

export const TYPE_LABEL: Record<QuestionType, string> = { nps: "NPS (0–10)", rating: "Rating (1–5 stars)", choice: "Pick one", text: "Free text" };

const DEFAULT_TEXT: Record<QuestionType, string> = {
  nps: "How likely are you to recommend us to a friend or colleague?",
  rating: "How easy was it?", choice: "Which describes you best?", text: "What's the main reason for your score?",
};

/** newQuestion makes a question with an id not yet used (q1, q2… or nps). */
export function newQuestion(type: QuestionType, taken: string[]): SurveyQuestion {
  let id = type === "nps" ? "nps" : "q1";
  for (let i = 1; taken.includes(id); i++) id = `q${i}`;
  return { id, type, text: DEFAULT_TEXT[type], ...(type === "choice" ? { options: ["Option 1", "Option 2"] } : {}), ...(type === "nps" ? { required: true } : {}) };
}

/** NPS_STARTER: a new survey's questions. */
export const NPS_STARTER: SurveyQuestion[] = [newQuestion("nps", []), { ...newQuestion("text", ["nps"]) }];

/** surveyProblem checks a survey as the server will. */
export function surveyProblem(s: Pick<Survey, "name" | "questions">): string | null {
  if (!s.name.trim()) return "Name the survey.";
  if (!s.questions.length) return "Ask at least one question.";
  if (s.questions.length > 10) return "Ask at most 10 questions.";
  if (s.questions.filter((q) => q.type === "nps").length > 1) return "Ask the NPS question once.";
  const ids = new Set<string>();
  for (const [i, q] of s.questions.entries()) {
    if (!q.text.trim()) return `Question ${i + 1} needs its words.`;
    if (!/^[a-z0-9_]{1,40}$/.test(q.id) || ids.has(q.id)) return `Question ${i + 1} needs its own id (lower-case letters, digits, _).`;
    ids.add(q.id);
    if (q.type === "choice" && ((q.options ?? []).filter((o) => o.trim()).length < 2 || (q.options ?? []).length > 12)) return `Question ${i + 1}: give 2 to 12 options.`;
  }
  return null;
}

/** npsSnippet is an email row of 0–10 links: a click opens the survey with that score picked. */
export function npsSnippet(id: number): string {
  const cell = (n: number) => `<td style="padding:2px"><a href="{{survey.${id}}}?score=${n}" style="display:block;width:32px;padding:8px 0;text-align:center;border:1px solid #d6d9e0;border-radius:6px;color:#1d2029;text-decoration:none;font-weight:600">${n}</a></td>`;
  return `<table role="presentation" cellpadding="0" cellspacing="0" style="border-collapse:collapse"><tr>${Array.from({ length: 11 }, (_, n) => cell(n)).join("")}</tr></table>` +
    `<p style="font-size:12px;color:#5b6070;margin:4px 0 0">0 = not at all likely, 10 = extremely likely</p>`;
}

/** npsTone colours a score: good from 30, fair from 0. */
export function npsTone(score: number): "ok" | "warn" | "bad" {
  return score >= 30 ? "ok" : score >= 0 ? "warn" : "bad";
}
