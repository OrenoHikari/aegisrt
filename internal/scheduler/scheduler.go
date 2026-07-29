package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/contextstore"
	"aegisrt/internal/pressure"
	"aegisrt/internal/resource"
)

var (
	ErrSchedulerStopped  = errors.New("scheduler has stopped")
	ErrQueueFull         = errors.New("scheduler queue is full")
	ErrDuplicateAgent    = errors.New("Agent ID already exists")
	ErrUnknownDependency = errors.New("dependency Agent does not exist")
)

// Executor is implemented by runtime.Runner.
type Executor interface {
	Run(ctx context.Context, acb *agent.ACB) error
}

// PressureSource supplies Linux resource-pressure samples.
type PressureSource interface {
	Sample() (pressure.Snapshot, error)
}

// Phase describes the state of a scheduled job.
type Phase string

const (
	PhaseQueued    Phase = "QUEUED"
	PhaseRunning   Phase = "RUNNING"
	PhaseSucceeded Phase = "SUCCEEDED"
	PhaseFailed    Phase = "FAILED"
	PhaseBlocked   Phase = "BLOCKED"
)

// Job is one Agent execution submitted to the Scheduler.
type Job struct {
	Agent     *agent.ACB
	Timeout   time.Duration
	Demand    Demand
	Contexts  []contextstore.Ref
	DependsOn []string
}

// Record is the Scheduler's observable state for one Agent.
type Record struct {
	ID   string `json:"id"`
	Role string `json:"role"`

	Phase Phase `json:"phase"`

	SubmittedAt time.Time  `json:"submitted_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`

	WorkerID int `json:"worker_id,omitempty"`

	Demand   Demand             `json:"demand"`
	Contexts []contextstore.Ref `json:"contexts,omitempty"`

	DependsOn         []string                          `json:"depends_on,omitempty"`
	DependencyOutputs map[string]agent.DependencyOutput `json:"dependency_outputs,omitempty"`

	RequestedContextBytes uint64  `json:"requested_context_bytes,omitempty"`
	ReusableContextBytes  uint64  `json:"reusable_context_bytes,omitempty"`
	ContextAffinity       float64 `json:"context_affinity,omitempty"`

	WorkspacePath     string `json:"workspace_path,omitempty"`
	WorkspaceRetained bool   `json:"workspace_retained"`

	OutputState         agent.OutputState `json:"output_state,omitempty"`
	OutputTransactionID string            `json:"output_transaction_id,omitempty"`
	OutputStagingPath   string            `json:"output_staging_path,omitempty"`
	OutputCommitPath    string            `json:"output_commit_path,omitempty"`
	OutputManifestPath  string            `json:"output_manifest_path,omitempty"`
	OutputCommitted     bool              `json:"output_committed"`
	OutputRetained      bool              `json:"output_retained"`
	OutputFileCount     int               `json:"output_file_count,omitempty"`
	OutputBytes         uint64            `json:"output_bytes,omitempty"`
	OutputError         string            `json:"output_error,omitempty"`

	OutputVerified           bool       `json:"output_verified"`
	OutputVerificationMethod string     `json:"output_verification_method,omitempty"`
	OutputManifestSHA256     string     `json:"output_manifest_sha256,omitempty"`
	OutputVerifiedAt         *time.Time `json:"output_verified_at,omitempty"`
	OutputVerificationError  string     `json:"output_verification_error,omitempty"`

	DispatchScore  float64            `json:"dispatch_score,omitempty"`
	DispatchReason string             `json:"dispatch_reason,omitempty"`
	Pressure       *pressure.Snapshot `json:"pressure_at_dispatch,omitempty"`
	PressureError  string             `json:"pressure_error,omitempty"`

	Error    string `json:"error,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`

	CgroupPath    string          `json:"cgroup_path,omitempty"`
	ResourceSpec  resource.Spec   `json:"resource_spec"`
	ResourceStats *resource.Stats `json:"resource_stats,omitempty"`
}

// Options configures the Scheduler.
type Options struct {
	WorkerCount     int
	QueueSize       int
	Policy          Policy
	PressureSource  PressureSource
	ContextRegistry contextstore.Catalog
	ContextResolver contextstore.Resolver
	OutputVerifier  OutputVerifier
	EventPublisher  EventPublisher
}

type queuedJob struct {
	Job
	submittedAt time.Time
	sequence    uint64
}

