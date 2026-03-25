#!/usr/bin/env bash

proxy_api_init() {
  HOST_CORAZA_PORT="${HOST_CORAZA_PORT:-19090}"
  WAF_LISTEN_PORT="${WAF_LISTEN_PORT:-9090}"
  WAF_API_BASEPATH="${WAF_API_BASEPATH:-/mamotama-api}"
  WAF_UI_BASEPATH="${WAF_UI_BASEPATH:-/mamotama-ui}"
  WAF_API_KEY_PRIMARY="${WAF_API_KEY_PRIMARY:-dev-only-change-this-key-please}"

  PROXY_BASE_URL="http://127.0.0.1:${HOST_CORAZA_PORT}"
  PROXY_API_URL="${PROXY_BASE_URL}${WAF_API_BASEPATH}"
  PROXY_UI_URL="${PROXY_BASE_URL}${WAF_UI_BASEPATH}"
  PROXY_AUTH_HEADER="X-API-Key: ${WAF_API_KEY_PRIMARY}"
}

proxy_api_need_cmd() {
  local name="$1"
  if ! command -v "${name}" >/dev/null 2>&1; then
    echo "[proxy-api][ERROR] missing command: ${name}" >&2
    return 1
  fi
}

proxy_api_wait_health() {
  local retries="${1:-60}"
  local interval="${2:-1}"
  local i code
  for i in $(seq 1 "${retries}"); do
    code="$(curl -sS -o /dev/null -w "%{http_code}" "${PROXY_BASE_URL}/healthz" || true)"
    if [[ "${code}" == "200" ]]; then
      return 0
    fi
    sleep "${interval}"
  done
  echo "[proxy-api][ERROR] healthz timeout" >&2
  return 1
}

proxy_api_expect_http_code() {
  local method="$1"
  local url="$2"
  local expected="$3"
  local code
  if [[ "${method}" == "HEAD" ]]; then
    code="$(curl -sS -I -o /dev/null -w "%{http_code}" "${url}" || true)"
  else
    code="$(curl -sS -o /dev/null -w "%{http_code}" -X "${method}" "${url}" || true)"
  fi
  if [[ "${code}" != "${expected}" ]]; then
    echo "[proxy-api][ERROR] ${method} ${url} => ${code} (expected ${expected})" >&2
    return 1
  fi
}

proxy_api_get_snapshot() {
  curl -fsS -H "${PROXY_AUTH_HEADER}" "${PROXY_API_URL}/proxy-rules"
}

proxy_api_apply_raw() {
  local raw="$1"
  local response_file="${2:-/tmp/proxy_api_put_resp.json}"
  local snapshot etag body code

  snapshot="$(proxy_api_get_snapshot)"
  etag="$(jq -r '.etag // empty' <<<"${snapshot}")"
  if [[ -z "${etag}" ]]; then
    echo "[proxy-api][ERROR] missing etag in proxy snapshot" >&2
    return 1
  fi
  body="$(jq -n --arg raw "${raw}" '{raw: $raw}')"
  code="$(curl -sS -o "${response_file}" -w "%{http_code}" \
    -H "${PROXY_AUTH_HEADER}" -H "If-Match: ${etag}" -H "Content-Type: application/json" \
    -X PUT --data "${body}" "${PROXY_API_URL}/proxy-rules")"
  if [[ "${code}" != "200" ]]; then
    echo "[proxy-api][ERROR] failed to apply proxy rules: ${code}" >&2
    cat "${response_file}" >&2 || true
    return 1
  fi
}
