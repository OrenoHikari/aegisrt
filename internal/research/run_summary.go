package research

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aegisrt/internal/orchestrator"
)

const MaximumAnalysisContextBytes = 60 * 1024

type ResearchBudget struct {
	MaxPapers                int           `json:"max_papers"`
	MaxLLMCalls              int           `json:"max_llm_calls"`
	MaxAnalysisCallsPerPaper int           `json:"max_analysis_calls_per_paper"`
	MaxReplans               int           `json:"max_replans"`
	MaxRuntime               time.Duration `json:"-"`
	MaxContextBytes          int           `json:"max_context_bytes"`
	MaxPDFBytes              int64         `json:"max_pdf_bytes"`
}

func (b ResearchBudget) Validate() error {
	if b.MaxPapers < 1 || b.MaxPapers > MaximumSearchResults {
		return fmt.Errorf("max-papers must be between 1 and %d", MaximumSearchResults)
	}
	if b.MaxAnalysisCallsPerPaper < 1 || b.MaxAnalysisCallsPerPaper > maximumFindingsPerPaper+1 {
		return fmt.Errorf("max-analysis-calls-per-paper must be between 1 and %d", maximumFindingsPerPaper+1)
	}
	if b.MaxLLMCalls-b.MaxPapers*b.MaxAnalysisCallsPerPaper < 2 {
		return fmt.Errorf("max-llm-calls must reserve at least two calls beyond the per-paper analysis budget")
	}
	if b.MaxReplans < 0 {
		return fmt.Errorf("max-replans cannot be negative")
	}
	if b.MaxRuntime <= 0 {
		return fmt.Errorf("maximum runtime must be greater than zero")
	}
	if b.MaxContextBytes < 1024 || b.MaxContextBytes > MaximumAnalysisContextBytes {
		return fmt.Errorf("max-context-bytes must be between 1024 and %d", MaximumAnalysisContextBytes)
	}
	if b.MaxPDFBytes != 0 {
		if err := ValidatePaperDownloadLimit(b.MaxPDFBytes); err != nil {
			return err
		}
	}
	return nil
}

type FailureCategory string

const (
	FailureSearch           FailureCategory = "SEARCH_FAILURE"
	FailurePDFUnavailable   FailureCategory = "PDF_UNAVAILABLE"
	FailurePDFLimit         FailureCategory = "PDF_LIMIT_EXCEEDED"
	FailurePDFParse         FailureCategory = "PDF_PARSE_FAILURE"
	FailureSection          FailureCategory = "SECTION_FAILURE"
	FailureLLMAnalysis      FailureCategory = "LLM_ANALYSIS_FAILURE"
	FailureEvidenceReject   FailureCategory = "EVIDENCE_REJECTED"
	FailureClaimUnsupported FailureCategory = "CLAIM_UNSUPPORTED"
	FailureCitationReject   FailureCategory = "CITATION_REJECTED"
	FailureReplanExhausted  FailureCategory = "REPLAN_EXHAUSTED"
	FailureProviderRate     FailureCategory = "PROVIDER_RATE_LIMIT"
	FailureContextLimit     FailureCategory = "CONTEXT_LIMIT"
	FailureUnknown          FailureCategory = "UNKNOWN"
)

type FailureCase struct {
	RunID      string          `json:"run_id"`
	Iteration  int             `json:"iteration"`
	TaskID     string          `json:"task_id,omitempty"`
	Capability string          `json:"capability,omitempty"`
	Category   FailureCategory `json:"category"`
	Reason     string          `json:"reason"`
	Recovered  bool            `json:"recovered"`
}

type PlanningSummary struct {
	PlansGenerated int `json:"plans_generated"`
	Replans        int `json:"replans"`
}

type SearchSummary struct {
	Queries            int `json:"queries"`
	PapersRetrieved    int `json:"papers_retrieved"`
	PapersDeduplicated int `json:"papers_deduplicated"`
}

type PaperRunSummary struct {
	PDFsRequested      int                 `json:"pdfs_requested"`
	PDFsAvailable      int                 `json:"pdfs_available"`
	ParsedSuccessfully int                 `json:"parsed_successfully"`
	ParseFailures      int                 `json:"parse_failures"`
	Diagnostics        []ParserDiagnostics `json:"diagnostics,omitempty"`
}

