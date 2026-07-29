package agent

import (
	"time"

	"aegisrt/internal/contextstore"
	"aegisrt/internal/resource"
)

// State describes the lifecycle state of an Agent.
type State string

const (
	StateCreated   State = "CREATED"
	StateReady     State = "READY"
	StateRunning   State = "RUNNING"
	StateBlocked   State = "BLOCKED"
	StateCompleted State = "COMPLETED"
	StateFailed    State = "FAILED"
	StateCancelled State = "CANCELLED"
)

// ACB is the Agent Control Block.
// It plays a role similar to a process control block in an operating system.
type ACB struct {
	ID       string   `json:"id"`
	Role     string   `json:"role"`
	Command  string   `json:"command"`
	Args     []string `json:"args"`
	State    State    `json:"state"`
	PID      int      `json:"pid,omitempty"`
	ExitCode *int     `json:"exit_code,omitempty"`
	Error    string   `json:"error,omitempty"`

	Resources     resource.Spec   `json:"resources"`
	ResourceStats *resource.Stats `json:"resource_stats,omitempty"`
	CgroupPath    string          `json:"cgroup_path,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Contexts contains authoritative ContextFS identities resolved
	// before the Agent enters the execution queue.
	Contexts []contextstore.Ref `json:"contexts,omitempty"`

	// WorkingDirectory and Environment configure the Agent process.
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      map[string]string `json:"-"`

	// WorkspacePath remains available for observability even when the
	// workspace has already been cleaned.
	WorkspacePath     string `json:"workspace_path,omitempty"`
	WorkspaceRetained bool   `json:"workspace_retained"`
}

// New creates a new Agent Control Block.
func New(id, role, command string, args []string) *ACB {
	now := time.Now().UTC()

	return &ACB{
		ID:        id,
		Role:      role,
		Command:   command,
		Args:      args,
		State:     StateCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Transition changes the Agent lifecycle state.
func (a *ACB) Transition(next State) {
	a.State = next
	a.UpdatedAt = time.Now().UTC()
}
