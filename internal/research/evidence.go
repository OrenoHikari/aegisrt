package research

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidEvidence      = errors.New("research evidence is missing a required source field")
	ErrDuplicateEvidence    = errors.New("research evidence is duplicated")
	ErrInvalidCitation      = errors.New("research citation does not reference a retrieved paper")
	ErrInsufficientEvidence = errors.New("insufficient evidence for research synthesis")
)

// EvidenceStore is deliberately in-memory and serializable through normal
// task outputs; it is not a second database.
type EvidenceStore struct {
	byID    map[string]Evidence
	byClaim map[string]string
	ordered []Evidence
}

func NewEvidenceStore() *EvidenceStore {
	return &EvidenceStore{byID: make(map[string]Evidence), byClaim: make(map[string]string)}
}

func (s *EvidenceStore) Add(evidence Evidence) error {
	evidence.ID = strings.TrimSpace(evidence.ID)
	evidence.Source = strings.TrimSpace(evidence.Source)
	evidence.PaperID = strings.TrimSpace(evidence.PaperID)
	evidence.Claim = strings.TrimSpace(evidence.Claim)
	evidence.Section = strings.TrimSpace(evidence.Section)
	evidence.SectionID = strings.TrimSpace(evidence.SectionID)
	evidence.Snippet = strings.TrimSpace(evidence.Snippet)
	evidence.ProducingTask = strings.TrimSpace(evidence.ProducingTask)
	if evidence.ID == "" || evidence.Source == "" || evidence.PaperID == "" || evidence.Claim == "" ||
		evidence.Section == "" || evidence.SectionID == "" || evidence.Snippet == "" || evidence.ProducingTask == "" ||
		evidence.Start < 0 || evidence.End <= evidence.Start || evidence.End-evidence.Start != len(evidence.Snippet) ||
		(evidence.Status != FindingVerifiedSource && evidence.Status != FindingSupported) {
		return fmt.Errorf("%w: %+v", ErrInvalidEvidence, evidence)
	}
	claimKey := evidence.PaperID + "\x00" + strings.ToLower(evidence.Claim)
	if _, exists := s.byID[evidence.ID]; exists {
		return fmt.Errorf("%w: id %s", ErrDuplicateEvidence, evidence.ID)
	}
	if prior, exists := s.byClaim[claimKey]; exists {
		return fmt.Errorf("%w: claim already stored as %s", ErrDuplicateEvidence, prior)
	}
	s.byID[evidence.ID] = evidence
	s.byClaim[claimKey] = evidence.ID
	s.ordered = append(s.ordered, evidence)
	return nil
}

func (s *EvidenceStore) Get(id string) (Evidence, bool) {
	evidence, exists := s.byID[id]
	return evidence, exists
}

func (s *EvidenceStore) All() []Evidence {
	return append([]Evidence(nil), s.ordered...)
}
