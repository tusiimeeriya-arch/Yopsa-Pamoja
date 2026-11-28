# Owner-role database access

The API connects as `pamoja_app`: not the owner, not a superuser, no
`BYPASSRLS`, so PostgreSQL row-level security is enforced by the database
itself. The owner role (`ADMIN_DATABASE_URL`) exists for setup and tooling only.

## Where the owner role is used

| Place | What it does |
|---|---|
| `server/src/owner.js` | The only module that reads `ADMIN_DATABASE_URL`. Exports `ownerConfigured`, `adminPool` and `adminTx()`. There is no fallback to `DATABASE_URL`: without the variable no owner pool exists and `adminTx()` throws. |
| `server/src/seed.js` | Loads the demo dataset. Refuses to run when `NODE_ENV`/`APP_ENV` is `production`. |
| `server/src/createAdmin.js` | Creates the first organisation administrator out of band. |
| `server/src/services/checks.js` | Self-test cleanup of the five protected identity tables (users, persons, groups, memberships, role assignments). |
| `server/src/routes/admin.js` | `POST /api/admin/purge-demo` and `POST /api/admin/system-checks`. Both are registered inside `if (!isProduction())`, so the production API does not mount them. |
| `server/scripts/purge-demo.mjs` | CLI demo purge, `--confirm "PURGE DEMO DATA"`. |
| `server/scripts/system-checks.mjs` | CLI self-tests. Runs the checks in-process against the database, through `quiet-session.mjs`. |
| `server/scripts/negative-tests.mjs` | Creates and removes its own fixtures as the owner role; its direct database probes use `quiet-session.mjs`. |
| `server/scripts/quiet-session.mjs` | Builds the check connection string: owner URL, `role=pamoja_app`, `log_min_messages=fatal`. See below. |
| `deploy/docker-compose.app.yml` | `seed` only. The `api` service is not given the owner URL. |

Nothing else in the repository opens an owner connection.

## Production guarantees

1. **The production API process holds no owner credentials.**
   `server/src/index.js` refuses to start when `NODE_ENV` or `APP_ENV` is
   `production` and `ADMIN_DATABASE_URL` is set, and it logs which state it
   started in:

   ```
   {"level":"info","message":"pamoja api listening on 8099","ownerCredentials":"absent","demoTooling":"absent"}
   ```

   ```
   {"level":"error","message":"refusing to start","reason":"ADMIN_DATABASE_URL must not be set for the production API; owner access is CLI-only"}
   exit=1
   ```

2. **The purge and self-test endpoints are absent in production.** With an
   authenticated organisation administrator, the production process answers
   `404 NOT_FOUND` on both paths; the same calls against a development process
   reach the routes.

   ```
   PRODUCTION POST /api/admin/purge-demo    -> 404 {"error":{"code":"NOT_FOUND","message":"Not found."}}
   PRODUCTION POST /api/admin/system-checks -> 404 {"error":{"code":"NOT_FOUND","message":"Not found."}}
   PRODUCTION /api/auth/me                  -> 200
   DEV        POST /api/admin/purge-demo    -> 403 {"error":{"code":"OTP_REQUIRED",...}}
   ```

## Keeping the database log readable

The check suites assert that PostgreSQL refuses things (a missing grant, a
locked payment entry, an immutable `is_demo`, a duplicate key). PostgreSQL logs
every `ERROR` server-side, so a run used to leave one block per expected refusal
in the database log, indistinguishable from a real fault.

`server/scripts/quiet-session.mjs` opens the suite's connection with
`options=-c log_min_messages=fatal -c role=pamoja_app`. The SQL still runs as
`pamoja_app` — same grants, same row-level security, `SET log_min_messages`
still refused — but the session does not log its own rejections. The caller
still receives them, so a guard that stops firing still fails the run. The API
process never loads this module and keeps full server-side error logging; the
development-only `POST /api/admin/system-checks` route runs on the API's own
pool and therefore still logs its expected refusals.

## Running the owner-role tooling

Run these as a one-off container or from a shell that has the owner URL; never
with the API's environment.

```sh
ADMIN_DATABASE_URL=... node server/scripts/purge-demo.mjs --confirm "PURGE DEMO DATA"
ADMIN_DATABASE_URL=... node server/scripts/system-checks.mjs
ADMIN_DATABASE_URL=... node server/scripts/negative-tests.mjs
ADMIN_DATABASE_URL=... node server/src/createAdmin.js
```

`system-checks.mjs` and the negative tests derive their application-role
connection from `ADMIN_DATABASE_URL` (override with `CHECK_DATABASE_URL`); they
still need the owner URL for that and for their own fixtures.

`system-checks.mjs` signs no one in: it runs the checks in-process with the
identity of an active organisation administrator, so row-level security behaves
exactly as it does for the API.
