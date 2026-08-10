"use strict";

const state = {
  system: null, run: null, plan: {versions: []}, papers: [], queries: [], evidence: emptyEvidence(), quality: {},
  events: [], history: [], reportHTML: "", eventSource: null, refreshTimer: null,
  activeTab: "facts", selectedPlanVersion: null, followLatestPlan: true, timelineMode: "important",
  selectedPreset: "", presentation: storedPresentationMode(), language: storedLanguage(), runOrigin: "none", generation: 0, refreshSequence: 0
};
const byId = (id) => document.getElementById(id);
const terminal = (status) => ["COMPLETED", "FAILED", "CANCELLED"].includes(status);

const messages = {
  en: {
    brandSubtitle: "Autonomous execution on CAPSuleRT", innovationThink: "LLM decides", innovationExecute: "CAPSuleRT executes", innovationVerify: "Results verify",
    simpleView: "Presentation", recentRuns: "Recent runs", newRun: "NEW RUN", safeBoundary: "Every action stays inside a registered capability.", experimentScope: "Edit the comparison/report goal for the three registered simulations. Executable settings come only from capsule-experiment.json.", integrationStatus: "Cognitive services",
    registeredOnly: "Registered actions only", realWorkers: "Real local workers", boundedRecovery: "Bounded recovery", cognitivePlane: "COGNITIVE PLANE", cognitiveActions: "Plan · Observe · Re-plan", validatedBridge: "Validated DAG", executionPlane: "EXECUTION PLANE", executionActions: "Schedule · Isolate · Verify",
    productEyebrow: "AUTONOMY, WITH AN EXECUTION PLANE", productTitle: "Agents should do the work—not just describe it.", productDescription: "The model decides what to do. CAPSuleRT schedules real workers, enforces boundaries, verifies outputs, and turns failures into observations.",
    goalLabel: "What should the agent accomplish?", goalPlaceholder: "Describe a goal, comparison, and desired output…", experimentGoalPlaceholder: "Describe how to compare and report the three supported simulations…", experimentDirectory: "Experiment directory", experimentDirectoryPlaceholder: "examples/experiment", experimentDirectoryHelp: "Workspace-relative directory containing capsule-experiment.json. Absolute paths and parent traversal are rejected.", cancel: "Cancel", runResearch: "Run agent", runExperiment: "Run fresh experiment",
    runSettings: "Advanced settings", executionMode: "Execution mode", pdfBudget: "Per-paper PDF budget", pdfBudgetHelp: "Oversized PDFs are skipped and replaced safely. Raise this only when you choose to accept the extra memory and parsing cost.",
    diagnostic: "EXECUTION SIGNAL", coreInnovation: "CORE CONTRIBUTION", agentLoopTitle: "Adaptive execution, backed by a real runtime", loopWaiting: "Waiting for a goal.",
    stepGoal: "Goal", stepGoalHint: "Natural language", stepPlan: "Plan", stepPlanHint: "Validated DAG", stepExecute: "Execute", stepExecuteHint: "CAPSuleRT", stepObserve: "Observe", stepObserveHint: "Real environment", stepDecide: "Decide", stepDecideHint: "Finish or re-plan", replanReturn: "Re-plan reuses valid work",
    agentDecision: "AGENT DECISION", observation: "Observation", reason: "Reason", nextAction: "Next action", outcome: "OUTCOME", atAGlance: "At a glance",
    papers: "Papers", analyzed: "Analyzed", replans: "Re-plans", reused: "Reused", citationClosure: "Citation closure", closureHint: "Only verified claims enter the final report.", candidates: "candidates", located: "located", supported: "supported", rejected: "rejected",
    measuredAdvantage: "RUNTIME PROOF", runtimeProofTitle: "What CAPSuleRT contributed to this run", telemetryProof: "Measured from Scheduler telemetry—not marketing copy.", peakParallel: "Peak parallel workers", overlapFactor: "Work overlap", recoveredWork: "Verified outputs reused", evidenceGate: "Verification gate", resourceAware: "Resource-aware scheduling", answerCoverage: "Answer completeness", coverageHint: "Citation integrity and answer coverage are evaluated separately.",
    verifiedOutput: "FINAL ARTIFACT", finalReport: "Verified report", downloadMarkdown: "Download Markdown", technicalDetails: "TECHNICAL EVIDENCE", inspectSystem: "Inspect telemetry, Scheduler state, evidence, and history",
    validatedDAG: "Real Task DAG", telemetry: "TELEMETRY", liveActivity: "Live activity", important: "Important", allEvents: "All events", autoScroll: "Auto scroll", runSummary: "RUN SUMMARY", researchResult: "Result", runtimeScheduler: "Runtime & Scheduler", evidenceInspect: "Evidence inspect", corpus: "RESEARCH CORPUS", papersDiagnostics: "Papers & parser diagnostics", pipelineDetail: "PIPELINE", researchProgress: "Research progress", persistedHistory: "PERSISTED",
    helpTitle: "From goal to verified execution", helpPlan: "Understand & Plan", helpPlanText: "The LLM creates a capability-validated Task DAG.", helpExecute: "Execute with CAPSuleRT", helpExecuteText: "The Scheduler runs registered capabilities with dependency and resource control.", helpAdapt: "Observe & Re-plan", helpAdaptText: "Real outcomes revise the DAG while verified work is reused.", helpVerify: "Verify outputs", helpVerifyText: "Only validated artifacts can enter the final result.", motto: "Reason freely. Execute safely. Verify deterministically.",
    presetExperiment: "Autonomous Experiment", presetResearch: "Real Research", presetReplan: "Re-plan Demo", presetGuard: "Evidence Guard",
    presetExperimentDescription: "Inspect local settings · CPU workers · re-plan · retry", presetResearchDescription: "DeepSeek · arXiv · PDF · verified evidence", presetReplanDescription: "Offline deterministic Plan v1 → Plan v2", presetGuardDescription: "Offline unsupported-claim rejection",
    checking: "checking", online: "online", offline: "offline", configured: "configured", notConfigured: "not configured", available: "available", unavailable: "unavailable", freshExecution: "FRESH EXECUTION", historicalSnapshot: "HISTORICAL SNAPSHOT", creatingExecution: "creating new run",
    chooseGoal: "Choose an example or enter a goal.", waiting: "Waiting", noPresets: "No examples configured", selected: "selected", noRuns: "No persisted runs yet.", historyRestored: "Persisted run restored. This is historical output, not a new execution.",
    planWaiting: "The validated plan will appear with planning telemetry.", telemetryWaiting: "Waiting for important telemetry.", reportWaiting: "The verified report will appear after completion.", summaryWaiting: "Summary appears as real run data becomes available.",
    loopPlanning: "The LLM is turning the goal into a capability-validated DAG.", loopExecuting: "CAPSuleRT is scheduling registered capabilities; the LLM cannot bypass it.", loopObserving: "The agent is reading bounded, structured results from the real execution environment.", loopReplanning: "The plan changed because reality differed from its assumptions; verified work is being reused.", loopSynthesizing: "Evidence-backed results are being synthesized and citation-checked.", loopCompleted: "The goal completed with verified outputs and citation closure.", loopFailed: "The loop stopped safely and exposed the blocking condition.",
    noObservation: "No execution observation yet.", noDecision: "No structured decision has been emitted.", waitPlan: "Wait for a validated plan.", goalSatisfied: "The verified execution satisfied the research goal.",
    allVerified: "All required task outputs and citation closure were verified.", validationFailed: "Execution could not satisfy a required validation condition.", publishReport: "Publish the verified report.", stopSafely: "Stop safely and surface diagnostics.", executeRevised: "Execute the revised validated DAG.",
    experimentOutcome: "VERIFIED WORKER OUTPUT", experimentComparison: "Method comparison and autonomous recovery", experimentSubtitle: "Autonomous execution on CAPSuleRT", resultVerify: "Results verify", experimentResult: "Experiment result", experimentReport: "Final experiment report", failureRecovery: "Failure recovery", experimentDisclosure: "Bounded Logistic Regression, Random Forest, and SVM simulation driven by capsule-experiment.json. Directory inspection, Scheduler, Worker processes, CPU work, failure and retry execute fresh; this is not arbitrary ML training.",
    method: "Method", accuracy: "Accuracy", workerRuntime: "Worker runtime", memoryPeak: "Working set / budget", attempt: "Attempt", state: "State",
    recentOption: "Recent runs", unavailableValue: "Unavailable"
  },
  "zh-CN": {
    brandSubtitle: "运行于 CAPSuleRT 的自主执行智能体", innovationThink: "LLM 决策", innovationExecute: "CAPSuleRT 执行", innovationVerify: "结果验证",
    simpleView: "演示", recentRuns: "最近任务", newRun: "新任务", safeBoundary: "所有行动都只能通过已注册 Capability 执行。", experimentScope: "目标可编辑，但仅用于三种已注册模拟的比较与报告；可执行设置只来自 capsule-experiment.json。", integrationStatus: "认知服务状态",
    registeredOnly: "只执行注册能力", realWorkers: "真实本地 Worker", boundedRecovery: "有边界的自主恢复", cognitivePlane: "认知平面", cognitiveActions: "规划 · 观察 · 重规划", validatedBridge: "已验证 DAG", executionPlane: "执行平面", executionActions: "调度 · 隔离 · 验证",
    productEyebrow: "有执行平面的自主智能体", productTitle: "让 Agent 真正完成工作，而不只是生成文字。", productDescription: "模型决定做什么；CAPSuleRT 调度真实 Worker、约束资源、验证输出，并把失败转化为下一轮决策的 Observation。",
    goalLabel: "你希望 Agent 完成什么？", goalPlaceholder: "描述目标、比较维度和期望输出……", experimentGoalPlaceholder: "描述如何比较并报告三种受支持的模拟方法……", experimentDirectory: "实验目录", experimentDirectoryPlaceholder: "examples/experiment", experimentDirectoryHelp: "workspace 内的相对目录，且必须包含 capsule-experiment.json；绝对路径和上级目录跳转会被拒绝。", cancel: "取消", runResearch: "运行任务", runExperiment: "全新执行实验",
    runSettings: "高级设置", executionMode: "执行模式", pdfBudget: "单篇 PDF 预算", pdfBudgetHelp: "超限 PDF 会被安全跳过并自动选择替代论文。只有在你愿意承担额外内存与解析开销时才提高额度。",
    diagnostic: "执行信号", coreInnovation: "核心贡献", agentLoopTitle: "由真实 Runtime 支撑的自适应执行", loopWaiting: "等待任务目标。",
    stepGoal: "目标", stepGoalHint: "自然语言", stepPlan: "规划", stepPlanHint: "验证 DAG", stepExecute: "执行", stepExecuteHint: "CAPSuleRT", stepObserve: "观察", stepObserveHint: "真实环境", stepDecide: "决策", stepDecideHint: "完成或重规划", replanReturn: "重规划会复用有效结果",
    agentDecision: "智能体决策", observation: "观察到什么", reason: "为什么", nextAction: "下一步", outcome: "当前结果", atAGlance: "一眼看懂",
    papers: "论文", analyzed: "已分析", replans: "重规划", reused: "复用任务", citationClosure: "引用闭环", closureHint: "只有可验证的结论才能进入最终报告。", candidates: "候选", located: "已定位", supported: "已支持", rejected: "已拒绝",
    measuredAdvantage: "运行时证明", runtimeProofTitle: "CAPSuleRT 在这次运行中贡献了什么", telemetryProof: "数据来自 Scheduler Telemetry，而不是宣传文案。", peakParallel: "峰值并行 Worker", overlapFactor: "任务时间重叠", recoveredWork: "复用的有效输出", evidenceGate: "验证关卡", resourceAware: "资源感知调度", answerCoverage: "回答完整度", coverageHint: "引用可信度与回答覆盖度会分别评估。",
    verifiedOutput: "最终产物", finalReport: "经过验证的报告", downloadMarkdown: "下载 Markdown", technicalDetails: "技术证据", inspectSystem: "查看 Telemetry、Scheduler 状态、证据与历史",
    validatedDAG: "真实任务 DAG", telemetry: "遥测事件", liveActivity: "实时活动", important: "关键事件", allEvents: "全部事件", autoScroll: "自动滚动", runSummary: "运行摘要", researchResult: "结果", runtimeScheduler: "Runtime 与 Scheduler", evidenceInspect: "证据审查", corpus: "研究语料", papersDiagnostics: "论文与解析诊断", pipelineDetail: "流水线", researchProgress: "调研进度", persistedHistory: "已持久化",
    helpTitle: "从目标到经过验证的执行", helpPlan: "理解并规划", helpPlanText: "LLM 生成经过 Capability 验证的任务 DAG。", helpExecute: "由 CAPSuleRT 执行", helpExecuteText: "Scheduler 在依赖和资源约束下运行已注册能力。", helpAdapt: "观察并重规划", helpAdaptText: "真实结果修订 DAG，同时复用经过验证的工作。", helpVerify: "验证输出", helpVerifyText: "只有经过验证的产物才能进入最终结果。", motto: "自由推理，安全执行，确定性验证。",
    presetExperiment: "自主实验", presetResearch: "真实科研调研", presetReplan: "重规划演示", presetGuard: "证据门控",
    presetExperimentDescription: "读取本地设置 · CPU Worker · 重规划 · 重试", presetResearchDescription: "DeepSeek · arXiv · PDF · 证据验证", presetReplanDescription: "离线确定性 Plan v1 → Plan v2", presetGuardDescription: "离线展示无证据结论拦截",
    checking: "检查中", online: "在线", offline: "离线", configured: "已配置", notConfigured: "未配置", available: "可用", unavailable: "不可用", freshExecution: "本次全新执行", historicalSnapshot: "历史快照", creatingExecution: "正在创建新任务",
    chooseGoal: "选择示例或输入任务目标。", waiting: "等待中", noPresets: "暂无示例", selected: "已选择", noRuns: "暂无历史任务。", historyRestored: "已恢复持久化历史任务；这是历史结果，并非一次新执行。",
    planWaiting: "规划完成后，这里会显示经过验证的 DAG。", telemetryWaiting: "等待关键遥测事件。", reportWaiting: "任务完成后，这里会显示经过验证的报告。", summaryWaiting: "真实运行数据产生后显示摘要。",
    loopPlanning: "LLM 正在把目标转化为经过 Capability 验证的 DAG。", loopExecuting: "CAPSuleRT 正在调度已注册能力；LLM 无法绕过执行平面。", loopObserving: "Agent 正在读取真实执行环境返回的有界结构化结果。", loopReplanning: "现实与原假设不符，Agent 正在复用有效结果并调整计划。", loopSynthesizing: "正在综合证据并验证引用闭环。", loopCompleted: "目标已通过可验证输出与引用闭环完成。", loopFailed: "闭环已安全停止，并给出阻塞原因。",
    noObservation: "尚无执行观察。", noDecision: "尚未产生结构化决策。", waitPlan: "等待经过验证的计划。", goalSatisfied: "经过验证的执行结果已经满足研究目标。",
    allVerified: "所有必要任务输出和引用闭环均已验证。", validationFailed: "执行未能满足必要的验证条件。", publishReport: "发布经过验证的报告。", stopSafely: "安全停止并显示诊断。", executeRevised: "执行修订后且经过验证的 DAG。",
    experimentOutcome: "经过验证的 Worker 输出", experimentComparison: "方法对比与自主恢复", experimentSubtitle: "运行于 CAPSuleRT 的自主执行智能体", resultVerify: "结果验证", experimentResult: "实验结果", experimentReport: "最终实验报告", failureRecovery: "失败恢复", experimentDisclosure: "这是由 capsule-experiment.json 驱动的 Logistic Regression、Random Forest 与 SVM 有界模拟。目录检查、Scheduler、Worker 进程、CPU 计算、失败与重试会真实执行，但不是任意机器学习训练。",
    method: "方法", accuracy: "准确率", workerRuntime: "Worker 运行时间", memoryPeak: "Worker 工作集 / 预算", attempt: "执行轮次", state: "状态",
    recentOption: "最近任务", unavailableValue: "不可用"
  }
};

