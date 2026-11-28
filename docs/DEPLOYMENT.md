# Deployment runbook — staging on Vultr

Status of this document: **staging executed, production documented only.**
Everything marked `NOT VERIFIED HERE` needs the real server and has to be
confirmed by the operator, with the output pasted back into the deployment
record. Nothing in this file contains a password, key or token.

| Phase | State |
|---|---|
| Staging on Vultr Cloud Compute (Ubuntu 24.04 LTS x64, Frankfurt) | Steps S1–S11, executed with the operator |
| Production | Steps P1–P8, written but **not executed** until staging passes |

Related documents: [OWNER_ACCESS.md](OWNER_ACCESS.md) (who may hold the database
owner credential), [VERIFICATION_REPORT.md](VERIFICATION_REPORT.md) (raw proof
for every claim below), [PROGRESS.md](PROGRESS.md) (what changed in this pass).

---

## 1. What is deployed

```
                    internet
                       │  80/443 (+ 443/udp)
                 ┌─────▼──────┐
                 │   caddy    │  TLS, HTTP→HTTPS, security headers,
                 │  (edge)    │  /api → api, everything else → web
                 └──┬──────┬──┘
          edge  ┌───▼──┐ ┌─▼────┐
                │ web  │ │ api  │  React SPA (nginx)  ·  Node/Express
                └──────┘ └──┬───┘
                            │ internal (no published port)
                       ┌────▼─────┐        ┌──────────┐
                       │ postgres │◀───────│ migrate  │ one-shot, owner role
                       └────┬─────┘        └──────────┘
                            │ internal
                       ┌────▼─────┐
                       │  backup  │ ── egress ─▶ Vultr Object Storage (S3)
                       └──────────┘
```

| Service | Image (pinned) | Runs as | Published | Purpose |
|---|---|---|---|---|
| `caddy` | `caddy:2.9.1-alpine` | uid 1000 | 80, 443, 443/udp | TLS + reverse proxy; the only public service |
| `web` | `nginxinc/nginx-unprivileged:1.27.4-alpine` | nginx unprivileged | – | static SPA build |
| `api` | built from `deploy/api.Dockerfile` (`node:22.17.0-alpine`) | `node` (uid 1000) | – | Express API on `:8080` |
| `postgres` | `postgres:16.9-alpine` | postgres | **none** | database, internal network only |
| `migrate` | same image as `api` | `node` | – | one-shot: apply migrations, set the app role password |
| `backup` | built from `deploy/backup.Dockerfile` | `postgres` | – | one-shot: encrypted dump → object storage |

Networks: `edge` (caddy, api, web), `internal` (postgres, api, migrate, backup —
`internal: true`, no route out), `egress` (backup only). Volumes:
`pamoja-pgdata`, `caddy-data`, `caddy-config`, `backup-logs`.

Every service runs with `no-new-privileges`, a read-only root filesystem, a
tmpfs for what it must write, memory and CPU limits, and rotated JSON logs.
Health checks: `pg_isready`, `/api/health` inside the API, nginx `GET /`,
`nc -z 127.0.0.1 80` for Caddy.

### Two roles, one database

* `pamoja_owner` — owns the schema. Used **only** by the one-shot `migrate`
  service and the CLI scripts (`seed`, `create-admin`, `purge-demo`,
  `system-checks`). Never present in the API container.
* `pamoja_app` — the least-privilege role the API connects as. Row-level
  security applies to it; it has no `DELETE` on the protected identity tables
  and no `UPDATE` on `audit_logs`.

`src/migrate.js` sets `pamoja_app`'s password from `APP_DB_PASSWORD` on every
run, so the real application password never appears in a migration file or in
the API's environment as owner access. The API refuses to start if
`ADMIN_DATABASE_URL`, `PGPASSWORD`, `POSTGRES_PASSWORD` or another owner
credential is in its environment (see `server/src/prodGuards.js`).

---

## 2. Prerequisites

### 2.1 Server

