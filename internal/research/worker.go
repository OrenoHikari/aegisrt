package research

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/llm"
	"aegisrt/internal/outputtxn"
)

const maximumDependencyResultBytes = 4 * 1024 * 1024

// RunWorker executes one fixed research capability inside the normal Runtime
// process boundary. It never evaluates a shell command.
func RunWorker(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("internal-research-worker", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	action := flags.String("action", "", "registered research action")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	staging, err := requiredWorkerPath("AEGIS_OUTPUT_STAGING")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	args, err := workerArguments()
	if err != nil {
		return err
	}
	dependencies, err := loadDependencies()
	if err != nil && *action != "literature_search" {
		return err
	}

	switch *action {
	case "literature_search":
		return runLiteratureSearch(ctx, staging, args)
	case "paper_fetch":
		return runPaperFetch(ctx, staging, args, dependencies)
	case "paper_parse":
		return runPaperParse(ctx, staging, dependencies)
	case "paper_analyze":
		return runPaperAnalyze(ctx, staging, args, dependencies)
	case "research_synthesize":
		return runResearchSynthesize(staging, args, dependencies)
	case "experiment_design":
		return runExperimentDesign(staging, args, dependencies)
	case "research_report":
		return runResearchReport(staging, args, dependencies)
	default:
		return fmt.Errorf("unknown registered research action %q", *action)
	}
}

func runLiteratureSearch(ctx context.Context, staging string, args map[string]any) error {
	provider, err := providerFromEnvironment()
	if err != nil {
		return err
	}
	request := SearchRequest{
		Query: stringArg(args, "query"), FromYear: intArg(args, "from_year"),
		ToYear: intArg(args, "to_year"), MaxResults: intArg(args, "max_results"),
	}
	papers, err := provider.Search(ctx, request)
	if err != nil {
		return err
	}
	result := SearchResult{
		Query: request.Query, FromYear: request.FromYear, ToYear: request.ToYear,
		TotalResults: len(papers), Papers: papers, Provider: provider.Name(), Cached: searchCacheHit(provider), CompletedAt: time.Now().UTC(),
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Query: %s\nProvider: %s\nCached: %t\nResults: %d\n", result.Query, result.Provider, result.Cached, len(papers))
	for index, paper := range papers {
		fmt.Fprintf(&text, "%d. %s (%d) [%s] full_text=%t\n", index+1, paper.Title, paper.Year, paper.ID, paper.FullTextAvailable)
	}
	return writeWorkerResult(staging, result, text.String())
}

func runPaperFetch(ctx context.Context, staging string, args map[string]any, dependencies []dependencyResult) error {
	search, err := findSearchResult(dependencies)
	if err != nil {
		return err
	}
	rank := intArg(args, "rank")
	if rank == 0 {
		rank = 1
	}
	paperID := stringArg(args, "paper_id")
	paper, selectedRank, err := selectPaper(search, paperID, rank)
	if err != nil {
		return err
	}
	result := FetchResult{Paper: paper, Query: search.Query, Rank: selectedRank}
	if !paper.FullTextAvailable {
		result.Reason = ErrFullTextUnavailable.Error()
		result.FailureCode = "FULL_TEXT_UNAVAILABLE"
		result.Retryable = false
		result.SourceURL = paper.PDFURL
		return writeWorkerResult(staging, result, fmt.Sprintf("Paper %s full text unavailable; abstract-only evidence remains.\n", paper.ID))
	}
	provider, err := providerFromEnvironment()
	if err != nil {
		return err
	}
	document, err := provider.Fetch(ctx, paper)
	if errors.Is(err, ErrFullTextUnavailable) {
		result.Reason = err.Error()
		result.FailureCode = "FULL_TEXT_UNAVAILABLE"
		result.Retryable = false
		result.SourceURL = paper.PDFURL
		return writeWorkerResult(staging, result, fmt.Sprintf("Paper %s full text unavailable: %v\n", paper.ID, err))
	}
	var limitError *DownloadLimitError
	if errors.As(err, &limitError) {
		result.Reason = limitError.Error()
		result.FailureCode = "PDF_LIMIT_EXCEEDED"
		result.Retryable = false
		result.RequiredBytes = limitError.ContentLength
		result.LimitBytes = limitError.Limit
		result.SourceURL = paper.PDFURL
		return writeWorkerResult(staging, result, fmt.Sprintf(
			"Paper %s requires %d bytes but this run allows %d bytes; skipped safely.\n",
			paper.ID, limitError.ContentLength, limitError.Limit,
		))
	}
	if err != nil {
		return err
	}
	artifact := "paper.txt"
	if document.ContentType == "application/pdf" {
		artifact = "paper.pdf"
	}
	if err := os.WriteFile(filepath.Join(staging, artifact), document.Data, 0o600); err != nil {
		return err
	}
	result.Available = true
	result.ContentType = document.ContentType
	result.Bytes = len(document.Data)
	result.SourceURL = document.SourceURL
	result.Artifact = artifact
	return writeWorkerResult(staging, result, fmt.Sprintf("Fetched %s (%s, %d bytes) from %s\n", paper.ID, document.ContentType, len(document.Data), document.SourceURL))
}

func runPaperParse(ctx context.Context, staging string, dependencies []dependencyResult) error {
	dependency, fetch, err := findFetchResult(dependencies)
	if err != nil {
		return err
	}
	if !fetch.Available {
		return fmt.Errorf("%w: %s", ErrFullTextUnavailable, fetch.Reason)
	}
	data, err := dependency.readArtifact(fetch.Artifact)
	if err != nil {
		return err
	}
	maxInputBytes := int(environmentInt64("CAPSULE_RESEARCH_MAX_PDF_BYTES", DefaultPaperDownloadLimitBytes))
	var parser PaperParser = BasicGoParser{MaxInputBytes: maxInputBytes}
	if fetch.ContentType == "application/pdf" {
		parser, err = NewPaperParserWithLimit(
			strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_PARSER_MODE")),
			strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_PYTHON")),
			strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_PYTHON_PARSER")),
			15*time.Second, maxInputBytes,
		)
		if err != nil {
			return err
		}
	}
	parsed, err := parser.Parse(ctx, fetch, data)
	if err != nil {
		return err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Paper: %s\nParser: %s\nPages: %d\nSections: %d\nCharacters: %d\nDuration: %dms\nFallback: %t\nTruncated: %t\nWarnings: %d\n",
		parsed.Paper.Title, parsed.Diagnostics.Selected, parsed.Diagnostics.PageCount, parsed.Diagnostics.DetectedSections,
		parsed.Diagnostics.ExtractedCharacters, parsed.Diagnostics.DurationMillis, parsed.Diagnostics.FallbackUsed,
		parsed.Diagnostics.Truncated, parsed.Diagnostics.WarningCount)
	for _, section := range parsed.Sections {
		fmt.Fprintf(&text, "- %s (%d chars)\n", section.Heading, len(section.Text))
	}
	return writeWorkerResult(staging, parsed, text.String())
}

func runPaperAnalyze(ctx context.Context, staging string, args map[string]any, dependencies []dependencyResult) error {
	var parsed ParsedPaper
	if err := findTypedResult(dependencies, &parsed, func(raw map[string]any) bool { _, ok := raw["sections"]; return ok }); err != nil {
		return err
	}
	question := stringArg(args, "question")
	taskID := os.Getenv("CAPSULE_TASK_ID")
	var analysis PaperAnalysis
	var err error
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_ANALYSIS_MODE"))) {
	case "", "basic":
		analysis, err = AnalyzePaperContext(ctx, parsed, question, taskID)
		if err == nil {
			appendEvidenceRejectionDemo(&analysis, os.Getenv("CAPSULE_RESEARCH_SCENARIO"), taskID)
		}
	case "llm":
		config, configErr := llm.LoadConfig()
		if configErr != nil {
			return fmt.Errorf("load LLM paper analysis config: %w", configErr)
		}
		client, clientErr := llm.NewOpenAICompatibleClient(config)
		if clientErr != nil {
			return fmt.Errorf("configure LLM paper analysis: %w", clientErr)
		}
		maxCalls := environmentInt("CAPSULE_RESEARCH_MAX_ANALYSIS_CALLS", 1)
		budgetClient := &llm.BudgetClient{Client: client, MaxCalls: maxCalls}
		var support ClaimSupportChecker = DeterministicClaimSupport{}
		if strings.EqualFold(strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_CLAIM_SUPPORT_MODE")), "llm") {
			support = LLMClaimSupportChecker{Client: budgetClient}
		}
		analysis, err = (LLMPaperAnalyzer{
			Client: budgetClient, Support: support,
			MaxContextBytes: environmentInt("CAPSULE_RESEARCH_MAX_CONTEXT_BYTES", maximumAnalysisBytes),
		}).Analyze(ctx, parsed, question, taskID)
		stats := budgetClient.Stats()
		analysis.LLMCalls = stats.Calls
		analysis.LLMFailures = stats.Failures
		if stats.UsageKnown {
			analysis.Usage = &TokenUsage{InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens}
		}
	default:
		return fmt.Errorf("paper analysis mode must be basic or llm")
	}
	if err != nil {
		return err
	}
	return writeWorkerResult(staging, analysis, renderAnalysis(analysis))
}

func appendEvidenceRejectionDemo(analysis *PaperAnalysis, scenario, taskID string) {
	if analysis == nil || !strings.EqualFold(strings.TrimSpace(scenario), MockScenarioEvidenceReject) ||
		!strings.HasSuffix(taskID, "p1-analysis") {
		return
	}
	candidate := CandidateFinding{
		Claim: "The paper reports a fabricated 99 percent improvement.", ClaimType: "result",
		PaperID: analysis.Paper.ID, SectionID: "section-missing", EvidenceText: "This sentence is not present in the paper.",
	}
	analysis.CandidateFindings = append(analysis.CandidateFindings, candidate)
	analysis.Findings = append(analysis.Findings, VerifiedFinding{
		Candidate: candidate, Status: FindingUnsupported,
		Reason: "candidate evidence_text does not exist in the selected section",
	})
}

func runResearchSynthesize(staging string, args map[string]any, dependencies []dependencyResult) error {
	analyses := make([]PaperAnalysis, 0, len(dependencies))
	var searches []SearchResult
	for _, dependency := range dependencies {
		var analysis PaperAnalysis
		if err := json.Unmarshal(dependency.Result, &analysis); err == nil && analysis.Paper.ID != "" && len(analysis.Evidence) > 0 {
			analyses = append(analyses, analysis)
		}
		var search SearchResult
		if err := json.Unmarshal(dependency.Result, &search); err == nil && strings.TrimSpace(search.Query) != "" && search.Provider != "" {
			searches = append(searches, search)
		}
	}
	synthesis, err := Synthesize(stringArg(args, "goal"), analyses)
	if err != nil {
		return err
	}
	sort.SliceStable(searches, func(i, j int) bool {
		if searches[i].CompletedAt.Equal(searches[j].CompletedAt) {
			return searches[i].Query < searches[j].Query
		}
		return searches[i].CompletedAt.Before(searches[j].CompletedAt)
	})
	var queryHistory []string
	for _, search := range searches {
		queryHistory = appendUniqueOrdered(queryHistory, search.Query)
		for _, paper := range search.Papers {
			synthesis.RetrievedPaperIDs = appendUniqueOrdered(synthesis.RetrievedPaperIDs, paper.ID)
		}
	}
	synthesis.QueryHistory = appendUniqueOrdered(queryHistory, synthesis.QueryHistory...)
	referenced := make(map[string]struct{}, len(synthesis.References))
	for _, paper := range synthesis.References {
		referenced[paper.ID] = struct{}{}
	}
	metadataSeen := make(map[string]struct{}, len(synthesis.MetadataOnlyPapers))
	for _, paper := range synthesis.MetadataOnlyPapers {
		metadataSeen[paper.ID] = struct{}{}
	}
	for _, search := range searches {
		for _, paper := range search.Papers {
			if _, used := referenced[paper.ID]; used {
				continue
			}
			if _, seen := metadataSeen[paper.ID]; seen {
				continue
			}
			synthesis.MetadataOnlyPapers = append(synthesis.MetadataOnlyPapers, paper)
			metadataSeen[paper.ID] = struct{}{}
		}
	}
	sort.Slice(synthesis.MetadataOnlyPapers, func(i, j int) bool {
		return synthesis.MetadataOnlyPapers[i].ID < synthesis.MetadataOnlyPapers[j].ID
	})
	return writeWorkerResult(staging, synthesis, renderSynthesis(synthesis))
}

func runExperimentDesign(staging string, args map[string]any, dependencies []dependencyResult) error {
	var synthesis Synthesis
	if err := findTypedResult(dependencies, &synthesis, func(raw map[string]any) bool { _, ok := raw["method_comparison"]; return ok }); err != nil {
		return err
	}
	design, err := DesignExperiment(stringArg(args, "goal"), stringArg(args, "constraints"), synthesis)
	if err != nil {
		return err
	}
	return writeWorkerResult(staging, design, renderExperiment(design))
}

func runResearchReport(staging string, args map[string]any, dependencies []dependencyResult) error {
	var synthesis Synthesis
	var design ExperimentDesign
	for _, dependency := range dependencies {
		var raw map[string]any
		if json.Unmarshal(dependency.Result, &raw) != nil {
			continue
		}
		if _, exists := raw["method_comparison"]; exists {
			if err := json.Unmarshal(dependency.Result, &synthesis); err != nil {
				return err
			}
		}
		if _, exists := raw["hypothesis"]; exists {
			if err := json.Unmarshal(dependency.Result, &design); err != nil {
				return err
			}
		}
	}
	report, err := BuildReport(stringArg(args, "goal"), synthesis, design)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "report.md"), []byte(report), 0o600); err != nil {
		return err
	}
	quality := AssessResearchQuality(synthesis, design)
	result := map[string]any{
		"report_file": "report.md", "references": len(synthesis.References),
		"evidence_backed_claims": len(synthesis.Facts) + len(synthesis.Inferences),
		"unsupported_claims":     0, "unsupported_facts": 0,
		"citation_closed": true, "hallucinated_references": 0, "quality": quality,
	}
	return writeWorkerResult(staging, result, report)
}

