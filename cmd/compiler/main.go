// Command compiler runs the contextual compiler as a standalone HTTP service.
//
// Configuration is loaded from a JSON or YAML file specified by the CONFIG_PATH
// environment variable (default: config.yaml). The format is auto-detected
// by file extension (.json, .yaml, .yml).
//
// Optional persistence:
//   - DATABASE_URL: Postgres connection string — enables Postgres storage
//   - SQLITE_PATH: Path to SQLite database file — enables SQLite storage
//   - Neither: runs in pure in-memory mode (current default)
//
// This binary serves as a reference implementation. Production deployments
// should import the pkg/ packages directly and wire their own adapters.
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	openaiembed "github.com/Yes-League/contextual-compiler/adapters/embeddings/openai"
	"github.com/Yes-League/contextual-compiler/adapters/events/logwriter"
	"github.com/Yes-League/contextual-compiler/adapters/llm/anthropic"
	"github.com/Yes-League/contextual-compiler/adapters/llm/gemini"
	"github.com/Yes-League/contextual-compiler/adapters/llm/openai"
	memoryvec "github.com/Yes-League/contextual-compiler/adapters/vector/memory"
	"github.com/Yes-League/contextual-compiler/adapters/storage/postgres"
	sqliteadapter "github.com/Yes-League/contextual-compiler/adapters/storage/sqlite"
	"github.com/Yes-League/contextual-compiler/api"
	"github.com/Yes-League/contextual-compiler/pkg/classifier"
	"github.com/Yes-League/contextual-compiler/pkg/compiler"
	"github.com/Yes-League/contextual-compiler/pkg/gate"
	"github.com/Yes-League/contextual-compiler/pkg/health"
	"github.com/Yes-League/contextual-compiler/pkg/keywords"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8200"
	}

	cfg := loadConfig()
	deps, cleanup := buildDeps()
	defer cleanup()

	metrics := &api.Metrics{}

	c := compiler.New(cfg, deps, compiler.Callbacks{
		OnClassify: func(source, category string) {
			log.Printf("classify: source=%s category=%s", source, category)
		},
		OnGateSkip: func() {
			metrics.GateSkips.Add(1)
			log.Printf("gate: skipped LLM call")
		},
		OnLLMFallback: func(err error) {
			metrics.LLMFallbacks.Add(1)
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

	handler := api.NewHandler(c, api.WithMetrics(metrics))
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

	cfg, err := compiler.LoadConfigFromFile(configPath)
	if err != nil {
		log.Printf("No config file at %s (%v), using defaults with example categories", configPath, err)
		return defaultDemoConfig()
	}

	if err := compiler.ValidateConfig(cfg); err != nil {
		log.Printf("Warning: config validation: %v", err)
	}

	return cfg
}

// buildDeps creates storage adapters based on environment variables.
// Returns deps and a cleanup function to close database connections.
func buildDeps() (compiler.Deps, func()) {
	var deps compiler.Deps
	cleanup := func() {}

	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			log.Fatalf("Failed to open Postgres: %v", err)
		}
		if err := db.Ping(); err != nil {
			log.Fatalf("Failed to ping Postgres: %v", err)
		}

		store := postgres.New(db)
		if err := store.EnsureSchema(); err != nil {
			log.Fatalf("Failed to ensure Postgres schema: %v", err)
		}

		deps.GateStore = store
		deps.HealthStore = store
		deps.KeywordStore = store
		cleanup = func() { db.Close() }
		log.Println("Storage: Postgres")
	} else if path := os.Getenv("SQLITE_PATH"); path != "" {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			log.Fatalf("Failed to open SQLite: %v", err)
		}
		if err := db.Ping(); err != nil {
			log.Fatalf("Failed to ping SQLite: %v", err)
		}

		store := sqliteadapter.New(db)
		if err := store.EnsureSchema(); err != nil {
			log.Fatalf("Failed to ensure SQLite schema: %v", err)
		}

		deps.GateStore = store
		deps.HealthStore = store
		deps.KeywordStore = store
		cleanup = func() { db.Close() }
		log.Printf("Storage: SQLite (%s)", path)
	} else {
		log.Println("Storage: in-memory (no persistence)")
	}

	// Wire LLM adapter: prefer Anthropic, fall back to OpenAI, then Gemini
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		deps.LLM = anthropic.New(key)
		log.Println("LLM: Anthropic")
	} else if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		deps.LLM = openai.New(key)
		log.Println("LLM: OpenAI")
	} else if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		deps.LLM = gemini.New(key)
		log.Println("LLM: Gemini")
	} else {
		log.Println("LLM: none (pure heuristic mode)")
	}

	// Wire vector search: requires OpenAI API key for embeddings
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		embedder := openaiembed.New(key)
		deps.Vector = memoryvec.New(embedder)
		log.Println("Vector: in-memory (OpenAI embeddings)")
	}

	// Wire event sink
	if os.Getenv("LOG_EVENTS") == "true" {
		deps.Events = logwriter.New(os.Stdout)
		log.Println("Events: log writer (stdout)")
	}

	return deps, cleanup
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

// Ensure interfaces are satisfied at compile time.
var (
	_ gate.GateStore        = (*postgres.Store)(nil)
	_ health.HealthStore    = (*postgres.Store)(nil)
	_ keywords.KeywordStore = (*postgres.Store)(nil)

	_ gate.GateStore        = (*sqliteadapter.Store)(nil)
	_ health.HealthStore    = (*sqliteadapter.Store)(nil)
	_ keywords.KeywordStore = (*sqliteadapter.Store)(nil)

	_ compiler.LLMClassifier = (*anthropic.Client)(nil)
	_ compiler.LLMClassifier = (*openai.Client)(nil)
	_ compiler.LLMClassifier = (*gemini.Client)(nil)
	_ compiler.EventSink     = (*logwriter.Sink)(nil)
	_ compiler.Embedder      = (*openaiembed.Client)(nil)
	_ compiler.VectorSearcher = (*memoryvec.Store)(nil)
)
