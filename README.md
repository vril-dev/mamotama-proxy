# mamotama

Coraza + CRS WAF project

[English](README.md) | [Japanese](README.ja.md)

![Admin Top](docs/images/ui-samples/01-status.png)

## Overview

`mamotama` is a lightweight yet powerful application protection stack built with Coraza WAF and OWASP Core Rule Set (CRS).

---

## About Rule Files

To comply with licensing, this repository does **not** bundle the full OWASP CRS files.
Instead, it includes a minimal bootstrappable base rule file: `data/rules/mamotama.conf`.

### Setup

Fetch and place CRS files with the following script (default: `v4.23.0`):

```bash
./scripts/install_crs.sh
```

Specify a version:

```bash
./scripts/install_crs.sh v4.23.0
```

Edit `data/rules/crs/crs-setup.conf` as needed (for example, paranoia level and anomaly score settings).

---

## Environment Variables

Use `.env` only for Docker runtime wiring.

### Docker / Local MySQL (Optional)

| Variable | Example | Description |
| --- | --- | --- |
| `MYSQL_PORT` | `13306` | Host port mapped to MySQL container `3306` (used when profile `mysql` is enabled). |
| `MYSQL_DATABASE` | `mamotama` | Initial database name created in local MySQL container. |
| `MYSQL_USER` | `mamotama` | Application user created for local MySQL container. |
| `MYSQL_PASSWORD` | `mamotama` | Password for `MYSQL_USER`. |
| `MYSQL_ROOT_PASSWORD` | `mamotama-root` | Root password for local MySQL container. |
| `MYSQL_TZ` | `UTC` | Container timezone. |

### Coraza App Config (`data/conf/config.json`)

Runtime configuration is now centralized in JSON (`edge` style).

- Docker keeps only runtime/environment wiring in `.env` (UID/GID, host ports, mysql profile vars, config path).
- Coraza app behavior (server/admin/security/storage/fp-tuner/paths) is controlled by `data/conf/config.json`.
- `data/conf/proxy.json` remains the dedicated live-updated proxy transport config.

Main blocks in `config.json`:

| Block | Purpose |
| --- | --- |
| `server` | listen address, timeout values, max header size, concurrency caps, optional built-in TLS listener |
| `runtime` | Go runtime caps (`gomaxprocs`, `memory_limit_mb`) |
| `admin` | API/UI base paths, API keys, CORS, strict/insecure toggles |
| `paths` | file locations for rules/bypass/country/rate/bot/semantic/CRS/proxy |
| `proxy` | rollback history size for `/proxy-rules:rollback` |
| `crs` | CRS enable flag |
| `fp_tuner` | provider mode/endpoint/auth/timeout/approval/audit settings |
| `storage` | backend (`file|db`), DB driver/path/dsn/retention/sync interval |

Container startup uses only:

| Variable | Example | Description |
| --- | --- | --- |
| `WAF_CONFIG_FILE` | `conf/config.json` | App config JSON path loaded at startup. |
| `WAF_LISTEN_PORT` | `9090` | Compose port/healthcheck/gotestwaf target helper. Keep aligned with `server.listen_addr` in config JSON. |

#### Optional Built-in TLS Termination

`[proxy]` can terminate HTTPS directly from `data/conf/config.json`. This is config-only; there is no admin UI editor for listener certificates.

```json
"server": {
  "listen_addr": ":9443",
  "tls": {
    "enabled": true,
    "cert_file": "/etc/mamotama/tls/fullchain.pem",
    "key_file": "/etc/mamotama/tls/privkey.pem",
    "min_version": "tls1.2",
    "redirect_http": true,
    "http_redirect_addr": ":9080"
  }
}
```

- `server.tls.enabled=false` is the default.
- Initial scope is manual certificate files only (`cert_file` / `key_file`). ACME auto-issuance is not included.
- `server.tls.redirect_http=true` starts a second plain HTTP listener that permanently redirects to HTTPS.
- `server.tls.http_redirect_addr` must be different from `server.listen_addr`.

### Admin UI

At startup, if `admin.api_key_primary` is too short or known-weak, Coraza fails to start in secure mode.
For local testing only, you can temporarily relax this with `admin.allow_insecure_defaults=true` in `config.json`.

## Host Network Hardening (L3/L4 Basics)

mamotama focuses on application-layer (L7) protection.
Large volumetric L3/L4 attacks that saturate links cannot be mitigated by mamotama alone.
For internet-exposed deployments, combine it with upstream protections such as ISP, CDN, load balancer, or scrubbing services.

The following Linux kernel settings are a host-side hardening baseline for improving resilience against SYN floods and spoofed source traffic.
They are not a substitute for upstream DDoS mitigation.

`/etc/sysctl.d/99-mamotama-network-hardening.conf`

