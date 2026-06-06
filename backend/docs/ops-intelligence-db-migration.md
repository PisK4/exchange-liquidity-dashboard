# EdgeX Ops Intelligence Database Migration

This document covers the hard-cutover database rename from the former local/deployment database name to the new EdgeX Ops Intelligence database:

```text
edgex_ops_intelligence
```

The application now uses the new technical identifiers by default:

- environment variable: `OPS_INTELLIGENCE_MYSQL_DSN`
- default Docker database: `edgex_ops_intelligence`
- backend binary: `/app/ops-intelligence`
- config file: `config/edgex-ops-intelligence.yaml`

## Why this migration is manual

Changing `MYSQL_DATABASE` in Docker Compose does not create or copy a database inside an existing MySQL volume. The official MySQL image only initializes `MYSQL_DATABASE` when the data directory is empty.

If a host already has a populated MySQL volume, you must explicitly dump the old database and restore it into `edgex_ops_intelligence`.

## 1. Back up the old database

If the old deployment is still running, export the previous database explicitly by overriding `MYSQL_DATABASE`:

```bash
cd deploy
MYSQL_DATABASE=edgex_dashboard make backup-mysql
```

This writes a dump such as:

```text
deploy/backups/20260606T000000Z.sql.gz
```

If the old Compose project name is no longer active, run `mysqldump` against the old MySQL instance directly and keep the output as a `.sql.gz` archive.

## 2. Start the new stack

Use the hard-cutover Compose project and the new database name:

```bash
cd deploy
MYSQL_DATABASE=edgex_ops_intelligence \
OPS_INTELLIGENCE_MYSQL_DSN='root:root@tcp(mysql:3306)/edgex_ops_intelligence?parseTime=true' \
make up
```

On a fresh MySQL volume, the new database is initialized automatically. On an existing MySQL volume, create it manually before restore:

```bash
docker compose --project-name edgex-ops-intelligence exec -T mysql \
  mysql -uroot -p"${MYSQL_ROOT_PASSWORD:-root}" \
  -e "CREATE DATABASE IF NOT EXISTS edgex_ops_intelligence CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
```

## 3. Restore the dump into the new database

```bash
cd deploy
MYSQL_DATABASE=edgex_ops_intelligence make restore-mysql FILE=backups/<dump-file>.sql.gz
```

For example:

```bash
MYSQL_DATABASE=edgex_ops_intelligence make restore-mysql FILE=backups/20260606T000000Z.sql.gz
```

## 4. Verify the service

Run the liveness and readiness probes:

```bash
make smoke
make smoke-readiness
```

Then verify row counts via the health payload:

```bash
curl -fsS http://127.0.0.1:8080/api/health | jq '.deps.mysql.snapshot_row_counts'
```

## 5. Rollback option

The hard-cutover code no longer reads legacy environment variable names, but the DSN can still point to any reachable MySQL schema. For emergency rollback to the old physical database, set:

```bash
OPS_INTELLIGENCE_MYSQL_DSN='root:root@tcp(mysql:3306)/edgex_dashboard?parseTime=true'
```

This is only a rollback bridge. The recommended target database name remains `edgex_ops_intelligence`.