type LLMRunSummary struct {
	Calls        int  `json:"calls"`
	Failures     int  `json:"failures"`
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

type EvidenceRunSummary struct {
	Candidates     int `json:"candidates"`
	SourceVerified int `json:"source_verified"`
	Supported      int `json:"supported"`
	Rejected       int `json:"rejected"`
}

type ReportRunSummary struct {
	Facts                   int             `json:"facts"`
	Inferences              int             `json:"inferences"`
	Proposals               int             `json:"proposals"`
	References              int             `json:"references"`
	RetrievedReferences     int             `json:"retrieved_references"`
	ValidMetadataReferences int             `json:"valid_metadata_references"`
	HallucinatedReferences  int             `json:"hallucinated_references"`
	DuplicatedReferences    int             `json:"duplicated_references"`
	CitationClosure         bool            `json:"citation_closure"`
	Quality                 ResearchQuality `json:"quality"`
}

type RunSummary struct {
	RunID        string             `json:"run_id"`
	Goal         string             `json:"goal"`
	Mode         string             `json:"mode"`
	Budget       ResearchBudgetJSON `json:"budget"`
	Planning     PlanningSummary    `json:"planning"`
	Search       SearchSummary      `json:"search"`
	Paper        PaperRunSummary    `json:"paper"`
	LLM          LLMRunSummary      `json:"llm"`
	Evidence     EvidenceRunSummary `json:"evidence"`
	Report       ReportRunSummary   `json:"report"`
	Duration     string             `json:"duration"`
	DurationMS   int64              `json:"duration_ms"`
	FailureCount int                `json:"failure_count"`
}

type ResearchBudgetJSON struct {
	MaxPapers                int    `json:"max_papers"`
	MaxLLMCalls              int    `json:"max_llm_calls"`
	MaxAnalysisCallsPerPaper int    `json:"max_analysis_calls_per_paper"`
	MaxReplans               int    `json:"max_replans"`
	MaxRuntime               string `json:"max_runtime"`
	MaxContextBytes          int    `json:"max_context_bytes"`
	MaxPDFBytes              int64  `json:"max_pdf_bytes"`
}

func BuildRunSummary(result orchestrator.LoopResult, runErr error, duration time.Duration, budget ResearchBudget, mode string) (RunSummary, []FailureCase) {
	maxPDFBytes := budget.MaxPDFBytes
	if maxPDFBytes <= 0 {
		maxPDFBytes = DefaultPaperDownloadLimitBytes
	}
	summary := RunSummary{
		RunID: result.RunID, Goal: result.Goal, Mode: mode,
		Budget: ResearchBudgetJSON{
			MaxPapers: budget.MaxPapers, MaxLLMCalls: budget.MaxLLMCalls,
			MaxAnalysisCallsPerPaper: budget.MaxAnalysisCallsPerPaper,
			MaxReplans:               budget.MaxReplans, MaxRuntime: budget.MaxRuntime.String(), MaxContextBytes: budget.MaxContextBytes,
			MaxPDFBytes: maxPDFBytes,
		},
		Planning: PlanningSummary{PlansGenerated: len(result.Iterations), Replans: result.Replans},
		Duration: duration.String(), DurationMS: duration.Milliseconds(),
	}
	queries := make(map[string]struct{})
	papers := make(map[string]struct{})
	deduplicated := make(map[string]struct{})
	seenCapabilities := make(map[string]struct{})
	failureSeen := make(map[string]struct{})
	referenceIDs := make(map[string]struct{})
	var failures []FailureCase
	usageKnown, inputTokens, outputTokens := false, 0, 0
	for _, iteration := range result.Iterations {
		for _, observation := range iteration.Observations {
			key := observation.TaskID + "\x00" + observation.Capability
			first := false
			if _, exists := seenCapabilities[key]; !exists {
				seenCapabilities[key] = struct{}{}
				first = true
			}
			switch observation.Capability {
			case "literature.search":
				if query, ok := observation.Output["query"].(string); ok {
					queries[strings.ToLower(strings.TrimSpace(query))] = struct{}{}
				}
				for _, raw := range jsonArray(observation.Output["papers"]) {
					paper, _ := raw.(map[string]any)
					id, _ := paper["id"].(string)
					if id != "" {
						papers[id] = struct{}{}
						if len(jsonArray(paper["metadata_sources"])) > 1 {
							deduplicated[id] = struct{}{}
						}
					}
				}
			case "paper.fetch":
				if first {
					summary.Paper.PDFsRequested++
					if available, _ := observation.Output["available"].(bool); available {
						summary.Paper.PDFsAvailable++
					}
				}
			case "paper.parse":
				if first {
					if observation.Success {
						summary.Paper.ParsedSuccessfully++
						if raw, ok := observation.Output["diagnostics"].(map[string]any); ok {
							encoded, _ := json.Marshal(raw)
							var diagnostics ParserDiagnostics
							if json.Unmarshal(encoded, &diagnostics) == nil {
								summary.Paper.Diagnostics = append(summary.Paper.Diagnostics, diagnostics)
							}
						}
					} else {
						summary.Paper.ParseFailures++
					}
				}
			case "paper.analyze":
				if first {
					if !observation.Success && (strings.Contains(observation.Error, "LLM paper analysis") || strings.Contains(observation.Error, "structured paper analysis")) {
						// These errors occur only after the bounded main analysis call was
						// dispatched. Token usage remains unavailable because failed output
						// transactions are intentionally discarded by the Runtime.
						summary.LLM.Calls++
						summary.LLM.Failures++
					}
					summary.LLM.Calls += jsonInt(observation.Output["llm_calls"])
					summary.LLM.Failures += jsonInt(observation.Output["llm_failures"])
					summary.Evidence.Candidates += len(jsonArray(observation.Output["candidate_findings"]))
					for _, raw := range jsonArray(observation.Output["findings"]) {
						finding, _ := raw.(map[string]any)
						if evidenceID, _ := finding["evidence_id"].(string); evidenceID != "" {
							summary.Evidence.SourceVerified++
						}
						switch finding["status"] {
						case string(FindingSupported):
							summary.Evidence.Supported++
						case string(FindingUnsupported):
							summary.Evidence.Rejected++
						}
					}
					if usage, ok := observation.Output["usage"].(map[string]any); ok {
						inputTokens += jsonInt(usage["input_tokens"])
						outputTokens += jsonInt(usage["output_tokens"])
						usageKnown = true
					}
				}
			case "research.synthesize":
				if first {
					summary.Report.Facts = len(jsonArray(observation.Output["facts"]))
					summary.Report.Inferences = len(jsonArray(observation.Output["inferences"]))
					for _, raw := range jsonArray(observation.Output["references"]) {
						reference, _ := raw.(map[string]any)
						id, _ := reference["id"].(string)
						if id == "" {
							continue
						}
						if _, exists := referenceIDs[id]; exists {
							summary.Report.DuplicatedReferences++
						}
						referenceIDs[id] = struct{}{}
					}
				}
			case "experiment.design":
				if first {
					summary.Report.Proposals = countProposalOutput(observation.Output)
				}
			case "research.report":
				if first {
					summary.Report.References = jsonInt(observation.Output["references"])
					summary.Report.ValidMetadataReferences = summary.Report.References
					summary.Report.HallucinatedReferences = jsonInt(observation.Output["hallucinated_references"])
					summary.Report.CitationClosure, _ = observation.Output["citation_closed"].(bool)
					if raw, ok := observation.Output["quality"].(map[string]any); ok {
						encoded, _ := json.Marshal(raw)
						_ = json.Unmarshal(encoded, &summary.Report.Quality)
					}
				}
			}
			for _, failure := range failuresFromObservation(observation, runErr == nil && result.Replans > 0) {
				failureKey := failure.TaskID + "\x00" + string(failure.Category) + "\x00" + failure.Reason
				if _, exists := failureSeen[failureKey]; !exists {
					failureSeen[failureKey] = struct{}{}
					failures = append(failures, failure)
				}
			}
		}
	}
	summary.Search.Queries = len(queries)
	summary.Search.PapersRetrieved = len(papers)
	summary.Search.PapersDeduplicated = len(deduplicated)
	summary.Report.RetrievedReferences = len(papers)
	if usageKnown {
		summary.LLM.InputTokens = &inputTokens
		summary.LLM.OutputTokens = &outputTokens
	}
	if runErr != nil {
		category := ClassifyFailure("", runErr.Error(), nil)
		failure := FailureCase{RunID: result.RunID, Iteration: len(result.Iterations), Category: category, Reason: boundedFailureReason(runErr.Error())}
		key := "run\x00" + string(category) + "\x00" + failure.Reason
		if _, exists := failureSeen[key]; !exists {
			failures = append(failures, failure)
		}
	}
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Iteration != failures[j].Iteration {
			return failures[i].Iteration < failures[j].Iteration
		}
		if failures[i].TaskID != failures[j].TaskID {
			return failures[i].TaskID < failures[j].TaskID
		}
		return failures[i].Category < failures[j].Category
	})
	summary.FailureCount = len(failures)
	return summary, failures
}

