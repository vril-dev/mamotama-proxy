#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib/proxy_api.sh
source "${SCRIPT_DIR}/lib/proxy_api.sh"

BENCH_REQUESTS="${BENCH_REQUESTS:-600}"
WARMUP_REQUESTS="${WARMUP_REQUESTS:-100}"
BENCH_CONCURRENCY="${BENCH_CONCURRENCY:-1,10,50}"
BENCH_PATH="${BENCH_PATH:-/bench}"
BENCH_TIMEOUT_SEC="${BENCH_TIMEOUT_SEC:-30}"
BENCH_MAX_FAIL_RATE_PCT="${BENCH_MAX_FAIL_RATE_PCT:-}"
BENCH_MIN_RPS="${BENCH_MIN_RPS:-}"
BENCH_DISABLE_RATE_LIMIT="${BENCH_DISABLE_RATE_LIMIT:-1}"
UPSTREAM_PORT="${UPSTREAM_PORT:-18080}"
OUTPUT_FILE="${OUTPUT_FILE:-data/logs/proxy/proxy-benchmark-summary.md}"

benchmark_failed=0
bench_ip_counter=10
baseline_rate_limit_raw=""
rate_limit_overridden=0

need_cmd() {
  local name="$1"
  if ! command -v "${name}" >/dev/null 2>&1; then
    echo "[proxy-bench][ERROR] missing command: ${name}" >&2
    exit 1
  fi
}

need_cmd docker
need_cmd curl
need_cmd jq
need_cmd ab
need_cmd python3

if [[ "${BENCH_REQUESTS}" -lt 5 ]]; then
  echo "[proxy-bench][ERROR] BENCH_REQUESTS must be >= 5" >&2
  exit 1
fi

if [[ "${WARMUP_REQUESTS}" -lt 0 ]]; then
  echo "[proxy-bench][ERROR] WARMUP_REQUESTS must be >= 0" >&2
  exit 1
fi

if [[ ! "${BENCH_PATH}" =~ ^/ ]]; then
  echo "[proxy-bench][ERROR] BENCH_PATH must start with '/'" >&2
  exit 1
fi

proxy_api_init

tmp_dir="$(mktemp -d)"
upstream_pid=""
baseline_raw=""

cleanup() {
  if [[ "${rate_limit_overridden}" -eq 1 ]]; then
    restore_rate_limit_rules || true
  fi
  if [[ -n "${baseline_raw}" ]]; then
    restore_proxy_rules || true
  fi
  if [[ -n "${upstream_pid}" ]]; then
    kill "${upstream_pid}" >/dev/null 2>&1 || true
    wait "${upstream_pid}" >/dev/null 2>&1 || true
  fi
  rm -rf "${tmp_dir}" >/dev/null 2>&1 || true
  (
    cd "${ROOT_DIR}"
    CORAZA_PORT="${HOST_CORAZA_PORT}" docker compose down --remove-orphans >/dev/null 2>&1 || true
  )
}
trap cleanup EXIT

apply_proxy_raw() {
  local raw="$1"
  proxy_api_apply_raw "${raw}" "${tmp_dir}/put_resp.json"
}

restore_proxy_rules() {
  if [[ -z "${baseline_raw}" ]]; then
    return 0
  fi
  echo "[proxy-bench] restoring baseline proxy rules"
  apply_proxy_raw "${baseline_raw}"
}

rate_limit_get_snapshot() {
  curl -fsS -H "${PROXY_AUTH_HEADER}" "${PROXY_API_URL}/rate-limit-rules"
}

rate_limit_apply_raw() {
  local raw="$1"
  local response_file="${tmp_dir}/put_rate_limit_resp.json"
  local snapshot etag body code

  snapshot="$(rate_limit_get_snapshot)"
  etag="$(jq -r '.etag // empty' <<<"${snapshot}")"
  if [[ -z "${etag}" ]]; then
    echo "[proxy-bench][ERROR] missing etag in rate-limit snapshot" >&2
    return 1
  fi

  body="$(jq -n --arg raw "${raw}" '{raw: $raw}')"
  code="$(curl -sS -o "${response_file}" -w "%{http_code}" \
    -H "${PROXY_AUTH_HEADER}" -H "If-Match: ${etag}" -H "Content-Type: application/json" \
    -X PUT --data "${body}" "${PROXY_API_URL}/rate-limit-rules")"
  if [[ "${code}" != "200" ]]; then
    echo "[proxy-bench][ERROR] failed to apply rate-limit rules: ${code}" >&2
    cat "${response_file}" >&2 || true
    return 1
  fi
}

