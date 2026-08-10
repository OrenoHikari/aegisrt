package research

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultArxivEndpoint    = "https://export.arxiv.org/api/query"
	defaultMetadataLimit    = 4 * 1024 * 1024
	defaultProviderTimeout  = 30 * time.Second
	minimumArxivAPIInterval = 3 * time.Second
)

var arxivVersionSuffix = regexp.MustCompile(`v[0-9]+$`)

// ArxivOptions configures the first real LiteratureProvider.
type ArxivOptions struct {
	Endpoint         string
	HTTPClient       *http.Client
	MaxMetadataBytes int64
	MaxPDFBytes      int64
	UserAgent        string
}

// ArxivProvider consumes the documented Atom API and only fetches PDFs from
// the arxiv.org host family.
type ArxivProvider struct {
	endpoint         *url.URL
	httpClient       *http.Client
	maxMetadataBytes int64
	maxPDFBytes      int64
	userAgent        string
	publicAPI        bool
}

func NewArxivProvider(options ArxivOptions) (*ArxivProvider, error) {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		endpoint = defaultArxivEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("invalid arXiv API endpoint")
	}
	metadataLimit := options.MaxMetadataBytes
	if metadataLimit <= 0 {
		metadataLimit = defaultMetadataLimit
	}
	pdfLimit := options.MaxPDFBytes
	if pdfLimit <= 0 {
		pdfLimit = DefaultPaperDownloadLimitBytes
	}
	if err := ValidatePaperDownloadLimit(pdfLimit); err != nil {
		return nil, err
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultProviderTimeout}
	}
	userAgent := strings.TrimSpace(options.UserAgent)
	if userAgent == "" {
		userAgent = "CAPSuleRT-Research/1.0 (public academic metadata client)"
	}
	return &ArxivProvider{
		endpoint: parsed, httpClient: client, maxMetadataBytes: metadataLimit,
		maxPDFBytes: pdfLimit, userAgent: userAgent,
		publicAPI: strings.EqualFold(parsed.Hostname(), "export.arxiv.org") && parsed.Path == "/api/query",
	}, nil
}

func (p *ArxivProvider) Name() string { return "arxiv" }

func (p *ArxivProvider) Search(ctx context.Context, request SearchRequest) ([]Paper, error) {
	request, err := validateSearchRequest(request)
	if err != nil {
		return nil, err
	}
	// arXiv asks clients to leave a three-second delay between API calls. A
	// research plan admits one new query per iteration, and the public provider
	// applies the delay in a cancellation-aware way before each request.
	if p.publicAPI {
		timer := time.NewTimer(minimumArxivAPIInterval)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	queryURL := *p.endpoint
	parameters := queryURL.Query()
	parameters.Set("search_query", arxivSearchExpression(request))
	parameters.Set("start", "0")
	parameters.Set("max_results", strconv.Itoa(request.MaxResults))
	parameters.Set("sortBy", "relevance")
	parameters.Set("sortOrder", "descending")
	queryURL.RawQuery = parameters.Encode()

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create arXiv search request: %w", err)
	}
	httpRequest.Header.Set("Accept", "application/atom+xml, application/xml")
	httpRequest.Header.Set("User-Agent", p.userAgent)
	response, err := p.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("search arXiv: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("search arXiv: HTTP %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, p.maxMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read arXiv response: %w", err)
	}
	if int64(len(data)) > p.maxMetadataBytes {
		return nil, fmt.Errorf("%w: metadata exceeds %d bytes", ErrMalformedProviderResponse, p.maxMetadataBytes)
	}
	var feed arxivFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedProviderResponse, err)
	}
	papers := make([]Paper, 0, len(feed.Entries))
	seenPaperIDs := make(map[string]struct{}, len(feed.Entries))
	for _, entry := range feed.Entries {
		paper, err := normalizeArxivEntry(entry)
		if err != nil {
			return nil, err
		}
		if request.FromYear != 0 && paper.Year < request.FromYear {
			continue
		}
		if request.ToYear != 0 && paper.Year > request.ToYear {
			continue
		}
		if _, duplicate := seenPaperIDs[paper.ID]; duplicate {
			continue
		}
		seenPaperIDs[paper.ID] = struct{}{}
		papers = append(papers, paper)
		if len(papers) == request.MaxResults {
			break
		}
	}
	return papers, nil
}

func (p *ArxivProvider) Fetch(ctx context.Context, paper Paper) (Document, error) {
	pdfURL, err := validateArxivPDFURL(paper.PDFURL)
	if err != nil {
		return Document{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, pdfURL.String(), nil)
	if err != nil {
		return Document{}, fmt.Errorf("create paper request: %w", err)
	}
	httpRequest.Header.Set("Accept", "application/pdf")
	httpRequest.Header.Set("User-Agent", p.userAgent)
	fetchClient := *p.httpClient
	configuredRedirectCheck := p.httpClient.CheckRedirect
	fetchClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("paper download exceeded redirect limit")
		}
		if _, err := validateArxivPDFURL(request.URL.String()); err != nil {
			return err
		}
		if configuredRedirectCheck != nil {
			return configuredRedirectCheck(request, via)
		}
		return nil
	}
	response, err := fetchClient.Do(httpRequest)
	if err != nil {
		return Document{}, fmt.Errorf("fetch paper: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusForbidden {
		return Document{}, fmt.Errorf("%w: HTTP %s", ErrFullTextUnavailable, response.Status)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Document{}, fmt.Errorf("fetch paper: HTTP %s", response.Status)
	}
	if response.ContentLength > p.maxPDFBytes {
		return Document{}, &DownloadLimitError{ContentLength: response.ContentLength, Limit: p.maxPDFBytes}
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/pdf" {
		return Document{}, fmt.Errorf("%w: expected application/pdf, got %q", ErrInvalidPaperContent, contentType)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, p.maxPDFBytes+1))
	if err != nil {
		return Document{}, fmt.Errorf("read paper: %w", err)
	}
	if int64(len(data)) > p.maxPDFBytes {
		return Document{}, &DownloadLimitError{ContentLength: int64(len(data)), Limit: p.maxPDFBytes}
	}
	if !strings.HasPrefix(string(data), "%PDF-") {
		return Document{}, fmt.Errorf("%w: PDF signature is missing", ErrInvalidPaperContent)
	}
	return Document{Paper: paper, ContentType: "application/pdf", SourceURL: pdfURL.String(), Data: data}, nil
}

