package scheduler

import (
	"testing"
	"time"

	"aegisrt/internal/pressure"
)

func TestCAPSPrioritizesLowCPUJobUnderCPUPressure(t *testing.T) {
	now := time.Now().UTC()

	candidates := []Candidate{
		{
			ID:          "cpu-heavy",
			SubmittedAt: now,
			Sequence:    1,
			Demand: Demand{
				CPU:    1.0,
				Memory: 0.1,
				IO:     0.1,
			},
		},
		{
			ID:          "memory-heavy",
			SubmittedAt: now,
			Sequence:    2,
			Demand: Demand{
				CPU:    0.1,
				Memory: 1.0,
				IO:     0.1,
			},
		},
	}

	snapshot := pressure.Snapshot{
		CPU: pressure.Resource{
			Some: pressure.Values{Avg10: 80},
		},
	}

	decision := NewCAPSPolicy().Select(
		now,
		candidates,
		snapshot,
	)

	if candidates[decision.Index].ID != "memory-heavy" {
		t.Fatalf(
			"expected memory-heavy Agent, got %s",
			candidates[decision.Index].ID,
		)
	}
}

func TestCAPSPrioritizesLowMemoryJobUnderMemoryPressure(t *testing.T) {
	now := time.Now().UTC()

	candidates := []Candidate{
		{
			ID:          "memory-heavy",
			SubmittedAt: now,
			Sequence:    1,
			Demand: Demand{
				CPU:    0.1,
				Memory: 1.0,
				IO:     0.1,
			},
		},
		{
			ID:          "cpu-heavy",
			SubmittedAt: now,
			Sequence:    2,
			Demand: Demand{
				CPU:    1.0,
				Memory: 0.1,
				IO:     0.1,
			},
		},
	}

	snapshot := pressure.Snapshot{
		Memory: pressure.Resource{
			Some: pressure.Values{Avg10: 80},
		},
	}

	decision := NewCAPSPolicy().Select(
		now,
		candidates,
		snapshot,
	)

	if candidates[decision.Index].ID != "cpu-heavy" {
		t.Fatalf(
			"expected cpu-heavy Agent, got %s",
			candidates[decision.Index].ID,
		)
	}
}

func TestCAPSAgingPreventsStarvation(t *testing.T) {
	now := time.Now().UTC()

	candidates := []Candidate{
		{
			ID:          "old-cpu-heavy",
			SubmittedAt: now.Add(-20 * time.Second),
			Sequence:    1,
			Demand: Demand{
				CPU: 1,
			},
		},
		{
			ID:          "new-light",
			SubmittedAt: now,
			Sequence:    2,
			Demand: Demand{
				CPU: 0.1,
			},
		},
	}

	snapshot := pressure.Snapshot{
		CPU: pressure.Resource{
			Some: pressure.Values{Avg10: 90},
		},
	}

	policy := NewCAPSPolicy()
	policy.AgingPerSecond = 0.1

	decision := policy.Select(now, candidates, snapshot)

	if candidates[decision.Index].ID != "old-cpu-heavy" {
		t.Fatalf(
			"expected aging to select old Agent, got %s",
			candidates[decision.Index].ID,
		)
	}
}