// Scheduler provides a bounded policy-driven queue.
type Scheduler struct {
	executor        Executor
	workerCount     int
	queueCapacity   int
	policy          Policy
	pressureSource  PressureSource
	contexts        contextstore.Catalog
	contextResolver contextstore.Resolver
	outputVerifier  OutputVerifier
	events          EventPublisher

	mu       sync.Mutex
	cond     *sync.Cond
	queue    []queuedJob
	records  map[string]*Record
	sequence uint64
	started  bool
	stopped  bool

	startOnce sync.Once
	stopOnce  sync.Once

	workers sync.WaitGroup
	pending sync.WaitGroup
}

// New creates a backward-compatible FIFO Scheduler.
func New(
	executor Executor,
	workerCount int,
	queueSize int,
) (*Scheduler, error) {
	return NewWithOptions(
		executor,
		Options{
			WorkerCount: workerCount,
			QueueSize:   queueSize,
			Policy:      FIFOPolicy{},
		},
	)
}

// NewWithOptions creates a policy-driven Scheduler.
func NewWithOptions(
	executor Executor,
	options Options,
) (*Scheduler, error) {
	if executor == nil {
		return nil, fmt.Errorf("executor is required")
	}

	if options.WorkerCount <= 0 {
		return nil, fmt.Errorf("worker count must be greater than zero")
	}

	if options.QueueSize <= 0 {
		return nil, fmt.Errorf("queue size must be greater than zero")
	}

	if options.Policy == nil {
		options.Policy = FIFOPolicy{}
	}

	if options.PressureSource == nil {
		options.PressureSource = zeroPressureSource{}
	}

	if options.ContextRegistry == nil {
		options.ContextRegistry = contextstore.NewRegistry()
	}

	if options.ContextResolver == nil {
		options.ContextResolver =
			contextstore.PassthroughResolver{}
	}

	if options.OutputVerifier == nil {
		options.OutputVerifier = TrustOutputVerifier{}
	}

	if options.EventPublisher == nil {
		options.EventPublisher =
			defaultEventPublisher()
	}

	scheduler := &Scheduler{
		executor:        executor,
		workerCount:     options.WorkerCount,
		queueCapacity:   options.QueueSize,
		policy:          options.Policy,
		pressureSource:  options.PressureSource,
		contexts:        options.ContextRegistry,
		contextResolver: options.ContextResolver,
		outputVerifier:  options.OutputVerifier,
		events:          options.EventPublisher,
		records:         make(map[string]*Record),
	}

	scheduler.cond = sync.NewCond(&scheduler.mu)

	return scheduler, nil
}

// Start launches the fixed set of execution workers.
func (s *Scheduler) Start() {
	s.startOnce.Do(func() {
		s.mu.Lock()

		if s.stopped {
			s.mu.Unlock()
			return
		}

		s.started = true

		for workerID := 1; workerID <= s.workerCount; workerID++ {
			s.workers.Add(1)
			go s.worker(workerID)
		}

		s.mu.Unlock()
	})
}

// Submit places an Agent in the bounded waiting queue.
func (s *Scheduler) Submit(job Job) error {
	if job.Agent == nil {
		return fmt.Errorf("Agent is required")
	}

	if job.Agent.ID == "" {
		return fmt.Errorf("Agent ID is required")
	}

	if job.Timeout <= 0 {
		return fmt.Errorf("Agent timeout must be greater than zero")
	}

	if job.Demand.isZero() {
		job.Demand = balancedDemand()
	}

	if err := job.Demand.Validate(); err != nil {
		return fmt.Errorf("invalid Agent demand: %w", err)
	}

	contexts, err :=
		s.contextResolver.Resolve(job.Contexts)
	if err != nil {
		return fmt.Errorf(
			"resolve Agent contexts: %w",
			err,
		)
	}

	job.Contexts = contexts
	job.Agent.Contexts = contextstore.CloneRefs(contexts)

	dependencies, err := normalizeDependencies(
		job.Agent.ID,
		job.DependsOn,
	)
	if err != nil {
		return fmt.Errorf(
			"invalid Agent dependencies: %w",
			err,
		)
	}

	job.DependsOn = dependencies

	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return ErrSchedulerStopped
	}

	if _, exists := s.records[job.Agent.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateAgent, job.Agent.ID)
	}

	for _, dependencyID := range job.DependsOn {
		if _, exists := s.records[dependencyID]; !exists {
			return fmt.Errorf(
				"%w: %s",
				ErrUnknownDependency,
				dependencyID,
			)
		}
	}

	if len(s.queue) >= s.queueCapacity {
		return ErrQueueFull
	}

	s.sequence++

	entry := queuedJob{
		Job:         job,
		submittedAt: now,
		sequence:    s.sequence,
	}

	s.records[job.Agent.ID] = &Record{
		ID:           job.Agent.ID,
		Role:         job.Agent.Role,
		Phase:        PhaseQueued,
		SubmittedAt:  now,
		Demand:       job.Demand,
		Contexts:     contextstore.CloneRefs(contexts),
		DependsOn:    cloneStrings(job.DependsOn),
		ResourceSpec: job.Agent.Resources,
	}

	s.pending.Add(1)
	s.queue = append(s.queue, entry)
	s.cond.Signal()

	s.emitSubmitted(
		s.records[job.Agent.ID],
	)

	return nil
}

