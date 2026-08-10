package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAICompatibleClientGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer secret-value" {
			t.Fatalf("authorization header was not set")
		}

		var payload chatCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Model != "test-model" {
			t.Fatalf("unexpected model %q", payload.Model)
		}
		if payload.ResponseFormat == nil || payload.ResponseFormat.Type != "json_object" {
			t.Fatalf("JSON response format was not requested: %+v", payload.ResponseFormat)
		}
		if payload.Thinking == nil || payload.Thinking.Type != "disabled" {
			t.Fatalf("configured thinking mode was not sent: %+v", payload.Thinking)
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"goal\":\"ok\"}"}}]}`))
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(Config{
		BaseURL:  server.URL + "/v1",
		APIKey:   "secret-value",
		Model:    "test-model",
		Thinking: "disabled",
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	response, err := client.Generate(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "plan this"}},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if response.Content != `{"goal":"ok"}` {
		t.Fatalf("unexpected content %q", response.Content)
	}
}

func TestOpenAICompatibleClientHTTPError(t *testing.T) {
	const secret = "highly-sensitive-test-token"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, `{"error":"rate limited: `+secret+`"}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(Config{BaseURL: server.URL, Model: "test-model", APIKey: secret})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, err = client.Generate(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "plan this"}},
	})

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected HTTPError, got %v", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests ||
		!strings.Contains(httpErr.Body, "rate limited") {
		t.Fatalf("unexpected HTTP error: %+v", httpErr)
	}
	if strings.Contains(httpErr.Error(), secret) {
		t.Fatal("HTTP error leaked the configured API key")
	}
}

func TestOpenAICompatibleClientMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"choices":`))
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(Config{BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, err = client.Generate(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "plan this"}},
	})
	if err == nil || !strings.Contains(err.Error(), "decode LLM response") {
		t.Fatalf("expected malformed response error, got %v", err)
	}
}

func TestOpenAICompatibleClientEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	client, err := NewOpenAICompatibleClient(Config{BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, err = client.Generate(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "plan this"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("expected empty response error, got %v", err)
	}
}