| Item | Staging value |
|---|---|
| Provider / plan | Vultr Cloud Compute, `vc2-2c-4gb` (2 vCPU / 4 GB / 80 GB SSD) |
| Image | Ubuntu 24.04 LTS x64 |
| Region | `fra` (Frankfurt) — same region as the backup bucket |
| Label / hostname | `pamoja-staging` |
| Instance IP | read it from the Vultr console; it is the value the DNS record points at |
| Vultr automatic backups | enabled |

`deploy/terraform/` contains an illustrative provider configuration for this
instance. It has **never been applied** and is documentation only; the server
is created in the Vultr console. Do not run it against production.

### 2.2 DNS

Staging uses a free `duckdns.org` subdomain, e.g. `pamoja-staging.duckdns.org`,
pointed at the instance IP. Caddy obtains a Let's Encrypt certificate through
the HTTP-01 challenge, which needs **port 80 reachable from the internet** and
the name resolving correctly before the first start.

```sh
# from your workstation — expect the instance IP, then a 200 or 404 from Caddy
dig +short pamoja-staging.duckdns.org
```

`duckdns.org` is a staging convenience only. Production must use a
Yopsa-owned domain (P2).

### 2.3 Object Storage

One **private** Vultr Object Storage bucket in `fra`, plus an access key /
secret pair scoped to it. Bucket name suggestion: `pamoja-backups-staging`.
Nothing is public; the `backup` container reaches it over `egress`.

### 2.4 Backup key pair (age)

Generate the key pair **on your own machine**, never on the server:

```sh
age-keygen -o pamoja-backup-key.txt     # keep this offline; never upload it
grep '^# public key:' pamoja-backup-key.txt   # age1... goes into the env file
```

The server only ever holds the **public** key (`BACKUP_AGE_RECIPIENT`). Without
the private key an object in the bucket is unreadable, which is the point.

### 2.5 Workstation tools

`ssh`, `git`, `openssl`, `age` (for the restore drill), and optionally `aws` for
inspecting the bucket. Docker is **not** required on your workstation.

---

## 3. Secrets: where they live

| Rule | Detail |
|---|---|
| Location | `/opt/pamoja/.env.production` on the server, mode `0600`, owned by `pamoja-deploy` |
| Source of truth | this file only — never a chat message, ticket, commit or shell history |
| In the repository | nothing. `.gitignore` excludes `.env*`; only `deploy/.env.production.example` (empty values) is committed |
| In logs | the scripts print key names and counts, never values. `gitleaks` runs in CI on every push |
| Rotation | edit the file, then `scripts/deploy.sh` (or a compose `up -d` with the same `--env-file`) picks it up |

Generate each secret **on the server**, directly into the file, so it never
crosses a terminal you might copy from:

```sh
# 64 hex characters: URL-safe, so it can go inside DATABASE_URL unchanged
openssl rand -hex 32
# for AUTH_TOKEN_SECRET / PAMOJA_MASTER_KEY (base64 is fine, they are not URLs)
openssl rand -base64 48
```

> **Passwords inside URLs must be URL-safe.** `DATABASE_URL` and
> `ADMIN_DATABASE_URL` embed the password. `openssl rand -base64` can emit `+`,
> `/` and `=`, which break the URL. Use `openssl rand -hex 32` for
> `POSTGRES_PASSWORD` and `APP_DB_PASSWORD`, or percent-encode the value.

When you paste output back into the deployment record, redact: any line
containing `PASSWORD`, `SECRET`, `TOKEN`, `DATABASE_URL`, `ACCESS_KEY`,
`AGE_RECIPIENT`'s private half, and the one-time admin password.

---

## 4. Staging, step by step

Every command below is run **by the operator**. Where output is needed, the
expected result is stated; paste that line back.

### S1 — Create the instance and its firewall

In the Vultr console: deploy `pamoja-staging` as in §2.1, attach a firewall
group with exactly these rules, then add your SSH public key to the instance.

| Direction | Protocol | Port | Source |
|---|---|---|---|
| inbound | TCP | 22 | any (staging choice; production restricts this — P2) |
| inbound | TCP | 80 | any — ACME HTTP-01 and the HTTPS redirect |
| inbound | TCP | 443 | any |
| inbound | UDP | 443 | any — HTTP/3 |

