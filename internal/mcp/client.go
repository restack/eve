// Package mcp provides a client for the Model Context Protocol.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	return nil
}

// ListTools retrieves the available tools from the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]*tools.Tool, error) {
	resp, err := c.callRPC(ctx, "tools/list", nil)
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

// callRPC is a helper for JSON-RPC over HTTP.
func (c *Client) callRPC(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	rpcReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	}

	body, _ := json.Marshal(rpcReq)
	req, err := http.NewRequestWithContext(ctx, "POST", c.serverURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCP server error (%d): %s", resp.StatusCode, string(data))
	}

	var rpcResp struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ID interface{} `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, err
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("MCP error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}
