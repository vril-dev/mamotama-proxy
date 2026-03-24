#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOGS_DIR="${ROOT_DIR}/data/logs"
TARGET_DIR="${LOGS_DIR}/proxy"

mkdir -p "${TARGET_DIR}"

migrated=0

for d in "${LOGS_DIR}"/*; do
  [[ -d "${d}" ]] || continue
  base="$(basename "${d}")"

  case "${base}" in
    coraza|gotestwaf|proxy)
      continue
      ;;
  esac

  for f in "${d}"/*.ndjson; do
    [[ -f "${f}" ]] || continue
    name="$(basename "${f}")"
    dest="${TARGET_DIR}/${name}"

    if [[ -f "${dest}" ]]; then
      cat "${f}" >>"${dest}"
      rm -f "${f}"
    else
      mv "${f}" "${dest}"
    fi

    migrated=$((migrated + 1))
    echo "[migrate-proxy-logs] migrated ${base}/${name} -> proxy/${name}"
  done

  # Cleanup legacy placeholders and empty directories.
  rm -f "${d}/empty"
  rmdir "${d}" 2>/dev/null || true
done

if [[ "${migrated}" -eq 0 ]]; then
  echo "[migrate-proxy-logs] no legacy ndjson files found"
else
  echo "[migrate-proxy-logs] migrated files: ${migrated}"
fi
