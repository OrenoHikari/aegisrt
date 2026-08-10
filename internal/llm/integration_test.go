package llm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type connectivityClient struct {
	mu       sync.Mutex
	response Response
	err      error
	calls    int
	request  Request
}

func (c *connectivityClient) Generate(_ context.Context, request Request) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.request = request
	return c.response, c.err
}

func TestValidateRealLLMConfig(t *testing.T) {
	requirements := ConfigRequirements{RequireExplicitEndpoint: true, RequireCredential: true}
	tests := []struct {
		name   string
		config Config
		want   string
	}{
		{name: "endpoint", config: Config{Model: "model", APIKey: "secret"}, want: "BASE_URL"},
		{name: "model", config: Config{BaseURL: "https://llm.example/v1", APIKey: "secret"}, want: "MODEL"},
		{name: "credential", config: Config{BaseURL: "https://llm.example/v1", Model: "model"}, want: "API_KEY"},
		{name: "embedded credential", config: Config{BaseURL: "https://user:secret@llm.example/v1", Model: "model", APIKey: "secret"}, want: "embedded credentials"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateConfig(test.config, requirements); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q validation error, got %v", test.want, err)
			}
		})
	}
	if err := ValidateConfig(Config{BaseURL: "https://llm.example/v1", Model: "model", APIKey: "secret"}, requirements); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestConnectivityCheckUsesSmallStructuredRequest(t *testing.T) {
	client := &connectivityClient{response: Response{Content: `{"status":"ok"}`, Usage: &Usage{InputTokens: 8, OutputTokens: 4}}}
	result, err := CheckConnectivity(context.Background(), client, "https://llm.example/v1", "model")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reachable || !result.StructuredResponse || result.Usage == nil || client.calls != 1 {
		t.Fatalf("unexpected connectivity result: %+v calls=%d", result, client.calls)
	}
	if client.request.MaxTokens == nil || *client.request.MaxTokens != 16 || len(client.request.Messages) != 2 {
		t.Fatalf("connectivity request was not bounded: %+v", client.request)
	}
	client.response.Content = `{"status":"maybe"}`
	if _, err := CheckConnectivity(context.Background(), client, "endpoint", "model"); err == nil {
		t.Fatal("malformed connectivity contract was accepted")
	}
}

func TestBudgetClientEnforcesCallsAndRecordsProviderUsage(t *testing.T) {
	base := &connectivityClient{response: Response{Content: `{}`, Usage: &Usage{InputTokens: 3, OutputTokens: 2}}}
	client := &BudgetClient{Client: base, MaxCalls: 2}
	for index := 0; index < 2; index++ {
		if _, err := client.Generate(context.Background(), Request{Messages: []Message{{Role: "user", Content: "bounded"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Generate(context.Background(), Request{Messages: []Message{{Role: "user", Content: "blocked"}}}); !errors.Is(err, ErrCallBudgetExceeded) {
		t.Fatalf("expected call budget error, got %v", err)
	}
	stats := client.Stats()
	if stats.Calls != 2 || stats.Failures != 0 || !stats.UsageKnown || stats.InputTokens != 6 || stats.OutputTokens != 4 || base.calls != 2 {
		t.Fatalf("unexpected budget stats: %+v base_calls=%d", stats, base.calls)
	}
}

func TestBudgetClientEnforcesConcurrentReservations(t *testing.T) {
	base := &connectivityClient{response: Response{Content: `{}`}}
	client := &BudgetClient{Client: base, MaxCalls: 5}
	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _ = client.Generate(context.Background(), Request{Messages: []Message{{Role: "user", Content: "bounded"}}})
		}()
	}
	wait.Wait()
	if stats := client.Stats(); stats.Calls != 5 || base.calls != 5 {
		t.Fatalf("concurrent callers exceeded budget: stats=%+v base_calls=%d", stats, base.calls)
	}
}
