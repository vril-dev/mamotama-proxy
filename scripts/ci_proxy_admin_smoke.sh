#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib/proxy_api.sh
source "${SCRIPT_DIR}/lib/proxy_api.sh"

proxy_api_init
proxy_api_need_cmd curl
proxy_api_need_cmd jq
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

updated_raw="$(jq '.flush_interval_ms = ((.flush_interval_ms // 0) + 10) % 1000' <<<"${raw}")"
put_body="$(jq -n --arg raw "${updated_raw}" '{raw: $raw}')"
put_code="$(curl -sS -o /tmp/proxy_put_resp.json -w "%{http_code}" \
  -H "${PROXY_AUTH_HEADER}" -H "If-Match: ${etag}" -H "Content-Type: application/json" \
  -X PUT --data "${put_body}" "${PROXY_API_URL}/proxy-rules")"
if [[ "${put_code}" != "200" ]]; then
  echo "[proxy-smoke][ERROR] put failed: ${put_code}" >&2
  cat /tmp/proxy_put_resp.json >&2 || true
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

echo "[proxy-smoke][OK] ui + proxy-rules smoke checks passed"