Port 5432 is **never** opened. The database has no published port either.

```sh
ssh root@<STAGING_IP> 'lsb_release -ds; uname -m; free -m | head -2'
```

Expected: `Ubuntu 24.04.x LTS`, `x86_64`, and about 3900 MB total memory.

### S2 — Harden the server

```sh
# as root, over SSH; keep this session open until S2's check passes
ssh root@<STAGING_IP>
curl -fsSLO https://raw.githubusercontent.com/<org>/<repo>/deploy-staging/scripts/harden-server.sh
sudo DEPLOY_USER=pamoja-deploy sh harden-server.sh
```

The script installs base packages, creates `pamoja-deploy` with key-only sudo,
turns on unattended security upgrades, hardens sshd, enables `ufw`
(22/80/443), `fail2ban`, Docker from Docker's repository, journald limits,
`chrony`, a 2 GB swap file and `vm.swappiness=10`.

**Do the second-session check it prints before closing anything:**

```sh
ssh pamoja-deploy@<STAGING_IP> 'sudo -n true && echo SECOND_SESSION_OK'
```

Paste back: `SECOND_SESSION_OK`, `sudo ufw status verbose`, and
`docker version --format '{{.Server.Version}}'`.

Recovery if SSH locks you out: the Vultr web console. Revert with
`rm /etc/ssh/sshd_config.d/99-pamoja-hardening.conf && systemctl reload ssh`.

### S3 — Check out the code and create the environment file

```sh
sudo install -d -o pamoja-deploy -g pamoja-deploy -m 755 /opt/pamoja
sudo -u pamoja-deploy git clone https://github.com/<org>/<repo>.git /opt/pamoja/app
sudo -u pamoja-deploy git -C /opt/pamoja/app checkout deploy-staging

sudo -u pamoja-deploy install -m 600 /dev/null /opt/pamoja/.env.production
sudo -u pamoja-deploy cp /opt/pamoja/app/deploy/.env.production.example /opt/pamoja/.env.production
sudo -u pamoja-deploy chmod 600 /opt/pamoja/.env.production
```

Fill it in. Read the header of `deploy/.env.production.example` first: it lists
every variable, whether it is required, and whether it is secret. The values
that must not be left empty are `PAMOJA_DOMAIN`, `POSTGRES_*`,
`ADMIN_DATABASE_URL`, `APP_DB_PASSWORD`, `DATABASE_URL`, `AUTH_TOKEN_SECRET`,
`PAMOJA_MASTER_KEY`, the five `BACKUP_*` values, and `PAMOJA_DEMO_TOOLING=on`
(staging only).

Shape of the two database URLs (hex passwords, so no encoding is needed):

```
POSTGRES_PASSWORD=<hex>
APP_DB_PASSWORD=<hex>

ADMIN_DATABASE_URL=postgres://pamoja_owner:<POSTGRES_PASSWORD>@postgres:5432/pamoja
DATABASE_URL=postgres://pamoja_app:<APP_DB_PASSWORD>@postgres:5432/pamoja
```

Note the host `postgres`: it is the Compose service name, resolved on the
internal network — not `localhost`.

Verify without printing secrets:

```sh
sudo -u pamoja-deploy sh -c 'set -a; . /opt/pamoja/.env.production; set +a
  for k in PAMOJA_DOMAIN POSTGRES_PASSWORD ADMIN_DATABASE_URL APP_DB_PASSWORD \
           DATABASE_URL AUTH_TOKEN_SECRET PAMOJA_MASTER_KEY BACKUP_S3_BUCKET \
           BACKUP_S3_ACCESS_KEY BACKUP_S3_SECRET_KEY BACKUP_AGE_RECIPIENT; do
    eval "v=\${$k}"
    if [ -n "$v" ]; then echo "$k present"; else echo "$k MISSING"; fi
  done'
```

Expected: one `present` line per key, no `MISSING`.

### S4 — First deploy