// Wait blocks until all accepted jobs have finished.
func (s *Scheduler) Wait() {
	s.pending.Wait()
}

// Stop wakes and terminates idle Scheduler workers.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cond.Broadcast()
		s.mu.Unlock()

		s.workers.Wait()
	})
}

// Snapshot returns a point-in-time copy of all scheduling records.
func (s *Scheduler) Snapshot() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]Record, 0, len(s.records))

	for _, record := range s.records {
		result = append(result, cloneRecord(record))
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].SubmittedAt.Before(result[j].SubmittedAt)
	})

	return result
}

func (s *Scheduler) worker(workerID int) {
	defer s.workers.Done()

	for {
		entry, ok := s.take(workerID)
		if !ok {
			return
		}

		s.execute(entry)
	}
}

func (s *Scheduler) take(workerID int) (queuedJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		// Propagate permanent dependency failures through the DAG.
		for s.blockUnrunnableLocked() {
		}

		if len(s.queue) == 0 {
			if s.stopped {
				return queuedJob{}, false
			}

			s.cond.Wait()
			continue
		}

		readyQueueIndexes := make([]int, 0, len(s.queue))

		for index, entry := range s.queue {
			ready, _, _ :=
				s.dependencyStatusLocked(entry)

			if ready {
				readyQueueIndexes = append(
					readyQueueIndexes,
					index,
				)
			}
		}

		if len(readyQueueIndexes) == 0 {
			// At least one dependency is still QUEUED or RUNNING.
			s.cond.Wait()
			continue
		}

		now := time.Now().UTC()

		pressureSnapshot, sampleErr :=
			s.pressureSource.Sample()

		if pressureSnapshot.Timestamp.IsZero() {
			pressureSnapshot.Timestamp = now
		}

		contextSnapshot := s.contexts.Snapshot()

		candidates := make(
			[]Candidate,
			0,
			len(readyQueueIndexes),
		)

		for _, queueIndex := range readyQueueIndexes {
			entry := s.queue[queueIndex]

			requested :=
				contextSnapshot.RequestedBytes(entry.Contexts)

			reusable :=
				contextSnapshot.ReusableBytes(entry.Contexts)

			affinity :=
				contextSnapshot.Affinity(entry.Contexts)

			candidates = append(candidates, Candidate{
				ID:                    entry.Agent.ID,
				SubmittedAt:           entry.submittedAt,
				Sequence:              entry.sequence,
				Demand:                entry.Demand,
				RequestedContextBytes: requested,
				ReusableContextBytes:  reusable,
				ContextAffinity:       affinity,
			})
		}

		decision := s.policy.Select(
			now,
			candidates,
			pressureSnapshot,
		)

		if decision.Index < 0 ||
			decision.Index >= len(readyQueueIndexes) {
			decision = Decision{
				Index:  0,
				Reason: "policy returned invalid index; FIFO fallback",
			}
		}

		selectedQueueIndex :=
			readyQueueIndexes[decision.Index]

		entry := s.queue[selectedQueueIndex]

		_, _, dependencyOutputs :=
			s.dependencyStatusLocked(entry)

		if err := applyDependencyOutputs(
			entry.Agent,
			dependencyOutputs,
		); err != nil {
			s.blockEntryLocked(
				selectedQueueIndex,
				err.Error(),
			)
			continue
		}

		selectedCandidate := candidates[decision.Index]

		s.queue = append(
			s.queue[:selectedQueueIndex],
			s.queue[selectedQueueIndex+1:]...,
		)

		startedAt := now
		record := s.records[entry.Agent.ID]

		record.Phase = PhaseRunning
		record.StartedAt = &startedAt
		record.WorkerID = workerID
		record.DispatchScore = decision.Score
		record.DispatchReason = decision.Reason
		record.DependencyOutputs =
			cloneDependencyOutputs(dependencyOutputs)

		record.RequestedContextBytes =
			selectedCandidate.RequestedContextBytes

		record.ReusableContextBytes =
			selectedCandidate.ReusableContextBytes

		record.ContextAffinity =
			selectedCandidate.ContextAffinity

		snapshotCopy := pressureSnapshot
		record.Pressure = &snapshotCopy

		if sampleErr != nil {
			record.PressureError = sampleErr.Error()
			record.DispatchReason +=
				"; PSI unavailable, zero-pressure fallback"
		}

		_ = s.contexts.Touch(entry.Contexts)

		s.emitDispatched(record)

		return entry, true
	}
}

