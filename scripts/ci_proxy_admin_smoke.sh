#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib/proxy_api.sh
source "${SCRIPT_DIR}/lib/proxy_api.sh"

proxy_api_init
proxy_api_need_cmd curl
proxy_api_need_cmd jq
proxy_api_need_cmd python3

PROXY_ECHO_PORT="${PROXY_ECHO_PORT:-18080}"
PROTECTED_HOST="${PROTECTED_HOST:-protected.example.test}"
proxy_echo_log="$(mktemp /tmp/proxy_echo.XXXXXX.log)"
proxy_route_headers="/tmp/proxy_route_headers.txt"
proxy_route_body="/tmp/proxy_route_body.json"

cleanup() {
  if [[ -n "${proxy_echo_pid:-}" ]] && kill -0 "${proxy_echo_pid}" >/dev/null 2>&1; then
    kill "${proxy_echo_pid}" >/dev/null 2>&1 || true
    wait "${proxy_echo_pid}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

python3 - "${PROXY_ECHO_PORT}" >"${proxy_echo_log}" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

port = int(sys.argv[1])


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self._respond()

    def do_HEAD(self):
        self._respond(send_body=False)

    def log_message(self, _format, *_args):
        return

    def _respond(self, send_body=True):
        payload = {
            "method": self.command,
            "path": self.path,
            "host": self.headers.get("Host", ""),
            "x_service": self.headers.get("X-Service", ""),
            "x_route": self.headers.get("X-Route", ""),
        }
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Upstream-Echo", "1")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if send_body:
            self.wfile.write(body)


ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
PY
proxy_echo_pid=$!

for _ in $(seq 1 20); do
  code="$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${PROXY_ECHO_PORT}/healthz" || true)"
  if [[ "${code}" == "200" ]]; then
    break
  fi
  sleep 0.5
