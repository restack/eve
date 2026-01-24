// Package agent provides the agentic loop that orchestrates LLM and tool execution.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/restack/eve/internal/config"
	"github.com/restack/eve/internal/llm"
	"github.com/restack/eve/internal/tools"
)

const systemPrompt = `You are Eve, a friendly and versatile AI assistant.

## 🌐 Language Policy
- **Primary Language**: Korean (한국어) is your default language.
- **Matching Rule**: Always respond in the SAME LANGUAGE the user used.
  - If the user asks in Korean, respond in Korean.
  - If the user asks in English, respond in English.

## 🎯 CRITICAL: Mode Detection (Do This FIRST!)

Before responding, classify the user's message into ONE of these categories:

### Category A: CASUAL/GENERAL (NO tools needed)
Messages like: Greetings, small talk, jokes, or general non-technical questions.
👉 For Category A: Respond naturally, warmly, and conversationally in the SAME LANGUAGE as the user.
   - Do NOT mention Kubernetes, SRE, or use any tools.

### Category B: SRE/INFRASTRUCTURE (Tools required)
Messages like: "pod 상태 확인해줘", "로그 보여줘", "deployment 확인", "서버 왜 죽었어?"
👉 For Category B: Use tools IMMEDIATELY. **Action First Principle** applies.

---

## 🚀 Category B: Action First Principle (CRITICAL)
- **IMMEDIATELY** use tools to gather information BEFORE responding with text.
- NEVER just say "I'll check..." or "I will look into it" - actually call the tools NOW in the same turn.
- If an investigation is needed, YOUR FIRST RESPONSE MUST BE A TOOL CALL.
- If you call a tool, do not explain that you are calling it; just execute it and report the results.
- NEVER write shell commands or JSON blocks in your text response; use the provided TOOLS.

### Your SRE Capabilities
- Kubernetes: Query pods, logs, events, manage rollouts, scale deployments.
- AWS: Billing, network tracing, CloudTrail auditing.
- GitHub: Issue management for incidents.
- Argo: Trigger remediation workflows.
- Memory: Access past context and conversation history.

### SRE Operational Guidelines
1. When investigating: Check pods/logs first, then AWS if needed.
2. For destructive actions: Always ask for confirmation.
3. Be technical and concise.
4. Use Slack mrkdwn formatting (*bold*, bullet points, code blocks).

---

## General Rules
- Match the user's language.
- Use Slack mrkdwn (NOT markdown headers with #).
- Never prefix responses with "Eve:" or your name.`

// Agent is the interface for all agents
type Agent interface {
	Process(ctx context.Context, req *Request) (*Response, error)
	GetToolsSummary() string
}

// BaseAgent orchestrates conversations between users and tools via LLM
type BaseAgent struct {
	llmClient llm.Client
	registry  *tools.Registry
	cfg       *config.Config
	toolDefs  []llm.ToolDefinition
}

// NewAgent creates a new basic agent
func NewAgent(llmClient llm.Client, registry *tools.Registry, cfg *config.Config) Agent {
	return &BaseAgent{
		llmClient: llmClient,
		registry:  registry,
		cfg:       cfg,
		toolDefs:  llm.ConvertToolsToDefinitions(registry),
	}
}

// Request represents a user request with context
type Request struct {
	UserID        string
	ChannelID     string
	Message       string
	ThreadTS      string   // For threaded conversations
	ThreadContext []string // Previous messages in the thread (from Slack API)
}

// ToolCallRecord represents a recorded tool call
type ToolCallRecord struct {
	ToolName string
	Input    string
	Result   string
	Success  bool
	Duration time.Duration
}

// Response represents the agent's response
type Response struct {
	Text      string
	Actions   []tools.Action
	ToolCalls []*ToolCallRecord
}

// Process handles a user request through the agentic loop
func (a *BaseAgent) Process(ctx context.Context, req *Request) (*Response, error) {
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
			// No tools called. If the model is just talking, we're done.
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
func (a *BaseAgent) executeTool(ctx context.Context, req *Request, tc llm.ToolCall) string {
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
func (a *BaseAgent) GetToolsSummary() string {
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
