package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/resource"
)

// Event is an observable lifecycle event produced by the Runtime.
type Event struct {
	Timestamp string      `json:"timestamp"`
	AgentID   string      `json:"agent_id"`
	Role      string      `json:"role"`
	State     agent.State `json:"state"`

	PID      int    `json:"pid,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`

	CgroupPath    string          `json:"cgroup_path,omitempty"`
	ResourceSpec  resource.Spec   `json:"resource_spec"`
	ResourceStats *resource.Stats `json:"resource_stats,omitempty"`
}

// Runner starts, monitors, isolates, and reclaims Agent processes.
type Runner struct {
	Log       io.Writer
	Resources *resource.Manager
}

func (r *Runner) emit(acb *agent.ACB, message string) {
	event := Event{
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		AgentID:       acb.ID,
		Role:          acb.Role,
		State:         acb.State,
		PID:           acb.PID,
		ExitCode:      acb.ExitCode,
		Message:       message,
		Error:         acb.Error,
		CgroupPath:    acb.CgroupPath,
		ResourceSpec:  acb.Resources,
		ResourceStats: acb.ResourceStats,
	}

	encoder := json.NewEncoder(r.Log)
	_ = encoder.Encode(event)
}

// Run executes one Agent and records its lifecycle.
func (r *Runner) Run(ctx context.Context, acb *agent.ACB) error {
	r.emit(acb, "agent control block created")

	acb.Transition(agent.StateReady)
	r.emit(acb, "agent is ready")

	var group *resource.Group

	if r.Resources != nil {
		var err error

		group, err = r.Resources.Create(acb.ID, acb.Resources)
		if err != nil {
			acb.Error = err.Error()
			acb.Transition(agent.StateFailed)
			r.emit(acb, "failed to create Agent resource domain")

			return fmt.Errorf("create Agent resource domain: %w", err)
		}

		acb.CgroupPath = group.Path
		defer group.Cleanup()

		r.emit(acb, "Agent resource domain created")
	}

	cmd := exec.Command(acb.Command, acb.Args...)
	cmd.Stdout = r.Log
	cmd.Stderr = r.Log

	if err := cmd.Start(); err != nil {
		acb.Error = err.Error()
		acb.Transition(agent.StateFailed)
		r.emit(acb, "failed to start Agent process")

		return fmt.Errorf("start Agent: %w", err)
	}

	acb.PID = cmd.Process.Pid

	if group != nil {
		if err := group.Attach(acb.PID); err != nil {
			// The child may still be outside the Agent cgroup if Attach failed.
			// Try both resource-domain termination and direct process termination.
			group.Kill()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()

			acb.Error = err.Error()
			acb.Transition(agent.StateFailed)
			r.emit(acb, "failed to attach Agent to resource domain")

			return fmt.Errorf("attach Agent to resource domain: %w", err)
		}
	}

	acb.Transition(agent.StateRunning)
	r.emit(acb, "Agent process started")

	waitResult := make(chan error, 1)

	go func() {
		waitResult <- cmd.Wait()
	}()

	select {
	case err := <-waitResult:
		setExitCode(acb, cmd)

		if group != nil {
			collectResourceStats(acb, group)
		}

		if err != nil {
			acb.Error = err.Error()
			acb.Transition(agent.StateFailed)
			r.emit(acb, "Agent exited with an error")

			return fmt.Errorf("wait for Agent: %w", err)
		}

		acb.Transition(agent.StateCompleted)
		r.emit(acb, "Agent completed successfully")

		return nil

	case <-ctx.Done():
		if group != nil {
			// Kill the whole Agent fault domain, including descendants.
			group.Kill()
		}

		// Fallback in case the process was not successfully placed in the cgroup.
		_ = cmd.Process.Kill()

		// Wait is still required to reclaim the child process.
		<-waitResult

		setExitCode(acb, cmd)

		if group != nil {
			collectResourceStats(acb, group)
		}

		acb.Error = ctx.Err().Error()
		acb.Transition(agent.StateFailed)
		r.emit(acb, "Agent terminated by Runtime timeout")

		return fmt.Errorf("Agent context ended: %w", ctx.Err())
	}
}

func collectResourceStats(acb *agent.ACB, group *resource.Group) {
	stats := group.Stats()
	acb.ResourceStats = &stats
}

func setExitCode(acb *agent.ACB, cmd *exec.Cmd) {
	if cmd.ProcessState == nil {
		return
	}

	exitCode := cmd.ProcessState.ExitCode()
	acb.ExitCode = &exitCode
}