```conf
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_max_syn_backlog = 4096
net.core.somaxconn = 4096

net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.default.send_redirects = 0

# Assumes symmetric routing. Consider 2 for asymmetric routing, multi-NIC, or tunnel setups.
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
```

Apply:

```bash
sudo sysctl --system
```

Notes:

- `rp_filter=1` can break traffic in asymmetric routing environments
- `tcp_syncookies` is a fallback for SYN flood handling and does not prevent bandwidth exhaustion
- firewall / nftables / iptables rate limits should be tuned to real traffic, not copied blindly

---

## Admin Dashboard

Admin UI is served by the Go binary at `/mamotama-ui` (embedded static assets).
You can still edit source under `web/mamotama-admin/` and rebuild assets for embedding.

![Admin Dashboard](docs/images/ui-samples/01-status.png)

### Main Screens and Features

| Path | Description |
| --- | --- |
| `/status` | WAF runtime status and configuration overview |
| `/logs` | Fetch and view WAF logs |
| `/rules` | View/edit active base rule files (`rules/mamotama.conf` etc.) |
| `/rule-sets` | Enable/disable CRS core rule files (`rules/crs/rules/*.conf`) |
| `/bypass` | View/edit bypass config directly (`waf.bypass`) |
| `/country-block` | View/edit country block config directly (`country-block.conf`) |
| `/rate-limit` | View/edit rate-limit config directly (`rate-limit.conf`) |
| `/notifications` | View/edit aggregate notification config directly (`conf/notifications.conf`) |
| `/bot-defense` | View/edit bot-defense config directly (`bot-defense.conf`) |
| `/semantic` | View/edit semantic security config directly (`semantic.conf`) |
| `/cache-rules` | Visual + raw editing for cache rules (`cache.conf`), with Validate/Save |
| `/proxy-rules` | View/validate/probe/update/rollback upstream + transport + maintenance/redirect fallback tuning (`conf/proxy.json`) |

Upstream failure response behavior:
- If `error_html_file` and `error_redirect_url` are both unset, the proxy returns the default `502 Bad Gateway` response and the browser shows a simple built-in error page.
- If `error_html_file` is set, HTML-capable clients receive that maintenance page and other clients receive plain text `503 Service Unavailable`.
- If `error_redirect_url` is set, `GET` / `HEAD` requests are redirected there and other methods receive plain text `503 Service Unavailable`.
- `error_html_file` and `error_redirect_url` are mutually exclusive; choose one per protected application.

### Screenshots

#### Dashboard
![Dashboard](docs/images/ui-samples/01-status.png)

#### Rules Editor
![Rules Editor](docs/images/ui-samples/03-rules.png)

#### Rule Sets
![Rule Sets](docs/images/ui-samples/04-rule-sets.png)

#### Bypass Rules
![Bypass Rules](docs/images/ui-samples/05-bypass-rules.png)

#### Country Block
![Country Block](docs/images/ui-samples/06-country-block.png)

#### Rate Limit
![Rate Limit](docs/images/ui-samples/07-rate-limit.png)

#### Notifications
![Notifications](docs/images/ui-samples/14-notifications.png)

#### Cache Rules
![Cache Rules](docs/images/ui-samples/11-cache-rules.png)

#### Logs
![Logs](docs/images/ui-samples/02-logs.png)

#### Bot Defense
![Bot Defense](docs/images/ui-samples/08-bot-defense.png)

#### Semantic Security
![Semantic Security](docs/images/ui-samples/09-semantic-security.png)

#### FP Tuner
![FP Tuner](docs/images/ui-samples/10-fp-tuner.png)

#### Proxy Rules
![Proxy Rules](docs/images/ui-samples/12-proxy-rules.png)

#### Settings
![Settings](docs/images/ui-samples/13-settings.png)

### Libraries

- coraza 3.3.3
- go 1.25.7
- React 19
- Vite 7
- Tailwind CSS
- react-router-dom
- ShadCN UI (Tailwind-based UI)

### Startup

```bash
make setup
make ui-build-sync
make compose-up
```

Open the embedded admin UI at `http://localhost:${CORAZA_PORT:-9090}/mamotama-ui`.
Set API key in `Settings` (`X-API-Key`) and operate via `/mamotama-api/*`.

### Make Shortcuts

```bash
make help
make build          # one-shot: web build + embed sync + go binary
make check          # go-test + ui-test + compose config checks
make smoke          # embedded UI + proxy-rules smoke checks
make ci-local       # local CI baseline (check + smoke)
make compose-down
```

#### Optional: Legacy Proxy Env Migration (`WAF_APP_URL` -> `conf/proxy.json`)

If you are migrating from older env-based proxy config, generate and validate `proxy.json` with:

```bash
./scripts/migrate_proxy_config.sh
./scripts/migrate_proxy_config.sh --check
```

By default this reads `.env` and resolves to `data/conf/proxy.json`.

