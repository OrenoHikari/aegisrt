package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"aegisrt/internal/llm"
)

func runLLMCheck(arguments []string) error {
	flags := flag.NewFlagSet("agent llm-check", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	timeout := flags.Duration("timeout", 30*time.Second, "connectivity check timeout")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	config, err := llm.LoadConfig()
	if err != nil {
		return err
	}
	config.Timeout = *timeout
	requirements := llm.ConfigRequirements{RequireExplicitEndpoint: true, RequireCredential: true}
	if err := llm.ValidateConfig(config, requirements); err != nil {
		fmt.Printf("[LLM CONNECTIVITY] SKIPPED: %v\n", err)
		printLLMConfigurationTemplate()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := llm.CheckOpenAICompatibleConnectivity(ctx, config)
	if err != nil {
		return fmt.Errorf("LLM connectivity check failed: %w", err)
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println("[LLM CONNECTIVITY] PASS")
	fmt.Println(string(encoded))
	return nil
}

func printLLMConfigurationTemplate() {
	fmt.Println("Configure the ignored local file config/llm.local.json (chmod 600), or use environment variables:")
	fmt.Println("  export CAPSULE_LLM_BASE_URL=https://your-compatible-endpoint.example/v1")
	fmt.Println("  export CAPSULE_LLM_API_KEY='<set-in-your-shell>'")
	fmt.Println("  export CAPSULE_LLM_MODEL=your-model")
}
