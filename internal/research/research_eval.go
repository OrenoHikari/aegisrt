package research

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maximumEvalCorpusBytes = 1024 * 1024

type EvalCorpus struct {
	Version int        `json:"version"`
	Name    string     `json:"name"`
	Mode    string     `json:"mode"`
	Tasks   []EvalTask `json:"tasks"`
}

type EvalTask struct {
	ID             string   `json:"id"`
	Category       string   `json:"category"`
	Scenario       string   `json:"scenario"`
	Query          string   `json:"query"`
	RecoveryQuery  string   `json:"recovery_query,omitempty"`
	PaperRanks     []int    `json:"paper_ranks"`
	GoldClaimTypes []string `json:"gold_claim_types"`
	Injection      string   `json:"injection,omitempty"`
}

type EvalTaskResult struct {
	ID                     string   `json:"id"`
	Category               string   `json:"category"`
	Passed                 bool     `json:"passed"`
	Reason                 string   `json:"reason,omitempty"`
	RetrievedPapers        int      `json:"retrieved_papers"`
	UsablePapers           int      `json:"usable_papers"`
	AnalyzedPapers         int      `json:"analyzed_papers"`
	CandidateFindings      int      `json:"candidate_findings"`
	VerifiedFindings       int      `json:"verified_findings"`
	SupportedFindings      int      `json:"supported_findings"`
	UnsupportedFindings    int      `json:"unsupported_findings"`
	Facts                  int      `json:"facts"`
	FactsWithEvidence      int      `json:"facts_with_evidence"`
	Replans                int      `json:"replans"`
	RecoveryAttempted      bool     `json:"recovery_attempted"`
	RecoverySucceeded      bool     `json:"recovery_succeeded"`
	CitationChecks         int      `json:"citation_checks"`
	CitationClosed         int      `json:"citation_closed"`
	HallucinatedReferences int      `json:"hallucinated_references"`
	GoldClaimTypes         []string `json:"gold_claim_types"`
	PredictedClaimTypes    []string `json:"predicted_claim_types"`
	TruePositiveTypes      int      `json:"true_positive_types"`
}

type EvalMetrics struct {
	TotalTasks                 int     `json:"total_tasks"`
	PassedTasks                int     `json:"passed_tasks"`
	FailedTasks                int     `json:"failed_tasks"`
	TaskSuccessRate            float64 `json:"task_success_rate"`
	ReplanCount                int     `json:"replan_count"`
	RecoveryAttempts           int     `json:"recovery_attempts"`
	RecoverySuccesses          int     `json:"recovery_successes"`
	RecoveryRate               float64 `json:"recovery_rate"`
	Duration                   string  `json:"duration"`
	DurationMillis             int64   `json:"duration_ms"`
	RetrievedPapers            int     `json:"retrieved_papers"`
	UsablePapers               int     `json:"usable_papers"`
	AnalyzedPapers             int     `json:"analyzed_papers"`
	CandidateFindings          int     `json:"candidate_findings"`
	VerifiedFindings           int     `json:"verified_findings"`
	SupportedFindings          int     `json:"supported_findings"`
	UnsupportedFindings        int     `json:"unsupported_findings"`
	EvidenceVerificationRate   float64 `json:"evidence_verification_rate"`
	CitationClosureRate        float64 `json:"citation_closure_rate"`
	HallucinatedReferenceCount int     `json:"hallucinated_reference_count"`
	FactWithEvidenceRatio      float64 `json:"fact_with_evidence_ratio"`
	UnsupportedFactCount       int     `json:"unsupported_fact_count"`
	InvalidPlanCount           int     `json:"invalid_plan_count"`
	RepeatedPlanCount          int     `json:"repeated_plan_count"`
	MaxLoopAbortCount          int     `json:"max_loop_abort_count"`
	ClaimTypePrecision         float64 `json:"claim_type_precision"`
	ClaimTypeRecall            float64 `json:"claim_type_recall"`
	InputTokens                *int    `json:"input_tokens"`
	OutputTokens               *int    `json:"output_tokens"`
}

type EvalReport struct {
	Name        string           `json:"name"`
	Mode        string           `json:"mode"`
	GeneratedAt time.Time        `json:"generated_at"`
	Metrics     EvalMetrics      `json:"metrics"`
	Tasks       []EvalTaskResult `json:"tasks"`
}