function t(key) { return messages[state.language]?.[key] || messages.en[key] || key; }

document.addEventListener("DOMContentLoaded", async () => {
  bindControls();
  applyLanguage();
  applyPresentationMode();
  await loadSystem();
  await loadHistory();
  const requestedRun = new URLSearchParams(window.location.search).get("run");
  if (requestedRun) await selectRun(requestedRun);
  else renderAll();
  window.setInterval(() => { if (state.run && !terminal(state.run.status)) scheduleRefresh(); }, 1000);
});

function bindControls() {
  byId("run-button").addEventListener("click", createRun);
  byId("cancel-button").addEventListener("click", cancelRun);
  byId("presentation-toggle").addEventListener("change", () => {
    state.presentation = byId("presentation-toggle").checked;
    try { window.localStorage.setItem("capsule-dashboard-view", state.presentation ? "presentation" : "developer"); } catch (_) { /* optional preference */ }
    applyPresentationMode(); renderTimeline();
  });
  byId("language-select").addEventListener("change", () => {
    state.language = byId("language-select").value;
    try { window.localStorage.setItem("capsule-dashboard-language", state.language); } catch (_) { /* optional preference */ }
    applyLanguage(); renderAll();
  });
  byId("pdf-limit-select").addEventListener("change", updateBudgetSummary);
  byId("timeline-presentation").addEventListener("click", () => setTimelineMode("important"));
  byId("timeline-developer").addEventListener("click", () => setTimelineMode("all"));
  byId("history-select").addEventListener("change", (event) => { if (event.target.value) selectRun(event.target.value); });
  byId("help-button").addEventListener("click", () => byId("help-panel").classList.remove("hidden"));
  byId("help-close").addEventListener("click", () => byId("help-panel").classList.add("hidden"));
  byId("help-panel").addEventListener("click", (event) => { if (event.target === byId("help-panel")) byId("help-panel").classList.add("hidden"); });
  document.querySelectorAll("input[name=mode]").forEach((node) => node.addEventListener("change", () => { state.selectedPreset = ""; updateScenarioControl(); renderPresets(); }));
  document.querySelectorAll(".tab").forEach((node) => node.addEventListener("click", () => {
    state.activeTab = node.dataset.tab;
    document.querySelectorAll(".tab").forEach((tab) => tab.classList.toggle("active", tab === node));
    renderResults();
  }));
}

