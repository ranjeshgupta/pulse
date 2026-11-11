# Tutorial 2: Contacts

In about 20 minutes you will find your way around the contact list and a contact's profile, define
the fields your team uses, import a CSV of customers into a static segment, filter the list and save
a view as a segment, export it, merge duplicates, and link contacts to companies.

**You need:** a workspace with some contacts (tutorial 1) and access to edit contacts (the **Admin**
or **Editor** role). For the import, use the sample file
[`files/sample-contacts.csv`](files/sample-contacts.csv): eight customers from a store opening in Pune.

---

## 1. The contact list

Go to **Contacts** (under *Audience*). Everyone Pulse has seen an event or a profile for is here,
newest first, 50 at a time (**Load more** at the bottom fetches the next 50).

![The Contacts page: the search box, Columns and Export CSV, the list, and the Import button highlighted](img/02-contacts-list.png)

| Part | What it does |
|---|---|
| Search box | Filters and quick find (step 7) |
| **Duplicates** | Contacts that share an email or phone number (step 10) |
| **Import** | Import a CSV (step 5). Only for roles that can edit contacts |
| **Columns** | Choose the columns of the list (step 9) |
| **Export CSV** | Download the list as it is filtered now (step 9) |

Click any row to open that contact's profile.

## 2. A contact's profile

The profile opens as a panel on the right. From top to bottom:

<img src="img/02-profile-top.png" alt="The profile of store-1001: Merge into…, its segments, Owner, stage and company, and Tags and points with 25 points" width="420" align="right">

- **Merge into…**: fold this contact into another one (step 10).
- **Segments**: every segment the contact is in right now, dynamic and static.
- **Owner, stage and company**: three drop-downs (step 3).
- **Tags and points**: the contact's tags and score (step 4).
- **Messages they agreed to**: consent per channel (step 4).
- **Attributes**: every profile field, such as `city`, `plan` or `first_seen`.
- **Activity**: the contact's events, newest first. Every change you make in the profile is
  recorded here too (`tag_added`, `points_changed`, `stage_changed`, `owner_changed`, …).

<br clear="right">

The panel refreshes every few seconds, so a new event or segment shows up while you look. Click
**Close** (or outside the panel) to go back to the list.

![The Attributes and Activity sections of the profile](img/02-profile-attributes.png)

## 3. Owner, stage and company

Under **Owner, stage and company**, each drop-down saves as soon as you pick a value, and a message
at the bottom of the screen confirms it.

| Drop-down | Choices | Notes |
|---|---|---|
| **Owner** | **No owner** or a teammate | The teammate responsible for this contact. Filter with `owner:me` (step 7). People who can't view settings only see the current owner |
| **Stage** | Your lifecycle stages (Settings → Stages, see tutorial 3) | Shows *since …* when it last changed. If no stages exist it says **No stages defined** |
| **Company** | **No company** or one of your companies | **Open** goes to the company. The company's fields appear below as `company_…` (step 11) |

## 4. Tags, points and consent

**Tags** are short labels such as `vip` or `gift-cards`.

1. Type a tag in **Add a tag** (letters, digits, `-` and `_`, up to 64 characters; the box suggests
   tags already in use) and click **Add tag**.
2. To remove a tag, click **×** on it.

**Points** are the contact's score. Scoring rules add points automatically when contacts do certain
events (tutorial 3); here you change them by hand:

1. Type a whole number in the box next to the points, e.g. `+10` or `-5` (`25` works too).
2. Click **Adjust**. The number above changes within a second or two.

**Messages they agreed to** lists every channel (Email, SMS, WhatsApp, RCS, Push, App inbox) as
**Subscribed** or **Unsubscribed**. **Unsubscribe** and **Subscribe** change one channel;
**Unsubscribe from everything** (it asks you to confirm) stops all messages on every channel.
Every journey and broadcast respects these settings.

<img src="img/02-profile-consent.png" alt="Messages they agreed to: every channel Subscribed, with Unsubscribe buttons and Unsubscribe from everything" width="420">

