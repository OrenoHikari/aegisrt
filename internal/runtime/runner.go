package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"aegisrt/internal/agent"
)

// Event is an observable lifecycle event produced by the Runtime.
type Event struct {
	Timestamp string      `json:"timestamp"`
	AgentID   string      `json:"agent_id"`
	Role      string      `json:"role"`
	State     agent.State `json:"state"`
	PID       int         `json:"pid,omitempty"`
	ExitCode  *int        `json:"exit_code,omitempty"`
	Message   string      `json:"message,omitempty"`
}

// Runner starts, monitors, and reclaims Agent processes.
type Runner struct {
	Log io.Writer
}

func (r *Runner) emit(acb *agent.ACB, message string) {
	event := Event{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		AgentID:   acb.ID,
		Role:      acb.Role,
		State:     acb.State,
		PID:       acb.PID,
		ExitCode:  acb.ExitCode,
		Message:   message,
	}

	encoder := json.NewEncoder(r.Log)
	_ = encoder.Encode(event)
}

// Run executes one Agent and records its lifecycle.
func (r *Runner) Run(ctx context.Context, acb *agent.ACB) error {
	r.emit(acb, "agent control block created")

	acb.Transition(agent.StateReady)
	r.emit(acb, "agent is ready")

	cmd := exec.CommandContext(ctx, acb.Command, acb.Args...)
	cmd.Stdout = r.Log
	cmd.Stderr = r.Log

	if err := cmd.Start(); err != nil {
		acb.Error = err.Error()
		acb.Transition(agent.StateFailed)
		r.emit(acb, "failed to start agent process")
		return fmt.Errorf("start agent: %w", err)
	}

	acb.PID = cmd.Process.Pid
	acb.Transition(agent.StateRunning)
	r.emit(acb, "agent process started")

	err := cmd.Wait()

	if ctx.Err() != nil {
		acb.Error = ctx.Err().Error()
		acb.Transition(agent.StateFailed)
		r.emit(acb, "agent terminated by runtime timeout")
		return fmt.Errorf("agent context ended: %w", ctx.Err())
	}

	exitCode := cmd.ProcessState.ExitCode()
	acb.ExitCode = &exitCode

	if err != nil {
		acb.Error = err.Error()
		acb.Transition(agent.StateFailed)
		r.emit(acb, "agent exited with an error")
		return fmt.Errorf("wait for agent: %w", err)
	}

	acb.Transition(agent.StateCompleted)
	r.emit(acb, "agent completed successfully")
	return nil
}
