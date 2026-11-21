# Security policy

## Reporting a vulnerability

Please don't report security problems in public issues, pull requests or discussions.

Report them privately through GitHub's private vulnerability reporting:

1. Open this repository on GitHub and go to the **Security** tab.
2. Click **Report a vulnerability**.
3. Fill in the form. Only you and the maintainers can see the report.

If you don't see the button, the feature may not be turned on yet. In that case open a public issue
that says only that you have a security report and would like a private channel, with no details,
and a maintainer will get in touch.

Helpful things to include:

- What the problem is and what an attacker could do with it (for example, read another
  workspace's data, sign in as someone else, or make the server call an internal address).
- The Pulse commit or version, and which roles were running (`pulse all` or separate roles).
- Steps to reproduce, ideally against the local stack from the README (`make up`), with the
  requests involved.
- Any configuration that matters, such as `PULSE_ALLOW_PRIVATE_SSO`, `PULSE_ALLOW_PRIVATE_WEBHOOKS`
  or a reverse proxy in front of Pulse.

The maintainers will reply in the private report, work on a fix with you there, and publish a
security advisory when the fix is released. Tell us if you'd like to be credited.

## Supported versions

Pulse has no long-term-supported releases yet. Security fixes go into the main branch, so run a
recent version of it.

## Out of scope

- The local development stack in `deploy/docker-compose.yml`. It uses published development
  passwords, an admin token and a secret key on purpose and must never be exposed to a network.
- Problems that need an operator to turn off a protection on purpose, such as allowing private
  addresses for webhooks or single sign-on, unless the documentation says the setting is safe.
- Reports from automated scanners without a demonstrated impact.

## Running Pulse safely

The production checklist is in [Installation](docs/developer/01-installation.md), and how Pulse
protects data is described in the [security section of the architecture document](docs/architecture.md#15-security).
In short: set your own `PULSE_SECRET_KEY` (and a long random `PULSE_ADMIN_TOKEN` only if you need one), serve Pulse over HTTPS, set
`PULSE_PAGES_URL` to its own registrable domain, keep public sign-up off (`PULSE_ALLOW_SIGNUP`, off by
default) unless it sits behind a CAPTCHA or on a private network, turn on **Require verified identity
for web events** in each workspace that tracks a website or app, and never run the development
`deploy/docker-compose.yml` on a server.
