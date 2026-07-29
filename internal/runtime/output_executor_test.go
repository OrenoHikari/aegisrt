package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"aegisrt/internal/agent"
	"aegisrt/internal/outputtxn"
)

type outputProbeExecutor struct {
	fail bool
}

func (e *outputProbeExecutor) Run(
	_ context.Context,
	acb *agent.ACB,
) error {
	staging := acb.Environment["AEGIS_OUTPUT_STAGING"]

	if staging == "" {
		return errors.New(
			"AEGIS_OUTPUT_STAGING is missing",
		)
	}

	path := filepath.Join(
		staging,
		"results",
		"answer.txt",
	)

	if err := os.MkdirAll(
		filepath.Dir(path),
		0o755,
	); err != nil {
		return err
	}

	if err := os.WriteFile(
		path,
		[]byte("committable output"),
		0o644,
	); err != nil {
		return err
	}

	if e.fail {
		return errors.New("simulated Agent failure")
	}

	return nil
}

func TestOutputExecutorCommitsSuccessfulOutput(
	t *testing.T,
) {
	manager, err := outputtxn.Open(
		t.TempDir(),
		outputtxn.DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open output manager: %v", err)
	}

	executor, err :=
		NewTransactionalOutputExecutor(
			&outputProbeExecutor{},
			manager,
			OutputDiscardOnFailure,
		)
	if err != nil {
		t.Fatalf("create output executor: %v", err)
	}

	acb := agent.New(
		"agent-output-success",
		"test",
		"fake",
		nil,
	)

	if err := executor.Run(
		context.Background(),
		acb,
	); err != nil {
		t.Fatalf("execute Agent: %v", err)
	}

	if !acb.OutputCommitted {
		t.Fatal("successful output was not committed")
	}

	if acb.OutputState !=
		agent.OutputStateCommitted {
		t.Fatalf(
			"unexpected output state %s",
			acb.OutputState,
		)
	}

	if acb.OutputFileCount != 1 {
		t.Fatalf(
			"expected one output file, got %d",
			acb.OutputFileCount,
		)
	}

	if _, err := os.Stat(
		filepath.Join(
			acb.OutputCommitPath,
			"results",
			"answer.txt",
		),
	); err != nil {
		t.Fatalf("committed output is missing: %v", err)
	}

	if _, err := os.Stat(
		acb.OutputStagingPath,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"staging directory still exists: %v",
			err,
		)
	}
}

func TestOutputExecutorDiscardsFailedOutput(
	t *testing.T,
) {
	manager, err := outputtxn.Open(
		t.TempDir(),
		outputtxn.DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open output manager: %v", err)
	}

	executor, err :=
		NewTransactionalOutputExecutor(
			&outputProbeExecutor{fail: true},
			manager,
			OutputDiscardOnFailure,
		)
	if err != nil {
		t.Fatalf("create output executor: %v", err)
	}

	acb := agent.New(
		"agent-output-failure",
		"test",
		"fake",
		nil,
	)

	err = executor.Run(context.Background(), acb)
	if err == nil {
		t.Fatal("expected simulated failure")
	}

	if acb.OutputCommitted {
		t.Fatal("failed output was committed")
	}

	if acb.OutputState !=
		agent.OutputStateDiscarded {
		t.Fatalf(
			"unexpected output state %s",
			acb.OutputState,
		)
	}

	if _, err := os.Stat(
		acb.OutputStagingPath,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"failed staging directory still exists: %v",
			err,
		)
	}
}

func TestOutputExecutorRetainsFailedOutput(
	t *testing.T,
) {
	manager, err := outputtxn.Open(
		t.TempDir(),
		outputtxn.DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open output manager: %v", err)
	}

	executor, err :=
		NewTransactionalOutputExecutor(
			&outputProbeExecutor{fail: true},
			manager,
			OutputRetainOnFailure,
		)
	if err != nil {
		t.Fatalf("create output executor: %v", err)
	}

	acb := agent.New(
		"agent-output-retained",
		"test",
		"fake",
		nil,
	)

	err = executor.Run(context.Background(), acb)
	if err == nil {
		t.Fatal("expected simulated failure")
	}

	if acb.OutputState != agent.OutputStateRetained {
		t.Fatalf(
			"unexpected output state %s",
			acb.OutputState,
		)
	}

	if _, err := os.Stat(
		acb.OutputStagingPath,
	); err != nil {
		t.Fatalf(
			"retained staging output is unavailable: %v",
			err,
		)
	}

	transaction := outputtxn.Transaction{
		ID:         acb.OutputTransactionID,
		AgentID:    acb.ID,
		StagingDir: acb.OutputStagingPath,
		FinalDir: filepath.Join(
			manager.CommittedRoot(),
			acb.ID,
			acb.OutputTransactionID,
		),
	}

	if err := manager.Abort(transaction); err != nil {
		t.Fatalf("cleanup retained transaction: %v", err)
	}
}