#### Optional: Local MySQL Container (profile: `mysql`)

For future DB-driver validation, you can start a local MySQL container:

```bash
docker compose --profile mysql up -d mysql
```

When using MySQL for DB-backed logs/configs, set `storage.backend=db`, `storage.db_driver=mysql`, and `storage.db_dsn` in `data/conf/config.json` (for example `mamotama:mamotama@tcp(mysql:3306)/mamotama?charset=utf8mb4&parseTime=true`).

For multi-node operation, set `storage.db_sync_interval_sec` (for example `10`) so each node periodically reconciles runtime files from `config_blobs` and applies reload only when content actually changes.

Scale-out note: for multiple Coraza nodes, use a shared MySQL backend (`db + mysql`) as the standard setup. `file` and `db + sqlite` are intended for single-node or local validation use.

### WAF Regression Test (GoTestWAF)

Run the local regression test:

```bash
./scripts/run_gotestwaf.sh
```

Prerequisites:

- Docker and Docker Compose are available.
- The script automatically builds/starts `coraza`.
- Default host port is `HOST_CORAZA_PORT=19090`.
- The first run may take longer because the GoTestWAF image is pulled.

Default gate is `MIN_BLOCKED_RATIO=70`. Optional extra gates:

```bash
MIN_TRUE_NEGATIVE_PASSED_RATIO=95 MAX_FALSE_POSITIVE_RATIO=5 MAX_BYPASS_RATIO=30 ./scripts/run_gotestwaf.sh
```

Reports are written to `data/logs/gotestwaf/`:

- JSON full report: `gotestwaf-report.json`
- Markdown summary: `gotestwaf-report-summary.md`
- Key-value summary: `gotestwaf-report-summary.txt`

### Proxy Tuning Benchmark

Run preset-based benchmark against local `coraza` with concurrency:

```bash
BENCH_REQUESTS=600 WARMUP_REQUESTS=100 BENCH_CONCURRENCY=1,10,50 ./scripts/benchmark_proxy_tuning.sh
```

The script:

- launches a temporary upstream (`python3 -m http.server`)
- applies proxy presets via `/mamotama-api/proxy-rules`
- runs ApacheBench (`ab`) against `BENCH_PATH` (default: `/bench`)
- assigns isolated `X-Forwarded-For` / `X-Real-IP` per case to reduce cross-case rate-limit bias
- temporarily disables `rate-limit-rules` during benchmark by default (`BENCH_DISABLE_RATE_LIMIT=1`)
- writes markdown summary (default: `data/logs/proxy/proxy-benchmark-summary.md`)
- restores baseline proxy config at the end

Optional quality gates:

```bash
BENCH_MAX_FAIL_RATE_PCT=0.5 BENCH_MIN_RPS=300 BENCH_CONCURRENCY=10,50 BENCH_DISABLE_RATE_LIMIT=1 ./scripts/benchmark_proxy_tuning.sh
```

`BENCH_MAX_FAIL_RATE_PCT` and `BENCH_MIN_RPS` are optional. If set, the script exits with non-zero status when a row breaches the threshold.
Set `BENCH_DISABLE_RATE_LIMIT=0` when you intentionally want to include rate-limit behavior in benchmark results.

Recommended presets:

| Preset | Main knobs | Suggested use |
| --- | --- | --- |
| `balanced` | `force_http2=false`, `disable_compression=false`, `buffer_request_body=false`, `flush_interval_ms=0` | General web workloads, safe default |
| `low-latency` | `force_http2=true`, `disable_compression=true`, `buffer_request_body=false`, `flush_interval_ms=5` | API/SSE style low-latency focus |
| `buffered-guard` | `force_http2=true`, `buffer_request_body=true`, `max_response_buffer_bytes=1048576`, `flush_interval_ms=25` | Stricter buffering/control over response size |

Summary columns:

- `concurrency`: parallel clients used by `ab -c`
- `fail_rate_pct`: `(failed + non_2xx) / complete * 100`
- `avg_latency_ms`, `p95_latency_ms`, `p99_latency_ms`: latency in milliseconds
- `rps`: measured requests/sec from `ab`

### Deployment Examples

Practical example stacks are available under:

- `examples/nextjs` (Next.js frontend)
- `examples/wordpress` (WordPress + high-paranoia CRS setup)
- `examples/api-gateway` (REST API + strict rate-limit profile)

See `examples/README.md` for common setup flow.

### FP Tuner Mock Flow

You can test send/receive/apply flow without an external LLM contract:

```bash
./scripts/test_fp_tuner_mock.sh
```

Default is simulate-only apply (`SIMULATE=1`). To actually append and hot-reload:

```bash
SIMULATE=0 ./scripts/test_fp_tuner_mock.sh
```

### FP Tuner HTTP Stub Flow

You can also run HTTP provider mode with a local stub endpoint:

