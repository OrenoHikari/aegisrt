package research

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"aegisrt/internal/llm"
)

type ClaimSupport string

const (
	ClaimSupported   ClaimSupport = "SUPPORTED"
	ClaimUnsupported ClaimSupport = "UNSUPPORTED"
	ClaimUncertain   ClaimSupport = "UNCERTAIN"
)

type ClaimSupportChecker interface {
	Check(ctx context.Context, candidate CandidateFinding, evidence Evidence) (ClaimSupport, string, error)
}

type DeterministicClaimSupport struct{}

var (
	claimTokenRegexp  = regexp.MustCompile(`[\pL\pN]+`)
	claimNumberRegexp = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?%?`)
)

func (DeterministicClaimSupport) Check(_ context.Context, candidate CandidateFinding, evidence Evidence) (ClaimSupport, string, error) {
	claim := normalizeWhitespace(strings.ToLower(candidate.Claim))
	source := normalizeWhitespace(strings.ToLower(evidence.Snippet))
	if claim == "" || source == "" {
		return ClaimUnsupported, "claim or evidence is empty", nil
	}
	if strings.Contains(source, claim) {
		return ClaimSupported, "the canonical evidence contains the claim text", nil
	}
	for _, number := range claimNumberRegexp.FindAllString(claim, -1) {
		if !strings.Contains(source, number) {
			return ClaimUnsupported, "a quantitative value in the claim is absent from the evidence", nil
		}
	}
	for _, qualifier := range []string{
		"state-of-the-art", "state of the art", "outperform", "best", "highest", "lowest",
		"increase", "decrease", "reduce", "improve", "achieve",
	} {
		if strings.Contains(claim, qualifier) && !strings.Contains(source, qualifier) {
			return ClaimUnsupported, "a material comparative qualifier is absent from the evidence", nil
		}
	}
	claimTokens := significantTokens(claim)
	if len(claimTokens) == 0 {
		return ClaimUncertain, "claim has no significant tokens", nil
	}
	sourceTokens := make(map[string]struct{})
	for _, token := range significantTokens(source) {
		sourceTokens[token] = struct{}{}
	}
	matched := 0
	for _, token := range claimTokens {
		if _, exists := sourceTokens[token]; exists {
			matched++
		}
	}
	ratio := float64(matched) / float64(len(claimTokens))
	if ratio >= 0.75 {
		return ClaimSupported, "high-precision lexical support passed", nil
	}
	if ratio < 0.35 {
		return ClaimUnsupported, "claim and evidence have insufficient lexical support", nil
	}
	return ClaimUncertain, "evidence exists but deterministic entailment is inconclusive", nil
}

func significantTokens(value string) []string {
	stop := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {},
		"for": {}, "from": {}, "in": {}, "is": {}, "of": {}, "on": {}, "or": {}, "that": {},
		"the": {}, "this": {}, "to": {}, "was": {}, "were": {}, "with": {},
	}
	var result []string
	for _, token := range claimTokenRegexp.FindAllString(strings.ToLower(value), -1) {
		if _, ignored := stop[token]; !ignored && len([]rune(token)) > 1 {
			result = append(result, token)
		}
	}
	return result
}

// LLMClaimSupportChecker is optional semantic assistance. Evidence existence
// and canonical ranges have already been established deterministically.
type LLMClaimSupportChecker struct{ Client llm.Client }

func (c LLMClaimSupportChecker) Check(ctx context.Context, candidate CandidateFinding, evidence Evidence) (ClaimSupport, string, error) {
	if c.Client == nil {
		return "", "", fmt.Errorf("LLM claim support client is required")
	}
	payload, _ := json.Marshal(map[string]string{"claim": candidate.Claim, "evidence": evidence.Snippet})
	temperature := 0.0
	maximumTokens := 512
	response, err := c.Client.Generate(ctx, llm.Request{Messages: []llm.Message{
		{Role: "system", Content: `Judge whether the evidence entails the claim. Return exactly this json object: {"decision":"SUPPORTED|UNSUPPORTED|UNCERTAIN","reason":"..."}. Do not use outside knowledge.`},
		{Role: "user", Content: string(payload)},
	}, Temperature: &temperature, MaxTokens: &maximumTokens, JSONMode: true})
	if err != nil {
		return "", "", err
	}
	return ParseClaimSupportDecision(response.Content)
}

func ParseClaimSupportDecision(content string) (ClaimSupport, string, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()
	var value struct {
		Decision ClaimSupport `json:"decision"`
		Reason   string       `json:"reason"`
	}
	if err := decoder.Decode(&value); err != nil {
		return "", "", fmt.Errorf("malformed claim support response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", "", fmt.Errorf("malformed claim support response: trailing JSON")
	}
	value.Reason = strings.TrimSpace(value.Reason)
	if value.Reason == "" {
		return "", "", fmt.Errorf("malformed claim support response: reason is required")
	}
	switch value.Decision {
	case ClaimSupported, ClaimUnsupported, ClaimUncertain:
		return value.Decision, value.Reason, nil
	default:
		return "", "", fmt.Errorf("malformed claim support response: invalid decision %q", value.Decision)
	}
}

// EvidenceVerifier resolves candidate snippets to canonical document bytes.
type EvidenceVerifier struct{ Support ClaimSupportChecker }

func (v EvidenceVerifier) Verify(
	ctx context.Context,
	document PaperDocument,
	candidates []CandidateFinding,
	taskID string,
) ([]VerifiedFinding, []Evidence) {
	checker := v.Support
	if checker == nil {
		checker = DeterministicClaimSupport{}
	}
	sections := make(map[string]Section, len(document.Sections))
	for _, section := range document.Sections {
		sections[section.ID] = section
	}
	findings := make([]VerifiedFinding, 0, len(candidates))
	evidence := make([]Evidence, 0, len(candidates))
	for _, candidate := range candidates {
		finding := VerifiedFinding{Candidate: candidate, Status: FindingUnsupported}
		if strings.TrimSpace(candidate.PaperID) != document.Paper.ID {
			finding.Reason = "candidate paper_id does not match the parsed document"
			findings = append(findings, finding)
			continue
		}
		section, exists := sections[strings.TrimSpace(candidate.SectionID)]
		if !exists {
			finding.Reason = "candidate section_id does not exist"
			findings = append(findings, finding)
			continue
		}
		start, end, ok := locateNormalizedEvidence(section.Text, candidate.EvidenceText)
		if !ok {
			finding.Reason = "candidate evidence_text does not exist in the selected section"
			findings = append(findings, finding)
			continue
		}
		canonical := Evidence{
			ID:     fmt.Sprintf("%s-e%d", document.Paper.ID, len(evidence)+1),
			Source: document.Paper.URL, PaperID: document.Paper.ID, Claim: strings.TrimSpace(candidate.Claim),
			Section: section.Heading, SectionID: section.ID, Start: start, End: end,
			Snippet: section.Text[start:end], ProducingTask: strings.TrimSpace(taskID), Status: FindingVerifiedSource,
		}
		decision, reason, err := checker.Check(ctx, candidate, canonical)
		if err != nil {
			finding.Reason = "claim support check failed: " + err.Error()
			findings = append(findings, finding)
			continue
		}
		switch decision {
		case ClaimSupported:
			canonical.Status = FindingSupported
			finding.Status = FindingSupported
		case ClaimUncertain:
			finding.Status = FindingVerifiedSource
		case ClaimUnsupported:
			finding.Status = FindingUnsupported
		}
		finding.EvidenceID = canonical.ID
		finding.Reason = reason
		evidence = append(evidence, canonical)
		findings = append(findings, finding)
	}
	return findings, evidence
}

func normalizeWhitespace(value string) string { return strings.Join(strings.Fields(value), " ") }

func locateNormalizedEvidence(sectionText, candidate string) (int, int, bool) {
	candidate = normalizeWhitespace(candidate)
	if candidate == "" || len(candidate) > maximumSectionCharacters {
		return 0, 0, false
	}
	normalized, mapping := normalizedTextMapping(sectionText)
	index := strings.Index(normalized, candidate)
	if index < 0 || index+len(candidate) > len(mapping) {
		return 0, 0, false
	}
	start := mapping[index]
	last := mapping[index+len(candidate)-1]
	end := last + 1
	if start < 0 || end > len(sectionText) || start >= end {
		return 0, 0, false
	}
	return start, end, true
}

func normalizedTextMapping(value string) (string, []int) {
	var normalized strings.Builder
	var mapping []int
	inWhitespace := true
	for index := 0; index < len(value); {
		r, size := utf8.DecodeRuneInString(value[index:])
		if unicode.IsSpace(r) {
			inWhitespace = true
			index += size
			continue
		}
		if inWhitespace && normalized.Len() > 0 {
			normalized.WriteByte(' ')
			mapping = append(mapping, index)
		}
		inWhitespace = false
		chunk := value[index : index+size]
		normalized.WriteString(chunk)
		for offset := 0; offset < size; offset++ {
			mapping = append(mapping, index+offset)
		}
		index += size
	}
	return normalized.String(), mapping
}