func failuresFromObservation(observation orchestrator.Observation, recovered bool) []FailureCase {
	base := FailureCase{
		RunID: observation.RunID, Iteration: observation.Iteration, TaskID: observation.TaskID,
		Capability: observation.Capability, Recovered: recovered,
	}
	var result []FailureCase
	add := func(category FailureCategory, reason string) {
		failure := base
		failure.Category = category
		failure.Reason = boundedFailureReason(reason)
		result = append(result, failure)
	}
	if !observation.Success {
		add(ClassifyFailure(observation.Capability, observation.Error, observation.Output), observation.Error)
		return result
	}
	switch observation.Capability {
	case "paper.fetch":
		if available, _ := observation.Output["available"].(bool); !available {
			reason, _ := observation.Output["reason"].(string)
			category := FailurePDFUnavailable
			if code, _ := observation.Output["failure_code"].(string); code == "PDF_LIMIT_EXCEEDED" {
				category = FailurePDFLimit
			}
			add(category, reason)
		}
	case "paper.parse":
		if len(jsonArray(observation.Output["sections"])) == 0 {
			add(FailureSection, "parser produced no detected sections")
		}
	case "paper.analyze":
		for _, raw := range jsonArray(observation.Output["findings"]) {
			finding, _ := raw.(map[string]any)
			if finding["status"] != string(FindingUnsupported) {
				continue
			}
			reason, _ := finding["reason"].(string)
			if evidenceID, _ := finding["evidence_id"].(string); evidenceID == "" {
				add(FailureEvidenceReject, reason)
			} else {
				add(FailureClaimUnsupported, reason)
			}
		}
	case "research.report":
		closed, _ := observation.Output["citation_closed"].(bool)
		if !closed || jsonInt(observation.Output["hallucinated_references"]) > 0 {
			add(FailureCitationReject, "report citation closure was not established")
		}
	}
	return result
}

