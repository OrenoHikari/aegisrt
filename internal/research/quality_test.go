package research

import "testing"

func TestAssessResearchQualityDistinguishesIntegrityFromCoverage(t *testing.T) {
	synthesis := Synthesis{
		References: []Paper{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}},
		Facts:      []Finding{{}, {}, {}},
		MethodComparison: []MethodComparison{
			{PaperID: "p1", Method: "method one"},
			{PaperID: "p2", Method: "method two", Datasets: []string{"dataset"}, Metrics: []string{"accuracy"}, Limitations: []string{"cost"}},
			{PaperID: "p3"},
		},
	}
	partial := AssessResearchQuality(synthesis, ExperimentDesign{})
	if partial.Status != QualityPartial || partial.Score >= 75 || len(partial.Gaps) == 0 {
		t.Fatalf("sparse but cited report was not marked partial: %+v", partial)
	}

	for index := range synthesis.MethodComparison {
		synthesis.MethodComparison[index].Method = "method"
		synthesis.MethodComparison[index].Datasets = []string{"dataset"}
		synthesis.MethodComparison[index].Metrics = []string{"accuracy"}
		synthesis.MethodComparison[index].Limitations = []string{"cost"}
	}
	design := ExperimentDesign{
		BaselineSuggestions: []Finding{{}, {}}, Datasets: []Finding{{}}, Metrics: []Finding{{}},
		AblationPlan: []Finding{{}}, EvaluationProtocol: []Finding{{}},
	}
	ready := AssessResearchQuality(synthesis, design)
	if ready.Status != QualityReady || ready.Score != 100 || len(ready.Gaps) != 0 {
		t.Fatalf("complete report was not marked ready: %+v", ready)
	}
}