> Subscribing a contact to one channel again also lifts an "everything" unsubscribe, so that
> channel works.

## 5. Define your contact fields

Pulse keeps any field you send, without setup. Defining a field gives it a label and a type, which
helps in two places: the segment builder offers the right comparisons (numbers get *greater than*,
dates get *in the last … days*), and imports check and convert the values.

1. Go to **Settings → Contact fields** (under *Contact data*). Defined fields are listed first;
   below, **Seen on contacts, not defined yet** shows fields your contacts already have. Click one
   of those to define it.

   ![Settings → Contact fields with two defined fields and the fields seen on contacts, and the Define field button highlighted](img/02-contact-fields.png)
2. Click **Define field** and fill in:
   - **Attribute key**: as it appears in events and files, e.g. `lifetime_value` (letters, digits
     and `_`, starting with a letter or `_`). It can't be changed later.
   - **Label**: e.g. `Lifetime value`
   - **Type**: **Text**, **Number**, **Yes / no** or **Date**
   - **Description** (optional), and **Holds personal data** for phone numbers, addresses, birthdays
3. Click **Save attribute**.

   <img src="img/02-define-field.png" alt="The Define attribute form for lifetime_value, a Number" width="420">

For the sample file, define two fields:

| Attribute key | Label | Type |
|---|---|---|
| `lifetime_value` | Lifetime value | Number |
| `loyalty_member` | Loyalty member | Yes / no |

**Remove** deletes only the definition; the values on contacts stay.

## 6. Import a CSV

Go to **Contacts → Import**. The import has four steps, shown at the top: **Upload**, **Map
columns**, **Static segment**, **Review**.

**What the file needs:** a first row that names the columns, and in every row an external ID or an
email (the email then becomes the ID). Files can be up to 10 GB. Existing contacts are updated, and
empty cells leave the values a contact already has.

### Upload

1. Drop the CSV file on the box, or click **Choose File** and pick `sample-contacts.csv`. Its name
   and size appear.
2. Click **Next**.

   ![Step 1, Upload, with sample-contacts.csv chosen and the Next button highlighted](img/02-import-upload.png)

### Map columns

Each column of your file is a row here, with a few example values. Under **Import as**, Pulse has
already guessed:

| Your column | Import as | Why |
|---|---|---|
| `customer_id` | **External ID** | `id`, `user_id`, `customer_id` and `contact_id` are recognised |
| `email` | **Email** | also `e_mail`, `email_address`, `mail` |
| `mobile` | **Phone (international format)** | also `phone_number`, `mobile_number`, … Spaces, dashes and brackets are removed, so `+91 98200 11001` becomes `+919820011001` |
| `lifetime_value`, `loyalty_member` | **Lifetime value**, **Loyalty member** | the fields you defined in step 5 |
| `first_name`, `last_name`, `city`, `plan` | **New field: …** | fields without a definition; created as text, marked *new field* |
| `tags` | **Tags (separated by ;)** | `store-pune;diwali-2026` gives two tags |

