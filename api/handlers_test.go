package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yes-league/contextual-compiler/pkg/classifier"
	"github.com/yes-league/contextual-compiler/pkg/compiler"
)

func testCompiler() *compiler.Compiler {
	cfg := compiler.DefaultConfig()
	cfg.Classifier = classifier.Config{
		Categories: []classifier.CategoryConfig{
			{
				Name:     "performance",
				Keywords: []string{"latency", "p99", "slow", "timeout"},
				Weights:  map[string]float64{"p99": 2.0},
			},
			{
				Name:     "reliability",
				Keywords: []string{"error", "failure", "crash", "outage"},
				Weights:  map[string]float64{"outage": 2.0},
			},
		},
		TypeToCategory: map[string]string{
			"metric": "performance",
			"error":  "reliability",
		},
	}
	return compiler.New(cfg, compiler.Deps{}, compiler.Callbacks{})
}

func TestHealthEndpoint(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("Expected status ok, got %s", resp["status"])
	}
}

func TestClassifyEndpoint(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := ClassifyRequest{
		Source:  "prometheus",
		Type:    "metric",
		Content: "High p99 latency detected in API gateway",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/classify", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp compiler.ClassifyResult
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Category != "performance" {
		t.Errorf("Expected performance, got %s", resp.Category)
	}
}

func TestClassifyEndpoint_MissingType(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := ClassifyRequest{Content: "some content"}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/classify", bytes.NewReader(b))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d", w.Code)
	}
}

func TestClassifyEndpoint_MissingContent(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := ClassifyRequest{Type: "metric"}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/classify", bytes.NewReader(b))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d", w.Code)
	}
}

func TestClassifySignalEndpoint(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := ClassifySignalRequest{
		TenantID: "t1",
		Source:   "prometheus",
		Type:     "metric",
		Payload:  json.RawMessage(`{"message": "p99 latency spike"}`),
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/classify/signal", bytes.NewReader(b))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEntityHealthEndpoint(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/v1/health/tenant-1/entity-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp compiler.HealthResult
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.EntityID != "entity-1" {
		t.Errorf("Expected entity-1, got %s", resp.EntityID)
	}
	if resp.Score < 0 || resp.Score > 100 {
		t.Errorf("Score out of bounds: %v", resp.Score)
	}
}

func TestRecordHealthEventEndpoint(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := RecordHealthEventRequest{
		Severity:   "critical",
		Category:   "reliability",
		Confidence: 0.9,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/v1/health/tenant-1/entity-1/events", bytes.NewReader(b))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPromoteKeywordsEndpoint(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/v1/keywords/promote", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFlushStateEndpoint(t *testing.T) {
	h := NewHandler(testCompiler())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/v1/state/flush", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
