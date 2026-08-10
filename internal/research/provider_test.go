package research

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const validArxivFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <id>https://arxiv.org/abs/2401.01234v2</id>
    <published>2024-01-04T12:00:00Z</published>
    <title>  A Normalized   Research Paper </title>
    <summary> A provider-neutral abstract. </summary>
    <author><name>Ada Researcher</name></author>
    <author><name>Lin Scholar</name></author>
    <link title="pdf" href="http://arxiv.org/pdf/2401.01234v2" type="application/pdf"/>
    <arxiv:doi>10.1000/example</arxiv:doi>
    <arxiv:journal_ref>Example Conference</arxiv:journal_ref>
  </entry>
</feed>`

func TestArxivProviderNormalSearchAndMetadata(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Query().Get("max_results"); got != "2" {
			t.Errorf("max_results=%q", got)
		}
		return testHTTPResponse(http.StatusOK, "application/atom+xml", validArxivFeed), nil
	})}
	provider, err := NewArxivProvider(ArxivOptions{Endpoint: "https://example.test/query", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	papers, err := provider.Search(context.Background(), SearchRequest{Query: "research", FromYear: 2023, ToYear: 2025, MaxResults: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 1 {
		t.Fatalf("papers=%d", len(papers))
	}
	paper := papers[0]
	if paper.ID != "2401.01234" || paper.Title != "A Normalized Research Paper" || paper.Year != 2024 || paper.Provider != "arxiv" {
		t.Fatalf("unexpected normalized paper: %+v", paper)
	}
	if paper.DOI != "10.1000/example" || paper.PDFURL != "https://arxiv.org/pdf/2401.01234v2" || !paper.FullTextAvailable {
		t.Fatalf("metadata was not normalized: %+v", paper)
	}
}

func TestArxivProviderEmptyResult(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return testHTTPResponse(http.StatusOK, "application/atom+xml", `<feed xmlns="http://www.w3.org/2005/Atom"></feed>`), nil
	})}
	provider, _ := NewArxivProvider(ArxivOptions{Endpoint: "https://example.test/query", HTTPClient: client})
	papers, err := provider.Search(context.Background(), SearchRequest{Query: "nothing", MaxResults: 1})
	if err != nil || len(papers) != 0 {
		t.Fatalf("papers=%v err=%v", papers, err)
	}
}

func TestArxivSearchExpressionUsesConjunctiveTermsInsteadOfExactPhrase(t *testing.T) {
	request := SearchRequest{
		Query:    "lightweight vision-language model document understanding",
		FromYear: 2023, ToYear: 2026,
	}
	want := `all:"lightweight" AND all:"vision-language" AND all:"model" AND all:"document" AND all:"understanding" AND submittedDate:[202301010000 TO 202612312359]`
	if got := arxivSearchExpression(request); got != want {
		t.Fatalf("search expression = %q, want %q", got, want)
	}
	if strings.Contains(arxivSearchExpression(SearchRequest{Query: `safe " phrase\\value`}), `all:"safe phrase`) {
		t.Fatal("multiple natural-language terms were collapsed back into an exact phrase")
	}
}

func TestArxivProviderMalformedResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return testHTTPResponse(http.StatusOK, "application/atom+xml", `<feed><entry>`), nil
	})}
	provider, _ := NewArxivProvider(ArxivOptions{Endpoint: "https://example.test/query", HTTPClient: client})
	_, err := provider.Search(context.Background(), SearchRequest{Query: "bad", MaxResults: 1})
	if !errors.Is(err, ErrMalformedProviderResponse) {
		t.Fatalf("expected malformed response, got %v", err)
	}
}

func TestArxivProviderTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	provider, _ := NewArxivProvider(ArxivOptions{Endpoint: "https://example.test/query", HTTPClient: client})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := provider.Search(ctx, SearchRequest{Query: "slow", MaxResults: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

type staticRoundTripper struct {
	status        int
	contentType   string
	contentLength int64
	body          string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testHTTPResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status),
		Header:        http.Header{"Content-Type": []string{contentType}},
		ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body)),
	}
}

func (t staticRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.status, Status: http.StatusText(t.status),
		Header:        http.Header{"Content-Type": []string{t.contentType}},
		ContentLength: t.contentLength, Body: io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

func TestArxivProviderRejectsOversizedDownload(t *testing.T) {
	client := &http.Client{Transport: staticRoundTripper{status: http.StatusOK, contentType: "application/pdf", contentLength: 9, body: "%PDF-1.7"}}
	provider, _ := NewArxivProvider(ArxivOptions{HTTPClient: client, MaxPDFBytes: 8})
	_, err := provider.Fetch(context.Background(), Paper{PDFURL: "https://arxiv.org/pdf/2401.1"})
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("expected oversized download, got %v", err)
	}
	var limitError *DownloadLimitError
	if !errors.As(err, &limitError) || limitError.ContentLength != 9 || limitError.Limit != 8 {
		t.Fatalf("structured download limit error = %#v", err)
	}
}

func TestPaperDownloadLimitIsUserConfigurableButHardBounded(t *testing.T) {
	for _, limit := range []int64{1, DefaultPaperDownloadLimitBytes, MaximumPaperDownloadLimitBytes} {
		if err := ValidatePaperDownloadLimit(limit); err != nil {
			t.Errorf("valid limit %d: %v", limit, err)
		}
	}
	for _, limit := range []int64{0, -1, MaximumPaperDownloadLimitBytes + 1} {
		if err := ValidatePaperDownloadLimit(limit); err == nil {
			t.Errorf("invalid limit %d was accepted", limit)
		}
	}
}

func TestArxivProviderRejectsInvalidContent(t *testing.T) {
	client := &http.Client{Transport: staticRoundTripper{status: http.StatusOK, contentType: "text/html", contentLength: 13, body: "<html></html>"}}
	provider, _ := NewArxivProvider(ArxivOptions{HTTPClient: client})
	_, err := provider.Fetch(context.Background(), Paper{PDFURL: "https://arxiv.org/pdf/2401.1"})
	if !errors.Is(err, ErrInvalidPaperContent) {
		t.Fatalf("expected invalid paper content, got %v", err)
	}
}

func TestArxivProviderRejectsUnsafePDFURL(t *testing.T) {
	provider, _ := NewArxivProvider(ArxivOptions{})
	for _, raw := range []string{"file:///etc/passwd", "http://arxiv.org/paper.pdf", "https://localhost/paper.pdf", "https://127.0.0.1/paper.pdf", "https://arxiv.org:8443/paper.pdf"} {
		_, err := provider.Fetch(context.Background(), Paper{PDFURL: raw})
		if !errors.Is(err, ErrFullTextUnavailable) {
			t.Errorf("URL %q: expected safety rejection, got %v", raw, err)
		}
	}
}

func TestArxivProviderRejectsRedirectOutsideAllowlist(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() == "arxiv.org" {
			response := testHTTPResponse(http.StatusFound, "text/plain", "redirect")
			response.Header.Set("Location", "https://127.0.0.1/private.pdf")
			response.Request = request
			return response, nil
		}
		t.Fatal("redirect target reached the transport")
		return nil, nil
	})}
	provider, _ := NewArxivProvider(ArxivOptions{HTTPClient: client})
	_, err := provider.Fetch(context.Background(), Paper{PDFURL: "https://arxiv.org/pdf/2401.1"})
	if !errors.Is(err, ErrFullTextUnavailable) {
		t.Fatalf("expected redirect safety rejection, got %v", err)
	}
}