done
if [[ "${code:-}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] echo upstream failed to start" >&2
  cat "${proxy_echo_log}" >&2 || true
  exit 1
fi

proxy_api_wait_health

proxy_api_expect_http_code "GET" "${PROXY_UI_URL}" "200"
proxy_api_expect_http_code "HEAD" "${PROXY_UI_URL}" "200"
proxy_api_expect_http_code "HEAD" "${PROXY_UI_URL}/" "200"

proxy_get_json="$(proxy_api_get_snapshot)"
etag="$(jq -r '.etag // empty' <<<"${proxy_get_json}")"
raw="$(jq -r '.raw // empty' <<<"${proxy_get_json}")"

if [[ -z "${etag}" || -z "${raw}" ]]; then
  echo "[proxy-smoke][ERROR] proxy-rules response missing etag/raw" >&2
  exit 1
fi

validate_body="$(jq -n --arg raw "${raw}" '{raw: $raw}')"
validate_code="$(curl -sS -o /tmp/proxy_validate_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "Content-Type: application/json" \
  -X POST --data "${validate_body}" "${PROXY_API_URL}/proxy-rules:validate")"
if [[ "${validate_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] validate failed: ${validate_code}" >&2
  cat /tmp/proxy_validate_resp.json >&2 || true
  exit 1
fi

probe_raw="$(jq --arg upstream "http://127.0.0.1:${WAF_LISTEN_PORT}" \
  '.upstream_url = $upstream' <<<"${raw}")"
probe_body="$(jq -n --arg raw "${probe_raw}" --argjson timeout_ms 1000 '{raw: $raw, timeout_ms: $timeout_ms}')"
probe_code="$(curl -sS -o /tmp/proxy_probe_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "Content-Type: application/json" \
  -X POST --data "${probe_body}" "${PROXY_API_URL}/proxy-rules:probe")"
if [[ "${probe_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] probe failed: ${probe_code}" >&2
  cat /tmp/proxy_probe_resp.json >&2 || true
  exit 1
fi

dry_run_body="$(jq -n --arg raw "${raw}" --arg host "example.test" --arg path "/healthz" '{raw: $raw, host: $host, path: $path}')"
dry_run_code="$(curl -sS -o /tmp/proxy_dry_run_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "Content-Type: application/json" \
  -X POST --data "${dry_run_body}" "${PROXY_API_URL}/proxy-rules:dry-run")"
if [[ "${dry_run_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] dry-run failed: ${dry_run_code}" >&2
  cat /tmp/proxy_dry_run_resp.json >&2 || true
  exit 1
fi

route_raw="$(jq -n \
  --arg protectedHost "${PROTECTED_HOST}" \
  --arg upstream "http://host.docker.internal:${PROXY_ECHO_PORT}" \
  '{
    upstream_url: $upstream,
    upstreams: [
      {
        name: "service-a",
        url: $upstream,
        weight: 1,
        enabled: true
      }
    ],
    routes: [
      {
        name: "service-a-prefix",
        enabled: true,
        priority: 10,
        match: {
          hosts: [$protectedHost],
          path: { type: "prefix", value: "/servicea/" }
        },
        action: {
          upstream: "service-a",
          host_rewrite: "service-a.internal",
          path_rewrite: { prefix: "/service-a/" },
          request_headers: {
            set: { "X-Service": "service-a" },
            add: { "X-Route": "service-a-prefix" },
            remove: ["X-Debug"]
          },
          response_headers: {
            add: { "Cache-Control": "no-store" }
          }
        }
      }
    ],
    default_route: {
      name: "default",
      enabled: true,
      action: { upstream: $upstream }
    },
    dial_timeout: 5,
    response_header_timeout: 10,
    idle_conn_timeout: 90,
    max_idle_conns: 100,
    max_idle_conns_per_host: 100,
    max_conns_per_host: 200,
    force_http2: true,
    disable_compression: false,
    expect_continue_timeout: 1,
    tls_insecure_skip_verify: false,
    tls_client_cert: "",
    tls_client_key: "",
    buffer_request_body: true,
    max_response_buffer_bytes: 1048576,
    flush_interval_ms: 25,
    health_check_path: "/healthz",
    health_check_interval_sec: 15,
    health_check_timeout_sec: 2,
    error_html_file: "",
    error_redirect_url: ""
  }')"

route_validate_body="$(jq -n --arg raw "${route_raw}" '{raw: $raw}')"
route_validate_code="$(curl -sS -o /tmp/proxy_route_validate_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "Content-Type: application/json" \
  -X POST --data "${route_validate_body}" "${PROXY_API_URL}/proxy-rules:validate")"
if [[ "${route_validate_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] route validate failed: ${route_validate_code}" >&2
  cat /tmp/proxy_route_validate_resp.json >&2 || true
  exit 1
fi

route_dry_run_body="$(jq -n --arg raw "${route_raw}" --arg host "${PROTECTED_HOST}" --arg path "/servicea/users" '{raw: $raw, host: $host, path: $path}')"
route_dry_run_code="$(curl -sS -o /tmp/proxy_route_dry_run_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "Content-Type: application/json" \
  -X POST --data "${route_dry_run_body}" "${PROXY_API_URL}/proxy-rules:dry-run")"
if [[ "${route_dry_run_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] route dry-run failed: ${route_dry_run_code}" >&2
  cat /tmp/proxy_route_dry_run_resp.json >&2 || true
  exit 1
fi
if ! jq -e \
  --arg upstream "http://host.docker.internal:${PROXY_ECHO_PORT}" \
  '.dry_run.source == "route"
   and .dry_run.route_name == "service-a-prefix"
   and .dry_run.rewritten_host == "service-a.internal"
   and .dry_run.rewritten_path == "/service-a/users"
   and .dry_run.selected_upstream == "service-a"
   and .dry_run.selected_upstream_url == $upstream
   and .dry_run.final_url == ($upstream + "/service-a/users")' \
  /tmp/proxy_route_dry_run_resp.json >/dev/null; then
  echo "[proxy-smoke][ERROR] route dry-run assertion failed" >&2
  cat /tmp/proxy_route_dry_run_resp.json >&2 || true
  exit 1
fi

put_body="$(jq -n --arg raw "${route_raw}" '{raw: $raw}')"
put_code="$(curl -sS -o /tmp/proxy_put_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "If-Match: ${etag}" -H "Content-Type: application/json" \
  -X PUT --data "${put_body}" "${PROXY_API_URL}/proxy-rules")"
if [[ "${put_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] put failed: ${put_code}" >&2
  cat /tmp/proxy_put_resp.json >&2 || true
  exit 1
fi

route_snapshot="$(proxy_api_get_snapshot)"
if ! jq -e '.proxy.routes | length == 1' <<<"${route_snapshot}" >/dev/null; then
  echo "[proxy-smoke][ERROR] applied route config missing from snapshot" >&2
  jq '.proxy.routes' <<<"${route_snapshot}" >&2 || true
  exit 1
fi

route_request_code="$(curl -sS -D "${proxy_route_headers}" -o "${proxy_route_body}" -w "%{http_code}" \
  -H "Host: ${PROTECTED_HOST}" \
  "${PROXY_BASE_URL}/servicea/users")"
if [[ "${route_request_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] routed request failed: ${route_request_code}" >&2
  cat "${proxy_route_body}" >&2 || true
  exit 1
fi
if ! jq -e '.path == "/service-a/users" and .host == "service-a.internal" and .x_service == "service-a" and .x_route == "service-a-prefix"' "${proxy_route_body}" >/dev/null; then
  echo "[proxy-smoke][ERROR] route request assertion failed" >&2
  cat "${proxy_route_body}" >&2 || true
  exit 1
fi
if ! grep -iq '^Cache-Control: no-store' "${proxy_route_headers}"; then
  echo "[proxy-smoke][ERROR] response header rewrite assertion failed" >&2
  cat "${proxy_route_headers}" >&2 || true
  exit 1
fi

conflict_code="$(curl -sS -o /tmp/proxy_conflict_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "If-Match: stale-etag" -H "Content-Type: application/json" \
  -X PUT --data "${put_body}" "${PROXY_API_URL}/proxy-rules")"
if [[ "${conflict_code}" != "409" ]]; then
  echo "[proxy-smoke][ERROR] etag conflict check failed: ${conflict_code}" >&2
  cat /tmp/proxy_conflict_resp.json >&2 || true
  exit 1
fi

rollback_code="$(curl -sS -o /tmp/proxy_rollback_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "Content-Type: application/json" \
  -X POST --data '{}' "${PROXY_API_URL}/proxy-rules:rollback")"
if [[ "${rollback_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] rollback failed: ${rollback_code}" >&2
  cat /tmp/proxy_rollback_resp.json >&2 || true
  exit 1
fi

rolled_back_snapshot="$(proxy_api_get_snapshot)"
if ! jq -e '.proxy.routes | length == 0' <<<"${rolled_back_snapshot}" >/dev/null; then
  echo "[proxy-smoke][ERROR] rollback did not restore original proxy config" >&2
  jq '.proxy.routes' <<<"${rolled_back_snapshot}" >&2 || true
  exit 1
fi

echo "[proxy-smoke][OK] ui + proxy-rules + route rewrite smoke checks passed"
