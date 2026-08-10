package research

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

const validCrossrefResponse = `{
  "status":"ok",
  "message":{"items":[{
    "DOI":"10.1234/Example.DOI",
    "title":["  Formal   Publication Metadata "],
    "container-title":["Example Journal"],
    "abstract":"<jats:p>A structured abstract.</jats:p>",
    "URL":"https://doi.org/10.1234/example.doi",
    "author":[{"given":"Ada","family":"Researcher"}],
    "published":{"date-parts":[[2024,4,2]]},
    "link":[{"URL":"https://publisher.example/paper.pdf","content-type":"application/pdf"}]
  }]}
}`

func TestCrossrefProviderNormalAndMalformed(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("query.bibliographic") != "formal publication" || request.Header.Get("User-Agent") == "" {
			t.Errorf("Crossref request was not normalized: %s", request.URL)
		}
		return testHTTPResponse(http.StatusOK, "application/json", validCrossrefResponse), nil
	})}
	provider, err := NewCrossrefProvider(CrossrefOptions{Endpoint: "https://crossref.example/works", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	papers, err := provider.Search(context.Background(), SearchRequest{Query: "formal publication", MaxResults: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 1 || papers[0].DOI != "10.1234/example.doi" || papers[0].Title != "Formal Publication Metadata" || papers[0].Venue != "Example Journal" || papers[0].Provider != "crossref" {
		t.Fatalf("unexpected Crossref metadata: %+v", papers)
	}
	if papers[0].FullTextAvailable || papers[0].Abstract != "A structured abstract." {
		t.Fatalf("Crossref access boundary or abstract normalization failed: %+v", papers[0])
	}

	malformedClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return testHTTPResponse(http.StatusOK, "application/json", `{"status":"failed","message":{}}`), nil
	})}
	malformed, _ := NewCrossrefProvider(CrossrefOptions{Endpoint: "https://crossref.example/works", HTTPClient: malformedClient})
	if _, err := malformed.Search(context.Background(), SearchRequest{Query: "bad", MaxResults: 1}); !errors.Is(err, ErrMalformedProviderResponse) {
		t.Fatalf("malformed Crossref response accepted: %v", err)
	}
}

func TestCrossrefProviderTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	provider, _ := NewCrossrefProvider(CrossrefOptions{Endpoint: "https://crossref.example/works", HTTPClient: client})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := provider.Search(ctx, SearchRequest{Query: "timeout", MaxResults: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected Crossref timeout, got %v", err)
	}
}

func TestProviderDeduplicationPriorityAndMerge(t *testing.T) {
	papers := []Paper{
		{ID: "arxiv-1", Title: "A Shared Paper", Year: 2024, DOI: "10.1000/shared", ArxivID: "2401.00001", Provider: "arxiv", URL: "https://arxiv.org/abs/2401.00001", FullTextAvailable: true},
		{ID: "10.1000/shared", Title: "A shared paper!", Year: 2024, DOI: "https://doi.org/10.1000/SHARED", Venue: "Journal", Provider: "crossref", URL: "https://doi.org/10.1000/shared"},
		{ID: "title-only", Title: "Another: Result", Year: 2023, Provider: "one", URL: "https://example.test/one"},
		{ID: "title-copy", Title: "Another Result", Year: 2023, Provider: "two", URL: "https://example.test/two"},
	}
	result := DeduplicatePapers(papers)
	if len(result) != 2 {
		t.Fatalf("deduplication produced %d papers: %+v", len(result), result)
	}
	if result[0].Provider != "arxiv" || result[0].Venue != "Journal" || !result[0].FullTextAvailable || !containsFold(result[0].MetadataSources, "crossref") {
		t.Fatalf("metadata merge overwrote provenance or omitted fields: %+v", result[0])
	}
}

type countingProvider struct {
	calls int
}

func (p *countingProvider) Name() string { return "counting" }
func (p *countingProvider) Search(_ context.Context, request SearchRequest) ([]Paper, error) {
	p.calls++
	return []Paper{{ID: request.Query, Title: "Cached", Year: 2024, URL: "https://example.test", Provider: p.Name()}}, nil
}
func (p *countingProvider) Fetch(context.Context, Paper) (Document, error) {
	return Document{}, ErrFullTextUnavailable
}

func TestSearchCacheHitTTLAndIsolation(t *testing.T) {
	base := &countingProvider{}
	cache, err := NewCachingProvider(base, t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	request := SearchRequest{Query: "cache query", MaxResults: 1}
	if _, err := cache.Search(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Search(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if base.calls != 1 || !cache.LastSearchCacheHit() {
		t.Fatalf("fresh cache was not used: calls=%d hit=%t", base.calls, cache.LastSearchCacheHit())
	}
	now = now.Add(2 * time.Hour)
	if _, err := cache.Search(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if base.calls != 2 || cache.LastSearchCacheHit() {
		t.Fatalf("expired cache was used: calls=%d hit=%t", base.calls, cache.LastSearchCacheHit())
	}
	if _, err := cache.Search(context.Background(), SearchRequest{Query: "other query", MaxResults: 1}); err != nil {
		t.Fatal(err)
	}
	if base.calls != 3 {
		t.Fatalf("different query shared a cache entry: %d", base.calls)
	}
}
