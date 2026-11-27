# Run Pamoja locally on Windows

Everything runs in Docker; you do not install Node, PostgreSQL or Go on Windows.

## 1. Prerequisites

| Need | Why | Check |
|---|---|---|
| Windows 10/11 with WSL2 | Docker Desktop's Linux engine | `wsl --status` |
| Docker Desktop 4.x (Compose v2) | Runs the database, API, seed and web app | `docker compose version` |
| Git for Windows | Clone the repository | `git --version` |
| A browser | Opens the app | — |

In Docker Desktop: **Settings → General → Use the WSL 2 based engine** (on) and
**Settings → Resources → WSL integration** enabled for your distribution.

## 2. Get the code

```powershell
git clone <repository-url> pamoja
cd pamoja
```

## 3. Create the environment file

Create `.env` in the repository root (it is git-ignored). Minimum for a demo:

```ini
POSTGRES_PASSWORD=choose-a-long-database-password
APP_DB_PASSWORD=choose-a-different-app-password
PAMOJA_MASTER_KEY=replace-with-32-bytes-of-base64
NODE_ENV=development
```

Generate the key material on Windows PowerShell:

```powershell
[Convert]::ToBase64String((1..32 | ForEach-Object { Get-Random -Maximum 256 }))
```

For a production-like run also set `NODE_ENV=production` and **do not** start the
`seed` service (see §6).

## 4. Start the app

```powershell
docker compose -f deploy/docker-compose.app.yml up --build
```

First start takes a few minutes (npm install, schema, demo seed). Wait for
`pamoja api listening on 8080` and the Vite `ready` line.

| URL | What |
|---|---|
| http://localhost:3000/ | The Pamoja web app |
| http://localhost:3000/api/health | API health (through the dev proxy) |
| http://localhost:8080/api/health | API health directly (published for local use) |
| localhost:5432 | PostgreSQL (`pamoja` owner, `pamoja_app` application role) |

The API is only reachable through the Vite proxy at `/api` in the browser, so
session cookies stay same-origin.

## 5. Create the first administrator (securely)

There is **no default account and no default password**. Create the first
organisation administrator out of band:

```powershell
docker compose -f deploy/docker-compose.app.yml exec api `
  sh -lc "ADMIN_EMAIL=you@yopsa.co.ug ADMIN_PASSWORD='A-long-passphrase-9!' ADMIN_NAME='Your Name' npm run create-admin"
```

The password must be at least 12 characters with upper case, lower case, a digit
and a symbol. Only a scrypt hash is stored. The grant is written to the
append-only audit log as `bootstrap.first_admin`.

Sign in, then use **Admin → Roles** to grant further roles. IT Admin proposes a
role; a second administrator must approve it, and both steps need a one-time
code.

## 6. Demo data and production safety

- Demo accounts exist **only** when the demo seed has run. They are listed on
  **Admin → Demo guide**, never on the login page.
- `server/src/seed.js` refuses to run when `NODE_ENV=production` or
  `APP_ENV=production`.
- The demo password default (`PamojaDemo#2026`) is only used by the seed in a
  non-production environment; set `DEMO_PASSWORD` to change it.
- Before go-live: **Admin → Demo guide → Purge demo data** (typed confirmation +
  one-time code). It deletes only `isDemo` rows, disables demo accounts and keeps
  every audit row.
- Then confirm there are no demo users:
  `docker compose -f deploy/docker-compose.app.yml exec db psql -U pamoja -d pamoja -c "select email from users where is_demo"` → 0 rows.

For a production-like stack, start without the seed:

```powershell
docker compose -f deploy/docker-compose.app.yml up --build db migrate api web
```

## 7. Prove it works

```powershell
# In-app: Admin → System checks (25 checks) and Admin → Scheduled jobs
# Negative tests (9 cases, real HTTP):
docker compose -f deploy/docker-compose.app.yml run --rm --no-deps -e API_BASE=http://api:8080/api api node scripts/negative-tests.mjs

# Database proofs as the application role (not owner, not superuser):
docker compose -f deploy/docker-compose.app.yml exec -T db psql -U pamoja_app -d pamoja -f /repo/scripts/db-proof.sql
```

## 8. Stop and reset

```powershell
docker compose -f deploy/docker-compose.app.yml down          # stop, keep data
docker compose -f deploy/docker-compose.app.yml down -v       # stop and delete the database volume
```

## Troubleshooting

| Symptom | Fix |
|---|---|
| `port is already allocated` | Another PostgreSQL/Node is using 3000 or 5432; stop it or change the host port in the compose file |
| `password authentication failed for user "pamoja_app"` | `migrate` did not finish; run `docker compose -f deploy/docker-compose.app.yml logs migrate` |
| Blank page at :3000 | Wait for the web container's `npm ci` to finish; check `... logs web` |
| `Refusing to load demo data in production` | Expected with `NODE_ENV=production`; create the first admin instead |
