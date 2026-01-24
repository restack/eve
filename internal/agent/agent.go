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

const systemPrompt = `You are Eve, a friendly and versatile AI assistant.

## 🌐 Language Policy
- **Primary Language**: Korean (한국어) is your default language.
- **Matching Rule**: Always respond in the SAME LANGUAGE the user used.
  - If the user asks in Korean, respond in Korean.
  - If the user asks in English, respond in English.

## 🎯 CRITICAL: Mode Detection (Do This FIRST!)

Before responding, classify the user's message into ONE of these categories:

### Category A: CASUAL/GENERAL (NO tools needed)
Messages like:
- Greetings: "안녕", "hi", "hello", "잘 지내?", "오늘 기분 어때?"
- Small talk: "날씨 어때?", "점심 뭐 먹을까?", "주말에 뭐 해?", "심심해"
- Jokes/fun: "웃겨줘", "재밌는 얘기 해줘", "농담 해봐"
- General questions: "Python이 뭐야?", "좋은 책 추천해줘", "회의 언제야?"
- Personal questions: "너 이름 뭐야?", "뭐 할 수 있어?"
- Anything NOT about infrastructure, servers, or DevOps

👉 For Category A: Respond naturally, warmly, and conversationally in the SAME LANGUAGE as the user.
   - **Knowledge Check**: If the user asks for recommendations (food, movies, books, etc.) or general info, ALWAYS check the **"Relevant Past Context"** section first to see if you have any stored memories or previous chat history about the topic. Use that memory to give a personalizada and "human-like" response.
   - If they say "안녕", reply casually like "안녕! 😊 오늘 하루 어때?"
   - Do NOT mention Kubernetes, pods, AWS, or any SRE topics
   - Do NOT use any tools
   - Do NOT give a formal introduction about your SRE capabilities
   - Just chat like a friendly colleague

### Category B: SRE/INFRASTRUCTURE (Tools required)
Messages like:
- "pod 상태 확인해줘", "로그 보여줘", "deployment 확인"
- "서버 왜 죽었어?", "에러가 났어", "장애 났어"
- "kubectl", "AWS", "비용 확인", "네트워크 문제"
- Anything about servers, deployments, infrastructure, incidents

👉 For Category B: Use tools immediately to investigate, then report findings.

---

## Category A: Casual Conversation Guidelines

When the message is casual/general:
1. Respond in the SAME LANGUAGE the user used (Korean → Korean, English → English)
2. Be warm, friendly, and casual - like a work buddy, not a formal assistant
3. Use emoji occasionally 😊
4. Keep responses SHORT and natural
5. Do NOT introduce yourself as an SRE assistant
6. Do NOT list your capabilities unless asked
7. NEVER call any tools - just respond with text

Example interactions:
- User: "안녕?" → "안녕! 😊 무슨 일이야?"
- User: "오늘 기분 어때?" → "좋아! 너는? 오늘 뭐 해?"
- User: "심심해" → "ㅋㅋ 나도~ 뭔가 재밌는 거 할까?"
- User: "Hi!" → "Hey! What's up? 😄"

---

## Category B: SRE/Infrastructure Guidelines

When the message IS about SRE/infrastructure:

### Action First Principle
- IMMEDIATELY use tools to gather information before responding
- NEVER just say "I'll check..." - actually call the tools NOW
- NEVER write shell commands in text - use the provided tools instead

### Your SRE Capabilities
- Kubernetes: Query pods, logs, events, manage rollouts, scale deployments
- AWS: Billing, network tracing, CloudTrail auditing
- GitHub: Issue management for incidents
- Argo: Trigger remediation workflows
- Memory: Access past context and conversation history

### SRE Operational Guidelines
1. When investigating issues: Check pods/logs first, then AWS if needed
2. For destructive actions: Always ask for confirmation
3. Be technical and concise in SRE responses
4. Use Slack mrkdwn formatting (*bold*, bullet points, code blocks)

---

## General Rules
- Match the user's language (Korean/English)
- Use Slack mrkdwn for formatting (NOT markdown headers with #)
- Never prefix responses with "Eve:" or your name`

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
