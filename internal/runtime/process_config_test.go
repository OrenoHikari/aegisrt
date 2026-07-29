package runtime

import (
	"os/exec"
	"strings"
	"testing"

	"aegisrt/internal/agent"
)

func TestConfigureCommandAppliesDirectoryAndEnvironment(
	t *testing.T,
) {
	acb := agent.New(
		"agent-process-config",
		"test",
		"/usr/bin/true",
		nil,
	)

	acb.WorkingDirectory = "/tmp"
	acb.Environment = map[string]string{
		"AEGIS_TEST_VALUE": "configured",
	}

	command := exec.Command(acb.Command)
	configureCommand(command, acb)

	if command.Dir != "/tmp" {
		t.Fatalf(
			"expected /tmp working directory, got %q",
			command.Dir,
		)
	}

	found := false

	for _, item := range command.Env {
		if strings.HasPrefix(
			item,
			"AEGIS_TEST_VALUE=",
		) {
			found = item == "AEGIS_TEST_VALUE=configured"
			break
		}
	}

	if !found {
		t.Fatal("configured environment variable is missing")
	}
}
