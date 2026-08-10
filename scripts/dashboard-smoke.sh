#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"

if [[ "${1:-}" != "--skip-build" ]]; then
  make build
fi

smoke_root="$(mktemp -d /tmp/capsule-dashboard-smoke.XXXXXX)"
smoke_port="${CAPSULE_DASHBOARD_SMOKE_PORT:-18088}"
smoke_url="http://127.0.0.1:${smoke_port}"
history_root="${CAPSULE_DASHBOARD_SMOKE_HISTORY_ROOT:-$smoke_root/history}"
server_log="$smoke_root/server.log"
server_pid=""
curl_options=(--silent --fail --noproxy '*' --connect-timeout 1 --max-time 3)

cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill -INT "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

./bin/capsulectl dashboard --mock --listen "127.0.0.1:${smoke_port}" --root "$history_root" >"$server_log" 2>&1 &
server_pid=$!
for _ in $(seq 1 80); do
  if curl "${curl_options[@]}" "$smoke_url/healthz" >/dev/null; then
    break
  fi
  sleep 0.1
done
curl "${curl_options[@]}" "$smoke_url/healthz" >/dev/null

run_case() {
  local scenario="$1"
  local goal="$2"
  local workload="${3:-research}"
  local mode="${4:-mock}"
  local response run_id status saw_active=0 directory_field=""
  if [[ "$workload" == "experiment" ]]; then
    directory_field=',"experiment_directory":"examples/experiment"'
  fi
  response="$(curl "${curl_options[@]}" -H 'Content-Type: application/json' -d "{\"goal\":\"${goal}\",\"workload\":\"${workload}\",\"mode\":\"${mode}\",\"scenario\":\"${scenario}\",\"max_pdf_mb\":32${directory_field}}" "$smoke_url/api/runs")"
  printf '%s' "$response" | grep '"max_pdf_mb":32' >/dev/null
  run_id="$(printf '%s' "$response" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
  if [[ -z "$run_id" ]]; then
    printf 'FAIL: no run id for %s\n' "$scenario" >&2
    exit 1
  fi
  for _ in $(seq 1 600); do
    response="$(curl "${curl_options[@]}" "$smoke_url/api/runs/$run_id")"
    status="$(printf '%s' "$response" | grep -o '"status":"[A-Z]*"' | sed -n '1s/"status":"\([A-Z]*\)"/\1/p')"
    if [[ "$status" == "COMPLETED" ]]; then
	  if [[ "$workload" == "experiment" && "$saw_active" != "1" ]]; then
		printf 'FAIL: experiment %s never exposed a non-terminal execution state\n' "$run_id" >&2
		exit 1
	  fi
      printf 'PASS %-20s %s\n' "$scenario" "$run_id" >&2
      printf '%s' "$run_id"
      return
    fi
    if [[ "$status" == "FAILED" || "$status" == "CANCELLED" ]]; then
      printf 'FAIL %-20s %s\n%s\n' "$scenario" "$run_id" "$response" >&2
      exit 1
    fi
	if [[ "$status" == "PLANNING" || "$status" == "RUNNING" || "$status" == "OBSERVING" || "$status" == "REPLANNING" || "$status" == "SYNTHESIZING" ]]; then
	  saw_active=1
	fi
    sleep 0.1
  done
  printf 'FAIL: timeout waiting for %s\n' "$scenario" >&2
  exit 1
}

normal_id="$(run_case normal 'Produce a compact verified research summary.')"
replan_id="$(run_case search-replan 'Recover from insufficient initial evidence and produce a verified report.')"
guard_id="$(run_case evidence-rejection 'Reject unsupported claims and produce a citation-closed report.')"
experiment_id="$(run_case resource-replan 'Compare three classification methods and recover autonomously from resource limits.' experiment local)"
experiment_repeat_id="$(run_case resource-replan 'Compare three classification methods and recover autonomously from resource limits.' experiment local)"

if [[ "$experiment_id" == "$experiment_repeat_id" ]]; then
  printf 'FAIL: repeated experiment reused run id %s\n' "$experiment_id" >&2
  exit 1
fi

