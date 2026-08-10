package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultConfigFile      = "config/llm.local.json"
	maximumConfigFileBytes = 16 * 1024
)

type fileConfig struct {
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	Thinking string `json:"thinking,omitempty"`
}

// LoadConfig reads an optional permission-restricted local JSON file, then
// overlays non-empty environment values. The secret is never serialized by
// Config and callers must continue to sanitize provider errors.
func LoadConfig() (Config, error) {
	config := ConfigFromEnvironment()
	configuredPath := strings.TrimSpace(os.Getenv("CAPSULE_LLM_CONFIG_FILE"))
	path := configuredPath
	if path == "" {
		path = DefaultConfigFile
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && configuredPath == "" {
		return withProviderDefaults(config), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("inspect LLM config file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Config{}, fmt.Errorf("LLM config file must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Config{}, fmt.Errorf("LLM config file permissions must be 0600")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve LLM config file: %w", err)
	}
	file, err := os.Open(filepath.Clean(absolutePath))
	if err != nil {
		return Config{}, fmt.Errorf("open LLM config file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumConfigFileBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read LLM config file: %w", err)
	}
	if len(data) > maximumConfigFileBytes {
		return Config{}, fmt.Errorf("LLM config file exceeds %d bytes", maximumConfigFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var stored fileConfig
	if err := decoder.Decode(&stored); err != nil {
		return Config{}, fmt.Errorf("decode LLM config file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("LLM config file contains trailing JSON")
	}
	if config.BaseURL == "" {
		config.BaseURL = strings.TrimSpace(stored.BaseURL)
	}
	if config.APIKey == "" {
		config.APIKey = strings.TrimSpace(stored.APIKey)
	}
	if config.Model == "" {
		config.Model = strings.TrimSpace(stored.Model)
	}
	if config.Thinking == "" {
		config.Thinking = strings.TrimSpace(stored.Thinking)
	}
	config.SourceFile = absolutePath
	return withProviderDefaults(config), nil
}

func withProviderDefaults(config Config) Config {
	if strings.TrimSpace(config.Thinking) != "" {
		return config
	}
	endpoint, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err == nil && strings.EqualFold(endpoint.Hostname(), "api.deepseek.com") {
		// DeepSeek V4 defaults to thinking mode. CAPSuleRT requests bounded
		// machine-readable objects, so non-thinking mode avoids spending the
		// output budget on reasoning_content that is deliberately not consumed.
		config.Thinking = "disabled"
	}
	return config
}