async function loadSystem() {
  try {
    state.system = await api("/api/status");
    renderSystemStatus();
    const maximum = state.system.maximum_max_pdf_mb || 64, preferred = state.system.default_max_pdf_mb || 32;
    const choices = [20, 32, 48, 64].filter(value => value <= maximum);
    if (!choices.includes(preferred)) choices.push(preferred);
    byId("pdf-limit-select").innerHTML = [...new Set(choices)].sort((a, b) => a - b).map(value => `<option value="${value}">${value} MiB</option>`).join("");
    byId("pdf-limit-select").value = String(preferred); updateBudgetSummary();
    const mode = state.system.default_mode || "mock";
    const radio = document.querySelector(`input[name=mode][value="${mode}"]`);
    if (radio) radio.checked = true;
    const initialPreset = state.system.presets?.find(preset => preset.mode === mode) || state.system.presets?.[0];
    if (initialPreset) applyPreset(initialPreset.id, false);
    else if (state.system.preset_goal) byId("goal-input").value = state.system.preset_goal;
    updateScenarioControl(); renderPresets();
  } catch (error) { showNotice(error.message); }
}

function renderPresets() {
  const target = byId("preset-buttons");
  const presets = state.system?.presets || [];
  if (!presets.length) { target.innerHTML = `<span class="muted-text">${escapeHTML(t("noPresets"))}</span>`; return; }
  target.innerHTML = presets.map(preset => `<button class="preset-button ${state.selectedPreset === preset.id ? "active" : ""}" data-preset="${escapeHTML(preset.id)}" title="${escapeHTML(presetDescription(preset))}" type="button"><strong>${escapeHTML(presetName(preset))}</strong><span>${escapeHTML(preset.mode.toUpperCase())}</span></button>`).join("");
  target.querySelectorAll("[data-preset]").forEach(node => node.addEventListener("click", () => applyPreset(node.dataset.preset)));
}

function presetName(preset) {
  const key = {"autonomous-experiment": "presetExperiment", "rec-research": "presetResearch", "replan-demo": "presetReplan", "evidence-guard": "presetGuard"}[preset.id];
  return key ? t(key) : preset.name;
}

function presetDescription(preset) {
  const key = {"autonomous-experiment": "presetExperimentDescription", "rec-research": "presetResearchDescription", "replan-demo": "presetReplanDescription", "evidence-guard": "presetGuardDescription"}[preset.id];
  return key ? t(key) : (preset.description || preset.name);
}

function applyPreset(id, announce = true) {
  const preset = state.system?.presets?.find(item => item.id === id);
  if (!preset) return;
  if (announce && state.run && !terminal(state.run.status)) {
    showNotice(state.language === "zh-CN" ? "当前任务仍在执行；请先取消或等待完成。" : "A run is still active; cancel it or wait for completion first.");
    return;
  }
  if (announce) clearLoadedRun();
  state.selectedPreset = preset.id;
  byId("goal-input").value = preset.goal;
  byId("experiment-directory-input").value = preset.workload === "experiment" ? (preset.experiment_directory || "examples/experiment") : "examples/experiment";
  if (preset.mode !== "local") {
    const mode = document.querySelector(`input[name=mode][value="${preset.mode}"]`);
    if (mode) mode.checked = true;
  }
  if (preset.scenario) byId("scenario-select").value = preset.scenario;
  updateScenarioControl(); renderPresets();
  if (announce) { hideNotice(); renderAll(); }
}

async function loadHistory() {
  try {
    const data = await api("/api/runs");
    state.history = data.runs || [];
    renderHistory();
  } catch (error) { showNotice(error.message); }
}

async function createRun() {
  hideNotice();
  const goal = byId("goal-input").value.trim();
  const preset = state.system?.presets?.find(item => item.id === state.selectedPreset);
  const workload = preset?.workload || state.run?.workload || "research";
  let mode = document.querySelector("input[name=mode]:checked")?.value || "mock";
  let scenario = byId("scenario-select").value;
  const experimentDirectory = workload === "experiment" ? byId("experiment-directory-input").value.trim() : "";
  if (workload === "experiment" && !experimentDirectory) {
    showNotice(state.language === "zh-CN" ? "请输入 workspace 内的相对实验目录。" : "Enter an experiment directory relative to the configured workspace root.");
    byId("experiment-directory-input").focus();
    return;
  }
  if (workload === "experiment") { mode = "local"; scenario = "resource-replan"; }
  if (workload === "research" && mode === "real" && !state.system?.llm_configured) {
    const accepted = window.confirm("The real LLM is not configured. Switch explicitly to the deterministic Mock normal demo?");
    if (!accepted) { showNotice("REAL run was not started. Configure the LLM or select a clearly marked MOCK preset."); return; }
    mode = "mock"; scenario = "normal";
    const mock = document.querySelector('input[name=mode][value="mock"]'); if (mock) mock.checked = true;
    updateScenarioControl(); showNotice("Mode explicitly switched to MOCK; it will not be presented as a real run.", "info");
  }
  clearLoadedRun();
  const generation = state.generation;
  state.runOrigin = "fresh";
  state.run = {id: "", goal, workload, mode, experiment_directory: experimentDirectory, status: "PLANNING", duration_ms: 0, created_at: new Date().toISOString(), runtime: {}, progress: {}};
  renderAll();
  byId("run-button").disabled = true;
  try {
    const maxPDFMB = Number(byId("pdf-limit-select").value);
    const run = await api("/api/runs", { method: "POST", body: JSON.stringify({goal, workload, mode, scenario, experiment_directory: experimentDirectory, max_pdf_mb: maxPDFMB}) });
    if (generation !== state.generation) return;
    state.run = run;
    window.history.replaceState(null, "", `?run=${encodeURIComponent(run.id)}`);
    connectEvents(run.id, generation); await loadHistory();
    if (sameRun(generation, run.id)) renderAll();
  } catch (error) {
    if (generation !== state.generation) return;
    state.run = null; state.runOrigin = "none";
    window.history.replaceState(null, "", window.location.pathname);
    renderAll(); showNotice(error.message);
  } finally {
    if (generation === state.generation) byId("run-button").disabled = Boolean(state.run && !terminal(state.run.status));
  }
}

async function cancelRun() {
  if (!state.run?.id || terminal(state.run.status)) return;
  const generation = state.generation, id = state.run.id;
  try {
    const run = await api(`/api/runs/${encodeURIComponent(id)}/cancel`, {method: "POST"});
    if (!sameRun(generation, id)) return;
    state.run = run; scheduleRefresh(true);
  } catch (error) { if (sameRun(generation, id)) showNotice(error.message); }
}

async function selectRun(id) {
  if (state.run && !terminal(state.run.status)) {
    showNotice(state.language === "zh-CN" ? "当前任务仍在执行；请先取消或等待完成。" : "A run is still active; cancel it or wait for completion first.");
    return;
  }
  clearLoadedRun();
  const generation = state.generation;
  state.runOrigin = "history";
  renderAll();
  try {
    const run = await api(`/api/runs/${encodeURIComponent(id)}`);
    if (generation !== state.generation) return;
    state.run = run;
    byId("goal-input").value = state.run.goal || "";
    byId("experiment-directory-input").value = state.run.experiment_directory || "examples/experiment";
    const matchingExperiment = state.run.workload === "experiment"
      ? state.system?.presets?.find(item => item.workload === "experiment")
      : null;
    state.selectedPreset = matchingExperiment?.id || "";
    if (["real", "mock"].includes(state.run.mode)) {
      const mode = document.querySelector(`input[name=mode][value="${state.run.mode}"]`);
      if (mode) mode.checked = true;
    }
    if (state.run.scenario) byId("scenario-select").value = state.run.scenario;
    if (state.run.max_pdf_mb) { byId("pdf-limit-select").value = String(state.run.max_pdf_mb); updateBudgetSummary(); }
    updateScenarioControl();
    await refreshDetails(generation, id);
    if (!sameRun(generation, id)) return;
    if (!terminal(state.run.status)) connectEvents(id, generation);
    byId("history-select").value = id;
    window.history.replaceState(null, "", `?run=${encodeURIComponent(id)}`);
    showNotice(t("historyRestored"), "info");
  } catch (error) {
    if (generation !== state.generation) return;
    state.runOrigin = "none"; renderAll(); showNotice(error.message);
  }
}