```bash
./scripts/test_fp_tuner_http.sh
```

What this script does:

- Starts a local temporary provider stub on `127.0.0.1:${MOCK_PROVIDER_PORT:-18091}`
- Creates a temporary app config derived from `data/conf/config.json` with `fp_tuner.mode=http` and provider endpoint override
- Starts/rebuilds `coraza` with `WAF_CONFIG_FILE=<temporary-config>`
- Sends `propose` / `apply` requests and checks response contract
- Verifies provider-bound payload is masked before external send

Default host API port is `HOST_CORAZA_PORT=19090` (no `:80` dependency).

### FP Tuner Command Bridge Flow

For external-tool integration (including future Codex CLI / Claude Code workflows), run the provider bridge in `command` mode:

```bash
./scripts/test_fp_tuner_bridge_command.sh
```

Related scripts:

- `scripts/fp_tuner_provider_bridge.py`: local HTTP bridge (`/propose`)
- `scripts/fp_tuner_provider_cmd_example.sh`: example command provider (stdin JSON -> stdout JSON)
- `scripts/fp_tuner_provider_openai.sh`: OpenAI-compatible command provider (stdin JSON -> API call -> stdout JSON)
- `scripts/fp_tuner_provider_claude.sh`: Claude Messages API command provider (stdin JSON -> API call -> stdout JSON)

You can replace `BRIDGE_COMMAND` with your own command that outputs proposal JSON:

```bash
BRIDGE_COMMAND="/path/to/your-provider-command.sh" ./scripts/test_fp_tuner_bridge_command.sh
```

OpenAI command provider example:

```bash
export FP_TUNER_OPENAI_API_KEY="<your-api-key>"
export FP_TUNER_OPENAI_MODEL="<your-model-name>"

BRIDGE_COMMAND="./scripts/fp_tuner_provider_openai.sh" ./scripts/test_fp_tuner_bridge_command.sh
```

Local mock test for the OpenAI command provider:

```bash
./scripts/test_fp_tuner_openai_command.sh
```

Claude command provider example:

```bash
export FP_TUNER_CLAUDE_API_KEY="<your-api-key>"
export FP_TUNER_CLAUDE_MODEL="claude-sonnet-4-6"

BRIDGE_COMMAND="./scripts/fp_tuner_provider_claude.sh" ./scripts/test_fp_tuner_bridge_command.sh
```

Local mock test for the Claude command provider:

```bash
./scripts/test_fp_tuner_claude_command.sh
```

### FP Tuner Admin UI Flow

The admin panel (`/fp-tuner`) now supports choosing one `waf_block` event directly from recent logs.

Typical flow:

1. Open `FP Tuner` in admin UI.
2. In `Pick From Recent waf_block Logs`, click `Use` on the event you want to tune.
3. Confirm populated event fields (`path`, `rule_id`, `matched_variable`, `matched_value`).
4. Click `Propose`, review/edit `proposal.rule_line`.
5. Click `Apply` (`simulate` first, then real apply with approval token if required).

This keeps external provider payload small by sending one selected event at a time.

---

## Admin API Endpoints (`/mamotama-api`)

### Endpoint List

