package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"aegisrt/internal/agent"
	agentRuntime "aegisrt/internal/runtime"
)

const maximumFailureArtifactBytes = 16 * 1024

// FailureAwareExecutor converts only the trusted experiment worker's bounded
// failure artifact into a structured Scheduler error. The staging output stays
// uncommitted and cannot be consumed as a successful dependency.
type FailureAwareExecutor struct {
	next agentRuntime.AgentExecutor
}

func NewFailureAwareExecutor(next agentRuntime.AgentExecutor) (*FailureAwareExecutor, error) {
	if next == nil {
		return nil, fmt.Errorf("next Agent executor is required")
	}
	return &FailureAwareExecutor{next: next}, nil
}

func (e *FailureAwareExecutor) Run(ctx context.Context, acb *agent.ACB) error {
	runErr := e.next.Run(ctx, acb)
	if runErr == nil || acb == nil || acb.Role != CapabilityRun || acb.OutputStagingPath == "" {
		return runErr
	}
	path := filepath.Join(acb.OutputStagingPath, "failure.json")
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > maximumFailureArtifactBytes {
		return runErr
	}
	var failure FailureObservation
	if json.Unmarshal(data, &failure) != nil || failure.Validate() != nil || failure.TaskID != acb.ID {
		return runErr
	}
	encoded, err := json.Marshal(failure)
	if err != nil {
		return runErr
	}
	return fmt.Errorf("%w; %s%s", runErr, structuredFailureMarker, encoded)
}