function connectEvents(id, generation = state.generation) {
  if (state.eventSource) state.eventSource.close();
  const source = new EventSource(`/api/runs/${encodeURIComponent(id)}/events`);
  state.eventSource = source;
  source.addEventListener("telemetry", (message) => {
    if (!sameRun(generation, id)) { source.close(); return; }
    try {
      const event = JSON.parse(message.data);
      if (!state.events.some((item) => item.sequence === event.sequence)) state.events.push(event);
      renderTimeline(); renderCurrentAction(); scheduleRefresh();
    } catch (_) { showNotice("A malformed telemetry event was ignored."); }
  });
  source.onerror = () => { if (!sameRun(generation, id) || terminal(state.run.status)) source.close(); };
}

function scheduleRefresh(immediate = false) {
  if (state.refreshTimer) return;
  const generation = state.generation, id = state.run?.id || "";
  state.refreshTimer = window.setTimeout(async () => {
    state.refreshTimer = null;
    if (sameRun(generation, id)) await refreshDetails(generation, id);
  }, immediate ? 0 : isExperiment() ? 80 : 350);
}

async function refreshDetails(generation = state.generation, runID = state.run?.id) {
  if (!runID || !sameRun(generation, runID)) return;
  const refreshSequence = ++state.refreshSequence;
  const id = encodeURIComponent(runID);
  try {
    const [run, plan, paperData, evidence] = await Promise.all([
      api(`/api/runs/${id}`), api(`/api/runs/${id}/plan`), api(`/api/runs/${id}/papers`), api(`/api/runs/${id}/evidence`)
    ]);
    if (!sameRun(generation, runID) || refreshSequence !== state.refreshSequence) return;
    state.run = run; state.plan = plan; state.papers = paperData.papers || []; state.queries = paperData.query_history || []; state.evidence = evidence || emptyEvidence(); state.quality = paperData.quality || run.summary?.report?.quality || {};
    const versions = state.plan.versions || [];
    if (state.followLatestPlan || !versions.some(version => version.version === state.selectedPlanVersion)) state.selectedPlanVersion = versions.at(-1)?.version || null;
    if (terminal(run.status)) {
      if (state.eventSource) { state.eventSource.close(); state.eventSource = null; }
      if (run.status === "COMPLETED") await loadReport(runID, generation, refreshSequence);
      if (!sameRun(generation, runID) || refreshSequence !== state.refreshSequence) return;
      await loadHistory();
      if (!sameRun(generation, runID) || refreshSequence !== state.refreshSequence) return;
    }
    renderAll();
  } catch (error) { if (sameRun(generation, runID)) showNotice(error.message); }
}

async function loadReport(id, generation = state.generation, refreshSequence = state.refreshSequence) {
  try {
    const response = await fetch(`/api/runs/${encodeURIComponent(id)}/report?format=html`, {cache: "no-store"});
    if (!response.ok) return;
    const report = await response.text();
    if (sameRun(generation, id) && refreshSequence === state.refreshSequence) state.reportHTML = report;
  } catch (_) { /* run state already carries report failure */ }
}

function renderAll() {
  const experiment = isExperiment();
  byId("goal-input").readOnly = false;
  byId("goal-input").placeholder = experiment ? t("experimentGoalPlaceholder") : t("goalPlaceholder");
  byId("experiment-directory-control").classList.toggle("hidden", !experiment);
  document.body.classList.toggle("has-run", Boolean(state.run));
  document.body.classList.toggle("experiment-run", experiment);
  document.body.dataset.runStatus = state.run?.status || "IDLE";
  byId("experiment-panel").classList.toggle("hidden", !experiment);
  byId("run-button-label").textContent = experiment ? t("runExperiment") : t("runResearch");
  byId("composer-note").textContent = experiment ? t("experimentScope") : t("safeBoundary");
  byId("brand-subtitle").textContent = experiment ? t("experimentSubtitle") : t("brandSubtitle");
  byId("innovation-verify").textContent = experiment ? t("resultVerify") : t("innovationVerify");
  byId("proof-gate-label").textContent = experiment ? t("failureRecovery") : t("evidenceGate");
  byId("summary-title").textContent = experiment ? t("experimentResult") : t("researchResult");
  byId("report-title").textContent = experiment ? t("experimentReport") : t("finalReport");
  renderPresets(); renderBanner(); renderFailure(); renderMetrics(); renderDAG(); renderDecision(); renderTimeline(); renderHistory();
  renderProgress(); renderSummary(); renderExperiment(); renderPapers(); renderFindings(); renderResults(); renderReport(); renderCurrentAction(); renderLoop();
}

function renderBanner() {
  const run = state.run, mode = run?.mode?.toUpperCase() || "NO RUN";
  ["mode-badge", "header-mode"].forEach(id => { byId(id).textContent = mode; byId(id).className = `mode-badge ${run?.mode || ""}`; });
  byId("run-status").textContent = run?.status || "IDLE";
  const origin = state.runOrigin === "history" ? t("historicalSnapshot") : t("freshExecution");
  setText("run-origin", run ? origin : "");
  byId("run-origin").className = `run-origin ${state.runOrigin}`;
  const runID = run?.id || "";
  setText("run-id", runID ? shortRunID(runID) : run ? t("creatingExecution") : "");
  byId("run-id").title = runID;
  byId("run-goal").textContent = run?.goal || t("chooseGoal");
  byId("run-duration").textContent = run ? duration(run.duration_ms) : "—";
  byId("cancel-button").classList.toggle("hidden", !run?.id || terminal(run.status));
  byId("run-button").disabled = Boolean(run && !terminal(run.status));
  if (run?.error) showNotice(run.error);
}

function renderFailure() {
  const panel = byId("failure-panel"), failure = state.run?.failures?.[0];
  if (!failure) { panel.classList.add("hidden"); return; }
  setText("failure-code", `${failure.recovered ? "RECOVERED · " : ""}${failure.code}`); setText("failure-message", failure.message);
  panel.classList.toggle("recovered", Boolean(failure.recovered));
  panel.classList.remove("hidden");
}

function renderMetrics() {
  const runtime = state.run?.runtime || {}, progress = state.run?.progress || {};
  const completed = runtime.succeeded_tasks ?? 0, failed = runtime.failed_tasks ?? 0, active = runtime.active_agents ?? 0;
  setText("metric-active", active); setText("metric-running", runtime.running_tasks ?? 0); setText("metric-queue", runtime.scheduler_queue ?? 0);
  setText("metric-completed", completed); setText("metric-failed", failed); setText("metric-scheduler", runtime.scheduled_tasks ?? completed + failed + active);
  setText("metric-cpu", runtime.cpu_pressure_avg10 == null ? "—" : `${number(runtime.cpu_pressure_avg10, 2)}%`);
  setText("metric-memory", runtime.memory_pressure_avg10 == null ? "—" : `${number(runtime.memory_pressure_avg10, 2)}%`);
  setText("metric-papers", progress.retrieved_papers ?? 0); setText("metric-analyzed", progress.analyzed_papers ?? 0);
  setText("metric-replans", progress.replans ?? 0); setText("metric-calls", progress.llm_calls ?? 0);
  const reused = state.plan?.versions?.at(-1)?.tasks?.filter(task => task.change === "REUSED").length || 0;
  setText("metric-reused", reused);
  setText("metric-candidates", progress.candidate_findings ?? 0); setText("metric-located", progress.source_verified ?? 0);
  setText("metric-supported", progress.supported_findings ?? 0); setText("metric-rejected", progress.rejected_findings ?? 0);
  setText("metric-closure", progress.citation_closure == null ? "—" : progress.citation_closure ? "PASS" : "FAIL");
  renderMeasuredAdvantages(runtime, progress, reused);
  setText("runtime-live", state.system?.runtime_online ? t("online") : t("offline"));
}