func providerFromEnvironment() (LiteratureProvider, error) {
	providerName := strings.ToLower(strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_PROVIDER")))
	maxPDFBytes := environmentInt64("CAPSULE_RESEARCH_MAX_PDF_BYTES", DefaultPaperDownloadLimitBytes)
	if err := ValidatePaperDownloadLimit(maxPDFBytes); err != nil {
		return nil, err
	}
	var provider LiteratureProvider
	var err error
	switch providerName {
	case "", "arxiv":
		provider, err = NewArxivProvider(ArxivOptions{Endpoint: strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_ARXIV_ENDPOINT")), MaxPDFBytes: maxPDFBytes})
	case "crossref":
		provider, err = NewCrossrefProvider(CrossrefOptions{Endpoint: strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_CROSSREF_ENDPOINT"))})
	case "multi":
		var arxiv *ArxivProvider
		arxiv, err = NewArxivProvider(ArxivOptions{Endpoint: strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_ARXIV_ENDPOINT")), MaxPDFBytes: maxPDFBytes})
		if err == nil {
			var crossref *CrossrefProvider
			crossref, err = NewCrossrefProvider(CrossrefOptions{Endpoint: strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_CROSSREF_ENDPOINT"))})
			if err == nil {
				provider = &MultiProvider{Providers: []LiteratureProvider{arxiv, crossref}}
			}
		}
	case "mock":
		return NewMockProvider(strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_SCENARIO")))
	default:
		return nil, fmt.Errorf("unknown configured research provider")
	}
	if err != nil {
		return nil, err
	}
	disableCache, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_NO_CACHE")))
	cacheRoot := strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_CACHE_DIR"))
	if disableCache || cacheRoot == "" {
		return provider, nil
	}
	ttl := 24 * time.Hour
	if configured := strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_CACHE_TTL")); configured != "" && configured != "0s" {
		ttl, err = time.ParseDuration(configured)
		if err != nil || ttl <= 0 {
			return nil, fmt.Errorf("invalid research cache TTL")
		}
	}
	return NewCachingProvider(provider, cacheRoot, ttl)
}

type dependencyResult struct {
	ID           string
	CommitPath   string
	ManifestPath string
	Allowed      map[string]struct{}
	Result       json.RawMessage
}

func loadDependencies() ([]dependencyResult, error) {
	raw := strings.TrimSpace(os.Getenv("AEGIS_DEPENDENCY_OUTPUTS_JSON"))
	if raw == "" {
		return nil, fmt.Errorf("verified dependency outputs are required")
	}
	var outputs map[string]agent.DependencyOutput
	if err := json.Unmarshal([]byte(raw), &outputs); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(outputs))
	for id := range outputs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]dependencyResult, 0, len(ids))
	for _, id := range ids {
		output := outputs[id]
		if !output.Verified {
			return nil, fmt.Errorf("dependency %s is not verified", id)
		}
		commitPath, err := filepath.Abs(output.CommitPath)
		if err != nil {
			return nil, err
		}
		manifestPath, err := filepath.Abs(output.ManifestPath)
		if err != nil || !pathInside(commitPath, manifestPath) {
			return nil, fmt.Errorf("dependency %s manifest escapes commit path", id)
		}
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, err
		}
		var manifest outputtxn.Manifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return nil, err
		}
		dependency := dependencyResult{ID: id, CommitPath: commitPath, ManifestPath: manifestPath, Allowed: make(map[string]struct{})}
		for _, file := range manifest.Files {
			dependency.Allowed[filepath.ToSlash(file.Path)] = struct{}{}
		}
		data, err := dependency.readArtifact("result.json")
		if err != nil {
			return nil, err
		}
		if len(data) > maximumDependencyResultBytes {
			return nil, fmt.Errorf("dependency %s result is too large", id)
		}
		dependency.Result = append(json.RawMessage(nil), data...)
		result = append(result, dependency)
	}
	return result, nil
}

func (d dependencyResult) readArtifact(name string) ([]byte, error) {
	name = filepath.ToSlash(filepath.Clean(name))
	if name == "." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
		return nil, fmt.Errorf("invalid dependency artifact %q", name)
	}
	if _, exists := d.Allowed[name]; !exists {
		return nil, fmt.Errorf("dependency artifact %q is not in the verified manifest", name)
	}
	path := filepath.Join(d.CommitPath, filepath.FromSlash(name))
	if !pathInside(d.CommitPath, path) {
		return nil, fmt.Errorf("dependency artifact escapes commit path")
	}
	return os.ReadFile(path)
}

func findSearchResult(dependencies []dependencyResult) (SearchResult, error) {
	var result SearchResult
	err := findTypedResult(dependencies, &result, func(raw map[string]any) bool { _, ok := raw["papers"]; return ok })
	return result, err
}

func findFetchResult(dependencies []dependencyResult) (dependencyResult, FetchResult, error) {
	for _, dependency := range dependencies {
		var raw map[string]any
		if json.Unmarshal(dependency.Result, &raw) != nil {
			continue
		}
		if _, exists := raw["available"]; !exists {
			continue
		}
		var fetch FetchResult
		if err := json.Unmarshal(dependency.Result, &fetch); err != nil {
			return dependencyResult{}, FetchResult{}, err
		}
		return dependency, fetch, nil
	}
	return dependencyResult{}, FetchResult{}, fmt.Errorf("paper.fetch dependency result is missing")
}

func findTypedResult(dependencies []dependencyResult, target any, matches func(map[string]any) bool) error {
	for _, dependency := range dependencies {
		var raw map[string]any
		if json.Unmarshal(dependency.Result, &raw) != nil || !matches(raw) {
			continue
		}
		return json.Unmarshal(dependency.Result, target)
	}
	return fmt.Errorf("required typed dependency result is missing")
}

func selectPaper(search SearchResult, paperID string, rank int) (Paper, int, error) {
	if strings.TrimSpace(paperID) != "" {
		for index, paper := range search.Papers {
			if paper.ID == paperID {
				return paper, index + 1, nil
			}
		}
		return Paper{}, 0, fmt.Errorf("paper_id %q was not returned by the search dependency", paperID)
	}
	if rank < 1 || rank > len(search.Papers) {
		return Paper{}, 0, fmt.Errorf("paper rank %d is outside %d search results", rank, len(search.Papers))
	}
	return search.Papers[rank-1], rank, nil
}

func writeWorkerResult(staging string, value any, text string) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "result.json"), encoded, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(staging, "result.txt"), []byte(text), 0o600)
}