func ClassifyFailure(capability, reason string, output map[string]any) FailureCategory {
	lower := strings.ToLower(reason)
	if strings.Contains(lower, "429") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") {
		return FailureProviderRate
	}
	if (strings.Contains(lower, "maximum") && strings.Contains(lower, "replan")) || strings.Contains(lower, "replan exhausted") {
		return FailureReplanExhausted
	}
	if (strings.Contains(lower, "context") && (strings.Contains(lower, "limit") || strings.Contains(lower, "deadline"))) || strings.Contains(lower, "too large") || strings.Contains(lower, "call budget") {
		return FailureContextLimit
	}
	switch capability {
	case "literature.search":
		return FailureSearch
	case "paper.fetch":
		if code, _ := output["failure_code"].(string); code == "PDF_LIMIT_EXCEEDED" {
			return FailurePDFLimit
		}
		if available, _ := output["available"].(bool); !available {
			return FailurePDFUnavailable
		}
		return FailureSearch
	case "paper.parse":
		return FailurePDFParse
	case "paper.analyze":
		return FailureLLMAnalysis
	case "research.report":
		return FailureCitationReject
	}
	return FailureUnknown
}

func countProposalOutput(output map[string]any) int {
	count := 0
	if hypothesis, ok := output["hypothesis"].(map[string]any); ok && hypothesis["kind"] == string(FindingProposal) {
		count++
	}
	for _, key := range []string{"baseline_suggestions", "datasets", "metrics", "ablation_plan", "evaluation_protocol", "expected_risks"} {
		count += len(jsonArray(output[key]))
	}
	return count
}

func jsonArray(value any) []any {
	items, _ := value.([]any)
	return items
}

func boundedFailureReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "unspecified failure"
	}
	if len(value) > 512 {
		value = truncateUTF8Bytes(value, 512)
	}
	return value
}

func WriteRunArtifacts(root string, summary RunSummary, failures []FailureCase) (string, string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", "", err
	}
	summaryData, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return "", "", err
	}
	failureData, err := json.MarshalIndent(failures, "", "  ")
	if err != nil {
		return "", "", err
	}
	summaryPath := filepath.Join(root, "run-summary.json")
	failurePath := filepath.Join(root, "failure-cases.json")
	if err := writeAtomicFile(summaryPath, append(summaryData, '\n')); err != nil {
		return "", "", err
	}
	if err := writeAtomicFile(failurePath, append(failureData, '\n')); err != nil {
		return "", "", err
	}
	return summaryPath, failurePath, nil
}
