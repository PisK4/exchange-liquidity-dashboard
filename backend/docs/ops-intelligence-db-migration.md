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

## Schema bootstrap vs migration scripts

This document covers the physical database-name hard cutover. Schema bootstrap
and historical schema upgrades are related but separate concerns.

Current backend startup behavior:

- The process calls `collector.ApplyMigrations(db)` after opening MySQL.
- `collector.ApplyMigrations` executes the compiled `initSchemaSQL` string in
  `backend/internal/collector/mysql_store.go`, then runs `applySchemaPostInit`.
- It does **not** scan, sort, or execute `backend/migrations/*.sql`.
- `backend/schema/ops-intelligence-schema.sql` is a consolidated fresh-database
  schema snapshot for operator review/manual bootstrap; it is not read by the
  runtime unless the code is changed to embed or load it.

Fresh database behavior:

- A fresh database can be initialized by starting the backend with
  `OPS_INTELLIGENCE_MYSQL_DSN` pointing at an existing empty schema.
- Alternatively, operators may manually apply
  `backend/schema/ops-intelligence-schema.sql` to an explicitly selected empty
  schema for review/rehearsal.
- The database/schema itself must still exist first. Docker creates it only on
  an empty MySQL data directory through `MYSQL_DATABASE`; otherwise use
  `CREATE DATABASE IF NOT EXISTS ...` manually.

Restored or existing database behavior:

- `CREATE TABLE IF NOT EXISTS` will not widen columns, add missing columns, or
  add indexes to tables that already exist.
- `applySchemaPostInit` covers only the guarded repairs encoded in the current
  binary, such as Listing metric fallback indexes, Listing signal fingerprint
  width, and Activity source/bootstrap timestamp columns.
- For production upgrades from historical dumps, audit
  `backend/migrations/*.sql` and explicitly apply the missing chronological
  migrations after a backup. `make -C backend migrate-up` and
  `make -C backend migrate-down` print operator command plans only; they do not
  execute SQL.

Before using the Makefile migration plan as an operator checklist, compare it
with the committed migration files:

```bash
ls backend/migrations/*.up.sql | sort
ls backend/migrations/*.down.sql | sort -r
make -C backend migrate-up
make -C backend migrate-down
```

Schema verification examples:

```sql
SELECT TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX, COLUMN_NAME
  FROM INFORMATION_SCHEMA.STATISTICS
 WHERE TABLE_SCHEMA = DATABASE()
   AND INDEX_NAME IN ('idx_orderbook_canonical_surface_tier_latest',
                      'idx_symbol_volume_canonical_surface_latest')
 ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX;
```

```sql
SELECT TABLE_NAME, COLUMN_NAME
  FROM INFORMATION_SCHEMA.COLUMNS
 WHERE TABLE_SCHEMA = DATABASE()
   AND (
     (TABLE_NAME = 't_activity_source_state'
      AND COLUMN_NAME IN ('last_checked_at','last_success_at','producer_watermark_at','bootstrap_completed_at'))
     OR
     (TABLE_NAME = 't_activity_event'
      AND COLUMN_NAME IN ('source_observed_at','source_producer_watermark_at','source_bootstrap_completed_at'))
   )
 ORDER BY TABLE_NAME, COLUMN_NAME;
```

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
