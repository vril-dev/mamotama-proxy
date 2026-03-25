#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib/proxy_api.sh
source "${SCRIPT_DIR}/lib/proxy_api.sh"

BENCH_REQUESTS="${BENCH_REQUESTS:-60}"
WARMUP_REQUESTS="${WARMUP_REQUESTS:-10}"
UPSTREAM_PORT="${UPSTREAM_PORT:-18080}"
OUTPUT_FILE="${OUTPUT_FILE:-data/logs/proxy/proxy-benchmark-summary.md}"

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
need_cmd awk
need_cmd sort
need_cmd python3

if [[ "${BENCH_REQUESTS}" -lt 5 ]]; then
  echo "[proxy-bench][ERROR] BENCH_REQUESTS must be >= 5" >&2
  exit 1
fi

proxy_api_init

tmp_dir="$(mktemp -d)"
upstream_pid=""
baseline_raw=""

cleanup() {
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

run_load() {
  local count="$1"
  local out_file="$2"
  : > "${out_file}"
  local i line status latency
  for i in $(seq 1 "${count}"); do
    line="$(curl -sS --max-time 10 -o /dev/null -w "%{http_code} %{time_total}" "${PROXY_BASE_URL}/bench" || true)"
    status="${line%% *}"
    latency="${line#* }"
    if [[ "${status}" != "200" ]]; then
      echo "[proxy-bench][ERROR] non-200 status during benchmark: ${status}" >&2
      return 1
    fi
    echo "${latency}" >> "${out_file}"
  done
}

calc_avg() {
  awk '{sum+=$1} END {if (NR>0) printf "%.4f", sum/NR; else print "0.0000"}' "$1"
}

calc_p95() {
  local file="$1"
  local n idx
  n="$(wc -l < "${file}")"
  if [[ "${n}" -eq 0 ]]; then
    echo "0.0000"
    return 0
  fi
  idx=$(( (n * 95 + 99) / 100 ))
  sort -n "${file}" | awk -v target="${idx}" 'NR==target {printf "%.4f", $1; exit}'
}

calc_rps() {
  awk -v avg="$1" 'BEGIN {if (avg > 0) printf "%.1f", (1/avg); else print "0.0"}'
}

build_preset_raw() {
  local base_raw="$1"
  local preset="$2"
  local upstream="http://host.docker.internal:${UPSTREAM_PORT}"
  case "${preset}" in
    balanced)
      jq --arg upstream "${upstream}" '
        .upstream_url = $upstream
        | .force_http2 = false
        | .disable_compression = false
        | .buffer_request_body = false
        | .max_response_buffer_bytes = 0
        | .flush_interval_ms = 0
        | .health_check_path = "/bench"
      ' <<<"${base_raw}"
      ;;
    low-latency)
      jq --arg upstream "${upstream}" '
        .upstream_url = $upstream
        | .force_http2 = true
        | .disable_compression = true
        | .buffer_request_body = false
        | .max_response_buffer_bytes = 0
        | .flush_interval_ms = 5
        | .health_check_path = "/bench"
      ' <<<"${base_raw}"
      ;;
    buffered-guard)
      jq --arg upstream "${upstream}" '
        .upstream_url = $upstream
        | .force_http2 = true
        | .disable_compression = false
        | .buffer_request_body = true
        | .max_response_buffer_bytes = 1048576
        | .flush_interval_ms = 25
        | .health_check_path = "/bench"
      ' <<<"${base_raw}"
      ;;
    *)
      echo "[proxy-bench][ERROR] unknown preset: ${preset}" >&2
      return 1
      ;;
  esac
}

run_preset() {
  local preset="$1"
  local preset_raw latency_file avg p95 rps

  preset_raw="$(build_preset_raw "${baseline_raw}" "${preset}")"
  apply_proxy_raw "${preset_raw}"

  run_load "${WARMUP_REQUESTS}" "${tmp_dir}/warmup_${preset}.txt"
  run_load "${BENCH_REQUESTS}" "${tmp_dir}/bench_${preset}.txt"

  latency_file="${tmp_dir}/bench_${preset}.txt"
  avg="$(calc_avg "${latency_file}")"
  p95="$(calc_p95 "${latency_file}")"
  rps="$(calc_rps "${avg}")"

  printf '| %s | %s | %s | %s |\n' "${preset}" "${avg}" "${p95}" "${rps}"
}

mkdir -p "${tmp_dir}/upstream"
cat > "${tmp_dir}/upstream/bench" <<'EOF'
ok
EOF

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

mkdir -p "$(dirname "${OUTPUT_FILE}")"
{
  echo "# Proxy Tuning Benchmark"
  echo
  echo "- requests: ${BENCH_REQUESTS}"
  echo "- warmup: ${WARMUP_REQUESTS}"
  echo "- host port: ${HOST_CORAZA_PORT}"
  echo "- listen port: ${WAF_LISTEN_PORT}"
  echo "- generated_at: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  echo
  echo "| preset | avg_latency_sec | p95_latency_sec | approx_rps |"
  echo "| --- | ---: | ---: | ---: |"
  run_preset balanced
  run_preset low-latency
  run_preset buffered-guard
} | tee "${OUTPUT_FILE}"

echo "[proxy-bench][OK] benchmark summary saved: ${OUTPUT_FILE}"