| Method | Path | Description |
| --- | --- | --- |
| GET | `/mamotama-api/status` | Get current WAF status/config |
| GET | `/mamotama-api/metrics` | Export Prometheus-style runtime counters for rate limit and semantic scoring |
| GET | `/mamotama-api/logs/read` | Read WAF logs (`tail`) with optional country filter via `country` query |
| GET | `/mamotama-api/logs/stats` | Return WAF block summary + hourly series (`hours`, `scan` query supported) |
| GET | `/mamotama-api/logs/download` | Download WAF log file (`waf`) |
| GET | `/mamotama-api/rules` | Get active rule files (multi-file aware) |
| POST | `/mamotama-api/rules:validate` | Validate rule syntax (no save) |
| PUT | `/mamotama-api/rules` | Save rule file and hot-reload base WAF (`If-Match` supported) |
| GET | `/mamotama-api/crs-rule-sets` | Get CRS rule list and enabled/disabled state |
| POST | `/mamotama-api/crs-rule-sets:validate` | Validate CRS selection (no save) |
| PUT | `/mamotama-api/crs-rule-sets` | Save CRS selection and hot-reload (`If-Match` supported) |
| GET | `/mamotama-api/bypass-rules` | Get bypass file content |
| POST | `/mamotama-api/bypass-rules:validate` | Validate bypass content only (no save) |
| PUT | `/mamotama-api/bypass-rules` | Save bypass file (`If-Match` optimistic lock via `ETag`) |
| GET | `/mamotama-api/country-block-rules` | Get country block file content |
| POST | `/mamotama-api/country-block-rules:validate` | Validate country block file (no save) |
| PUT | `/mamotama-api/country-block-rules` | Save country block file (`If-Match` optimistic lock via `ETag`) |
| GET | `/mamotama-api/rate-limit-rules` | Get rate-limit config file |
| POST | `/mamotama-api/rate-limit-rules:validate` | Validate rate-limit config (no save) |
| PUT | `/mamotama-api/rate-limit-rules` | Save rate-limit config (`If-Match` optimistic lock via `ETag`) |
| GET | `/mamotama-api/notifications` | Get aggregate notification config file |
| GET | `/mamotama-api/notifications/status` | Get notification runtime status and active alert states |
| POST | `/mamotama-api/notifications/validate` | Validate notification config (no save) |
| POST | `/mamotama-api/notifications/test` | Send a test notification using current runtime config |
| PUT | `/mamotama-api/notifications` | Save notification config (`If-Match` optimistic lock via `ETag`) |
| GET | `/mamotama-api/bot-defense-rules` | Get bot-defense config file |
| POST | `/mamotama-api/bot-defense-rules:validate` | Validate bot-defense config (no save) |
| PUT | `/mamotama-api/bot-defense-rules` | Save bot-defense config (`If-Match` optimistic lock via `ETag`) |
| GET | `/mamotama-api/semantic-rules` | Get semantic security config and runtime stats |
| POST | `/mamotama-api/semantic-rules:validate` | Validate semantic config (no save) |
| PUT | `/mamotama-api/semantic-rules` | Save semantic config (`If-Match` optimistic lock via `ETag`) |
| POST | `/mamotama-api/fp-tuner/propose` | Build FP tuning proposal from request payload (`event` or `events[]`) or latest `waf_block` / `semantic_anomaly` log event |
| POST | `/mamotama-api/fp-tuner/apply` | Validate/apply proposed scoped exclusion rule (`simulate=true` by default, approval token required for real apply when enabled) |
| GET | `/mamotama-api/cache-rules` | Return `cache.conf` raw + structured data with `ETag` |
| POST | `/mamotama-api/cache-rules:validate` | Validate cache config (no save) |
| PUT | `/mamotama-api/cache-rules` | Save `cache.conf` (`If-Match` optimistic lock via `ETag`) |

If logs or rules are missing, API returns `500` with `{"error":"..."}`.

`GET /mamotama-api/status` also includes built-in listener TLS fields:
- `server_tls_enabled`
- `server_tls_cert_file`
- `server_tls_key_configured`
- `server_tls_min_version`
- `server_tls_redirect_http`
- `server_tls_http_redirect_addr`

---

## WAF Bypass / Special Rule Settings

`mamotama` supports request-level WAF bypass and path-specific special rule application.

### Bypass File Location

Specify in `data/conf/config.json` via `paths.bypass_file` (default: `conf/waf.bypass`).

### File Format

```text
# Normal bypass entries
/about/
/about/user.php

# Special rule application (do not bypass WAF; apply the given rule file)
/about/admin.php rules/admin-rule.conf

# Comment lines (starting with #)
#/should/be/ignored.php rules/test.conf
```

### Edit from UI

You can directly edit and save `waf.bypass` from dashboard `/bypass`.

### Country Block Settings

You can edit `paths.country_block_file` (default: `conf/country-block.conf`) from `/country-block`.
Use one country code per line (`JP`, `US`, `UNKNOWN`).
Matched countries are blocked with `403` before WAF inspection.

### Rate Limit Settings

You can edit `paths.rate_limit_file` (default: `conf/rate-limit.conf`) from `/rate-limit`.
Configuration format is JSON with `default_policy` and `rules`.
On exceed, response uses `action.status` (typically `429`) and includes `Retry-After` header.

#### JSON Parameter Quick Reference (what changes what)

| Parameter | Example | Effect |
| --- | --- | --- |
| `enabled` | `true` / `false` | Enables/disables rate limit globally. `false` means pass-through. |
| `allowlist_ips` | `["127.0.0.1/32", "10.0.0.5"]` | Always exempt matching IP/CIDR from rate limiting. |
| `allowlist_countries` | `["JP", "US"]` | Always exempt matching country codes. |
| `session_cookie_names` | `["session", "sid"]` | Cookie names checked when `key_by` uses session identity. |
| `jwt_header_names` | `["Authorization"]` | Header names checked for JWT subject extraction. |
| `jwt_cookie_names` | `["token", "access_token"]` | Cookie names checked for JWT subject extraction. |
| `adaptive_enabled` | `true` / `false` | Tighten rate limits automatically when semantic risk score is high. |
| `adaptive_score_threshold` | `6` | Minimum semantic risk score that activates adaptive throttling. |
| `adaptive_limit_factor_percent` | `50` | Percentage applied to `limit` when adaptive mode is active. |
| `adaptive_burst_factor_percent` | `50` | Percentage applied to `burst` when adaptive mode is active. |
| `default_policy.enabled` | `true` | Enable/disable default policy itself. |
| `default_policy.limit` | `120` | Base allowed requests per window. |
| `default_policy.burst` | `20` | Additional burst allowance. Effective cap is `limit + burst`. |
| `default_policy.window_seconds` | `60` | Window size in seconds. Smaller is stricter. |
| `default_policy.key_by` | `"ip"` | Aggregation key: `ip` / `country` / `ip_country` / `session` / `ip_session` / `jwt_sub` / `ip_jwt_sub`. |
| `default_policy.action.status` | `429` | HTTP status on exceed (`4xx`/`5xx`). |
| `default_policy.action.retry_after_seconds` | `60` | `Retry-After` value in seconds. If `0`, remaining window time is auto-calculated. |
| `rules[]` | see below | Overrides `default_policy` when matched. Evaluated top-down. |
| `rules[].match_type` | `"prefix"` | Match type: `exact` / `prefix` / `regex`. |
| `rules[].match_value` | `"/login"` | Match target according to type. |
| `rules[].methods` | `["POST"]` | Restrict methods. Empty means all methods. |
| `rules[].policy.*` |  | Policy fields used when this rule matches. |

