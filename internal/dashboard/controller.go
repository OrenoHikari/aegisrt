package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aegisrt/internal/experiment"
	"aegisrt/internal/research"
)

var (
	ErrRunActive  = errors.New("a research run is already active")
	ErrRunUnknown = errors.New("dashboard run does not exist")
)

const (
	// DefaultExperimentWorkScale makes the local competition scenario long
	// enough for a browser to observe Scheduler and re-plan transitions. The
	// experiment CLI itself intentionally keeps its fast default of 1.
	DefaultExperimentWorkScale = 2000
	DefaultExperimentDirectory = "examples/experiment"
	experimentPollInterval     = 40 * time.Millisecond
	maximumExperimentDirectory = 512
)

type Options struct {
	Root                string
	Executable          string
	WorkDir             string
	Python              string
	PresetFile          string
	DefaultMode         string
	MaxHistory          int
	MaxPapers           int
	MaxPDFBytes         int64
	MaxLLMCalls         int
	MaxReplans          int
	ExperimentWorkScale int
	LoopTimeout         time.Duration
	PollInterval        time.Duration
	Executor            RunExecutor
}

type RunSpec struct {
	ID         string
	Goal       string
	Workload   string
	Mode       string
	Scenario   string
	Root       string
	Executable string
	WorkDir    string
	LogPath    string
	Arguments  []string
}

type RunExecutor interface {
	Execute(ctx context.Context, spec RunSpec) error
}

type ProcessExecutor struct{}

func (ProcessExecutor) Execute(ctx context.Context, spec RunSpec) error {
	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	command := exec.CommandContext(ctx, spec.Executable, spec.Arguments...)
	command.Dir = spec.WorkDir
	command.Stdout, command.Stderr = logFile, logFile
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		if err := command.Process.Signal(os.Interrupt); errors.Is(err, os.ErrProcessDone) {
			return nil
		} else {
			return err
		}
	}
	command.WaitDelay = 15 * time.Second
	return command.Run()
}

type Controller struct {
	mu       sync.RWMutex
	options  Options
	presets  []DemoPreset
	runs     map[string]*Run
	order    []string
	activeID string
	wg       sync.WaitGroup
}

