# Deployment assets

| File | Purpose |
|---|---|
| `docker-compose.dev.yml` | Local developer stack: PostgreSQL 16, baseline migration, API on `localhost:8080`, worker |
| `.env.example` | Every environment variable, with the ones the P0 build reads marked clearly |
| `docker-compose.prod.yml` | The deployed stack: Caddy edge, nginx SPA, API, PostgreSQL, one-shot `migrate` and `backup` |
| `.env.production.example` | The environment file the operator creates at `/opt/pamoja/.env.production` (empty values only) |
| `Caddyfile` | TLS, HTTP→HTTPS, security headers, path blocks, `/api` and `/health` proxying |
| `api.Dockerfile`, `web.Dockerfile`, `caddy.Dockerfile`, `backup.Dockerfile` | Release images, pinned, unprivileged |
| `web-nginx.conf` | SPA routing and cache rules for the web image |
| `systemd/` | Backup and monitoring timers |
| `terraform/` | Illustrative Vultr instance definition — **never applied**, documentation only |

The deployment runbook is [docs/DEPLOYMENT.md](../docs/DEPLOYMENT.md): staging
steps S1–S11, production steps P1–P8, rollback, rotation and incident response.
Operational scripts live in [scripts/](../scripts).

## Local quick start

```sh
docker compose -f deploy/docker-compose.dev.yml up --build
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
```

The API connects to PostgreSQL, waits for it, and serves the status page at
`http://localhost:8080/`.

## Sandbox preview

The Base44 preview environment has its own compose file, kept in the environment
configuration (not in this repository) because it carries sandbox-specific
paths and the preview port. It runs the same API from the working tree with live
reload.
