# Activity Agent current implementation contract

This file is the repo-local contract for the current Activity Agent runtime.
Use it before relying on upstream research notes under
`architecture/方案设计/EdgeX运营/活动/`.

## 1. Source of truth and scope

Current implementation facts live in:

- `backend/internal/activity/*` — ingestion, parser, producer, delivery,
  repository, decision tokens, and Lark card rendering.
- `backend/internal/api/activity.go` — Activity REST API surfaces.
- `backend/cmd/ops-intelligence/main.go` — worker wiring, HTTP clients,
  webhook and decision-token resolution.
- `backend/internal/config/config.go` and `config/edgex-ops-intelligence.yaml`
  — runtime schema and default source matrix.
- `backend/docs/runbook.md` — operational checks and deploy guidance.

Upstream Activity documents are business intent, source evidence, and design
history. They are not a guarantee that every researched source group, parser,
or automation rule is enabled in the current runtime.

## 2. Current runtime source matrix

The default runtime seeds one Activity source per platform:

| Platform | Source group | Fetch mode | Notes |
|---|---|---|---|
| `binance` | `cms_article_list` | `http_direct` | List seed only; detailed CMS/Launchpool parsers from upstream research are not all runtime guarantees. |
| `okx` | `help_announcement` | `http_direct` | Latest announcement feed seed. |
| `bingx` | `openapi_notice` | `http_direct_json` | OpenAPI notice seed. |
| `gate` | `launchpool_project_list` | `utls_proxy_json` | Proxy-oriented Launchpool project list seed. |
| `mexc` | `latest_events` | `utls_proxy_html` | Latest events page seed. |
| `bybit` | `announcements_api` | `http_direct_json` | Announcement API seed; do not assume every SSR detail parser is enabled. |
| `bitget` | `support_ongoing_section` | `utls_html` | Support section seed; browser-context research remains evidence. |
| `hyperliquid` | `cloudfront_entries` | `http_direct_json` | CloudFront entries seed. |
| `lighter` | `incentive_docs` | `markdown_doc` | Markdown docs seed. |

Any broader exchange-source matrix in upstream research is an evidence backlog
unless it is also present in runtime config and parser code.

## 3. Pipeline and delivery gates

The current Activity path is:

```text
source fetch -> raw evidence -> parser -> activity event
  -> producer -> delivery outbox -> Lark delivery attempt
  -> review / decision / redrive APIs
```

`auto_push_enabled: true` is not sufficient by itself to guarantee a Lark card.
Delivery is still gated by:

- source policy and source health;
- producer suppression for historical/bootstrap/missing-time events;
- `needs_human_review` and `review_status`;
- webhook availability;
- decision-token secret availability for interactive decision buttons;
- outbox status, retry state, and next-attempt time.

When webhook configuration is missing, rows may be collected or written as a
disabled delivery state instead of being posted to Lark. When decision-token
secret configuration is missing, interactive decision-card delivery must not be
treated as fully enabled.

## 4. Decision token and webhook configuration

Activity delivery supports direct YAML/Nacos values and environment-variable
indirection:

| Purpose | Direct field | Env indirection field | Common env name |
|---|---|---|---|
| Lark webhook URL | `Runtime.activity_agent.delivery.webhook_url` or `Alert.Webhooks.Activity` | `Runtime.activity_agent.delivery.webhook_url_env` | `LARK_ACTIVITY_WEBHOOK_URL` |
| Decision-token secret | `Runtime.activity_agent.decision_token.secret` | `Runtime.activity_agent.decision_token.secret_env` | `ACTIVITY_DECISION_TOKEN_SECRET` |

Direct YAML/Nacos values win when non-empty. Do not commit real webhook URLs or
decision-token secrets to tracked Markdown, templates, or sample YAML.

## 5. API and DB verification surfaces

Relevant APIs:

- `GET /api/activity/events`
- `GET /api/activity/events/{id}`
- `GET /api/activity/source-health`
- `GET /api/activity/deliveries`
- `POST /api/activity/review/{id}`
- `GET /api/activity/decision/{id}`
- `POST /api/activity/deliveries/{id}/redrive`

Relevant tables:

- `t_activity_source_state`
- `t_activity_raw_evidence`
- `t_activity_event`
- `t_activity_delivery_outbox`
- `t_activity_delivery_attempt`
- `t_activity_worker_lease`

For Lark delivery, webhook HTTP 200 is not enough. Verify that the outbox row is
`sent`, that a delivery attempt exists, and that the Lark response body reports
success (for example `code=0`).

## 6. Historical-design boundaries

Upstream documents that describe fully automated operation, every platform
detail parser, Banner/popup surfaces, or digest-style recommendations are
planning and evidence material unless this repo-local contract and code say the
behavior is current. The current runtime includes review/decision safety valves
and delivery auditing; do not bypass those controls to satisfy historical
"fully automated" plans.
