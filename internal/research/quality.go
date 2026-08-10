package research

import "strings"

const (
	QualityReady        = "READY"
	QualityPartial      = "PARTIAL"
	QualityInsufficient = "INSUFFICIENT"
)

// AssessResearchQuality is deliberately deterministic. It does not grade
// prose style; it checks whether the verified result contains the comparison
// dimensions needed to make the report useful.
func AssessResearchQuality(synthesis Synthesis, design ExperimentDesign) ResearchQuality {
	quality := ResearchQuality{ReferenceCount: len(synthesis.References)}
	for _, comparison := range synthesis.MethodComparison {
		hasMethod := strings.TrimSpace(comparison.Method) != ""
		hasDataset := len(nonEmptyStrings(comparison.Datasets)) > 0
		hasMetric := len(nonEmptyStrings(comparison.Metrics)) > 0
		hasLimitation := len(nonEmptyStrings(comparison.Limitations)) > 0
		if hasMethod {
			quality.MethodRows++
		}
		if hasDataset {
			quality.DatasetRows++
		}
		if hasMetric {
			quality.MetricRows++
		}
		if hasLimitation {
			quality.LimitationRows++
		}
		if hasMethod && hasDataset && hasMetric {
			quality.CompleteComparisonRows++
		}
	}

	quality.ExperimentReady = len(design.BaselineSuggestions) >= 2 &&
		len(design.Datasets) > 0 && len(design.Metrics) > 0 &&
		len(design.AblationPlan) > 0 && len(design.EvaluationProtocol) > 0

	denominator := quality.ReferenceCount
	if denominator < 1 {
		denominator = 1
	}
	quality.Score += 30 * quality.MethodRows / denominator
	quality.Score += 15 * quality.DatasetRows / denominator
	quality.Score += 20 * quality.MetricRows / denominator
	quality.Score += 10 * quality.LimitationRows / denominator
	if quality.ReferenceCount >= 3 && len(synthesis.Facts) >= quality.ReferenceCount {
		quality.Score += 10
	}
	if quality.ExperimentReady {
		quality.Score += 15
	}

	if quality.MethodRows < minInt(2, quality.ReferenceCount) {
		quality.Gaps = append(quality.Gaps, "fewer than two verified method descriptions")
	}
	if quality.DatasetRows == 0 {
		quality.Gaps = append(quality.Gaps, "no verified dataset coverage")
	}
	if quality.MetricRows == 0 {
		quality.Gaps = append(quality.Gaps, "no verified evaluation metric coverage")
	}
	if quality.LimitationRows == 0 {
		quality.Gaps = append(quality.Gaps, "no verified limitation coverage")
	}
	if !quality.ExperimentReady {
		quality.Gaps = append(quality.Gaps, "experiment proposal lacks a baseline, dataset, metric, ablation, or protocol")
	}

	switch {
	case quality.Score >= 75 && len(quality.Gaps) == 0:
		quality.Status = QualityReady
	case quality.ReferenceCount >= 2 && len(synthesis.Facts) > 0:
		quality.Status = QualityPartial
	default:
		quality.Status = QualityInsufficient
	}
	return quality
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
