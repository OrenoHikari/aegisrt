// Package llm provides the cognitive plane's small, injectable model client.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL       = "https://api.openai.com/v1"
	defaultTimeout       = 60 * time.Second
	maximumResponseBytes = 4 * 1024 * 1024
)

// Message is one role/content item sent to an OpenAI-compatible endpoint.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request describes one text generation request.
type Request struct {
	Messages    []Message `json:"messages"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	JSONMode    bool      `json:"-"`
}

// Response contains the model's generated text.
type Response struct {
	Content string
	Usage   *Usage
}

// Usage contains provider-reported token counts. Nil means unavailable; the
// client never estimates missing usage.
type Usage struct {
	InputTokens  int `json:"prompt_tokens"`
	OutputTokens int `json:"completion_tokens"`
}

// Client is the model boundary consumed by Planner. Tests and offline demos
// can inject a deterministic implementation without changing the planner.
type Client interface {
	Generate(ctx context.Context, request Request) (Response, error)
}

// Config configures an OpenAI-compatible Chat Completions client.
type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
	SourceFile string
	Thinking   string
}

// ConfigFromEnvironment reads the CAPSuleRT LLM environment variables.
func ConfigFromEnvironment() Config {
	return Config{
		BaseURL:  strings.TrimSpace(os.Getenv("CAPSULE_LLM_BASE_URL")),
		APIKey:   strings.TrimSpace(os.Getenv("CAPSULE_LLM_API_KEY")),
		Model:    strings.TrimSpace(os.Getenv("CAPSULE_LLM_MODEL")),
		Thinking: strings.TrimSpace(os.Getenv("CAPSULE_LLM_THINKING")),
	}
}

// OpenAICompatibleClient calls the widely supported Chat Completions JSON
// protocol. It intentionally has no dependency on a model-provider SDK.
type OpenAICompatibleClient struct {
	endpoint   *url.URL
	apiKey     string
	model      string
	httpClient *http.Client
	thinking   string
}

// NewOpenAICompatibleClient creates a client with a bounded HTTP timeout.
func NewOpenAICompatibleClient(config Config) (*OpenAICompatibleClient, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse LLM base URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("LLM base URL must use http or https")
	}

	if parsed.Host == "" {
		return nil, fmt.Errorf("LLM base URL host is required")
	}

	model := strings.TrimSpace(config.Model)
	if model == "" {
		return nil, fmt.Errorf("LLM model is required")
	}
	thinking := strings.ToLower(strings.TrimSpace(config.Thinking))
	if thinking != "" && thinking != "enabled" && thinking != "disabled" {
		return nil, fmt.Errorf("LLM thinking mode must be enabled or disabled")
	}

	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/chat/completions"
	parsed.RawQuery = ""
	parsed.Fragment = ""

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	httpClient := &http.Client{Timeout: timeout}
	if config.HTTPClient != nil {
		copy := *config.HTTPClient
		if copy.Timeout <= 0 {
			copy.Timeout = timeout
		}
		httpClient = &copy
	}

	return &OpenAICompatibleClient{
		endpoint:   parsed,
		apiKey:     strings.TrimSpace(config.APIKey),
		model:      model,
		httpClient: httpClient,
		thinking:   thinking,
	}, nil
}

type chatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Thinking       *thinkingConfig `json:"thinking,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

// HTTPError describes a non-successful model response.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("LLM API returned %s", e.Status)
	}

	return fmt.Sprintf("LLM API returned %s: %s", e.Status, body)
}

// Generate sends one context-aware HTTP request and decodes its first choice.
func (c *OpenAICompatibleClient) Generate(
	ctx context.Context,
	request Request,
) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if len(request.Messages) == 0 {
		return Response{}, fmt.Errorf("LLM request must contain at least one message")
	}

	wireRequest := chatCompletionRequest{
		Model:       c.model,
		Messages:    request.Messages,
		Temperature: request.Temperature,
		MaxTokens:   request.MaxTokens,
	}
	if request.JSONMode {
		wireRequest.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	if c.thinking != "" {
		wireRequest.Thinking = &thinkingConfig{Type: c.thinking}
	}
	payload, err := json.Marshal(wireRequest)
	if err != nil {
		return Response{}, fmt.Errorf("encode LLM request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint.String(),
		bytes.NewReader(payload),
	)
	if err != nil {
		return Response{}, fmt.Errorf("create LLM request: %w", err)
	}

	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("request LLM API: %w", err)
	}
	defer httpResponse.Body.Close()

	data, err := io.ReadAll(io.LimitReader(httpResponse.Body, maximumResponseBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("read LLM response: %w", err)
	}

	if len(data) > maximumResponseBytes {
		return Response{}, fmt.Errorf("LLM response exceeds %d bytes", maximumResponseBytes)
	}

	if httpResponse.StatusCode < http.StatusOK ||
		httpResponse.StatusCode >= http.StatusMultipleChoices {
		return Response{}, &HTTPError{
			StatusCode: httpResponse.StatusCode,
			Status:     httpResponse.Status,
			Body:       sanitizeErrorBody(string(data), c.apiKey),
		}
	}

	var decoded chatCompletionResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Response{}, fmt.Errorf("decode LLM response: %w", err)
	}

	if len(decoded.Choices) == 0 {
		return Response{}, fmt.Errorf("LLM response contains no choices")
	}

	content := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if content == "" {
		return Response{}, fmt.Errorf("LLM response content is empty")
	}

	return Response{Content: content, Usage: decoded.Usage}, nil
}

func sanitizeErrorBody(body, secret string) string {
	body = strings.TrimSpace(body)
	if secret = strings.TrimSpace(secret); secret != "" {
		body = strings.ReplaceAll(body, secret, "[REDACTED]")
	}
	if len(body) > 2048 {
		body = body[:2048] + "…"
	}
	return body
}
