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
}

// NewHandler creates a Handler from a configured compiler.
func NewHandler(c *compiler.Compiler) *Handler {
	return &Handler{compiler: c}
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
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
