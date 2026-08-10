package research

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultCrossrefEndpoint = "https://api.crossref.org/works"

var (
	crossrefHTMLTag = regexp.MustCompile(`<[^>]+>`)
	doiPattern      = regexp.MustCompile(`(?i)^10\.[0-9]{4,9}/\S+$`)
)

type CrossrefOptions struct {
	Endpoint   string
	HTTPClient *http.Client
	UserAgent  string
	Mailto     string
}

// CrossrefProvider adds formal-publication metadata. Fetch intentionally
// remains unavailable because arbitrary publisher URLs are outside the
// current download allowlist.
type CrossrefProvider struct {
	endpoint   *url.URL
	httpClient *http.Client
	userAgent  string
	mailto     string
}

func NewCrossrefProvider(options CrossrefOptions) (*CrossrefProvider, error) {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		endpoint = defaultCrossrefEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("invalid Crossref API endpoint")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultProviderTimeout}
	}
	userAgent := strings.TrimSpace(options.UserAgent)
	if userAgent == "" {
		userAgent = "CAPSuleRT-Research/1.0 (public scholarly metadata client)"
	}
	return &CrossrefProvider{endpoint: parsed, httpClient: client, userAgent: userAgent, mailto: strings.TrimSpace(options.Mailto)}, nil
}

func (p *CrossrefProvider) Name() string { return "crossref" }

func (p *CrossrefProvider) Search(ctx context.Context, request SearchRequest) ([]Paper, error) {
	request, err := validateSearchRequest(request)
	if err != nil {
		return nil, err
	}
	queryURL := *p.endpoint
	parameters := queryURL.Query()
	parameters.Set("query.bibliographic", request.Query)
	parameters.Set("rows", strconv.Itoa(request.MaxResults))
	if p.mailto != "" {
		parameters.Set("mailto", p.mailto)
	}
	var filters []string
	if request.FromYear != 0 {
		filters = append(filters, fmt.Sprintf("from-pub-date:%04d-01-01", request.FromYear))
	}
	if request.ToYear != 0 {
		filters = append(filters, fmt.Sprintf("until-pub-date:%04d-12-31", request.ToYear))
	}
	if len(filters) > 0 {
		parameters.Set("filter", strings.Join(filters, ","))
	}
	queryURL.RawQuery = parameters.Encode()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL.String(), nil)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", p.userAgent)
	response, err := p.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("search Crossref: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("search Crossref: HTTP %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, defaultMetadataLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > defaultMetadataLimit {
		return nil, fmt.Errorf("%w: Crossref response is too large", ErrMalformedProviderResponse)
	}
	var envelope crossrefEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("%w: Crossref JSON: %v", ErrMalformedProviderResponse, err)
	}
	if !strings.EqualFold(strings.TrimSpace(envelope.Status), "ok") {
		return nil, fmt.Errorf("%w: Crossref status is %q", ErrMalformedProviderResponse, envelope.Status)
	}
	papers := make([]Paper, 0, len(envelope.Message.Items))
	for _, item := range envelope.Message.Items {
		paper, normalizeErr := normalizeCrossrefItem(item)
		if normalizeErr != nil {
			continue
		}
		if (request.FromYear != 0 && paper.Year < request.FromYear) || (request.ToYear != 0 && paper.Year > request.ToYear) {
			continue
		}
		papers = append(papers, paper)
		if len(papers) == request.MaxResults {
			break
		}
	}
	return papers, nil
}

func (p *CrossrefProvider) Fetch(_ context.Context, paper Paper) (Document, error) {
	return Document{}, fmt.Errorf("%w: Crossref metadata does not authorize publisher full-text retrieval for %s", ErrFullTextUnavailable, paper.ID)
}

type crossrefEnvelope struct {
	Status  string `json:"status"`
	Message struct {
		Items []crossrefItem `json:"items"`
	} `json:"message"`
}

type crossrefItem struct {
	DOI            string           `json:"DOI"`
	Title          []string         `json:"title"`
	ContainerTitle []string         `json:"container-title"`
	Abstract       string           `json:"abstract"`
	URL            string           `json:"URL"`
	Author         []crossrefAuthor `json:"author"`
	Issued         crossrefDate     `json:"issued"`
	Published      crossrefDate     `json:"published"`
	Link           []crossrefLink   `json:"link"`
}

type crossrefAuthor struct {
	Given  string `json:"given"`
	Family string `json:"family"`
}

type crossrefDate struct {
	DateParts [][]int `json:"date-parts"`
}

type crossrefLink struct {
	URL         string `json:"URL"`
	ContentType string `json:"content-type"`
}

func normalizeCrossrefItem(item crossrefItem) (Paper, error) {
	doi := normalizeDOI(item.DOI)
	if doi == "" || len(item.Title) == 0 {
		return Paper{}, fmt.Errorf("invalid Crossref work")
	}
	title := normalizeSpace(item.Title[0])
	if title == "" {
		return Paper{}, fmt.Errorf("invalid Crossref title")
	}
	year := crossrefYear(item.Published)
	if year == 0 {
		year = crossrefYear(item.Issued)
	}
	if year == 0 || year > time.Now().UTC().Year()+1 {
		return Paper{}, fmt.Errorf("invalid Crossref year")
	}
	var authors []string
	for _, author := range item.Author {
		if name := normalizeSpace(strings.TrimSpace(author.Given + " " + author.Family)); name != "" {
			authors = append(authors, name)
		}
	}
	venue := ""
	if len(item.ContainerTitle) > 0 {
		venue = normalizeSpace(item.ContainerTitle[0])
	}
	pdfURL := ""
	for _, link := range item.Link {
		parsed, err := url.Parse(strings.TrimSpace(link.URL))
		if err == nil && parsed.Scheme == "https" && strings.EqualFold(strings.TrimSpace(link.ContentType), "application/pdf") {
			pdfURL = parsed.String()
			break
		}
	}
	landing := safePublicURL(item.URL)
	if landing == "" {
		landing = "https://doi.org/" + doi
	}
	return Paper{
		ID: doi, Title: title, Authors: authors, Year: year,
		Abstract: stripCrossrefMarkup(item.Abstract), Venue: venue, DOI: doi,
		URL: landing, PDFURL: pdfURL, Provider: "crossref", MetadataSources: []string{"crossref"},
		FullTextAvailable: false,
	}, nil
}

func safePublicURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.User != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return parsed.String()
}

func crossrefYear(value crossrefDate) int {
	if len(value.DateParts) == 0 || len(value.DateParts[0]) == 0 {
		return 0
	}
	return value.DateParts[0][0]
}

func normalizeDOI(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "https://doi.org/")
	value = strings.TrimPrefix(value, "http://doi.org/")
	if !doiPattern.MatchString(value) {
		return ""
	}
	return value
}

func stripCrossrefMarkup(value string) string {
	value = crossrefHTMLTag.ReplaceAllString(value, " ")
	return normalizeSpace(html.UnescapeString(value))
}
