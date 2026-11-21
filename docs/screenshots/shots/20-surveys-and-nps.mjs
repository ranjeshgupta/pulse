// Screenshots for docs/tutorials/20-surveys-and-nps.md, in the seeded docs workspace.
// Makes the "How did we do?" survey once (found by name on later runs), answers it through its
// public link until it has a spread of scores, then takes the editor, the Use it links, the survey
// page as a contact sees it (score picked from the email row), and the Results tab.
import { BASE, api, clearHighlights, go, highlight, session, settle, shot } from "../lib.mjs";

const AREA = "tutorials";
const NAME = "How did we do?";

// Score and reason pairs: 6 promoters, 3 passives, 3 detractors (NPS 25).
const ANSWERS = [
  [10, "Delivery was quick and the skillet is great."], [9, "Good prices, easy returns."],
  [10, "Love the new lighting range."], [9, ""], [10, "Support sorted my order in minutes."], [9, ""],
  [8, "Fine, but the app is slow at checkout."], [7, ""], [8, "Good, would like more sizes."],
  [4, "My rug arrived two weeks late."], [6, "Packaging was damaged."], [3, "Refund took too long."],
];

export default async function (browser) {
  const { context, page } = await session(browser);

  let sv = (await api(context, "GET", "/v1/surveys")).surveys?.find((s) => s.name === NAME);
  if (!sv) {
    sv = await api(context, "POST", "/v1/surveys", {
      name: NAME, status: "live", intro: "Two quick questions about your last order.",
      thank_you: "Thanks! Your answer helps us get better.",
      questions: [
        { id: "q1", type: "nps", text: "How likely are you to recommend us to a friend or colleague?", required: true },
        { id: "q2", type: "text", text: "What's the main reason for your score?" },
      ],
    }); // answers {"id": …}
  }
  const { survey, links } = await api(context, "GET", `/v1/surveys/${sv.id}`);
  // The public link is on the pages origin; the browser container reaches Pulse at BASE.
  const publicPath = new URL(links.public).pathname;

  const report = await api(context, "GET", `/v1/surveys/${survey.id}/report?days=30`);
  if ((report.responses ?? 0) < ANSWERS.length) {
    for (const [score, why] of ANSWERS) {
      const res = await context.request.post(`${BASE}${publicPath}`, { form: { q_q1: String(score), q_q2: why } });
      if (!res.ok()) throw new Error(`answering the survey: ${res.status()} ${await res.text()}`);
    }
  }

  // 1. The editor: questions.
  await go(page, `/surveys/${survey.id}`);
  await page.getByRole("tab", { name: "Questions and links" }).click();
  await settle(page, { quiet: 600 });
  await page.setViewportSize({ width: 1440, height: 1100 });
  const thanks = await page.getByLabel("Thank-you message").boundingBox(); // the picture ends below it; Use it has its own
  await shot(page, AREA, "20-questions", { clip: { x: 232, y: 0, width: 1440 - 232, height: Math.ceil(thanks.y + thanks.height + 20) } });
  await page.setViewportSize({ width: 1440, height: 900 });

  // 2. Use it: variable, NPS row for email, public link.
  const useIt = page.locator("section, .panel", { has: page.getByText("Use it", { exact: true }) }).last();
  await useIt.scrollIntoViewIfNeeded();
  await highlight(page.getByRole("button", { name: "Copy HTML" }));
  await shot(page, AREA, "20-use-it", { clip: useIt, pad: 8 });
  await clearHighlights(page);

  // 3. The survey page a contact opens from the email row, with 9 already picked.
  const visitor = await browser.newPage({ viewport: { width: 760, height: 900 } });
  await visitor.goto(`${BASE}${publicPath}?score=9`);
  await visitor.waitForLoadState("networkidle");
  await shot(visitor, AREA, "20-survey-page", { clip: visitor.locator(".card"), pad: 16 });
  await visitor.close();

  // 4. Results.
  await page.setViewportSize({ width: 1440, height: 1500 });
  await go(page, `/surveys/${survey.id}`);
  await page.getByRole("tab", { name: "Results" }).click();
  await page.getByText(/promoters/i).first().waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "20-results", { clip: { x: 232, y: 0, width: 1440 - 232, height: 1300 } });
  await context.close();
}
