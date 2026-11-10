# Load test, November 2026

What Pulse sustains on one machine, what limited it at each step, and what was changed.

## Setup

- **Hardware:** one 12-core / 31 GB workstation running *everything*: Kafka, ClickHouse, Postgres, Redis, Mailpit,
  Pulse (`pulse all`, every role in one process) and the load generator. These numbers are a floor. Production
  spreads the roles and stores across machines.
- **Load:** `cmd/loadgen` (`make load ARGS=...`). 200-event HTTP batches over N contacts. A contact's first event is an
  `identify` with attributes, and later events are a view/login/purchase mix. Each run is a new tenant with 6 segments
  (attribute, event-window, negative and combined rules) and a journey that emails repeat buyers.
- **End-to-end latency** comes from a probe contact. Once a second it flips an attribute, and the probe measures the time until
  the API shows the segment change. Probe events share partitions with the load, so any backlog shows up in this number.
- Bottlenecks were found from per-stage Kafka consumer lag, plus CPU, plus Postgres `pg_stat_activity` sampling.

## Results

| Run | Before | After |
|---|---|---|
| 2k ev/s, 100k contacts | p50 114 ms, p95 2.35 s | — |
| 5k ev/s, 100k contacts | **p50 18 s, max 98 s** (profile stage backlogged) | **p50 26 ms, p95 140 ms** |
| 10k ev/s, 500k contacts | — | **p50 113 ms, p95 313 ms**, max 498 ms |
| 20k ev/s, 20k contacts (mostly existing) | — | p50 91 ms, p95 273 ms |
| 20k ev/s, 500k contacts (mostly new) | stalled: ~1–2k ev/s processed | keeps up. Lag peaks at 48k during the creation burst, back to 0 within 45 s. p50 182 ms, p95 2.3 s |

Ingest stayed healthy throughout: 200-event requests took p50 7–14 ms with 0 failures at every rate.
For comparison, Mautic's cron cycle puts segment entry at up to 15 minutes and a campaign's first action at up to about 30 minutes.

## Bottlenecks found and fixed, in the order the data showed them

1. **Profile stage, per-event round trips.** At 5k ev/s its lag grew about 4k/s while every later stage sat near 0, and CPU
   was low. Each event made about 6 sequential network calls, including a synchronous Kafka produce with 5 ms linger.
   *Fix:* process each partition's poll as one batch (`kafka.Consumer.RunPartitionBatches`), folding events in
   order with profiles held in memory. That means one Redis read, one contact lookup, one write, one publish per batch, and producer linger set to 0.
2. **Segment engine, the same pattern,** exposed once profile sped up (67k lag at 30 s). *Fix:* `HandleBatch`
   prefetches every counter in one pipeline, then writes every membership bit in one pipeline. `SETBIT`
   returns the old bit, so changes are detected in order even for several events of one contact.
3. **Contact creation was serialized.** 20k ev/s kept up with existing contacts but not with new ones (a controlled
   experiment confirmed this). IDs were reserved in blocks of 100 under a process-wide mutex held across a Postgres round trip.
   *Fix:* one `UPDATE tenant_counters … + n` per batch and one multi-row `INSERT … unnest(...)`, with no mutex.
4. **Partition starvation under backlog.** Per-partition lag showed only 4 of 12 partitions moving: one 5,000-record
   fetch filled up from a few partitions, and the poll waits for every partition's goroutine. *Fix:*
   `FetchMaxPartitionBytes(256 KiB)` keeps each poll balanced. Profile writes also went from one UPDATE per contact to a single
   `UPDATE … FROM unnest(...)` per batch.

Along the way: `Consumer.Close` hung under `BlockRebalanceOnPoll`, so every restart was SIGKILLed after 10 s and the next instance
waited about 45 s for a session timeout. Restart now takes 3.9 s.

## Next limits (not yet addressed)

- **Redis is single-threaded** and reached 100% of one core at the end of the 20k run. The next step is Redis Cluster or
  Dragonfly, sharding counters and bitmaps by tenant.
- **The per-poll barrier:** a poll's partitions run in parallel, but the next poll waits for the slowest one. The next step is a
  goroutine per assigned partition with per-partition commits, so one slow partition can't stall the others.
- **One process, one machine:** every role and store shares 12 cores here. Running roles as separate deployments
  (each its own consumer group, scaled to the partition count) is the path to 100k ev/s. Partition count (12) caps
  parallelism per role, so raise it for production.
- **Burst creation:** p95 rises to about 2 s while hundreds of thousands of contacts are created at once (bulk imports). A
  dedicated import path (CSV → COPY) would keep that out of the real-time pipeline.

## Analytics queries

Batch 7's reports (cohorts, attribution, custom reports, funnel breakdowns) were not load-tested yet. They are bounded
(a year, named events, `LIMIT 100`), but cohorts by first seen, attribution and attribute breakdowns will want the
rollups listed in the architecture doc ("At scale") once a tenant has billions of events.

## Reproduce

```bash
make up
make load ARGS="-contacts 100000 -rate 5000 -duration 45s"
make load ARGS="-contacts 500000 -rate 20000 -duration 45s -workers 96"
```
