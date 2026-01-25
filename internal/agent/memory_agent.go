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

func NewMemoryAgent(llmClient llm.Client, registry *tools.Registry, memStore memory.MemoryStore, cfg *config.Config) Agent {
	return &MemoryAgent{
		llmClient: llmClient, registry: registry, memory: memStore, cfg: cfg,
		toolDefs: llm.ConvertToolsToDefinitions(registry),
	}
}

func (a *MemoryAgent) Process(ctx context.Context, req *Request) (*Response, error) {
	slog.Info("memory agent processing request", "user", req.UserID, "message", req.Message)
	if err := a.ensureSession(ctx, req); err != nil {
		slog.Warn("session fail", "error", err)
	}
	a.recordChatMessage(ctx, "user", req.Message, req)

	history, _ := a.memory.GetSessionObservations(ctx, a.sessionID)
	sort.Slice(history, func(i, j int) bool { return history[i].CreatedAt.Before(history[j].CreatedAt) })
	if len(history) > 10 {
		history = history[len(history)-10:]
	}

	messages := a.enrichMessages(req, nil, history)
	content, toolCalls, err := a.runAgentLoop(ctx, req, messages, a.toolDefs)
	if err != nil {
		return nil, err
	}

	for _, tc := range toolCalls {
		a.recordToolExecution(ctx, tc, req)
	}
	a.recordChatMessage(ctx, "assistant", content, req)
	return &Response{Text: content, ToolCalls: toolCalls}, nil
}

