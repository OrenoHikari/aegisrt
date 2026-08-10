package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"aegisrt/internal/experiment"
	"aegisrt/internal/research"
)

const (
	maximumDashboardArtifactBytes = 4 * 1024 * 1024
	maximumDashboardReportBytes   = 2 * 1024 * 1024
)

type resultArtifact struct {
	path     string
	data     []byte
	modified time.Time
	taskID   string
}

func ScanArtifacts(root string) (ArtifactSnapshot, error) {
	artifacts, err := findResultArtifacts(filepath.Join(root, "outputs", "committed"))
	if err != nil {
		return ArtifactSnapshot{}, err
	}
	return scanResultArtifacts(artifacts)
}

// ScanVerifiedArtifacts limits the cognitive-plane view to committed output
// transactions that the existing Runtime explicitly reported as verified.
func ScanVerifiedArtifacts(root string, events []DashboardEvent) (ArtifactSnapshot, error) {
	committedRoot, err := filepath.Abs(filepath.Join(root, "outputs", "committed"))
	if err != nil {
		return ArtifactSnapshot{}, err
	}
	verified := make(map[string]string)
	for _, event := range events {
		if event.Kind != "runtime.output.verified" {
			continue
		}
		if outputVerified, ok := event.Data["output_verified"].(bool); ok && !outputVerified {
			continue
		}
		commitPath, _ := event.Data["output_commit_path"].(string)
		absolute, err := filepath.Abs(strings.TrimSpace(commitPath))
		if err != nil || !pathWithin(committedRoot, absolute) {
			continue
		}
		verified[filepath.Clean(absolute)] = event.TaskID
	}
	var artifacts []resultArtifact
	for directory, taskID := range verified {
		found, err := findResultArtifacts(directory)
		if err != nil {
			return ArtifactSnapshot{}, err
		}
		for index := range found {
			found[index].taskID = taskID
		}
		artifacts = append(artifacts, found...)
	}
	return scanResultArtifacts(artifacts)
}

