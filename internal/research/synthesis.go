package research

import (
	"fmt"
	"sort"
	"strings"
)

// Synthesize combines multiple analyses while preserving every source edge.
func Synthesize(goal string, analyses []PaperAnalysis) (Synthesis, error) {
	store := NewEvidenceStore()
	seenPaperIDs := make(map[string]struct{}, len(analyses))
	synthesis := Synthesis{Goal: strings.TrimSpace(goal)}
	usable := make([]PaperAnalysis, 0, len(analyses))
	for _, analysis := range analyses {
		paperID := strings.TrimSpace(analysis.Paper.ID)
		if paperID == "" {
			continue
		}
		if _, exists := seenPaperIDs[paperID]; exists {
			return Synthesis{}, fmt.Errorf("%w: paper %s", ErrInvalidCitation, paperID)
		}
		seenPaperIDs[paperID] = struct{}{}
		synthesis.RetrievedPaperIDs = appendUnique(synthesis.RetrievedPaperIDs, paperID)
		synthesis.QueryHistory = appendUnique(synthesis.QueryHistory, analysis.Query)
		if len(supportedEvidence(analysis)) == 0 {
			synthesis.MetadataOnlyPapers = append(synthesis.MetadataOnlyPapers, analysis.Paper)
			continue
		}
		usable = append(usable, analysis)
	}
	if len(usable) < 2 {
		return Synthesis{}, fmt.Errorf("%w: need at least two usable papers", ErrInsufficientEvidence)
	}

	allEvidenceIDs := make([]string, 0)
	for _, analysis := range usable {
		paperEvidence := supportedEvidence(analysis)
		synthesis.References = append(synthesis.References, analysis.Paper)
		synthesis.ResearchDirections = appendUnique(synthesis.ResearchDirections, analysis.Method)
		synthesis.Datasets = appendUnique(synthesis.Datasets, analysis.Datasets...)
		synthesis.Metrics = appendUnique(synthesis.Metrics, analysis.Metrics...)
		synthesis.Limitations = appendUnique(synthesis.Limitations, analysis.Limitations...)
		synthesis.MethodComparison = append(synthesis.MethodComparison, MethodComparison{
			PaperID: analysis.Paper.ID, Method: analysis.Method,
			Datasets: append([]string(nil), analysis.Datasets...), Metrics: append([]string(nil), analysis.Metrics...),
			Limitations: append([]string(nil), analysis.Limitations...),
		})
		for _, evidence := range paperEvidence {
			if evidence.PaperID != analysis.Paper.ID {
				return Synthesis{}, fmt.Errorf("%w: evidence %s paper mismatch", ErrInvalidCitation, evidence.ID)
			}
			if err := store.Add(evidence); err != nil {
				return Synthesis{}, err
			}
			allEvidenceIDs = append(allEvidenceIDs, evidence.ID)
			synthesis.Facts = append(synthesis.Facts, Finding{
				Kind: FindingFact, Statement: evidence.Claim, EvidenceIDs: []string{evidence.ID},
			})
		}
	}
	synthesis.Evidence = store.All()
	sort.Slice(synthesis.References, func(i, j int) bool {
		if synthesis.References[i].Year != synthesis.References[j].Year {
			return synthesis.References[i].Year < synthesis.References[j].Year
		}
		return synthesis.References[i].ID < synthesis.References[j].ID
	})
	if len(synthesis.ResearchDirections) > 1 {
		synthesis.Inferences = append(synthesis.Inferences, Finding{
			Kind:        FindingInference,
			Statement:   fmt.Sprintf("The reviewed work separates into %d distinct technical routes rather than one uniform method.", len(synthesis.ResearchDirections)),
			EvidenceIDs: append([]string(nil), allEvidenceIDs...),
		})
	}
	if !hasSharedDataset(usable) {
		synthesis.Inferences = append(synthesis.Inferences, Finding{
			Kind:        FindingInference,
			Statement:   "The reported numerical results are not directly comparable because the reviewed papers use different datasets or evaluation splits.",
			EvidenceIDs: datasetEvidenceIDs(usable),
		})
	}
	if err := ValidateSynthesis(synthesis); err != nil {
		return Synthesis{}, err
	}
	return synthesis, nil
}

func supportedEvidence(analysis PaperAnalysis) []Evidence {
	result := make([]Evidence, 0, len(analysis.Evidence))
	for _, evidence := range analysis.Evidence {
		if evidence.Status == FindingSupported {
			result = append(result, evidence)
		}
	}
	return result
}

