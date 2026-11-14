# Tutorial 6: Triggered and automation campaigns

In about 20 minutes you will build a welcome series in the step builder: a welcome email, a decision
that waits to see if it was opened, a wait, a condition, and a different path for people who didn't
open it. You will take it live, watch a test contact go through it, open the people behind its
numbers, and pause it.

**You need:** a workspace with an email channel and two email templates (the seeded Acme Retail
workspace has **Welcome email** and **Weekend sale**; tutorial 5 shows how to make templates), and
**Triggered & automation campaigns: Publish** to go live (Admins and Editors have it, see tutorial 1).

---

## 1. Triggered and automation campaigns

Pulse has three types of campaign ([tutorial 14](14-campaigns-hub-and-one-time-campaigns.md)). Two of
them react to each person on their own, as they do something or join a segment:

| Type | Starts | Example |
|---|---|---|
| **Triggered** | For each person when they do something, like buying, signing up, abandoning a cart or submitting a form | A welcome series after `newsletter_signup` |
| **Automation** | For each person when they join a segment, then follows up step by step | A win-back series when someone joins *Inactive 90 days* |

Both are built as **journeys**: a flow of messages, waits and checks that each person moves through
step by step, drawn in the step builder. The third type, **One-time**, is the other way round: one
message to a whole segment at a set time ([tutorial 7](07-one-time-campaigns.md)).

The campaign we build:

| Step | What it does |
|---|---|
| **Start** | A contact does `newsletter_signup` |
| **Send email** | Sends *Welcome email* |
| **Opens email** (decision) | Yes as soon as they open it; No if they haven't within 2 days |
| **Wait** (on Yes) | 1 day |
| **Contact segments** (condition) | Are they in *Repeat buyers in Pune*? Yes: done. No: the next step |
| **Send email** | Sends *Weekend sale* |
| **Modify contact's tags** (on No of the decision) | Tags them `welcome-unopened`, so you can try another channel later |
| **Exit journey** | At the end of every path |

## 2. Create the campaign

1. Click **Campaigns** in the sidebar. The **Triggered** and **Automation** tabs list these
   campaigns with their status, what starts them, how many people are **In progress** and how many
   it was **Sent to** (the numbers update by themselves and open the people behind them).

   ![The Campaigns page, Triggered tab, with the New campaign button highlighted](img/06-campaigns-triggered.png)

2. Click **New campaign**. Pulse asks **What do you want to do?** Choose **React when someone does
   something** (a triggered campaign). **Follow up when someone joins a segment** makes an
   automation instead; you can switch between them later in the Start step.

   ![What do you want to do? with React when someone does something highlighted](img/06-new-goal.png)

3. The step builder opens with three columns:

   ![A new campaign in the step builder: the step palette on the left, the canvas with Start and Exit journey in the middle, the Start settings on the right](img/06-new-campaign.png)

   | Part | What it's for |
   |---|---|
   | **Add a step** (left) | Every step, by kind, with a search box. Click one to add it after the selected step, or drag it onto the canvas |
   | Canvas (middle) | The steps. It starts as **Start → Exit journey** |
   | Inspector (right) | The settings of the selected step. **Start** is selected first |

   Above them, **Campaigns · Triggered** takes you back to the list, and the line under the title
   says what the type means.

4. Click the title **Untitled campaign** and type a name: `Welcome series`.

## 3. Choose what starts it

<a id="3-choose-the-trigger"></a>

With **Start** selected, the inspector asks what starts the campaign for a contact:

<img src="img/06-trigger.png" alt="The Start settings: do something (an event), Event name newsletter_signup, Entering again never enters again, Goal, Schedule" width="300" align="right">

1. **Start this campaign for a contact when they**: **do something (an event)** is already chosen.
   The line under it says *This makes it a Triggered campaign.*
2. **Event name**: `newsletter_signup`, exactly as your website or app sends it (for example
   `Pulse.track("newsletter_signup")`, see tutorial 1). **+ Only when the event's properties
   match…** can narrow it, e.g. only sign-ups from the footer ([tutorial 12](12-events-and-properties.md)).
