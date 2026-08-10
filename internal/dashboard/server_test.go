package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDashboardAPI(t *testing.T) {
	controller := newTestController(t, fakeSuccessfulExecutor(t, false))
	server, err := NewServer(controller)
	if err != nil {
		t.Fatal(err)
	}

	invalid := httptest.NewRecorder()
	server.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"goal":"","extra":true}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d", invalid.Code)
	}

	requestBody := bytes.NewBufferString(`{"goal":"research safely","mode":"mock","scenario":"normal","max_pdf_mb":48}`)
	created := httptest.NewRecorder()
	server.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/runs", requestBody))
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	var view RunView
	if err := json.Unmarshal(created.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.MaxPDFMB != 48 {
		t.Fatalf("selected PDF budget was not returned: %+v", view)
	}
	view = waitForTerminal(t, controller, view.ID)

	for _, endpoint := range []string{"", "/plan", "/papers", "/evidence", "/runtime"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs/"+view.ID+endpoint, nil))
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status = %d: %s", endpoint, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Errorf("GET %s may be cached: %q", endpoint, response.Header().Get("Cache-Control"))
		}
	}
	report := httptest.NewRecorder()
	server.ServeHTTP(report, httptest.NewRequest(http.MethodGet, "/api/runs/"+view.ID+"/report", nil))
	if report.Code != http.StatusOK || !strings.Contains(report.Body.String(), "Verified report") {
		t.Fatalf("report response = %d %q", report.Code, report.Body.String())
	}
	evidence := httptest.NewRecorder()
	server.ServeHTTP(evidence, httptest.NewRequest(http.MethodGet, "/api/runs/"+view.ID+"/evidence", nil))
	if !strings.Contains(evidence.Body.String(), `"supported_count":1`) {
		t.Fatalf("evidence response = %s", evidence.Body.String())
	}
	if !strings.Contains(evidence.Body.String(), `"reason_code":"CLAIM_NOT_SUPPORTED"`) || !strings.Contains(evidence.Body.String(), `"task_id":"analysis"`) {
		t.Fatalf("evidence inspect metadata = %s", evidence.Body.String())
	}
	if err := os.WriteFile(filepath.Join(controller.options.Root, "runs", view.ID, "report.md"), []byte("# Safe\n\n<script>alert(1)</script> [bad](javascript:alert(2))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	htmlReport := httptest.NewRecorder()
	server.ServeHTTP(htmlReport, httptest.NewRequest(http.MethodGet, "/api/runs/"+view.ID+"/report?format=html", nil))
	if htmlReport.Code != http.StatusOK || !strings.Contains(htmlReport.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(htmlReport.Body.String(), "<h1>Safe</h1>") || strings.Contains(htmlReport.Body.String(), "<script>") || strings.Contains(htmlReport.Body.String(), "javascript:") {
		t.Fatalf("sanitized HTML report = %d %q", htmlReport.Code, htmlReport.Body.String())
	}
	badFormat := httptest.NewRecorder()
	server.ServeHTTP(badFormat, httptest.NewRequest(http.MethodGet, "/api/runs/"+view.ID+"/report?format=pdf", nil))
	if badFormat.Code != http.StatusBadRequest {
		t.Fatalf("invalid report format status = %d", badFormat.Code)
	}
	unknown := httptest.NewRecorder()
	server.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/runs/missing", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown status = %d", unknown.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	eventRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+view.ID+"/events", nil).WithContext(ctx)
	eventResponse := httptest.NewRecorder()
	server.ServeHTTP(eventResponse, eventRequest)
	if eventResponse.Code != http.StatusOK || !strings.Contains(eventResponse.Body.String(), "event: telemetry") || !strings.Contains(eventResponse.Body.String(), "cognitive.plan.created") {
		t.Fatalf("SSE response = %d %q", eventResponse.Code, eventResponse.Body.String())
	}
	if !strings.Contains(eventResponse.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("SSE response may be cached: %q", eventResponse.Header().Get("Cache-Control"))
	}
}

func TestDashboardExperimentDirectoryAPI(t *testing.T) {
	controller := newTestController(t, executorFunc(func(_ context.Context, spec RunSpec) error {
		return writeFakeExperimentArtifacts(t, spec.Root)
	}))
	server, err := NewServer(controller)
	if err != nil {
		t.Fatal(err)
	}
	created := httptest.NewRecorder()
	server.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(
		`{"goal":"inspect this directory and run its experiment","workload":"experiment","experiment_directory":"./examples//experiment"}`,
	)))
	if created.Code != http.StatusAccepted {
		t.Fatalf("create experiment status = %d: %s", created.Code, created.Body.String())
	}
	var view RunView
	if err := json.Unmarshal(created.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ExperimentDirectory != DefaultExperimentDirectory || view.Mode != "local" {
		t.Fatalf("experiment API view = %+v", view.RunRecord)
	}
	_ = waitForTerminal(t, controller, view.ID)

	unsafe := httptest.NewRecorder()
	server.ServeHTTP(unsafe, httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(
		`{"goal":"escape","workload":"experiment","experiment_directory":"../outside"}`,
	)))
	if unsafe.Code != http.StatusBadRequest || !strings.Contains(unsafe.Body.String(), "cannot contain ..") {
		t.Fatalf("unsafe experiment directory response = %d %s", unsafe.Code, unsafe.Body.String())
	}
}

