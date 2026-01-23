package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NewEmbedder creates an appropriate Embedder based on configuration
func NewEmbedder(cfg *Config) (Embedder, error) {
	switch cfg.EmbedderType {
	case "ollama":
		return NewOllamaEmbedder(cfg.EmbedderBaseURL, cfg.EmbedderModel)
	case "openai":
		return NewOpenAIEmbedder(cfg.EmbedderAPIKey, cfg.EmbedderModel)
	case "llama-cpp":
		return NewLlamaCPPEmbedder(cfg.EmbedderBaseURL, cfg.EmbedderModel)
	default:
		return nil, fmt.Errorf("unknown embedder type: %s", cfg.EmbedderType)
	}
}

// LlamaCPPEmbedder uses llama.cpp server for embedding generation
type LlamaCPPEmbedder struct {
	baseURL    string
	model      string
	httpClient *http.Client
	dimension  int
}

// NewLlamaCPPEmbedder creates a new llama.cpp embedder
func NewLlamaCPPEmbedder(baseURL, model string) (*LlamaCPPEmbedder, error) {
	e := &LlamaCPPEmbedder{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}

	// Detect dimension
	dim, err := e.detectDimension()
	if err != nil {
		return nil, fmt.Errorf("failed to detect llama.cpp embedding dimension: %w", err)
	}
	e.dimension = dim

	return e, nil
}

// Embed converts a single text to a vector using llama.cpp /embedding endpoint
func (e *LlamaCPPEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	req := map[string]interface{}{
		"content": text,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/embedding", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llama.cpp error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try decoding as a struct first: {"embedding": [...]}
	var structResult struct {
		Embedding interface{} `json:"embedding"`
	}
	if err := json.Unmarshal(bodyBytes, &structResult); err == nil {
		if vec, ok := extractVector(structResult.Embedding); ok {
			return vec, nil
		}
	}

	// Try decoding as a raw array: [...]
	var arrayResult []interface{}
	if err := json.Unmarshal(bodyBytes, &arrayResult); err == nil {
		// If it's a simple float array
		if vec, ok := extractVector(arrayResult); ok {
			return vec, nil
		}
		// If it's an array of objects: [{"embedding": [...]}] or [{"embedding": [[...]]}]
		for _, item := range arrayResult {
			if m, ok := item.(map[string]interface{}); ok {
				if emb, ok := m["embedding"]; ok {
					if vec, ok := extractVector(emb); ok {
						return vec, nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("failed to decode llama.cpp response: %s", string(bodyBytes))
}

func extractVector(v interface{}) ([]float32, bool) {
	// Try []float32
	if vec, ok := v.([]float32); ok {
		return vec, true
	}
	// Try []interface{}
	if list, ok := v.([]interface{}); ok {
		if len(list) == 0 {
			return nil, false
		}
		// Check if it's a list of numbers
		if _, ok := list[0].(float64); ok {
			vec := make([]float32, len(list))
			for i, val := range list {
				if f, ok := val.(float64); ok {
					vec[i] = float32(f)
				}
			}
			return vec, true
		}
		// Check if it's a 2D list: [[...]]
		if subList, ok := list[0].([]interface{}); ok {
			return extractVector(subList)
		}
	}
	return nil, false
}

// EmbedBatch converts multiple texts to vectors
func (e *LlamaCPPEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		embedding, err := e.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("failed to embed text %d: %w", i, err)
		}
		results[i] = embedding
	}
	return results, nil
}

// Dimension returns the vector dimension
func (e *LlamaCPPEmbedder) Dimension() int {
	return e.dimension
}

func (e *LlamaCPPEmbedder) detectDimension() (int, error) {
	// Add a small delay/retry for server startup if needed, but simple call should work
	embedding, err := e.Embed(context.Background(), "test")
	if err != nil {
		return 0, err
	}
	return len(embedding), nil
}

// OllamaEmbedder uses Ollama for embedding generation
type OllamaEmbedder struct {
	baseURL    string
	model      string
	httpClient *http.Client
	dimension  int
}

// NewOllamaEmbedder creates a new Ollama embedder
func NewOllamaEmbedder(baseURL, model string) (*OllamaEmbedder, error) {
	e := &OllamaEmbedder{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Detect dimension by making a test embedding
	dim, err := e.detectDimension()
	if err != nil {
		return nil, fmt.Errorf("failed to detect embedding dimension: %w", err)
	}
	e.dimension = dim

	return e, nil
}

// Embed converts a single text to a vector
func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	req := map[string]interface{}{
		"model":  e.model,
		"prompt": text,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Embedding, nil
}

// EmbedBatch converts multiple texts to vectors
func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		embedding, err := e.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("failed to embed text %d: %w", i, err)
		}
		results[i] = embedding
	}
	return results, nil
}

// Dimension returns the vector dimension
func (e *OllamaEmbedder) Dimension() int {
	return e.dimension
}

func (e *OllamaEmbedder) detectDimension() (int, error) {
	embedding, err := e.Embed(context.Background(), "test")
	if err != nil {
		return 0, err
	}
	return len(embedding), nil
}

// OpenAIEmbedder uses OpenAI API for embedding generation
type OpenAIEmbedder struct {
	apiKey     string
	model      string
	httpClient *http.Client
	dimension  int
}

// NewOpenAIEmbedder creates a new OpenAI embedder
func NewOpenAIEmbedder(apiKey, model string) (*OpenAIEmbedder, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	if model == "" {
		model = "text-embedding-3-small"
	}

	// Set default dimensions based on model
	dim := 1536 // text-embedding-3-small default
	if model == "text-embedding-3-large" {
		dim = 3072
	} else if model == "text-embedding-ada-002" {
		dim = 1536
	}

	return &OpenAIEmbedder{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		dimension: dim,
	}, nil
}

// Embed converts a single text to a vector
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	req := map[string]interface{}{
		"model": e.model,
		"input": text,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return result.Data[0].Embedding, nil
}

// EmbedBatch converts multiple texts to vectors
func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	req := map[string]interface{}{
		"model": e.model,
		"input": texts,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Sort by index
	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}

	return embeddings, nil
}

// Dimension returns the vector dimension
func (e *OpenAIEmbedder) Dimension() int {
	return e.dimension
}

// Ensure implementations satisfy the Embedder interface
var (
	_ Embedder = (*OllamaEmbedder)(nil)
	_ Embedder = (*OpenAIEmbedder)(nil)
)
