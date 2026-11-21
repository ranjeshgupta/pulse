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

The production set-up is in [Installation](docs/developer/01-installation.md), and how Pulse
protects data is described in the [security section of the architecture document](docs/architecture.md#15-security).
Before a deployment is reachable from the internet, check each of these:

- **Never run `deploy/docker-compose.yml` on a server.** It is the local development stack: it uses
  published passwords, an admin token and a secret key, allows public sign-up and lets Pulse call
  private addresses. Its ports are bound to `127.0.0.1` so it isn't reachable from other machines
  on your laptop, but that doesn't make it safe anywhere else. Write your own compose file or
  deployment ([Installation](docs/developer/01-installation.md#production-deployment)).
- **Your own secrets.** Set `PULSE_SECRET_KEY` (`openssl rand -hex 32`, kept in a secret store and
  backed up), and `PULSE_ADMIN_TOKEN` only if you need it, as a long random value. Pulse refuses the
  dev values unless `PULSE_PUBLIC_URL` is a local address.
- **Public sign-up off.** `PULSE_ALLOW_SIGNUP` is off by default; create workspaces with the admin
  token. Turn it on only behind a CAPTCHA or on a private network (it is then limited by
  `PULSE_SIGNUP_RATE` per client IP).
- **HTTPS only.** Serve Pulse over https and set `PULSE_PUBLIC_URL` to the `https://` address. The
  session cookie is then `__Host-pulse_session` (`Secure`, this host only) and Pulse sends HSTS for
  a year, including subdomains, so serve every subdomain of the app's host over https too.
- **A separate pages domain.** Set `PULSE_PAGES_URL` to a host on its own registrable domain (for
  example `acme-pages.example` next to `pulse.example.com`), not a sibling subdomain, so landing
  pages, forms and surveys your users write never share cookies with the app. `api` logs a warning
  at start when it is missing.
- **The right client IP.** Set `PULSE_TRUST_PROXY=true` only when Pulse can be reached through your
  reverse proxy alone (bind it to `127.0.0.1` or a private network). Behind Cloudflare, also set
  `PULSE_TRUST_CLOUDFLARE=true`, and only together with an origin firewall that accepts connections
  from [Cloudflare's IP ranges](https://www.cloudflare.com/ips/) alone; otherwise anyone can choose
  their IP with a header and get around per-IP limits.
- **Private data services.** Kafka, Redis and ClickHouse connections have no TLS settings (and
  Kafka and Redis no passwords) in Pulse; keep them on a private network, and give Postgres and
  ClickHouse your own passwords.
- **Private addresses stay blocked.** Leave `PULSE_ALLOW_PRIVATE_WEBHOOKS` and
  `PULSE_ALLOW_PRIVATE_SSO` off unless you know you need them.
- **Verified identity.** Turn on **Require verified identity for web events** in each workspace that
  tracks a website or app.
