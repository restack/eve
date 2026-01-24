// Package agent provides the agentic loop that orchestrates LLM and tool execution.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/restack/eve/internal/config"
	"github.com/restack/eve/internal/llm"
	"github.com/restack/eve/internal/memory"
	"github.com/restack/eve/internal/tools"
)

// MemoryAgent is an agent with integrated memory capabilities
type MemoryAgent struct {
	llmClient llm.Client
	registry  *tools.Registry
	memory    memory.MemoryStore
	cfg       *config.Config
	toolDefs  []llm.ToolDefinition

	sessionID string
}

// NewMemoryAgent creates a new memory-aware agent
func NewMemoryAgent(
	llmClient llm.Client,
	registry *tools.Registry,
	memStore memory.MemoryStore,
	cfg *config.Config,
) Agent {
	return &MemoryAgent{
		llmClient: llmClient,
		registry:  registry,
		memory:    memStore,
		cfg:       cfg,
		toolDefs:  llm.ConvertToolsToDefinitions(registry),
	}
}

// Process handles a user request with memory context
func (a *MemoryAgent) Process(ctx context.Context, req *Request) (*Response, error) {
	slog.Info("memory agent processing request",
		"user", req.UserID,
		"channel", req.ChannelID,
		"message", req.Message,
	)

	// Ensure session
	if err := a.ensureSession(ctx, req); err != nil {
		slog.Warn("failed to ensure session", "error", err)
	}

	// 1. Record incoming user message
	a.recordChatMessage(ctx, "user", req.Message, req)

	// 2. Search relevant long-term memory
	memories, err := a.searchMemory(ctx, req.Message, req.ChannelID)
	if err != nil {
		slog.Warn("memory search failed", "error", err)
	}

	// 3. Fetch session history (STM)
	history, err := a.memory.GetSessionObservations(ctx, a.sessionID)
	if err != nil {
		slog.Warn("failed to fetch session history", "error", err)
	} else {
		// Sort by time
		sort.Slice(history, func(i, j int) bool {
			return history[i].CreatedAt.Before(history[j].CreatedAt)
		})
		// Limit history to last 10 messages to prevent context bloat
		if len(history) > 10 {
			history = history[len(history)-10:]
		}
	}

	// 4. Enrich messages with memory context and history
	messages := a.enrichMessages(req, memories, history)

	// 5. Run agent loop
	response, toolCalls, err := a.runAgentLoop(ctx, req, messages)
	if err != nil {
		return nil, err
	}

	// 6. Record tool executions
	for _, tc := range toolCalls {
		a.recordToolExecution(ctx, tc, req)
	}

	// 7. Record assistant response
	a.recordChatMessage(ctx, "assistant", response, req)

	return &Response{Text: response}, nil
}

func (a *MemoryAgent) searchMemory(ctx context.Context, query, channelID string) (*memory.SearchResult, error) {
	opts := memory.SearchOptions{
		Limit:          4, // Increase limit to 4 for better knowledge retrieval
		MinScore:       0.7,
		ChannelID:      channelID,
		IncludeContent: false,
	}

	return a.memory.Search(ctx, query, opts)
}

func (a *MemoryAgent) recordChatMessage(ctx context.Context, role, content string, req *Request) {
	obs := &memory.Observation{
		Type:      memory.ObservationTypeChatMessage,
		SessionID: a.sessionID,
		ChannelID: req.ChannelID,
		UserID:    req.UserID,
		Title:     fmt.Sprintf("Chat: %s", role),
		Content:   content,
		Metadata: memory.ObservationMetadata{
			Role: role,
		},
	}

	if err := a.memory.Store(ctx, obs); err != nil {
		slog.Warn("failed to record chat message", "error", err)
	}
}

