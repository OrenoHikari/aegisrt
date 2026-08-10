package dashboard

import (
	"path/filepath"
	"testing"
)

func TestScanVerifiedArtifactsRejectsUnverifiedAndOutsidePaths(t *testing.T) {
	root := t.TempDir()
	commitPath, err := writeFakeArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := ScanVerifiedArtifacts(root, nil)
	if err != nil || empty.Evidence.CandidateCount != 0 {
		t.Fatalf("unverified output was visible: %+v err=%v", empty, err)
	}
	outside := DashboardEvent{Kind: "runtime.output.verified", Data: map[string]any{
		"output_verified": true, "output_commit_path": filepath.Join(t.TempDir(), "outside"),
	}}
	empty, err = ScanVerifiedArtifacts(root, []DashboardEvent{outside})
	if err != nil || empty.Evidence.CandidateCount != 0 {
		t.Fatalf("outside output was visible: %+v err=%v", empty, err)
	}
	verified := DashboardEvent{Kind: "runtime.output.verified", Data: map[string]any{
		"output_verified": true, "output_commit_path": commitPath,
	}, TaskID: "analysis"}
	snapshot, err := ScanVerifiedArtifacts(root, []DashboardEvent{verified})
	if err != nil || snapshot.Evidence.SupportedCount != 1 || snapshot.Evidence.RejectedCount != 1 {
		t.Fatalf("verified output was not visible: %+v err=%v", snapshot, err)
	}
	if got := snapshot.Evidence.Findings[0].TaskID; got != "paper-analyze-1" {
		t.Fatalf("authoritative producing task = %q", got)
	}
	rejected := snapshot.Evidence.Findings[1]
	if rejected.TaskID != "analysis" || rejected.ReasonCode != "CLAIM_NOT_SUPPORTED" || rejected.Reason == "" {
		t.Fatalf("rejection inspection metadata = %+v", rejected)
	}
}