3. Set **notes** to **Don't import**: those are the store's own remarks.
4. Click **Next**. If something is wrong, Pulse lists it in red under the table and **Next** stays
   off: no column mapped to External ID or Email, two columns mapped to the same field, or a field
   name with characters other than letters, digits and `_`.

   ![Step 2, Map columns, for the sample file, with notes set to Don't import](img/02-import-map.png)

### Static segment

A static segment holds exactly the contacts you put in it; it is handy for "everyone in this file".

5. Choose one of:
   - **Don't add them to a segment**
   - **Add them to a static segment**, then pick it from the list
   - **Create a new static segment** and type a name, here `Pune store opening guests`
6. Click **Next**.

   ![Step 3, Static segment, creating Pune store opening guests](img/02-import-segment.png)

### Review and start

7. Check the file, the columns that will be imported (and how many are skipped) and the segment.
8. Tick **These people agreed to hear from us.** Only import people who did. If your file has
   unsubscribe columns such as `optout_email` (with `true`, `yes` or `1`), Pulse respects them.
9. Click **Start import**.

   ![Step 4, Review, with the consent box ticked and Start import highlighted](img/02-import-review.png)

While the file uploads you see a progress bar and **Cancel upload**: keep the page open until it
finishes. After that the import runs on the server in the background, and you can leave the page.

### Import history

The import appears under **Import history** with its status: **Uploading**, **Waiting**,
**Importing**, then **Done** (or **Failed** or **Cancelled**), how long it took, and how many rows
were **Imported** and **Skipped**. **Cancel** stops a running import; rows already imported stay.

The sample file gives **6 imported, 2 skipped**. Click **Show errors** to see why:

![Import history: sample-contacts.csv Done, 6 imported and 2 skipped, with the skipped rows shown](img/02-import-history.png)

- **Row 6: lifetime_value: "unknown" is not a number.** Because `lifetime_value` is defined as a
  Number, Pulse checks it and skips the row rather than store bad data. Fix the cell and import the
  file again: rows already imported are just updated.
- **Row 9: no external_id or email.** A row needs one of them.

Row numbers count the heading row as row 1, like a spreadsheet. The list shows up to 50 skipped rows.

Go back to **Contacts**: the new contacts (`store-1001` to `store-1007`, without Farah) are at the
top, and each is in the segment **Pune store opening guests**.

> **A `tags` column replaces tags.** Importing a contact with a `tags` cell sets its tags to exactly
> that list, so a tag you added by hand is removed if the file doesn't have it. Leave the cell empty
> (or don't import the column) to keep the tags a contact has.

## 7. Filter your contacts

Type in the search box. Words like `key:value` become **filters**, shown as chips; anything else is a
quick search. As you type, suggestions appear: type `is:` and pick one with the arrow keys and
**Enter**, or click it.

![The search box with is: typed and its five suggestions](img/02-filter-suggest.png)

| Filter | Shows contacts who |
|---|---|
| `is:anonymous` | have no known ID or email yet (website visitors) |
| `is:identified` | are known: an ID, email or phone |
| `is:unsubscribed` | unsubscribed from any channel, or from everything |
| `is:bounced` | had an email or SMS bounce |
| `is:dnc` | are "Do Not Contact": unsubscribed or bounced |
| `segment:"Pune store opening guests"` | are in that segment, dynamic or static (by name or ID) |
| `tag:vip` | have the tag |
| `stage:Lead` | are in the stage |
| `company:"Northwind Traders"` | work at the company (by name or ID) |
| `owner:me`, `owner:none`, `owner:"Priya Shah"` | are owned by you, by nobody, or by a teammate (name, email or ID; naming a teammate needs access to view settings) |
| `points:>50` (also `>=`, `<`, `<=`, `=`) | have more than 50 points. `points:<10` includes contacts with no points |
| `email:@example.com` | have an email that contains the text |
| `name:kavya` | have a name, first name or last name that contains the text |

How filters work:

- Several filters must all match. `tag:vip points:>50` is VIPs with more than 50 points.
- Put a value with spaces in double quotes: `segment:"VIP customers"`.
- **Enter** (or a space after a finished filter) makes the chip. **×** on a chip removes it,
  **Backspace** in an empty box removes the last one, **Clear** removes everything.
- Above the list you see how many contacts match, e.g. **26 contacts** (above 10,000 it says
  *About …*).
- Other text, e.g. `kavya` or `98200`, is a quick search on the external ID, email, name and
  phone, in any case.
- The address bar keeps the filters (`/contacts?q=…`), so you can bookmark a view or send the link
  to a teammate.

![The filters Tag: vip and Points > 50: 26 contacts, with Save as segment highlighted](img/02-filter-chips.png)

> `email:` and `name:` look for the text as you typed it, in lower case, and with a capital first
> letter (`kavya`, `Kavya`), not every mix of cases.

## 8. Save a view as a segment

When a filtered view is a group you will use again, keep it as a segment:

1. With the filters in place, click **Save as segment**.
2. Check the name (Pulse suggests one from the filters) and click **Create segment**.

   <img src="img/02-save-segment.png" alt="The Save as segment window, named VIPs with over 50 points, listing Tag: vip and Points > 50" width="420">

You land on the new dynamic segment. Contacts join and leave it on their own from now on, as they
start or stop matching. See tutorial 3 for what you can do there.

**Save as segment** is only there for roles that can edit segments, and it is greyed out while the
search box has quick-search text: use `email:` or `name:` instead.

## 9. Choose columns and export

1. Click **Columns** and tick what the list should show: **Email**, **Name**, **Phone**, **City**,
   **Points**, **Stage**, **Company**, **Owner**, **Tags**, **Last seen**, **Date added**, **Last
   updated**, plus every field you defined (here **Lifetime value** and **Loyalty member**). External
   ID always shows.
2. Click **Done**. **Reset** goes back to Email, Name, City and Last updated. Your choice is
   remembered in this browser.

<img src="img/02-columns.png" alt="The Columns window with Points, Stage and Tags ticked" width="420">

To download the list, click **Export CSV**. You get the contacts of the current view (all contacts
when there's no filter) as `contacts-view-<date>.csv` (or `contacts-<date>.csv`). The file has
`external_id`, `email`, `created_at` and `updated_at`, then the columns you chose (Name becomes
`name`, `first_name` and `last_name`). Exporting needs contact edit access, like importing.

![Export CSV highlighted next to the filtered view](img/02-export.png)

## 10. Find and merge duplicates

The sample file has Kabir Sethi twice: he signed up again at the till and got a second ID
(`store-1006` and `store-1007`, same email).

1. On **Contacts**, click **Duplicates**. Choose **Same email address** or **Same phone number**.
   Each group shows the contacts that share the value.
2. Under **Keep**, choose the contact to keep, here `store-1006`.
3. Click **Merge into store-1006** and confirm. It can't be undone.

   ![Duplicates: kabir.sethi@example.com with two contacts, store-1006 kept, and Merge into store-1006 highlighted](img/02-duplicates.png)

What a merge does:

- The kept contact's values win; the other contact fills in only what the kept one lacks.
- History, tags, points and static segments move to the kept contact.
- The merged contact leaves every segment and its profile says it was merged into the kept one.
  Events that still arrive for it go to the kept contact.

To merge two contacts that don't share an email or phone (say, an old and a new address), open the
one to fold away, click **Merge into…**, search by ID, email, name or phone, choose the contact to
keep and click **Merge**.

## 11. Companies

For B2B, link contacts to the companies they work for. Go to **Companies** (under *Audience*).

![Companies: three companies with their domains, attributes and number of contacts, and New company highlighted](img/02-companies.png)

1. Click **New company**, enter a **Name** and a **Domain (optional)**, and click **Create company**.
2. On the company's page, click **Add attribute** to add fields such as `industry` = `Wholesale` or
   `size` = `240`, then **Save**. Each one is copied to the company's contacts as
   `company_<name>` (`company_industry`, `company_size`), within seconds, so segments and
   messages can use them. Numbers and true/false keep their type.
3. Link a contact: open its profile and choose the company under **Company** (step 3). Your server
   can do it too: an identify event with `attrs.company` links the contact, and creates the company
   if it's new.

![Northwind Traders: name, domain, attributes industry and size, 9 contacts, and its contacts below](img/02-company.png)

The company page lists its contacts (click one to open it) and counts them on the right. **Delete**
removes the company; its contacts stay, unlinked. In the segment builder, the **Company** group has
**Company** (works at a company), **Company name** and **Other company field** (any `company_…`
field, such as industry).

## What you have now

- Two typed contact fields, `lifetime_value` and `loyalty_member`
- Six customers from the store opening, imported into the static segment **Pune store opening
  guests**, and one of them (`store-1001`) with an owner, a stage, tags and points
- A feel for filters, saved views, exports, duplicates and companies

**Next:** Tutorial 3, Segments: build dynamic segments with the rule builder, manage static ones, and
set up scoring and stages.
