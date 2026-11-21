# Production deploy: one server behind Cloudflare

This folder runs Pulse on a single Ubuntu 24.04 server: Kafka, ClickHouse, Postgres, Redis, every
Pulse role in one process, and Caddy for TLS. Cloudflare sits in front for DNS, TLS to visitors and
the WAF. The server's firewall admits web traffic from Cloudflare only.

```
visitor ──https──▶ Cloudflare (WAF, TLS) ──https (Origin Certificate)──▶ Caddy :443 ──▶ Pulse 127.0.0.1:7070
                                                                                        ├─ Postgres, Redis
                                                                                        └─ Kafka, ClickHouse
```

Sized for a 6 vCPU / 12 GB / 100 GB NVMe server (for example a Contabo Cloud VPS 6), which handles
a public preview and workspaces up to a few million contacts. Memory caps per service are in
`docker-compose.yml`; raise them together if the server is bigger.

| File | What it does |
|---|---|
| `install.sh` | One-time server setup: packages, swap, Docker log rotation, secrets, firewall, cron, start |
| `gen-secrets.sh` | Writes `.env` once with fresh random secrets (never overwrites it) |
| `firewall.sh` | ufw: SSH, plus 80/443 from Cloudflare's published ranges only (refreshed weekly) |
| `create-workspace.sh` | Creates a workspace and its first admin (public signup is off) |
| `backup.sh` | Nightly Postgres dump (kept 14 days), weekly ClickHouse backup (kept 2) |
| `update.sh` | Backup, `git pull`, rebuild, restart |

You need a domain on Cloudflare and two names on it: one for the app (here `pulse.example.com`)
and one for landing pages and forms (`go.example.com`). Pages get their own origin so a page's
scripts can never touch a signed-in app session.

## 1. Create the Origin Certificate (Cloudflare)

Cloudflare → your domain → **SSL/TLS → Origin Server → Create Certificate**:

- Key type RSA (2048) or ECC, either works.
- Hostnames: `pulse.example.com` and `go.example.com` (or `*.example.com`).
- Validity: 15 years.

Keep the page open: Cloudflare shows the **private key only once**. You'll paste both parts on the
server in step 3. Don't email the key or paste it into chats or tickets.

## 2. Get the code onto the server

SSH in as root (or a sudo user and `sudo -i`), then:

```sh
git clone https://github.com/ranjeshgupta/pulse.git /opt/pulse
/opt/pulse/deploy/production/install.sh pulse.example.com go.example.com
```

The first run installs Docker, adds 4 GB of swap, writes `.env` with fresh secrets, turns on the
firewall and stops: it asks for the certificate.

> The firewall keeps SSH (port 22) open from anywhere, so your session isn't cut off. To allow SSH
> from your own IP only, run `SSH_FROM=203.0.113.7 /opt/pulse/deploy/production/firewall.sh` and
> change the cron line in `/etc/cron.d/pulse` to match.

## 3. Put the certificate on the server

```sh
cd /opt/pulse/deploy/production
nano certs/origin.pem    # paste "Origin Certificate", save
nano certs/origin.key    # paste "Private Key", save
chmod 600 certs/origin.key
./install.sh pulse.example.com go.example.com
```

This run builds Pulse (a few minutes the first time), starts everything and waits until
`/healthz` answers. Check with `docker compose ps`: every service should be `healthy` or `running`.

## 4. Point DNS at the server (Cloudflare)

**DNS → Records**, two records, both **Proxied** (orange cloud):

| Type | Name | Content |
|---|---|---|
| A | `pulse` | the server's IPv4 |
| A | `go` | the server's IPv4 |

Then:

- **SSL/TLS → Overview**: mode **Full (strict)**. This setting covers the whole domain. Sites
  served by Cloudflare Workers or Pages don't use it, but other proxied records pointing at servers
  without a valid certificate would start failing with error 526. If the domain has such records,
  leave the domain's mode alone and add a rule for the two Pulse names only: **Rules → Configuration
  Rules → Create rule**, *When* `Hostname is in {pulse.example.com go.example.com}`, *Then*
  **SSL → Full (strict)**.
- **SSL/TLS → Edge Certificates**: *Always Use HTTPS* on, *Minimum TLS Version* 1.2.
- **Caching**: leave the defaults. Cloudflare doesn't cache HTML or API responses unless you add
  a "Cache Everything" rule, and you shouldn't: pages and API answers are per visitor.

Open `https://pulse.example.com/healthz`: it should answer `{"status":"ok"}`.

## 5. WAF rules (Cloudflare → Security)

Pulse already limits sign-in attempts, signups, expensive reports and public form posts per IP. The
WAF adds a layer before traffic reaches the server.

