// Command compiler runs the contextual compiler as a standalone HTTP service.
//
// Configuration is loaded from a YAML file specified by the CONFIG_PATH
// environment variable (default: config.yaml). All adapter dependencies
// are nil by default — the service runs in pure-heuristic, in-memory mode
// unless adapters are wired in programmatically.
//
// This binary serves as a reference implementation. Production deployments
// should import the pkg/ packages directly and wire their own adapters.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yes-league/contextual-compiler/api"
	"github.com/yes-league/contextual-compiler/pkg/classifier"
	"github.com/yes-league/contextual-compiler/pkg/compiler"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8200"
	}

	cfg := loadConfig()

	c := compiler.New(cfg, compiler.Deps{}, compiler.Callbacks{
		OnClassify: func(source, category string) {
			log.Printf("classify: source=%s category=%s", source, category)
		},
		OnGateSkip: func() {
			log.Printf("gate: skipped LLM call")
		},
		OnLLMFallback: func(err error) {
			log.Printf("llm: fallback to heuristic: %v", err)
		},
		OnAgreement: func(agreed bool) {
			log.Printf("agreement: heuristic_llm_agreed=%v", agreed)
		},
		OnKeywordsPromoted: func(count int) {
			log.Printf("keywords: promoted %d keywords", count)
		},
	})

	if err := c.LoadState(); err != nil {
		log.Printf("Warning: failed to load state: %v", err)
	}

	handler := api.NewHandler(c)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")

		if err := c.FlushState(); err != nil {
			log.Printf("Warning: failed to flush state: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("Contextual Compiler listening on :%s", port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func loadConfig() compiler.Config {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	// Try loading config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("No config file at %s, using defaults with example categories", configPath)
		return defaultDemoConfig()
	}

	var cfg compiler.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("Warning: failed to parse %s: %v, using defaults", configPath, err)
		return defaultDemoConfig()
	}

	return cfg
}

func defaultDemoConfig() compiler.Config {
	cfg := compiler.DefaultConfig()
	cfg.Classifier = classifier.Config{
		Categories: []classifier.CategoryConfig{
			{
				Name:     "performance",
				Keywords: []string{"latency", "throughput", "p50", "p95", "p99", "slow", "timeout", "response_time", "cpu", "memory"},
				Weights:  map[string]float64{"p99": 2.0, "p95": 2.0, "timeout": 2.0},
			},
			{
				Name:     "reliability",
				Keywords: []string{"error", "failure", "crash", "panic", "exception", "unavailable", "downtime", "outage", "5xx"},
				Weights:  map[string]float64{"outage": 2.0, "crash": 2.0, "panic": 2.0},
			},
			{
				Name:     "security",
				Keywords: []string{"auth", "authentication", "authorization", "permission", "forbidden", "401", "403", "vulnerability", "cve"},
				Weights:  map[string]float64{"cve": 2.0, "vulnerability": 2.0},
			},
			{
				Name:     "deployment",
				Keywords: []string{"deploy", "release", "rollout", "rollback", "build", "ci", "cd", "pipeline", "container"},
				Weights:  map[string]float64{"deploy": 2.0, "rollback": 2.0},
			},
		},
		SourcePriors: map[string]map[string]float64{
			"sentry":     {"reliability": 3.0},
			"prometheus": {"performance": 3.0},
		},
		TypeToCategory: map[string]string{
			"metric": "performance",
			"error":  "reliability",
			"deploy": "deployment",
			"audit":  "security",
		},
	}
	return cfg
}
