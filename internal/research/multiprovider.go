package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type MultiProvider struct{ Providers []LiteratureProvider }

func (p *MultiProvider) Name() string {
	names := make([]string, 0, len(p.Providers))
	for _, provider := range p.Providers {
		names = append(names, provider.Name())
	}
	return strings.Join(names, "+")
}

func (p *MultiProvider) Search(ctx context.Context, request SearchRequest) ([]Paper, error) {
	if len(p.Providers) == 0 {
		return nil, fmt.Errorf("multi-provider has no providers")
	}
	request, err := validateSearchRequest(request)
	if err != nil {
		return nil, err
	}
	var combined []Paper
	var searchErrors []error
	for _, provider := range p.Providers {
		papers, err := provider.Search(ctx, request)
		if err != nil {
			searchErrors = append(searchErrors, fmt.Errorf("%s: %w", provider.Name(), err))
			continue
		}
		combined = append(combined, papers...)
	}
	if len(combined) == 0 && len(searchErrors) == len(p.Providers) {
		return nil, errors.Join(searchErrors...)
	}
	combined = DeduplicatePapers(combined)
	if len(combined) > request.MaxResults {
		combined = combined[:request.MaxResults]
	}
	return combined, nil
}

func (p *MultiProvider) Fetch(ctx context.Context, paper Paper) (Document, error) {
	for _, provider := range p.Providers {
		if provider.Name() == paper.Provider || containsFold(paper.MetadataSources, provider.Name()) {
			document, err := provider.Fetch(ctx, paper)
			if err == nil || !errors.Is(err, ErrFullTextUnavailable) {
				return document, err
			}
		}
	}
	return Document{}, fmt.Errorf("%w: no configured provider can fetch %s", ErrFullTextUnavailable, paper.ID)
}

func DeduplicatePapers(papers []Paper) []Paper {
	result := make([]Paper, 0, len(papers))
	indexByKey := make(map[string]int)
	for _, paper := range papers {
		paper.MetadataSources = appendUniqueOrdered(paper.MetadataSources, paper.Provider)
		keys := paperDedupKeys(paper)
		match := -1
		for _, key := range keys {
			if index, exists := indexByKey[key]; exists {
				match = index
				break
			}
		}
		if match < 0 {
			result = append(result, paper)
			match = len(result) - 1
		} else {
			result[match] = mergePaperMetadata(result[match], paper)
		}
		for _, key := range paperDedupKeys(result[match]) {
			indexByKey[key] = match
		}
	}
	return result
}

func paperDedupKeys(paper Paper) []string {
	var keys []string
	if doi := normalizeDOI(paper.DOI); doi != "" {
		keys = append(keys, "doi:"+doi)
	}
	if id := normalizeArxivID(paper.ArxivID); id != "" {
		keys = append(keys, "arxiv:"+id)
	} else if paper.Provider == "arxiv" {
		if id := normalizeArxivID(paper.ID); id != "" {
			keys = append(keys, "arxiv:"+id)
		}
	}
	if title := normalizedTitle(paper.Title); title != "" && paper.Year != 0 {
		keys = append(keys, fmt.Sprintf("title:%s:%d", title, paper.Year))
	}
	return keys
}

func mergePaperMetadata(primary, secondary Paper) Paper {
	if primary.DOI == "" {
		primary.DOI = secondary.DOI
	}
	if primary.ArxivID == "" {
		primary.ArxivID = secondary.ArxivID
	}
	if primary.Abstract == "" {
		primary.Abstract = secondary.Abstract
	}
	if primary.Venue == "" {
		primary.Venue = secondary.Venue
	}
	if primary.URL == "" {
		primary.URL = secondary.URL
	}
	if primary.PDFURL == "" {
		primary.PDFURL = secondary.PDFURL
	}
	if len(primary.Authors) == 0 {
		primary.Authors = append([]string(nil), secondary.Authors...)
	}
	primary.FullTextAvailable = primary.FullTextAvailable || secondary.FullTextAvailable
	primary.MetadataSources = appendUniqueOrdered(primary.MetadataSources, secondary.MetadataSources...)
	primary.MetadataSources = appendUniqueOrdered(primary.MetadataSources, secondary.Provider)
	return primary
}

func normalizedTitle(value string) string {
	var result strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if space && result.Len() > 0 {
				result.WriteByte(' ')
			}
			space = false
			result.WriteRune(r)
		} else {
			space = true
		}
	}
	return result.String()
}

func normalizeArxivID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "arxiv:")
	value = arxivVersionSuffix.ReplaceAllString(value, "")
	return value
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
