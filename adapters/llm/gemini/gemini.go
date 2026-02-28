// Package gemini implements compiler.LLMClassifier using Google's Gemini
// generateContent REST API.
//
// Key differences from Anthropic/OpenAI adapters:
//   - Auth: API key as query param ?key=...
//   - System prompt via systemInstruction top-level field
//   - Structured output via generationConfig.responseMimeType
//   - Response path: candidates[0].content.parts[0].text
//
// Usage:
//
//	client := gemini.New(os.Getenv("GEMINI_API_KEY"))
//	result, err := client.Classify(ctx, "high p99 latency", "metric", []string{"performance", "reliability"})
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Yes-League/contextual-compiler/pkg/compiler"
)

const (
	defaultModel   = "gemini-2.0-flash"
	defaultBaseURL = "https://generativelanguage.googleapis.com"
	defaultTimeout = 30 * time.Second
)

// Compile-time interface check.
var _ compiler.LLMClassifier = (*Client)(nil)

// Client calls Google's Gemini generateContent API for classification.
type Client struct {
	httpClient *http.Client
	apiKey     string
	model      string
	baseURL    string
}

// Option configures a Client.
type Option func(*Client)

// WithModel overrides the default model.
func WithModel(model string) Option {
	return func(c *Client) { c.model = model }
}

// WithTimeout overrides the default HTTP timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.httpClient.Timeout = d }
}

// WithBaseURL overrides the API base URL (useful for proxies or testing).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// New creates a Gemini classification client.
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		apiKey:     apiKey,
		model:      defaultModel,
		baseURL:    defaultBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

const systemPrompt = `You are a signal classifier. Given a signal's content and type, classify it into exactly one of the provided categories.

Respond with ONLY a JSON object in this exact format:
{"category":"<one of the provided categories>","confidence":<0.0 to 1.0>,"keywords":["<relevant keywords>"]}

Rules:
- category MUST be one of the provided categories, exactly as written
- confidence is your certainty from 0.0 (no idea) to 1.0 (certain)
- keywords are the terms from the content that drove your decision`

// Classify implements compiler.LLMClassifier.
func (c *Client) Classify(ctx context.Context, content, signalType string, categories []string) (*compiler.LLMResult, error) {
	userPrompt := fmt.Sprintf("Categories: %s\nSignal type: %s\nContent: %s",
		strings.Join(categories, ", "), signalType, content)

	reqBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{
				{"text": systemPrompt},
			},
		},
		"contents": []map[string]any{
			{
				"parts": []map[string]string{
					{"text": userPrompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		c.baseURL, c.model, c.apiKey)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini: returned %d: %s", resp.StatusCode, string(respBody))
	}

	var genResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w", err)
	}
	if len(genResp.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: no candidates in response")
	}
	if len(genResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini: no parts in response")
	}

	var result compiler.LLMResult
	if err := json.Unmarshal([]byte(genResp.Candidates[0].Content.Parts[0].Text), &result); err != nil {
		return nil, fmt.Errorf("gemini: parse classification JSON: %w", err)
	}

	// Validate category is in the provided list
	valid := false
	for _, cat := range categories {
		if cat == result.Category {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("gemini: LLM returned unknown category %q", result.Category)
	}

	return &result, nil
}
