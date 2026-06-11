# AWS DEV-like local deployment rehearsal

This runbook rehearses the AWS DEV deployment shape on a local Docker host
before publishing the service to a company AWS server.

The rehearsal intentionally uses the same Compose stack that production-like
deployments use:

- one backend container running `/app/ops-intelligence --role=all --addr=:8080`
- one Next.js standalone web container
- bundled MySQL in the default Compose stack for local rehearsal
- Nacos as the production-like config source when available
- AWS Secrets Manager for DB/API/token secrets when available
- same-origin reverse proxy for external browser access

The bundled MySQL service is part of the default Compose stack. Rehearsing
against an external RDS/MySQL instance requires a separate Compose override or
operator-specific adaptation; do not run the default bundled MySQL against the
same schema as another stack.

## Guardrails

- Keep **one backend replica**. Do not run `docker compose up --scale backend=2`.
- Do not run multiple Compose stacks against the same MySQL/RDS schema.
- Do not introduce Redis for this deployment shape.
- Do not commit production DSNs, AWS credentials, Lark production webhooks, or
  decision/callback secrets into tracked YAML, docs, or `.env` templates.
- Keep `OPS_INTELLIGENCE_MYSQL_DSN` empty in Nacos/AWS SM mode unless doing a
  short-lived override. An explicit DSN env overrides the Nacos `Database` block.
- In `deploy/docker-compose.yaml`, `OPS_INTELLIGENCE_MYSQL_DSN` preserves an
  explicitly empty value. If the variable is omitted entirely, local file-backed
  Compose uses the bundled MySQL default DSN.
- `NEXT_PUBLIC_API_BASE` is a **build-time** browser value. Rebuild the web
  image after changing it.
- `/metrics` is a real Prometheus endpoint on the backend port. Restrict it to
  internal or Prometheus networks at the reverse proxy.

## Files involved

| File | Purpose |
|---|---|
| `deploy/docker-compose.yaml` | Main local/AWS DEV-like Compose stack. |
| `deploy/.env.production.template` | Production-like env template; copy to `deploy/.env`. |
| `deploy/nginx/ops-intelligence.conf` | Host-level same-origin reverse-proxy sample. |
| `config/nacos.yaml` | Nacos bootstrap defaults and env override reference. |
| `config/edgex-ops-intelligence.yaml` | Local file-mode main config and Nacos payload shape reference. |
| `config/aws-secrets-manager.example.json` | Example AWS SM JSON structure without real values. |
| `backend/schema/ops-intelligence-schema.sql` | Fresh empty DB schema reference. |

## Mode A: local file-backed rehearsal

Use this when you want to validate the image build, container health checks, web
health route, backend health/readiness, and MySQL bootstrap without requiring a
reachable company Nacos or AWS Secrets Manager.

```bash
cd deploy
make up
make ps
make smoke
make smoke-web
make smoke-readiness
```

Expected behavior:

- `backend` starts in local config mode by default.
- Compose waits for MySQL health before starting the backend.
- Compose waits for backend `/api/health` before starting the web container.
- `web` exposes `/healthz` as container liveness.
- `/api/readiness` may return `503` while the first collector cycle warms up;
  this should not restart either container.
- Backend startup executes the compiled fresh schema bootstrap plus guarded
  post-init repairs. It does not automatically scan or run
  `backend/migrations/*.sql`.

Useful direct checks:

```bash
curl -fsS http://127.0.0.1:8080/api/health
curl -fsS http://127.0.0.1:8080/metrics
curl -fsS http://127.0.0.1:3001/healthz
docker compose --project-name edgex-ops-intelligence ps
```

## Mode B: production-like Nacos/AWS SM rehearsal

Use this when you can reach the company Nacos endpoint and the AWS account/role
or local AWS credential chain can read the target Secrets Manager secret.

1. Create a private env file:

   ```bash
   cd deploy
   cp .env.production.template .env
   ```

2. Edit `deploy/.env`:

   ```dotenv
   USE_LOCAL_CONFIG=false
   OPS_INTELLIGENCE_CONFIG_SOURCE=nacos
   NACOS_CONFIG_SERVER_ADDR=<nacos-host>:8848
   NACOS_CONFIG_NAMESPACE=<dev-namespace>
   NACOS_CONFIG_GROUP=DEFAULT_GROUP
   NACOS_CONFIG_NAME=edgex-ops-intelligence.yaml
   NACOS_CONFIG_USERNAME=<optional>
   NACOS_CONFIG_PASSWORD=<optional>

   AWS_SM_ENABLE=true
   AWS_SM_REGION=ap-southeast-1
   AWS_SM_SECRET_ID=<dev-secret-id>

   # Keep empty so Nacos Database + AWS SM-resolved Aws.DB* values win.
   # docker-compose.yaml preserves this explicit empty value. A non-empty DSN
   # overrides the Nacos/AWS-SM-derived Database block.
   OPS_INTELLIGENCE_MYSQL_DSN=
   ```

