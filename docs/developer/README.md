# Pulse developer documentation

For people who install, run, integrate with or change Pulse. If you use Pulse to run campaigns, start
with the [tutorials](../tutorials/README.md) instead.

| # | Guide | Covers |
|---|---|---|
| 1 | [Installation](01-installation.md) | Local quick start, what each container does, production deployment, upgrades, backups, troubleshooting |
| 2 | [Configuration reference](02-configuration.md) | Every `PULSE_*` setting, workspace settings (Automation & limits), limits |
| 3 | [API guide](03-api.md) | Authentication (sessions, two-step sign-in, keys), events, contacts and the contacts behind every number, segments, templates and AMP, campaigns (journeys and broadcasts), reports and dashboards, providers, settings, web push, errors, every route |
| 4 | [Web SDK](04-web-sdk.md) | `pulse.js`: install, identify, track, identity verification, on-site content (variants, order), forms, web push (opt-in prompt, install check) |
| 5 | [Mobile SDKs](05-mobile-sdks.md) | Flutter, Android (Kotlin) and iOS (Swift): push, in-app, app inbox |
| 6 | [Webhooks & integrations](06-webhooks-and-integrations.md) | Outbound webhooks, provider webhooks and their verification, reason codes, the custom gateway (custom requests and authentication), Gmail and Gupshup, Zapier / Make, CRM, Shopify, ad audiences |
| 7 | [Architecture & extending](07-architecture-and-extending.md) | Roles, Kafka topics, data stores, adding a channel, a journey step or a segment filter |
| 8 | [Contributing & testing](08-contributing-and-testing.md) | Repository layout, `make test` / `it` / `e2e`, web app tests, screenshots for the docs |

Design notes written while building Pulse: [review of Mautic and Listmonk](../01-review.md),
[architecture](../02-architecture.md), [load test](../03-load-test.md),
[feature parity](../05-feature-parity.md), [the marketing team's plan](../06-marketing-team-plan.md).

## Screenshots in these docs

Every picture comes from the running app, taken by a script, so it can be retaken after a UI change:

```bash
make up && make docs-shots            # all guides
make docs-shots SHOT=dev-01           # just the installation guide (pictures img/dev-01-*.png)
```

How it works, and how to add pictures to a new guide: [docs/screenshots/README.md](../screenshots/README.md).
