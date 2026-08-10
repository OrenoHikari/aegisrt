package research

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var reportCitationPattern = regexp.MustCompile(`\[P[0-9]+\]`)

// BuildReport is citation-closed: every emitted reference comes from the
// validated Synthesis and every FACT/INFERENCE citation resolves through
// Evidence to one of those papers.
func BuildReport(goal string, synthesis Synthesis, design ExperimentDesign) (string, error) {
	if err := ValidateSynthesis(synthesis); err != nil {
		return "", err
	}
	if err := validateExperimentDesign(design); err != nil {
		return "", err
	}
	referenceKey := make(map[string]string, len(synthesis.References))
	for index, paper := range synthesis.References {
		referenceKey[paper.ID] = fmt.Sprintf("P%d", index+1)
	}
	evidenceByID := make(map[string]Evidence, len(synthesis.Evidence))
	for _, evidence := range synthesis.Evidence {
		evidenceByID[evidence.ID] = evidence
	}
	citations := func(ids []string) string {
		seen := make(map[string]struct{})
		var keys []string
		for _, id := range ids {
			evidence := evidenceByID[id]
			key := referenceKey[evidence.PaperID]
			if key != "" {
				if _, exists := seen[key]; !exists {
					keys = append(keys, "["+key+"]")
					seen[key] = struct{}{}
				}
			}
		}
		sort.Strings(keys)
		return strings.Join(keys, " ")
	}

	var report strings.Builder
	fmt.Fprintf(&report, "# Research Goal\n\n%s\n\n", strings.TrimSpace(goal))
	quality := AssessResearchQuality(synthesis, design)
	report.WriteString("# Research Readiness\n\n")
	fmt.Fprintf(&report, "- **Answer completeness:** %s (%d/100)\n", quality.Status, quality.Score)
	fmt.Fprintf(&report, "- **Evidence base:** %d references and %d evidence-backed findings\n", quality.ReferenceCount, len(synthesis.Facts)+len(synthesis.Inferences))
	fmt.Fprintf(&report, "- **Comparison coverage:** methods %d/%d, datasets %d/%d, metrics %d/%d, limitations %d/%d\n",
		quality.MethodRows, quality.ReferenceCount, quality.DatasetRows, quality.ReferenceCount,
		quality.MetricRows, quality.ReferenceCount, quality.LimitationRows, quality.ReferenceCount)
	if len(quality.Gaps) > 0 {
		report.WriteString("- **Known gaps:** " + strings.Join(quality.Gaps, "; ") + "\n")
	}
	report.WriteString("\n")
	report.WriteString("# Search Strategy\n\n")
	for _, query := range synthesis.QueryHistory {
		fmt.Fprintf(&report, "- `%s`\n", query)
	}
	report.WriteString("\n# Papers Reviewed\n\n")
	for _, paper := range synthesis.References {
		fmt.Fprintf(&report, "- [%s] **%s** (%d), %s\n", referenceKey[paper.ID], paper.Title, paper.Year, strings.Join(paper.Authors, ", "))
	}
	if len(synthesis.MetadataOnlyPapers) > 0 {
		report.WriteString("\n## Metadata-only Candidates\n\n")
		for _, paper := range synthesis.MetadataOnlyPapers {
			fmt.Fprintf(&report, "- **%s** (%d), `%s`, full text available: %t — not used as paper-content Evidence\n",
				paper.Title, paper.Year, paper.ID, paper.FullTextAvailable)
		}
	}
	report.WriteString("\n# Main Research Directions\n\n")
	for _, direction := range synthesis.ResearchDirections {
		fmt.Fprintf(&report, "- %s\n", direction)
	}
	report.WriteString("\n# Method Comparison\n\n| Paper | Verified method evidence | Datasets | Metrics | Limitations |\n|---|---|---|---|---|\n")
	for _, comparison := range synthesis.MethodComparison {
		fmt.Fprintf(&report, "| [%s] | %s | %s | %s | %s |\n",
			referenceKey[comparison.PaperID], reportCoverageValue(comparison.Method),
			reportCoverageValues(comparison.Datasets), reportCoverageValues(comparison.Metrics),
			reportCoverageValues(comparison.Limitations),
		)
	}
	report.WriteString("\n# Evidence-backed Findings\n\n")
	for _, finding := range synthesis.Facts {
		fmt.Fprintf(&report, "- **FACT:** %s %s\n", finding.Statement, citations(finding.EvidenceIDs))
	}
	for _, finding := range synthesis.Inferences {
		fmt.Fprintf(&report, "- **INFERENCE:** %s %s\n", finding.Statement, citations(finding.EvidenceIDs))
	}
	report.WriteString("\n# Datasets and Metrics\n\n")
	fmt.Fprintf(&report, "- Datasets: %s\n- Metrics: %s\n", reportCoverageValues(synthesis.Datasets), reportCoverageValues(synthesis.Metrics))
	report.WriteString("\n# Current Limitations\n\n")
	for _, limitation := range synthesis.Limitations {
		fmt.Fprintf(&report, "- %s\n", limitation)
	}
	report.WriteString("\n# Research Opportunities\n\n")
	for _, finding := range synthesis.Inferences {
		fmt.Fprintf(&report, "- **INFERENCE:** %s %s\n", finding.Statement, citations(finding.EvidenceIDs))
	}
	report.WriteString("\n# Proposed Experiment\n\n")
	fmt.Fprintf(&report, "- **PROPOSAL — Hypothesis:** %s\n", design.Hypothesis.Statement)
	writeProposalList(&report, "Baselines", design.BaselineSuggestions)
	writeProposalList(&report, "Datasets", design.Datasets)
	writeProposalList(&report, "Metrics", design.Metrics)
	writeProposalList(&report, "Ablations", design.AblationPlan)
	writeProposalList(&report, "Evaluation protocol", design.EvaluationProtocol)
	writeProposalList(&report, "Risks", design.ExpectedRisks)
	report.WriteString("\n# Evidence / References\n\n")
	for _, paper := range synthesis.References {
		identifier := paper.DOI
		if identifier == "" {
			identifier = paper.ID
		}
		fmt.Fprintf(&report, "- [%s] %s. **%s**. %d. %s. `%s`. %s\n",
			referenceKey[paper.ID], strings.Join(paper.Authors, ", "), paper.Title,
			paper.Year, paper.Provider, identifier, paper.URL,
		)
	}
	report.WriteString("\n## Evidence Ledger\n\n")
	for _, evidence := range synthesis.Evidence {
		fmt.Fprintf(&report, "- `%s` [%s], section **%s**, task `%s`: %s\n",
			evidence.ID, referenceKey[evidence.PaperID], evidence.Section,
			evidence.ProducingTask, evidence.Snippet,
		)
	}
	result := report.String()
	if err := ValidateReportClosure(result, synthesis, design); err != nil {
		return "", err
	}
	return result, nil
}

func reportCoverageValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "_Not established by verified evidence_"
	}
	return escapeTable(value)
}

func reportCoverageValues(values []string) string {
	return reportCoverageValue(strings.Join(nonEmptyStrings(values), ", "))
}

// ValidateReportClosure checks the emitted Markdown, after generation, against
// the verified synthesis graph. It rejects new references, metadata, FACTs,
// or citations introduced only in report text.
func ValidateReportClosure(report string, synthesis Synthesis, design ExperimentDesign) error {
	if err := ValidateSynthesis(synthesis); err != nil {
		return err
	}
	if err := validateExperimentDesign(design); err != nil {
		return err
	}
	referenceByKey := make(map[string]Paper, len(synthesis.References))
	keyByPaper := make(map[string]string, len(synthesis.References))
	for index, paper := range synthesis.References {
		key := fmt.Sprintf("P%d", index+1)
		referenceByKey[key] = paper
		keyByPaper[paper.ID] = key
	}
	for _, token := range reportCitationPattern.FindAllString(report, -1) {
		key := strings.TrimSuffix(strings.TrimPrefix(token, "["), "]")
		if _, exists := referenceByKey[key]; !exists {
			return fmt.Errorf("%w: report contains nonexistent citation %s", ErrInvalidCitation, token)
		}
	}

	evidenceByID := make(map[string]Evidence, len(synthesis.Evidence))
	for _, evidence := range synthesis.Evidence {
		evidenceByID[evidence.ID] = evidence
	}
	allowedFacts := make(map[string][]string, len(synthesis.Facts))
	allowedInferences := make(map[string][]string, len(synthesis.Inferences))
	for _, finding := range synthesis.Facts {
		allowedFacts[finding.Statement] = expectedCitationTokens(finding.EvidenceIDs, evidenceByID, keyByPaper)
	}
	for _, finding := range synthesis.Inferences {
		allowedInferences[finding.Statement] = expectedCitationTokens(finding.EvidenceIDs, evidenceByID, keyByPaper)
	}
	for _, line := range strings.Split(report, "\n") {
		switch {
		case strings.HasPrefix(line, "- **FACT:** "):
			if err := validateFindingLine(line, "- **FACT:** ", allowedFacts); err != nil {
				return err
			}
		case strings.HasPrefix(line, "- **INFERENCE:** "):
			if err := validateFindingLine(line, "- **INFERENCE:** ", allowedInferences); err != nil {
				return err
			}
		}
	}
	reviewedBlock, ok := markdownBetween(report, "# Papers Reviewed\n", "# Main Research Directions\n")
	if !ok {
		return fmt.Errorf("%w: reviewed-paper section is missing", ErrInvalidCitation)
	}
	expectedReviewed := make(map[string]struct{}, len(synthesis.References)+len(synthesis.MetadataOnlyPapers))
	for index, paper := range synthesis.References {
		expectedReviewed[fmt.Sprintf("- [P%d] **%s** (%d), %s", index+1, paper.Title, paper.Year, strings.Join(paper.Authors, ", "))] = struct{}{}
	}
	for _, paper := range synthesis.MetadataOnlyPapers {
		expectedReviewed[fmt.Sprintf("- **%s** (%d), `%s`, full text available: %t — not used as paper-content Evidence",
			paper.Title, paper.Year, paper.ID, paper.FullTextAvailable)] = struct{}{}
	}
	if err := validateExactMarkdownBullets(reviewedBlock, expectedReviewed); err != nil {
		return err
	}

	referenceBlock, ok := markdownBetween(report, "# Evidence / References\n", "## Evidence Ledger\n")
	if !ok {
		return fmt.Errorf("%w: report reference section is missing", ErrInvalidCitation)
	}
	expectedLines := make(map[string]struct{}, len(synthesis.References))
	for index, paper := range synthesis.References {
		identifier := paper.DOI
		if identifier == "" {
			identifier = paper.ID
		}
		line := fmt.Sprintf("- [P%d] %s. **%s**. %d. %s. `%s`. %s",
			index+1, strings.Join(paper.Authors, ", "), paper.Title, paper.Year,
			paper.Provider, identifier, paper.URL)
		expectedLines[line] = struct{}{}
	}
	if err := validateExactMarkdownBullets(referenceBlock, expectedLines); err != nil {
		return err
	}
	return nil
}

