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

// OllamaClient implements the Client interface for Ollama
type OllamaClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewOllamaClient creates a new Ollama client
func NewOllamaClient(baseURL, model string) *OllamaClient {
	return &OllamaClient{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// ollamaRequest is the Ollama API request format
type ollamaRequest struct {
	Model    string           `json:"model"`
	Messages []ollamaMessage  `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
}

type ollamaMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ollamaResponse is the Ollama API response format
type ollamaResponse struct {
	Model      string        `json:"model"`
	Message    ollamaMessage `json:"message"`
	Done       bool          `json:"done"`
	DoneReason string        `json:"done_reason,omitempty"`
}

// Chat sends a chat request to Ollama
func (c *OllamaClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Convert messages to Ollama format
	var msgs []ollamaMessage
	for _, m := range req.Messages {
		msg := ollamaMessage{
			Role:      m.Role,
			Content:   m.Content,
			ToolCalls: m.ToolCalls,
		}
		msgs = append(msgs, msg)
	}

	model := c.model
	if req.Model != "" {
		model = req.Model
	}

	ollamaReq := ollamaRequest{
		Model:    model,
		Messages: msgs,
		Tools:    req.Tools,
		Stream:   false,
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (%d): %s", resp.StatusCode, string(respBody))
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	finishReason := "stop"
	if len(ollamaResp.Message.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}

	return &ChatResponse{
		Message: Message{
			Role:      ollamaResp.Message.Role,
			Content:   ollamaResp.Message.Content,
			ToolCalls: ollamaResp.Message.ToolCalls,
		},
		FinishReason: finishReason,
	}, nil
}
