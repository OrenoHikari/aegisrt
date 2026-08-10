# CAPSuleAgent Competition Demo Script

CAPSuleAgent separates cognition from execution: the LLM may reason and revise a plan, while every action is validated and executed by CAPSuleRT. The presentation motto is: **Reason freely. Execute safely. Verify deterministically.**

## Before presenting

```bash
make build
./bin/capsulectl dashboard --listen 127.0.0.1:8080
```

Open `http://127.0.0.1:8080`. Keep **Simple** enabled. A fresh page intentionally shows no old result; persisted runs appear only after an explicit **Recent runs** selection. REAL, MOCK, and LOCAL are always labeled in the header, and the Dashboard never silently changes execution mode.

## 60-second route

1. Use the hero architecture to state the boundary: the cognitive plane decides; CAPSuleRT schedules, constrains, and verifies every action.
2. Select **Autonomous Experiment Demo**. Edit the natural-language Goal if desired, keep `examples/experiment` as the workspace-relative experiment directory, and click **Run fresh experiment**. Point out the new Run ID and `FRESH EXECUTION` badge; a restored item is instead labeled `HISTORICAL SNAPSHOT`.
3. In the visible Task DAG, show Plan v1 running only the registered `experiment.manifest.inspect` Worker. It lists the real directory and strictly validates `capsule-experiment.json`; the resulting Observation contains workspace-relative paths, the Manifest SHA-256, and allowlisted method settings.
4. Read the adjacent structured Decision: discovery requests `REPLAN`, then Plan v2 reuses the verified Manifest inspection and schedules Dataset preparation plus the three configured experiment Workers.
5. The included Manifest requests Random Forest with `n_estimators=1000`, so Plan v2 produces a real Worker failure: `MEMORY_LIMIT_EXCEEDED` at 92 MiB against a 64 MiB budget. That structured failure becomes another Observation and requests a bounded second re-plan.
6. Switch to Plan v3. Point out that Manifest inspection, Dataset, Logistic Regression, and SVM outputs are `REUSED`; only Random Forest receives a new task ID and retries with `n_estimators=100`. If a user supplies an already-safe Manifest, Plan v2 completes directly and Plan v3 is correctly absent.
7. Use **Runtime proof** to show peak parallel workers, task-time overlap, reused outputs, and bounded recovery. These values come from Scheduler telemetry. Finish on the comparison chart: Worker runtime and every Scheduler/transaction event are fresh execution; Random Forest's 91% accuracy and the displayed working-set values are deterministic scenario fixtures, not ML benchmark measurements.
8. Expand **Technical evidence** only if a judge asks for raw Telemetry, Scheduler state, plan diff, Manifest provenance, or planner provenance.

Narration: “The Goal is editable and the configuration comes from a real workspace-relative directory, but execution is deliberately bounded. The Manifest may name one CSV dataset and exactly the three registered methods; it cannot contain shell commands, scripts, executables, modules, or environment variables. Every click creates new Worker processes and transactions. CAPSuleAgent first discovers and validates the configuration, then observes execution and revises only the invalid part of the DAG. Accuracy and working-set values remain repeatable fixtures; Worker CPU time, Scheduler lifecycle, failure, re-plan, reuse, and output verification are real.”

The accepted Manifest schema is intentionally small:

```json
{
  "version": 1,
  "dataset": "classification.csv",
  "methods": [
    {"method": "logistic_regression"},
    {"method": "random_forest", "n_estimators": 1000},
    {"method": "svm"}
  ]
}
```

The directory and Dataset must remain beneath the configured server-side workspace root. Absolute paths, parent traversal, symlink escape, unknown fields or methods, non-CSV data, and Random Forest values outside 1–1000 are rejected before experiment Workers run.

## 3-minute route

1. Run **Re-plan Demo** and explain the validated DAG and live CAPSuleRT Scheduler state.
2. Expand **Autonomous Decision**: Observation is what execution established; Reason is the bounded justification; Action is the next validated step. It is not hidden chain-of-thought.
3. Switch Plan versions and inspect the v1 → v2 diff.
4. Select **Evidence Guard** and run it. Expand a rejected finding to show the actual claim, source, section, producing task, reason code, and rejection reason.
5. Contrast the result tabs:
   - FACT: directly source-backed.
   - INFERENCE: derived from verified findings.
   - PROPOSAL: AI-generated recommendation, not a paper claim.
6. Show the measured Scheduler proof: peak parallelism and overlap savings demonstrate multi-Agent execution; PSI shows resource stall pressure, not utilization. Point out whether cgroup isolation was actually enabled.
7. Contrast citation closure with `READY / PARTIAL / INSUFFICIENT` answer completeness so a thin but correctly cited report is never oversold.

## 5-minute route

1. Start with the architecture and the rule that the LLM cannot invoke unrestricted tools or bypass CAPSuleRT.
2. Run **REC Research** in REAL mode. Show the REAL badge and the current action while arXiv retrieval, PDF parsing, LLM analysis, evidence verification, and synthesis execute.
3. While waiting, restore a completed run from **Recent runs** to demonstrate disk-backed history and replay.
4. Inspect paper parser diagnostics and canonical evidence anchors (`P1`, `P2`, …).
5. Return to the live run. Explain any structured failure category directly; do not conceal provider or parser failures.
6. If completed, open the full Markdown report and download the source. Emphasize citation closure and zero hallucinated references in the persisted summary.

## Fallback route

If the network, arXiv, PDF service, or REAL LLM is unavailable:

1. Leave the failed REAL run visible long enough to show its structured diagnostic; do not relabel it as MOCK.
2. Explicitly select **Re-plan Demo**. Say: “I am switching to a deterministic offline fixture that exercises the same Planner, Orchestrator, Scheduler, telemetry, evidence, and report presentation path.”
3. Run **Evidence Guard** next to prove unsupported material is excluded from FACT.
4. Restore the most recent successful REAL run from history for its actual papers, evidence, summary, and report.

## Optional local checks

```bash
make dashboard-smoke
make dashboard-screenshot
```

The smoke target runs Normal, Re-plan, Evidence Guard, and the manifest-driven local-real Autonomous Experiment discovery/recovery completely offline. The screenshot target also writes `dashboard-experiment.png` alongside the three Research captures at 1920×1080 under `var/dashboard/screenshots/` when Chromium/Chrome exists; otherwise it exits successfully with an explicit `SKIPPED` message and installs nothing.
