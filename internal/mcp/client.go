// Package mcp provides a client for the Model Context Protocol.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/restack/eve/internal/tools"
)

// Client represents an MCP client that connects to a server via HTTP/SSE.
type Client struct {
	serverURL  string
	httpClient *http.Client
	serverInfo *ServerInfo
	sessionID  string
	mu         sync.RWMutex
}

// ServerInfo contains metadata about the MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCP Tool definitions (protocol level)
type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type mcpListToolsResponse struct {
	Tools []mcpTool `json:"tools"`
}

type mcpCallToolRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type mcpCallToolResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// NewClient creates a new MCP client.
func NewClient(serverURL string) *Client {
	return &Client{
		serverURL: serverURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Initialize performs the handshake with the MCP server.
func (c *Client) Initialize(ctx context.Context) error {
	// MCP protocol requires initialization before other methods
	initReq := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"clientInfo": map[string]interface{}{
			"name":    "eve",
			"version": "0.3.0",
		},
	}

	slog.Debug("sending MCP initialize", "url", c.serverURL)
	resp, err := c.callRPC(ctx, "initialize", initReq)
	if err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}

	// Parse server info if available
	var serverResp struct {
		ServerInfo *ServerInfo `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp, &serverResp); err == nil && serverResp.ServerInfo != nil {
		c.mu.Lock()
		c.serverInfo = serverResp.ServerInfo
		c.mu.Unlock()
	}

	// Send initialized notification (no response expected)
	slog.Debug("sending MCP notifications/initialized", "url", c.serverURL)
	if err := c.notifyRPC(ctx, "notifications/initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("failed to send initialized notification: %w", err)
	}

	// Give the server a moment to transition state
	time.Sleep(500 * time.Millisecond)

	return nil
}

// ListTools retrieves the available tools from the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]*tools.Tool, error) {
	// Add a small retry loop for ListTools in case server is still transitioning
	var resp json.RawMessage
	var err error

	for i := 0; i < 5; i++ {
		resp, err = c.callRPC(ctx, "tools/list", nil)
		if err == nil {
			break
		}
		if i < 4 {
			slog.Warn("MCP tools/list failed, retrying...", "url", c.serverURL, "error", err, "attempt", i+1)
			time.Sleep(1 * time.Second)
		}
	}

	if err != nil {
		return nil, err
	}

	var list mcpListToolsResponse
	if err := json.Unmarshal(resp, &list); err != nil {
		return nil, fmt.Errorf("failed to decode tools list: %w", err)
	}

	var eveTools []*tools.Tool
	for _, t := range list.Tools {
		mcpT := t

		schemaBytes, _ := json.Marshal(mcpT.InputSchema)
		var eveSchema tools.InputSchema
		json.Unmarshal(schemaBytes, &eveSchema)

		eveTools = append(eveTools, &tools.Tool{
			Name:        mcpT.Name,
			Description: mcpT.Description,
			InputSchema: eveSchema,
			Handler: func(ctx context.Context, input json.RawMessage) (*tools.Result, error) {
				return c.CallTool(ctx, mcpT.Name, input)
			},
		})
	}

	return eveTools, nil
}

// CallTool executes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, input json.RawMessage) (*tools.Result, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("invalid tool input: %w", err)
	}

	callReq := mcpCallToolRequest{
		Name:      name,
		Arguments: args,
	}

	resp, err := c.callRPC(ctx, "tools/call", callReq)
	if err != nil {
		return nil, err
	}

	var callResp mcpCallToolResponse
	if err := json.Unmarshal(resp, &callResp); err != nil {
		return nil, fmt.Errorf("failed to decode tool response: %w", err)
	}

	var output strings.Builder
	for _, content := range callResp.Content {
		if content.Type == "text" {
			output.WriteString(content.Text)
		}
	}

	if callResp.IsError {
		return tools.NewErrorResult(output.String()), nil
	}

	return tools.NewSuccessResult(output.String()), nil
}

// callRPC is a helper for JSON-RPC over HTTP with an ID.
func (c *Client) callRPC(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	return c.doRPC(ctx, method, params, time.Now().UnixNano())
}

// notifyRPC is for notifications (no ID).
func (c *Client) notifyRPC(ctx context.Context, method string, params interface{}) error {
	_, err := c.doRPC(ctx, method, params, nil)
	return err
}

func (c *Client) doRPC(ctx context.Context, method string, params interface{}, id interface{}) (json.RawMessage, error) {
	rpcReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if id != nil {
		rpcReq["id"] = id
	}

	body, _ := json.Marshal(rpcReq)
	req, err := http.NewRequestWithContext(ctx, "POST", c.serverURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	c.mu.RLock()
	if c.sessionID != "" {
		req.Header.Set("mcp-session-id", c.sessionID)
	}
	c.mu.RUnlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Capture session ID if present
	if sid := resp.Header.Get("mcp-session-id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}

	// Read full response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle success status codes (including 200, 202, etc.)
	if resp.StatusCode >= 300 || resp.StatusCode < 200 {
		return nil, fmt.Errorf("MCP server error (%d): %s", resp.StatusCode, string(respBody))
	}

	// 202 Accepted is often used for notifications or async processing
	if resp.StatusCode == http.StatusAccepted {
		if id == nil {
			return nil, nil // Notification accepted
		}
		// If it's a request but returned 202, it might still have a body or we might need to wait
	}

	// Notifications don't expect a result body in some implementations, but let's check
	if id == nil {
		return nil, nil
	}

	// Check if response looks like SSE (starts with "event:" or "data:")
	bodyStr := strings.TrimSpace(string(respBody))
	if strings.HasPrefix(bodyStr, "event:") || strings.HasPrefix(bodyStr, "data:") {
		return c.parseSSEResponse(respBody)
	}

	// Check Content-Type for SSE
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return c.parseSSEResponse(respBody)
	}

	// Standard JSON-RPC response
	var rpcResp struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ID interface{} `json:"id"`
	}

	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		// Try SSE parsing as fallback
		if sseResult, sseErr := c.parseSSEResponse(respBody); sseErr == nil {
			return sseResult, nil
		}
		return nil, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(respBody[:min(200, len(respBody))]))
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("MCP error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// parseSSEResponse extracts JSON data from SSE format
func (c *Client) parseSSEResponse(body []byte) (json.RawMessage, error) {
	// Normalize line endings
	content := strings.ReplaceAll(string(body), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	var fullData strings.Builder
	foundData := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)
			if data == "" {
				continue
			}
			fullData.WriteString(data)
			foundData = true

			// Stop if we found a complete JSON object in one or more data chunks
			current := fullData.String()
			if json.Valid([]byte(current)) {
				break
			}
		}
	}

	if !foundData {
		return nil, fmt.Errorf("no data found in SSE response (body: %s)", string(body[:min(100, len(body))]))
	}

	dataStr := fullData.String()
	// Try to parse as JSON-RPC response
	var rpcResp struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ID interface{} `json:"id"`
	}

	if err := json.Unmarshal([]byte(dataStr), &rpcResp); err == nil {
		if rpcResp.Error != nil {
			return nil, fmt.Errorf("MCP error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
		}
		return rpcResp.Result, nil
	}

	// Maybe it's just the result directly
	return json.RawMessage(dataStr), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