func TestDashboardStaticAssetsAndStatus(t *testing.T) {
	controller := newTestController(t, executorFunc(func(context.Context, RunSpec) error { return nil }))
	server, err := NewServer(controller)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/", "/styles.css", "/app.js", "/api/status"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint, nil))
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("asset %s = %d bytes=%d", endpoint, response.Code, response.Body.Len())
		}
		if response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("asset %s lacks security policy", endpoint)
		}
		if strings.HasPrefix(endpoint, "/api/") && !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("API %s may be cached: %q", endpoint, response.Header().Get("Cache-Control"))
		}
	}
	index := httptest.NewRecorder()
	server.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, required := range []string{
		"dag-view", "plan-version-selector", "plan-diff", "decision-type", "decision-observation",
		"timeline", "findings", "report", "mode-badge", "history-select", "presentation-toggle", "summary-grid",
		"agent-loop", "language-select", "pdf-limit-select", "technical-details", "runtime-proof", "proof-parallel", "metric-quality",
		"experiment-panel", "experiment-results", "experiment-recovery", "experiment-directory-control", "experiment-directory-input", "run-button-label", "run-origin", "run-id",
	} {
		if !strings.Contains(index.Body.String(), `id="`+required+`"`) {
			t.Fatalf("embedded frontend is missing %s", required)
		}
	}
	for _, phrase := range []string{"THINK", "EXECUTE", "VERIFY", "Reason freely. Execute safely. Verify deterministically.", "Evidence funnel", "How CAPSuleAgent works", "Adaptive execution, backed by a real runtime"} {
		if !strings.Contains(index.Body.String(), phrase) {
			t.Fatalf("embedded frontend is missing presentation content %q", phrase)
		}
	}
	if strings.Contains(strings.ToLower(index.Body.String()), "aegis") {
		t.Fatal("legacy branding is visible in the competition frontend")
	}
	javascript := httptest.NewRecorder()
	server.ServeHTTP(javascript, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	for _, endpoint := range []string{"/api/status", "/api/runs", "/events", "/evidence", "/report"} {
		if !strings.Contains(javascript.Body.String(), endpoint) {
			t.Fatalf("embedded frontend does not consume %s", endpoint)
		}
	}
	for _, required := range []string{"presentation-mode", "importantEvent", "REUSED", "REMOVED", "format=html", "window.confirm", "PDF_LIMIT_EXCEEDED", "MEMORY_LIMIT_EXCEEDED", "Run fresh experiment", "capsule-dashboard-language", "max_pdf_mb", "experiment_directory", "capsule-experiment.json", "renderExperimentRecovery", "URLSearchParams", "accuracy-cell", "cache: \"no-store\"", "FRESH EXECUTION", "HISTORICAL SNAPSHOT"} {
		if !strings.Contains(javascript.Body.String(), required) {
			t.Fatalf("embedded frontend is missing behavior %q", required)
		}
	}
	if strings.Contains(javascript.Body.String(), "state.history[0]") {
		t.Fatal("dashboard must not present the latest persisted run as a new execution")
	}
	if strings.Contains(javascript.Body.String(), `readOnly = experiment`) || strings.Contains(javascript.Body.String(), `Plan v1<small>`) {
		t.Fatal("experiment goal or recovery path is still hard-coded")
	}
	stylesheet := httptest.NewRecorder()
	server.ServeHTTP(stylesheet, httptest.NewRequest(http.MethodGet, "/styles.css", nil))
	for _, required := range []string{"body:not(.has-run)", ".execution-board", ".experiment-recovery", ".experiment-directory-control", "prefers-reduced-motion"} {
		if !strings.Contains(stylesheet.Body.String(), required) {
			t.Fatalf("embedded stylesheet is missing %q", required)
		}
	}
}