func arxivSearchExpression(request SearchRequest) string {
	cleanQuery := strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '\r', '\n', '\t':
			return ' '
		default:
			return r
		}
	}, request.Query)
	terms := strings.Fields(cleanQuery)
	clauses := make([]string, 0, len(terms))
	for _, term := range terms {
		// Quoting an entire natural-language query makes arXiv treat it as one
		// exact phrase. Quote each term independently instead, then require all
		// terms. This preserves precision while tolerating normal title/abstract
		// word order, for example "lightweight vision-language document models".
		clauses = append(clauses, `all:"`+term+`"`)
	}
	expression := strings.Join(clauses, " AND ")
	if request.FromYear != 0 || request.ToYear != 0 {
		fromYear := request.FromYear
		if fromYear == 0 {
			fromYear = 1900
		}
		toYear := request.ToYear
		if toYear == 0 {
			toYear = time.Now().UTC().Year()
		}
		expression += fmt.Sprintf(" AND submittedDate:[%04d01010000 TO %04d12312359]", fromYear, toYear)
	}
	return expression
}

func validateArxivPDFURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%w: PDF URL must be public HTTPS", ErrFullTextUnavailable)
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "arxiv.org" && host != "www.arxiv.org" && host != "export.arxiv.org" {
		return nil, fmt.Errorf("%w: PDF host %q is not allowed", ErrFullTextUnavailable, host)
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return nil, fmt.Errorf("%w: PDF port %q is not allowed", ErrFullTextUnavailable, port)
	}
	return parsed, nil
}

func normalizeArxivEntry(entry arxivEntry) (Paper, error) {
	title := normalizeSpace(entry.Title)
	abstract := normalizeSpace(entry.Summary)
	identifierURL, err := url.Parse(strings.TrimSpace(entry.ID))
	if err != nil || identifierURL.Host == "" || (identifierURL.Scheme != "https" && identifierURL.Scheme != "http") {
		return Paper{}, fmt.Errorf("%w: invalid arXiv entry identifier", ErrMalformedProviderResponse)
	}
	identifierHost := strings.ToLower(identifierURL.Hostname())
	if identifierHost != "arxiv.org" && identifierHost != "www.arxiv.org" && identifierHost != "export.arxiv.org" {
		return Paper{}, fmt.Errorf("%w: unexpected arXiv identifier host", ErrMalformedProviderResponse)
	}
	identifier := arxivVersionSuffix.ReplaceAllString(path.Base(identifierURL.Path), "")
	if identifier == "" || title == "" || abstract == "" {
		return Paper{}, fmt.Errorf("%w: entry is missing id, title, or abstract", ErrMalformedProviderResponse)
	}
	published, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Published))
	if err != nil {
		return Paper{}, fmt.Errorf("%w: invalid publication time", ErrMalformedProviderResponse)
	}
	authors := make([]string, 0, len(entry.Authors))
	for _, author := range entry.Authors {
		if name := normalizeSpace(author.Name); name != "" {
			authors = append(authors, name)
		}
	}
	if len(authors) == 0 {
		return Paper{}, fmt.Errorf("%w: entry has no authors", ErrMalformedProviderResponse)
	}
	pdfURL := ""
	for _, link := range entry.Links {
		if strings.EqualFold(link.Title, "pdf") || strings.EqualFold(link.Type, "application/pdf") {
			pdfURL = strings.Replace(strings.TrimSpace(link.Href), "http://", "https://", 1)
			break
		}
	}
	return Paper{
		ID: identifier, Title: title, Authors: authors, Year: published.Year(), Abstract: abstract,
		Venue: normalizeSpace(entry.JournalRef), DOI: strings.TrimSpace(entry.DOI),
		ArxivID: identifier, URL: "https://arxiv.org/abs/" + identifier, PDFURL: pdfURL,
		Provider: "arxiv", MetadataSources: []string{"arxiv"}, FullTextAvailable: pdfURL != "",
	}, nil
}

func normalizeSpace(value string) string { return strings.Join(strings.Fields(value), " ") }

type arxivFeed struct {
	Entries []arxivEntry `xml:"entry"`
}

type arxivEntry struct {
	ID         string        `xml:"id"`
	Title      string        `xml:"title"`
	Summary    string        `xml:"summary"`
	Published  string        `xml:"published"`
	Authors    []arxivAuthor `xml:"author"`
	Links      []arxivLink   `xml:"link"`
	DOI        string        `xml:"doi"`
	JournalRef string        `xml:"journal_ref"`
}

type arxivAuthor struct {
	Name string `xml:"name"`
}

type arxivLink struct {
	Href  string `xml:"href,attr"`
	Title string `xml:"title,attr"`
	Type  string `xml:"type,attr"`
}
