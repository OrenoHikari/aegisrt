// Package research implements the first CAPSuleRT vertical application while
// keeping all execution behind registered Scheduler capabilities.
package research

import "time"

// SearchRequest is the provider-neutral literature query.
type SearchRequest struct {
	Query      string `json:"query"`
	FromYear   int    `json:"from_year,omitempty"`
	ToYear     int    `json:"to_year,omitempty"`
	MaxResults int    `json:"max_results"`
}

// Paper is normalized metadata. Downstream capabilities never consume raw
// provider JSON or Atom XML.
type Paper struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Authors           []string `json:"authors"`
	Year              int      `json:"year"`
	Abstract          string   `json:"abstract"`
	Venue             string   `json:"venue,omitempty"`
	DOI               string   `json:"doi,omitempty"`
	ArxivID           string   `json:"arxiv_id,omitempty"`
	URL               string   `json:"url"`
	PDFURL            string   `json:"pdf_url,omitempty"`
	Provider          string   `json:"provider"`
	MetadataSources   []string `json:"metadata_sources,omitempty"`
	FullTextAvailable bool     `json:"full_text_available"`
}

// SearchResult is the stable output of literature.search.
type SearchResult struct {
	Query        string    `json:"query"`
	FromYear     int       `json:"from_year,omitempty"`
	ToYear       int       `json:"to_year,omitempty"`
	TotalResults int       `json:"total_results"`
	Papers       []Paper   `json:"papers"`
	Provider     string    `json:"provider"`
	Cached       bool      `json:"cached"`
	CompletedAt  time.Time `json:"completed_at"`
}

// Document is public paper content returned by a provider under size and URL
// safety limits.
type Document struct {
	Paper       Paper  `json:"paper"`
	ContentType string `json:"content_type"`
	SourceURL   string `json:"source_url"`
	Data        []byte `json:"-"`
}

// FetchResult describes either a fetched public artifact or an explicit
// evidence-grade downgrade when full text is unavailable.
type FetchResult struct {
	Paper         Paper  `json:"paper"`
	Query         string `json:"query"`
	Rank          int    `json:"rank"`
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
	FailureCode   string `json:"failure_code,omitempty"`
	Retryable     bool   `json:"retryable"`
	RequiredBytes int64  `json:"required_bytes,omitempty"`
	LimitBytes    int64  `json:"limit_bytes,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	Bytes         int    `json:"bytes,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	Artifact      string `json:"artifact,omitempty"`
}

// Page is normalized page text with a stable byte range in the concatenated
// extracted page stream.
type Page struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

// Section is a bounded parsed-paper section. Start and End are byte offsets in
// PaperDocument.Text; Evidence offsets are section-relative byte offsets.
type Section struct {
	ID                string `json:"id"`
	Heading           string `json:"heading"`
	NormalizedHeading string `json:"normalized_heading"`
	Text              string `json:"text"`
	PageStart         int    `json:"page_start"`
	PageEnd           int    `json:"page_end"`
	Start             int    `json:"start"`
	End               int    `json:"end"`
	Truncated         bool   `json:"truncated,omitempty"`
}

// ParserDiagnostics is a dashboard-ready summary of one bounded parse. It is
// part of the normal paper.parse result and telemetry rather than a parallel
// parser log.
type ParserDiagnostics struct {
	Selected            string   `json:"selected"`
	Attempted           []string `json:"attempted"`
	PageCount           int      `json:"page_count"`
	ExtractedCharacters int      `json:"extracted_characters"`
	DetectedSections    int      `json:"detected_sections"`
	DurationMillis      int64    `json:"duration_ms"`
	FallbackUsed        bool     `json:"fallback_used"`
	Truncated           bool     `json:"truncated"`
	WarningCount        int      `json:"warning_count"`
	Warnings            []string `json:"warnings,omitempty"`
}