func scanResultArtifacts(artifacts []resultArtifact) (ArtifactSnapshot, error) {
	sort.Slice(artifacts, func(i, j int) bool {
		if !artifacts[i].modified.Equal(artifacts[j].modified) {
			return artifacts[i].modified.Before(artifacts[j].modified)
		}
		return artifacts[i].path < artifacts[j].path
	})
	papers := make(map[string]*PaperView)
	statusRank := make(map[string]int)
	queries := make(map[string]struct{})
	var queryHistory []string
	deduplicated := make(map[string]struct{})
	snapshot := ArtifactSnapshot{}
	var latestSynthesis *resultArtifact
	var latestDesign *resultArtifact
	var synthesisValue research.Synthesis
	var designValue research.ExperimentDesign
	var synthesisAvailable, designAvailable bool
	referenceLabels := make(map[string]string)

	for index := range artifacts {
		artifact := &artifacts[index]
		var raw map[string]json.RawMessage
		if json.Unmarshal(artifact.data, &raw) != nil {
			continue
		}
		switch {
		case raw["total_results"] != nil && raw["papers"] != nil:
			var result research.SearchResult
			if json.Unmarshal(artifact.data, &result) != nil {
				continue
			}
			queryKey := strings.ToLower(strings.TrimSpace(result.Query))
			if _, exists := queries[queryKey]; queryKey != "" && !exists {
				queries[queryKey] = struct{}{}
				queryHistory = append(queryHistory, boundedText(strings.TrimSpace(result.Query), 1024))
			}
			for _, paper := range result.Papers {
				updatePaperView(papers, statusRank, paper, "RETRIEVED", nil, nil)
				if len(paper.MetadataSources) > 1 {
					deduplicated[paper.ID] = struct{}{}
				}
			}
		case raw["available"] != nil && raw["rank"] != nil:
			var result research.FetchResult
			if json.Unmarshal(artifact.data, &result) == nil && result.Available {
				updatePaperView(papers, statusRank, result.Paper, "DOWNLOADED", nil, nil)
				snapshot.Progress.PDFsAvailable++
			}
		case raw["diagnostics"] != nil && raw["sections"] != nil:
			var document research.PaperDocument
			if json.Unmarshal(artifact.data, &document) != nil {
				continue
			}
			sections := make([]SectionView, 0, len(document.Sections))
			for _, section := range document.Sections {
				sections = append(sections, SectionView{
					ID: section.ID, Heading: section.Heading, PageStart: section.PageStart,
					PageEnd: section.PageEnd, Characters: len(section.Text),
				})
			}
			diagnostics := document.Diagnostics
			updatePaperView(papers, statusRank, document.Paper, "PARSED", sections, &diagnostics)
			snapshot.Progress.ParsedPapers++
		case raw["candidate_findings"] != nil && raw["findings"] != nil:
			var analysis research.PaperAnalysis
			if json.Unmarshal(artifact.data, &analysis) != nil {
				continue
			}
			updatePaperView(papers, statusRank, analysis.Paper, "ANALYZED", nil, nil)
			snapshot.Progress.AnalyzedPapers++
			snapshot.Progress.LLMCalls += analysis.LLMCalls
			if analysis.Usage != nil {
				addTokenUsage(&snapshot.Progress, analysis.Usage.InputTokens, analysis.Usage.OutputTokens)
			}
			appendAnalysisEvidence(&snapshot.Evidence, analysis, artifact.taskID)
		case raw["method_comparison"] != nil && raw["facts"] != nil:
			if latestSynthesis == nil || artifact.modified.After(latestSynthesis.modified) {
				latestSynthesis = artifact
			}
		case raw["hypothesis"] != nil && raw["ablation_plan"] != nil:
			if latestDesign == nil || artifact.modified.After(latestDesign.modified) {
				latestDesign = artifact
			}
		}
	}

	if latestSynthesis != nil {
		if json.Unmarshal(latestSynthesis.data, &synthesisValue) == nil {
			synthesisAvailable = true
			for index, paper := range synthesisValue.References {
				referenceLabels[paper.ID] = fmt.Sprintf("P%d", index+1)
			}
			for _, finding := range synthesisValue.Facts {
				snapshot.Evidence.Facts = append(snapshot.Evidence.Facts, resultFindingView(finding))
			}
			for _, finding := range synthesisValue.Inferences {
				snapshot.Evidence.Inferences = append(snapshot.Evidence.Inferences, resultFindingView(finding))
			}
		}
	}
	if latestDesign != nil {
		if json.Unmarshal(latestDesign.data, &designValue) == nil {
			designAvailable = true
			appendProposal := func(finding research.Finding) {
				if strings.TrimSpace(finding.Statement) != "" {
					snapshot.Evidence.Proposals = append(snapshot.Evidence.Proposals, resultFindingView(finding))
				}
			}
			appendProposal(designValue.Hypothesis)
			for _, group := range [][]research.Finding{
				designValue.BaselineSuggestions, designValue.Datasets, designValue.Metrics, designValue.AblationPlan,
				designValue.EvaluationProtocol, designValue.ExpectedRisks,
			} {
				for _, finding := range group {
					appendProposal(finding)
				}
			}
		}
	}
	if synthesisAvailable && designAvailable {
		snapshot.Quality = research.AssessResearchQuality(synthesisValue, designValue)
	}

	snapshot.Progress.SearchQueries = len(queries)
	snapshot.Queries = queryHistory
	snapshot.Progress.RetrievedPapers = len(papers)
	snapshot.Progress.DeduplicatedPapers = len(deduplicated)
	snapshot.Progress.CandidateFindings = snapshot.Evidence.CandidateCount
	snapshot.Progress.SourceVerified = snapshot.Evidence.SourceVerifiedCount
	snapshot.Progress.SupportedFindings = snapshot.Evidence.SupportedCount
	snapshot.Progress.RejectedFindings = snapshot.Evidence.RejectedCount
	for _, paper := range papers {
		paper.Abstract = boundedText(paper.Abstract, 8*1024)
		paper.Reference = referenceLabels[paper.ID]
		snapshot.Papers = append(snapshot.Papers, *paper)
	}
	sort.Slice(snapshot.Papers, func(i, j int) bool {
		if snapshot.Papers[i].Year != snapshot.Papers[j].Year {
			return snapshot.Papers[i].Year > snapshot.Papers[j].Year
		}
		return snapshot.Papers[i].ID < snapshot.Papers[j].ID
	})
	return snapshot, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ReadRunSummary(root string) (*research.RunSummary, error) {
	data, err := readBounded(filepath.Join(root, "run-summary.json"), maximumDashboardArtifactBytes)
	if err != nil {
		return nil, err
	}
	var summary research.RunSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func ReadExperimentSummary(root string) (*experiment.RunSummary, error) {
	data, err := readBounded(filepath.Join(root, "experiment-summary.json"), maximumDashboardArtifactBytes)
	if err != nil {
		return nil, err
	}
	var summary experiment.RunSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func ReadFailures(root string) ([]research.FailureCase, error) {
	data, err := readBounded(filepath.Join(root, "failure-cases.json"), maximumDashboardArtifactBytes)
	if err != nil {
		return nil, err
	}
	var failures []research.FailureCase
	if err := json.Unmarshal(data, &failures); err != nil {
		return nil, err
	}
	return failures, nil
}

func ReadReport(root string) ([]byte, error) {
	return readBounded(filepath.Join(root, "report.md"), maximumDashboardReportBytes)
}

func findResultArtifacts(root string) ([]resultArtifact, error) {
	var result []resultArtifact
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return fs.SkipAll
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Name() != "result.json" {
			return nil
		}
		if len(result) >= 256 {
			return fmt.Errorf("dashboard run contains too many result artifacts")
		}
		data, err := readBounded(path, maximumDashboardArtifactBytes)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result = append(result, resultArtifact{path: path, data: data, modified: info.ModTime()})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].modified.Equal(result[j].modified) {
			return result[i].modified.Before(result[j].modified)
		}
		return result[i].path < result[j].path
	})
	return result, err
}

