package controlclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumResponseBytes = 64 * 1024 * 1024

// HTTPError describes a non-successful Runtime API response.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string {
	message := strings.TrimSpace(e.Body)

	if message == "" {
		return fmt.Sprintf(
			"Runtime API returned %s",
			e.Status,
		)
	}

	return fmt.Sprintf(
		"Runtime API returned %s: %s",
		e.Status,
		message,
	)
}

// Client is a small AegisRT Runtime API client.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// New creates a Runtime API client.
func New(
	baseURL string,
	timeout time.Duration,
) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)

	if baseURL == "" {
		return nil, fmt.Errorf(
			"Runtime API URL is required",
		)
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf(
			"parse Runtime API URL: %w",
			err,
		)
	}

	if parsed.Scheme != "http" &&
		parsed.Scheme != "https" {
		return nil, fmt.Errorf(
			"Runtime API URL must use http or https",
		)
	}

	if parsed.Host == "" {
		return nil, fmt.Errorf(
			"Runtime API URL host is required",
		)
	}

	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &Client{
		baseURL: parsed,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// Get performs one Runtime API GET request.
func (c *Client) Get(
	ctx context.Context,
	path string,
	query url.Values,
) ([]byte, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	endpoint := *c.baseURL
	endpoint.Path =
		strings.TrimSuffix(endpoint.Path, "/") +
			"/" +
			strings.TrimPrefix(path, "/")

	if query != nil {
		endpoint.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"create Runtime API request: %w",
			err,
		)
	}

	request.Header.Set(
		"Accept",
		"application/json, text/plain",
	)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf(
			"request Runtime API: %w",
			err,
		)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(
		io.LimitReader(
			response.Body,
			maximumResponseBytes+1,
		),
	)
	if err != nil {
		return nil, "", fmt.Errorf(
			"read Runtime API response: %w",
			err,
		)
	}

	if len(data) > maximumResponseBytes {
		return nil, "", fmt.Errorf(
			"Runtime API response exceeds %d bytes",
			maximumResponseBytes,
		)
	}

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {
		return nil,
			response.Header.Get("Content-Type"),
			&HTTPError{
				StatusCode: response.StatusCode,
				Status:     response.Status,
				Body:       string(data),
			}
	}

	return data,
		response.Header.Get("Content-Type"),
		nil
}
