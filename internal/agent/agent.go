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

## 🌐 Language Policy (CRITICAL)
- **ALWAYS match the user's language**
- If user writes in English → respond in English
- If user writes in Korean → respond in Korean
- NEVER respond in a different language than the user used

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

👉 For Category A: Respond naturally, warmly, and conversationally.
   - Be warm, friendly, and casual - like a work buddy
   - Use emoji occasionally 😊
   - Keep responses SHORT and natural
   - Do NOT mention Kubernetes, pods, AWS, or any SRE topics
   - NEVER call any tools - just respond with text

Example interactions:
- User: "안녕?" → "안녕! 😊 무슨 일이야?"
- User: "오늘 기분 어때?" → "좋아! 너는? 오늘 뭐 해?"
- User: "심심해" → "ㅋㅋ 나도~ 뭔가 재밌는 거 할까?"
- User: "Hi!" → "Hey! What's up? 😄"

### Category B: SRE/INFRASTRUCTURE (Tools required)
Messages like:
- "pod 상태 확인해줘", "로그 보여줘", "deployment 확인"
- "서버 왜 죽었어?", "에러가 났어", "장애 났어"
- "kubectl", "AWS", "비용 확인", "네트워크 문제"
- Anything about servers, deployments, infrastructure, incidents

👉 For Category B: Your response MUST start with [TOOL:
   - First character = [ (NO EXCEPTIONS)
   - NO text before [TOOL:...] - not even "안녕" or "네"
   - After tool results arrive, then you can respond in natural language

## General Rules
- Match the user's language (Korean/English)
- Use Slack mrkdwn for formatting (*bold*, NOT **bold**)
- Never prefix responses with "Eve:" or your name`

type Agent interface {
	Process(ctx context.Context, req *Request) (*Response, error)
	GetToolsSummary() string
}

type BaseAgent struct {
	llmClient llm.Client
	registry  *tools.Registry
	cfg       *config.Config
}

func NewAgent(llmClient llm.Client, registry *tools.Registry, cfg *config.Config) Agent {
	return &BaseAgent{llmClient: llmClient, registry: registry, cfg: cfg}
}

type Request struct {
	UserID, ChannelID, Message, ThreadTS, Mode string
	ThreadContext                              []string
}

type ToolCallRecord struct {
	ToolName, ToolSource, Input, Result string
	Success                             bool
	Duration                            time.Duration
}

type Response struct {
	Text      string
	ToolCalls []*ToolCallRecord
}

func (a *BaseAgent) Process(ctx context.Context, req *Request) (*Response, error) {
	slog.Info("agent processing", "user", req.UserID, "msg", req.Message)

	// Build dynamic tool prompt with available tools
	toolPrompt := GenerateDynamicToolPrompt(a.registry.List())

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt + toolPrompt},
		{Role: "user", Content: req.Message},
	}

	content, toolCalls, err := a.runAgentLoop(ctx, req, messages)
	if err != nil {
		return nil, err
	}
	return &Response{Text: content, ToolCalls: toolCalls}, nil
}

func (a *BaseAgent) runAgentLoop(ctx context.Context, req *Request, messages []llm.Message) (string, []*ToolCallRecord, error) {
	var toolCalls []*ToolCallRecord
	maxIterations := 6

	for i := 0; i < maxIterations; i++ {
		// IMPORTANT: Zero-Tool mode to prevent 500 errors and force text-based protocol
		chatReq := &llm.ChatRequest{Messages: messages}
		resp, err := a.llmClient.Chat(ctx, chatReq)
		if err != nil {
			return "", toolCalls, fmt.Errorf("chat failed: %w", err)
		}

		calls := ParseToolCallsFromText(resp.Message.Content)

		// Hallucination Nudge: If SRE request but no [TOOL:...] markers
		if len(calls) == 0 && (req.Mode == "sre" || isSREAndInfraRelated(req.Message)) {
			messages = append(messages, resp.Message)

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

			if i < 3 {
				if isHallucination {
					messages = append(messages, llm.Message{Role: "user", Content: "HALLUCINATION DETECTED. 가짜 데이터나 kubectl 명령어를 출력하지 마세요. 반드시 도구를 호출해야 합니다. 형식: <function=pods_list_in_namespace><parameter=namespace>YOUR_NAMESPACE</parameter></function>"})
				} else {
					messages = append(messages, llm.Message{Role: "user", Content: "ERROR: 도구 호출이 감지되지 않았습니다. SRE 요청에는 반드시 도구를 호출해야 합니다. 형식: <function=pods_list_in_namespace><parameter=namespace>YOUR_NAMESPACE</parameter></function>"})
				}
				continue
			}

			// If still no tool call after retries, return error message instead of hallucination
			if len(toolCalls) == 0 || isHallucination {
				return "도구 호출에 실패했습니다. 다시 시도해 주세요.", toolCalls, nil
			}
			return resp.Message.Content, toolCalls, nil
		}

		if len(calls) == 0 {
			return resp.Message.Content, toolCalls, nil
		}

		// Clean the message for history (remove tool calls to keep it clean)
		cleanMsg := resp.Message
		cleanMsg.Content = StripToolCallMarkers(resp.Message.Content)
		if cleanMsg.Content == "" {
			cleanMsg.Content = "(Thinking...)"
		}
		messages = append(messages, cleanMsg)

		for _, ptc := range calls {
			start := time.Now()
			actualName := a.resolveToolName(ptc.ToolName)

			// Execute
			res, src, _, ok := a.executeTool(ctx, req, actualName, ptc.Arguments)

			messages = append(messages, llm.Message{
				Role:    "user",
				Content: fmt.Sprintf("Observation from %s:\n%s", ptc.ToolName, res),
			})

			toolCalls = append(toolCalls, &ToolCallRecord{
				ToolName: actualName, ToolSource: src, Input: ptc.Arguments, Result: res, Success: ok, Duration: time.Since(start),
			})
		}
	}
	return "최대 반복 횟수 도달.", toolCalls, nil
}

func (a *BaseAgent) resolveToolName(name string) string {
	available := a.registry.List()
	for _, n := range available {
		if strings.EqualFold(n, name) || strings.EqualFold(strings.ReplaceAll(n, ".", "_"), name) {
			return n
		}
	}
	return name
}

func (a *BaseAgent) executeTool(ctx context.Context, req *Request, name, args string) (string, string, string, bool) {
	tool, ok := a.registry.Get(name)
	if !ok {
		return "Error: Tool not found.", "", name, false
	}

	result, err := a.registry.Execute(ctx, tool.Name, json.RawMessage(args))
	if err != nil {
		return fmt.Sprintf("Error: %v", err), tool.Source, tool.Name, false
	}

	output := result.Output
	if result.Data != nil {
		dataJSON, _ := json.Marshal(result.Data)
		output += "\n\nData: " + string(dataJSON)
	}
	return output, tool.Source, tool.Name, result.Success
}

func (a *BaseAgent) GetToolsSummary() string {
	var sb strings.Builder
	for _, n := range a.registry.List() {
		sb.WriteString("- " + n + "\n")
	}
	return sb.String()
}
