package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"aegisrt/internal/llm"
	"aegisrt/internal/research"
)

const maximumAPIRequestBytes = 16 * 1024

type Server struct {
	controller *Controller
	static     fs.FS
}

func NewServer(controller *Controller) (*Server, error) {
	if controller == nil {
		return nil, fmt.Errorf("dashboard controller is required")
	}
	static, err := fs.Sub(staticAssets, "static")
	if err != nil {
		return nil, err
	}
	return &Server{controller: controller, static: static}, nil
}

func (s *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	if strings.HasPrefix(request.URL.Path, "/api/") {
		response.Header().Set("Cache-Control", "no-store, max-age=0")
		response.Header().Set("Pragma", "no-cache")
	}
	if request.URL.Path == "/api/status" {
		s.handleStatus(response, request)
		return
	}
	if request.URL.Path == "/api/runs" {
		s.handleRuns(response, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/api/runs/") {
		s.handleRun(response, request)
		return
	}
	if request.URL.Path == "/healthz" {
		writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	s.serveStatic(response, request)
}

func (s *Server) handleStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	configured := false
	if config, err := llm.LoadConfig(); err == nil {
		configured = llm.ValidateConfig(config, llm.ConfigRequirements{
			RequireExplicitEndpoint: true,
			RequireCredential:       true,
		}) == nil
	}
	writeJSON(response, http.StatusOK, SystemStatus{
		RuntimeOnline: true, LLMConfigured: configured, ProviderAvailable: true,
		DefaultMode: s.controller.DefaultMode(), PresetGoal: s.controller.PresetGoal(), Presets: s.controller.Presets(),
		DefaultMaxPDFMB: int(s.controller.options.MaxPDFBytes / (1024 * 1024)),
		MaximumMaxPDFMB: int(research.MaximumPaperDownloadLimitBytes / (1024 * 1024)),
	})
}

func (s *Server) handleRuns(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(response, http.StatusOK, map[string]any{"runs": s.controller.List()})
	case http.MethodPost:
		var create CreateRunRequest
		if err := decodeRequest(response, request, &create); err != nil {
			writeAPIError(response, http.StatusBadRequest, err)
			return
		}
		view, err := s.controller.Create(create)
		if errors.Is(err, ErrRunActive) {
			writeAPIError(response, http.StatusConflict, err)
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, err)
			return
		}
		writeJSON(response, http.StatusAccepted, view)
	default:
		methodNotAllowed(response, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleRun(response http.ResponseWriter, request *http.Request) {
	remainder := strings.TrimPrefix(request.URL.Path, "/api/runs/")
	parts := strings.Split(remainder, "/")
	if len(parts) == 0 || parts[0] == "" || strings.Contains(parts[0], ".") {
		writeAPIError(response, http.StatusNotFound, ErrRunUnknown)
		return
	}
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	} else if len(parts) > 2 {
		writeAPIError(response, http.StatusNotFound, ErrRunUnknown)
		return
	}
	if action == "events" {
		s.handleEvents(response, request, id)
		return
	}
	if action == "cancel" {
		if request.Method != http.MethodPost {
			methodNotAllowed(response, http.MethodPost)
			return
		}
		if err := s.controller.Cancel(id); err != nil {
			s.writeRunError(response, err)
			return
		}
		view, _ := s.controller.View(id)
		writeJSON(response, http.StatusOK, view)
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	switch action {
	case "":
		view, err := s.controller.View(id)
		if err != nil {
			s.writeRunError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, view)
	case "plan":
		view, err := s.controller.Plans(id)
		if err != nil {
			s.writeRunError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, view)
	case "runtime":
		view, err := s.controller.View(id)
		if err != nil {
			s.writeRunError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, view.Runtime)
	case "papers":
		view, err := s.controller.Artifacts(id)
		if err != nil {
			s.writeRunError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"papers": view.Papers, "progress": view.Progress, "query_history": view.Queries, "quality": view.Quality})
	case "evidence":
		view, err := s.controller.Artifacts(id)
		if err != nil {
			s.writeRunError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, view.Evidence)
	case "report":
		data, err := s.controller.Report(id)
		if err != nil {
			if errors.Is(err, ErrRunUnknown) || errors.Is(err, io.EOF) {
				s.writeRunError(response, err)
			} else if errors.Is(err, fs.ErrNotExist) {
				writeAPIError(response, http.StatusNotFound, fmt.Errorf("report is not available yet"))
			} else {
				writeAPIError(response, http.StatusInternalServerError, err)
			}
			return
		}
		format := strings.TrimSpace(request.URL.Query().Get("format"))
		if format == "html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			data = []byte(RenderMarkdownSafe(data))
		} else if format == "" || format == "markdown" {
			response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			response.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+"-report.md"))
		} else {
			writeAPIError(response, http.StatusBadRequest, fmt.Errorf("report format must be markdown or html"))
			return
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(data)
	default:
		writeAPIError(response, http.StatusNotFound, ErrRunUnknown)
	}
}

func (s *Server) handleEvents(response http.ResponseWriter, request *http.Request, id string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	store, err := s.controller.EventStore(id)
	if err != nil {
		s.writeRunError(response, err)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeAPIError(response, http.StatusInternalServerError, fmt.Errorf("streaming is unavailable"))
		return
	}
	since := uint64(0)
	rawSince := strings.TrimSpace(request.URL.Query().Get("since"))
	if rawSince == "" {
		rawSince = strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	}
	if rawSince != "" {
		parsed, parseErr := strconv.ParseUint(rawSince, 10, 64)
		if parseErr != nil {
			writeAPIError(response, http.StatusBadRequest, fmt.Errorf("since must be an unsigned integer"))
			return
		}
		since = parsed
	}
	updates, unsubscribe := store.Subscribe()
	defer unsubscribe()
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	response.Header().Set("Connection", "keep-alive")
	response.WriteHeader(http.StatusOK)
	last := since
	for _, event := range store.Snapshot(since) {
		if event.Sequence > last && writeSSE(response, event) == nil {
			last = event.Sequence
		}
	}
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-updates:
			if event.Sequence <= last {
				continue
			}
			if err := writeSSE(response, event); err != nil {
				return
			}
			last = event.Sequence
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(response, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) serveStatic(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(response, http.MethodGet, http.MethodHead)
		return
	}
	name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	if strings.Contains(name, "/") {
		http.NotFound(response, request)
		return
	}
	data, err := fs.ReadFile(s.static, name)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Content-Length", strconv.Itoa(len(data)))
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = response.Write(data)
	}
}

func (s *Server) writeRunError(response http.ResponseWriter, err error) {
	if errors.Is(err, ErrRunUnknown) {
		writeAPIError(response, http.StatusNotFound, err)
		return
	}
	if strings.Contains(err.Error(), "already") {
		writeAPIError(response, http.StatusConflict, err)
		return
	}
	writeAPIError(response, http.StatusInternalServerError, err)
}

func decodeRequest(response http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(response, request.Body, maximumAPIRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request must contain one JSON object")
	}
	return nil
}

func writeSSE(writer io.Writer, event DashboardEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: telemetry\ndata: %s\n\n", event.Sequence, data)
	return err
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		http.Error(response, "encode response", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_, _ = response.Write(buffer.Bytes())
}

func writeAPIError(response http.ResponseWriter, status int, err error) {
	message := "request failed"
	if err != nil {
		message = boundedText(err.Error(), 512)
	}
	writeJSON(response, status, APIError{Error: message})
}

func methodNotAllowed(response http.ResponseWriter, allowed ...string) {
	response.Header().Set("Allow", strings.Join(allowed, ", "))
	writeAPIError(response, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
}
