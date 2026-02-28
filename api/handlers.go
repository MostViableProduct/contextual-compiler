// Package api provides HTTP handlers for the contextual compiler REST API.
//
// All handlers operate on a *compiler.Compiler instance and expose the
// cascade classification, health scoring, and keyword promotion endpoints.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/Yes-League/contextual-compiler/pkg/compiler"
)

// Handler wraps a compiler and exposes HTTP endpoints.
type Handler struct {
	compiler *compiler.Compiler
	metrics  *Metrics
}

// HandlerOption configures a Handler.
type HandlerOption func(*Handler)

// WithMetrics attaches a Metrics instance for counter tracking.
func WithMetrics(m *Metrics) HandlerOption {
	return func(h *Handler) { h.metrics = m }
}

// NewHandler creates a Handler from a configured compiler.
func NewHandler(c *compiler.Compiler, opts ...HandlerOption) *Handler {
	h := &Handler{compiler: c}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// RegisterRoutes registers all API routes on a ServeMux.
// The caller is responsible for middleware (auth, CORS, logging, etc.).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("POST /v1/classify", h.handleClassify)
	mux.HandleFunc("POST /v1/classify/signal", h.handleClassifySignal)
	mux.HandleFunc("GET /v1/health/{tenant_id}/{entity_id}", h.handleEntityHealth)
	mux.HandleFunc("POST /v1/health/{tenant_id}/{entity_id}/events", h.handleRecordHealthEvent)
	mux.HandleFunc("POST /v1/keywords/promote", h.handlePromoteKeywords)
	mux.HandleFunc("POST /v1/state/flush", h.handleFlushState)
	if h.metrics != nil {
		mux.HandleFunc("GET /metrics", h.metrics.handleMetrics)
	}
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	depStatus := func(has bool) string {
		if has {
			return "connected"
		}
		return "disabled"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"dependencies": map[string]string{
			"llm":     depStatus(h.compiler.HasLLM()),
			"vector":  depStatus(h.compiler.HasVector()),
			"events":  depStatus(h.compiler.HasEvents()),
			"storage": depStatus(h.compiler.HasStorage()),
		},
	})
}

// ClassifyRequest is the request body for POST /v1/classify.
type ClassifyRequest struct {
	TenantID string `json:"tenant_id,omitempty"`
	Source   string `json:"source"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

func (h *Handler) handleClassify(w http.ResponseWriter, r *http.Request) {
	var req ClassifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "missing required field: type")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "missing required field: content")
		return
	}

	result, err := h.compiler.Classify(r.Context(), compiler.Signal{
		TenantID: req.TenantID,
		Source:   req.Source,
		Type:     req.Type,
		Content:  req.Content,
		Payload:  req.Payload,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if h.metrics != nil {
		h.metrics.ClassifyTotal.Add(1)
		switch result.ClassificationSource {
		case "llm":
			h.metrics.ClassifyLLM.Add(1)
		case "heuristic_gated":
			h.metrics.ClassifyGated.Add(1)
		default:
			h.metrics.ClassifyHeuristic.Add(1)
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// ClassifySignalRequest is the request body for POST /v1/classify/signal.
type ClassifySignalRequest struct {
	TenantID string          `json:"tenant_id,omitempty"`
	Source   string          `json:"source"`
	Type     string          `json:"type"`
	Payload  json.RawMessage `json:"payload"`
}

func (h *Handler) handleClassifySignal(w http.ResponseWriter, r *http.Request) {
	var req ClassifySignalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "missing required field: type")
		return
	}
	if len(req.Payload) == 0 {
		writeError(w, http.StatusBadRequest, "missing required field: payload")
		return
	}

	result, err := h.compiler.ClassifySignal(r.Context(), req.TenantID, req.Source, req.Type, req.Payload)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleEntityHealth(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	entityID := r.PathValue("entity_id")
	if tenantID == "" || entityID == "" {
		writeError(w, http.StatusBadRequest, "tenant_id and entity_id are required")
		return
	}

	if h.metrics != nil {
		h.metrics.HealthQueries.Add(1)
	}
	result := h.compiler.ScoreHealth(tenantID, entityID)
	writeJSON(w, http.StatusOK, result)
}

// RecordHealthEventRequest is the request body for POST /v1/health/{tenant_id}/{entity_id}/events.
type RecordHealthEventRequest struct {
	Severity   string  `json:"severity"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
}

func (h *Handler) handleRecordHealthEvent(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	entityID := r.PathValue("entity_id")
	if tenantID == "" || entityID == "" {
		writeError(w, http.StatusBadRequest, "tenant_id and entity_id are required")
		return
	}

	var req RecordHealthEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Severity == "" {
		writeError(w, http.StatusBadRequest, "missing required field: severity")
		return
	}

	h.compiler.RecordHealthEvent(tenantID, entityID, req.Severity, req.Category, req.Confidence)
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

func (h *Handler) handlePromoteKeywords(w http.ResponseWriter, r *http.Request) {
	count, err := h.compiler.PromoteKeywords()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.metrics != nil && count > 0 {
		h.metrics.KeywordsPromoted.Add(int64(count))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"promoted": count})
}

func (h *Handler) handleFlushState(w http.ResponseWriter, r *http.Request) {
	if err := h.compiler.FlushState(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "flushed"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
