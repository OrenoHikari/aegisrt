package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

var ErrCallBudgetExceeded = errors.New("LLM call budget exceeded")

type ConfigRequirements struct {
	RequireExplicitEndpoint bool
	RequireCredential       bool
}

// ValidateConfig validates presence and syntax without making a network call.
func ValidateConfig(config Config, requirements ConfigRequirements) error {
	baseURL := strings.TrimSpace(config.BaseURL)
	if requirements.RequireExplicitEndpoint && baseURL == "" {
		return fmt.Errorf("CAPSULE_LLM_BASE_URL is required for a real research run")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("CAPSULE_LLM_BASE_URL must be an http(s) endpoint without embedded credentials")
	}
	if strings.TrimSpace(config.Model) == "" {
		return fmt.Errorf("CAPSULE_LLM_MODEL is required for a real research run")
	}
	if requirements.RequireCredential && strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("CAPSULE_LLM_API_KEY is required for a real research run")
	}
	return nil
}

func SanitizedEndpoint(config Config) string {
	value := strings.TrimSpace(config.BaseURL)
	if value == "" {
		value = defaultBaseURL
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "invalid"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

type ConnectivityResult struct {
	Endpoint           string `json:"endpoint"`
	Model              string `json:"model"`
	Reachable          bool   `json:"reachable"`
	StructuredResponse bool   `json:"structured_response"`
	LatencyMillis      int64  `json:"latency_ms"`
	Usage              *Usage `json:"usage,omitempty"`
}

func CheckConnectivity(ctx context.Context, client Client, endpoint, model string) (ConnectivityResult, error) {
	result := ConnectivityResult{Endpoint: endpoint, Model: strings.TrimSpace(model)}
	if client == nil {
		return result, fmt.Errorf("LLM connectivity client is required")
	}
	maximum := 16
	temperature := 0.0
	started := time.Now()
	response, err := client.Generate(ctx, Request{Messages: []Message{
		{Role: "system", Content: `Return exactly this json object: {"status":"ok"}. Return no other text.`},
		{Role: "user", Content: "health check"},
	}, Temperature: &temperature, MaxTokens: &maximum, JSONMode: true})
	result.LatencyMillis = time.Since(started).Milliseconds()
	if err != nil {
		return result, err
	}
	result.Reachable = true
	decoder := json.NewDecoder(bytes.NewBufferString(response.Content))
	decoder.DisallowUnknownFields()
	var value struct {
		Status string `json:"status"`
	}
	if err := decoder.Decode(&value); err != nil {
		return result, fmt.Errorf("LLM connectivity response is not structured JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || value.Status != "ok" {
		return result, fmt.Errorf("LLM connectivity response failed the structured contract")
	}
	result.StructuredResponse = true
	result.Usage = response.Usage
	return result, nil
}

func CheckOpenAICompatibleConnectivity(ctx context.Context, config Config) (ConnectivityResult, error) {
	if err := ValidateConfig(config, ConfigRequirements{RequireExplicitEndpoint: true, RequireCredential: true}); err != nil {
		return ConnectivityResult{Endpoint: SanitizedEndpoint(config), Model: strings.TrimSpace(config.Model)}, err
	}
	client, err := NewOpenAICompatibleClient(config)
	if err != nil {
		return ConnectivityResult{}, err
	}
	return CheckConnectivity(ctx, client, SanitizedEndpoint(config), config.Model)
}

type CallStats struct {
	Calls        int
	Failures     int
	InputTokens  int
	OutputTokens int
	UsageKnown   bool
}

// BudgetClient reserves calls before dispatch, making the hard call limit safe
// under concurrent planning/decision requests.
type BudgetClient struct {
	Client   Client
	MaxCalls int
	mu       sync.Mutex
	stats    CallStats
}

func (c *BudgetClient) Generate(ctx context.Context, request Request) (Response, error) {
	if c.Client == nil {
		return Response{}, fmt.Errorf("budgeted LLM client is required")
	}
	c.mu.Lock()
	if c.MaxCalls <= 0 || c.stats.Calls >= c.MaxCalls {
		c.mu.Unlock()
		return Response{}, fmt.Errorf("%w: maximum %d", ErrCallBudgetExceeded, c.MaxCalls)
	}
	c.stats.Calls++
	c.mu.Unlock()
	response, err := c.Client.Generate(ctx, request)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.stats.Failures++
	}
	if response.Usage != nil {
		c.stats.InputTokens += response.Usage.InputTokens
		c.stats.OutputTokens += response.Usage.OutputTokens
		c.stats.UsageKnown = true
	}
	return response, err
}

func (c *BudgetClient) Stats() CallStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}