3. Ensure the Nacos `edgex-ops-intelligence.yaml` payload uses the bridge-style
   top-level layout:

   ```yaml
   Database:
     Name: edgex_ops_intelligence
     Addr: ""
     UserName: ""
     Password: ""

   Aws:
     DBAddr: OPS_DB_ADDR
     DBUser: OPS_DB_USER
     DBPass: OPS_DB_PASS
     CoinGeckoAPIKey: OPS_COINGECKO_API_KEY
     ListingCallbackSecret: OPS_LISTING_CALLBACK_SECRET
     ActivityDecisionToken: OPS_ACTIVITY_DECISION_TOKEN

   Alert:
     AppName: edgex-ops-intelligence
     Push:
       Listing: "<DEV listing webhook>"
       Activity: "<DEV activity webhook>"
       Liquidity: ""

   Runtime: {}
   Catalog: {}
   Clients: {}
   ```

   `Aws.DBAddr`, `Aws.DBUser`, and `Aws.DBPass` resolve the connection host,
   username, and password from AWS Secrets Manager. They do not resolve the
   database name; `Database.Name` selects the schema unless a full explicit DSN
   is provided.

   The DEV business webhooks may reuse current DEV values. Production webhook
   URLs must stay in Nacos/private config and must not be copied into tracked
   docs or templates.

   Business Lark webhook URLs are not read from AWS Secrets Manager in this
   deployment shape. Use `Alert.Push.Listing`, `Alert.Push.Activity`, and
   `Alert.Push.Liquidity` in the selected YAML/Nacos payload. `Alert.Webhooks.*`
   is a legacy compatibility input only.

   The backend fetches the Nacos payload once during startup. `ListenConfig()`
   exists in code, but runtime hot reload and Nacos naming/service registration
   are not wired in this deployment shape. Restart the backend after changing
   Nacos config.

4. Provide AWS credentials through the normal AWS SDK credential chain:

   - EC2/ECS instance profile in AWS DEV, preferred.
   - Local `AWS_PROFILE`/SSO or temporary env credentials for local rehearsal.

   Do not write AWS access keys into `deploy/.env`.

5. Validate Compose rendering before booting:

   ```bash
   docker compose --project-name edgex-ops-intelligence config --quiet
   docker compose --project-name edgex-ops-intelligence config | rg 'OPS_INTELLIGENCE_CONFIG_SOURCE|AWS_SM_ENABLE|NEXT_PUBLIC_API_BASE'
   ```

6. Rebuild and run:

   ```bash
   make up
   make ps
   make smoke
   make smoke-web
   make smoke-readiness
   ```

## Same-origin reverse proxy rehearsal

For a public AWS host, expose only the reverse proxy and keep Compose service
ports bound to `127.0.0.1` by default.

Recommended routing:

```text
https://ops.example.com/        -> 127.0.0.1:3001 (web)
https://ops.example.com/api/*   -> 127.0.0.1:8080 (backend)
https://ops.example.com/metrics -> 127.0.0.1:8080 (backend, restricted)
```

For this topology:

```dotenv
NEXT_PUBLIC_API_BASE=
SERVER_API_BASE=http://backend:8080
WEB_PUBLISH_HOST=127.0.0.1
BACKEND_PUBLISH_HOST=127.0.0.1
```

Then rebuild web so the empty `NEXT_PUBLIC_API_BASE` is baked into the browser
bundle:

```bash
cd deploy
make up
```

Use `deploy/nginx/ops-intelligence.conf` as a starting point for a host-level
Nginx config. Adapt TLS, domain names, and `/metrics` allowlists before using it
on an AWS server.

## Database bootstrap notes

- Fresh local MySQL volumes are initialized by the Compose MySQL service.
- Backend startup applies the compiled fresh schema and guarded post-init checks.
- `backend/schema/ops-intelligence-schema.sql` is a reference/manual bootstrap
  file for empty schemas only.
- Existing/restored DBs need an audited migration plan; do not assume
  `CREATE TABLE IF NOT EXISTS` will repair old columns or indexes.

## Cleanup

```bash
cd deploy
make down
```

`make down` keeps the `mysql-data` volume. Remove volumes only when you
explicitly want to discard rehearsal data.