override_rate_limit_rules_for_benchmark() {
  local snapshot
  local disabled_raw

  snapshot="$(rate_limit_get_snapshot)"
  baseline_rate_limit_raw="$(jq -r '.raw // empty' <<<"${snapshot}")"
  if [[ -z "${baseline_rate_limit_raw}" ]]; then
    echo "[proxy-bench][WARN] rate-limit raw is empty, skip override" >&2
    return 0
  fi

  disabled_raw="$(jq '
    .enabled = false
    | .default_policy.enabled = false
    | .rules = []
  ' <<<"${baseline_rate_limit_raw}")"

  rate_limit_apply_raw "${disabled_raw}"
  rate_limit_overridden=1
  echo "[proxy-bench] rate-limit rules temporarily disabled"
}

restore_rate_limit_rules() {
  if [[ -z "${baseline_rate_limit_raw}" ]]; then
    return 0
  fi
  echo "[proxy-bench] restoring baseline rate-limit rules"
  rate_limit_apply_raw "${baseline_rate_limit_raw}"
}

validate_concurrency_levels() {
  local csv="$1"
  local lvl trimmed
  local levels=()

  IFS=',' read -r -a levels <<<"${csv}"
  if [[ "${#levels[@]}" -eq 0 ]]; then
    echo "[proxy-bench][ERROR] BENCH_CONCURRENCY is empty" >&2
    return 1
  fi

  for lvl in "${levels[@]}"; do
    trimmed="${lvl//[[:space:]]/}"
    if [[ -z "${trimmed}" ]]; then
      echo "[proxy-bench][ERROR] BENCH_CONCURRENCY contains empty value" >&2
      return 1
    fi
    if ! [[ "${trimmed}" =~ ^[0-9]+$ ]]; then
      echo "[proxy-bench][ERROR] invalid concurrency value: ${trimmed}" >&2
      return 1
    fi
    if [[ "${trimmed}" -le 0 ]]; then
      echo "[proxy-bench][ERROR] concurrency must be > 0: ${trimmed}" >&2
      return 1
    fi
  done
}

extract_ab_metric() {
  local file="$1"
  local pattern="$2"
  local fallback="${3:-0}"
  local out

  out="$(sed -nE "${pattern}" "${file}" | head -n1 || true)"
  if [[ -n "${out}" ]]; then
    printf "%s" "${out}"
    return 0
  fi
  printf "%s" "${fallback}"
}

bench_count_for_concurrency() {
  local count="$1"
  local concurrency="$2"
  if [[ "${count}" -lt "${concurrency}" ]]; then
    printf "%s" "${concurrency}"
    return 0
  fi
  printf "%s" "${count}"
}

run_ab() {
  local count="$1"
  local concurrency="$2"
  local url="$3"
  local output_file="$4"
  local bench_ip="${5:-198.18.0.10}"

  ab -n "${count}" -c "${concurrency}" -s "${BENCH_TIMEOUT_SEC}" \
    -H "X-Forwarded-For: ${bench_ip}" -H "X-Real-IP: ${bench_ip}" \
    "${url}" > "${output_file}"
}

