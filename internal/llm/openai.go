package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAICompatibleClient implements Client for OpenAI-compatible APIs (vLLM, LocalAI, etc.)
type OpenAICompatibleClient struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

// NewOpenAICompatibleClient creates a new OpenAI-compatible client
func NewOpenAICompatibleClient(baseURL, model, apiKey string) *OpenAICompatibleClient {
	return &OpenAICompatibleClient{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 300 * time.Second,
		},
	}
}

type openAIRequest struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
}

type openAIResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Index        int     `json:"index"`
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
}

// Chat sends a chat request to an OpenAI-compatible API
func (c *OpenAICompatibleClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := c.model
	if req.Model != "" {
		model = req.Model
	}

	openAIReq := openAIRequest{
		Model:    model,
		Messages: req.Messages,
		Tools:    req.Tools,
		Stream:   false,
	}

	body, err := json.Marshal(openAIReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var openAIResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := openAIResp.Choices[0]
	return &ChatResponse{
		Message:      choice.Message,
		FinishReason: choice.FinishReason,
	}, nil
}
