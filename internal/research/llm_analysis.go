package research

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"aegisrt/internal/llm"
)

const (
	maximumAnalysisSections = 8
	maximumAnalysisBytes    = MaximumAnalysisContextBytes
	maximumSectionPrompt    = 12 * 1024
)

type LLMPaperAnalyzer struct {
	Client          llm.Client
	Support         ClaimSupportChecker
	MaxContextBytes int
}

type structuredPaperAnalysis struct {
	Problem          string             `json:"problem"`
	Method           string             `json:"method"`
	Contributions    []string           `json:"contributions"`
	Datasets         []string           `json:"datasets"`
	Metrics          []string           `json:"metrics"`
	Experiments      []string           `json:"experiments"`
	MainResults      []string           `json:"main_results"`
	Limitations      []string           `json:"limitations"`
	RelevantFindings []CandidateFinding `json:"relevant_findings"`
}

func (a LLMPaperAnalyzer) Analyze(
	ctx context.Context,
	document PaperDocument,
	question string,
	taskID string,
) (PaperAnalysis, error) {
	if a.Client == nil {
		return PaperAnalysis{}, fmt.Errorf("LLM paper analysis client is required")
	}
	sections := selectAnalysisSectionsWithLimit(document.Sections, a.MaxContextBytes)
	if len(sections) == 0 {
		return PaperAnalysis{}, fmt.Errorf("paper has no selectable sections")
	}
	payload, err := json.Marshal(map[string]any{
		"research_question": strings.TrimSpace(question),
		"paper": map[string]any{
			"paper_id": document.Paper.ID, "title": document.Paper.Title,
			"authors": document.Paper.Authors, "year": document.Paper.Year,
		},
		"sections": sections,
	})
	if err != nil {
		return PaperAnalysis{}, err
	}
	temperature := 0.0
	maximumTokens := 4096
	response, err := a.Client.Generate(ctx, llm.Request{Messages: []llm.Message{
		{Role: "system", Content: paperAnalysisSystemPrompt},
		{Role: "user", Content: string(payload)},
	}, Temperature: &temperature, MaxTokens: &maximumTokens, JSONMode: true})
	if err != nil {
		return PaperAnalysis{}, fmt.Errorf("LLM paper analysis: %w", err)
	}
	parsed, err := ParseStructuredPaperAnalysis(response.Content, document.Paper.ID)
	if err != nil {
		return PaperAnalysis{}, err
	}
	findings, evidence := (EvidenceVerifier{Support: a.Support}).Verify(ctx, document, parsed.RelevantFindings, taskID)
	var usage *TokenUsage
	if response.Usage != nil {
		usage = &TokenUsage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens}
	}
	analysis := analysisFromVerified(document, question, parsed.RelevantFindings, findings, evidence, usage)
	// A structurally valid analysis with rejected candidates remains an
	// observable result. Downstream synthesis still refuses it because no FACT
	// can be formed without SUPPORTED evidence, allowing the Agent loop to
	// re-plan from the real rejection instead of losing the observation.
	return analysis, nil
}