func NewController(options Options) (*Controller, error) {
	if strings.TrimSpace(options.Root) == "" {
		options.Root = filepath.Join("var", "dashboard")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, err
	}
	options.Root = root
	if strings.TrimSpace(options.Executable) == "" {
		options.Executable, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	options.Executable, err = filepath.Abs(options.Executable)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.WorkDir) == "" {
		options.WorkDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	options.WorkDir, err = filepath.Abs(options.WorkDir)
	if err != nil {
		return nil, err
	}
	options.DefaultMode = strings.ToLower(strings.TrimSpace(options.DefaultMode))
	if options.DefaultMode == "" {
		options.DefaultMode = "real"
	}
	if options.DefaultMode != "real" && options.DefaultMode != "mock" {
		return nil, fmt.Errorf("dashboard default mode must be real or mock")
	}
	if options.Executor == nil {
		options.Executor = ProcessExecutor{}
	}
	if options.MaxHistory <= 0 {
		options.MaxHistory = 10
	}
	if options.MaxPapers <= 0 {
		options.MaxPapers = 3
	}
	if options.MaxPapers < 2 {
		return nil, fmt.Errorf("dashboard max-papers must be at least 2 because research.synthesize requires two analyzed papers")
	}
	if options.MaxPDFBytes <= 0 {
		options.MaxPDFBytes = 32 * 1024 * 1024
	}
	if err := research.ValidatePaperDownloadLimit(options.MaxPDFBytes); err != nil {
		return nil, fmt.Errorf("invalid dashboard PDF budget: %w", err)
	}
	if options.MaxLLMCalls <= 0 {
		options.MaxLLMCalls = 16
	}
	if options.MaxReplans <= 0 {
		options.MaxReplans = 3
	}
	if options.ExperimentWorkScale == 0 {
		options.ExperimentWorkScale = DefaultExperimentWorkScale
	}
	if options.ExperimentWorkScale < 1 || options.ExperimentWorkScale > experiment.MaximumWorkScale {
		return nil, fmt.Errorf("dashboard experiment work scale must be between 1 and %d", experiment.MaximumWorkScale)
	}
	if options.LoopTimeout <= 0 {
		options.LoopTimeout = 10 * time.Minute
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 250 * time.Millisecond
	}
	if err := (research.ResearchBudget{
		MaxPapers: options.MaxPapers, MaxLLMCalls: options.MaxLLMCalls,
		MaxAnalysisCallsPerPaper: 1, MaxReplans: options.MaxReplans,
		MaxRuntime: options.LoopTimeout, MaxContextBytes: 60 * 1024,
	}).Validate(); err != nil {
		return nil, fmt.Errorf("invalid dashboard research budget: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o755); err != nil {
		return nil, err
	}
	presets, err := readPresets(options.PresetFile)
	if err != nil {
		return nil, err
	}
	controller := &Controller{options: options, presets: presets, runs: make(map[string]*Run)}
	if err := controller.loadHistory(); err != nil {
		return nil, err
	}
	return controller, nil
}

func (c *Controller) DefaultMode() string { return c.options.DefaultMode }
func (c *Controller) PresetGoal() string {
	if len(c.presets) == 0 {
		return ""
	}
	return c.presets[0].Goal
}
func (c *Controller) Presets() []DemoPreset { return append([]DemoPreset(nil), c.presets...) }

func (c *Controller) Create(request CreateRunRequest) (RunView, error) {
	request.Goal = strings.TrimSpace(request.Goal)
	if request.Goal == "" || len(request.Goal) > 4096 {
		return RunView{}, fmt.Errorf("goal must contain 1-4096 bytes")
	}
	request.Workload = strings.ToLower(strings.TrimSpace(request.Workload))
	if request.Workload == "" {
		request.Workload = "research"
	}
	if request.Workload != "research" && request.Workload != "experiment" {
		return RunView{}, fmt.Errorf("workload must be research or experiment")
	}
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	if request.Workload == "experiment" {
		if strings.TrimSpace(request.ExperimentDirectory) == "" {
			request.ExperimentDirectory = DefaultExperimentDirectory
		}
		var err error
		request.ExperimentDirectory, err = normalizeExperimentDirectory(request.ExperimentDirectory)
		if err != nil {
			return RunView{}, err
		}
		request.Mode = "local"
		request.Scenario = "resource-replan"
	} else {
		if strings.TrimSpace(request.ExperimentDirectory) != "" {
			return RunView{}, fmt.Errorf("experiment_directory is only valid for experiment runs")
		}
		if request.Mode == "" {
			request.Mode = c.options.DefaultMode
		}
		if request.Mode != "mock" && request.Mode != "real" {
			return RunView{}, fmt.Errorf("research run mode must be mock or real")
		}
	}
	request.Scenario = strings.ToLower(strings.TrimSpace(request.Scenario))
	if request.Workload == "experiment" {
		request.Scenario = "resource-replan"
	} else if request.Mode == "real" {
		request.Scenario = ""
	} else if request.Scenario == "" {
		request.Scenario = research.MockScenarioNormal
	}
	if request.Workload == "research" && request.Mode == "mock" && !validMockScenario(request.Scenario) {
		return RunView{}, fmt.Errorf("unknown mock scenario")
	}
	if request.MaxPDFMB == 0 {
		request.MaxPDFMB = int(c.options.MaxPDFBytes / (1024 * 1024))
	}
	maxPDFBytes := int64(request.MaxPDFMB) * 1024 * 1024
	if request.MaxPDFMB <= 0 || maxPDFBytes/1024/1024 != int64(request.MaxPDFMB) {
		return RunView{}, fmt.Errorf("max_pdf_mb must be greater than zero")
	}
	if err := research.ValidatePaperDownloadLimit(maxPDFBytes); err != nil {
		return RunView{}, fmt.Errorf("max_pdf_mb: %w", err)
	}

	c.mu.Lock()
	if c.activeID != "" {
		c.mu.Unlock()
		return RunView{}, ErrRunActive
	}
	id, err := newRunID()
	if err != nil {
		c.mu.Unlock()
		return RunView{}, err
	}
	root := filepath.Join(c.options.Root, "runs", id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		c.mu.Unlock()
		return RunView{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &Run{RunRecord: RunRecord{
		ID: id, Goal: request.Goal, Workload: request.Workload, Mode: request.Mode, Scenario: request.Scenario,
		ExperimentDirectory: request.ExperimentDirectory, MaxPDFMB: request.MaxPDFMB,
		Status: StatusPlanning, CreatedAt: time.Now().UTC(), Root: root,
	}, cancel: cancel}
	run.Events = NewEventStore(id, func(event DashboardEvent) { c.consumeEvent(id, event) })
	c.runs[id] = run
	c.order = append([]string{id}, c.order...)
	c.activeID = id
	c.trimHistoryLocked()
	_ = c.persistLocked(run)
	c.wg.Add(1)
	go c.execute(ctx, run)
	c.mu.Unlock()
	return c.View(id)
}

func (c *Controller) View(id string) (RunView, error) {
	c.mu.RLock()
	run, ok := c.runs[id]
	if !ok {
		c.mu.RUnlock()
		return RunView{}, ErrRunUnknown
	}
	record := run.RunRecord
	events := run.Events
	c.mu.RUnlock()

	allEvents := events.Snapshot(0)
	artifacts, _ := ScanVerifiedArtifacts(record.Root, allEvents)
	failures := dashboardFailures(record)
	record.DurationMS = currentDuration(record)
	if record.Summary != nil {
		applySummaryProgress(&artifacts.Progress, record.Summary)
	}
	artifacts.Progress.Replans = countReplans(events.Snapshot(0))
	return RunView{
		RunRecord: record,
		Runtime:   BuildRuntimeSnapshot(allEvents),
		Progress:  artifacts.Progress,
		Decision:  BuildDecisionView(allEvents),
		Failures:  failures,
	}, nil
}

func (c *Controller) List() []RunView {
	c.mu.RLock()
	ids := append([]string(nil), c.order...)
	limit := c.options.MaxHistory
	c.mu.RUnlock()
	if len(ids) > limit {
		ids = ids[:limit]
	}
	result := make([]RunView, 0, len(ids))
	for _, id := range ids {
		if view, err := c.View(id); err == nil {
			result = append(result, view)
		}
	}
	return result
}

func (c *Controller) EventStore(id string) (*EventStore, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	run, ok := c.runs[id]
	if !ok {
		return nil, ErrRunUnknown
	}
	return run.Events, nil
}

func (c *Controller) Plans(id string) (PlanSnapshot, error) {
	c.mu.RLock()
	run, ok := c.runs[id]
	if !ok {
		c.mu.RUnlock()
		return PlanSnapshot{}, ErrRunUnknown
	}
	store := run.Events
	status := run.Status
	root := run.Root
	c.mu.RUnlock()
	snapshot := BuildPlanSnapshot(store.Snapshot(0))
	if status.Terminal() {
		if persisted, err := readPersistedPlan(root); err == nil {
			snapshot = persisted
		}
	}
	if status == StatusCancelled && len(snapshot.Versions) > 0 {
		tasks := snapshot.Versions[len(snapshot.Versions)-1].Tasks
		for index := range tasks {
			if tasks[index].Status != "COMPLETED" && tasks[index].Status != "REUSED" {
				tasks[index].Status = "CANCELLED"
			}
		}
	}
	return snapshot, nil
}

func (c *Controller) Artifacts(id string) (ArtifactSnapshot, error) {
	c.mu.RLock()
	run, ok := c.runs[id]
	if !ok {
		c.mu.RUnlock()
		return ArtifactSnapshot{}, ErrRunUnknown
	}
	root := run.Root
	status := run.Status
	summary := run.Summary
	events := run.Events
	c.mu.RUnlock()
	snapshot, err := ScanVerifiedArtifacts(root, events.Snapshot(0))
	if err != nil {
		return ArtifactSnapshot{}, err
	}
	scannedQuality := snapshot.Quality
	if status.Terminal() {
		if persisted, persistedErr := readPersistedArtifacts(root); persistedErr == nil {
			snapshot = persisted
			if snapshot.Quality.Status == "" {
				snapshot.Quality = scannedQuality
			}
		}
	}
	if summary != nil {
		applySummaryProgress(&snapshot.Progress, summary)
	}
	snapshot.Progress.Replans = countReplans(events.Snapshot(0))
	return snapshot, nil
}

func (c *Controller) Report(id string) ([]byte, error) {
	c.mu.RLock()
	run, ok := c.runs[id]
	if !ok {
		c.mu.RUnlock()
		return nil, ErrRunUnknown
	}
	root := run.Root
	c.mu.RUnlock()
	return ReadReport(root)
}

func (c *Controller) Cancel(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, ok := c.runs[id]
	if !ok {
		return ErrRunUnknown
	}
	if run.Status.Terminal() {
		return fmt.Errorf("run is already %s", run.Status)
	}
	run.Status = StatusCancelled
	run.Error = "run cancelled by user"
	if run.cancel != nil {
		run.cancel()
	}
	return c.persistLocked(run)
}

func (c *Controller) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.activeID != "" {
		if run := c.runs[c.activeID]; run != nil && run.cancel != nil {
			run.cancel()
		}
	}
	c.mu.Unlock()
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Controller) execute(ctx context.Context, run *Run) {
	defer c.wg.Done()
	started := time.Now().UTC()
	c.mu.Lock()
	run.StartedAt = &started
	run.Status = StatusPlanning
	_ = c.persistLocked(run)
	c.mu.Unlock()

	tailCtx, stopTail := context.WithCancel(context.Background())
	pollInterval := c.options.PollInterval
	if run.Workload == "experiment" && pollInterval > experimentPollInterval {
		pollInterval = experimentPollInterval
	}
	tailer := &EventTailer{Path: filepath.Join(run.Root, "runtime-events.jsonl"), Store: run.Events, Interval: pollInterval}
	tailDone := make(chan struct{})
	go func() {
		tailer.Run(tailCtx)
		close(tailDone)
	}()
	spec := c.runSpec(run)
	err := c.options.Executor.Execute(ctx, spec)
	stopTail()
	<-tailDone
	_ = tailer.ReadAvailable()

	finished := time.Now().UTC()
	var summary *research.RunSummary
	var experimentSummary *experiment.RunSummary
	var summaryErr error
	if run.Workload == "experiment" {
		experimentSummary, summaryErr = ReadExperimentSummary(run.Root)
	} else {
		summary, summaryErr = ReadRunSummary(run.Root)
	}
	if err == nil && summaryErr != nil {
		err = fmt.Errorf("load completed run summary: %w", summaryErr)
	}
	if snapshotErr := persistRunViews(run); err == nil && snapshotErr != nil {
		err = fmt.Errorf("persist dashboard run views: %w", snapshotErr)
	}
	c.mu.Lock()
	run.FinishedAt = &finished
	run.DurationMS = finished.Sub(started).Milliseconds()
	if summaryErr == nil {
		run.Summary = summary
		run.Experiment = experimentSummary
	}
	if run.Status == StatusCancelled || errors.Is(ctx.Err(), context.Canceled) {
		run.Status = StatusCancelled
		if run.Error == "" {
			run.Error = "run cancelled"
		}
	} else if err != nil {
		run.Status = StatusFailed
		if run.Error == "" {
			run.Error = readProcessDiagnostic(filepath.Join(run.Root, "dashboard-run.log"))
			if run.Error == "" {
				run.Error = boundedError(err)
			}
		}
	} else {
		run.Status = StatusCompleted
		run.Error = ""
	}
	if c.activeID == run.ID {
		c.activeID = ""
	}
	_ = c.persistLocked(run)
	c.mu.Unlock()
}

func (c *Controller) runSpec(run *Run) RunSpec {
	if run.Workload == "experiment" {
		return RunSpec{
			ID: run.ID, Goal: run.Goal, Workload: run.Workload, Mode: run.Mode,
			Scenario: run.Scenario, Root: run.Root, Executable: c.options.Executable,
			WorkDir: c.options.WorkDir, LogPath: filepath.Join(run.Root, "dashboard-run.log"),
			Arguments: []string{
				"experiment", "demo", "--task", run.Goal, "--root", run.Root,
				"--workspace-root", c.options.WorkDir, "--experiment-dir", run.ExperimentDirectory,
				"--max-replans", fmt.Sprint(c.options.MaxReplans),
				"--work-scale", fmt.Sprint(c.options.ExperimentWorkScale),
				"--loop-timeout", c.options.LoopTimeout.String(),
			},
		}
	}
	arguments := []string{
		"agent", "research", "--task", run.Goal, "--root", run.Root,
		"--max-papers", fmt.Sprint(c.options.MaxPapers), "--max-llm-calls", fmt.Sprint(c.options.MaxLLMCalls),
		"--max-replans", fmt.Sprint(c.options.MaxReplans), "--loop-timeout", c.options.LoopTimeout.String(),
		"--max-pdf-mb", fmt.Sprint(run.MaxPDFMB),
	}
	if run.Mode == "mock" {
		arguments = append(arguments, "--mock", "--mock-scenario", run.Scenario)
	} else {
		arguments = append(arguments, "--provider", "arxiv", "--analysis-mode", "llm", "--paper-parser", "python")
		if strings.TrimSpace(c.options.Python) != "" {
			arguments = append(arguments, "--python", c.options.Python)
		}
	}
	return RunSpec{
		ID: run.ID, Goal: run.Goal, Workload: run.Workload, Mode: run.Mode, Scenario: run.Scenario, Root: run.Root,
		Executable: c.options.Executable, WorkDir: c.options.WorkDir,
		LogPath: filepath.Join(run.Root, "dashboard-run.log"), Arguments: arguments,
	}
}

func (c *Controller) consumeEvent(id string, event DashboardEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run := c.runs[id]
	if run == nil || run.Status.Terminal() {
		return
	}
	if value, ok := event.Data["run_id"].(string); ok && value != "" {
		run.CognitiveRunID = value
	}
	switch event.Kind {
	case "cognitive.plan.created":
		run.Status = StatusPlanning
	case "orchestrator.execution.started", "runtime.agent.submitted", "runtime.agent.dispatched":
		run.Status = StatusRunning
		if role, _ := event.Data["role"].(string); role == "research.synthesize" {
			run.Status = StatusSynthesizing
		}
	case "cognitive.observation.created":
		run.Status = StatusObserving
	case "cognitive.replan.requested", "cognitive.plan.revised":
		run.Status = StatusReplanning
	case "research.report.validation.started":
		run.Status = StatusSynthesizing
	case "cognitive.goal.completed":
		// The cognitive goal has completed, but the child process still has to
		// flush telemetry, export the report, and persist its summary. Only
		// execute() may publish a terminal Dashboard state after those steps.
		run.Status = StatusSynthesizing
	case "cognitive.loop.aborted":
		// Preserve the diagnostic while the child process finalizes. Marking the
		// run terminal here would stop the browser before final artifacts exist.
		run.Status = StatusSynthesizing
		if value, _ := event.Data["error"].(string); value != "" {
			run.Error = boundedText(value, 512)
		}
	}
}

func (c *Controller) loadHistory() error {
	entries, err := os.ReadDir(filepath.Join(c.options.Root, "runs"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(c.options.Root, "runs", entry.Name())
		data, err := readBounded(filepath.Join(root, "dashboard-run.json"), 64*1024)
		if err != nil {
			continue
		}
		var record RunRecord
		if json.Unmarshal(data, &record) != nil || record.ID != entry.Name() {
			continue
		}
		record.Root = root
		if !record.Status.Terminal() {
			record.Status = StatusFailed
			record.Error = "dashboard restarted before the run reached a terminal state"
		}
		run := &Run{RunRecord: record}
		run.Events = NewEventStore(record.ID, nil)
		tailer := &EventTailer{Path: filepath.Join(root, "runtime-events.jsonl"), Store: run.Events}
		_ = tailer.ReadAvailable()
		_ = backfillPersistentRunViews(run)
		c.runs[record.ID] = run
		c.order = append(c.order, record.ID)
	}
	sort.Slice(c.order, func(i, j int) bool { return c.runs[c.order[i]].CreatedAt.After(c.runs[c.order[j]].CreatedAt) })
	c.trimHistoryLocked()
	return nil
}

func (c *Controller) trimHistoryLocked() {
	if len(c.order) <= c.options.MaxHistory {
		return
	}
	for _, id := range c.order[c.options.MaxHistory:] {
		if id != c.activeID {
			delete(c.runs, id)
		}
	}
	c.order = c.order[:c.options.MaxHistory]
}

func (c *Controller) persistLocked(run *Run) error {
	return writeJSONAtomic(filepath.Join(run.Root, "dashboard-run.json"), run.RunRecord)
}

func persistRunViews(run *Run) error {
	if run == nil || run.Events == nil {
		return fmt.Errorf("run and event store are required")
	}
	events := run.Events.Snapshot(0)
	plan := BuildPlanSnapshot(events)
	artifacts, err := ScanVerifiedArtifacts(run.Root, events)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		name  string
		value any
	}{
		{"plan.json", plan},
		{"papers.json", map[string]any{"papers": artifacts.Papers, "query_history": artifacts.Queries, "progress": artifacts.Progress, "quality": artifacts.Quality}},
		{"evidence.json", artifacts.Evidence},
	} {
		if err := writeJSONAtomic(filepath.Join(run.Root, item.name), item.value); err != nil {
			return err
		}
	}
	return nil
}

// backfillPersistentRunViews upgrades terminal Stage 6 histories in place.
// Existing snapshots are never replaced: this matters when a self-contained
// run directory has been relocated and its legacy events contain old absolute
// output paths.
func backfillPersistentRunViews(run *Run) error {
	if run == nil || run.Events == nil || !run.Status.Terminal() {
		return nil
	}
	events := run.Events.Snapshot(0)
	if _, err := os.Stat(filepath.Join(run.Root, "plan.json")); errors.Is(err, os.ErrNotExist) {
		plan := BuildPlanSnapshot(events)
		if len(plan.Versions) > 0 {
			if err := writeJSONAtomic(filepath.Join(run.Root, "plan.json"), plan); err != nil {
				return err
			}
		}
	} else if err != nil {
		return err
	}
	_, papersErr := os.Stat(filepath.Join(run.Root, "papers.json"))
	_, evidenceErr := os.Stat(filepath.Join(run.Root, "evidence.json"))
	if papersErr != nil && !errors.Is(papersErr, os.ErrNotExist) {
		return papersErr
	}
	if evidenceErr != nil && !errors.Is(evidenceErr, os.ErrNotExist) {
		return evidenceErr
	}
	if papersErr == nil && evidenceErr == nil {
		return nil
	}
	artifacts, err := ScanVerifiedArtifacts(run.Root, events)
	if err != nil {
		return err
	}
	if len(artifacts.Papers) == 0 && len(artifacts.Queries) == 0 && artifacts.Evidence.CandidateCount == 0 {
		return nil
	}
	if errors.Is(papersErr, os.ErrNotExist) {
		value := map[string]any{"papers": artifacts.Papers, "query_history": artifacts.Queries, "progress": artifacts.Progress}
		if err := writeJSONAtomic(filepath.Join(run.Root, "papers.json"), value); err != nil {
			return err
		}
	}
	if errors.Is(evidenceErr, os.ErrNotExist) {
		if err := writeJSONAtomic(filepath.Join(run.Root, "evidence.json"), artifacts.Evidence); err != nil {
			return err
		}
	}
	return nil
}

func readPersistedPlan(root string) (PlanSnapshot, error) {
	data, err := readBounded(filepath.Join(root, "plan.json"), maximumDashboardArtifactBytes)
	if err != nil {
		return PlanSnapshot{}, err
	}
	var snapshot PlanSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return PlanSnapshot{}, err
	}
	return snapshot, nil
}

func readPersistedArtifacts(root string) (ArtifactSnapshot, error) {
	papersData, err := readBounded(filepath.Join(root, "papers.json"), maximumDashboardArtifactBytes)
	if err != nil {
		return ArtifactSnapshot{}, err
	}
	var papers struct {
		Papers   []PaperView              `json:"papers"`
		Queries  []string                 `json:"query_history"`
		Progress ArtifactProgress         `json:"progress"`
		Quality  research.ResearchQuality `json:"quality"`
	}
	if err := json.Unmarshal(papersData, &papers); err != nil {
		return ArtifactSnapshot{}, err
	}
	evidenceData, err := readBounded(filepath.Join(root, "evidence.json"), maximumDashboardArtifactBytes)
	if err != nil {
		return ArtifactSnapshot{}, err
	}
	var evidence EvidenceSnapshot
	if err := json.Unmarshal(evidenceData, &evidence); err != nil {
		return ArtifactSnapshot{}, err
	}
	progress := papers.Progress
	progress.SearchQueries = maximum(progress.SearchQueries, len(papers.Queries))
	progress.RetrievedPapers = maximum(progress.RetrievedPapers, len(papers.Papers))
	progress.CandidateFindings = evidence.CandidateCount
	progress.SourceVerified = evidence.SourceVerifiedCount
	progress.SupportedFindings = evidence.SupportedCount
	progress.RejectedFindings = evidence.RejectedCount
	return ArtifactSnapshot{Papers: papers.Papers, Queries: papers.Queries, Evidence: evidence, Progress: progress, Quality: papers.Quality}, nil
}

func maximum(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func dashboardFailures(record RunRecord) []FailureView {
	if record.Workload == "experiment" && record.Experiment != nil {
		result := make([]FailureView, 0, len(record.Experiment.FailedAttempts))
		for _, failure := range record.Experiment.FailedAttempts {
			result = append(result, FailureView{
				Code: failure.FailureCode, Message: failure.Reason, TaskID: failure.TaskID,
				Capability: experiment.CapabilityRun, Recovered: record.Status == StatusCompleted,
			})
		}
		if len(result) > 0 || record.Status != StatusFailed {
			return result
		}
	}
	if record.Status != StatusFailed {
		return nil
	}
	cases, _ := ReadFailures(record.Root)
	result := make([]FailureView, 0, len(cases))
	for _, failure := range cases {
		if failure.Category == research.FailureEvidenceReject || failure.Category == research.FailureClaimUnsupported {
			continue
		}
		result = append(result, FailureView{
			Code: failureDisplayCode(failure.Category, failure.Reason), Message: boundedText(failure.Reason, 512),
			TaskID: failure.TaskID, Capability: failure.Capability, Recovered: failure.Recovered,
		})
	}
	if len(result) == 0 && strings.TrimSpace(record.Error) != "" {
		result = append(result, FailureView{Code: failureDisplayCode(research.FailureUnknown, record.Error), Message: boundedText(record.Error, 512)})
	}
	return result
}

func failureDisplayCode(category research.FailureCategory, reason string) string {
	lower := strings.ToLower(reason)
	switch category {
	case research.FailureProviderRate:
		return "PROVIDER_RATE_LIMIT"
	case research.FailureSearch:
		if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline") {
			return "PROVIDER_TIMEOUT"
		}
		return "SEARCH_FAILED"
	case research.FailurePDFUnavailable:
		return "PDF_UNAVAILABLE"
	case research.FailurePDFLimit:
		return "PDF_LIMIT_EXCEEDED"
	case research.FailurePDFParse:
		return "PDF_PARSE_FAILED"
	case research.FailureSection:
		return "SECTION_EXTRACTION_FAILED"
	case research.FailureLLMAnalysis:
		if strings.Contains(lower, "credential") || strings.Contains(lower, "api key") || strings.Contains(lower, "configure") {
			return "LLM_UNAVAILABLE"
		}
		return "LLM_ANALYSIS_FAILED"
	case research.FailureReplanExhausted:
		return "REPLAN_EXHAUSTED"
	case research.FailureContextLimit:
		return "CONTEXT_LIMIT"
	case research.FailureCitationReject:
		return "CITATION_VALIDATION_FAILED"
	}
	if strings.Contains(lower, "llm") && (strings.Contains(lower, "credential") || strings.Contains(lower, "configure")) {
		return "LLM_UNAVAILABLE"
	}
	if strings.Contains(lower, "provider") && (strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline")) {
		return "PROVIDER_TIMEOUT"
	}
	if strings.Contains(lower, "replan") && (strings.Contains(lower, "maximum") || strings.Contains(lower, "exhaust")) {
		return "REPLAN_EXHAUSTED"
	}
	if strings.Contains(lower, "evidence") && strings.Contains(lower, "insufficient") {
		return "EVIDENCE_INSUFFICIENT"
	}
	return "RUN_FAILED"
}

func readProcessDiagnostic(path string) string {
	data, err := readBounded(path, 256*1024)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "capsulectl:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "capsulectl:"))
		}
		if bearer := strings.Index(strings.ToLower(line), "bearer "); bearer >= 0 {
			line = line[:bearer] + "Bearer [REDACTED]"
		}
		return boundedText(line, 512)
	}
	return ""
}

