package resource

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const cgroupRoot = "/sys/fs/cgroup"

var invalidName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// Spec defines the resource boundary of one Agent.
type Spec struct {
	CPUQuotaPercent uint64
	MemoryMaxBytes  uint64
	PidsMax         uint64
}

// Stats contains cgroup v2 accounting data for one Agent.
type Stats struct {
	CPUUsageUsec uint64 `json:"cpu_usage_usec"`
	MemoryPeak   uint64 `json:"memory_peak_bytes"`
	PidsPeak     uint64 `json:"pids_peak"`
	OOMKills     uint64 `json:"oom_kills"`
}

// Manager owns the cgroup subtree delegated to the Runtime service.
type Manager struct {
	RootPath   string
	AgentsPath string
}

// Group represents one Agent resource domain.
type Group struct {
	Path string
}

// NewManagerFromCurrent discovers the current process's cgroup v2 directory.
func NewManagerFromCurrent() (*Manager, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, fmt.Errorf("read current cgroup: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			relative := strings.TrimPrefix(line, "0::")
			root := filepath.Join(cgroupRoot, relative)

			return &Manager{
				RootPath:   root,
				AgentsPath: filepath.Join(root, "agents"),
			}, nil
		}
	}

	return nil, fmt.Errorf("unified cgroup v2 entry not found")
}

// Initialize prepares this hierarchy:
//
// service root
// ├── runtime/
// └── agents/
//
//	└── <agent-id>/
func (m *Manager) Initialize() error {
	var stat syscall.Statfs_t

	if err := syscall.Statfs(cgroupRoot, &stat); err != nil {
		return err
	}

	// cgroup2 filesystem magic number.
	if uint64(stat.Type) != 0x63677270 {
		return fmt.Errorf("%s is not a cgroup v2 filesystem", cgroupRoot)
	}

	// Move the Runtime into a leaf before enabling controllers below
	// the service root.
	runtimePath := filepath.Join(m.RootPath, "runtime")

	if err := os.MkdirAll(runtimePath, 0o755); err != nil {
		return fmt.Errorf("create runtime leaf: %w", err)
	}

	if err := write(
		filepath.Join(runtimePath, "cgroup.procs"),
		strconv.Itoa(os.Getpid()),
	); err != nil {
		return fmt.Errorf(
			"move daemon into runtime leaf: %w; "+
				"run through a delegated systemd service",
			err,
		)
	}

	required := []string{"cpu", "memory", "pids"}

	if err := enableControllers(m.RootPath, required); err != nil {
		return fmt.Errorf("enable root controllers: %w", err)
	}

	if err := os.MkdirAll(m.AgentsPath, 0o755); err != nil {
		return fmt.Errorf("create agents subtree: %w", err)
	}

	if err := enableControllers(m.AgentsPath, required); err != nil {
		return fmt.Errorf("enable agent controllers: %w", err)
	}

	return nil
}

// Create creates and configures an empty Agent cgroup.
func (m *Manager) Create(agentID string, spec Spec) (*Group, error) {
	if spec.CPUQuotaPercent == 0 || spec.CPUQuotaPercent > 1000 {
		return nil, fmt.Errorf(
			"CPU quota must be between 1 and 1000 percent",
		)
	}

	if spec.MemoryMaxBytes < 16*1024*1024 {
		return nil, fmt.Errorf("memory limit must be at least 16 MiB")
	}

	if spec.PidsMax == 0 {
		return nil, fmt.Errorf("process limit must be greater than zero")
	}

	name := invalidName.ReplaceAllString(agentID, "-")
	name = strings.Trim(name, ".-")

	if name == "" {
		return nil, fmt.Errorf("invalid Agent ID %q", agentID)
	}

	path := filepath.Join(m.AgentsPath, name)

	if err := os.Mkdir(path, 0o755); err != nil {
		return nil, fmt.Errorf("create Agent cgroup: %w", err)
	}

	// cpu.max format: quota period.
	// 25% -> 25000 100000.
	quota := spec.CPUQuotaPercent * 1000

	settings := map[string]string{
		"cpu.max":          fmt.Sprintf("%d 100000", quota),
		"memory.max":       strconv.FormatUint(spec.MemoryMaxBytes, 10),
		"memory.oom.group": "1",
		"pids.max":         strconv.FormatUint(spec.PidsMax, 10),
	}

	for file, value := range settings {
		if err := write(filepath.Join(path, file), value); err != nil {
			_ = os.Remove(path)

			return nil, fmt.Errorf(
				"configure %s: %w",
				file,
				err,
			)
		}
	}

	return &Group{Path: path}, nil
}

// Attach moves a process and its threads into the Agent cgroup.
func (g *Group) Attach(pid int) error {
	return write(
		filepath.Join(g.Path, "cgroup.procs"),
		strconv.Itoa(pid),
	)
}

// Stats reads accounting values while the cgroup still exists.
func (g *Group) Stats() Stats {
	cpu := readKeyValues(filepath.Join(g.Path, "cpu.stat"))
	memory := readKeyValues(filepath.Join(g.Path, "memory.events"))

	return Stats{
		CPUUsageUsec: cpu["usage_usec"],
		MemoryPeak:   readUint(filepath.Join(g.Path, "memory.peak")),
		PidsPeak:     readUint(filepath.Join(g.Path, "pids.peak")),
		OOMKills:     memory["oom_kill"],
	}
}

// Kill terminates the Agent and all descendants in its cgroup.
func (g *Group) Kill() {
	_ = write(filepath.Join(g.Path, "cgroup.kill"), "1")
}

// Cleanup waits briefly for the group to become empty and removes it.
func (g *Group) Cleanup() {
	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		events := readKeyValues(
			filepath.Join(g.Path, "cgroup.events"),
		)

		if events["populated"] == 0 {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	_ = os.Remove(g.Path)
}

func enableControllers(path string, required []string) error {
	availableData, err := os.ReadFile(
		filepath.Join(path, "cgroup.controllers"),
	)
	if err != nil {
		return err
	}

	currentData, err := os.ReadFile(
		filepath.Join(path, "cgroup.subtree_control"),
	)
	if err != nil {
		return err
	}

	available := make(map[string]bool)
	current := make(map[string]bool)

	for _, name := range strings.Fields(string(availableData)) {
		available[name] = true
	}

	for _, name := range strings.Fields(string(currentData)) {
		current[name] = true
	}

	var additions []string

	for _, name := range required {
		if !available[name] {
			return fmt.Errorf(
				"controller %q is not delegated to %s",
				name,
				path,
			)
		}

		if !current[name] {
			additions = append(additions, "+"+name)
		}
	}

	if len(additions) == 0 {
		return nil
	}

	return write(
		filepath.Join(path, "cgroup.subtree_control"),
		strings.Join(additions, " "),
	)
}

func write(path, value string) error {
	return os.WriteFile(path, []byte(value), 0o644)
}

func readUint(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	value, err := strconv.ParseUint(
		strings.TrimSpace(string(data)),
		10,
		64,
	)
	if err != nil {
		return 0
	}

	return value
}

func readKeyValues(path string) map[string]uint64 {
	result := make(map[string]uint64)

	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			result[fields[0]] = value
		}
	}

	return result
}
