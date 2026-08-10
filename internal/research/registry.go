package research

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
	"aegisrt/internal/resource"
	"aegisrt/internal/scheduler"
)

// RegistrationOptions configure trusted research worker Jobs.
type RegistrationOptions struct {
	Executable               string
	Provider                 string
	MockScenario             string
	ArxivEndpoint            string
	CrossrefEndpoint         string
	CacheDirectory           string
	CacheTTL                 time.Duration
	DisableCache             bool
	ParserMode               string
	PythonExecutable         string
	PythonParserScript       string
	AnalysisMode             string
	ClaimSupportMode         string
	MaxAnalysisCallsPerPaper int
	MaxContextBytes          int
	MaxPDFBytes              int64
	LLMConfigFile            string
	TaskTimeout              time.Duration
}

// Registrations returns the research-specific Capability adapters for the same
// generic Registry used by every CAPSuleRT Agent task.
func Registrations(options RegistrationOptions) ([]orchestrator.Registration, error) {
	executable := strings.TrimSpace(options.Executable)
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve research worker executable: %w", err)
		}
	}
	taskTimeout := options.TaskTimeout
	if taskTimeout <= 0 {
		taskTimeout = 45 * time.Second
	}
	maxPDFBytes := options.MaxPDFBytes
	if maxPDFBytes <= 0 {
		maxPDFBytes = DefaultPaperDownloadLimitBytes
	}
	if err := ValidatePaperDownloadLimit(maxPDFBytes); err != nil {
		return nil, err
	}
	build := func(capability, action string, demand scheduler.Demand) orchestrator.JobFactory {
		return func(ctx context.Context, task planner.Task) (scheduler.Job, error) {
			if err := validateResearchTaskArguments(task); err != nil {
				return scheduler.Job{}, err
			}
			encoded, err := json.Marshal(task.Arguments)
			if err != nil {
				return scheduler.Job{}, err
			}
			acb := agent.New(task.ID, capability, executable, []string{"internal-research-worker", "--action", action})
			acb.Environment = map[string]string{
				"CAPSULE_TASK_ID":                     task.ID,
				"CAPSULE_TASK_NAME":                   task.Name,
				"CAPSULE_TASK_DESCRIPTION":            task.Description,
				"CAPSULE_TASK_CAPABILITY":             capability,
				"CAPSULE_TASK_ARGUMENTS_JSON":         string(encoded),
				"CAPSULE_RESEARCH_PROVIDER":           strings.TrimSpace(options.Provider),
				"CAPSULE_RESEARCH_SCENARIO":           strings.TrimSpace(options.MockScenario),
				"CAPSULE_RESEARCH_ARXIV_ENDPOINT":     strings.TrimSpace(options.ArxivEndpoint),
				"CAPSULE_RESEARCH_CROSSREF_ENDPOINT":  strings.TrimSpace(options.CrossrefEndpoint),
				"CAPSULE_RESEARCH_CACHE_DIR":          strings.TrimSpace(options.CacheDirectory),
				"CAPSULE_RESEARCH_CACHE_TTL":          options.CacheTTL.String(),
				"CAPSULE_RESEARCH_NO_CACHE":           fmt.Sprintf("%t", options.DisableCache),
				"CAPSULE_RESEARCH_PARSER_MODE":        strings.TrimSpace(options.ParserMode),
				"CAPSULE_RESEARCH_PYTHON":             strings.TrimSpace(options.PythonExecutable),
				"CAPSULE_RESEARCH_PYTHON_PARSER":      strings.TrimSpace(options.PythonParserScript),
				"CAPSULE_RESEARCH_ANALYSIS_MODE":      strings.TrimSpace(options.AnalysisMode),
				"CAPSULE_RESEARCH_CLAIM_SUPPORT_MODE": strings.TrimSpace(options.ClaimSupportMode),
				"CAPSULE_RESEARCH_MAX_ANALYSIS_CALLS": strconv.Itoa(options.MaxAnalysisCallsPerPaper),
				"CAPSULE_RESEARCH_MAX_CONTEXT_BYTES":  strconv.Itoa(options.MaxContextBytes),
				"CAPSULE_RESEARCH_MAX_PDF_BYTES":      strconv.FormatInt(maxPDFBytes, 10),
				"CAPSULE_LLM_CONFIG_FILE":             strings.TrimSpace(options.LLMConfigFile),
			}
			acb.Resources = resource.Spec{CPUQuotaPercent: 75, MemoryMaxBytes: 256 * 1024 * 1024, PidsMax: 16}
			return scheduler.Job{
				Agent: acb, Context: ctx, Timeout: taskTimeout, Demand: demand,
				DependsOn: append([]string(nil), task.DependsOn...),
			}, nil
		}
	}

	stringField := func(description string, required bool) planner.ArgumentField {
		return planner.ArgumentField{Type: planner.ArgumentString, Description: description, Required: required}
	}
	numberField := func(description string, required bool) planner.ArgumentField {
		return planner.ArgumentField{Type: planner.ArgumentNumber, Description: description, Required: required}
	}
	timeoutSeconds := int(taskTimeout.Seconds())
	workerSafety := planner.SafetyMetadata{ReadOnly: true, Permission: "verified_dependencies.read"}
	networkSafety := planner.SafetyMetadata{ReadOnly: true, Permission: "public_academic_network.read"}

	return []orchestrator.Registration{
		{
			Capability: planner.Capability{
				Name: "literature.search", Description: "Search normalized scholarly metadata through the configured public LiteratureProvider. Build one coverage-oriented academic query that includes every comparison axis explicitly requested by the user; later observations may justify a different query in a revised plan.",
				InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
					"query":       stringField("Academic search query", true),
					"from_year":   numberField("Optional inclusive start year", false),
					"to_year":     numberField("Optional inclusive end year", false),
					"max_results": numberField("Maximum results from 1 to 10", true),
				}},
				OutputDescription: "Provider-neutral paper metadata and result count.",
				OutputSchema:      map[string]string{"query": "string", "total_results": "number", "papers": "array"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: networkSafety,
			},
			Build: build("literature.search", "literature_search", scheduler.Demand{CPU: 0.1, Memory: 0.1, IO: 0.5}),
		},
		{
			Capability: planner.Capability{
				Name: "paper.fetch", Description: "Fetch one publicly available paper selected from a verified literature.search result.",
				InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
					"rank":     numberField("One-based result rank", false),
					"paper_id": stringField("Optional exact identifier from the search result", false),
				}},
				OutputDescription: "Fetched artifact metadata or a structured unavailable/size-budget observation.",
				OutputSchema: map[string]string{
					"paper": "object", "available": "boolean", "reason": "string", "failure_code": "string",
					"retryable": "boolean", "required_bytes": "number", "limit_bytes": "number",
				},
				TimeoutSeconds: timeoutSeconds, ExecutionType: "go_worker", Safety: networkSafety, RequiresDependency: true,
			},
			Build: build("paper.fetch", "paper_fetch", scheduler.Demand{CPU: 0.1, Memory: 0.2, IO: 0.8}),
		},
		{
			Capability: planner.Capability{
				Name: "paper.parse", Description: "Parse one verified fetched paper into bounded sections and references.",
				InputSchema:       planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{}},
				OutputDescription: "Metadata, abstract, bounded section text, headings, and references.",
				OutputSchema:      map[string]string{"paper": "object", "sections": "array", "truncated": "boolean"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: workerSafety, RequiresDependency: true,
			},
			Build: build("paper.parse", "paper_parse", scheduler.Demand{CPU: 0.5, Memory: 0.5, IO: 0.4}),
		},
		{
			Capability: planner.Capability{
				Name: "paper.analyze", Description: "Extract structured paper findings only when each claim can retain source evidence.",
				InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
					"question": stringField("Research question applied to the paper", true),
				}},
				OutputDescription: "Problem, method, contributions, datasets, metrics, results, limitations, and Evidence.",
				OutputSchema:      map[string]string{"problem": "string", "method": "string", "evidence": "array"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: workerSafety, RequiresDependency: true,
			},
			Build: build("paper.analyze", "paper_analyze", scheduler.Demand{CPU: 0.5, Memory: 0.4, IO: 0.2}),
		},
		{
			Capability: planner.Capability{
				Name: "research.synthesize", Description: "Compare multiple evidence-backed paper analyses and identify directions, incompatibilities, trends, datasets, metrics, and limitations.",
				InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
					"goal": stringField("Original research goal", true),
				}},
				OutputDescription: "Cross-paper synthesis whose FACT and INFERENCE findings cite Evidence.",
				OutputSchema:      map[string]string{"facts": "array", "inferences": "array", "references": "array", "evidence": "array"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: workerSafety, RequiresDependency: true,
			},
			Build: build("research.synthesize", "research_synthesize", scheduler.Demand{CPU: 0.6, Memory: 0.5, IO: 0.2}),
		},
		{
			Capability: planner.Capability{
				Name: "experiment.design", Description: "Generate explicitly labeled PROPOSAL items from verified literature synthesis.",
				InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
					"goal":        stringField("Original research goal", true),
					"constraints": stringField("Optional experimental constraints", false),
				}},
				OutputDescription: "Hypothesis, baselines, datasets, metrics, ablations, protocol, and risks, all labeled PROPOSAL.",
				OutputSchema:      map[string]string{"hypothesis": "object", "ablation_plan": "array", "expected_risks": "array"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: workerSafety, RequiresDependency: true,
			},
			Build: build("experiment.design", "experiment_design", scheduler.Demand{CPU: 0.3, Memory: 0.3, IO: 0.1}),
		},
		{
			Capability: planner.Capability{
				Name: "research.report", Description: "Create a citation-validated Markdown report from synthesis and experiment design outputs.",
				InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
					"goal": stringField("Original research goal", true),
				}},
				OutputDescription: "report.md with FACT, INFERENCE, PROPOSAL, Evidence ledger, retrieved references, and a separate answer-completeness assessment.",
				OutputSchema:      map[string]string{"report_file": "string", "references": "number", "evidence_backed_claims": "number", "quality": "object"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: workerSafety, RequiresDependency: true,
			},
			Build: build("research.report", "research_report", scheduler.Demand{CPU: 0.2, Memory: 0.3, IO: 0.3}),
		},
	}, nil
}

func validateResearchTaskArguments(task planner.Task) error {
	for _, name := range []string{"from_year", "to_year", "max_results", "rank"} {
		value, exists := task.Arguments[name]
		if !exists {
			continue
		}
		number, ok := value.(float64)
		if ok && math.Trunc(number) != number {
			return fmt.Errorf("task %s argument %s must be an integer", task.ID, name)
		}
	}
	if task.Capability == "literature.search" {
		_, err := validateSearchRequest(SearchRequest{
			Query: stringArg(task.Arguments, "query"), FromYear: intArg(task.Arguments, "from_year"),
			ToYear: intArg(task.Arguments, "to_year"), MaxResults: intArg(task.Arguments, "max_results"),
		})
		return err
	}
	if task.Capability == "paper.fetch" {
		rank := intArg(task.Arguments, "rank")
		paperID := stringArg(task.Arguments, "paper_id")
		if rank == 0 && paperID == "" {
			return fmt.Errorf("paper.fetch requires rank or paper_id")
		}
		if rank < 0 || rank > MaximumSearchResults {
			return fmt.Errorf("paper.fetch rank must be between 1 and %d", MaximumSearchResults)
		}
	}
	return nil
}