func (a *MemoryAgent) runAgentLoop(ctx context.Context, req *Request, messages []llm.Message, selectedTools []llm.ToolDefinition) (string, []*ToolCallRecord, error) {
	var toolCalls []*ToolCallRecord
	maxIterations := 10
	requireTool := req.Mode == "sre" || isSREAndInfraRelated(req.Message)
	askedForAnalysis := false // Track if we've already asked for post-tool analysis

	for i := 0; i < maxIterations; i++ {
		// After tool execution is complete, don't offer tools anymore to force text response
		chatTools := selectedTools
		if len(toolCalls) > 0 && askedForAnalysis {
			chatTools = nil // No more tools after analysis prompt
		}

		chatReq := &llm.ChatRequest{Messages: messages, Tools: chatTools, ToolChoice: "auto"}
		resp, err := a.llmClient.Chat(ctx, chatReq)
		if err != nil {
			return "", toolCalls, fmt.Errorf("llm chat failed: %w", err)
		}

		nativeTCs := resp.Message.ToolCalls
		promptTCs := ParseToolCallsFromText(resp.Message.Content)
		for _, ptc := range promptTCs {
			nativeTCs = append(nativeTCs, llm.ToolCall{
				ID:       fmt.Sprintf("mp%d-%x", i, time.Now().UnixNano()),
				Type:     "function",
				Function: llm.FunctionCall{Name: ptc.ToolName, Arguments: ptc.Arguments},
			})
		}

		// If we've already executed tools and asked for analysis, return the text response
		if len(toolCalls) > 0 && askedForAnalysis {
			slog.Debug("post-tool analysis response", "raw_content", truncateForLog(resp.Message.Content, 500))
			content := StripToolCallMarkers(resp.Message.Content)
			slog.Debug("post-tool analysis stripped", "content", truncateForLog(content, 500))
			if content == "" {
				slog.Warn("LLM returned empty analysis, retrying once more")
				// Try one more time with a simpler prompt
				messages = append(messages, resp.Message)
				messages = append(messages, llm.Message{Role: "user", Content: "위의 도구 결과를 보고 핵심 내용만 요약해서 알려주세요. 반드시 텍스트로 응답하세요."})
				continue
			}
			return content, toolCalls, nil
		}

		if len(nativeTCs) > 0 {
			// If we already have tool results, don't call more tools - ask for final response
			if len(toolCalls) > 0 {
				// Don't add the LLM's response (which contains tool calls) - it creates confusing history
				// Just ask for analysis directly
				finalMsg := buildPostToolAnalysisPrompt(req.Message)
				messages = append(messages, llm.Message{Role: "user", Content: finalMsg})
				askedForAnalysis = true
				continue
			}

			resp.Message.Content = StripToolCallMarkers(resp.Message.Content)
			if resp.Message.Content == "" {
				resp.Message.Content = "(Tool calling...)"
			}
		}
		messages = append(messages, resp.Message)

		if len(nativeTCs) == 0 {
			// Detect hallucination patterns - expanded list
			content := resp.Message.Content
			isHallucination := strings.Contains(content, "<details>") ||
				strings.Contains(content, "<summary>") ||
				strings.Contains(content, "{ \"status\"") ||
				strings.Contains(content, "{\"status\"") ||
				strings.Contains(content, "확인해드릴게요") ||
				strings.Contains(content, "확인해드리겠습니다") ||
				strings.Contains(content, "확인해볼게요") ||
				strings.Contains(content, "조회할게요") ||
				strings.Contains(content, "조회해볼게요") ||
				strings.Contains(content, "실행할게요") ||
				strings.Contains(content, "확인하는 중") ||
				strings.Contains(content, "조회하는 중") ||
				strings.Contains(content, "kubectl ") ||
				strings.Contains(content, "```bash") ||
				strings.Contains(content, "```shell")

			if requireTool && len(selectedTools) > 0 && i < 3 {
				// Use language-appropriate retry message
				var retryMsg string
				if isKorean(req.Message) {
					if isHallucination {
						retryMsg = "HALLUCINATION DETECTED. 가짜 데이터나 kubectl 명령어를 출력하지 마세요. 반드시 도구를 호출해야 합니다. 형식: <function=pods_list_in_namespace><parameter=namespace>YOUR_NAMESPACE</parameter></function>"
					} else {
						retryMsg = "ERROR: 도구 호출이 감지되지 않았습니다. SRE 요청에는 반드시 도구를 호출해야 합니다. 형식: <function=pods_list_in_namespace><parameter=namespace>YOUR_NAMESPACE</parameter></function>"
					}
				} else {
					if isHallucination {
						retryMsg = "HALLUCINATION DETECTED. Do not output fake data or kubectl commands. You MUST call a tool. Format: <function=pods_list_in_namespace><parameter=namespace>YOUR_NAMESPACE</parameter></function>"
					} else {
						retryMsg = "ERROR: No tool call detected. For SRE requests, you MUST call a tool. Format: <function=pods_list_in_namespace><parameter=namespace>YOUR_NAMESPACE</parameter></function>"
					}
				}
				messages = append(messages, llm.Message{Role: "user", Content: retryMsg})
				continue
			}
			// If still no tool call after retries, return error message instead of hallucination
			if requireTool && (len(toolCalls) == 0 || isHallucination) {
				errMsg := "Tool call failed. Please try again."
				if isKorean(req.Message) {
					errMsg = "도구 호출에 실패했습니다. 다시 시도해 주세요."
				}
				return errMsg, toolCalls, nil
			}
			return resp.Message.Content, toolCalls, nil
		}

		for _, tc := range nativeTCs {
			start := time.Now()
			toolResult, toolSource, actualName, success := a.executeTool(ctx, req, tc)
			// Preprocess tool result to highlight errors/warnings for better LLM analysis
			processedResult := PreprocessToolResult(actualName, toolResult)
			messages = append(messages, llm.Message{Role: "tool", Content: processedResult, ToolCallID: tc.ID, Name: tc.Function.Name})
			toolCalls = append(toolCalls, &ToolCallRecord{
				ToolName: actualName, ToolSource: toolSource, Input: tc.Function.Arguments,
				Result: toolResult, Success: success, Duration: time.Since(start),
			})
		}
	}
	return "Max iterations.", toolCalls, nil
}