#### Typical Tuning

- Temporarily disable globally: set `enabled=false`
- Improve spike tolerance: increase `burst`
- Apply per-login or per-user throttling: set `key_by="session"` or `key_by="jwt_sub"`
- Tighten suspicious clients automatically: enable `adaptive_enabled`
- Tighten login path: add a rule with `match_type=prefix`, `match_value=/login`, `methods=["POST"]`
- Separate by IP + country: set `key_by="ip_country"`
- Exempt trusted locations: add to `allowlist_ips` or `allowlist_countries`

#### Recommended Settings

- General public traffic: keep `default_policy.key_by="ip"`
- Browser login/forms with stable session cookies: use `key_by="session"`
- Authenticated APIs with stable trusted JWT `sub`: use `key_by="jwt_sub"`
- Start adaptive throttling on higher-risk login or write paths first: `adaptive_enabled=true`, `adaptive_score_threshold=6`, `adaptive_limit_factor_percent=50`, `adaptive_burst_factor_percent=50`

Oversized JWT header/cookie values are ignored for `jwt_sub` extraction and are not base64-decoded or JSON-parsed.

#### Monitoring Points

- Watch `/mamotama-api/metrics` for sustained increases in rate-limit blocked and adaptive counters
- Watch `/mamotama-api/metrics` for semantic action counters around login and write endpoints
- Inspect logs for `rl_key_hash`, `adaptive`, `risk_score`, `reason_list`, and `score_breakdown` when tuning false positives or throttling

### Notifications

You can edit `paths.notification_file` (default: `conf/notifications.conf`) from `/notifications`.
Notifications are disabled by default and emit only on aggregate state transitions, not on every blocked request.

- upstream notifications aggregate repeated proxy errors and transition `quiet -> active -> escalated -> quiet(recovered)`
- security notifications aggregate `waf_block`, `rate_limited`, `semantic_anomaly`, `bot_challenge`, and `ip_reputation` events and transition `quiet -> active -> escalated -> quiet(recovered)`
- supported sinks are `webhook` and `email`
- `POST /mamotama-api/notifications/test` sends a test notification using the current runtime config
- `GET /mamotama-api/notifications/status` shows active alert states, sink counts, and the last dispatch error

#### JSON Parameter Quick Reference

| Parameter | Example | Effect |
| --- | --- | --- |
| `enabled` | `true` / `false` | Enables/disables notification dispatch globally. Default is `false`. |
| `cooldown_seconds` | `900` | Minimum seconds between sends for the same alert key/state progression. |
| `sinks[].type` | `"webhook"` / `"email"` | Delivery backend. |
| `sinks[].enabled` | `true` / `false` | Enables/disables an individual sink. |
| `sinks[].webhook_url` | `"https://hooks.example.invalid/mamotama"` | Target URL for webhook delivery. |
| `sinks[].headers` | `{"X-Mamotama-Token":"..."}` | Optional webhook headers. |
| `sinks[].smtp_address` | `"smtp.example.invalid:587"` | SMTP relay used by email sink. |
| `sinks[].from` / `sinks[].to` | `"alerts@example.invalid"` / `["secops@example.invalid"]` | Email sender and recipients. |
| `upstream.window_seconds` | `60` | Aggregation window for proxy error counting. |
| `upstream.active_threshold` | `3` | Count that moves upstream alert state from `quiet` to `active`. |
| `upstream.escalated_threshold` | `10` | Count that moves upstream alert state from `active` to `escalated`. |
| `security.window_seconds` | `300` | Aggregation window for security event counting. |
| `security.active_threshold` | `20` | Count that moves security alert state from `quiet` to `active`. |
| `security.escalated_threshold` | `100` | Count that moves security alert state from `active` to `escalated`. |
| `security.sources` | `["waf_block","rate_limited"]` | Security event types included in aggregate tracking. |