check_bench_thresholds() {
  local preset="$1"
  local concurrency="$2"
  local fail_rate="$3"
  local rps="$4"

  if [[ -n "${BENCH_MAX_FAIL_RATE_PCT}" ]]; then
    if awk -v v="${fail_rate}" -v m="${BENCH_MAX_FAIL_RATE_PCT}" 'BEGIN {exit (v<=m)?0:1}'; then
      :
    else
      echo "[proxy-bench][WARN] threshold breach preset=${preset} c=${concurrency} fail_rate_pct=${fail_rate} > ${BENCH_MAX_FAIL_RATE_PCT}" >&2
      benchmark_failed=1
    fi
  fi

  if [[ -n "${BENCH_MIN_RPS}" ]]; then
    if awk -v v="${rps}" -v m="${BENCH_MIN_RPS}" 'BEGIN {exit (v>=m)?0:1}'; then
      :
    else
      echo "[proxy-bench][WARN] threshold breach preset=${preset} c=${concurrency} rps=${rps} < ${BENCH_MIN_RPS}" >&2
      benchmark_failed=1
    fi
  fi
}

build_preset_raw() {
  local base_raw="$1"
  local preset="$2"
  local upstream="http://host.docker.internal:${UPSTREAM_PORT}"
  case "${preset}" in
    balanced)
      jq --arg upstream "${upstream}" --arg path "${BENCH_PATH}" '
        .upstream_url = $upstream
        | .force_http2 = false
        | .disable_compression = false
        | .buffer_request_body = false
        | .max_response_buffer_bytes = 0
        | .flush_interval_ms = 0
        | .health_check_path = $path
      ' <<<"${base_raw}"
      ;;
    low-latency)
      jq --arg upstream "${upstream}" --arg path "${BENCH_PATH}" '
        .upstream_url = $upstream
        | .force_http2 = true
        | .disable_compression = true
        | .buffer_request_body = false
        | .max_response_buffer_bytes = 0
        | .flush_interval_ms = 5
        | .health_check_path = $path
      ' <<<"${base_raw}"
      ;;
    buffered-guard)
      jq --arg upstream "${upstream}" --arg path "${BENCH_PATH}" '
        .upstream_url = $upstream
        | .force_http2 = true
        | .disable_compression = false
        | .buffer_request_body = true
        | .max_response_buffer_bytes = 1048576
        | .flush_interval_ms = 25
        | .health_check_path = $path
      ' <<<"${base_raw}"
      ;;
    *)
      echo "[proxy-bench][ERROR] unknown preset: ${preset}" >&2
      return 1
      ;;
  esac
}

run_preset_concurrency() {
  local preset="$1"
  local concurrency="$2"
  local bench_url="${PROXY_BASE_URL}${BENCH_PATH}"
  local out_file="${tmp_dir}/ab_${preset}_c${concurrency}.txt"
  local warmup_file="${tmp_dir}/ab_warmup_${preset}_c${concurrency}.txt"
  local bench_ip
  local warmup_count run_count
  local complete failed non2xx fail_rate avg p95 p99 rps

  bench_ip="198.18.0.${bench_ip_counter}"
  bench_ip_counter=$((bench_ip_counter + 1))
  if [[ "${bench_ip_counter}" -ge 240 ]]; then
    bench_ip_counter=10
  fi

  warmup_count="$(bench_count_for_concurrency "${WARMUP_REQUESTS}" "${concurrency}")"
  run_count="$(bench_count_for_concurrency "${BENCH_REQUESTS}" "${concurrency}")"

  if [[ "${WARMUP_REQUESTS}" -gt 0 ]]; then
    run_ab "${warmup_count}" "${concurrency}" "${bench_url}" "${warmup_file}" "${bench_ip}"
  fi
  run_ab "${run_count}" "${concurrency}" "${bench_url}" "${out_file}" "${bench_ip}"

  complete="$(extract_ab_metric "${out_file}" 's/^Complete requests:[[:space:]]+([0-9]+)$/\1/p')"
  failed="$(extract_ab_metric "${out_file}" 's/^Failed requests:[[:space:]]+([0-9]+)$/\1/p')"
  non2xx="$(extract_ab_metric "${out_file}" 's/^Non-2xx responses:[[:space:]]+([0-9]+)$/\1/p')"
  avg="$(extract_ab_metric "${out_file}" 's/^Time per request:[[:space:]]+([0-9.]+)[[:space:]]+\[ms\][[:space:]]+\(mean\)$/\1/p' '0.0')"
  rps="$(extract_ab_metric "${out_file}" 's/^Requests per second:[[:space:]]+([0-9.]+)[[:space:]]+\[#\/sec\][[:space:]]+\(mean\)$/\1/p' '0.0')"
  p95="$(extract_ab_metric "${out_file}" 's/^[[:space:]]*95%[[:space:]]+([0-9.]+)$/\1/p' '0.0')"
  p99="$(extract_ab_metric "${out_file}" 's/^[[:space:]]*99%[[:space:]]+([0-9.]+)$/\1/p' '0.0')"
  fail_rate="$(awk -v c="${complete}" -v f="${failed}" -v n="${non2xx}" 'BEGIN {if (c>0) printf "%.2f", ((f+n)*100.0)/c; else print "0.00"}')"

  check_bench_thresholds "${preset}" "${concurrency}" "${fail_rate}" "${rps}"

  printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n' \
    "${preset}" "${concurrency}" "${complete}" "${failed}" "${non2xx}" "${fail_rate}" "${avg}" "${p95}" "${p99}" "${rps}"
}