function renderMeasuredAdvantages(runtime, progress, reused) {
  const peak = runtime.peak_parallel_agents || 0, overlap = runtime.average_parallelism || 0;
  setText("proof-parallel", peak ? (state.language === "zh-CN" ? `${peak} 个` : `${peak} workers`) : "—");
  setText("proof-parallel-note", state.language === "zh-CN" ? `${runtime.executed_tasks || 0} 个任务经过 CAPSuleRT Scheduler 实际执行。` : `${runtime.executed_tasks || 0} tasks were actually executed by the CAPSuleRT Scheduler.`);
  setText("proof-overlap", overlap ? `${number(overlap, 2)}×` : "—");
  setText("proof-overlap-note", runtime.parallel_saved_ms > 0
    ? (state.language === "zh-CN" ? `并行重叠了 ${duration(runtime.parallel_saved_ms)} 的串行任务时间。` : `${duration(runtime.parallel_saved_ms)} of serial task time overlapped.`)
    : (state.language === "zh-CN" ? "当前任务尚未产生可测并行重叠。" : "No measurable task overlap yet."));
  setText("proof-reused", reused ? (state.language === "zh-CN" ? `${reused} 个` : `${reused} outputs`) : "—");
  setText("proof-reused-note", state.language === "zh-CN" ? `${progress.replans || 0} 次重规划没有从头重做有效任务。` : `${progress.replans || 0} re-plan(s) retained valid work instead of restarting.`);
  if (isExperiment()) {
    const recovered = state.run?.experiment?.failed_attempts?.length || 0;
    setText("proof-evidence", `${recovered} recovered`);
    setText("proof-evidence-note", state.language === "zh-CN" ? "资源失败进入 Observation 后触发重规划与受限重试。" : "Resource failure became an Observation, then triggered a bounded re-plan and retry.");
  } else {
    setText("proof-evidence", `${progress.supported_findings || 0} / ${progress.rejected_findings || 0}`);
    setText("proof-evidence-note", state.language === "zh-CN" ? "左侧为进入 FACT 层的证据，右侧为被拦截的候选。" : "Supported findings admitted / unsupported candidates rejected.");
  }
  const cpu = runtime.peak_cpu_pressure, memory = runtime.peak_memory_pressure;
  setText("proof-pressure", cpu == null || memory == null ? (state.language === "zh-CN" ? "尚无 Linux PSI 样本。" : "No Linux PSI sample yet.")
    : (state.language === "zh-CN" ? `峰值 CPU 等待 ${number(cpu, 2)}% · 内存等待 ${number(memory, 2)}%（PSI，不是利用率）` : `Peak CPU stall ${number(cpu, 2)}% · memory stall ${number(memory, 2)}% (PSI, not utilization)`));
  setText("proof-isolation", runtime.cgroup_isolated
    ? (state.language === "zh-CN" ? `cgroup 已启用 · 单 Agent 上限 ${runtime.cpu_quota_percent || "—"}% CPU / ${bytes(runtime.memory_max_bytes)}` : `cgroup enabled · per-Agent ceiling ${runtime.cpu_quota_percent || "—"}% CPU / ${bytes(runtime.memory_max_bytes)}`)
    : (state.language === "zh-CN" ? "本地运行启用了资源需求评分，但未启用 cgroup 强隔离。" : "Demand-aware scoring is active; cgroup hard isolation is off for this local run."));

  const quality = state.quality || {}, status = quality.status || "—";
  setText("metric-quality", status); setText("metric-quality-score", quality.score == null ? "—" : `${quality.score}/100`);
  setText("quality-gaps", quality.gaps?.length ? quality.gaps.map(qualityGapText).join(" · ") : t("coverageHint"));
  byId("quality-card").className = `quality-card ${safeClass(status)}`;
}

function qualityGapText(value) {
  if (state.language !== "zh-CN") return value;
  const translations = {
    "fewer than two verified method descriptions": "可验证的方法描述不足两项",
    "no verified dataset coverage": "缺少可验证的数据集信息",
    "no verified evaluation metric coverage": "缺少可验证的评估指标",
    "no verified limitation coverage": "缺少可验证的局限分析",
    "experiment proposal lacks a baseline, dataset, metric, ablation, or protocol": "实验方案缺少基线、数据集、指标、消融或执行协议"
  };
  return translations[value] || value;
}

function renderDAG() {
  const target = byId("dag-view"), versions = state.plan?.versions || [], selector = byId("plan-version-selector");
  if (!versions.length) {
    selector.innerHTML = ""; byId("plan-transition").classList.add("hidden"); byId("plan-diff").classList.add("hidden");
    target.className = "empty-state"; target.textContent = t("planWaiting"); return;
  }
  if (state.selectedPlanVersion == null) state.selectedPlanVersion = versions.at(-1).version;
  selector.innerHTML = versions.map(version => `<button class="version-button ${version.version === state.selectedPlanVersion ? "active" : ""}" data-version="${version.version}" type="button">v${version.version}</button>`).join("");
  selector.querySelectorAll("[data-version]").forEach(node => node.addEventListener("click", () => {
    state.selectedPlanVersion = Number(node.dataset.version); state.followLatestPlan = false; renderDAG();
  }));
  const transition = byId("plan-transition");
  if (versions.length > 1) { transition.innerHTML = versions.map((version, index) => `${index ? '<span>↓ REPLAN</span>' : ''}<strong>Plan v${version.version}</strong>`).join(""); transition.classList.remove("hidden"); }
  else transition.classList.add("hidden");
  const selectedIndex = Math.max(0, versions.findIndex(version => version.version === state.selectedPlanVersion));
  const selected = versions[selectedIndex], previous = selectedIndex > 0 ? versions[selectedIndex - 1] : null;
  renderPlanDiff(selected, previous);
  target.className = "dag-flow";
  target.innerHTML = selected.tasks.map(task => `<article class="dag-task ${safeClass(task.status)} ${safeClass(task.change)}">
    <div class="task-top"><span class="task-dot"></span><span class="task-state">${escapeHTML(task.status)}</span><span class="task-change">${escapeHTML(task.change)}</span></div>
    <p class="task-name">${escapeHTML(task.name || task.id)}</p>
    <div class="task-capability">${escapeHTML(task.capability)}</div>
    <div class="dependency-edge">${task.depends_on?.length ? `from ${task.depends_on.map(escapeHTML).join(", ")}` : "plan root"}</div>
  </article>`).join("");
}

function renderPlanDiff(selected, previous) {
  const target = byId("plan-diff");
  if (!previous) { target.classList.add("hidden"); target.innerHTML = ""; return; }
  const previousByID = new Map(previous.tasks.map(task => [task.id, task]));
  const rows = selected.tasks.filter(task => ["REUSED", "NEW"].includes(task.change)).map(task => ({kind: task.change, label: task.name || task.id}));
  (selected.removed || []).forEach(id => rows.push({kind: "REMOVED", label: previousByID.get(id)?.name || id}));
  target.innerHTML = `<strong>Plan v${previous.version} → v${selected.version}</strong><div>${rows.map(row => `<span class="diff-${row.kind.toLowerCase()}"><i>${row.kind === "REUSED" ? "✓" : row.kind === "NEW" ? "+" : "−"}</i>${escapeHTML(row.label)} <small>${row.kind}</small></span>`).join("")}</div>`;
  target.classList.remove("hidden");
}

function renderDecision() {
  const decision = state.run?.decision || {};
  const type = decision.type || (state.run?.status === "FAILED" ? "FAILED" : state.run?.status === "COMPLETED" ? "GOAL_COMPLETED" : "WAITING");
  setText("decision-type", type);
  setText("decision-observation", decision.observation_summary || defaultObservation(type));
  setText("decision-reason", decision.reason || (type === "GOAL_COMPLETED" ? t("goalSatisfied") : t("noDecision")));
  setText("decision-action", decision.action || defaultAction(type));
  const transition = byId("decision-transition");
  if (decision.from_plan && decision.to_plan) {
    const queryChange = state.queries.length > 1 ? `Search query: ${state.queries[0]} → ${state.queries.at(-1)}` : "";
    transition.innerHTML = `<strong>Plan v${decision.from_plan} → Plan v${decision.to_plan}</strong><span>${escapeHTML(decision.replan_observation_summary || "")}</span><span>${escapeHTML(decision.replan_reason || "")}</span><span>${escapeHTML(decision.replan_action || queryChange)}</span>${queryChange ? `<small>${escapeHTML(queryChange)}</small>` : ""}`;
    transition.classList.remove("hidden");
  } else transition.classList.add("hidden");
  const history = decision.history || [];
  byId("decision-history").innerHTML = history.length > 1 ? history.map(item => `<div><strong>Iteration ${item.iteration} · ${escapeHTML(item.type)}</strong><span>${escapeHTML(item.reason)}</span></div>`).join("") : "";
}

function renderTimeline() {
  const target = byId("timeline");
  let events = state.events;
  if (state.timelineMode === "important" || state.presentation) events = events.filter(importantEvent);
  if (!events.length) { target.innerHTML = `<div class="empty-state">${escapeHTML(t("telemetryWaiting"))}</div>`; return; }
  target.innerHTML = events.slice(-180).map(event => {
    const category = /replan|plan\.revised/.test(event.kind) ? "replan" : /evidence|claim/.test(event.kind) ? "evidence" : /failed|aborted|blocked|rejected/.test(event.kind) ? "failed" : "";
    return `<div class="timeline-event"><span class="event-time">${escapeHTML(clock(event.timestamp))}</span><span class="event-marker"></span><div><strong class="event-kind ${category}">${escapeHTML(friendlyKind(event.kind, event))}</strong><span class="event-message">${escapeHTML(eventMessage(event))}</span></div></div>`;
  }).join("");
  if (byId("autoscroll").checked) target.scrollTop = target.scrollHeight;
}

function importantEvent(event) {
  if (/^cognitive\./.test(event.kind)) return true;
  if (/^research\.(paper\.parsed|paper\.analysis\.completed|evidence\.|claim\.|report\.)/.test(event.kind)) return true;
  if (event.kind === "runtime.agent.dispatched" || event.kind === "runtime.agent.finished") {
    return ["literature.search", "paper.fetch"].includes(event.data?.role) || String(event.data?.role || "").startsWith("experiment.");
  }
  return false;
}