#### Recommended Settings

- keep notifications disabled until webhook/email delivery has been verified with `POST /mamotama-api/notifications/test`
- start with webhook delivery first; Slack / Teams can usually consume the webhook sink directly
- enable upstream notifications first on public reverse-proxy traffic to catch sustained backend outages without per-request noise
- enable security notifications only after rate-limit / semantic thresholds have been tuned enough to avoid false-positive floods

Example:

```json
{
  "enabled": false,
  "cooldown_seconds": 900,
  "sinks": [
    {
      "name": "primary-webhook",
      "type": "webhook",
      "enabled": false,
      "webhook_url": "https://hooks.example.invalid/mamotama",
      "timeout_seconds": 5
    }
  ],
  "upstream": {
    "enabled": true,
    "window_seconds": 60,
    "active_threshold": 3,
    "escalated_threshold": 10
  },
  "security": {
    "enabled": true,
    "window_seconds": 300,
    "active_threshold": 20,
    "escalated_threshold": 100,
    "sources": ["waf_block", "rate_limited", "semantic_anomaly", "bot_challenge"]
  }
}
```

### Bot Defense Settings

You can edit `paths.bot_defense_file` (default: `conf/bot-defense.conf`) from `/bot-defense`.
When enabled, suspicious (or all, depending on mode) browser-like GET requests on matched paths receive a challenge response before WAF inspection.

#### JSON Parameter Quick Reference

| Parameter | Example | Effect |
| --- | --- | --- |
| `enabled` | `true` / `false` | Enables/disables bot challenge globally. |
| `mode` | `"suspicious"` | `suspicious` checks UA patterns, `always` challenges all matched requests. |
| `path_prefixes` | `["/", "/login"]` | Apply challenge only to matching request paths. |
| `exempt_cidrs` | `["127.0.0.1/32"]` | Skip challenge for trusted source IP/CIDR. |
| `suspicious_user_agents` | `["curl", "wget"]` | UA substrings used in `suspicious` mode. |
| `challenge_cookie_name` | `"__mamotama_bot_ok"` | Cookie name used for challenge pass state. |
| `challenge_secret` | `"long-random-secret"` | Signing secret for challenge token (empty = ephemeral per process). |
| `challenge_ttl_seconds` | `86400` | Token validity period in seconds. |
| `challenge_status_code` | `429` | HTTP status returned on challenge response (`4xx/5xx`). |

### Semantic Security Settings

You can edit `paths.semantic_file` (default: `conf/semantic.conf`) from `/semantic`.
This is a heuristic detector (rule-based, non-ML) with staged enforcement: `off | log_only | challenge | block`.

#### JSON Parameter Quick Reference

| Parameter | Example | Effect |
| --- | --- | --- |
| `enabled` | `true` / `false` | Enables/disables semantic scoring pipeline. |
| `mode` | `"challenge"` | Enforcement stage: `off` / `log_only` / `challenge` / `block`. |
| `exempt_path_prefixes` | `["/healthz"]` | Skip semantic scoring for matching paths. |
| `log_threshold` | `4` | Minimum score to emit semantic anomaly log. |
| `challenge_threshold` | `7` | Minimum score to issue semantic challenge in `challenge` mode. |
| `block_threshold` | `9` | Minimum score to hard-block (`403`) in `block` mode. |
| `max_inspect_body` | `16384` | Max request body bytes inspected by semantic scoring. |
| `temporal_window_seconds` | `10` | Sliding window used for per-IP temporal observations. |
| `temporal_max_entries_per_ip` | `128` | Max in-memory observations kept per IP within the temporal window. |
| `temporal_burst_threshold` | `20` | Request-count threshold for `temporal:ip_burst`. |
| `temporal_burst_score` | `2` | Score added when `temporal:ip_burst` fires. |
| `temporal_path_fanout_threshold` | `8` | Distinct-path threshold for `temporal:ip_path_fanout`. |
| `temporal_path_fanout_score` | `2` | Score added when `temporal:ip_path_fanout` fires. |
| `temporal_ua_churn_threshold` | `4` | Distinct-User-Agent threshold for `temporal:ip_ua_churn`. |
| `temporal_ua_churn_score` | `1` | Score added when `temporal:ip_ua_churn` fires. |

`semantic_anomaly` logs include `reason_list` and `score_breakdown`, and `/mamotama-api/metrics` exposes Prometheus-style counters for rate limiting and semantic actions.

### Rule File Editing (multi-file aware)

Dashboard `/rules` edits active base rule set (`paths.rules_file` and, when CRS enabled, `paths.crs_setup_file` + enabled `*.conf` under `paths.crs_rules_dir`).
Before save, server-side syntax validation is performed. Successful save hot-reloads base WAF.
If reload fails, automatic rollback is applied.

