package scheduler

import (
	"testing"
	"time"

	"aegisrt/internal/pressure"
)

func TestCAPSPrefersWarmContext(t *testing.T) {
	now := time.Now().UTC()

	candidates := []Candidate{
		{
			ID:              "cold-agent",
			SubmittedAt:     now,
			Sequence:        1,
			Demand:          balancedDemand(),
			ContextAffinity: 0,
		},
		{
			ID:                    "warm-agent",
			SubmittedAt:           now,
			Sequence:              2,
			Demand:                balancedDemand(),
			RequestedContextBytes: 100,
			ReusableContextBytes:  100,
			ContextAffinity:       1,
		},
	}

	decision := NewCAPSPolicy().Select(
		now,
		candidates,
		pressure.Snapshot{},
	)

	if candidates[decision.Index].ID != "warm-agent" {
		t.Fatalf(
			"expected warm Agent, got %s",
			candidates[decision.Index].ID,
		)
	}
}

func TestHighPressureCanOverrideAffinity(t *testing.T) {
	now := time.Now().UTC()

	candidates := []Candidate{
		{
			ID:              "warm-cpu-heavy",
			SubmittedAt:     now,
			Sequence:        1,
			ContextAffinity: 1,
			Demand: Demand{
				CPU: 1,
			},
		},
		{
			ID:          "cold-cpu-light",
			SubmittedAt: now,
			Sequence:    2,
			Demand: Demand{
				CPU: 0.1,
			},
		},
	}

	snapshot := pressure.Snapshot{
		CPU: pressure.Resource{
			Some: pressure.Values{
				Avg10: 90,
			},
		},
	}

	decision := NewCAPSPolicy().Select(
		now,
		candidates,
		snapshot,
	)

	if candidates[decision.Index].ID != "cold-cpu-light" {
		t.Fatalf(
			"expected CPU-light Agent, got %s",
			candidates[decision.Index].ID,
		)
	}
}
