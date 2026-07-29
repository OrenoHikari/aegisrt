package scheduler

// RuntimeStatus is a point-in-time Scheduler status summary.
type RuntimeStatus struct {
	Started bool `json:"started"`
	Stopped bool `json:"stopped"`

	WorkerCount   int `json:"worker_count"`
	QueueCapacity int `json:"queue_capacity"`
	QueueDepth    int `json:"queue_depth"`

	TotalAgents int           `json:"total_agents"`
	PhaseCounts map[Phase]int `json:"phase_counts"`
}

// Status returns Scheduler state without exposing mutable internals.
func (s *Scheduler) Status() RuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	phaseCounts := make(map[Phase]int)

	for _, record := range s.records {
		phaseCounts[record.Phase]++
	}

	return RuntimeStatus{
		Started:       s.started,
		Stopped:       s.stopped,
		WorkerCount:   s.workerCount,
		QueueCapacity: s.queueCapacity,
		QueueDepth:    len(s.queue),
		TotalAgents:   len(s.records),
		PhaseCounts:   phaseCounts,
	}
}

// Record returns one immutable Agent scheduling record.
func (s *Scheduler) Record(
	agentID string,
) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.records[agentID]
	if !exists {
		return Record{}, false
	}

	return cloneRecord(record), true
}