run_preset() {
  local preset="$1"
  local preset_raw lvl concurrency
  local levels=()

  preset_raw="$(build_preset_raw "${baseline_raw}" "${preset}")"
  apply_proxy_raw "${preset_raw}"

  IFS=',' read -r -a levels <<<"${BENCH_CONCURRENCY}"
  for lvl in "${levels[@]}"; do
    concurrency="${lvl//[[:space:]]/}"
    run_preset_concurrency "${preset}" "${concurrency}"
  done
}

validate_concurrency_levels "${BENCH_CONCURRENCY}"

mkdir -p "${tmp_dir}/upstream"
cat > "${tmp_dir}/upstream/bench" <<'UPSTREAM_EOF'
ok
UPSTREAM_EOF

python3 -m http.server "${UPSTREAM_PORT}" --bind 127.0.0.1 --directory "${tmp_dir}/upstream" >/dev/null 2>&1 &
upstream_pid="$!"

(
  cd "${ROOT_DIR}"
  CORAZA_PORT="${HOST_CORAZA_PORT}" WAF_LISTEN_PORT="${WAF_LISTEN_PORT}" docker compose up -d --build coraza >/dev/null
)
proxy_api_wait_health 90 1

snapshot="$(proxy_api_get_snapshot)"
baseline_raw="$(jq -r '.raw // empty' <<<"${snapshot}")"
if [[ -z "${baseline_raw}" ]]; then
  echo "[proxy-bench][ERROR] failed to read baseline proxy config" >&2
  exit 1
fi

if [[ "${BENCH_DISABLE_RATE_LIMIT}" == "1" ]]; then
  override_rate_limit_rules_for_benchmark
fi

mkdir -p "$(dirname "${OUTPUT_FILE}")"
{
  echo "# Proxy Tuning Benchmark"
  echo
  echo "- benchmark_tool: ab"
  echo "- requests_per_case: ${BENCH_REQUESTS}"
  echo "- warmup_requests_per_case: ${WARMUP_REQUESTS}"
  echo "- concurrency_levels: ${BENCH_CONCURRENCY}"
  echo "- benchmark_path: ${BENCH_PATH}"
  echo "- benchmark_timeout_sec: ${BENCH_TIMEOUT_SEC}"
  echo "- disable_rate_limit: ${BENCH_DISABLE_RATE_LIMIT}"
  echo "- host port: ${HOST_CORAZA_PORT}"
  echo "- listen port: ${WAF_LISTEN_PORT}"
  echo "- generated_at: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  echo
  echo "| preset | concurrency | complete | failed | non_2xx | fail_rate_pct | avg_latency_ms | p95_latency_ms | p99_latency_ms | rps |"
  echo "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"
  run_preset balanced
  run_preset low-latency
  run_preset buffered-guard
} | tee "${OUTPUT_FILE}"

if [[ "${benchmark_failed}" -ne 0 ]]; then
  echo "[proxy-bench][ERROR] benchmark thresholds were breached" >&2
  exit 1
fi

echo "[proxy-bench][OK] benchmark summary saved: ${OUTPUT_FILE}"