function eventMessage(event) {
  const data = event.data || {};
  if (event.kind === "cognitive.observation.created" && data.failure_code === "MEMORY_LIMIT_EXCEEDED") {
    return state.language === "zh-CN" ? `预计内存 ${bytes(data.memory_peak_bytes)} 超过 ${bytes(data.memory_limit_bytes)}；失败已进入决策层。` : `Estimated memory ${bytes(data.memory_peak_bytes)} exceeded ${bytes(data.memory_limit_bytes)}; the failure entered the decision layer.`;
  }
  if (event.kind === "cognitive.observation.created" && data.failure_code === "PDF_LIMIT_EXCEEDED") {
    const required = bytes(data.required_bytes), limit = bytes(data.limit_bytes);
    return state.language === "zh-CN" ? `PDF 需要 ${required}，本次预算为 ${limit}；已安全跳过并交由 Agent 选择替代论文。` : `PDF needs ${required}; this run allows ${limit}. It was skipped safely so the agent can select an alternative.`;
  }
  if (event.kind === "cognitive.decision.made") return `${data.decision || "Decision"}: ${data.reason || ""}`;
  if (event.kind === "cognitive.replan.requested") return data.reason || "A revised plan was requested.";
  if (event.kind === "cognitive.plan.created" || event.kind === "cognitive.plan.revised") return `Validated Plan v${data.version || data.iteration || "?"} · ${data.tasks || 0} tasks`;
  if (event.kind === "runtime.agent.dispatched") return `Started ${data.role || event.task_id}`;
  if (event.kind === "runtime.agent.finished") return `${event.phase || "Finished"} · ${data.role || event.task_id}`;
  if (event.kind === "research.paper.parsed") return `${data.parser || "parser"} · ${data.pages || 0} pages · ${data.sections || 0} sections`;
  if (event.kind === "research.paper.analysis.completed") return `${data.candidates || 0} candidates · ${data.supported || 0} supported · ${data.rejected || 0} rejected`;
  if (/evidence\.rejected|claim\.unsupported/.test(event.kind)) return `${data.rejected || 0} unsupported candidate(s) rejected`;
  if (/evidence\.verified|claim\.supported/.test(event.kind)) return `${data.source_verified || data.supported || 0} source-backed finding(s)`;
  if (event.kind === "cognitive.goal.completed") return "Verified goal completed.";
  return event.task_id || event.phase || "Telemetry event";
}

function renderCurrentAction() {
  const running = state.plan?.versions?.at(-1)?.tasks?.find(task => task.status === "RUNNING");
  if (running) { setText("current-action", `Executing · ${running.name || running.capability}`); return; }
  const event = [...state.events].reverse().find(importantEvent);
  setText("current-action", event ? friendlyKind(event.kind, event) : state.run?.status || t("waiting"));
}

function renderHistory() {
  const target = byId("history"), select = byId("history-select");
  select.innerHTML = `<option value="">${escapeHTML(t("recentOption"))}</option>` + state.history.map(run => `<option value="${escapeHTML(run.id)}">${escapeHTML(run.mode.toUpperCase())} · ${escapeHTML(run.status)} · ${escapeHTML(shortGoal(run.goal))}</option>`).join("");
  if (state.run) select.value = state.run.id;
  if (!state.history.length) { target.innerHTML = `<div class="empty-state">${escapeHTML(t("noRuns"))}</div>`; return; }
  target.innerHTML = state.history.map(run => `<button class="history-item ${state.run?.id === run.id ? "active" : ""}" data-run-id="${escapeHTML(run.id)}" type="button">
    <div class="history-top"><span class="mode-label ${safeClass(run.mode)}">${escapeHTML(run.mode.toUpperCase())}</span><span>${escapeHTML(run.status)}</span></div>
    <p class="history-goal">${escapeHTML(run.goal)}</p>
    <span class="history-meta">${dateTime(run.created_at)} · ${duration(run.duration_ms)} · ${run.progress?.replans || 0} replans · ${run.progress?.supported_findings || 0} supported</span>
  </button>`).join("");
  target.querySelectorAll("[data-run-id]").forEach(node => node.addEventListener("click", () => selectRun(node.dataset.runId)));
}

function renderProgress() {
  const p = state.run?.progress || {};
  const stages = [["Queries", p.search_queries], ["Retrieved", p.retrieved_papers], ["Deduplicated", p.deduplicated_papers], ["PDF available", p.pdfs_available], ["Parsed", p.parsed_papers], ["Analyzed", p.analyzed_papers]];
  byId("progress-stages").innerHTML = stages.map(([label, value]) => `<div class="progress-stage"><strong>${value ?? 0}</strong><span>${label}</span></div>`).join("");
  setText("input-tokens", p.input_tokens == null ? "Unavailable" : integer(p.input_tokens));
  setText("output-tokens", p.output_tokens == null ? "Unavailable" : integer(p.output_tokens));
}

function renderSummary() {
  const summary = state.run?.summary, experiment = state.run?.experiment, runtime = state.run?.runtime || {};
  if (experiment) {
    const planner = experiment.planner_mode === "OFFLINE_DETERMINISTIC_LLM_FIXTURE"
      ? (state.language === "zh-CN" ? "离线确定性 Planner" : "Offline deterministic planner")
      : (experiment.planner_mode || "—");
    const groups = [
      ["Status", [["Run", state.run.status], ["Duration", duration(experiment.duration_ms)]]],
      ["Provenance", [["Execution", experiment.execution_mode || state.run.mode], ["Planner", planner], [state.language === "zh-CN" ? "实验目录" : "Directory", state.run.experiment_directory || "examples/experiment"]]],
      ["Scheduler", [["Scheduled", runtime.scheduled_tasks || 0], ["Executed", runtime.executed_tasks || 0], ["Failed", runtime.failed_tasks || 0]]],
      ["Agent loop", [["Plan versions", (state.plan?.versions || []).length], ["Re-plans", experiment.replans], ["Outputs reused", state.plan?.versions?.at(-1)?.tasks?.filter(task => task.change === "REUSED").length || 0]]],
      ["Recovery", [["Failure", experiment.failed_attempts?.[0]?.failure_code || "—"], ["Recovered", experiment.status === "COMPLETED" ? "YES" : "NO"]]],
      ["Result", [["Methods", experiment.experiments?.length || 0], ["Best", experiment.best_name || "—"], ["Accuracy", experiment.experiments?.find(item => item.method === experiment.best_method)?.accuracy?.toFixed(2) || "—"]]]
    ];
    byId("summary-grid").innerHTML = groups.map(([name, values]) => `<section><h3>${name}</h3>${values.map(([label, value]) => `<div><span>${label}</span><strong>${escapeHTML(value)}</strong></div>`).join("")}</section>`).join("");
    return;
  }
  if (!summary) { byId("summary-grid").innerHTML = `<div class="empty-state">${escapeHTML(t("summaryWaiting"))}</div>`; return; }
  const groups = [
    ["Status", [["Run", state.run.status], ["Duration", duration(summary.duration_ms)]]],
    ["Scheduler", [["Succeeded", runtime.succeeded_tasks || 0], ["Failed", runtime.failed_tasks || 0]]],
    ["Research", [["Retrieved", summary.search.papers_retrieved], ["Parsed", summary.paper.parsed_successfully], ["Analyzed", state.run.progress?.analyzed_papers || 0]]],
    ["LLM", [["Calls", summary.llm.calls], ["Input", summary.llm.input_tokens == null ? "Unavailable" : integer(summary.llm.input_tokens)], ["Output", summary.llm.output_tokens == null ? "Unavailable" : integer(summary.llm.output_tokens)]]],
    ["Evidence", [["Candidate", summary.evidence.candidates], ["Located", summary.evidence.source_verified], ["Supported", summary.evidence.supported], ["Rejected", summary.evidence.rejected]]],
    ["Output", [["Facts", summary.report.facts], ["Inferences", summary.report.inferences], ["Proposals", summary.report.proposals], ["References", summary.report.references]]],
    ["Reliability", [["Citation closure", summary.report.citation_closure ? "PASS" : "FAIL"], ["Answer completeness", state.quality?.status || summary.report.quality?.status || "—"], ["Hallucinated refs", summary.report.hallucinated_references]]]
  ];
  byId("summary-grid").innerHTML = groups.map(([name, values]) => `<section><h3>${name}</h3>${values.map(([label, value]) => `<div><span>${label}</span><strong>${value}</strong></div>`).join("")}</section>`).join("");
}

