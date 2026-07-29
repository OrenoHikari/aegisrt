package runtime

import (
	"context"
	"errors"
	"fmt"

	"aegisrt/internal/agent"
	"aegisrt/internal/outputtxn"
)

// OutputFailureRetention controls failed staging output retention.
type OutputFailureRetention string

const (
	// OutputDiscardOnFailure removes failed or invalid output.
	OutputDiscardOnFailure OutputFailureRetention = "discard-on-failure"

	// OutputRetainOnFailure keeps staging output for debugging.
	OutputRetainOnFailure OutputFailureRetention = "retain-on-failure"
)

// TransactionalOutputExecutor adds atomic Agent output commits.
type TransactionalOutputExecutor struct {
	next      AgentExecutor
	outputs   *outputtxn.Manager
	retention OutputFailureRetention
}

// NewTransactionalOutputExecutor creates an output-aware wrapper.
func NewTransactionalOutputExecutor(
	next AgentExecutor,
	outputs *outputtxn.Manager,
	retention OutputFailureRetention,
) (*TransactionalOutputExecutor, error) {
	if next == nil {
		return nil, fmt.Errorf("next Agent executor is required")
	}

	if outputs == nil {
		return nil, fmt.Errorf(
			"transactional output manager is required",
		)
	}

	switch retention {
	case "":
		retention = OutputDiscardOnFailure

	case OutputDiscardOnFailure,
		OutputRetainOnFailure:
	default:
		return nil, fmt.Errorf(
			"unsupported output retention policy %q",
			retention,
		)
	}

	return &TransactionalOutputExecutor{
		next:      next,
		outputs:   outputs,
		retention: retention,
	}, nil
}

// Run executes one Agent inside a private output transaction.
func (e *TransactionalOutputExecutor) Run(
	ctx context.Context,
	acb *agent.ACB,
) error {
	if acb == nil {
		return fmt.Errorf("Agent control block is required")
	}

	transaction, err := e.outputs.Begin(acb.ID)
	if err != nil {
		return fmt.Errorf(
			"begin Agent output transaction: %w",
			err,
		)
	}

	acb.OutputState = agent.OutputStateStaging
	acb.OutputTransactionID = transaction.ID
	acb.OutputStagingPath = transaction.StagingDir
	acb.OutputCommitPath = ""
	acb.OutputManifestPath = ""
	acb.OutputCommitted = false
	acb.OutputRetained = true
	acb.OutputFileCount = 0
	acb.OutputBytes = 0
	acb.OutputError = ""

	if acb.Environment == nil {
		acb.Environment = make(map[string]string)
	}

	acb.Environment["AEGIS_OUTPUT_TRANSACTION_ID"] =
		transaction.ID

	acb.Environment["AEGIS_OUTPUT_STAGING"] =
		transaction.StagingDir

	runErr := e.next.Run(ctx, acb)
	if runErr != nil {
		return e.finishFailure(
			acb,
			transaction,
			runErr,
		)
	}

	result, commitErr := e.outputs.Commit(
		ctx,
		transaction,
	)
	if commitErr != nil {
		wrappedErr := fmt.Errorf(
			"commit Agent output transaction: %w",
			commitErr,
		)

		return e.finishFailure(
			acb,
			transaction,
			wrappedErr,
		)
	}

	acb.OutputState = agent.OutputStateCommitted
	acb.OutputCommitPath = result.FinalDir
	acb.OutputManifestPath = result.ManifestPath
	acb.OutputCommitted = true
	acb.OutputRetained = true
	acb.OutputFileCount = result.FileCount
	acb.OutputBytes = result.TotalBytes
	acb.OutputError = ""

	return nil
}

func (e *TransactionalOutputExecutor) finishFailure(
	acb *agent.ACB,
	transaction outputtxn.Transaction,
	cause error,
) error {
	acb.OutputError = cause.Error()
	acb.OutputCommitted = false

	if e.retention == OutputRetainOnFailure {
		acb.OutputState = agent.OutputStateRetained
		acb.OutputRetained = true

		return cause
	}

	abortErr := e.outputs.Abort(transaction)

	acb.OutputState = agent.OutputStateDiscarded
	acb.OutputRetained = false

	if abortErr != nil {
		return errors.Join(
			cause,
			fmt.Errorf(
				"discard failed Agent output: %w",
				abortErr,
			),
		)
	}

	return cause
}
