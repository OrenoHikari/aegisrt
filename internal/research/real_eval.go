package research

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maximumRealEvalBytes = 1024 * 1024

// RealEvalSuite is configuration, not domain logic: the normal research
// pipeline receives each Goal unchanged and remains useful for arbitrary goals.
type RealEvalSuite struct {
	Version int            `json:"version"`
	Name    string         `json:"name"`
	Mode    string         `json:"mode"`
	Goals   []RealEvalGoal `json:"goals"`
}

type RealEvalGoal struct {
	ID        string `json:"id"`
	Category  string `json:"category"`
	Goal      string `json:"goal"`
	MaxPapers int    `json:"max_papers"`
}

type GoldAnnotationSet struct {
	Version int                  `json:"version"`
	Suite   string               `json:"suite"`
	Goals   []GoldGoalAnnotation `json:"goals"`
}

type GoldGoalAnnotation struct {
	GoalID string                `json:"goal_id"`
	Status string                `json:"status"`
	Papers []GoldPaperAnnotation `json:"papers"`
}

type GoldPaperAnnotation struct {
	PaperID  string        `json:"paper_id"`
	Title    string        `json:"title,omitempty"`
	Findings []GoldFinding `json:"findings"`
}

// GoldFinding captures a human review of one system finding. Booleans are
// considered only when Reviewed is true, so an incomplete corpus never
// masquerades as measured recall or precision.
type GoldFinding struct {
	Claim              string `json:"claim"`
	Section            string `json:"section"`
	Evidence           string `json:"evidence"`
	Reviewed           bool   `json:"reviewed"`
	EvidenceExists     bool   `json:"evidence_exists"`
	SupportsClaim      bool   `json:"supports_claim"`
	Overstated         bool   `json:"overstated"`
	AttributionCorrect bool   `json:"attribution_correct"`
}

type ReviewedEvidenceMetrics struct {
	ReviewedFindings           int      `json:"reviewed_findings"`
	CorrectlySupportedFindings int      `json:"correctly_supported_findings"`
	UnsupportedFindings        int      `json:"unsupported_findings"`
	EvidencePrecision          *float64 `json:"evidence_precision"`
	RecallAvailable            bool     `json:"recall_available"`
}

func LoadRealEvalSuite(path string) (RealEvalSuite, error) {
	var suite RealEvalSuite
	if err := readStrictEvalJSON(path, &suite); err != nil {
		return RealEvalSuite{}, err
	}
	if suite.Version != 1 || suite.Mode != "real" || strings.TrimSpace(suite.Name) == "" {
		return RealEvalSuite{}, fmt.Errorf("real eval suite must be named mode=real version 1")
	}
	if len(suite.Goals) < 1 || len(suite.Goals) > 10 {
		return RealEvalSuite{}, fmt.Errorf("real eval suite must contain between 1 and 10 goals")
	}
	seen := make(map[string]struct{}, len(suite.Goals))
	for _, goal := range suite.Goals {
		if strings.TrimSpace(goal.ID) == "" || strings.TrimSpace(goal.Category) == "" || strings.TrimSpace(goal.Goal) == "" {
			return RealEvalSuite{}, fmt.Errorf("real eval goal is incomplete")
		}
		if goal.MaxPapers < 3 || goal.MaxPapers > 5 {
			return RealEvalSuite{}, fmt.Errorf("real eval goal %s max_papers must be between 3 and 5", goal.ID)
		}
		if _, exists := seen[goal.ID]; exists {
			return RealEvalSuite{}, fmt.Errorf("duplicate real eval goal %s", goal.ID)
		}
		seen[goal.ID] = struct{}{}
	}
	return suite, nil
}

