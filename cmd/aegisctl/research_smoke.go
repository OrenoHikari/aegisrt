package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegisrt/internal/llm"
	"aegisrt/internal/research"
)

func runResearchSmoke(arguments []string) error {
	flags := flag.NewFlagSet("agent research-smoke", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	query := flags.String("query", "referring expression counting", "small real arXiv search query")
	goal := flags.String("task", "调研 Referring Expression Counting 的代表性方法，并给出一个有证据依据的后续实验方案。", "real end-to-end smoke goal")
	python := flags.String("python", defaultResearchPython(), "Python interpreter containing pypdf")
	root := flags.String("root", filepath.Join("var", "research-smoke"), "smoke run output directory")
	maxPapers := flags.Int("max-papers", 3, "maximum papers in the end-to-end smoke run")
	timeout := flags.Duration("timeout", 10*time.Minute, "hard timeout for each smoke phase")
	noCache := flags.Bool("no-cache", true, "disable literature query cache for the full run")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*query) == "" || strings.TrimSpace(*goal) == "" || *timeout <= 0 {
		return fmt.Errorf("research smoke query, task, and positive timeout are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	parserStatus := research.DetectPythonPDFParser(ctx, *python, 10*time.Second)
	if !parserStatus.Available {
		fmt.Printf("[PDF PARSER SMOKE] FAIL: %s\n", parserStatus.Reason)
		return fmt.Errorf("pypdf parser unavailable; run make research-python-setup")
	}
	fmt.Printf("[PDF PARSER DEPENDENCY] PASS: %s %s\n", parserStatus.Parser, parserStatus.Version)

	provider, err := research.NewArxivProvider(research.ArxivOptions{})
	if err != nil {
		return err
	}
	papers, err := provider.Search(ctx, research.SearchRequest{Query: *query, MaxResults: 3})
	if err != nil {
		fmt.Printf("[REAL PROVIDER SMOKE] FAIL: %v\n", err)
		return err
	}
	if len(papers) == 0 {
		fmt.Println("[REAL PROVIDER SMOKE] FAIL: arXiv returned no papers")
		return fmt.Errorf("arXiv returned no papers")
	}
	fmt.Printf("[REAL PROVIDER SMOKE] PASS: %d papers; first=%s [%s]\n", len(papers), papers[0].Title, papers[0].ID)

	var fetched research.Document
	var fetchErr error
	for _, paper := range papers {
		if !paper.FullTextAvailable {
			continue
		}
		fetched, fetchErr = provider.Fetch(ctx, paper)
		if fetchErr == nil {
			break
		}
	}
	if len(fetched.Data) == 0 {
		if fetchErr == nil {
			fetchErr = research.ErrFullTextUnavailable
		}
		fmt.Printf("[REAL PDF DOWNLOAD] FAIL: %v\n", fetchErr)
		return fmt.Errorf("no searchable paper PDF could be downloaded: %w", fetchErr)
	}
	fmt.Printf("[REAL PDF DOWNLOAD] PASS: %d bytes from %s\n", len(fetched.Data), fetched.SourceURL)
	parserScript, err := filepath.Abs(filepath.Join("worker", "python", "paper_parser.py"))
	if err != nil {
		return err
	}
	parser, err := research.NewPaperParserWithPython("python", *python, parserScript, minDuration(*timeout, 60*time.Second))
	if err != nil {
		return err
	}
	document, err := parser.Parse(ctx, research.FetchResult{
		Paper: fetched.Paper, Query: *query, Available: true, ContentType: fetched.ContentType, SourceURL: fetched.SourceURL,
	}, fetched.Data)
	if err != nil {
		fmt.Printf("[PDF PARSER SMOKE] FAIL: %v\n", err)
		return err
	}
	fmt.Printf("[PDF PARSER SMOKE] PASS: parser=%s pages=%d sections=%d chars=%d duration_ms=%d fallback=%t truncated=%t warnings=%d\n",
		document.Diagnostics.Selected, document.Diagnostics.PageCount, document.Diagnostics.DetectedSections,
		document.Diagnostics.ExtractedCharacters, document.Diagnostics.DurationMillis, document.Diagnostics.FallbackUsed,
		document.Diagnostics.Truncated, document.Diagnostics.WarningCount)

	config, err := llm.LoadConfig()
	if err != nil {
		return err
	}
	config.Timeout = minDuration(*timeout, 60*time.Second)
	if err := llm.ValidateConfig(config, llm.ConfigRequirements{RequireExplicitEndpoint: true, RequireCredential: true}); err != nil {
		fmt.Printf("[LLM CONNECTIVITY] SKIPPED: %v\n", err)
		fmt.Println("[REAL PAPER ANALYSIS] SKIPPED: real LLM configuration is unavailable")
		fmt.Println("[REAL END-TO-END RESEARCH] SKIPPED: real LLM configuration is unavailable")
		printLLMConfigurationTemplate()
		return nil
	}
	connectivity, err := llm.CheckOpenAICompatibleConnectivity(ctx, config)
	if err != nil {
		fmt.Printf("[LLM CONNECTIVITY] FAIL: %v\n", err)
		return err
	}
	fmt.Printf("[LLM CONNECTIVITY] PASS: endpoint=%s model=%s structured=%t latency_ms=%d\n",
		connectivity.Endpoint, connectivity.Model, connectivity.StructuredResponse, connectivity.LatencyMillis)
	client, err := llm.NewOpenAICompatibleClient(config)
	if err != nil {
		return err
	}
	analysisBudget := &llm.BudgetClient{Client: client, MaxCalls: 1}
	analysis, err := (research.LLMPaperAnalyzer{
		Client: analysisBudget, Support: research.DeterministicClaimSupport{}, MaxContextBytes: research.MaximumAnalysisContextBytes,
	}).Analyze(ctx, document, *goal, "research-smoke-paper")
	if err != nil {
		fmt.Printf("[REAL PAPER ANALYSIS] FAIL: %v\n", err)
		return err
	}
	if len(analysis.Evidence) == 0 {
		fmt.Println("[REAL PAPER ANALYSIS] FAIL: no candidate evidence could be source-verified")
		return fmt.Errorf("real paper analysis produced no source-verified evidence")
	}
	supported := 0
	for _, evidence := range analysis.Evidence {
		if evidence.Status == research.FindingSupported {
			supported++
		}
	}
	fmt.Printf("[REAL PAPER ANALYSIS] PASS: candidates=%d source_verified=%d supported=%d calls=%d\n",
		len(analysis.CandidateFindings), len(analysis.Evidence), supported, analysisBudget.Stats().Calls)
	fullArguments := []string{
		"--task", *goal, "--root", *root, "--analysis-mode", "llm", "--paper-parser", "python", "--python", *python,
		"--provider", "arxiv", "--max-papers", fmt.Sprint(*maxPapers), "--max-llm-calls", "16", "--loop-timeout", timeout.String(),
	}
	if *noCache {
		fullArguments = append(fullArguments, "--no-cache")
	}
	if err := runResearchAgent(fullArguments); err != nil {
		fmt.Printf("[REAL END-TO-END RESEARCH] FAIL: %v\n", err)
		return err
	}
	fmt.Println("[REAL END-TO-END RESEARCH] PASS")
	return nil
}
