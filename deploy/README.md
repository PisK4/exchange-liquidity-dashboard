# EdgeX Ops Intelligence Deploy Guide

This directory contains the Docker Compose deployment entry points for the
current AWS DEV-like deployment shape. Read this file before using historical
plans under `docs/plan/`.

## Files

| File | Purpose |
|---|---|
| `docker-compose.yaml` | Main Compose stack: bundled MySQL, one `--role=all` backend, and one Next.js standalone web container. |
| `.env.production.template` | Operator-facing production-like env template. Copy to `deploy/.env` and edit private values there. |
| `aws-dev-local-rehearsal.md` | Step-by-step local rehearsal for the AWS DEV deployment shape. |
| `nginx/ops-intelligence.conf` | Host-level same-origin Nginx sample for `/`, `/api/*`, and restricted `/metrics`. |
| `Makefile` | Wrapper around Docker Compose plus smoke, backup, restore, and image checks. |
| `Dockerfile.backend` | Go backend image build. |
| `Dockerfile.web` | Next.js standalone web image build. |

## Supported phase-1 topology

- One backend replica only.
- Backend command: `/app/ops-intelligence --role=all --addr=:8080`.
- One Next.js standalone web container.
- Bundled MySQL is part of the default Compose stack for local rehearsal.
- Redis is not part of this deployment shape.
- External RDS/MySQL rehearsal requires an operator-specific Compose override;
  do not run the default bundled MySQL against the same schema as another stack.

Do not scale the backend with `docker compose up --scale backend=2`, and do not
run multiple Compose stacks against the same MySQL/RDS schema. Multi-replica
deployment requires a separate design for role split, leases, and idempotency.

## Configuration and secrets

Local developer Compose defaults to file-backed config:

```dotenv
USE_LOCAL_CONFIG=true
OPS_INTELLIGENCE_CONFIG_SOURCE=local
```

Production-like AWS DEV mode should use Nacos and AWS Secrets Manager:

```dotenv
USE_LOCAL_CONFIG=false
OPS_INTELLIGENCE_CONFIG_SOURCE=nacos
AWS_SM_ENABLE=true
```

The Nacos payload uses the same bridge-style main YAML layout as
`config/edgex-ops-intelligence.yaml`. AWS Secrets Manager currently resolves DB
addr/user/pass, CoinGecko API key, Listing callback secret, and Activity
decision token. It does not resolve Lark business webhook URLs; configure those
through `Alert.Push.Listing`, `Alert.Push.Activity`, and `Alert.Push.Liquidity`
in the selected YAML/Nacos config.

`OPS_INTELLIGENCE_MYSQL_DSN` has highest priority when non-empty. In
`docker-compose.yaml`, an explicitly empty value is preserved so the backend can
compose the DSN from Nacos `Database` plus AWS-SM-resolved DB fields. If the
variable is omitted entirely, local file-backed Compose uses the bundled MySQL
default DSN.

## Frontend deployment

`NEXT_PUBLIC_API_BASE` is a build-time browser value. Leave it empty for
same-origin reverse-proxy deployments so browser requests use relative `/api/*`
paths, then rebuild the web image. `SERVER_API_BASE` is runtime-only for
Next.js server-side requests inside the Compose network and can remain
`http://backend:8080`.

The web container exposes `/healthz` for liveness only. It does not validate
backend readiness.

## Health, readiness, and metrics

- Backend liveness: `/api/health`.
- Backend traffic readiness: `/api/readiness`.
- Web liveness: `/healthz`.
- Prometheus scrape endpoint: `/metrics` on the backend port.

Docker health checks target liveness endpoints, not readiness. Restrict
`/metrics` to trusted monitoring networks at the reverse proxy.

## Fresh DB vs existing DB

Fresh databases can be initialized by backend startup or manually reviewed with
`backend/schema/ops-intelligence-schema.sql`. Runtime startup does not scan or
execute `backend/migrations/*.sql`; those files are chronological manual/audit
scripts for existing database upgrades.

For old database-name cutovers or restored databases, read
`backend/docs/ops-intelligence-db-migration.md` before changing DSNs.

## Common commands

```bash
cd deploy
cp .env.production.template .env  # for production-like private settings
make up
make ps
make smoke
make smoke-web
make smoke-readiness
```

For the complete rehearsal procedure, read `aws-dev-local-rehearsal.md`. For
operational troubleshooting, read `../backend/docs/runbook.md`.