```sh
sudo -u pamoja-deploy FIRST_DEPLOY=1 /opt/pamoja/app/scripts/deploy.sh
```

`FIRST_DEPLOY=1` skips the pre-deploy backup (there is no database yet) and is
only ever used once. `scripts/deploy.sh` then, in order:

1. `git fetch` + `git checkout deploy-staging` + `git pull --ff-only`
2. tag the release images with the commit SHA
3. build `api`, `web`, `caddy`, `backup`
4. run the one-shot `migrate` service (owner role): apply `0001_core.sql` and
   `0002_invariants.sql`, then set the `pamoja_app` password from
   `APP_DB_PASSWORD`
5. `compose up -d`
6. poll `https://$PAMOJA_DOMAIN/health` and `/ready` for up to 120 s; on failure
   the previous image tags are restarted
7. run `scripts/smoke-test.sh`

Use `DRY_RUN=1` first if you want to see the plan without changing anything.

Expected tail of the output:

```
deploy ok: <sha>
smoke test: PASS
```

Paste back: the `commit <sha> <subject>`, `migrations complete: 2 applied, 2 total`,
`application role password updated`, `deploy ok:`, and the `smoke test:` line.

If the certificate cannot be issued yet, the health poll fails and the deploy
reports `health check failed`. Almost always DNS (§2.2) or port 80 in the
firewall (§S1); fix that and re-run — migrations are idempotent.

### S5 — Load synthetic data (staging only)

The demo seed refuses to run when `NODE_ENV` or `APP_ENV` is `production`, and
the stack runs both. Load the synthetic data as an explicit one-off, as the
owner, inside the same stack:

```sh
cd /opt/pamoja/app
sudo -u pamoja-deploy docker compose --project-name pamoja \
  --env-file /opt/pamoja/.env.production -f deploy/docker-compose.prod.yml \
  run --rm --no-deps -T \
  -e NODE_ENV=development -e APP_ENV=development -e DEMO_PASSWORD='<demo password>' \
  migrate node src/seed.js
```

Expected: `demo seed complete`. It creates 5 demo groups, 14 users and the
associated meetings, payments, receipts, loans and audit rows, all flagged
`is_demo`. Dates are relative to the day it runs, so the data stays coherent.

The override affects **only that one container**, never the API. Production
never runs this step; `scripts/production-preflight.sh` fails if any demo row
exists.

`PAMOJA_DEMO_TOOLING=on` in the staging env file makes the read-only admin demo
guide reachable at Admin → Demo (still administrator-only). With the variable
unset, that path answers 404 at the edge — which is the production posture.

### S6 — Create the first administrator

```sh
cd /opt/pamoja/app
sudo -u pamoja-deploy ADMIN_EMAIL='<you>@yopsa.co.ug' ADMIN_NAME='<Your Name>' \
  sh scripts/create-admin.sh
```

It prints a generated one-time password **once**. Sign in immediately, change
it, and keep it out of chat, tickets and logs. The account is granted `it_admin`
at Org scope, which is the role that administers users, roles and policy — not
a financial approval role. Additional administrators are created in the app.

### S7 — Smoke test and production-parity checks

```sh
BASE_URL=https://pamoja-staging.duckdns.org bash scripts/smoke-test.sh
```

13 checks: TLS certificate ≥14 days, the four security headers plus HSTS, CSRF
refusal, the login rate limit (or account lockout with
`SMOKE_LOCKOUT_EMAIL=<dedicated account>`), port 5432 closed from the internet,
demo tooling absent (the demo guide is either 404 or requires sign-in),
`/health` and `/ready`.

Run the production-only gate **after** the production deploy (P6), not on
staging: it asserts zero demo rows, so it is meant to fail on a seeded staging
database.

### S8 — Install the scheduled jobs

```sh
cd /opt/pamoja/app
sudo cp deploy/systemd/pamoja-backup.* deploy/systemd/pamoja-monitor.* /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now pamoja-backup.timer pamoja-monitor.timer
systemctl list-timers 'pamoja-*' --all
```