func ValidateSynthesis(synthesis Synthesis) error {
	if len(synthesis.References) == 0 || len(synthesis.Evidence) == 0 {
		return ErrInsufficientEvidence
	}
	references := make(map[string]struct{}, len(synthesis.References))
	retrieved := make(map[string]struct{}, len(synthesis.RetrievedPaperIDs))
	for _, paperID := range synthesis.RetrievedPaperIDs {
		paperID = strings.TrimSpace(paperID)
		if paperID == "" {
			return fmt.Errorf("%w: empty retrieved paper identity", ErrInvalidCitation)
		}
		retrieved[paperID] = struct{}{}
	}
	for _, paper := range synthesis.References {
		if paper.ID == "" || paper.Title == "" || paper.URL == "" {
			return fmt.Errorf("%w: incomplete reference", ErrInvalidCitation)
		}
		if _, exists := references[paper.ID]; exists {
			return fmt.Errorf("%w: duplicate reference %s", ErrInvalidCitation, paper.ID)
		}
		if _, exists := retrieved[paper.ID]; !exists {
			return fmt.Errorf("%w: reference %s was not retrieved", ErrInvalidCitation, paper.ID)
		}
		references[paper.ID] = struct{}{}
	}
	metadataOnly := make(map[string]struct{}, len(synthesis.MetadataOnlyPapers))
	for _, paper := range synthesis.MetadataOnlyPapers {
		if paper.ID == "" || paper.Title == "" || paper.URL == "" || paper.Provider == "" {
			return fmt.Errorf("%w: incomplete metadata-only paper", ErrInvalidCitation)
		}
		if _, used := references[paper.ID]; used {
			return fmt.Errorf("%w: paper %s is both referenced and metadata-only", ErrInvalidCitation, paper.ID)
		}
		if _, duplicate := metadataOnly[paper.ID]; duplicate {
			return fmt.Errorf("%w: duplicate metadata-only paper %s", ErrInvalidCitation, paper.ID)
		}
		if _, exists := retrieved[paper.ID]; !exists {
			return fmt.Errorf("%w: metadata-only paper %s was not retrieved", ErrInvalidCitation, paper.ID)
		}
		metadataOnly[paper.ID] = struct{}{}
	}
	store := NewEvidenceStore()
	referencedByEvidence := make(map[string]struct{}, len(synthesis.References))
	for _, evidence := range synthesis.Evidence {
		if evidence.Status != FindingSupported {
			return fmt.Errorf("%w: FACT evidence %s is not SUPPORTED", ErrInvalidCitation, evidence.ID)
		}
		if _, exists := references[evidence.PaperID]; !exists {
			return fmt.Errorf("%w: evidence paper %s", ErrInvalidCitation, evidence.PaperID)
		}
		if err := store.Add(evidence); err != nil {
			return err
		}
		referencedByEvidence[evidence.PaperID] = struct{}{}
	}
	for paperID := range references {
		if _, exists := referencedByEvidence[paperID]; !exists {
			return fmt.Errorf("%w: reference %s has no retrieved evidence", ErrInvalidCitation, paperID)
		}
	}
	for _, finding := range append(append([]Finding(nil), synthesis.Facts...), synthesis.Inferences...) {
		if finding.Kind != FindingFact && finding.Kind != FindingInference {
			return fmt.Errorf("invalid synthesis finding kind %q", finding.Kind)
		}
		if strings.TrimSpace(finding.Statement) == "" || len(finding.EvidenceIDs) == 0 {
			return fmt.Errorf("%w: finding lacks evidence", ErrInvalidCitation)
		}
		for _, evidenceID := range finding.EvidenceIDs {
			if _, exists := store.Get(evidenceID); !exists {
				return fmt.Errorf("%w: unknown evidence %s", ErrInvalidCitation, evidenceID)
			}
		}
		if finding.Kind == FindingFact && !factMatchesEvidence(finding, store) {
			return fmt.Errorf("%w: FACT statement is not the supported evidence claim", ErrInvalidCitation)
		}
	}
	for _, comparison := range synthesis.MethodComparison {
		if _, exists := references[comparison.PaperID]; !exists {
			return fmt.Errorf("%w: method comparison paper %s was not retrieved", ErrInvalidCitation, comparison.PaperID)
		}
		for _, value := range append(append(append([]string{comparison.Method}, comparison.Datasets...), comparison.Metrics...), comparison.Limitations...) {
			if strings.TrimSpace(value) != "" && !paperEvidenceContains(synthesis.Evidence, comparison.PaperID, value) {
				return fmt.Errorf("%w: comparison field for %s lacks supported source text", ErrInvalidCitation, comparison.PaperID)
			}
		}
	}
	for _, value := range append(append([]string(nil), synthesis.Datasets...), synthesis.Metrics...) {
		if strings.TrimSpace(value) != "" && !paperEvidenceContains(synthesis.Evidence, "", value) {
			return fmt.Errorf("%w: dataset or metric %q lacks supported source text", ErrInvalidCitation, value)
		}
	}
	return nil
}

func factMatchesEvidence(finding Finding, store *EvidenceStore) bool {
	wanted := normalizeWhitespace(strings.ToLower(finding.Statement))
	for _, evidenceID := range finding.EvidenceIDs {
		evidence, exists := store.Get(evidenceID)
		if exists && normalizeWhitespace(strings.ToLower(evidence.Claim)) == wanted {
			return true
		}
	}
	return false
}

func paperEvidenceContains(evidence []Evidence, paperID, value string) bool {
	wanted := normalizeWhitespace(strings.ToLower(value))
	for _, item := range evidence {
		if item.Status != FindingSupported || (paperID != "" && item.PaperID != paperID) {
			continue
		}
		claim := normalizeWhitespace(strings.ToLower(item.Claim))
		if strings.Contains(claim, wanted) || strings.Contains(wanted, claim) {
			return true
		}
	}
	return false
}