func (a *MemoryAgent) enrichMessages(req *Request, memories *memory.SearchResult, history []*memory.Observation) []llm.Message {
	// Start with system prompt
	systemContent := systemPrompt

	// Add LTM context if available
	if memories != nil && len(memories.Observations) > 0 {
		var memCtx strings.Builder
		memCtx.WriteString("\n\n## Relevant Past Context\n")
		memCtx.WriteString("The following information from past interactions may be relevant:\n\n")

		for _, obs := range memories.Observations {
			// Skip current session messages in LTM search to avoid duplication
			if obs.SessionID == a.sessionID {
				continue
			}
			memCtx.WriteString(fmt.Sprintf("- **[%s]** %s (relevance: %.0f%%)\n",
				obs.CreatedAt.Format("2006-01-02"),
				obs.Summary,
				obs.Score*100,
			))
		}

		memCtx.WriteString("\nUse this context if helpful, but prioritize current information.\n")
		systemContent += memCtx.String()
	}

	messages := []llm.Message{
		{Role: "system", Content: systemContent},
	}

	// Add Slack thread context if available (from Slack API)
	// This is the actual thread content that Eve was mentioned in
	if len(req.ThreadContext) > 0 {
		var threadCtx strings.Builder
		threadCtx.WriteString("## Thread Context (Previous messages in this Slack thread):\n")
		for _, msg := range req.ThreadContext {
			threadCtx.WriteString(msg + "\n---\n")
		}
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: threadCtx.String(),
		})
		messages = append(messages, llm.Message{
			Role:    "assistant",
			Content: "I've reviewed the thread context above. How can I help with this?",
		})
	}

	// Add Conversation History
	// Track if the current message is already in history to avoid duplication
	currentMsgInHistory := false
	for _, obs := range history {
		role := obs.Metadata.Role
		if role == "" {
			continue
		}

		messages = append(messages, llm.Message{
			Role:    role,
			Content: obs.Content,
		})

		if role == "user" && obs.Content == req.Message {
			currentMsgInHistory = true
		}
	}

	// Always ensure the current message is at the end if not already found in history
	if !currentMsgInHistory {
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: req.Message,
		})
	}

	return messages
}

// ToolCallRecord represents a recorded tool call
type ToolCallRecord struct {
	ToolName string
	Input    string
	Result   string
	Success  bool
	Duration time.Duration
}

func (a *MemoryAgent) selectTools(message string) []llm.ToolDefinition {
	msg := strings.ToLower(message)

	// Keywords for each category
	categories := map[string][]string{
		"kubernetes": {"pod", "k8s", "deployment", "node", "namespace", "pvc", "service", "ingress", "rollout", "restart", "logs"},
		"aws":        {"aws", "billing", "cost", "vpc", "network", "trace", "cloudtrail", "iam", "s3", "ec2", "rds"},
		"incident":   {"incident", "issue", "github", "error", "failed", "crash", "outage", "problem"},
		"argo":       {"workflow", "argo", "trigger", "remediate", "recipe"},
	}

	neededCategories := make(map[string]bool)
	hasSreKeyword := false

	// Simple keyword matching
	for cat, keywords := range categories {
		for _, kw := range keywords {
			if strings.Contains(msg, kw) {
				neededCategories[cat] = true
				hasSreKeyword = true
				break
			}
		}
	}

	// If no SRE keywords, it's likely a casual conversation. Return empty tools.
	if !hasSreKeyword {
		slog.Info("casual conversation detected, withholding tool schemas")
		return []llm.ToolDefinition{}
	}

	// If it's an SRE query, we can either return all tools OR filter them.
	// For now, let's return all tools if any keyword is matched to ensure capability,
	// but we could filter by category name prefix as well.
	slog.Info("SRE query detected, providing tool schemas", "matched_categories", len(neededCategories))
	return a.toolDefs
}

