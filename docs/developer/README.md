# Pulse developer documentation

For people who install, run, integrate with or change Pulse. If you use Pulse to run campaigns, start
with the [tutorials](../tutorials/README.md) instead.

| # | Guide | Covers |
|---|---|---|
| 1 | [Installation](01-installation.md) | Local quick start, what each container does, production deployment, upgrades, backups, troubleshooting |
| 2 | Configuration reference *(coming)* | Every `PULSE_*` setting, workspace settings, limits |
| 3 | API guide *(coming)* | Authentication, events, contacts, segments, journeys, broadcasts, errors |
| 4 | Web SDK *(coming)* | `pulse.js`: install, identify, track, identity verification, on-site content, forms, push |
| 5 | Mobile SDKs *(coming)* | Flutter, Android (Kotlin) and iOS (Swift): push, in-app, app inbox |
| 6 | Webhooks & integrations *(coming)* | Outbound webhooks, provider webhooks, Zapier / Make, CRM, Shopify, ad audiences |
| 7 | Architecture & extending *(coming)* | Roles, Kafka topics, data stores, adding a channel, a journey step or a segment filter |
| 8 | Contributing & testing *(coming)* | Repository layout, `make test` / `it` / `e2e`, web app tests, screenshots for the docs |

Design notes written while building Pulse: [review of Mautic and Listmonk](../01-review.md),
[architecture](../02-architecture.md), [load test](../03-load-test.md),
[feature parity](../05-feature-parity.md).

## Screenshots in these docs

Every picture comes from the running app, taken by a script, so it can be retaken after a UI change:

```bash
make up && make docs-shots            # all guides
make docs-shots SHOT=dev-01           # just this installation guide
```

How it works, and how to add pictures to a new guide: [docs/screenshots/README.md](../screenshots/README.md).
