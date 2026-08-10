package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrMalformedProviderResponse = errors.New("literature provider returned a malformed response")
	ErrFullTextUnavailable       = errors.New("paper full text is unavailable")
	ErrDownloadTooLarge          = errors.New("paper download exceeds the configured limit")
	ErrInvalidPaperContent       = errors.New("paper content is invalid or unsupported")
)

const (
	DefaultMaxSearchResults        = 5
	MaximumSearchResults           = 10
	DefaultPaperDownloadLimitBytes = int64(20 * 1024 * 1024)
	MaximumPaperDownloadLimitBytes = int64(64 * 1024 * 1024)
)

// DownloadLimitError preserves the provider-advertised size and the
// user-selected run budget. Callers can turn it into a structured Observation
// instead of losing the reason behind a worker exit status.
type DownloadLimitError struct {
	ContentLength int64
	Limit         int64
}

func (e *DownloadLimitError) Error() string {
	return fmt.Sprintf("%s: content length %d exceeds limit %d", ErrDownloadTooLarge, e.ContentLength, e.Limit)
}

func (e *DownloadLimitError) Unwrap() error { return ErrDownloadTooLarge }

func ValidatePaperDownloadLimit(limit int64) error {
	if limit <= 0 || limit > MaximumPaperDownloadLimitBytes {
		return fmt.Errorf("paper download limit must be between 1 and %d bytes", MaximumPaperDownloadLimitBytes)
	}
	return nil
}

// LiteratureProvider normalizes search and public full-text retrieval behind
// one provider-independent boundary.
type LiteratureProvider interface {
	Name() string
	Search(ctx context.Context, request SearchRequest) ([]Paper, error)
	Fetch(ctx context.Context, paper Paper) (Document, error)
}

func validateSearchRequest(request SearchRequest) (SearchRequest, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return SearchRequest{}, fmt.Errorf("literature query is required")
	}
	if request.MaxResults == 0 {
		request.MaxResults = DefaultMaxSearchResults
	}
	if request.MaxResults < 1 || request.MaxResults > MaximumSearchResults {
		return SearchRequest{}, fmt.Errorf("max_results must be between 1 and %d", MaximumSearchResults)
	}
	if request.FromYear < 0 || request.ToYear < 0 {
		return SearchRequest{}, fmt.Errorf("year range cannot be negative")
	}
	if request.FromYear != 0 && request.FromYear < 1900 {
		return SearchRequest{}, fmt.Errorf("from_year must be at least 1900")
	}
	if request.ToYear != 0 && request.ToYear < 1900 {
		return SearchRequest{}, fmt.Errorf("to_year must be at least 1900")
	}
	if request.FromYear != 0 && request.ToYear != 0 && request.FromYear > request.ToYear {
		return SearchRequest{}, fmt.Errorf("from_year cannot be after to_year")
	}
	return request, nil
}
