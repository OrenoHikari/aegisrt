package research

import (
	"context"
	"testing"
	"time"

	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
)

func TestResearchCapabilityRegistry(t *testing.T) {
	registrations, err := Registrations(RegistrationOptions{Executable: "/fixed/capsulectl", Provider: "mock", MockScenario: MockScenarioNormal})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := orchestrator.NewRegistry(registrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"literature.search", "paper.fetch", "paper.parse", "paper.analyze",
		"research.synthesize", "experiment.design", "research.report",
	} {
		capability, ok := registry.Lookup(name)
		if !ok || capability.Description == "" || capability.ExecutionType == "" || capability.TimeoutSeconds <= 0 {
			t.Fatalf("capability %s is not fully registered: %+v", name, capability)
		}
	}
	task := planner.Task{
		ID: "search-1", Name: "search", Description: "search literature", Capability: "literature.search",
		Arguments: map[string]any{"query": "visual counting", "max_results": 3},
	}
	job, err := registry.Build(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if job.Agent.Command != "/fixed/capsulectl" || job.Agent.Role != "literature.search" || job.Agent.Environment["CAPSULE_RESEARCH_PROVIDER"] != "mock" {
		t.Fatalf("research task did not become a trusted Scheduler job: %+v", job.Agent)
	}
	if len(job.Agent.Args) != 3 || job.Agent.Args[0] != "internal-research-worker" {
		t.Fatalf("unexpected fixed worker command: %v", job.Agent.Args)
	}
}

func TestResearchRegistrationPropagatesStage4ModesAndNoCache(t *testing.T) {
	registrations, err := Registrations(RegistrationOptions{
		Executable: "/fixed/capsulectl", Provider: "multi", CacheDirectory: "/fixed/cache",
		CacheTTL: 2 * time.Hour, DisableCache: true, ParserMode: "python",
		PythonParserScript: "/fixed/parser.py", AnalysisMode: "llm", ClaimSupportMode: "deterministic",
		MaxPDFBytes: 32 * 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := orchestrator.NewRegistry(registrations)
	job, err := registry.Build(context.Background(), planner.Task{
		ID: "analyze", Name: "analyze", Description: "analyze", Capability: "paper.analyze",
		Arguments: map[string]any{"question": "question"}, DependsOn: []string{"parse"},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := job.Agent.Environment
	if env["CAPSULE_RESEARCH_NO_CACHE"] != "true" || env["CAPSULE_RESEARCH_CACHE_TTL"] != "2h0m0s" || env["CAPSULE_RESEARCH_PARSER_MODE"] != "python" || env["CAPSULE_RESEARCH_ANALYSIS_MODE"] != "llm" || env["CAPSULE_RESEARCH_MAX_PDF_BYTES"] != "33554432" {
		t.Fatalf("Stage 4 configuration was not propagated: %+v", env)
	}

	t.Setenv("CAPSULE_RESEARCH_PROVIDER", "arxiv")
	t.Setenv("CAPSULE_RESEARCH_CACHE_DIR", t.TempDir())
	t.Setenv("CAPSULE_RESEARCH_NO_CACHE", "true")
	t.Setenv("CAPSULE_RESEARCH_CACHE_TTL", "1h")
	provider, err := providerFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if _, cached := provider.(*CachingProvider); cached {
		t.Fatal("--no-cache configuration still wrapped the provider")
	}
}

func TestResearchRegistryRejectsInvalidArgumentsBeforeExecution(t *testing.T) {
	registrations, _ := Registrations(RegistrationOptions{Executable: "/fixed/capsulectl", Provider: "mock"})
	registry, _ := orchestrator.NewRegistry(registrations)
	task := planner.Task{
		ID: "search-1", Name: "search", Description: "bad search", Capability: "literature.search",
		Arguments: map[string]any{"query": "visual counting", "max_results": 11},
	}
	if _, err := registry.Build(context.Background(), task); err == nil {
		t.Fatal("invalid search arguments were accepted")
	}
	unknown := task
	unknown.Capability = "shell.run"
	if _, err := registry.Build(context.Background(), unknown); err == nil {
		t.Fatal("unknown research capability was accepted")
	}
}
