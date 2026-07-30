package controlclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestClientGet(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			if request.URL.Path != "/v1/events" {
				t.Fatalf(
					"unexpected path %q",
					request.URL.Path,
				)
			}

			if request.URL.Query().Get("since") != "10" {
				t.Fatalf(
					"unexpected query %q",
					request.URL.RawQuery,
				)
			}

			writer.Header().Set(
				"Content-Type",
				"application/json",
			)

			_, _ = writer.Write(
				[]byte(`{"count":1}`),
			)
		}),
	)
	defer server.Close()

	client, err := New(
		server.URL,
		time.Second,
	)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	data, _, err := client.Get(
		context.Background(),
		"/v1/events",
		url.Values{
			"since": []string{"10"},
		},
	)
	if err != nil {
		t.Fatalf("request API: %v", err)
	}

	if string(data) != `{"count":1}` {
		t.Fatalf(
			"unexpected body %q",
			data,
		)
	}
}

func TestClientReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(
			writer http.ResponseWriter,
			_ *http.Request,
		) {
			http.Error(
				writer,
				"not ready",
				http.StatusServiceUnavailable,
			)
		}),
	)
	defer server.Close()

	client, err := New(
		server.URL,
		time.Second,
	)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, _, err = client.Get(
		context.Background(),
		"/readyz",
		nil,
	)

	var httpErr *HTTPError

	if !errors.As(err, &httpErr) {
		t.Fatalf(
			"expected HTTPError, got %v",
			err,
		)
	}

	if httpErr.StatusCode !=
		http.StatusServiceUnavailable {
		t.Fatalf(
			"expected 503, got %d",
			httpErr.StatusCode,
		)
	}
}
