package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"aegisrt/internal/resource"
)

func main() {
	manager, err := resource.NewManagerFromCurrent()
	if err != nil {
		fatal(err)
	}

	if err := manager.Initialize(); err != nil {
		fatal(err)
	}

	group, err := manager.Create(
		"agent-resource-demo",
		resource.Spec{
			CPUQuotaPercent: 25,
			MemoryMaxBytes:  128 * 1024 * 1024,
			PidsMax:         16,
		},
	)
	if err != nil {
		fatal(err)
	}
	defer group.Cleanup()

	script := `
import os
import time

memory = bytearray(32 * 1024 * 1024)

print(
    f"agent pid={os.getpid()} allocated={len(memory)}",
    flush=True,
)

end = time.monotonic() + 8
value = 0

while time.monotonic() < end:
    value = (value + 1) % 1000003

print(
    f"agent completed value={value}",
    flush=True,
)
`

	cmd := exec.Command("python3", "-c", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fatal(err)
	}

	if err := group.Attach(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		fatal(err)
	}

	fmt.Printf("agent cgroup=%s\n", group.Path)

	if err := cmd.Wait(); err != nil {
		fatal(err)
	}

	stats := group.Stats()

	output, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		fatal(err)
	}

	fmt.Println(string(output))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "cgroup demo error:", err)
	os.Exit(1)
}