// PaperDocument is the canonical, bounded output of paper.parse.
type PaperDocument struct {
	Paper       Paper             `json:"paper"`
	Query       string            `json:"query"`
	Abstract    string            `json:"abstract"`
	Parser      string            `json:"parser"`
	Fallback    bool              `json:"fallback"`
	Pages       []Page            `json:"pages"`
	Sections    []Section         `json:"sections"`
	Text        string            `json:"text"`
	References  []string          `json:"references,omitempty"`
	Characters  int               `json:"characters"`
	Truncated   bool              `json:"truncated"`
	Diagnostics ParserDiagnostics `json:"diagnostics"`
}

// ParsedPaper remains as a source-compatible name for the upgraded document
// model; there is one canonical paper representation, not a parallel model.
type ParsedPaper = PaperDocument

// CandidateFinding is proposed by an analyzer. Its evidence fields are
// untrusted until the deterministic EvidenceVerifier resolves them.
type CandidateFinding struct {
	Claim        string  `json:"claim"`
	ClaimType    string  `json:"claim_type"`
	PaperID      string  `json:"paper_id"`
	SectionID    string  `json:"section_id"`
	EvidenceText string  `json:"evidence_text"`
	Importance   string  `json:"importance,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
}

type FindingStatus string

const (
	FindingVerifiedSource FindingStatus = "VERIFIED_SOURCE"
	FindingSupported      FindingStatus = "SUPPORTED"
	FindingUnsupported    FindingStatus = "UNSUPPORTED"
)

// VerifiedFinding records the verifier outcome without mutating the model's
// candidate into a different claim or snippet.
type VerifiedFinding struct {
	Candidate  CandidateFinding `json:"candidate"`
	Status     FindingStatus    `json:"status"`
	EvidenceID string           `json:"evidence_id,omitempty"`
	Reason     string           `json:"reason,omitempty"`
}

// Evidence is the smallest traceable unit accepted by synthesis and reports.
type Evidence struct {
	ID            string        `json:"id"`
	Source        string        `json:"source"`
	PaperID       string        `json:"paper_id"`
	Claim         string        `json:"claim"`
	Section       string        `json:"section"`
	SectionID     string        `json:"section_id"`
	Start         int           `json:"start"`
	End           int           `json:"end"`
	Snippet       string        `json:"snippet"`
	ProducingTask string        `json:"producing_task"`
	Status        FindingStatus `json:"status"`
}

// PaperAnalysis is a structured interpretation grounded in Evidence.
type PaperAnalysis struct {
	Paper             Paper              `json:"paper"`
	Query             string             `json:"query"`
	ResearchQuestion  string             `json:"research_question"`
	Problem           string             `json:"problem"`
	Method            string             `json:"method"`
	KeyContributions  []string           `json:"key_contributions"`
	Datasets          []string           `json:"datasets"`
	Metrics           []string           `json:"metrics"`
	Experiments       []string           `json:"experiments"`
	MainResults       []string           `json:"main_results"`
	Limitations       []string           `json:"limitations"`
	CandidateFindings []CandidateFinding `json:"candidate_findings"`
	Findings          []VerifiedFinding  `json:"findings"`
	Evidence          []Evidence         `json:"evidence"`
	Usage             *TokenUsage        `json:"usage,omitempty"`
	LLMCalls          int                `json:"llm_calls"`
	LLMFailures       int                `json:"llm_failures"`
}

// TokenUsage is populated only when the compatible LLM endpoint returns
// authoritative usage values.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// FindingKind keeps paper facts, cross-paper interpretation, and experimental
// proposals semantically separate.
type FindingKind string

const (
	FindingFact      FindingKind = "FACT"
	FindingInference FindingKind = "INFERENCE"
	FindingProposal  FindingKind = "PROPOSAL"
)

// Finding is one reportable statement and its evidence linkage.
type Finding struct {
	Kind        FindingKind `json:"kind"`
	Statement   string      `json:"statement"`
	EvidenceIDs []string    `json:"evidence_ids,omitempty"`
}

// MethodComparison preserves paper identities instead of flattening summaries.
type MethodComparison struct {
	PaperID     string   `json:"paper_id"`
	Method      string   `json:"method"`
	Datasets    []string `json:"datasets"`
	Metrics     []string `json:"metrics"`
	Limitations []string `json:"limitations"`
}

// Synthesis is the cross-paper, citation-safe research result.
type Synthesis struct {
	Goal               string             `json:"goal"`
	QueryHistory       []string           `json:"query_history"`
	RetrievedPaperIDs  []string           `json:"retrieved_paper_ids"`
	References         []Paper            `json:"references"`
	MetadataOnlyPapers []Paper            `json:"metadata_only_papers,omitempty"`
	Evidence           []Evidence         `json:"evidence"`
	Facts              []Finding          `json:"facts"`
	Inferences         []Finding          `json:"inferences"`
	ResearchDirections []string           `json:"research_directions"`
	MethodComparison   []MethodComparison `json:"method_comparison"`
	Datasets           []string           `json:"datasets"`
	Metrics            []string           `json:"metrics"`
	Limitations        []string           `json:"limitations"`
}

// ExperimentDesign contains suggestions, not claims about published results.
type ExperimentDesign struct {
	Goal                string    `json:"goal"`
	Hypothesis          Finding   `json:"hypothesis"`
	BaselineSuggestions []Finding `json:"baseline_suggestions"`
	Datasets            []Finding `json:"datasets"`
	Metrics             []Finding `json:"metrics"`
	AblationPlan        []Finding `json:"ablation_plan"`
	EvaluationProtocol  []Finding `json:"evaluation_protocol"`
	ExpectedRisks       []Finding `json:"expected_risks"`
}

// ResearchQuality separates citation integrity from answer completeness. A
// citation-closed report can still be too sparse to support a decision.
type ResearchQuality struct {
	Status                 string   `json:"status"`
	Score                  int      `json:"score"`
	ReferenceCount         int      `json:"reference_count"`
	MethodRows             int      `json:"method_rows"`
	DatasetRows            int      `json:"dataset_rows"`
	MetricRows             int      `json:"metric_rows"`
	LimitationRows         int      `json:"limitation_rows"`
	CompleteComparisonRows int      `json:"complete_comparison_rows"`
	ExperimentReady        bool     `json:"experiment_ready"`
	Gaps                   []string `json:"gaps,omitempty"`
}

// RunMetrics is the first evaluation record for research demonstrations.
type RunMetrics struct {
	TotalTasks                 int     `json:"total_tasks"`
	SuccessfulTasks            int     `json:"successful_tasks"`
	FailedTasks                int     `json:"failed_tasks"`
	TaskSuccessRate            float64 `json:"task_success_rate"`
	ReplanCount                int     `json:"replan_count"`
	TotalIterations            int     `json:"total_iterations"`
	Duration                   string  `json:"duration"`
	DurationMillis             int64   `json:"duration_ms"`
	RetrievedPapers            int     `json:"retrieved_papers"`
	UsablePapers               int     `json:"usable_papers"`
	AnalyzedPapers             int     `json:"analyzed_papers"`
	CandidateFindings          int     `json:"candidate_findings"`
	VerifiedFindings           int     `json:"verified_findings"`
	SupportedFindings          int     `json:"supported_findings"`
	UnsupportedFindings        int     `json:"unsupported_findings"`
	EvidenceVerificationRate   float64 `json:"evidence_verification_rate"`
	EvidenceBackedClaims       int     `json:"evidence_backed_claims"`
	UnsupportedClaimCount      int     `json:"unsupported_claim_count"`
	CitationClosureRate        float64 `json:"citation_closure_rate"`
	HallucinatedReferenceCount int     `json:"hallucinated_reference_count"`
	FactWithEvidenceRatio      float64 `json:"fact_with_evidence_ratio"`
	UnsupportedFactCount       int     `json:"unsupported_fact_count"`
	DuplicatedReferences       int     `json:"duplicated_references"`
	SearchIterations           int     `json:"search_iterations"`
	RepeatedPlanCount          int     `json:"repeated_plan_count"`
	InvalidPlanCount           int     `json:"invalid_plan_count"`
	MaxLoopAbortCount          int     `json:"max_loop_abort_count"`
	RecoverySuccess            bool    `json:"recovery_success"`
	RecoveryRate               float64 `json:"recovery_rate"`
	InputTokens                *int    `json:"input_tokens"`
	OutputTokens               *int    `json:"output_tokens"`
}