- **Rate limiting rule** (Security → WAF → Rate limiting rules):
  *When* `URI Path starts with /v1/auth/` and `Hostname equals pulse.example.com`;
  *with the same* IP; *more than* 20 requests per 10 seconds; *then* Block for 10 seconds.
- **Managed rules**: keep the Cloudflare Free Managed Ruleset on.
- **Optional custom rule**, if you never expect admins outside some countries:
  `(http.host eq "pulse.example.com" and starts_with(http.request.uri.path, "/login") and not ip.src.country in {"IN"})` → Managed Challenge.

Don't turn on **Bot Fight Mode** or **I'm Under Attack**, and don't challenge these paths. They are
called by machines that can't solve a challenge:

| Path | Called by |
|---|---|
| `/v1/events`, `/v1/web/...`, `/sdk/...`, other `/v1/...` calls with an API key | Web, mobile and server SDKs |
| `/t/...` | Email open pixels and click redirects (mail clients and scanners) |
| `/u/...` | One-click unsubscribe (mail providers post to it) |
| `/v1/hooks/...`, provider callback URLs | Integrations and email/SMS providers' delivery reports |
| `/f/...`, `/s/...` on the pages host | Embedded forms and surveys on other sites |

## 6. Create the first workspace

```sh
cd /opt/pulse/deploy/production
./create-workspace.sh "Your Company" you@example.com
cat first-admin-*.txt
```

Sign in at `https://pulse.example.com/login` with the workspace short name, email and password
from that file. Change the password under **Your account**, store the API key somewhere safe and delete
the file (`rm first-admin-*.txt`).

## 7. Email

Each workspace adds its email provider in the app (Settings → Channels): Amazon SES, SendGrid,
Mailgun, Postmark or any SMTP server. Set up SPF, DKIM and DMARC for the sending domain at the
provider. To give every workspace a default relay, set `SMTP_ADDR` and `SMTP_FROM` in `.env` and
run `docker compose --env-file .env up -d`.

Many VPS providers block outgoing port 25; use the provider's port 587.

## After the deploy

If someone else ran the deploy over SSH for you:

1. Remove their key: delete its line from `/root/.ssh/authorized_keys`.
2. Change the root password: `passwd`.
3. Prefer keys over passwords: once your own key works, set `PasswordAuthentication no` in
   `/etc/ssh/sshd_config.d/50-local.conf` and run `systemctl reload ssh`.
4. Copy `/opt/pulse/deploy/production/.env` somewhere safe off the server. `PULSE_SECRET_KEY`
   decrypts the provider credentials stored in the database; a backup without it is incomplete.

## Day-to-day

| Task | Command (in `/opt/pulse/deploy/production`) |
|---|---|
| Status | `docker compose ps` |
| Logs | `docker compose logs -f --tail 100 pulse` |
| Update to the latest version | `./update.sh` |
| Backup now | `./backup.sh` (add `FULL=1` to include ClickHouse) |
| Restart Pulse only | `docker compose --env-file .env restart pulse` |

Backups land in `backups/` on the same disk. To copy them off the server, install rclone
(`apt install rclone`), configure a remote with `rclone config` (Cloudflare R2, S3, Backblaze...)
and set `BACKUP_REMOTE=remote:bucket` in `.env`. Contabo's Auto Backup snapshots the whole disk as
well.

### Restore

```sh
# Postgres (workspaces, users, campaigns, contacts' profile store)
docker compose exec -T postgres pg_restore -U pulse -d pulse --clean --if-exists < backups/postgres/pulse-<stamp>.dump
# ClickHouse (events, message history)
docker compose exec -T clickhouse clickhouse-client --user pulse --password "$CH_PASSWORD" \
  -q "RESTORE DATABASE pulse FROM Disk('backups', 'pulse-<stamp>.zip')"
```

Load `.env` first (`. ./.env`) so `$CH_PASSWORD` is set. Restore ClickHouse into an empty database:
drop it first with `DROP DATABASE pulse` if it exists.

## Troubleshooting

| Symptom | Check |
|---|---|
| Cloudflare error 521/522 | `docker compose ps caddy`; `ufw status` must list Cloudflare ranges on 80,443 |
| Cloudflare error 526 | SSL mode is Full (strict) but `certs/` doesn't hold the Origin Certificate for that name |
| Sign-in loops back to the login page | Open the app on its `https://` name, not the IP; the session cookie is https-only |
| Every visitor shows the same IP | `PULSE_TRUST_PROXY` and `PULSE_TRUST_CLOUDFLARE` must be `true` (they are by default here) |
| `install.sh` says the Cloudflare IP list looks wrong | The server couldn't reach cloudflare.com; the firewall is left as it was. Run it again |