func LoadEvalCorpus(path string) (EvalCorpus, error) {
	file, err := os.Open(path)
	if err != nil {
		return EvalCorpus{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumEvalCorpusBytes+1))
	if err != nil {
		return EvalCorpus{}, err
	}
	if len(data) > maximumEvalCorpusBytes {
		return EvalCorpus{}, fmt.Errorf("research eval corpus exceeds %d bytes", maximumEvalCorpusBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var corpus EvalCorpus
	if err := decoder.Decode(&corpus); err != nil {
		return EvalCorpus{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return EvalCorpus{}, fmt.Errorf("research eval corpus has trailing JSON")
	}
	if corpus.Version != 1 || corpus.Mode != "fixture" || len(corpus.Tasks) < 10 {
		return EvalCorpus{}, fmt.Errorf("research eval corpus must be fixture version 1 with at least 10 tasks")
	}
	seen := make(map[string]struct{}, len(corpus.Tasks))
	for _, task := range corpus.Tasks {
		if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Query) == "" || len(task.PaperRanks) == 0 {
			return EvalCorpus{}, fmt.Errorf("research eval task is incomplete")
		}
		if _, exists := seen[task.ID]; exists {
			return EvalCorpus{}, fmt.Errorf("duplicate research eval task %s", task.ID)
		}
		seen[task.ID] = struct{}{}
	}
	return corpus, nil
}

func RunOfflineEval(ctx context.Context, corpus EvalCorpus) (EvalReport, error) {
	started := time.Now()
	report := EvalReport{Name: corpus.Name, Mode: "fixture", GeneratedAt: time.Now().UTC()}
	for _, task := range corpus.Tasks {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		result := evaluateFixtureTask(ctx, task)
		report.Tasks = append(report.Tasks, result)
	}
	report.Metrics = aggregateEvalMetrics(report.Tasks, time.Since(started))
	return report, nil
}

func evaluateFixtureTask(ctx context.Context, task EvalTask) EvalTaskResult {
	result := EvalTaskResult{ID: task.ID, Category: task.Category, GoldClaimTypes: append([]string(nil), task.GoldClaimTypes...)}
	fail := func(err error) EvalTaskResult {
		result.Reason = err.Error()
		return result
	}
	provider, err := NewMockProvider(task.Scenario)
	if err != nil {
		return fail(err)
	}
	papers, err := provider.Search(ctx, SearchRequest{Query: task.Query, MaxResults: 3})
	if err != nil {
		return fail(err)
	}
	result.RetrievedPapers += len(papers)
	if len(papers) < 2 && task.RecoveryQuery != "" {
		result.RecoveryAttempted = true
		result.Replans++
		papers, err = provider.Search(ctx, SearchRequest{Query: task.RecoveryQuery, MaxResults: 3})
		if err != nil {
			return fail(err)
		}
		result.RetrievedPapers += len(papers)
		result.RecoverySucceeded = len(papers) >= 2
	}

	var analyses []PaperAnalysis
	var documents []PaperDocument
	encounteredUnavailable := false
	for _, rank := range task.PaperRanks {
		if rank < 1 || rank > len(papers) {
			continue
		}
		paper := papers[rank-1]
		document, fetchErr := provider.Fetch(ctx, paper)
		if errors.Is(fetchErr, ErrFullTextUnavailable) {
			encounteredUnavailable = true
			result.RecoveryAttempted = true
			continue
		}
		if fetchErr != nil {
			return fail(fetchErr)
		}
		result.UsablePapers++
		parsed, parseErr := ParseDocument(FetchResult{
			Paper: paper, Query: task.Query, Available: true, ContentType: document.ContentType,
		}, document.Data)
		if parseErr != nil {
			return fail(parseErr)
		}
		documents = append(documents, parsed)
		analysis, analysisErr := AnalyzePaperContext(ctx, parsed, task.Query, "eval-"+task.ID)
		if analysisErr != nil {
			return fail(analysisErr)
		}
		analyses = append(analyses, analysis)
		result.AnalyzedPapers++
		collectEvalFindings(&result, analysis.Findings)
	}
	if encounteredUnavailable {
		result.RecoverySucceeded = len(analyses) >= 2
		result.Replans++
	}

	guardPassed := true
	if task.Injection == "unsupported_claim" {
		if len(documents) == 0 || len(documents[0].Sections) == 0 {
			return fail(fmt.Errorf("unsupported-claim fixture has no section"))
		}
		section := documents[0].Sections[0]
		snippet := firstNonemptyLine(section.Text)
		candidate := CandidateFinding{
			Claim: "The method achieves 99.9% state-of-the-art accuracy.", ClaimType: "result",
			PaperID: documents[0].Paper.ID, SectionID: section.ID, EvidenceText: snippet,
		}
		findings, _ := (EvidenceVerifier{}).Verify(ctx, documents[0], []CandidateFinding{candidate}, "eval-guard")
		collectEvalFindings(&result, findings)
		guardPassed = len(findings) == 1 && findings[0].Status == FindingUnsupported && findings[0].EvidenceID != ""
	}

	var synthesis Synthesis
	if len(analyses) >= 2 {
		synthesis, err = Synthesize(task.Query, analyses)
		if err != nil {
			return fail(err)
		}
		result.Facts = len(synthesis.Facts)
		for _, fact := range synthesis.Facts {
			if len(fact.EvidenceIDs) > 0 {
				result.FactsWithEvidence++
			}
		}
		design, designErr := DesignExperiment(task.Query, "offline fixture evaluation", synthesis)
		if designErr != nil {
			return fail(designErr)
		}
		report, reportErr := BuildReport(task.Query, synthesis, design)
		result.CitationChecks++
		if reportErr == nil {
			result.CitationClosed++
		} else {
			return fail(reportErr)
		}
		if task.Injection == "hallucinated_citation" {
			mutated := strings.Replace(report, "[P1]", "[P999]", 1)
			result.CitationChecks++
			if closureErr := ValidateReportClosure(mutated, synthesis, design); errors.Is(closureErr, ErrInvalidCitation) {
				result.HallucinatedReferences++
				result.CitationClosed++
			} else {
				guardPassed = false
			}
		}
	}

	predicted := make(map[string]struct{})
	for _, analysis := range analyses {
		for _, finding := range analysis.Findings {
			if finding.Status == FindingSupported {
				predicted[finding.Candidate.ClaimType] = struct{}{}
			}
		}
	}
	for claimType := range predicted {
		result.PredictedClaimTypes = append(result.PredictedClaimTypes, claimType)
	}
	sort.Strings(result.PredictedClaimTypes)
	for _, gold := range result.GoldClaimTypes {
		if _, exists := predicted[gold]; exists {
			result.TruePositiveTypes++
		}
	}
	goldPassed := result.TruePositiveTypes == len(result.GoldClaimTypes)
	result.Passed = guardPassed && goldPassed && result.AnalyzedPapers > 0 && (!result.RecoveryAttempted || result.RecoverySucceeded)
	if !result.Passed && result.Reason == "" {
		result.Reason = "fixture expectations were not satisfied"
	}
	return result
}

func collectEvalFindings(result *EvalTaskResult, findings []VerifiedFinding) {
	result.CandidateFindings += len(findings)
	for _, finding := range findings {
		if finding.EvidenceID != "" {
			result.VerifiedFindings++
		}
		switch finding.Status {
		case FindingSupported:
			result.SupportedFindings++
		case FindingUnsupported:
			result.UnsupportedFindings++
		}
	}
}

func firstNonemptyLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func aggregateEvalMetrics(tasks []EvalTaskResult, duration time.Duration) EvalMetrics {
	metrics := EvalMetrics{TotalTasks: len(tasks), Duration: duration.String(), DurationMillis: duration.Milliseconds()}
	predictedTypes, goldTypes, truePositiveTypes := 0, 0, 0
	facts, factsWithEvidence := 0, 0
	citationChecks, citationClosed := 0, 0
	for _, task := range tasks {
		if task.Passed {
			metrics.PassedTasks++
		} else {
			metrics.FailedTasks++
		}
		metrics.ReplanCount += task.Replans
		if task.RecoveryAttempted {
			metrics.RecoveryAttempts++
		}
		if task.RecoverySucceeded {
			metrics.RecoverySuccesses++
		}
		metrics.RetrievedPapers += task.RetrievedPapers
		metrics.UsablePapers += task.UsablePapers
		metrics.AnalyzedPapers += task.AnalyzedPapers
		metrics.CandidateFindings += task.CandidateFindings
		metrics.VerifiedFindings += task.VerifiedFindings
		metrics.SupportedFindings += task.SupportedFindings
		metrics.UnsupportedFindings += task.UnsupportedFindings
		metrics.HallucinatedReferenceCount += task.HallucinatedReferences
		predictedTypes += len(task.PredictedClaimTypes)
		goldTypes += len(task.GoldClaimTypes)
		truePositiveTypes += task.TruePositiveTypes
		facts += task.Facts
		factsWithEvidence += task.FactsWithEvidence
		citationChecks += task.CitationChecks
		citationClosed += task.CitationClosed
	}
	metrics.TaskSuccessRate = ratio(metrics.PassedTasks, metrics.TotalTasks)
	metrics.RecoveryRate = ratio(metrics.RecoverySuccesses, metrics.RecoveryAttempts)
	metrics.EvidenceVerificationRate = ratio(metrics.VerifiedFindings, metrics.CandidateFindings)
	metrics.CitationClosureRate = ratio(citationClosed, citationChecks)
	metrics.FactWithEvidenceRatio = ratio(factsWithEvidence, facts)
	metrics.UnsupportedFactCount = facts - factsWithEvidence
	metrics.ClaimTypePrecision = ratio(truePositiveTypes, predictedTypes)
	metrics.ClaimTypeRecall = ratio(truePositiveTypes, goldTypes)
	return metrics
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func WriteEvalReports(report EvalReport, outputDirectory string) (string, string, error) {
	outputDirectory, err := filepath.Abs(outputDirectory)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return "", "", err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", err
	}
	jsonPath := filepath.Join(outputDirectory, "eval-report.json")
	markdownPath := filepath.Join(outputDirectory, "eval-report.md")
	markdown := renderEvalMarkdown(report)
	if err := writeAtomicFile(jsonPath, append(encoded, '\n')); err != nil {
		return "", "", err
	}
	if err := writeAtomicFile(markdownPath, []byte(markdown)); err != nil {
		return "", "", err
	}
	return jsonPath, markdownPath, nil
}

func renderEvalMarkdown(report EvalReport) string {
	metrics := report.Metrics
	var output strings.Builder
	fmt.Fprintf(&output, "# CAPSuleAgent Research Eval\n\n- Mode: **%s**\n- Corpus: %s\n- Tasks passed: %d/%d\n\n", report.Mode, report.Name, metrics.PassedTasks, metrics.TotalTasks)
	fmt.Fprintf(&output, "## Research Quality\n\n- Retrieved papers: %d\n- Usable papers: %d\n- Analyzed papers: %d\n- Claim-type precision: %.3f\n- Claim-type recall: %.3f\n\n", metrics.RetrievedPapers, metrics.UsablePapers, metrics.AnalyzedPapers, metrics.ClaimTypePrecision, metrics.ClaimTypeRecall)
	fmt.Fprintf(&output, "## Evidence Reliability\n\n- Candidate findings: %d\n- Source-verified findings: %d\n- Supported findings: %d\n- Unsupported findings: %d\n- Verification rate: %.3f\n- FACT with evidence ratio: %.3f\n\n", metrics.CandidateFindings, metrics.VerifiedFindings, metrics.SupportedFindings, metrics.UnsupportedFindings, metrics.EvidenceVerificationRate, metrics.FactWithEvidenceRatio)
	fmt.Fprintf(&output, "## Citation Reliability\n\n- Closure rate: %.3f\n- Hallucinated references rejected: %d\n- Unsupported FACTs: %d\n\n", metrics.CitationClosureRate, metrics.HallucinatedReferenceCount, metrics.UnsupportedFactCount)
	fmt.Fprintf(&output, "## Agent Recovery\n\n- Replans: %d\n- Recovery rate: %.3f\n- Invalid plans: %d\n- Repeated plans: %d\n- Max-loop aborts: %d\n\n", metrics.ReplanCount, metrics.RecoveryRate, metrics.InvalidPlanCount, metrics.RepeatedPlanCount, metrics.MaxLoopAbortCount)
	fmt.Fprintf(&output, "## Runtime\n\n- Duration: %s\n- Input tokens: unavailable (fixture mode)\n- Output tokens: unavailable (fixture mode)\n\n", metrics.Duration)
	output.WriteString("## Tasks\n\n| Task | Category | Passed | Reason |\n|---|---|---:|---|\n")
	for _, task := range report.Tasks {
		fmt.Fprintf(&output, "| %s | %s | %t | %s |\n", task.ID, task.Category, task.Passed, strings.ReplaceAll(task.Reason, "|", "\\|"))
	}
	return output.String()
}

func writeAtomicFile(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".eval-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
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