func LoadGoldAnnotations(path string, suite RealEvalSuite) (GoldAnnotationSet, error) {
	var annotations GoldAnnotationSet
	if err := readStrictEvalJSON(path, &annotations); err != nil {
		return GoldAnnotationSet{}, err
	}
	if annotations.Version != 1 || annotations.Suite != suite.Name {
		return GoldAnnotationSet{}, fmt.Errorf("gold annotations must match suite %q and version 1", suite.Name)
	}
	goalIDs := make(map[string]struct{}, len(suite.Goals))
	for _, goal := range suite.Goals {
		goalIDs[goal.ID] = struct{}{}
	}
	seenGoals := make(map[string]struct{}, len(annotations.Goals))
	for _, goal := range annotations.Goals {
		if _, exists := goalIDs[goal.GoalID]; !exists {
			return GoldAnnotationSet{}, fmt.Errorf("gold annotations reference unknown goal %s", goal.GoalID)
		}
		if _, exists := seenGoals[goal.GoalID]; exists {
			return GoldAnnotationSet{}, fmt.Errorf("duplicate gold goal %s", goal.GoalID)
		}
		seenGoals[goal.GoalID] = struct{}{}
		if goal.Status != "PENDING" && goal.Status != "IN_PROGRESS" && goal.Status != "COMPLETE" {
			return GoldAnnotationSet{}, fmt.Errorf("gold goal %s has invalid status", goal.GoalID)
		}
		seenPapers := make(map[string]struct{}, len(goal.Papers))
		for _, paper := range goal.Papers {
			if strings.TrimSpace(paper.PaperID) == "" || len(paper.Findings) < 3 || len(paper.Findings) > 5 {
				return GoldAnnotationSet{}, fmt.Errorf("gold paper in goal %s requires an ID and 3-5 findings", goal.GoalID)
			}
			if _, exists := seenPapers[paper.PaperID]; exists {
				return GoldAnnotationSet{}, fmt.Errorf("duplicate gold paper %s", paper.PaperID)
			}
			seenPapers[paper.PaperID] = struct{}{}
			for _, finding := range paper.Findings {
				if strings.TrimSpace(finding.Claim) == "" || strings.TrimSpace(finding.Section) == "" || strings.TrimSpace(finding.Evidence) == "" {
					return GoldAnnotationSet{}, fmt.Errorf("gold finding for paper %s is incomplete", paper.PaperID)
				}
			}
		}
		if goal.Status == "COMPLETE" && len(goal.Papers) == 0 {
			return GoldAnnotationSet{}, fmt.Errorf("completed gold goal %s has no papers", goal.GoalID)
		}
	}
	if len(seenGoals) != len(goalIDs) {
		return GoldAnnotationSet{}, fmt.Errorf("gold annotations must contain one entry for every suite goal")
	}
	return annotations, nil
}

func EvaluateReviewedEvidence(annotations GoldAnnotationSet) ReviewedEvidenceMetrics {
	metrics := ReviewedEvidenceMetrics{}
	for _, goal := range annotations.Goals {
		for _, paper := range goal.Papers {
			for _, finding := range paper.Findings {
				if !finding.Reviewed {
					continue
				}
				metrics.ReviewedFindings++
				if finding.EvidenceExists && finding.SupportsClaim && !finding.Overstated && finding.AttributionCorrect {
					metrics.CorrectlySupportedFindings++
				} else {
					metrics.UnsupportedFindings++
				}
			}
		}
	}
	if metrics.ReviewedFindings > 0 {
		precision := float64(metrics.CorrectlySupportedFindings) / float64(metrics.ReviewedFindings)
		metrics.EvidencePrecision = &precision
	}
	return metrics
}

func WriteHumanReviewTemplate(suite RealEvalSuite, annotations GoldAnnotationSet, path string) error {
	goals := make(map[string]GoldGoalAnnotation, len(annotations.Goals))
	for _, goal := range annotations.Goals {
		goals[goal.GoalID] = goal
	}
	var document strings.Builder
	document.WriteString("# CAPSuleAgent Real Research Evaluation Review\n\n")
	document.WriteString("Human reviewers fill this document. The agent must not score itself. Scores use 1 (poor) to 5 (excellent).\n\n")
	document.WriteString("Reviewed evidence precision: `correctly supported / reviewed findings`; recall is intentionally not reported for incomplete gold annotations.\n\n")
	for index, goal := range suite.Goals {
		fmt.Fprintf(&document, "## %d. %s — %s\n\n", index+1, goal.ID, goal.Category)
		fmt.Fprintf(&document, "Goal: %s\n\n", goal.Goal)
		document.WriteString("| Criterion | Score (1-5) | Reviewer notes |\n|---|---:|---|\n")
		for _, criterion := range []string{"Relevance", "Correctness", "Evidence Quality", "Coverage", "Experiment Usefulness"} {
			fmt.Fprintf(&document, "| %s |  |  |\n", criterion)
		}
		document.WriteString("\n### Finding review\n\n")
		document.WriteString("| Paper ID | Claim | Evidence exists? | Supports claim? | Overstated? | Attribution correct? | Notes |\n|---|---|---|---|---|---|---|\n")
		annotation := goals[goal.ID]
		rows := 0
		for _, paper := range annotation.Papers {
			for _, finding := range paper.Findings {
				fmt.Fprintf(&document, "| %s | %s |  |  |  |  | section: %s |\n", escapeMarkdownCell(paper.PaperID), escapeMarkdownCell(finding.Claim), escapeMarkdownCell(finding.Section))
				rows++
			}
		}
		for rows < goal.MaxPapers*3 {
			document.WriteString("|  |  |  |  |  |  |  |\n")
			rows++
		}
		document.WriteString("\nCitation review: total references ___; retrieved ___; valid metadata ___; hallucinated ___; duplicated ___.\n\n")
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	return writeAtomicFile(path, []byte(document.String()))
}

func readStrictEvalJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumRealEvalBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maximumRealEvalBytes {
		return fmt.Errorf("real eval file exceeds %d bytes", maximumRealEvalBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("real eval file has trailing JSON")
	}
	return nil
}

func escapeMarkdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "\n", " ")
}
