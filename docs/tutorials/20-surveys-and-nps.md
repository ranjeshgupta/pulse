# Tutorial 20: Surveys and NPS

In about 15 minutes you will make an NPS survey ("How likely are you to recommend us?"), send it in an
email with a row of 0 to 10 buttons, read the score, and follow up with unhappy customers the same day.

**You need:** a role that can edit **Content** to make surveys, and a template and campaign to send it
in ([tutorial 5](05-templates-and-content.md), [tutorial 6](06-triggered-and-automation-campaigns.md)).

---

## 1. Make a survey

1. Go to **Content → Surveys** and click **New survey**.
2. **Name (shown as the page title)**: `How did we do?`.
3. **Status**: **Draft** while you work on it. **Taking answers** when it's ready; **Closed** stops it.
   A draft or closed survey's links say it isn't taking answers.
4. **Intro (optional)**: a line above the questions.
5. **Questions**: a new survey starts with an **NPS (0–10)** question (*How likely are you to recommend
   us to a friend or colleague?*) and a **Free text** one (*What's the main reason for your score?*).
   Edit their words in place. **Add:** another question:

   | Type | People answer with |
   |---|---|
   | **NPS (0–10)** | A score from 0 to 10. One per survey |
   | **Rating (1–5 stars)** | One to five stars |
   | **Pick one** | One of your **Options (one per line)** |
   | **Free text** | Their own words |

   Tick **Must be answered** where you need an answer. **↑** and **↓** change the order; **Remove**
   asks first (answers already given stay in the results). A survey has up to 10 questions. Each shows
   its id, e.g. `q1`: its answers are `q_q1` on the contact's event (step 4).
6. **Thank-you message**: `Thanks! Your answer helps us get better.`
7. Set **Status** to **Taking answers** and click **Save**.

There is no branching: everyone sees every question.

## 2. Send it

Open the survey's **Questions and links** tab. **Use it** gives three ways:

| Way | What it is |
|---|---|
| **In any message** | A variable such as `{{survey.3}}`, with **Copy**. Put it as a link in an email, SMS, WhatsApp or push message. Each contact gets their own link, so their answer lands on their profile |
| **NPS row for email** | **Copy HTML**: a row of 0 to 10 buttons to paste into an email's HTML. A click opens the survey with that score already picked |
| **Public link** | One link for anyone (a website, a QR code, social posts). Answers from it aren't tied to a contact |

The NPS row is the easiest for people: one click on the score in the email. The answer is only saved
when they press **Send** on the survey page, so mail scanners that open links never answer for them.
The variable is also in the **{ } Variable** list of the template editor.

If someone answers the same message's survey again, their answer is updated, not counted twice.

## 3. Read the results

Open the survey; it starts on the **Results** tab. Choose **30 days**, **90 days** or **365 days**.

- **NPS**: the score (−100 to 100), and the share of **promoters** (9 and 10), **passives** (7 and 8)
  and **detractors** (0 to 6), with their counts.
- A bar for each score from 0 to 10, coloured by group.
- **By week**: the score each week, with the number of answers.
- Each other question: its answers, the count for each option, the **average** star rating, and the
  latest free-text comments with their score and who wrote them (a link to the contact, or *public
  link*). Roles that can't view contacts don't see who wrote them.

The results refresh every 30 seconds.

## 4. Act on the answers

Every answer is an event, `survey_answered`, on the contact, with the properties `nps` (the score),
`nps_group` (`promoter`, `passive` or `detractor`), `q_<id>` for each other question, and the campaign
the message came from. Use it like any event:

- **Follow up the same day.** Make a triggered campaign that starts on `survey_answered` where
  `nps_group` is `detractor`, and sends a "Sorry, how can we fix it?" email or alerts your team
  ([tutorial 6](06-triggered-and-automation-campaigns.md), [tutorial 12](12-events-and-properties.md#10-use-properties-to-start-a-triggered-campaign)).
- **Ask promoters for a review.** The same, with `promoter`.
- **Build segments**, e.g. *did survey_answered with nps 9 or more in the last 90 days*
  ([tutorial 3](03-segments.md)).

**Delete** (on the survey) deletes its answers too; the `survey_answered` events on contacts stay.

Limits: surveys are links to a hosted page. There is no survey pop-up inside your website or app yet.

## What you have now

- An NPS survey with a follow-up question, taking answers
- An email with one-click 0 to 10 buttons, and a public link
- The NPS score and comments, and a campaign that follows up with detractors

That's the end of the tutorials. For installing, configuring and integrating Pulse, see the
[developer docs](../developer/README.md).