| Timer | When | What |
|---|---|---|
| `pamoja-backup.timer` | daily 02:15 UTC (+ up to 5 min jitter) | encrypted dump → object storage |
| `pamoja-monitor.timer` | hourly (+ up to 2 min jitter) | disk, certificate, failed jobs, audit chain, backup age |

The monitor unit hard-codes `Environment=BASE_URL=https://pamoja-staging.duckdns.org`.
Change it when the domain changes (production, P7).

Run both once by hand before trusting the timers:

```sh
sudo systemctl start pamoja-backup.service; journalctl -u pamoja-backup -n 20 --no-pager
sudo systemctl start pamoja-monitor.service; journalctl -u pamoja-monitor -n 30 --no-pager
```

Expected: `OK key=s3://…/daily/pamoja-<stamp>.sql.gz.age bytes=… seconds=…` and
`monitor: all checks passed`. The first backup is the first real proof that the
bucket, the keys and the age recipient are right.

### S9 — Restore drill

Prove a backup can actually be read. The private key is not on the server, so
copy it in for the drill and remove it immediately afterwards:

```sh
# on your workstation
scp pamoja-backup-key.txt pamoja-deploy@<STAGING_IP>:/tmp/

# on the server
cd /opt/pamoja/app
sudo -u pamoja-deploy docker compose --project-name pamoja \
  --env-file /opt/pamoja/.env.production -f deploy/docker-compose.prod.yml \
  run --rm --no-deps -T -v /tmp/pamoja-backup-key.txt:/key.txt:ro \
  -e AGE_IDENTITY_FILE=/key.txt backup bash /scripts/restore-drill.sh

# remove the key from the server as soon as the drill prints its verdict
shred -u /tmp/pamoja-backup-key.txt
```

Expected: a table of live vs restored row counts that match, then
`PASS restore drill completed in <n>s`. Repeat monthly (production: monthly,
with the key never left on the box).

### S10 — Point an external uptime check at it

Nothing on the server can report that the server is down. Add a free external
monitor (or the Vultr one) for `https://pamoja-staging.duckdns.org/health`,
expecting `200` with `"status":"ok"`, alerting to the same channel as
`ALERT_WEBHOOK_URL`. One-minute interval is enough.

### S11 — Record the result

Paste into the deployment record: commit SHA, `docker compose ps` (all
`healthy`), `systemctl list-timers 'pamoja-*'`, the smoke-test summary, the
backup `OK` line, the restore-drill `PASS` line, and the monitor's first run.

---

## 5. Everyday operations

### Deploy a new commit

```sh
sudo -u pamoja-deploy /opt/pamoja/app/scripts/deploy.sh
```

Always deploys the tip of `deploy-staging` (override with `DEPLOY_BRANCH`). It
backs up first, builds by SHA, migrates, starts, health-checks, and rolls back
to the previous SHA if the health check fails. One deploy at a time is enforced
with `flock`.

### Roll back

Automatic on a failed health check. By hand:

```sh
cd /opt/pamoja/app
export API_IMAGE=pamoja-api:<previous-sha> WEB_IMAGE=pamoja-web:<previous-sha> \
       CADDY_IMAGE=pamoja-caddy:<previous-sha> BACKUP_IMAGE=pamoja-backup:<previous-sha>
docker compose --project-name pamoja --env-file /opt/pamoja/.env.production \
  -f deploy/docker-compose.prod.yml up -d
```

**Migrations are forward-only.** Rolling the images back does not undo a
migration. `migrate.js` records each applied file's SHA-256 and refuses an
edited file, so a bad migration is fixed by adding the next one, not by editing
history. Any release with a non-backward-compatible migration needs a forward
fix, not a rollback.

### Logs

```sh
docker compose --project-name pamoja --env-file /opt/pamoja/.env.production \
  -f /opt/pamoja/app/deploy/docker-compose.prod.yml logs -f --tail 100 api caddy
journalctl -u pamoja-monitor -n 50 --no-pager
```

Application logs are JSON with identifiers only — no names, phone numbers,
national ids, addresses, passwords or tokens. Docker rotates them at 10 MB × 3.

### Backup and restore