3. Leave **Entering again** on **never enters again**.

The Start card on the canvas now reads *Contact does ‘newsletter_signup’*.

<br clear="right">

The three ways to start:

| Start this campaign for a contact when they | Type | Use it for | Notes |
|---|---|---|---|
| **join a segment** | Automation | "Became a Pro customer", "Abandoned cart" (tutorial 3) | Only people who newly join from now on start. Tick **Also start it for contacts added when the segment is rebuilt** to include those too; rebuilding a segment can then start it for many contacts at once. **Members it can reach now** shows, per channel, how many members can be reached; click a number to see them |
| **do something (an event)** | Triggered | A sign-up, a purchase, any event your site or app sends | Type the event name exactly |
| **submit a form** | Triggered | A webinar sign-up, a "contact us" form (tutorial 5) | Choose the **Form**, or leave **Any form**. Each submission starts the campaign for the person who sent it, so **Form field value** conditions can check their answers. Inside the steps, the **Submits form** decision waits for a form instead |

<img src="img/06-trigger-segment.png" alt="The Start settings with join a segment, the segment VIP customers, the rebuild box and Members it can reach now per channel" width="300">
<img src="img/06-trigger-form.png" alt="The Start settings with submit a form and the form Newsletter sign-up chosen" width="300">

Changing the start changes the type: the header switches between **Campaigns · Triggered** and
**Campaigns · Automation**, and the campaign moves to that tab.

**Entering again** (*A contact who has been through this journey*) decides what happens when someone
who has already been through it triggers it again: **never enters again**, **can enter again after
finishing**, or **can enter again after some days** (1 to 3650). A contact is never in the same
campaign twice at once.

**Goal** (optional): tick **End the journey early for contacts who reach a goal** and give a **Goal
event**, such as `purchase`. A contact who does it leaves at once and counts as converted in the
report.

## 4. Add the welcome email: "+" → Action → Send email

Every line on the canvas has a **+** in the middle, and every free path has a **+** at its end.

1. Click the **+** between **Start** and **Exit journey**.

   <img src="img/06-plus.png" alt="The + on the line between Start and Exit journey, highlighted" width="380">

2. **Add a step** opens. It says where the step goes (*Between Start and Exit journey*) and asks what
   kind of step:

   ![Add a step: Action, Decision, Condition, Wait and Split, each with a one-line meaning and the number of steps in it](img/06-chooser.png)

   | Kind | Means |
   |---|---|
   | **Action** | Do something: send a message, update the contact, call a webhook |
   | **Decision** | Wait for the contact to do something; Yes if they do within the time limit, No if not |
   | **Condition** | Check something about the contact right now; Yes or No |
   | **Wait** | Pause, for a time or until a date or time of day |
   | **Split** | Send contacts down paths at random: A/B test or percentage split |

   You can also type straight away to search every step ("form", "email", "tag"…).
3. Click **Action**. The list shows every action with what it does. Use ↑ ↓ and Enter, or click.

   ![The list of actions: Send email, Send SMS, Send WhatsApp and more](img/06-chooser-actions.png)

4. Click **Send email**. The step appears as **1 Send email**, selected, and the inspector shows its
   settings.