func readPresets(path string) ([]DemoPreset, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := readBounded(path, 32*1024)
	if err != nil {
		return nil, fmt.Errorf("read dashboard presets: %w", err)
	}
	var presets []DemoPreset
	if strings.HasPrefix(strings.TrimSpace(string(data)), "[") {
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&presets); err != nil {
			return nil, fmt.Errorf("decode dashboard presets: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("dashboard presets contain trailing JSON")
		}
	} else {
		presets = []DemoPreset{{
			ID: "rec-research", Name: "REC Research", Description: "Full real research run.",
			Goal: strings.TrimSpace(string(data)), Mode: "real",
		}}
	}
	if len(presets) == 0 || len(presets) > 10 {
		return nil, fmt.Errorf("dashboard presets must contain 1-10 entries")
	}
	seen := make(map[string]struct{}, len(presets))
	for index := range presets {
		preset := &presets[index]
		preset.ID = strings.TrimSpace(preset.ID)
		preset.Name = strings.TrimSpace(preset.Name)
		preset.Description = strings.TrimSpace(preset.Description)
		preset.Goal = strings.TrimSpace(preset.Goal)
		preset.Workload = strings.ToLower(strings.TrimSpace(preset.Workload))
		if preset.Workload == "" {
			preset.Workload = "research"
		}
		preset.Mode = strings.ToLower(strings.TrimSpace(preset.Mode))
		preset.Scenario = strings.ToLower(strings.TrimSpace(preset.Scenario))
		preset.ExperimentDirectory = strings.TrimSpace(preset.ExperimentDirectory)
		validMode := (preset.Workload == "research" && (preset.Mode == "real" || preset.Mode == "mock")) ||
			(preset.Workload == "experiment" && preset.Mode == "local")
		if preset.ID == "" || len(preset.ID) > 64 || preset.Name == "" || len(preset.Name) > 80 ||
			preset.Goal == "" || len(preset.Goal) > 4096 || !validMode {
			return nil, fmt.Errorf("dashboard preset %d has invalid id, name, goal, or mode", index+1)
		}
		for _, char := range preset.ID {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return nil, fmt.Errorf("dashboard preset %q has an unsafe id", preset.ID)
			}
		}
		if _, exists := seen[preset.ID]; exists {
			return nil, fmt.Errorf("duplicate dashboard preset id %q", preset.ID)
		}
		seen[preset.ID] = struct{}{}
		if preset.Workload != "experiment" && preset.ExperimentDirectory != "" {
			return nil, fmt.Errorf("research dashboard preset %q cannot set experiment_directory", preset.ID)
		}
		if preset.Workload == "experiment" {
			if preset.Scenario != "resource-replan" {
				return nil, fmt.Errorf("experiment dashboard preset %q must use resource-replan", preset.ID)
			}
			if preset.ExperimentDirectory == "" {
				preset.ExperimentDirectory = DefaultExperimentDirectory
			}
			preset.ExperimentDirectory, err = normalizeExperimentDirectory(preset.ExperimentDirectory)
			if err != nil {
				return nil, fmt.Errorf("experiment dashboard preset %q: %w", preset.ID, err)
			}
		} else if preset.Mode == "real" {
			if preset.Scenario != "" {
				return nil, fmt.Errorf("real dashboard preset %q cannot set a mock scenario", preset.ID)
			}
		} else if !validMockScenario(preset.Scenario) {
			return nil, fmt.Errorf("mock dashboard preset %q has an invalid scenario", preset.ID)
		}
	}
	return presets, nil
}