func (a *MemoryAgent) executeTool(ctx context.Context, req *Request, tc llm.ToolCall) (string, string, string, bool) {
	toolName := tc.Function.Name
	tool, ok := a.registry.Get(toolName)
	if !ok {
		for _, n := range a.registry.List() {
			if strings.EqualFold(strings.ReplaceAll(n, ".", "_"), toolName) || strings.HasSuffix(strings.ToLower(n), strings.ToLower(toolName)) {
				if t, found := a.registry.Get(n); found {
					tool = t
					ok = true
					break
				}
			}
		}
	}
	if !ok {
		return fmt.Sprintf("Error: Tool '%s' not found.", toolName), "", toolName, false
	}

	result, err := a.registry.Execute(ctx, tool.Name, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		return fmt.Sprintf("Error: %v", err), tool.Source, tool.Name, false
	}

	output := result.Output
	if result.Data != nil {
		dataJSON, _ := json.Marshal(result.Data)
		output = fmt.Sprintf("%s\n\nData: %s", result.Output, string(dataJSON))
	}
	return output, tool.Source, tool.Name, result.Success
}

func (a *MemoryAgent) recordChatMessage(ctx context.Context, role, content string, req *Request) {
	_ = a.memory.Store(ctx, &memory.Observation{
		Type: memory.ObservationTypeChatMessage, SessionID: a.sessionID, ChannelID: req.ChannelID, UserID: req.UserID,
		Content: content, Metadata: memory.ObservationMetadata{Role: role},
	})
}

func (a *MemoryAgent) recordToolExecution(ctx context.Context, tc *ToolCallRecord, req *Request) {
	_ = a.memory.Store(ctx, &memory.Observation{
		Type: memory.ObservationTypeToolExecution, SessionID: a.sessionID, ChannelID: req.ChannelID, UserID: req.UserID,
		Content: tc.Result, Metadata: memory.ObservationMetadata{ToolName: tc.ToolName, Success: tc.Success},
	})
}

func (a *MemoryAgent) ensureSession(ctx context.Context, req *Request) error {
	id := req.ChannelID
	if req.ThreadTS != "" {
		id += ":" + req.ThreadTS
	}
	a.sessionID = id
	session, _ := a.memory.GetSession(ctx, id)
	if session == nil {
		return a.memory.CreateSession(ctx, &memory.Session{ID: id, StartedAt: time.Now().UTC(), ChannelID: req.ChannelID, UserID: req.UserID})
	}
	session.MessageCount++
	return a.memory.UpdateSession(ctx, session)
}

func (a *MemoryAgent) enrichMessages(req *Request, _ *memory.SearchResult, history []*memory.Observation) []llm.Message {
	// Build dynamic tool prompt with available tools
	var toolNames []string
	for _, n := range a.registry.List() {
		toolNames = append(toolNames, n)
	}
	toolPrompt := GenerateDynamicToolPrompt(toolNames)

	messages := []llm.Message{{Role: "system", Content: systemPrompt + toolPrompt}}

	// Add thread context if available (messages from the Slack thread)
	if len(req.ThreadContext) > 0 {
		var contextBuilder strings.Builder
		contextBuilder.WriteString("## Thread Context (previous messages in this thread):\n")
		for i, msg := range req.ThreadContext {
			contextBuilder.WriteString(fmt.Sprintf("[%d] %s\n", i+1, msg))
		}
		contextBuilder.WriteString("\n---\nUse the above thread context to understand what the user is referring to.\n")
		messages = append(messages, llm.Message{Role: "user", Content: contextBuilder.String()})
		messages = append(messages, llm.Message{Role: "assistant", Content: "I understand the thread context. I'll use it to answer your question."})
	}

	// Add session history from memory
	for _, obs := range history {
		if obs.Metadata.Role != "" {
			messages = append(messages, llm.Message{Role: obs.Metadata.Role, Content: obs.Content})
		}
	}
	messages = append(messages, llm.Message{Role: "user", Content: req.Message})
	return messages
}

func (a *MemoryAgent) GetToolsSummary() string {
	var sb strings.Builder
	for _, n := range a.registry.List() {
		t, _ := a.registry.Get(n)
		sb.WriteString("- " + t.Name + "\n")
	}
	return sb.String()
}
