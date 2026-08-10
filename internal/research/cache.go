package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const maximumSearchCacheBytes = 10 * 1024 * 1024

type searchCacheRecord struct {
	Provider  string        `json:"provider"`
	Request   SearchRequest `json:"request"`
	CreatedAt time.Time     `json:"created_at"`
	Papers    []Paper       `json:"papers"`
}

// CachingProvider is a small local TTL cache at the LiteratureProvider
// boundary. Fetch is never cached and still passes through the provider's
// download validation.
type CachingProvider struct {
	Provider LiteratureProvider
	Root     string
	TTL      time.Duration
	now      func() time.Time
	lastHit  atomic.Bool
}

func NewCachingProvider(provider LiteratureProvider, root string, ttl time.Duration) (*CachingProvider, error) {
	if provider == nil {
		return nil, fmt.Errorf("cache provider is required")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("cache TTL must be greater than zero")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("cache directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &CachingProvider{Provider: provider, Root: absolute, TTL: ttl, now: time.Now}, nil
}

func (p *CachingProvider) Name() string { return p.Provider.Name() }

func (p *CachingProvider) LastSearchCacheHit() bool { return p.lastHit.Load() }

func (p *CachingProvider) Search(ctx context.Context, request SearchRequest) ([]Paper, error) {
	p.lastHit.Store(false)
	request, err := validateSearchRequest(request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cachePath, err := p.cachePath(request)
	if err != nil {
		return nil, err
	}
	if record, ok := p.readFresh(cachePath, request); ok {
		p.lastHit.Store(true)
		return clonePapers(record.Papers), nil
	}
	papers, err := p.Provider.Search(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record := searchCacheRecord{
		Provider: p.Provider.Name(), Request: request,
		CreatedAt: p.now().UTC(), Papers: clonePapers(papers),
	}
	if err := p.write(cachePath, record); err != nil {
		return nil, fmt.Errorf("write literature cache: %w", err)
	}
	return papers, nil
}

func (p *CachingProvider) Fetch(ctx context.Context, paper Paper) (Document, error) {
	return p.Provider.Fetch(ctx, paper)
}

func (p *CachingProvider) cachePath(request SearchRequest) (string, error) {
	keyInput, err := json.Marshal(struct {
		Provider string        `json:"provider"`
		Request  SearchRequest `json:"request"`
	}{Provider: p.Provider.Name(), Request: request})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(keyInput)
	return filepath.Join(p.Root, hex.EncodeToString(sum[:])+".json"), nil
}

func (p *CachingProvider) readFresh(path string, request SearchRequest) (searchCacheRecord, bool) {
	file, err := os.Open(path)
	if err != nil {
		return searchCacheRecord{}, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumSearchCacheBytes+1))
	if err != nil || len(data) > maximumSearchCacheBytes {
		return searchCacheRecord{}, false
	}
	var record searchCacheRecord
	if json.Unmarshal(data, &record) != nil || record.Provider != p.Provider.Name() || record.Request != request || record.CreatedAt.IsZero() {
		return searchCacheRecord{}, false
	}
	age := p.now().UTC().Sub(record.CreatedAt)
	if age < 0 || age > p.TTL {
		return searchCacheRecord{}, false
	}
	return record, true
}

func (p *CachingProvider) write(path string, record searchCacheRecord) error {
	if err := os.MkdirAll(p.Root, 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(encoded) > maximumSearchCacheBytes {
		return fmt.Errorf("cache entry exceeds %d bytes", maximumSearchCacheBytes)
	}
	temporary, err := os.CreateTemp(p.Root, ".search-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func clonePapers(source []Paper) []Paper {
	result := make([]Paper, len(source))
	for index, paper := range source {
		paper.Authors = append([]string(nil), paper.Authors...)
		paper.MetadataSources = append([]string(nil), paper.MetadataSources...)
		result[index] = paper
	}
	return result
}

type cacheHitReporter interface{ LastSearchCacheHit() bool }

func searchCacheHit(provider LiteratureProvider) bool {
	reporter, ok := provider.(cacheHitReporter)
	return ok && reporter.LastSearchCacheHit()
}