### CRS Rule Set Toggle

Dashboard `/rule-sets` toggles each file under `rules/crs/rules/*.conf`.
State is persisted to `paths.crs_disabled_file` and WAF is hot-reloaded on save.

### Priority

- Special-rule entries take precedence (bypass entries on same path are ignored)
- If rule file does not exist:
  - `admin.strict_override=true`: fail immediately (`log.Fatalf`)
  - `false` or unset: log warning and continue with normal rules

### Example

```text
/about/                    # bypass everything under /about/
/about/admin.php rules/special.conf  # only admin.php uses special rule via WAF
```

### Notes

- Rules are evaluated top-to-bottom in file order
- Lines with `extraRuleFile` are prioritized
- Comment lines (`#...`) are ignored

---

## Log Retrieval

Logs are available via API.

```bash
curl -s -H "X-API-Key: <your-api-key>" \
     "http://<host>/mamotama-api/logs/read?src=waf&tail=100&country=JP" | jq .
```

- `src`: log type (`waf`)
- `tail`: number of lines
- `country`: country code filter (`JP`, `US`, `UNKNOWN`). Omit or set `ALL` for all records.
  - Under Cloudflare, `CF-IPCountry` header is used. If unavailable, `UNKNOWN` is used.

Use the API key configured in `data/conf/config.json` (`admin.api_key_primary` / `admin.api_key_secondary`).
For production, always enforce access controls and authentication.

## Cache Feature

You can dynamically configure cache target paths and TTL.

### Config File

Cache config is stored in `/data/conf/cache.conf`.
Hot reload is supported; changes apply right after saving the file.

#### Example

```bash
# Cache static assets (CSS/JS/images) for 10 minutes
ALLOW prefix=/_next/static/chunks/ methods=GET,HEAD ttl=600 vary=Accept-Encoding

# Cache specific HTML pages for 5 minutes (regex)
ALLOW regex=^/about/.*.html$ methods=GET ttl=300

# Deny cache for all API paths (safe default)
DENY prefix=/mamotama-api/

# Deny cache for authenticated user profile pages (regex)
DENY regex=^/users/[0-9]+/profile

# Everything else defaults to non-cache
```

- `ALLOW`: cache enabled (`ttl` in seconds, optional `vary`)
- `DENY`: excluded from cache
- Recommended methods are `GET`/`HEAD` (`POST` etc. are not cached)

Field details:
- `prefix`: match request path prefix
- `regex`: regex match (`^` and `$` supported)
- `methods`: target HTTP methods (comma-separated)
- `ttl`: cache duration in seconds
- `vary`: `Vary` header values added to response (comma-separated)

### Behavior Summary

- Go side sets `X-Mamotama-Cacheable` and `X-Accel-Expires` on responses matching cache rules
- these headers can be consumed by external cache/CDN layers if needed
- Requests with auth headers, cookies, or API paths are non-cacheable by default
- Upstream responses containing `Set-Cookie` are not stored (to prevent shared-cache leakage)

### How to Verify

Check response headers:
- `X-Mamotama-Cacheable: 1`
- `X-Accel-Expires: <seconds>`

By default this stack does not include an internal HTTP cache layer.

---

## Admin UI Access Restrictions

This project does not include access control by default.
If you expose admin UI (`/mamotama-ui`), always configure access controls such as Basic Auth and/or IP restrictions.

---

## Quality Gates (CI)

GitHub Actions workflow `ci` validates:

- `go test ./...` (`coraza/src`)
- `docker compose config` sanity check
- MySQL log-store integration test (`go test ./internal/handler -run TestLogsStatsMySQLStoreAggregatesAndIngestsIncrementally`, with `docker compose --profile mysql up -d mysql`)
- Proxy admin smoke (`./scripts/ci_proxy_admin_smoke.sh`: embedded UI + `proxy-rules` validate/probe/PUT/rollback + ETag conflict)
- `./scripts/run_gotestwaf.sh` (`waf-test` matrix, `MIN_BLOCKED_RATIO=70`, with both `storage.backend=file` and `storage.backend=db`)

In production workflows, set these as required branch protection checks:

- `ci / go-test`
- `ci / mysql-logstore-test`
- `ci / compose-validate`
- `ci / waf-test (file)`
- `ci / waf-test (sqlite)`

For local pre-push verification, use:

```bash
make ci-local
```

---

## False Positive Tuning

See:

- `docs/operations/waf-tuning.md`
- `docs/operations/fp-tuner-api.md`

## DB Operations

SQLite operation notes:

- `docs/operations/db-ops.md`

---

## What Is mamotama?

**mamotama** is inspired by the Japanese phrase **「護りたまえ」 (mamoritamae)**,
which means *"please protect"* or *"grant protection"*.

The name reflects the project's purpose: protecting web applications and infrastructure.