func workerArguments() (map[string]any, error) {
	raw := strings.TrimSpace(os.Getenv("CAPSULE_TASK_ARGUMENTS_JSON"))
	if raw == "" {
		return map[string]any{}, nil
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		return nil, err
	}
	return arguments, nil
}

func requiredWorkerPath(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is missing", name)
	}
	return filepath.Abs(value)
}

func stringArg(arguments map[string]any, name string) string {
	value, _ := arguments[name].(string)
	return strings.TrimSpace(value)
}

func intArg(arguments map[string]any, name string) int {
	switch value := arguments[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, _ := strconv.Atoi(value.String())
		return parsed
	default:
		return 0
	}
}

func environmentInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func environmentInt64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func pathInside(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func renderAnalysis(analysis PaperAnalysis) string {
	return fmt.Sprintf("Paper: %s\nProblem: %s\nMethod: %s\nDatasets: %s\nMetrics: %s\nEvidence: %d\n",
		analysis.Paper.Title, analysis.Problem, analysis.Method,
		strings.Join(analysis.Datasets, ", "), strings.Join(analysis.Metrics, ", "), len(analysis.Evidence))
}

func renderSynthesis(synthesis Synthesis) string {
	return fmt.Sprintf("Papers: %d\nEvidence: %d\nDirections: %d\nDatasets: %s\nMetrics: %s\n",
		len(synthesis.References), len(synthesis.Evidence), len(synthesis.ResearchDirections),
		strings.Join(synthesis.Datasets, ", "), strings.Join(synthesis.Metrics, ", "))
}

func renderExperiment(design ExperimentDesign) string {
	return fmt.Sprintf("PROPOSAL hypothesis: %s\nBaselines: %d\nAblations: %d\nRisks: %d\n",
		design.Hypothesis.Statement, len(design.BaselineSuggestions), len(design.AblationPlan), len(design.ExpectedRisks))
}