func (s *Scheduler) execute(entry queuedJob) {
	defer s.pending.Done()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		entry.Timeout,
	)
	defer cancel()

	runErr := s.executor.Run(ctx, entry.Agent)

	if runErr == nil && entry.Agent.OutputCommitted {
		verifyCtx, verifyCancel := context.WithTimeout(
			context.Background(),
			outputVerificationTimeout,
		)

		verification, verifyErr :=
			s.outputVerifier.Verify(
				verifyCtx,
				dependencyOutputFromACB(entry.Agent),
			)

		verifyCancel()

		if verifyErr != nil {
			entry.Agent.OutputVerified = false
			entry.Agent.OutputVerificationError =
				verifyErr.Error()

			runErr = fmt.Errorf(
				"verify committed Agent output: %w",
				verifyErr,
			)
		} else {
			verifiedAt := verification.VerifiedAt

			if verifiedAt.IsZero() {
				verifiedAt = time.Now().UTC()
			}

			entry.Agent.OutputVerified = true
			entry.Agent.OutputVerificationMethod =
				verification.Method
			entry.Agent.OutputManifestSHA256 =
				verification.ManifestSHA256
			entry.Agent.OutputVerifiedAt = &verifiedAt
			entry.Agent.OutputVerificationError = ""
		}
	}

	finishedAt := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.cond.Broadcast()

	record := s.records[entry.Agent.ID]

	record.FinishedAt = &finishedAt
	record.ExitCode = cloneInt(entry.Agent.ExitCode)
	record.CgroupPath = entry.Agent.CgroupPath
	record.ResourceStats =
		cloneStats(entry.Agent.ResourceStats)

	record.WorkspacePath = entry.Agent.WorkspacePath
	record.WorkspaceRetained =
		entry.Agent.WorkspaceRetained

	record.OutputState = entry.Agent.OutputState
	record.OutputTransactionID =
		entry.Agent.OutputTransactionID
	record.OutputStagingPath =
		entry.Agent.OutputStagingPath
	record.OutputCommitPath =
		entry.Agent.OutputCommitPath
	record.OutputManifestPath =
		entry.Agent.OutputManifestPath
	record.OutputCommitted =
		entry.Agent.OutputCommitted
	record.OutputRetained =
		entry.Agent.OutputRetained
	record.OutputFileCount =
		entry.Agent.OutputFileCount
	record.OutputBytes =
		entry.Agent.OutputBytes
	record.OutputError =
		entry.Agent.OutputError

	record.OutputVerified =
		entry.Agent.OutputVerified
	record.OutputVerificationMethod =
		entry.Agent.OutputVerificationMethod
	record.OutputManifestSHA256 =
		entry.Agent.OutputManifestSHA256
	record.OutputVerificationError =
		entry.Agent.OutputVerificationError

	if entry.Agent.OutputVerifiedAt != nil {
		verifiedAt := *entry.Agent.OutputVerifiedAt
		record.OutputVerifiedAt = &verifiedAt
	}

	if runErr != nil {
		record.Phase = PhaseFailed
		record.Error = runErr.Error()

		s.emitFinished(record)
		return
	}

	record.Phase = PhaseSucceeded

	// Successful execution means the requested contexts were loaded
	// and can benefit later Agents.
	_ = s.contexts.Add(entry.Contexts)

	s.emitFinished(record)
}

func cloneRecord(source *Record) Record {
	result := *source
	result.ExitCode = cloneInt(source.ExitCode)
	result.ResourceStats = cloneStats(source.ResourceStats)
	result.Contexts = contextstore.CloneRefs(source.Contexts)
	result.DependsOn = cloneStrings(source.DependsOn)
	result.DependencyOutputs =
		cloneDependencyOutputs(source.DependencyOutputs)

	if source.Pressure != nil {
		snapshot := *source.Pressure
		result.Pressure = &snapshot
	}

	if source.OutputVerifiedAt != nil {
		verifiedAt := *source.OutputVerifiedAt
		result.OutputVerifiedAt = &verifiedAt
	}

	return result
}

func cloneInt(source *int) *int {
	if source == nil {
		return nil
	}

	value := *source
	return &value
}

func cloneStats(source *resource.Stats) *resource.Stats {
	if source == nil {
		return nil
	}

	value := *source
	return &value
}

type zeroPressureSource struct{}

func (zeroPressureSource) Sample() (pressure.Snapshot, error) {
	return pressure.Snapshot{
		Timestamp: time.Now().UTC(),
	}, nil
}
