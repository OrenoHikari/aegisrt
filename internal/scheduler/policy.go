package scheduler

import (
	"fmt"
	"math"
	"time"

	"aegisrt/internal/pressure"
)

// Demand describes the normalized resource demand of one Agent.
type Demand struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
	IO     float64 `json:"io"`
}

// Validate checks that all demand values are normalized.
func (d Demand) Validate() error {
	values := map[string]float64{
		"cpu":    d.CPU,
		"memory": d.Memory,
		"io":     d.IO,
	}

	for name, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s demand must be finite", name)
		}

		if value < 0 || value > 1 {
			return fmt.Errorf(
				"%s demand must be between 0 and 1",
				name,
			)
		}
	}

	return nil
}

func (d Demand) isZero() bool {
	return d.CPU == 0 && d.Memory == 0 && d.IO == 0
}

func balancedDemand() Demand {
	return Demand{
		CPU:    0.5,
		Memory: 0.5,
		IO:     0.5,
	}
}

// Candidate is one queued Agent considered by a scheduling policy.
type Candidate struct {
	ID          string
	SubmittedAt time.Time
	Sequence    uint64
	Demand      Demand

	RequestedContextBytes uint64
	ReusableContextBytes  uint64
	ContextAffinity       float64
}

// Decision records how a policy selected one Agent.
type Decision struct {
	Index  int
	Score  float64
	Reason string
}

// Policy selects one queued Agent.
type Policy interface {
	Select(
		now time.Time,
		candidates []Candidate,
		snapshot pressure.Snapshot,
	) Decision
}

// FIFOPolicy preserves submission order.
type FIFOPolicy struct{}

// Select chooses the oldest sequence number.
func (FIFOPolicy) Select(
	_ time.Time,
	candidates []Candidate,
	_ pressure.Snapshot,
) Decision {
	selected := 0

	for index := 1; index < len(candidates); index++ {
		if candidates[index].Sequence < candidates[selected].Sequence {
			selected = index
		}
	}

	return Decision{
		Index:  selected,
		Score:  0,
		Reason: "FIFO submission order",
	}
}

// CAPSPolicy implements Context-Affinity and Pressure-aware Scheduling.
//
// Lower scores are preferred:
//
//	score = pressure penalty
//	      - context affinity benefit
//	      - waiting-age bonus
type CAPSPolicy struct {
	AgingPerSecond float64
	FullWeight     float64
	AffinityWeight float64
}

// NewCAPSPolicy returns the default CAPS policy.
func NewCAPSPolicy() *CAPSPolicy {
	return &CAPSPolicy{
		AgingPerSecond: 0.01,
		FullWeight:     2.0,
		AffinityWeight: 0.35,
	}
}

// Select chooses the Agent with the lowest combined score.
func (p *CAPSPolicy) Select(
	now time.Time,
	candidates []Candidate,
	snapshot pressure.Snapshot,
) Decision {
	if p == nil {
		p = NewCAPSPolicy()
	}

	agingPerSecond := p.AgingPerSecond
	if agingPerSecond <= 0 {
		agingPerSecond = 0.01
	}

	fullWeight := p.FullWeight
	if fullWeight <= 0 {
		fullWeight = 2
	}

	affinityWeight := p.AffinityWeight
	if affinityWeight <= 0 {
		affinityWeight = 0.35
	}

	cpuPressure := pressureLevel(snapshot.CPU, fullWeight)
	memoryPressure := pressureLevel(snapshot.Memory, fullWeight)
	ioPressure := pressureLevel(snapshot.IO, fullWeight)

	selected := 0
	selectedScore := math.Inf(1)
	selectedAge := time.Duration(0)

	for index, candidate := range candidates {
		age := now.Sub(candidate.SubmittedAt)
		if age < 0 {
			age = 0
		}

		pressurePenalty :=
			cpuPressure*candidate.Demand.CPU +
				memoryPressure*candidate.Demand.Memory +
				ioPressure*candidate.Demand.IO

		affinityBenefit :=
			affinityWeight * candidate.ContextAffinity

		ageBonus := age.Seconds() * agingPerSecond
		if ageBonus > 3 {
			ageBonus = 3
		}

		score := pressurePenalty -
			affinityBenefit -
			ageBonus

		if score < selectedScore-1e-9 ||
			(math.Abs(score-selectedScore) <= 1e-9 &&
				candidate.Sequence < candidates[selected].Sequence) {
			selected = index
			selectedScore = score
			selectedAge = age
		}
	}

	candidate := candidates[selected]

	return Decision{
		Index: selected,
		Score: selectedScore,
		Reason: fmt.Sprintf(
			"CAPS pressure cpu=%.3f memory=%.3f io=%.3f; "+
				"demand cpu=%.2f memory=%.2f io=%.2f; "+
				"context affinity=%.3f reusable=%d requested=%d; "+
				"wait=%s",
			cpuPressure,
			memoryPressure,
			ioPressure,
			candidate.Demand.CPU,
			candidate.Demand.Memory,
			candidate.Demand.IO,
			candidate.ContextAffinity,
			candidate.ReusableContextBytes,
			candidate.RequestedContextBytes,
			selectedAge.Round(time.Millisecond),
		),
	}
}

func pressureLevel(
	resource pressure.Resource,
	fullWeight float64,
) float64 {
	level := resource.Some.Avg10 +
		fullWeight*resource.Full.Avg10

	level /= 100

	if level < 0 {
		return 0
	}

	if level > 1 {
		return 1
	}

	return level
}