function renderExperiment() {
  if (!isExperiment()) return;
  const summary = state.run?.experiment, target = byId("experiment-results");
  renderExperimentRecovery(summary);
  if (!summary?.experiments?.length) {
    target.innerHTML = `<tr><td colspan="6">${state.language === "zh-CN" ? "等待真实 Worker 输出。" : "Waiting for real worker output."}</td></tr>`;
    setText("experiment-best", state.run?.status || t("waiting"));
    return;
  }
  target.innerHTML = summary.experiments.map(item => {
    const accuracy = Number.isFinite(Number(item.accuracy)) ? Math.max(0, Math.min(1, Number(item.accuracy))) : 0;
    return `<tr class="${item.method === summary.best_method ? "best" : ""}">
      <td><strong>${escapeHTML(item.display_name || item.method)}</strong></td>
      <td><div class="accuracy-cell"><meter min="0" max="1" value="${accuracy}">${accuracy}</meter><strong>${(accuracy * 100).toFixed(1)}%</strong></div></td>
      <td>${duration(item.runtime_ms)}</td><td>${bytes(item.memory_peak_bytes)} / ${bytes(item.memory_limit_bytes)}</td>
      <td>${escapeHTML(String(item.attempt))}</td><td>${escapeHTML(item.status)}</td>
    </tr>`;
  }).join("");
  setText("experiment-best", `${state.language === "zh-CN" ? "最佳" : "Best"} · ${summary.best_name}`);
  const deterministic = ["OFFLINE_DETERMINISTIC_LLM_FIXTURE", "OFFLINE_MANIFEST_DRIVEN_LLM_FIXTURE"].includes(summary.planner_mode);
  setText("experiment-note", state.language === "zh-CN"
    ? `Agent 会检查所选 workspace 相对目录并读取 capsule-experiment.json，仅执行 Logistic Regression、Random Forest 与 SVM 三种已注册模拟。本次新 Run 的 Worker 进程、CPU runtime、Scheduler 事件、失败、重试与 Output Transaction 都真实执行；准确率与 working-set 仍是场景夹具/估算，不是任意模型训练。认知规划：${deterministic ? "离线确定性 Planner" : summary.planner_mode || "未知"}。`
    : `The Agent inspects the selected workspace-relative directory and reads capsule-experiment.json, then runs only the registered Logistic Regression, Random Forest, and SVM simulations. Worker processes, CPU runtime, Scheduler events, failure, retry, and Output Transactions execute fresh; accuracy and working-set values remain scenario fixtures/estimates, not arbitrary model training. Cognitive planning: ${deterministic ? "offline deterministic planner" : summary.planner_mode || "unknown"}.`);
}

function renderExperimentRecovery(summary) {
  const target = byId("experiment-recovery");
  const versions = state.plan?.versions || [];
  if (!versions.length) {
    target.innerHTML = `<span class="recovery-step">${escapeHTML(t("planWaiting"))}</span>`;
    return;
  }
  const replans = (state.run?.decision?.history || []).filter(item => item.type === "REPLAN");
  const failure = summary?.failed_attempts?.[0];
  const retry = failure ? summary?.experiments?.find(item => item.method === failure.method && item.status === "SUCCEEDED") : null;
  const before = failure?.parameters?.n_estimators;
  const after = retry?.parameters?.n_estimators;
  const failureVersionIndex = failure
    ? versions.findIndex(version => (version.tasks || []).some(task => task.id === failure.task_id))
    : -1;
  const fragments = [];
  const arrow = () => fragments.push("<i>→</i>");
  versions.forEach((version, index) => {
    if (index > 0) arrow();
    const reused = (version.tasks || []).filter(task => task.change === "REUSED").length;
    const detail = reused > 0
      ? (state.language === "zh-CN" ? `${reused} 个已验证输出被复用` : `${reused} verified output${reused === 1 ? "" : "s"} reused`)
      : (state.language === "zh-CN" ? "已验证任务 DAG" : "Validated task DAG");
    fragments.push(`<span class="recovery-step">Plan v${escapeHTML(String(version.version || index + 1))}<small>${escapeHTML(detail)}</small></span>`);
    if (failureVersionIndex === index) {
      arrow();
      const failureDetail = Number.isFinite(Number(before)) ? `n_estimators ${before}` : `${bytes(failure.memory_peak_bytes)} / ${bytes(failure.memory_limit_bytes)}`;
      fragments.push(`<strong class="recovery-step failure">${escapeHTML(failure.failure_code)}<small>${escapeHTML(failureDetail)}</small></strong>`);
    }
    if (index >= versions.length - 1) return;
    arrow();
    const decision = replans[index];
    const reason = String(decision?.reason || (state.language === "zh-CN" ? "环境 Observation 触发计划修订" : "Environment observation revised the plan")).slice(0, 140);
    fragments.push(`<span class="recovery-step observation">Observation · REPLAN<small>${escapeHTML(reason)}</small></span>`);
  });
  if (state.run?.status === "COMPLETED") {
    arrow();
    const retryDetail = Number.isFinite(Number(after)) ? `n_estimators ${after}` : "";
    fragments.push(`<strong class="recovery-step success">${failure ? (state.language === "zh-CN" ? "重试成功" : "Retry succeeded") : "GOAL_COMPLETED"}${retryDetail ? `<small>${escapeHTML(retryDetail)}</small>` : ""}</strong>`);
  }
  target.innerHTML = fragments.join("");
}

function renderPapers() {
  const target = byId("papers"); setText("paper-count", `${state.papers.length} papers`);
  if (!state.papers.length) { target.innerHTML = '<div class="empty-state">Retrieved paper metadata will appear here.</div>'; return; }
  target.innerHTML = state.papers.map((paper, index) => `<details id="paper-${safeClass(paper.reference || `item-${index + 1}`)}" class="paper-card"><summary>
    <div><span class="paper-reference">${escapeHTML(paper.reference || `P${index + 1}`)}</span><span class="paper-status">${escapeHTML(paper.status)}</span></div>
    <h3 class="paper-title">${escapeHTML(paper.title || paper.id)}</h3>
    <div class="paper-meta">${escapeHTML((paper.authors || []).join(", "))} · ${paper.year || "year unavailable"} · ${escapeHTML(paper.source || "source unavailable")}</div>
  </summary><div class="paper-detail">
    ${paper.abstract ? `<p>${escapeHTML(paper.abstract)}</p>` : ""}
    ${paper.diagnostics ? `<p class="diagnostics">Parser: ${escapeHTML(paper.diagnostics.selected || "unknown")} · Pages: ${paper.diagnostics.page_count || 0} · Sections: ${paper.diagnostics.detected_sections || paper.sections?.length || 0} · Characters: ${integer(paper.diagnostics.extracted_characters || 0)} · Fallback: ${paper.diagnostics.fallback_used ? "Yes" : "No"}</p>` : ""}
    ${paper.sections?.length ? `<strong>Detected sections</strong><ul class="section-list">${paper.sections.map(section => `<li>${escapeHTML(section.heading || section.id)} · p.${section.page_start || "?"}–${section.page_end || "?"} · ${integer(section.characters)} chars</li>`).join("")}</ul>` : ""}
  </div></details>`).join("");
}

function renderFindings() {
  const target = byId("findings"), findings = state.evidence.findings || [];
  setText("evidence-count", `${findings.length} findings`);
  if (!findings.length) { target.innerHTML = '<div class="empty-state">Supported and rejected findings will appear here.</div>'; return; }
  target.innerHTML = findings.map((finding, index) => {
    const rejected = finding.status === "UNSUPPORTED", identifier = finding.evidence_id ? `evidence-${safeClass(finding.evidence_id)}` : `rejected-${index + 1}`;
    return `<details id="${identifier}" class="finding ${rejected ? "rejected" : "supported"}"><summary>
      <span class="finding-status">${rejected ? "REJECTED" : escapeHTML(finding.status)}</span><span class="finding-type">${escapeHTML(finding.claim_type || "claim")}</span>
      <p class="finding-claim">${escapeHTML(finding.claim)}</p>
      <span class="inspect-label">Inspect ${rejected ? "rejection" : "canonical evidence"} ↓</span>
    </summary><div class="finding-detail">
      <dl><div><dt>Claim</dt><dd>${escapeHTML(finding.claim)}</dd></div><div><dt>Paper</dt><dd>${escapeHTML(finding.paper_title || finding.paper_id)}</dd></div><div><dt>Section</dt><dd>${escapeHTML(finding.section || finding.section_id || "Unavailable")}</dd></div><div><dt>Task ID</dt><dd class="monospace">${escapeHTML(finding.task_id || "Unavailable")}</dd></div></dl>
      ${finding.snippet ? `<div class="canonical-evidence"><span>Canonical evidence</span><blockquote>${escapeHTML(finding.snippet)}</blockquote></div>` : ""}
      ${finding.reason ? `<div class="reject-reason"><span>${finding.reason_code ? escapeHTML(finding.reason_code) : "Reject reason"}</span><p>${escapeHTML(finding.reason)}</p></div>` : ""}
      <div class="verification-status">Verification status · <strong>${rejected ? "REJECTED" : escapeHTML(finding.status)}</strong></div>
    </div></details>`;
  }).join("");
}