func (a *MemoryAgent) runAgentLoop(ctx context.Context, req *Request, messages []llm.Message) (string, []*ToolCallRecord, error) {
	var toolCalls []*ToolCallRecord

	// Select relevant tools based on the message content
	selectedTools := a.selectTools(req.Message)

	maxIterations := 10
	for i := 0; i < maxIterations; i++ {
		chatReq := &llm.ChatRequest{
			Messages: messages,
			Tools:    selectedTools,
		}

		resp, err := a.llmClient.Chat(ctx, chatReq)
		if err != nil {
			return "", toolCalls, fmt.Errorf("llm chat failed: %w", err)
		}

		// Add assistant message to history
		messages = append(messages, resp.Message)

		// Check if we have tool calls to process
		if len(resp.Message.ToolCalls) > 0 {
			// Continue to tool processing
		} else {
			// No tool calls, check if the LLM is "hallucinating" tool usage as text
			content := strings.ToLower(resp.Message.Content)
			if (strings.Contains(content, "kubectl ") || strings.Contains(content, "aws ")) && selectedTools != nil {
				slog.Warn("LLM outputted commands as text instead of tool calls, retrying with feedback")
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: "I see you wrote some commands as text. Please use the PROVIDED TOOLS instead of writing them as markdown code blocks. I cannot execute text. Do NOT say you will do it, just call the tools now.",
				})
				continue
			}

			// Genuinely finished
			slog.Info("memory agent completed", "iterations", i+1)
			return resp.Message.Content, toolCalls, nil
		}

		// Process tool calls
		slog.Info("processing tool calls", "count", len(resp.Message.ToolCalls))

		for _, tc := range resp.Message.ToolCalls {
			start := time.Now()
			toolResult, success := a.executeTool(ctx, req, tc)
			duration := time.Since(start)

			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})

			toolCalls = append(toolCalls, &ToolCallRecord{
				ToolName: tc.Function.Name,
				Input:    tc.Function.Arguments,
				Result:   toolResult,
				Success:  success,
				Duration: duration,
			})
		}
	}

	return "", toolCalls, fmt.Errorf("max iterations reached without completion")
}

func (a *MemoryAgent) executeTool(ctx context.Context, req *Request, tc llm.ToolCall) (string, bool) {
	toolName := tc.Function.Name

	slog.Info("executing tool",
		"tool", toolName,
		"user", req.UserID,
	)

	// Check if tool exists
	tool, ok := a.registry.Get(toolName)
	if !ok {
		return fmt.Sprintf("Error: Tool '%s' not found", toolName), false
	}

	// Check permissions for destructive tools
	if tool.IsDestructive {
		if !a.cfg.IsUserAllowed(req.UserID) {
			slog.Warn("unauthorized user attempted destructive operation",
				"user", req.UserID,
				"tool", toolName,
			)
			return fmt.Sprintf("Error: User not authorized for destructive operation '%s'", toolName), false
		}
		if !a.cfg.IsChannelAllowed(req.ChannelID) {
			slog.Warn("unauthorized channel for destructive operation",
				"channel", req.ChannelID,
				"tool", toolName,
			)
			return fmt.Sprintf("Error: Channel not authorized for destructive operation '%s'", toolName), false
		}
	}

	// Execute the tool
	result, err := a.registry.Execute(ctx, toolName, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		slog.Error("tool execution error", "tool", toolName, "error", err)
		return fmt.Sprintf("Error executing tool: %v", err), false
	}

	// Format result
	if result.Success {
		if result.Data != nil {
			dataJSON, _ := json.Marshal(result.Data)
			return fmt.Sprintf("%s\n\nData: %s", result.Output, string(dataJSON)), true
		}
		return result.Output, true
	}
	return fmt.Sprintf("Error: %s", result.Error), false
}

