package agent

// DependencyOutput describes one committed upstream Agent result.
type DependencyOutput struct {
	AgentID       string `json:"agent_id"`
	TransactionID string `json:"transaction_id"`
	CommitPath    string `json:"commit_path"`
	ManifestPath  string `json:"manifest_path"`
	FileCount     int    `json:"file_count"`
	TotalBytes    uint64 `json:"total_bytes"`
}
