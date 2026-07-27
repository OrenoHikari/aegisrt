package pressure

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Values contains one PSI line such as "some" or "full".
type Values struct {
	Avg10  float64 `json:"avg10"`
	Avg60  float64 `json:"avg60"`
	Avg300 float64 `json:"avg300"`
	Total  uint64  `json:"total_usec"`
}

// Resource contains the PSI some/full metrics of one resource.
type Resource struct {
	Some Values `json:"some"`
	Full Values `json:"full"`
}

// Snapshot is one system-wide PSI sample.
type Snapshot struct {
	Timestamp time.Time `json:"timestamp"`
	CPU       Resource  `json:"cpu"`
	Memory    Resource  `json:"memory"`
	IO        Resource  `json:"io"`
}

// Sampler provides resource-pressure information to the Scheduler.
type Sampler interface {
	Sample() (Snapshot, error)
}

// Reader reads Linux PSI files under /proc/pressure.
type Reader struct {
	Root string
	now  func() time.Time
}

// NewReader creates a PSI reader for the host system.
func NewReader() *Reader {
	return &Reader{
		Root: "/proc/pressure",
		now:  time.Now,
	}
}

// Sample reads CPU, memory, and I/O pressure.
func (r *Reader) Sample() (Snapshot, error) {
	cpu, err := readResource(filepath.Join(r.Root, "cpu"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read CPU PSI: %w", err)
	}

	memory, err := readResource(filepath.Join(r.Root, "memory"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read memory PSI: %w", err)
	}

	ioPressure, err := readResource(filepath.Join(r.Root, "io"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read I/O PSI: %w", err)
	}

	now := time.Now
	if r.now != nil {
		now = r.now
	}

	return Snapshot{
		Timestamp: now().UTC(),
		CPU:       cpu,
		Memory:    memory,
		IO:        ioPressure,
	}, nil
}

func readResource(path string) (Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Resource{}, err
	}

	return parseResource(data)
}

func parseResource(data []byte) (Resource, error) {
	var result Resource
	var foundSome bool

	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		values, err := parseValues(fields[1:])
		if err != nil {
			return Resource{}, err
		}

		switch fields[0] {
		case "some":
			result.Some = values
			foundSome = true

		case "full":
			result.Full = values
		}
	}

	if err := scanner.Err(); err != nil {
		return Resource{}, err
	}

	if !foundSome {
		return Resource{}, fmt.Errorf("PSI some line not found")
	}

	return result, nil
}

func parseValues(fields []string) (Values, error) {
	var result Values

	for _, field := range fields {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			continue
		}

		switch parts[0] {
		case "avg10":
			value, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				return Values{}, fmt.Errorf("parse avg10: %w", err)
			}
			result.Avg10 = value

		case "avg60":
			value, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				return Values{}, fmt.Errorf("parse avg60: %w", err)
			}
			result.Avg60 = value

		case "avg300":
			value, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				return Values{}, fmt.Errorf("parse avg300: %w", err)
			}
			result.Avg300 = value

		case "total":
			value, err := strconv.ParseUint(parts[1], 10, 64)
			if err != nil {
				return Values{}, fmt.Errorf("parse total: %w", err)
			}
			result.Total = value
		}
	}

	return result, nil
}