func updatePaperView(papers map[string]*PaperView, ranks map[string]int, paper research.Paper, status string, sections []SectionView, diagnostics *research.ParserDiagnostics) {
	if strings.TrimSpace(paper.ID) == "" {
		return
	}
	view, exists := papers[paper.ID]
	if !exists {
		view = &PaperView{ID: paper.ID}
		papers[paper.ID] = view
	}
	view.Title, view.Authors, view.Year = paper.Title, append([]string(nil), paper.Authors...), paper.Year
	view.Source, view.Abstract = paper.Provider, paper.Abstract
	statusRanks := map[string]int{"RETRIEVED": 1, "DOWNLOADED": 2, "PARSED": 3, "ANALYZED": 4}
	if statusRanks[status] >= ranks[paper.ID] {
		view.Status = status
		ranks[paper.ID] = statusRanks[status]
	}
	if sections != nil {
		view.Sections = sections
	}
	if diagnostics != nil {
		copy := *diagnostics
		view.Diagnostics = &copy
	}
}

func appendAnalysisEvidence(snapshot *EvidenceSnapshot, analysis research.PaperAnalysis, taskID string) {
	snapshot.CandidateCount += len(analysis.CandidateFindings)
	evidenceByID := make(map[string]research.Evidence, len(analysis.Evidence))
	for _, evidence := range analysis.Evidence {
		evidenceByID[evidence.ID] = evidence
	}
	for _, finding := range analysis.Findings {
		view := EvidenceFindingView{
			Status: string(finding.Status), Claim: finding.Candidate.Claim, ClaimType: finding.Candidate.ClaimType,
			PaperID: analysis.Paper.ID, PaperTitle: analysis.Paper.Title,
			SectionID: finding.Candidate.SectionID, Reason: finding.Reason, ReasonCode: evidenceReasonCode(finding.Reason),
			EvidenceID: finding.EvidenceID, TaskID: taskID,
		}
		if evidence, ok := evidenceByID[finding.EvidenceID]; ok {
			view.Section, view.SectionID = evidence.Section, evidence.SectionID
			view.Snippet = boundedText(evidence.Snippet, 4*1024)
			if strings.TrimSpace(evidence.ProducingTask) != "" {
				view.TaskID = evidence.ProducingTask
			}
		}
		if finding.EvidenceID != "" {
			snapshot.SourceVerifiedCount++
		}
		switch finding.Status {
		case research.FindingSupported:
			snapshot.SupportedCount++
		case research.FindingUnsupported:
			snapshot.RejectedCount++
		}
		snapshot.Findings = append(snapshot.Findings, view)
	}
}

func evidenceReasonCode(reason string) string {
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "evidence_text") && (strings.Contains(lower, "not") || strings.Contains(lower, "does not exist")):
		return "SOURCE_TEXT_NOT_FOUND"
	case strings.Contains(lower, "section") && (strings.Contains(lower, "missing") || strings.Contains(lower, "does not exist") || strings.Contains(lower, "not found")):
		return "INVALID_SECTION"
	case strings.Contains(lower, "paper") && (strings.Contains(lower, "wrong") || strings.Contains(lower, "mismatch")):
		return "WRONG_PAPER"
	case strings.Contains(lower, "not support") || strings.Contains(lower, "unsupported") || strings.Contains(lower, "exceeds evidence"):
		return "CLAIM_NOT_SUPPORTED"
	default:
		return ""
	}
}

func resultFindingView(finding research.Finding) ResultFindingView {
	return ResultFindingView{Kind: string(finding.Kind), Statement: finding.Statement, EvidenceIDs: append([]string(nil), finding.EvidenceIDs...)}
}

func addTokenUsage(progress *ArtifactProgress, input, output int) {
	if progress.InputTokens == nil {
		progress.InputTokens = new(int)
		progress.OutputTokens = new(int)
	}
	*progress.InputTokens += input
	*progress.OutputTokens += output
}

func readBounded(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("dashboard artifact exceeds %d bytes", maximum)
	}
	return data, nil
}

func boundedText(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return strings.TrimSpace(value[:end]) + "…"
}
