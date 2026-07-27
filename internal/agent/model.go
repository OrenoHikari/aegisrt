package agent

import "time"

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
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Command   string    `json:"command"`
	Args      []string  `json:"args"`
	State     State     `json:"state"`
	PID       int       `json:"pid,omitempty"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
