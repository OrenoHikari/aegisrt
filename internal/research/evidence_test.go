package research

import (
	"errors"
	"testing"
)

func validEvidence() Evidence {
	snippet := "RESULT: The method reports a measured result."
	return Evidence{
		ID: "paper-1-e1", Source: "https://arxiv.org/abs/paper-1", PaperID: "paper-1",
		Claim: "The method reports a measured result.", Section: "Results",
		SectionID: "results-1", Start: 0, End: len(snippet), Snippet: snippet,
		ProducingTask: "analyze-1", Status: FindingSupported,
	}
}

func TestEvidenceStoreValidEvidence(t *testing.T) {
	store := NewEvidenceStore()
	evidence := validEvidence()
	if err := store.Add(evidence); err != nil {
		t.Fatal(err)
	}
	got, ok := store.Get(evidence.ID)
	if !ok || got.Claim != evidence.Claim || len(store.All()) != 1 {
		t.Fatalf("evidence not stored: %+v %t", got, ok)
	}
}

func TestEvidenceStoreRejectsMissingSource(t *testing.T) {
	evidence := validEvidence()
	evidence.Source = ""
	if err := NewEvidenceStore().Add(evidence); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("expected invalid evidence, got %v", err)
	}
}

func TestEvidenceStoreRejectsDuplicate(t *testing.T) {
	store := NewEvidenceStore()
	evidence := validEvidence()
	if err := store.Add(evidence); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(evidence); !errors.Is(err, ErrDuplicateEvidence) {
		t.Fatalf("expected duplicate evidence, got %v", err)
	}
}
