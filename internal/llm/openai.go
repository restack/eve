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

type OpenAICompatibleClient struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

func NewOpenAICompatibleClient(baseURL, model, apiKey string) *OpenAICompatibleClient {
	return &OpenAICompatibleClient{
		baseURL: baseURL, model: model, apiKey: apiKey,
		httpClient: &http.Client{Timeout: 300 * time.Second},
	}
}

type openAIRequest struct {
	Model       string                 `json:"model"`
	Messages    []Message              `json:"messages"`
	Tools       []ToolDefinition       `json:"tools,omitempty"`
	ToolChoice  interface{}            `json:"tool_choice,omitempty"`
	Stream      bool                   `json:"stream"`
	Temperature *float64               `json:"temperature,omitempty"`
	MaxTokens   *int                   `json:"max_tokens,omitempty"`
	Extra       map[string]interface{} `json:"extra_body,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func (c *OpenAICompatibleClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// SANITIZE MESSAGES: If no tools are being sent, strip all native tool metadata from history
	// to prevent local server template crashes (500 errors).
	sanitizedMessages := make([]Message, len(req.Messages))
	for i, m := range req.Messages {
		sanitized := m
		if len(req.Tools) == 0 {
			if sanitized.Role == "tool" {
				sanitized.Role = "user"
				sanitized.Content = fmt.Sprintf("[Observation]: %s", sanitized.Content)
			}
			sanitized.ToolCalls = nil
			sanitized.ToolCallID = ""
		}
		sanitizedMessages[i] = sanitized
	}

	openAIReq := openAIRequest{
		Model: c.model, Messages: sanitizedMessages, Stream: false,
		Temperature: req.Temperature, MaxTokens: req.MaxTokens,
	}

	if len(req.Tools) > 0 {
		openAIReq.Tools = req.Tools
		if req.ToolChoice != nil {
			openAIReq.ToolChoice = req.ToolChoice
		} else {
			openAIReq.ToolChoice = "auto"
		}
	}

	body, _ := json.Marshal(openAIReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var oaiResp openAIResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return nil, err
	}
	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no response")
	}

	return &ChatResponse{Message: oaiResp.Choices[0].Message}, nil
}