```sh
# backup now
sudo systemctl start pamoja-backup.service
# what is in the bucket
docker compose --project-name pamoja --env-file /opt/pamoja/.env.production \
  -f /opt/pamoja/app/deploy/docker-compose.prod.yml run --rm -T backup \
  bash -c 'aws --endpoint-url "$BACKUP_S3_ENDPOINT" s3 ls "s3://$BACKUP_S3_BUCKET/daily/" | tail -5'
```

Retention: `daily/` 7, `weekly/` 4, `monthly/` 6 (Sundays also copy to
`weekly/`, the 1st also to `monthly/`). Objects are `pg_dump | gzip | age`;
without the private key they are unreadable. `backup.sh` alerts to
`ALERT_WEBHOOK_URL` and exits non-zero on any failure, and refuses a dump
smaller than 1 KB.

### Rotate a secret

1. Edit `/opt/pamoja/.env.production` (mode 0600 stays).
2. `AUTH_TOKEN_SECRET` or `PAMOJA_MASTER_KEY`:
   `docker compose --project-name pamoja --env-file /opt/pamoja/.env.production -f /opt/pamoja/app/deploy/docker-compose.prod.yml up -d api`
   — every session is invalidated, users sign in again.
3. `APP_DB_PASSWORD` **and** the password inside `DATABASE_URL` together, then
   `scripts/deploy.sh` (the migrate step re-sets the role password).
4. `POSTGRES_PASSWORD` **and** `ADMIN_DATABASE_URL`: change the role password
   inside the database first, then the file, then redeploy.
5. Object Storage keys: create the new key in Vultr, update the file, run the
   backup once by hand, then delete the old key.

### Incident response

| Symptom | First check | Then |
|---|---|---|
| 502/503 from the edge | `docker compose ps` | `docker compose logs api`; if the API is crash-looping, read the one-line JSON `refusing to start` reason — a production guard fired, fix the environment |
| `refusing to start … ADMIN_DATABASE_URL must not be set` | `docker compose exec api printenv ADMIN_DATABASE_URL` | remove the owner credential from the API service; it belongs to `migrate` only |
| `/ready` 503 but `/health` 200 | schema missing or DB unreachable | `docker compose logs postgres`, confirm the `migrate` service completed |
| certificate not issued | DNS, port 80, rate limit | `docker compose logs caddy`; Let's Encrypt rate-limits repeated failures |
| disk filling | `df -h`, `docker system df` | rotate images (`docker image prune -f`), check `backup-logs` volume |
| suspected credential leak | which credential | rotate per §"Rotate a secret"; audit chain check: `docker compose exec api node scripts/audit-chain.mjs` |
| database corruption | newest backup | restore into a new database with `restore-drill.sh`, then swap `DATABASE_URL` |

---

## 6. Production (documented, not executed)

Production is the same stack with a different environment file. The differences
are deliberate:

| # | Step | Difference from staging |
|---|---|---|
| P1 | Instance | Production-sized plan, `fra`, Vultr backups enabled, label `pamoja-prod` |
| P2 | Firewall and SSH | **Restrict 22** to named admin addresses or a VPN. Set `SSH_ALLOW_FROM=<cidr>` when running `harden-server.sh` |
| P3 | DNS and TLS | A Yopsa-owned domain with an A record; no `duckdns.org`. Caddy issues the certificate on first start |
| P4 | Environment file | New secrets for production (never copied from staging). Separate Object Storage bucket, e.g. `pamoja-backups-prod`. `PAMOJA_DEMO_TOOLING` **empty** |
| P5 | First deploy | `FIRST_DEPLOY=1 scripts/deploy.sh`, then `scripts/create-admin.sh`. **No seed step** |
| P6 | Preflight | `BASE_URL=https://<domain> sh scripts/production-preflight.sh` — must print `production preflight: PASS`: zero demo rows in every table, the seed refuses, the API holds no owner credential, demo tooling 404, health and ready 200 |
| P7 | Timers | Same units; change `Environment=BASE_URL=…` in `pamoja-monitor.service` to the production domain |
| P8 | Backups | Same schedule; keep the age private key off-site, and run the restore drill monthly |