function renderResults() {
  const target = byId("result-items"), key = state.activeTab, items = state.evidence[key] || [];
  if (!items.length) { target.innerHTML = `<div class="empty-state">No ${escapeHTML(key)} available.</div>`; return; }
  target.innerHTML = items.map(item => {
    const count = item.evidence_ids?.length || 0;
    const className = key === "inferences" ? "inference" : key === "proposals" ? "proposal" : "fact";
    const explanation = key === "facts" ? "Paper directly supports this conclusion" : key === "inferences" ? `Derived from ${count} verified finding${count === 1 ? "" : "s"}` : "AI-generated proposal · not a paper claim";
    return `<article class="result-item ${className}"><div class="result-label">${key.slice(0, -1).toUpperCase()}</div><p>${escapeHTML(item.statement)}</p><span class="result-explanation">${explanation}</span>
      ${count ? `<div class="evidence-links">${item.evidence_ids.map(id => `<a href="#evidence-${safeClass(id)}">Evidence ${escapeHTML(id)}</a>`).join("")}</div>` : ""}</article>`;
  }).join("");
}

function renderReport() {
  const target = byId("report"), link = byId("download-report");
  if (state.reportHTML) { target.innerHTML = state.reportHTML; link.href = `/api/runs/${encodeURIComponent(state.run.id)}/report`; link.classList.remove("disabled"); }
  else { target.innerHTML = `<div class="empty-state">${escapeHTML(t("reportWaiting"))}</div>`; link.href = "#"; link.classList.add("disabled"); }
}

function renderLoop() {
  const status = state.run?.status || "IDLE";
  const current = status === "PLANNING" ? "plan" : status === "RUNNING" ? "execute" : status === "OBSERVING" ? "observe" :
    status === "REPLANNING" ? "decide" : status === "SYNTHESIZING" ? "decide" : status === "COMPLETED" || status === "FAILED" || status === "CANCELLED" ? "decide" : "goal";
  const order = ["goal", "plan", "execute", "observe", "decide"], currentIndex = order.indexOf(current);
  document.querySelectorAll("[data-loop]").forEach(node => {
    const index = order.indexOf(node.dataset.loop);
    node.classList.toggle("active", index === currentIndex && !["COMPLETED", "FAILED", "CANCELLED"].includes(status));
    node.classList.toggle("done", index < currentIndex || status === "COMPLETED");
    node.classList.toggle("recovered", status === "COMPLETED" && Boolean(state.run?.failures?.some(failure => failure.recovered)) && ["execute", "observe", "decide"].includes(node.dataset.loop));
  });
  const key = status === "PLANNING" ? "loopPlanning" : status === "RUNNING" ? "loopExecuting" : status === "OBSERVING" ? "loopObserving" :
    status === "REPLANNING" ? "loopReplanning" : status === "SYNTHESIZING" ? "loopSynthesizing" : status === "COMPLETED" ? "loopCompleted" :
    status === "FAILED" || status === "CANCELLED" ? "loopFailed" : "loopWaiting";
  setText("loop-explanation", t(key));
}

function setTimelineMode(mode) {
  state.timelineMode = mode;
  byId("timeline-presentation").classList.toggle("active", mode === "important");
  byId("timeline-developer").classList.toggle("active", mode === "all");
  renderTimeline();
}

function applyPresentationMode() {
  document.body.classList.toggle("presentation-mode", state.presentation);
  byId("presentation-toggle").checked = state.presentation;
  byId("technical-details").open = !state.presentation;
  if (state.presentation) setTimelineMode("important");
}

function applyLanguage() {
  document.documentElement.lang = state.language;
  byId("language-select").value = state.language;
  document.querySelectorAll("[data-i18n]").forEach(node => { node.textContent = t(node.dataset.i18n); });
  document.querySelectorAll("[data-i18n-placeholder]").forEach(node => { node.placeholder = t(node.dataset.i18nPlaceholder); });
  renderSystemStatus();
}

function renderSystemStatus() {
  if (!state.system) return;
  setChip("runtime-status", `CAPSuleRT · ${state.system.runtime_online ? t("online") : t("offline")}`, state.system.runtime_online);
  setChip("llm-status", `LLM · ${state.system.llm_configured ? t("configured") : t("notConfigured")}`, state.system.llm_configured);
  setChip("provider-status", `Provider · ${state.system.provider_available ? t("available") : t("unavailable")}`, state.system.provider_available);
}

function updateBudgetSummary() { setText("budget-summary", `${byId("pdf-limit-select").value || 32} MiB / PDF`); }

function updateScenarioControl() {
  const experiment = isExperiment();
  byId("scenario-select").disabled = experiment || document.querySelector("input[name=mode]:checked")?.value !== "mock";
  byId("run-settings").classList.toggle("experiment-disabled", experiment);
  byId("run-button-label").textContent = experiment ? t("runExperiment") : t("runResearch");
}
function isExperiment() {
  const preset = state.system?.presets?.find(item => item.id === state.selectedPreset);
  if (preset) return preset.workload === "experiment";
  return state.run?.workload === "experiment";
}
function clearLoadedRun() {
  state.generation += 1;
  state.refreshSequence += 1;
  if (state.eventSource) { state.eventSource.close(); state.eventSource = null; }
  if (state.refreshTimer) { window.clearTimeout(state.refreshTimer); state.refreshTimer = null; }
  state.run = null; state.runOrigin = "none"; state.plan = {versions: []}; state.papers = []; state.queries = [];
  state.evidence = emptyEvidence(); state.quality = {}; state.events = []; state.reportHTML = "";
  state.selectedPlanVersion = null; state.followLatestPlan = true;
  window.history.replaceState(null, "", window.location.pathname);
}
function sameRun(generation, id) { return state.generation === generation && state.run?.id === id; }
function emptyEvidence() { return { candidate_count: 0, source_verified_count: 0, supported_count: 0, rejected_count: 0, findings: [], facts: [], inferences: [], proposals: [] }; }
function storedPresentationMode() { try { return window.localStorage.getItem("capsule-dashboard-view") !== "developer"; } catch (_) { return true; } }
function storedLanguage() { try { const saved = window.localStorage.getItem("capsule-dashboard-language"); if (["en", "zh-CN"].includes(saved)) return saved; } catch (_) { /* optional preference */ } return navigator.language?.toLowerCase().startsWith("zh") ? "zh-CN" : "en"; }
function defaultObservation(type) { return type === "GOAL_COMPLETED" ? t("allVerified") : type === "FAILED" ? t("validationFailed") : t("noObservation"); }
function defaultAction(type) { return type === "GOAL_COMPLETED" ? t("publishReport") : type === "FAILED" ? t("stopSafely") : type === "REPLAN" ? t("executeRevised") : t("waitPlan"); }
function setChip(id, text, good) { const node = byId(id); node.textContent = text; node.className = `chip ${good ? "good" : "warn"}`; }
function setText(id, value) { byId(id).textContent = String(value); }
function showNotice(message, kind = "error") { const node = byId("notice"); node.textContent = message; node.className = `notice ${kind}`; }
function hideNotice() { byId("notice").classList.add("hidden"); }
function duration(ms) { if (!Number.isFinite(ms)) return "—"; if (ms < 1000) return `${ms} ms`; const seconds = Math.round(ms / 1000); return seconds < 60 ? `${seconds} s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`; }
function bytes(value) { if (!Number.isFinite(Number(value)) || Number(value) <= 0) return "—"; return `${(Number(value) / 1024 / 1024).toFixed(1)} MiB`; }
function number(value, digits) { return Number(value).toFixed(digits); }
function integer(value) { return new Intl.NumberFormat().format(value); }
function clock(value) { const date = new Date(value); return Number.isNaN(date.valueOf()) ? "—" : date.toLocaleTimeString([], {hour12: false}); }
function dateTime(value) { const date = new Date(value); return Number.isNaN(date.valueOf()) ? "—" : date.toLocaleString([], {month: "short", day: "numeric", hour: "2-digit", minute: "2-digit"}); }
function shortRunID(value) { const text = String(value || ""); return text.length > 12 ? `run · ${text.slice(-12)}` : text; }
function shortGoal(value) { const text = String(value || ""); return text.length > 36 ? text.slice(0, 35) + "…" : text; }
function friendlyKind(kind, event = {}) { const role = event.data?.role; if (kind === "runtime.agent.dispatched" && role) return `START · ${role}`; if (kind === "runtime.agent.finished" && role) return `${event.phase || "FINISH"} · ${role}`; return String(kind).replace(/^cognitive\./, "").replace(/^runtime\.agent\./, "task.").replace(/^research\./, "").toUpperCase(); }
function safeClass(value) { return String(value || "").replace(/[^a-zA-Z0-9_-]/g, "-"); }
function escapeHTML(value) { return String(value ?? "").replace(/[&<>"']/g, character => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"})[character]); }
async function api(url, options = {}) { const response = await fetch(url, {cache: "no-store", headers: {"Content-Type": "application/json", ...(options.headers || {})}, ...options}); const data = await response.json().catch(() => ({})); if (!response.ok) throw new Error(data.error || `${response.status} ${response.statusText}`); return data; }
