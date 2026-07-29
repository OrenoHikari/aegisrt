package agent

// OutputState describes the transactional output lifecycle.
type OutputState string

const (
	OutputStateNone      OutputState = ""
	OutputStateStaging   OutputState = "STAGING"
	OutputStateCommitted OutputState = "COMMITTED"
	OutputStateDiscarded OutputState = "DISCARDED"
	OutputStateRetained  OutputState = "RETAINED"
)