Do not load member data until P6 passes. The go-live checklist also covers the
operational steps the schema cannot enforce — changing the bootstrap admin
password, setting `DEMO_PASSWORD` nowhere in production, and recording who owns
the domain, the bucket, the age key and the Vultr account.

---

## 7. What was verified, and where

Run in this repository's sandbox (Linux, Docker) unless marked otherwise.

| Check | Result |
|---|---|
| `docker compose config` on `deploy/docker-compose.prod.yml` | valid; only `caddy` publishes ports (80, 443, 443/udp) |
| Build `api`, `web`, `caddy`, `backup` images from `deploy/*.Dockerfile` | all four build |
| `caddy validate` / `caddy adapt` on `deploy/Caddyfile` | `Valid configuration`; the demo-guide matcher adapts to a single expression |
| `nginx -t` on `deploy/web-nginx.conf` | `syntax is ok`, `test is successful` |
| `sh -n` on every script in `scripts/` | clean |
| Full stack up (postgres → migrate → api → web → caddy), production env | `migrations complete: 2 applied, 2 total`; `application role password updated`; api `Up (healthy)`; log `"ownerCredentials":"absent","demoTooling":"absent"` |
| API connects as `pamoja_app` | `pg_stat_activity`: only `pamoja_app` from the API container |
| `/health`, `/ready` through the edge | `200 ok`, `200 ready` |
| Demo tooling, `PAMOJA_DEMO_TOOLING` empty | `purge-demo 404`, `system-checks 404`, `demo-guide 404` |
| Demo tooling, `PAMOJA_DEMO_TOOLING=on` | `demo-guide 401` anonymous, `200` for a signed-in admin with 13 demo accounts; `purge-demo`/`system-checks` still `404` |
| `scripts/smoke-test.sh` against the edge, both postures | `smoke test: PASS` (TLS check skipped with `SMOKE_ALLOW_HTTP=1`) |
| Synthetic seed on a production-mode database | one-off override → `demo seed complete`; 5 demo groups, 14 users |
| `scripts/create-admin.sh` path (`node src/createAdmin.js`) | `Created user …`, `Granted it_admin (Org)`, sign-in 200 |
| `scripts/production-preflight.sh` mechanics | correctly FAILS a database that holds demo rows; seed refusal, owner-credential absence, health and ready all PASS |
| TLS certificate issued for the real domain | **NOT VERIFIED HERE** — needs DNS and port 80 on the real server |
| `harden-server.sh`, systemd timers, Object Storage upload, restore drill | **NOT VERIFIED HERE** — need the real server and the real bucket |
| Production deploy | **NOT VERIFIED HERE** — not executed in this pass |

---

## 8. Command reference

```sh
# deploy the current branch tip (backup → build → migrate → up → health → smoke)
sudo -u pamoja-deploy /opt/pamoja/app/scripts/deploy.sh
DRY_RUN=1 /opt/pamoja/app/scripts/deploy.sh          # print the plan only

# status of everything
docker compose --project-name pamoja --env-file /opt/pamoja/.env.production \
  -f /opt/pamoja/app/deploy/docker-compose.prod.yml ps

# smoke test (staging)
BASE_URL=https://pamoja-staging.duckdns.org bash /opt/pamoja/app/scripts/smoke-test.sh

# production-only gate
BASE_URL=https://<domain> sh /opt/pamoja/app/scripts/production-preflight.sh

# monitoring and backups by hand
BASE_URL=https://pamoja-staging.duckdns.org sh /opt/pamoja/app/scripts/monitor.sh
sudo systemctl start pamoja-backup.service

# audit chain
docker compose --project-name pamoja --env-file /opt/pamoja/.env.production \
  -f /opt/pamoja/app/deploy/docker-compose.prod.yml exec -T api \
  node scripts/audit-chain.mjs

# send one test message through the configured SMS/HTTP provider
TEST_DELIVERY_TO=+2567… sh /opt/pamoja/app/scripts/test-delivery.sh
```
