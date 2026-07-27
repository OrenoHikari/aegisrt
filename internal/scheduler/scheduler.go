package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/resource"
)

var (
	ErrSchedulerStopped = errors.New("scheduler has stopped")
	ErrQueueFull        = errors.New("scheduler queue is full")
	ErrDuplicateAgent   = errors.New("Agent ID already exists")
)

// Executor is implemented by runtime.Runner.
type Executor interface {
	Run(ctx context.Context, acb *agent.ACB) error
}

// Phase describes the state of a scheduled job.
type Phase string

const (
	PhaseQueued    Phase = "QUEUED"
	PhaseRunning   Phase = "RUNNING"
	PhaseSucceeded Phase = "SUCCEEDED"
	PhaseFailed    Phase = "FAILED"
)

// Job is one Agent execution submitted to the Scheduler.
type Job struct {
	Agent   *agent.ACB
	Timeout time.Duration
}

// Record is the Scheduler's observable state for one Agent.
type Record struct {
	ID   string `json:"id"`
	Role string `json:"role"`

	Phase Phase `json:"phase"`

	SubmittedAt time.Time  `json:"submitted_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`

	Error    string `json:"error,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`

	CgroupPath    string          `json:"cgroup_path,omitempty"`
	ResourceSpec  resource.Spec   `json:"resource_spec"`
	ResourceStats *resource.Stats `json:"resource_stats,omitempty"`
}

// Scheduler provides a bounded queue and a fixed number of execution slots.
type Scheduler struct {
	executor Executor
	queue    chan Job
	stop     chan struct{}

	workerCount int

	startOnce sync.Once
	stopOnce  sync.Once
	stopped   atomic.Bool

	workers sync.WaitGroup
	pending sync.WaitGroup

	mu      sync.RWMutex
	records map[string]*Record
}

// New creates a Scheduler.
//
// queueSize limits the number of Agents waiting to be executed.
// workerCount limits the number of Agents running concurrently.
func New(
	executor Executor,
	workerCount int,
	queueSize int,
) (*Scheduler, error) {
	if executor == nil {
		return nil, fmt.Errorf("executor is required")
	}

	if workerCount <= 0 {
		return nil, fmt.Errorf("worker count must be greater than zero")
	}

	if queueSize <= 0 {
		return nil, fmt.Errorf("queue size must be greater than zero")
	}

	return &Scheduler{
		executor:    executor,
		queue:       make(chan Job, queueSize),
		stop:        make(chan struct{}),
		workerCount: workerCount,
		records:     make(map[string]*Record),
	}, nil
}

// Start launches the fixed set of execution workers.
func (s *Scheduler) Start() {
	s.startOnce.Do(func() {
		for workerID := 1; workerID <= s.workerCount; workerID++ {
			s.workers.Add(1)
			go s.worker(workerID)
		}
	})
}

// Submit places an Agent in the bounded waiting queue.
func (s *Scheduler) Submit(job Job) error {
	if s.stopped.Load() {
		return ErrSchedulerStopped
	}

	if job.Agent == nil {
		return fmt.Errorf("Agent is required")
	}

	if job.Agent.ID == "" {
		return fmt.Errorf("Agent ID is required")
	}

	if job.Timeout <= 0 {
		return fmt.Errorf("Agent timeout must be greater than zero")
	}

	now := time.Now().UTC()

	s.mu.Lock()

	if _, exists := s.records[job.Agent.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDuplicateAgent, job.Agent.ID)
	}

	s.records[job.Agent.ID] = &Record{
		ID:           job.Agent.ID,
		Role:         job.Agent.Role,
		Phase:        PhaseQueued,
		SubmittedAt:  now,
		ResourceSpec: job.Agent.Resources,
	}

	s.pending.Add(1)
	s.mu.Unlock()

	select {
	case s.queue <- job:
		return nil

	default:
		s.mu.Lock()
		delete(s.records, job.Agent.ID)
		s.mu.Unlock()

		s.pending.Done()

		return ErrQueueFull
	}
}

// Wait blocks until all accepted jobs have finished.
func (s *Scheduler) Wait() {
	s.pending.Wait()
}

// Stop terminates idle Scheduler workers.
//
// Call Wait before Stop so that accepted jobs are not abandoned.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		s.stopped.Store(true)
		close(s.stop)
		s.workers.Wait()
	})
}

// Snapshot returns a point-in-time copy of all scheduling records.
func (s *Scheduler) Snapshot() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
		select {
		case job := <-s.queue:
			s.execute(workerID, job)

		case <-s.stop:
			return
		}
	}
}

func (s *Scheduler) execute(workerID int, job Job) {
	defer s.pending.Done()

	startedAt := time.Now().UTC()

	s.mu.Lock()
	record := s.records[job.Agent.ID]
	record.Phase = PhaseRunning
	record.StartedAt = &startedAt
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		job.Timeout,
	)
	defer cancel()

	err := s.executor.Run(ctx, job.Agent)

	finishedAt := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	record = s.records[job.Agent.ID]
	record.FinishedAt = &finishedAt
	record.ExitCode = cloneInt(job.Agent.ExitCode)
	record.CgroupPath = job.Agent.CgroupPath
	record.ResourceStats = cloneStats(job.Agent.ResourceStats)

	if err != nil {
		record.Phase = PhaseFailed
		record.Error = err.Error()
		return
	}

	record.Phase = PhaseSucceeded
}

func cloneRecord(source *Record) Record {
	result := *source
	result.ExitCode = cloneInt(source.ExitCode)
	result.ResourceStats = cloneStats(source.ResourceStats)

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
