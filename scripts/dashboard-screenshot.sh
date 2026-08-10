#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"

browser=""
for candidate in chromium-browser chromium google-chrome google-chrome-stable; do
  if command -v "$candidate" >/dev/null 2>&1; then
    browser="$(command -v "$candidate")"
    break
  fi
done
if [[ -z "$browser" ]]; then
  printf 'SKIPPED: dashboard screenshots require Chromium or Google Chrome; no browser was found.\n'
  exit 0
fi

if [[ "${1:-}" != "--skip-build" ]]; then
  make build
fi

screenshot_root="${CAPSULE_DASHBOARD_SCREENSHOT_ROOT:-var/dashboard/screenshots}"
screenshot_port="${CAPSULE_DASHBOARD_SCREENSHOT_PORT:-18089}"
screenshot_url="http://127.0.0.1:${screenshot_port}"
mkdir -p "$screenshot_root"
server_log="$screenshot_root/server.log"
server_pid=""
curl_options=(--silent --fail --noproxy '*' --connect-timeout 1 --max-time 3)

cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill -INT "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

./bin/capsulectl dashboard --mock --listen "127.0.0.1:${screenshot_port}" --root "$screenshot_root/history" >"$server_log" 2>&1 &
server_pid=$!
for _ in $(seq 1 80); do
  curl "${curl_options[@]}" "$screenshot_url/healthz" >/dev/null && break
  sleep 0.1
done
curl "${curl_options[@]}" "$screenshot_url/healthz" >/dev/null

run_and_capture() {
  local scenario="$1"
  local goal="$2"
  local filename="$3"
  local workload="${4:-research}"
  local mode="${5:-mock}"
  local response run_id status
  response="$(curl "${curl_options[@]}" -H 'Content-Type: application/json' -d "{\"goal\":\"${goal}\",\"workload\":\"${workload}\",\"mode\":\"${mode}\",\"scenario\":\"${scenario}\"}" "$screenshot_url/api/runs")"
  run_id="$(printf '%s' "$response" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
  for _ in $(seq 1 600); do
    response="$(curl "${curl_options[@]}" "$screenshot_url/api/runs/$run_id")"
    status="$(printf '%s' "$response" | grep -o '"status":"[A-Z]*"' | sed -n '1s/"status":"\([A-Z]*\)"/\1/p')"
    [[ "$status" == "COMPLETED" ]] && break
    if [[ "$status" == "FAILED" || "$status" == "CANCELLED" ]]; then
      printf 'FAIL: screenshot scenario %s ended %s\n' "$scenario" "$status" >&2
      exit 1
    fi
    sleep 0.1
  done
  if [[ "$status" != "COMPLETED" ]]; then
    printf 'FAIL: timeout waiting for screenshot scenario %s\n' "$scenario" >&2
    exit 1
  fi
  "$browser" --headless --disable-gpu --no-sandbox --hide-scrollbars --window-size=1920,1080 --virtual-time-budget=3000 --screenshot="$screenshot_root/$filename" "$screenshot_url/?run=$run_id" >/dev/null 2>&1
  printf 'WROTE %s\n' "$screenshot_root/$filename"
}

run_and_capture normal 'Produce a compact verified research summary.' dashboard-overview.png
run_and_capture search-replan 'Recover from insufficient initial evidence and produce a verified report.' dashboard-replan.png
run_and_capture evidence-rejection 'Reject unsupported claims and produce a citation-closed report.' dashboard-evidence.png
run_and_capture resource-replan 'Compare three classification methods and recover autonomously from resource limits.' dashboard-experiment.png experiment local