5. **Email template**: choose **Welcome email**. **Preview** shows it as a contact would get it;
   **Send a test** sends it to one contact (tutorial 4). If the channel has more than one provider,
   **Send through** picks the one this step sends with (the default is preselected; see
   [tutorial 4](04-channels.md#choose-the-provider-for-a-campaign-send-through)). When your
   workspace has topics, **Topic** says which one the step's messages belong to: people who
   unsubscribed from it are skipped, and the unsubscribe link in its emails leaves that topic only
   ([tutorial 4](04-channels.md#the-unsubscribes-panel)). Leave it on **No topic** here.

   <img src="img/06-send-email.png" alt="The Send email step settings: Email template Welcome email, Preview and Send a test" width="300">

Steps are numbered from Start, level by level (Yes before No), so a number can change as you add
steps. The number is how the issues list, the report and **Jump to step** refer to a step.

## 5. Add a decision: did they open it?

1. Click the **+** under **1 Send email**, then **Decision**.

   ![The list of decisions: Opens email, Clicks email, Replies to email, Downloads asset and more](img/06-chooser-decisions.png)

2. Click **Opens email**. Pulse picks the email to watch for you: the nearest **Send email** above
   it.

   ![The canvas with Start, 1 Send email, 2 Opens email with its Yes and No paths, and the decision's settings in the inspector](img/06-decision.png)

The decision has two paths: **Yes** (green) and **No** (red). The step that was after the + (Exit
journey) now hangs on **Yes**; **No** is free, with its own **+**. In the inspector:

| Setting | Here |
|---|---|
| **Which message?** | **Step 1 · Email ‘Welcome email’**. Only messages sent earlier on this path can be watched |
| **Take “Yes” if the message** | **is opened** (for email you can also pick a link click, or a reply) |
| **Take “No” if it hasn't happened within** | `2` **days** (1 minute to 365 days) |

Contacts wait at a decision. They take **Yes** the moment their email is opened, or **No** once the 2
days are up; the No line is labelled **No — after 2 days**.

> Some mail apps hide opens, so **Clicks email** (a link is clicked) is the surer signal when your
> email has a link.

Decisions that need an earlier message are greyed out until there is one: before step 1 existed,
**Opens email** said *"Opens email: add a Send email step above it first"*.

## 6. Add a wait on the Yes path

1. Click the **+** on the green **Yes** line, then **Wait**, then **Wait**.
2. The default is **Wait for** `1` **days**. Keep it. (Units: minutes, hours, days, weeks; up to 365
   days.)

   <img src="img/06-wait.png" alt="The Wait step settings: Wait for 1 days" width="300">

The other wait, **Wait until**, continues at a date and time, or at the next time of day (say
09:00) in a time zone you choose; a contact with a `timezone` attribute gets their own local time.

## 7. Add a condition

A condition checks the contact the moment they reach it and sends them down **Yes** or **No** straight
away.

1. Click the **+** under **3 Wait**, then **Condition**, then **Contact segments**.
2. **Is the contact in this segment?**: choose **Repeat buyers in Pune**.

   <img src="img/06-condition.png" alt="The Contact segments condition: Is the contact in this segment? Repeat buyers in Pune, and the Before it can go live list below" width="320">

Exit journey stays on **Yes** (they already buy from you); **No** is free.

To check a contact field instead (for example *city is Pune*, or *plan is pro*), pick **Contact field
value**: it uses the same filters as the segment builder (tutorial 3), including event counts such as
*did email_opened at least 1 time in 3 days*. **Contact tags**, **Contact stages**, **Contact points**,
**Contact owner** and **Contact device** are quicker versions of the same thing.

## 8. Close every path: the issues list

Under the inspector, **Before it can go live** lists what is still missing:

![Before it can go live: Step 2 · Opens email: connect the "No" path. Step 4 · Contact segments: connect the "No" path.](img/06-issues.png)

Click an item to jump to that step. The campaign can be saved as a draft at any time, but it can't go
live until the list is empty. Close the two open paths:

1. On the red **No** under **4 Contact segments**, click **+** → **Action** → **Send email**, and
   choose **Weekend sale**. Then click the **+** under it → **Action** → **Exit journey**.
2. On the red **No — after 2 days** under **2 Opens email**, click **+** → **Action**, type `tags`
   and pick **Modify contact's tags**. In **Add tags** type `welcome-unopened`. Then **+** under it →
   **Action** → **Exit journey**.

The list disappears when nothing is missing. Other things it checks: a send step without a template,
a decision without a time limit, a condition without a segment, a trigger without its segment or
event, and an end date before the start.

A campaign created through the API may end a path with a step that has no next step (for example
**Delete contact**); the editor shows it as the end of that path.

## 9. Tidy up and find your way around the canvas

![The canvas toolbar: Undo, Redo, Tidy up, Zoom to fit, Copy, Paste](img/06-toolbar.png)

| Tool | Does |
|---|---|
| **Undo** / **Redo** | Undo or redo the last change (Ctrl+Z, and Ctrl+Shift+Z or Ctrl+Y; ⌘ on a Mac) |
| **Tidy up** | Lays the steps out again in neat rows and columns |
| **Zoom to fit** | Fits all the steps in the canvas |
| **Copy** / **Paste** | Copy the selected steps and paste them after the selected step, here or in another campaign (Ctrl+C, Ctrl+V), see step 16 |
| **+** and **–** (bottom left) | Zoom in and out. You can also scroll to zoom and drag the background to move around |
| Minimap (bottom right) | All the steps in small; drag in it to move the view. Colours: Start yellow, actions blue, decisions red, conditions purple, waits grey, splits teal |
| **Show stats** (once saved) | The numbers on each step, see step 13 |

Click **Tidy up**, then **Zoom to fit**:

![The finished Welcome series after Tidy up: nine steps, Yes paths green, No paths red](img/06-canvas.png)

More ways to edit:

- **Move** a step by dragging it. **Connect** two steps by dragging from the dot at the bottom of one
  onto the other. Select a line and press Backspace to remove it.
- **Remove** a step: select it and click **Remove step** in the inspector (or press Backspace or
  Delete). The step before it is joined to the step after it. On a step with Yes/No paths, **Remove
  with the steps after it** also removes everything below it.
- **Palette** (left): click a step to add it after the selected step (it says where: *Click adds it:
  between Start and Send email (step 1)*), or drag it onto the canvas and connect it yourself.

## 10. Schedule

Click **Start** and scroll to **Schedule**:

<img src="img/06-schedule.png" alt="Schedule: Contacts can enter from (required), Ends (optional)" width="340">

- **Contacts can enter from (required)**: when the campaign starts taking contacts. It is filled in
  with now.
- **Ends (optional)**: leave it empty to keep the campaign running until you stop it. With an end,
  nobody new starts after it; **contacts already inside keep going after the end**.

Times are in your browser's time zone. The line at the top of the Start settings says what you
chose, e.g. *From 12 Nov 2026, 12:34 pm, runs until you stop it*.

## 11. Save and go live

1. Click **Save draft**. The campaign gets its own address and a **Draft** status.
2. Click **Save and go live**. (It is greyed out while **Before it can go live** lists anything.)

   ![The campaign header: Draft status, Duplicate, Save draft and the Save and go live button highlighted](img/06-go-live.png)

Pulse says *Campaign is live. People who match start it right away.* The status turns **Live**. While
a campaign is live its steps are locked:

![Live status, Duplicate, the Pause to edit button, and the note "This campaign is live, so its steps are locked"](img/06-live.png)

## 12. Try it with a test contact

Use the SDK test page from tutorial 1 (**Settings → Sign-in & keys → Your website → Open the test
page**), or your own site. In the browser's developer console on that page, sign a test person up:

```js
Pulse.identify("meera-test", { email: "meera.iyer@example.com", first_name: "Meera", city: "Bengaluru" });
Pulse.track("newsletter_signup", { source: "footer" });
```

The SDK sends its requests one after another, so the identify (with the email address) always
reaches Pulse before the sign-up that starts the campaign. Within seconds the welcome email goes out. On the local development stack every email lands in
Mailpit (`http://localhost:18025`):

![The welcome email in Mailpit: "Welcome to Acme Retail, Meera", "Hi Meera, Thanks for joining…"](img/06-mailpit.png)

The subject and greeting are personalised from the contact's `first_name`. Opening the email in
Mailpit loads its tracking image, which counts as an open, so the contact takes **Yes** at step 2 and
moves on to the 1-day wait.

## 13. Per-step numbers and the report

Back on the campaign, **Show stats** (in the toolbar) is on. Each step shows:

- **Entered**: how many times contacts reached it
- **Here now**: contacts waiting at it (decisions and waits); **Finished** on an Exit step
- **Yes · No**: how many went each way (decisions and conditions; split steps show each path)
- **Sent · Opened** (or Read, or Delivered, by channel): messages from send steps

![The canvas with stats: Send email Entered 1, Sent 1 · Opened 1; Opens email Yes 1 · No 0; Wait Here now 1](img/06-stats.png)

**Here now**, **Sent** and **Opened** (or Read, Delivered) are links: click one and the Contacts page
opens with exactly those people, e.g. everyone waiting at the 1-day wait right now. **Entered**,
**Yes · No** and split paths are plain numbers: Pulse counts how many times contacts passed a step,
but doesn't keep who, so there is no list behind them (point at one to read this).

The **Report** tab has the totals:

![The report: 1 entered, 1 in progress, 0 completed; the Funnel from Enrolled to Clicked; Why messages didn't go out, by step; Messages by step; By channel; Top links and By day](img/06-report.png)

| Part | Shows |
|---|---|
| Figures | **entered**, **in progress**, **completed**, and **reached the goal** / **left early** when there are any |
| Revenue | When contacts buy after a message: revenue, orders and buyers |
| Split steps | Contacts per path, the rate it is judged by, and the winner |
| **Funnel** | Unique contacts at each stage: **Enrolled**, **Targeted** (a message was tried: sent, skipped or failed), **Sent**, **Delivered**, **Opened** or **Read**, **Clicked**, with bounces, complaints, failures, unsubscribes and the skipped groups beside it |
| **Why messages didn't go out, by step** | Each reason (*Hard bounce*, *Opted out*, *No address on this channel*…) per send step, with the number of contacts |
| **Messages by step** | Sent, delivered, opened, clicked, failed… per send step. Click a row to jump to the step |
| **By channel** | The same per channel |
| **Top links**, **By day** | Most-clicked links; sends and opens per day |

Each contact counts once, and rates are out of messages sent. **Delivered** shows "—" when the
provider doesn't report delivery (plain SMTP, as in local development, and Gmail); sending services
with delivery webhooks fill it in ([tutorial 4](04-channels.md)).

Every number in the report is a link to its people: **1 in progress** opens the contacts still going
through the steps, a reason's count opens the contacts it happened to at that step, a day in **By day**
opens who got a message that day. The report counts *entries* where the list counts *people*, so a
person who went through twice is once in the list; the list says so when it matters.

## 14. Pause it

1. Click **Pause to edit** (top right).

   ![The live campaign with the Pause to edit button highlighted](img/06-pause.png)

2. Pulse says *Campaign paused*; the status turns **Paused** and the steps unlock: change what you
   need, then **Save and go live** again.

While a campaign is paused, nobody new starts it, and contacts already inside wait where they are. A
wait or decision that comes due while it is paused moves on when the campaign goes live again.

Leave this one paused.

## 15. Delete or archive a campaign

On the Campaigns page, each triggered or automation campaign that isn't live has a button at the end
of its row:

- **Delete**, for one nobody has entered yet (a draft you no longer need). It asks first and can't be
  undone.
- **Archive**, for one that has had contacts in it: those can't be deleted, because their history and
  reports are kept. An archived campaign disappears from the list and can't go live. Choose
  **Archived** in the status list to see archived campaigns, and click **Restore** (in the list or on
  the campaign's page) to bring one back as **Paused**.

![The Campaigns page, Triggered tab: the status list, Archive on the paused Welcome series and Delete on the draft Win back (draft) highlighted; the live Cart rescue has no button](img/06-archive.png)

Pause a live campaign before archiving it. Deleting needs **Triggered & automation campaigns: Edit**;
archiving and restoring need **Triggered & automation campaigns: Publish**, like going live. Through
the API: `DELETE /v1/journeys/{id}`, `POST /v1/journeys/{id}/archive` and
`POST /v1/journeys/{id}/unarchive`.

## 16. Send deadlines, copy and paste, duplicate, tags

**Send deadline.** Every send step (and **Send marketing message**) has a box **Send deadline: drop
messages that would go out late**. A message can be held back after its step runs: quiet hours, a
provider's daily quota (tutorial 4), retries. With a deadline, Pulse drops it instead of sending it
late: **Don't send after** a time of day (the first time it comes after the step runs, in the zone
named under it), **...or more than** a number of hours after the step runs (1 to 168). Dropped
messages are recorded as failed with the reason *Send deadline passed*, and the report counts them.

<img src="img/06-deadline.png" alt="The Send email step's deadline: Don't send after 21:00, or more than 12 hours late" width="420">

**Copy and paste steps.** Select a step (or several: Ctrl-click, or ⌘-click on a Mac, or Shift-drag a
box round them) and click **Copy** in the canvas toolbar (or press Ctrl+C). **Paste** (Ctrl+V) puts
the copied steps after the selected step, in this campaign or any other, even in another browser tab:

![The canvas toolbar with Copy and Paste highlighted](img/06-copy-paste.png)

![Paste steps: where they go, what is left to choose again, and the copied steps](img/06-paste.png)

- The steps keep their settings and the links between them; they get new names (`email2`), and a
  decision watching a copied email watches the copy.
- The first copied step goes where the selected step's path continued, and what came there before
  follows the copied steps' main path (their first outputs: Next, Yes, path A).
- Links to steps that weren't copied are left free (the issues list asks you to connect them), as are
  jumps to them. Templates, segments, forms and other things that don't exist in this workspace (when
  pasting into another workspace) are cleared, and a note says which steps to fix.
- If your browser doesn't let Pulse read the clipboard, the box opens empty: paste into it.

![The canvas with the pasted Send email step after the first one](img/06-pasted.png)

Nothing is saved until you click **Save draft**: **Undo** takes a paste back.

**Duplicate** (on the campaign's page, or the row's **Duplicate** on the Campaigns page) makes a
draft copy, "… (copy)", with the same steps, schedule and tags, and nobody in it. **Tags** under the
campaign's name label it on the Campaigns page (tutorial 14); they can change while it is live.

## Every step, at a glance

Each one is in the **+** chooser and the palette, under its kind. Steps marked *needs Contacts: Edit*
can only be added, saved or taken live by someone whose role has **Contacts: Edit** (they change
consent or CRM data); the chooser greys them out with *Needs edit access to contacts*.

**Decisions** wait for the contact (Yes when it happens; No at the time limit, 1 minute to 365 days):

| Step | Yes when the contact… |
|---|---|
| **Opens email** | Opens an email sent earlier on this path (needs a Send email above it) |
| **Clicks email** | Clicks any link, or a link containing some text, in an earlier email |
| **Replies to email** | Replies to an earlier email (needs a reply tracking address under Settings → Channels → Delivery rules) |
| **Downloads asset** | Downloads a file from Assets (one, or any) |
| **Submits form** | Submits one of your forms (one, or any) |
| **Visits a page** | Visits a page whose URL contains some text, or one of your landing pages (needs the web SDK) |
| **Device visit** | Visits from a device type (desktop, mobile, tablet) or operating system |
| **Requests dynamic content** | Loads a page with a dynamic web content slot (put **Push dynamic content** on Yes) |
| **Message outcome** | An earlier SMS, WhatsApp, RCS, push or inbox message is delivered or read, or an RCS message gets a reply |
| **Does an event** | Does an event, e.g. `purchase` (time limit from 1 second) |

**Conditions** check right now (Yes or No):

| Step | Yes if the contact… |
|---|---|
| **Contact field value** | Matches any contact field or behaviour rule, e.g. city is Pune |
| **Contact segments** | Is in a segment (dynamic or static) |
| **Contact tags** | Has any (or all) of some tags |
| **Contact stages** | Is in one of some lifecycle stages |
| **Contact points** | Has at least, more than, at most, less than, exactly or not a number of points |
| **Contact owner** | Is owned by some teammates, by anyone, or by no one |
| **Contact device** | Was last seen on a device type or OS |
| **Contact campaigns** | Is in, has finished, or has ever entered other triggered or automation campaigns (the step calls them journeys) |
| **Form field value** | Gave an answer on the form submission that brought them here (or their latest) |
| **Has active notification** | Can get push, or has an unread app inbox message |
| **Has valid email address** | Has a well-formed address on a domain that takes mail (optionally rejecting disposable addresses) |
| **Marked as Do Not Contact** | Unsubscribed from, or bounced on, a channel (or any) |
| **Prevent action after date** | Yes until a date, No after it: put the time-limited action on Yes |
| **Visited page** | Has visited a page (ever, or in the last N days) |

**Actions** do something, then continue:

| Step | Does |
|---|---|
| **Send email**, **Send SMS**, **Send push**, **Send to app inbox** | Send a template on that channel |
| **Send WhatsApp**, **Send RCS** | Send a template, with an optional SMS if it can't be delivered |
| **Send email to user** | Email a teammate, the contact's owner or other addresses about this contact (not a marketing email) |
| **Send marketing message** | Choose the channel by rules, e.g. an Indian number gets SMS, otherwise WhatsApp ([tutorial 13](13-personalising-messages.md#8-choose-the-channel-by-rule)); without conditions, send on the first channel in your order that the contact can receive and has agreed to. Like the send steps, it takes a **Topic**: someone who left the topic is skipped on every channel |
| **Send a webhook** | POST the contact as JSON to a public https:// address, or your own JSON body with variables (`{{contact.x \| json}}`) |
| **Show focus item** | Show a pop-up, bar or slide-in on the contact's next page view ([tutorial 8](08-website-and-app-messages.md)) |
| **Push dynamic content** | Set this contact's content for a dynamic web content slot, for some days |
| **Update contact** | Set contact fields (values can use variables such as `{{contact.city}}` or the trigger event's `{{event.product_name}}`, or dates like `now+3days`) |
| **Update contact owner** | Assign the contact to a teammate, or unassign (needs Contacts: Edit) |
| **Update company** | Set fields on the contact's company (needs Contacts: Edit) |
| **Add to company**, **Update contact's primary company** | Link the contact to a company; a contact has one (needs Contacts: Edit) |
| **Add to company's score** | Add points to the company (needs Contacts: Edit) |
| **Adjust contact points** | Add or take away points |
| **Change contact's stage** | Move the contact to a lifecycle stage |
| **Modify contact's tags** | Add or remove tags |
| **Modify contact's segments** | Add to or remove from static segments |
| **Change campaigns** | Add the contact to other triggered or automation campaigns, or take them out |
| **Jump to step** | Continue from another step (ends the path; at most 25 jumps per contact) |
| **Add Do Not Contact** | Stop a channel, or everything, for this contact |
| **Remove Do Not Contact** | Allow a channel again (needs Contacts: Edit) |
| **Delete contact** | Permanently delete the contact; you type DELETE to confirm (ends the path; needs Contacts: Edit) |
| **Push contact to integration** | Send the contact to HubSpot or Salesforce (tutorial 9) |
| **Exit journey** | End the campaign for this contact here |

**Waits**:

| Step | Does |
|---|---|
| **Wait** | Wait a set time, e.g. 2 days |
| **Wait until** | Wait for a date and time, or the next time of day in the contact's time zone |

**Splits** send each contact down one path at random (always the same one for a contact):

| Step | Does |
|---|---|
| **A/B split** | 2 to 5 paths by share; optionally picks a winner automatically (open, click or goal conversion rate, after a test period and a minimum of contacts per path) and sends everyone after down it |
| **Percentage split** | 2 to 5 paths by share, e.g. 70% and 30% |

> Also good to know: contacts don't leave a campaign when they leave the segment that started it (use a
> condition, a goal or **Change campaigns**), and a decision can't watch a **Send marketing
> message** step, because its channel is chosen per contact.

## What you have now

- A **Welcome series** triggered campaign: a welcome email, an open decision, a wait, a segment
  condition and a follow-up, with every path closed
- A test contact who went through it, the email in your test inbox, and numbers that open the people
  behind them
- The campaign paused, ready to edit

**Next:** [Tutorial 7](07-one-time-campaigns.md), One-time campaigns: send one message to a whole
segment, once or on a repeat, with an A/B test.