func (a *MemoryAgent) recordToolExecution(ctx context.Context, tc *ToolCallRecord, req *Request) {
	obs := &memory.Observation{
		Type:      memory.ObservationTypeToolExecution,
		Category:  extractCategory(tc.ToolName),
		SessionID: a.sessionID,
		ChannelID: req.ChannelID,
		UserID:    req.UserID,
		Title:     fmt.Sprintf("Tool: %s", tc.ToolName),
		Content:   tc.Result,
		Metadata: memory.ObservationMetadata{
			ToolName:   tc.ToolName,
			ToolInput:  tc.Input,
			ToolOutput: tc.Result,
			Success:    tc.Success,
			Duration:   tc.Duration.Milliseconds(),
		},
		Technologies: extractTechnologies(tc.ToolName, tc.Result),
	}

	if err := a.memory.Store(ctx, obs); err != nil {
		slog.Warn("failed to record tool execution", "error", err)
	}
}

func (a *MemoryAgent) ensureSession(ctx context.Context, req *Request) error {
	// Generate session ID based on channel + thread
	sessionKey := req.ChannelID
	if req.ThreadTS != "" {
		sessionKey += ":" + req.ThreadTS
	}

	a.sessionID = sessionKey

	// Try to get existing session
	session, err := a.memory.GetSession(ctx, sessionKey)
	if err != nil || session == nil {
		// Create new session
		session = &memory.Session{
			ID:        sessionKey,
			StartedAt: time.Now().UTC(),
			ChannelID: req.ChannelID,
			UserID:    req.UserID,
			ThreadTS:  req.ThreadTS,
		}
		return a.memory.CreateSession(ctx, session)
	}

	// Update existing session
	session.MessageCount++
	return a.memory.UpdateSession(ctx, session)
}

// RecordIncident records an incident to memory
func (a *MemoryAgent) RecordIncident(ctx context.Context, incident *Incident, req *Request) error {
	obs := &memory.Observation{
		Type:      memory.ObservationTypeIncident,
		Category:  "incident",
		SessionID: a.sessionID,
		ChannelID: req.ChannelID,
		UserID:    req.UserID,
		Title:     incident.Title,
		Content:   incident.Description,
		Metadata: memory.ObservationMetadata{
			Severity:     incident.Severity,
			Namespace:    incident.Namespace,
			Resource:     incident.Resource,
			ResourceKind: incident.ResourceKind,
			Resolution:   incident.Resolution,
			MTTR:         incident.MTTRMinutes,
		},
		Technologies: incident.Technologies,
	}

	return a.memory.Store(ctx, obs)
}

// Incident represents an incident to be recorded
type Incident struct {
	Title        string
	Description  string
	Severity     string
	Namespace    string
	Resource     string
	ResourceKind string
	Resolution   string
	MTTRMinutes  int64
	Technologies []string
}

// GetToolsSummary returns a summary of available tools
func (a *MemoryAgent) GetToolsSummary() string {
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

// Helper functions

func extractCategory(toolName string) string {
	parts := strings.Split(toolName, ".")
	if len(parts) > 0 {
		return parts[0]
	}
	return "general"
}

func extractTechnologies(toolName, output string) []string {
	techs := make(map[string]bool)

	// Extract from tool name
	if strings.HasPrefix(toolName, "kubernetes.") {
		techs["kubernetes"] = true
	}
	if strings.HasPrefix(toolName, "github.") {
		techs["github"] = true
	}
	if strings.HasPrefix(toolName, "argo.") {
		techs["argo"] = true
	}

	// Extract from output
	keywords := map[string]string{
		"deployment": "kubernetes",
		"pod":        "kubernetes",
		"service":    "kubernetes",
		"configmap":  "kubernetes",
		"secret":     "kubernetes",
		"ingress":    "kubernetes",
		"prometheus": "prometheus",
		"grafana":    "grafana",
		"oomkilled":  "memory-issue",
		"crashloop":  "crashloop",
		"postgres":   "postgresql",
		"redis":      "redis",
		"kafka":      "kafka",
	}

	lowerOutput := strings.ToLower(output)
	for keyword, tech := range keywords {
		if strings.Contains(lowerOutput, keyword) {
			techs[tech] = true
		}
	}

	result := make([]string, 0, len(techs))
	for tech := range techs {
		result = append(result, tech)
	}
	return result
}