curl "${curl_options[@]}" "$smoke_url/api/runs/$normal_id/report?format=html" | grep '<h1' >/dev/null
curl "${curl_options[@]}" "$smoke_url/api/runs/$normal_id" | grep -E '"peak_parallel_agents":[1-9]' >/dev/null
curl "${curl_options[@]}" "$smoke_url/api/runs/$normal_id/papers" | grep -E '"quality":\{"status":"(READY|PARTIAL|INSUFFICIENT)"' >/dev/null
curl "${curl_options[@]}" "$smoke_url/api/runs/$replan_id/plan" | grep '"version":2' >/dev/null
curl "${curl_options[@]}" "$smoke_url/api/runs/$guard_id/evidence" | grep -E '"rejected_count":[1-9]' >/dev/null
curl "${curl_options[@]}" "$smoke_url/api/runs/$experiment_id" | grep '"workload":"experiment"' >/dev/null
curl "${curl_options[@]}" "$smoke_url/api/runs/$experiment_id" | grep '"best_method":"random_forest"' >/dev/null
curl "${curl_options[@]}" "$smoke_url/api/runs/$experiment_id" | grep '"failure_code":"MEMORY_LIMIT_EXCEEDED"' >/dev/null
curl "${curl_options[@]}" "$smoke_url/api/runs/$experiment_id" | grep '"experiment_directory":"examples/experiment"' >/dev/null
experiment_plan="$(curl "${curl_options[@]}" "$smoke_url/api/runs/$experiment_id/plan")"
printf '%s' "$experiment_plan" | grep '"version":3' >/dev/null
printf '%s' "$experiment_plan" | grep '"capability":"experiment.manifest.inspect"' >/dev/null
for event_kind in 'runtime.agent.dispatched' 'cognitive.observation.created' 'cognitive.replan.requested' 'cognitive.plan.revised' 'cognitive.goal.completed'; do
  grep "\"kind\":\"$event_kind\"" "$history_root/runs/$experiment_id/runtime-events.jsonl" >/dev/null
done
grep '"capability":"experiment.manifest.inspect"' "$history_root/runs/$experiment_id/runtime-events.jsonl" | grep '"manifest_sha256"' >/dev/null
grep '"agent_id":"random-forest-retry"' "$history_root/runs/$experiment_id/runtime-events.jsonl" | grep '"kind":"runtime.agent.dispatched"' >/dev/null
for fresh_id in "$experiment_id" "$experiment_repeat_id"; do
  grep '"planner_mode": "OFFLINE_MANIFEST_DRIVEN_LLM_FIXTURE"' "$history_root/runs/$fresh_id/experiment-summary.json" >/dev/null
  grep '"work_scale": 2000' "$history_root/runs/$fresh_id/experiment-summary.json" >/dev/null
  grep -E '"runtime_ms": [1-9][0-9]{2,}' "$history_root/runs/$fresh_id/experiment-summary.json" >/dev/null
  grep 'capsule-experiment.json' "$history_root/runs/$fresh_id/experiment_report.md" >/dev/null
done
first_transaction="$(sed -n 's/.*"output_transaction_id":"\([^"]*\)".*/\1/p' "$history_root/runs/$experiment_id/runtime-events.jsonl" | sed -n '1p')"
repeat_transaction="$(sed -n 's/.*"output_transaction_id":"\([^"]*\)".*/\1/p' "$history_root/runs/$experiment_repeat_id/runtime-events.jsonl" | sed -n '1p')"
if [[ -z "$first_transaction" || -z "$repeat_transaction" || "$first_transaction" == "$repeat_transaction" ]]; then
  printf 'FAIL: repeated experiment did not create independent output transactions\n' >&2
  exit 1
fi
index_html="$(curl "${curl_options[@]}" "$smoke_url/")"
printf '%s' "$index_html" | grep 'presentation-toggle' >/dev/null
printf '%s' "$index_html" | grep 'language-select' >/dev/null
printf '%s' "$index_html" | grep 'pdf-limit-select' >/dev/null
printf '%s' "$index_html" | grep 'agent-loop' >/dev/null
printf '%s' "$index_html" | grep 'runtime-proof' >/dev/null
printf '%s' "$index_html" | grep 'metric-quality' >/dev/null
printf '%s' "$index_html" | grep 'execution-stage' >/dev/null
styles="$(curl "${curl_options[@]}" "$smoke_url/styles.css")"
printf '%s' "$styles" | grep 'body:not(.has-run)' >/dev/null
javascript="$(curl "${curl_options[@]}" "$smoke_url/app.js")"
printf '%s' "$javascript" | grep 'URLSearchParams' >/dev/null
if printf '%s' "$javascript" | grep 'state.history\[0\]' >/dev/null; then
  printf 'FAIL: dashboard still auto-restores the latest persisted run\n' >&2
  exit 1
fi

printf 'Dashboard API/static smoke PASS\nArtifacts: %s/runs\n' "$history_root"