func validateExactMarkdownBullets(block string, expected map[string]struct{}) error {
	found := 0
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "- ") {
			continue
		}
		if _, exists := expected[line]; !exists {
			return fmt.Errorf("%w: report contains hallucinated reference metadata", ErrInvalidCitation)
		}
		found++
	}
	if found != len(expected) {
		return fmt.Errorf("%w: report reference closure is incomplete", ErrInvalidCitation)
	}
	return nil
}

func expectedCitationTokens(ids []string, evidence map[string]Evidence, keys map[string]string) []string {
	seen := make(map[string]struct{})
	var tokens []string
	for _, id := range ids {
		if key := keys[evidence[id].PaperID]; key != "" {
			token := "[" + key + "]"
			if _, exists := seen[token]; !exists {
				tokens = append(tokens, token)
				seen[token] = struct{}{}
			}
		}
	}
	sort.Strings(tokens)
	return tokens
}

func validateFindingLine(line, prefix string, allowed map[string][]string) error {
	tokens := reportCitationPattern.FindAllString(line, -1)
	if len(tokens) == 0 {
		return fmt.Errorf("%w: report finding has no citation", ErrInvalidCitation)
	}
	statement := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, prefix), " "+strings.Join(tokens, " ")))
	expected, exists := allowed[statement]
	if !exists || strings.Join(tokens, " ") != strings.Join(expected, " ") {
		return fmt.Errorf("%w: report contains an unsupported finding", ErrInvalidCitation)
	}
	return nil
}

func markdownBetween(document, startMarker, endMarker string) (string, bool) {
	start := strings.Index(document, startMarker)
	if start < 0 {
		return "", false
	}
	start += len(startMarker)
	end := strings.Index(document[start:], endMarker)
	if end < 0 {
		return "", false
	}
	return document[start : start+end], true
}

func validateExperimentDesign(design ExperimentDesign) error {
	findings := []Finding{design.Hypothesis}
	findings = append(findings, design.BaselineSuggestions...)
	findings = append(findings, design.Datasets...)
	findings = append(findings, design.Metrics...)
	findings = append(findings, design.AblationPlan...)
	findings = append(findings, design.EvaluationProtocol...)
	findings = append(findings, design.ExpectedRisks...)
	for _, finding := range findings {
		if finding.Kind != FindingProposal || strings.TrimSpace(finding.Statement) == "" || len(finding.EvidenceIDs) != 0 {
			return fmt.Errorf("experiment design contains a non-PROPOSAL statement")
		}
	}
	return nil
}

func writeProposalList(builder *strings.Builder, label string, findings []Finding) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(builder, "\n### %s\n\n", label)
	for _, finding := range findings {
		fmt.Fprintf(builder, "- **PROPOSAL:** %s\n", finding.Statement)
	}
}

func escapeTable(value string) string { return strings.ReplaceAll(value, "|", "\\|") }
