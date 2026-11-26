# Deployment assets

| File | Purpose |
|---|---|
| `docker-compose.dev.yml` | Local developer stack: PostgreSQL 16, baseline migration, API on `localhost:8080`, worker |
| `.env.example` | Every environment variable, with the ones the P0 build reads marked clearly |

Production assets — `docker-compose.prod.yml`, `Caddyfile`, backup and restore
scripts, optional Kubernetes manifests — land in P10 together with the
[OPERATIONS_RUNBOOK](../docs/README.md). They are deliberately not scaffolded
here: a production compose that has never been booted would be a false promise.

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
