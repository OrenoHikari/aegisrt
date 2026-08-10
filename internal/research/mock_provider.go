package research

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed fixtures/*
var mockFixtures embed.FS

// MockProvider is deterministic and completely offline while implementing the
// same interface as the real provider.
type MockProvider struct {
	scenario string
	papers   map[string]Paper
	order    []string
}

func NewMockProvider(scenario string) (*MockProvider, error) {
	data, err := mockFixtures.ReadFile("fixtures/catalog.json")
	if err != nil {
		return nil, err
	}
	var catalog []Paper
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode mock research catalog: %w", err)
	}
	provider := &MockProvider{scenario: strings.TrimSpace(scenario), papers: make(map[string]Paper)}
	for _, paper := range catalog {
		paper.MetadataSources = appendUniqueOrdered(paper.MetadataSources, "mock")
		provider.papers[paper.ID] = paper
		provider.order = append(provider.order, paper.ID)
	}
	return provider, nil
}

func (p *MockProvider) Name() string { return "mock" }

func (p *MockProvider) Search(ctx context.Context, request SearchRequest) ([]Paper, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, err := validateSearchRequest(request)
	if err != nil {
		return nil, err
	}
	ids := []string{"mock.0001", "mock.0002", "mock.0003"}
	switch p.scenario {
	case "search-replan":
		if strings.Contains(strings.ToLower(request.Query), "sparse") {
			ids = []string{"mock.0001"}
		}
	case "unavailable":
		ids = []string{"mock.unavailable", "mock.0001", "mock.0002"}
	case "empty":
		ids = nil
	case MockScenarioEvidenceReject:
		// Use the normal offline corpus; the deterministic analysis worker adds
		// one deliberately unsupported candidate for the reliability demo.
	}
	result := make([]Paper, 0, len(ids))
	for _, id := range ids {
		paper := p.papers[id]
		if request.FromYear != 0 && paper.Year < request.FromYear {
			continue
		}
		if request.ToYear != 0 && paper.Year > request.ToYear {
			continue
		}
		result = append(result, paper)
		if len(result) == request.MaxResults {
			break
		}
	}
	return result, nil
}

func (p *MockProvider) Fetch(ctx context.Context, paper Paper) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	known, exists := p.papers[paper.ID]
	if !exists || !known.FullTextAvailable {
		return Document{}, fmt.Errorf("%w: %s", ErrFullTextUnavailable, paper.ID)
	}
	data, err := mockFixtures.ReadFile("fixtures/" + paper.ID + ".txt")
	if err != nil {
		return Document{}, fmt.Errorf("%w: %s", ErrFullTextUnavailable, paper.ID)
	}
	return Document{
		Paper: known, ContentType: "text/plain", SourceURL: known.URL, Data: data,
	}, nil
}
