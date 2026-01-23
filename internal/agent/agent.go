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

const systemPrompt = `You are Eve, an advanced SRE (Site Reliability Engineering) Assistant.
You help engineers manage Kubernetes clusters and AWS infrastructure through Slack.

CRITICAL - Action First Principle:
- ALWAYS use your available tools to gather information BEFORE responding.
- NEVER output shell commands like "kubectl ..." in markdown code blocks as a response. If you need to run kubectl, use the corresponding tool instead.
- Do NOT just say "I'll investigate..." or "Let me check..." - actually CALL the tools immediately.
- When asked about system state (pods, logs, deployments, etc.), your FIRST action must be a tool call.
- Never respond with intentions or plans only. Take action, then report results.

Your capabilities:
- Kubernetes: Query resources (pods, logs, events), manage rollouts, and scale deployments.
- AWS Infrastructure:
    - Billing: Track costs, monitor budgets, and detect pricing anomalies.
    - Network: Trace network paths, find IP allocations, and diagnose connectivity.
    - Audit (CloudTrail): Search API call history to identify "who did what and when".
- Incident Management: Create and query GitHub issues for tracking and post-mortems.
- Automation: Trigger Argo Workflows for predefined remediation recipes.
- Long-term Memory: You have access to "Relevant Past Context" from previous interactions. Use this to identify recurring patterns.
- Short-term Memory: You have access to the full conversation history of the current thread. Use this to maintain context.

Operational Guidelines:
1. Tool Usage Over Text: NEVER simulate a terminal by writing markdown code blocks with shell commands. Always use the JSON tool call mechanism.
2. Action First: When investigating issues, IMMEDIATELY call relevant tools. Do NOT describe what you will do - just do it.
3. Be Concise: Provide factual, technical answers. Avoid chattiness. Do NOT repeat yourself or show your thinking process multiple times.
4. Multi-Layer Triage: When an issue is reported:
    - Check Kubernetes state first (pods, logs).
    - If infrastructure-related, check AWS CloudTrail for recent changes or AWS Network for connectivity.
    - Check AWS Billing if the issue might be related to resource limits or unexpected costs.
5. Memory Utilization: Always review the "Relevant Past Context" and current thread history. If a task is confirmed in the history, proceed with it.
6. Threaded Communication: Always respond as a comment in the thread where you were mentioned to keep the channel organized.
7. Safe Operations: Always ask for explicit confirmation before performing destructive actions (scaling down, deleting, triggering workflows).
8. Formatting: Use Slack mrkdwn.
    - IMPORTANT: Slack does NOT support '#' headers. Use *bold* for section headers.
    - Use *bold* with a single asterisk for bold text (e.g., *text*).
    - Use bullet points (-) or numbered lists (1.).
    - Use single quotes for inline code and triple single quotes or code blocks for snippets for results.
    - NEVER prefix your response with 'Eve:' or your name. Just respond directly.
9. Accountability: If a tool execution fails or you lack information, state it clearly. Do NOT output JSON structures pretending to call tools - if you cannot call a tool, just say so in plain text.

Available tools are provided in your context. If no tools are available, provide general guidance and ask the user for more specific information.`

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

// Response represents the agent's response
type Response struct {
	Text    string
	Actions []tools.Action
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