// DesignExperiment creates explicitly labeled proposals grounded in the
// synthesis but never presents them as published facts.
func DesignExperiment(goal, constraints string, synthesis Synthesis) (ExperimentDesign, error) {
	if err := ValidateSynthesis(synthesis); err != nil {
		return ExperimentDesign{}, err
	}
	proposal := func(statement string) Finding {
		return Finding{Kind: FindingProposal, Statement: statement}
	}
	design := ExperimentDesign{Goal: strings.TrimSpace(goal)}
	referenceTitles := make(map[string]string, len(synthesis.References))
	for _, paper := range synthesis.References {
		referenceTitles[paper.ID] = strings.TrimSpace(paper.Title)
	}
	routeA, routeB := "the first reviewed method", "the second reviewed method"
	if len(synthesis.ResearchDirections) > 0 {
		routeA = boundedPhrase(synthesis.ResearchDirections[0], 120)
	}
	if len(synthesis.ResearchDirections) > 1 {
		routeB = boundedPhrase(synthesis.ResearchDirections[1], 120)
	}
	design.Hypothesis = proposal(fmt.Sprintf(
		"Combining complementary components from the reviewed routes %q and %q will improve the primary evaluation metrics for the research goal %q under a common protocol.",
		routeA, routeB, boundedPhrase(design.Goal, 160),
	))
	for _, comparison := range synthesis.MethodComparison {
		route := strings.TrimSpace(comparison.Method)
		if route == "" {
			route = referenceTitles[comparison.PaperID]
		}
		if route == "" {
			continue
		}
		design.BaselineSuggestions = append(design.BaselineSuggestions, proposal(
			fmt.Sprintf("Reproduce the %s route from %s as a separately reported baseline.", route, comparison.PaperID),
		))
	}
	for _, dataset := range synthesis.Datasets {
		design.Datasets = append(design.Datasets, proposal("Evaluate on "+dataset+" with the original paper split and a documented common split where licensing permits."))
	}
	for _, metric := range synthesis.Metrics {
		design.Metrics = append(design.Metrics, proposal("Report "+metric+" with confidence intervals and per-query-type breakdowns."))
	}
	if len(design.Datasets) == 0 {
		design.Datasets = append(design.Datasets, proposal(
			"Choose a public task dataset named in the research goal or experimental constraints and document its exact split and license.",
		))
	}
	if len(design.Metrics) == 0 {
		design.Metrics = append(design.Metrics, proposal(
			"Report the task-quality metric together with end-to-end latency, decoding throughput, peak memory, and the method-specific compression ratio.",
		))
	}
	design.AblationPlan = []Finding{
		proposal("Remove each proposed component one at a time while holding data, optimization, and evaluation settings fixed."),
		proposal("Evaluate the two selected research-route components separately and then in combination."),
		proposal("Repeat the comparison without auxiliary or cross-dataset pretraining to measure transfer dependence."),
	}
	design.EvaluationProtocol = []Finding{
		proposal("Use identical train/validation/test identities for all baselines within each dataset."),
		proposal("Report dataset-specific results separately before any macro average because published results are not directly comparable."),
	}
	design.ExpectedRisks = []Finding{
		proposal("Dataset licensing or unavailable annotations may prevent an exact reproduction."),
		proposal("The reviewed datasets and benchmark definitions may not represent deployment conditions outside the available evidence."),
	}
	if value := strings.TrimSpace(constraints); value != "" {
		design.ExpectedRisks = append(design.ExpectedRisks, proposal("Apply the stated experimental constraint: "+value))
	}
	return design, nil
}

func boundedPhrase(value string, maximum int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return strings.TrimSpace(string(runes[:maximum])) + "…"
}

func hasSharedDataset(analyses []PaperAnalysis) bool {
	if len(analyses) < 2 {
		return false
	}
	counts := make(map[string]int)
	for _, analysis := range analyses {
		seen := make(map[string]struct{})
		for _, dataset := range analysis.Datasets {
			key := strings.ToLower(dataset)
			if _, exists := seen[key]; !exists {
				counts[key]++
				seen[key] = struct{}{}
			}
		}
	}
	for _, count := range counts {
		if count == len(analyses) {
			return true
		}
	}
	return false
}

func datasetEvidenceIDs(analyses []PaperAnalysis) []string {
	var result []string
	for _, analysis := range analyses {
		for _, evidence := range analysis.Evidence {
			if evidence.Status != FindingSupported {
				continue
			}
			claim := strings.ToLower(evidence.Claim)
			if strings.Contains(claim, "dataset") || strings.Contains(claim, "rec-") || strings.Contains(claim, "phrase") || strings.Contains(claim, "fsc-") {
				result = append(result, evidence.ID)
			}
		}
	}
	if len(result) == 0 {
		for _, analysis := range analyses {
			for _, evidence := range analysis.Evidence {
				if evidence.Status == FindingSupported {
					result = append(result, evidence.ID)
					break
				}
			}
		}
	}
	return result
}
