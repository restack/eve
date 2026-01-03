// Package agent provides the agentic loop that orchestrates LLM and tool execution.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/restack/eve/internal/config"
	"github.com/restack/eve/internal/llm"
	"github.com/restack/eve/internal/tools"
)

const systemPrompt = `You are Eve, a Kubernetes operations assistant running inside a cluster.
You help SRE and Platform engineers with cluster operations through Slack.

Your capabilities:
- Query Kubernetes resources (pods, deployments, nodes, events, namespaces)
- Check rollout status and scale deployments
- Create GitHub issues for incident tracking
- Trigger Argo Workflows for remediation recipes

Guidelines:
- Be concise and factual. No personality or chattiness.
- When users ask about cluster state, use the appropriate kubernetes.* tools
- For destructive operations (scaling, workflow triggers), confirm intent first
- Format responses using Slack mrkdwn (use *bold*, backticks for code)
- If you cannot help, say so clearly

Available tools are provided in your context. Call them as needed to answer user queries.`

// Agent orchestrates conversations between users and tools via LLM
type Agent struct {
	llmClient llm.Client
	registry  *tools.Registry
	cfg       *config.Config
	toolDefs  []llm.ToolDefinition
}

// NewAgent creates a new agent
func NewAgent(llmClient llm.Client, registry *tools.Registry, cfg *config.Config) *Agent {
	return &Agent{
		llmClient: llmClient,
		registry:  registry,
		cfg:       cfg,
		toolDefs:  llm.ConvertToolsToDefinitions(registry),
	}
}

// Request represents a user request with context
type Request struct {
	UserID    string
	ChannelID string
	Message   string
	ThreadTS  string // For threaded conversations
}

// Response represents the agent's response
type Response struct {
	Text    string
	Actions []tools.Action
}

// Process handles a user request through the agentic loop
func (a *Agent) Process(ctx context.Context, req *Request) (*Response, error) {
	slog.Info("agent processing request",
		"user", req.UserID,
		"channel", req.ChannelID,
		"message", req.Message,
	)

	// Build initial conversation
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: req.Message},
	}

	// Agentic loop - continue until we get a final response (no tool calls)
	maxIterations := 10
	for i := 0; i < maxIterations; i++ {
		chatReq := &llm.ChatRequest{
			Messages: messages,
			Tools:    a.toolDefs,
		}

		resp, err := a.llmClient.Chat(ctx, chatReq)
		if err != nil {
			return nil, fmt.Errorf("llm chat failed: %w", err)
		}

		// Add assistant message to history
		messages = append(messages, resp.Message)

		// Check if we're done (no tool calls)
		if resp.FinishReason != "tool_calls" || len(resp.Message.ToolCalls) == 0 {
			slog.Info("agent completed", "iterations", i+1)
			return &Response{
				Text: resp.Message.Content,
			}, nil
		}

		// Process tool calls
		slog.Info("processing tool calls", "count", len(resp.Message.ToolCalls))

		for _, tc := range resp.Message.ToolCalls {
			toolResult := a.executeTool(ctx, req, tc)
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	return nil, fmt.Errorf("max iterations reached without completion")
}

// executeTool executes a single tool call and returns the result
func (a *Agent) executeTool(ctx context.Context, req *Request, tc llm.ToolCall) string {
	toolName := tc.Function.Name

	slog.Info("executing tool",
		"tool", toolName,
		"user", req.UserID,
	)

	// Check if tool exists
	tool, ok := a.registry.Get(toolName)
	if !ok {
		return fmt.Sprintf("Error: Tool '%s' not found", toolName)
	}

	// Check permissions for destructive tools
	if tool.IsDestructive {
		if !a.cfg.IsUserAllowed(req.UserID) {
			slog.Warn("unauthorized user attempted destructive operation",
				"user", req.UserID,
				"tool", toolName,
			)
			return fmt.Sprintf("Error: User not authorized for destructive operation '%s'", toolName)
		}
		if !a.cfg.IsChannelAllowed(req.ChannelID) {
			slog.Warn("unauthorized channel for destructive operation",
				"channel", req.ChannelID,
				"tool", toolName,
			)
			return fmt.Sprintf("Error: Channel not authorized for destructive operation '%s'", toolName)
		}
	}

	// Execute the tool
	result, err := a.registry.Execute(ctx, toolName, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		slog.Error("tool execution error", "tool", toolName, "error", err)
		return fmt.Sprintf("Error executing tool: %v", err)
	}

	// Format result for LLM context
	if result.Success {
		if result.Data != nil {
			dataJSON, _ := json.Marshal(result.Data)
			return fmt.Sprintf("%s\n\nData: %s", result.Output, string(dataJSON))
		}
		return result.Output
	}
	return fmt.Sprintf("Error: %s", result.Error)
}

// GetToolsSummary returns a summary of available tools for display
func (a *Agent) GetToolsSummary() string {
	var sb strings.Builder
	sb.WriteString("*Available Tools*\n\n")

	for _, name := range a.registry.List() {
		tool, _ := a.registry.Get(name)
		marker := ""
		if tool.IsDestructive {
			marker = " 🔴"
		}
		sb.WriteString(fmt.Sprintf("• `%s`%s - %s\n", tool.Name, marker, tool.Description))
	}

	return sb.String()
}
