package agent

import "time"

// OutputVerification describes a successful committed-output
// integrity verification.
type OutputVerification struct {
	Method         string    `json:"method"`
	ManifestSHA256 string    `json:"manifest_sha256,omitempty"`
	VerifiedAt     time.Time `json:"verified_at"`
	FileCount      int       `json:"file_count"`
	TotalBytes     uint64    `json:"total_bytes"`
}

// DependencyOutput describes one verified, committed upstream result.
type DependencyOutput struct {
	AgentID       string `json:"agent_id"`
	TransactionID string `json:"transaction_id"`
	CommitPath    string `json:"commit_path"`
	ManifestPath  string `json:"manifest_path"`
	FileCount     int    `json:"file_count"`
	TotalBytes    uint64 `json:"total_bytes"`

	Verified           bool      `json:"verified"`
	VerificationMethod string    `json:"verification_method"`
	ManifestSHA256     string    `json:"manifest_sha256,omitempty"`
	VerifiedAt         time.Time `json:"verified_at"`
}
