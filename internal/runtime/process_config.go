package runtime

import (
	"os"
	"os/exec"
	"sort"
	"strings"

	"aegisrt/internal/agent"
)

// configureCommand applies the Agent execution context to one process.
func configureCommand(
	command *exec.Cmd,
	acb *agent.ACB,
) {
	if acb.WorkingDirectory != "" {
		command.Dir = acb.WorkingDirectory
	}

	if len(acb.Environment) == 0 {
		return
	}

	environment := make(map[string]string)

	for _, item := range os.Environ() {
		key, value, found := strings.Cut(item, "=")
		if !found || key == "" {
			continue
		}

		environment[key] = value
	}

	for key, value := range acb.Environment {
		environment[key] = value
	}

	keys := make([]string, 0, len(environment))

	for key := range environment {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	command.Env = make([]string, 0, len(keys))

	for _, key := range keys {
		command.Env = append(
			command.Env,
			key+"="+environment[key],
		)
	}
}
