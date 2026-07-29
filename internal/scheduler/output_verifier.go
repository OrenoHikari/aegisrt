package scheduler

import (
	"context"
	"fmt"
	"time"

	"aegisrt/internal/agent"
)

const outputVerificationTimeout = 30 * time.Second

// OutputVerifier validates a committed Agent output before Scheduler
// exposes it to downstream DAG nodes.
type OutputVerifier interface {
	Verify(
		ctx context.Context,
		output agent.DependencyOutput,
	) (agent.OutputVerification, error)
}

// TrustOutputVerifier preserves compatibility for callers that have
// not yet connected a persistent transactional output manager.
//
// Production configurations should use outputtxn.Manager instead.
type TrustOutputVerifier struct{}

// Verify checks required metadata without reading physical artifacts.
func (TrustOutputVerifier) Verify(
	_ context.Context,
	output agent.DependencyOutput,
) (agent.OutputVerification, error) {
	if output.AgentID == "" ||
		output.TransactionID == "" ||
		output.CommitPath == "" ||
		output.ManifestPath == "" {
		return agent.OutputVerification{}, fmt.Errorf(
			"committed output metadata is incomplete",
		)
	}

	return agent.OutputVerification{
		Method:     "trusted-metadata",
		VerifiedAt: time.Now().UTC(),
		FileCount:  output.FileCount,
		TotalBytes: output.TotalBytes,
	}, nil
}
