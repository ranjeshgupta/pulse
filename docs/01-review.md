# Review: Mautic 7 and Listmonk 6

Findings from the local installs in `../mautic` and `../listmonk` (inspected November 2026),
plus WebEngage as a feature reference. The goal is to explain *why* Mautic is slow and Listmonk is fast,
so Pulse keeps Mautic's features without its batch architecture.

## Summary

| | Mautic 7.2.2 | Listmonk 6.2.0 | WebEngage (SaaS) |
|---|---|---|---|
| Language / framework | PHP 8.3, Symfony, Doctrine ORM | Go, single binary | Proprietary |
| Database | MySQL (126 tables) | Postgres (16 tables) | — |
| Async work | Symfony Messenger (email, hit, failed queues) + **cron** | In-process goroutine pool | Streaming |
| Contact model | Wide `leads` table, one SQL column per custom field | `subscribers.attribs JSONB` | Users + events |
| Behavioral events | Page hits, email opens/clicks, form submits | Opens/clicks only | Any custom event |
| Segments | Filter builder, rebuilt by cron | Raw SQL over JSONB, evaluated at send time | Real-time, event-based |
| Automation | Campaign builder (decisions, actions, conditions) | None (transactional API only) | Journey designer |
| Channels | Email, SMS, web notifications, focus items, DWC | Email (+ webhook messenger) | Email, SMS, WhatsApp, RCS, push, in-app, web |
| Tenancy | Single tenant | Single tenant | Multi-tenant |

## Mautic: why it is slow

The official image registers this crontab for `www-data` (seen in the `mautic-cron-1` container):

```
0,15,30,45  * * * *  mautic:segments:update
5,20,35,50  * * * *  mautic:campaigns:update
10,25,40,55 * * * *  mautic:campaigns:trigger
```

1. **Segments are recomputed, not maintained.** `segments:update` re-runs every segment's filters as SQL against the
   whole `leads` table and diffs the result into `lead_lists_leads`. Work grows with *total contacts × segments*,
   not with how many contacts actually changed. With millions of contacts, a single pass can outlast its 15-minute slot.
2. **Three chained cron stages.** A contact who qualifies at 10:01 joins the segment at 10:15, enters the campaign at 10:20
   and gets the first action at 10:25. Worst case is around 30 minutes before anything happens, longer under load.
3. **One hot log table.** `campaign_lead_event_log` carries about 15 secondary indexes and a unique key on
   `(event_id, lead_id, rotation)`. Every scheduled-event write updates all of them, and `campaigns:trigger`
   polls the same table for due events.
4. **Custom fields are columns.** Adding a field runs `ALTER TABLE leads` (hence the `mautic:custom-field:*-column`
   background commands). This is slow on large tables and caps the number of fields.
5. **Messenger covers only part of the work.** Only email sends and tracking hits go through queues. Segment and campaign logic stays in cron.

**What to keep from Mautic:** the campaign model (a segment or form as the source, then decisions such as opened/clicked/visited,
then actions and conditions with waits), points and stages, forms, landing pages, focus items, Dynamic Web Content,
the tracking pixel and JS, the contact timeline, plugins/integrations (CRM, Zapier), and the GrapesJS email builder.

## Listmonk: why it is fast

- **One Go binary** with a goroutine pool of senders and no ORM, so per-message overhead is tiny.
- **Cursor-based sending.** A campaign records `max_subscriber_id` at start and walks `subscribers` by id
  (`last_subscriber_id` → `max_subscriber_id`) in batches. That is one sequential index scan, and it resumes after a crash.
- **JSONB attributes.** New fields need no migration. Segments are SQL expressions evaluated once at send time.

**Its limits:** no event stream, no journeys, no real-time triggers, email only, single tenant, and segments need SQL skills.

## WebEngage: the feature target

Real-time segments on user attributes and events (including "did X n times in the last d days" and "did not do X"),
a visual journey designer with waits and splits, and every channel: email, SMS, WhatsApp, RCS, web push, mobile push,
in-app, on-site notifications. It also offers per-user frequency capping, DND/quiet hours, and analytics by funnel, cohort and retention.

## Design conclusions for Pulse

1. **React to changes.** An incoming event re-evaluates only the segments that reference that event or field, for that one contact.
   Cost per event stays constant no matter how big the database is.
2. **Keep history separate from state.** ClickHouse holds the immutable event log and analytics. Postgres holds mutable state
   (profiles, enrollments, timers). Redis holds hot membership bitmaps and counters.
3. **Use Kafka between every stage**, keyed by `tenant:contact` so each contact's events are processed in order. Each stage scales out separately.
4. **Durable timers instead of cron.** Journey waits are rows with a due time, claimed with `FOR UPDATE SKIP LOCKED`.
5. **Send like Listmonk:** stream contact ids and batch provider calls, with per-tenant and per-provider rate limits.
6. **Store custom fields as JSONB/Map**, never as columns.