func normalizeExperimentDirectory(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("experiment_directory is required")
	}
	if len(value) > maximumExperimentDirectory {
		return "", fmt.Errorf("experiment_directory exceeds %d bytes", maximumExperimentDirectory)
	}
	if strings.IndexByte(value, 0) >= 0 || strings.Contains(value, `\`) {
		return "", fmt.Errorf("experiment_directory contains an invalid character")
	}
	portablePath := strings.ReplaceAll(value, `\`, "/")
	windowsVolume := len(portablePath) >= 2 && portablePath[1] == ':' &&
		((portablePath[0] >= 'a' && portablePath[0] <= 'z') || (portablePath[0] >= 'A' && portablePath[0] <= 'Z'))
	if filepath.IsAbs(value) || strings.HasPrefix(portablePath, "/") || filepath.VolumeName(value) != "" || windowsVolume {
		return "", fmt.Errorf("experiment_directory must be relative to the configured workspace root")
	}
	for _, component := range strings.FieldsFunc(value, func(char rune) bool { return char == '/' || char == '\\' }) {
		if component == ".." {
			return "", fmt.Errorf("experiment_directory cannot contain ..")
		}
	}
	cleaned := filepath.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("experiment_directory escapes the configured workspace root")
	}
	return filepath.ToSlash(cleaned), nil
}

func validMockScenario(value string) bool {
	switch value {
	case research.MockScenarioNormal, research.MockScenarioSearchReplan, research.MockScenarioUnavailable, research.MockScenarioEvidenceReject:
		return true
	default:
		return false
	}
}

func newRunID() (string, error) {
	data := make([]byte, 6)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return fmt.Sprintf("run-%d-%s", time.Now().UTC().UnixMilli(), hex.EncodeToString(data)), nil
}

func currentDuration(record RunRecord) int64 {
	if record.StartedAt == nil {
		return 0
	}
	end := time.Now().UTC()
	if record.FinishedAt != nil {
		end = *record.FinishedAt
	}
	return end.Sub(*record.StartedAt).Milliseconds()
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	return boundedText(err.Error(), 512)
}

func applySummaryProgress(progress *ArtifactProgress, summary *research.RunSummary) {
	if summary == nil {
		return
	}
	progress.SearchQueries = summary.Search.Queries
	progress.RetrievedPapers = summary.Search.PapersRetrieved
	progress.DeduplicatedPapers = summary.Search.PapersDeduplicated
	progress.PDFsAvailable = summary.Paper.PDFsAvailable
	progress.ParsedPapers = summary.Paper.ParsedSuccessfully
	progress.Replans = summary.Planning.Replans
	progress.LLMCalls = summary.LLM.Calls
	progress.InputTokens = summary.LLM.InputTokens
	progress.OutputTokens = summary.LLM.OutputTokens
	progress.CandidateFindings = summary.Evidence.Candidates
	progress.SourceVerified = summary.Evidence.SourceVerified
	progress.SupportedFindings = summary.Evidence.Supported
	progress.RejectedFindings = summary.Evidence.Rejected
	closure := summary.Report.CitationClosure
	progress.CitationClosure = &closure
	progress.DurationMS = summary.DurationMS
}

func countReplans(events []DashboardEvent) int {
	count := 0
	for _, event := range events {
		if event.Kind == "cognitive.plan.revised" {
			count++
		}
	}
	return count
}