func ParseStructuredPaperAnalysis(content, paperID string) (structuredPaperAnalysis, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()
	var result structuredPaperAnalysis
	if err := decoder.Decode(&result); err != nil {
		return structuredPaperAnalysis{}, fmt.Errorf("malformed structured paper analysis: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return structuredPaperAnalysis{}, fmt.Errorf("malformed structured paper analysis: trailing JSON")
	}
	if len(result.RelevantFindings) == 0 || len(result.RelevantFindings) > maximumFindingsPerPaper {
		return structuredPaperAnalysis{}, fmt.Errorf("structured paper analysis must contain 1-%d candidate findings", maximumFindingsPerPaper)
	}
	allowedTypes := map[string]struct{}{
		"problem": {}, "method": {}, "contribution": {}, "dataset": {}, "metric": {},
		"experiment": {}, "result": {}, "limitation": {}, "relevant": {},
	}
	for index := range result.RelevantFindings {
		finding := &result.RelevantFindings[index]
		finding.Claim = strings.TrimSpace(finding.Claim)
		finding.ClaimType = strings.ToLower(strings.TrimSpace(finding.ClaimType))
		finding.PaperID = strings.TrimSpace(finding.PaperID)
		finding.SectionID = strings.TrimSpace(finding.SectionID)
		finding.EvidenceText = strings.TrimSpace(finding.EvidenceText)
		finding.Importance = strings.TrimSpace(finding.Importance)
		if finding.Claim == "" || finding.PaperID == "" || finding.SectionID == "" || finding.EvidenceText == "" {
			return structuredPaperAnalysis{}, fmt.Errorf("candidate finding %d is missing a required field", index)
		}
		if _, exists := allowedTypes[finding.ClaimType]; !exists {
			return structuredPaperAnalysis{}, fmt.Errorf("candidate finding %d has invalid claim_type %q", index, finding.ClaimType)
		}
		if len(finding.EvidenceText) > maximumSectionPrompt || finding.Confidence < 0 || finding.Confidence > 1 {
			return structuredPaperAnalysis{}, fmt.Errorf("candidate finding %d exceeds evidence or confidence limits", index)
		}
		if finding.PaperID != paperID {
			// Keep the mismatch visible to the deterministic verifier. It must not
			// be silently rewritten to the requested paper.
			continue
		}
	}
	return result, nil
}

func selectAnalysisSections(sections []Section) []map[string]any {
	return selectAnalysisSectionsWithLimit(sections, maximumAnalysisBytes)
}

func selectAnalysisSectionsWithLimit(sections []Section, maximumBytes int) []map[string]any {
	if maximumBytes <= 0 || maximumBytes > maximumAnalysisBytes {
		maximumBytes = maximumAnalysisBytes
	}
	priority := map[string]int{
		"abstract": 0, "introduction": 1, "method": 2, "experiments": 3,
		"results": 4, "discussion": 5, "conclusion": 6, "related_work": 7, "unknown": 8,
	}
	selected := append([]Section(nil), sections...)
	sort.SliceStable(selected, func(i, j int) bool {
		left, leftOK := priority[selected[i].NormalizedHeading]
		right, rightOK := priority[selected[j].NormalizedHeading]
		if !leftOK {
			left = 9
		}
		if !rightOK {
			right = 9
		}
		if left != right {
			return left < right
		}
		return selected[i].Start < selected[j].Start
	})
	result := make([]map[string]any, 0, maximumAnalysisSections)
	remaining := maximumBytes
	for _, section := range selected {
		if len(result) >= maximumAnalysisSections || remaining <= 0 || section.NormalizedHeading == "references" {
			continue
		}
		text := section.Text
		if len(text) > maximumSectionPrompt {
			text = truncateUTF8Bytes(text, maximumSectionPrompt)
		}
		if len(text) > remaining {
			text = truncateUTF8Bytes(text, remaining)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		result = append(result, map[string]any{
			"section_id": section.ID, "heading": section.Heading,
			"normalized_heading": section.NormalizedHeading, "text": text,
		})
		remaining -= len(text)
	}
	return result
}

const paperAnalysisSystemPrompt = `You are a structured scientific paper reader.
Use only the supplied paper metadata and sections. Return exactly one json object
with keys problem, method, contributions, datasets, metrics, experiments,
main_results, limitations, relevant_findings. Each relevant_finding must contain
claim, claim_type, paper_id, section_id, evidence_text, importance, confidence.
Use this exact value shape:
{"problem":"...","method":"...","contributions":["..."],"datasets":["..."],"metrics":["..."],"experiments":["..."],"main_results":["..."],"limitations":["..."],"relevant_findings":[{"claim":"...","claim_type":"method","paper_id":"...","section_id":"section-001","evidence_text":"verbatim source text","importance":"...","confidence":0.8}]}.
All list elements must be strings. confidence must be a JSON number from 0 to 1,
never a quoted string. Return every listed top-level key even when its list is empty.
claim_type must be exactly one of: problem, method, contribution, dataset, metric,
experiment, result, limitation, relevant. Never create another claim_type such as
observation, finding, analysis, or conclusion.
Copy evidence_text verbatim from the named section except that line-break and
space normalization is allowed. Keep claims close to what that evidence says.
For every non-empty method, dataset, metric, experiment, result, or limitation
you want the system to use, include a matching relevant_finding with the same
claim_type and source excerpt. Prioritize at least one method, one evaluation
dataset, one evaluation metric, one quantitative result, and one limitation when
the supplied sections contain them. Leave a top-level field empty instead of
returning a value that has no matching relevant_finding.
Never invent a section, paper, citation, dataset, metric, or numerical result.`
