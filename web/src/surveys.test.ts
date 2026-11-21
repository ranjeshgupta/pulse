import { describe, expect, it } from "vitest";
import { newQuestion, npsSnippet, npsTone, surveyProblem } from "./surveys";

describe("surveys", () => {
  it("makes questions with free ids", () => {
    expect(newQuestion("nps", []).id).toBe("nps");
    expect(newQuestion("text", ["q1", "q2"]).id).toBe("q3");
    expect(newQuestion("choice", []).options).toHaveLength(2);
  });
  it("checks a survey", () => {
    expect(surveyProblem({ name: "", questions: [] })).toMatch(/Name/);
    expect(surveyProblem({ name: "x", questions: [newQuestion("nps", []), newQuestion("nps", ["nps"])] })).toMatch(/once/);
    expect(surveyProblem({ name: "x", questions: [{ id: "a", type: "choice", text: "?", options: ["one"] }] })).toMatch(/2 to 12/);
    expect(surveyProblem({ name: "x", questions: [newQuestion("nps", []), newQuestion("text", ["nps"])] })).toBeNull();
  });
  it("builds the email row and tones", () => {
    const s = npsSnippet(7);
    expect(s.match(/\{\{survey\.7\}\}\?score=/g)).toHaveLength(11);
    expect(s).toContain("score=10");
    expect([npsTone(45), npsTone(5), npsTone(-12)]).toEqual(["ok", "warn", "bad"]);
  });
});
