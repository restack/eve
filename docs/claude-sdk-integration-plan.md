# Eve: Claude SDK 통합 계획서

## 1. Executive Summary

Eve는 현재 로컬 LLM(qwen3-coder)을 사용하는 Kubernetes 운영 에이전트입니다. 본 문서는 Anthropic Claude SDK를 통합하여 Eve의 성능, 신뢰성, 기능을 대폭 향상시키기 위한 전략적 계획을 제시합니다.

---

## 2. 현재 아키텍처 분석

### 2.1 프로젝트 구조

```
eve/
├── cmd/eve/main.go           # 애플리케이션 진입점
├── internal/
│   ├── agent/agent.go        # ReAct 에이전틱 루프
│   ├── config/config.go      # 환경 설정 관리
│   ├── llm/
│   │   ├── types.go          # LLM 인터페이스 정의
│   │   ├── openai.go         # OpenAI 호환 클라이언트
│   │   └── ollama.go         # Ollama 클라이언트
│   ├── mcp/client.go         # MCP 프로토콜 클라이언트
│   ├── slack/
│   │   ├── handler.go        # Slack Socket Mode 핸들러
│   │   └── alerts.go         # 알림 관리
│   └── tools/
│       ├── types.go          # 도구 타입 정의
│       └── registry.go       # 도구 레지스트리
└── manifests/                # Kubernetes 배포 매니페스트
```

### 2.2 현재 LLM 통합 방식

```go
// internal/llm/types.go
type Client interface {
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}
```

현재 지원 LLM:
- **OpenAI 호환 API**: vLLM, LocalAI, llama.cpp 등
- **Ollama**: 로컬 모델 실행

### 2.3 에이전트 루프 (ReAct 패턴)

```go
// internal/agent/agent.go - 현재 구현
maxIterations := 10
for i := 0; i < maxIterations; i++ {
    resp, err := a.llmClient.Chat(ctx, chatReq)
    if resp.FinishReason != "tool_calls" {
        return &Response{Text: resp.Message.Content}, nil
    }
    // Tool 실행 및 결과 메시지 추가
}
```

---

## 3. Claude SDK 통합 전략

### 3.1 통합 옵션 비교

| 옵션 | 설명 | 장점 | 단점 |
|------|------|------|------|
| **A. Claude API 직접 통합** | Anthropic API 직접 호출 | 단순함, 빠른 구현 | 기본 기능만 사용 |
| **B. Claude Agent SDK 사용** | 공식 Agent SDK 활용 | 고급 에이전트 기능, 컴퓨터 사용 | 아키텍처 변경 필요 |
| **C. 하이브리드 접근** | 로컬 LLM + Claude 폴백 | 비용 최적화, 유연성 | 복잡성 증가 |

**권장: 옵션 B (Claude Agent SDK)** - Eve의 에이전틱 특성과 가장 잘 맞음

### 3.2 Claude SDK 핵심 기능 활용

#### 3.2.1 Enhanced Tool Calling

Claude의 Tool Use는 현재 Eve의 MCP 도구 시스템과 완벽하게 호환됩니다:

```go
// 제안: internal/llm/claude.go

package llm

import (
    "context"
    "github.com/anthropics/anthropic-sdk-go"
)

type ClaudeClient struct {
    client *anthropic.Client
    model  string
}

func NewClaudeClient(apiKey, model string) *ClaudeClient {
    return &ClaudeClient{
        client: anthropic.NewClient(anthropic.WithAPIKey(apiKey)),
        model:  model, // claude-sonnet-4-20250514 권장
    }
}

func (c *ClaudeClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
    // Claude SDK의 Messages API 사용
    // Tool definitions 변환 및 호출
}
```

#### 3.2.2 Extended Thinking (심층 추론)

복잡한 Kubernetes 문제 해결에 Extended Thinking 활용:

```go
// 복잡한 트러블슈팅 시나리오에서 활성화
func (c *ClaudeClient) ChatWithThinking(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
    // Extended Thinking 파라미터 활성화
    // budget_tokens 설정으로 추론 깊이 조절
}
```

**활용 케이스:**
- 복잡한 Pod 장애 근본 원인 분석
- 리소스 병목 현상 진단
- 다단계 복구 계획 수립

#### 3.2.3 Vision 기능

Slack에서 공유된 스크린샷/그래프 분석:

```go
// 이미지가 포함된 메시지 처리
func (a *Agent) ProcessWithImage(ctx context.Context, req *Request, imageData []byte) (*Response, error) {
    // Claude Vision으로 Grafana 대시보드, kubectl output 스크린샷 분석
}
```

**활용 케이스:**
- Grafana 대시보드 스크린샷 분석
- 에러 로그 스크린샷 해석
- 아키텍처 다이어그램 이해

---

## 4. 구현 계획

### Phase 1: Claude API 클라이언트 구현 (1단계)

#### 4.1.1 새 파일 생성: `internal/llm/claude.go`

```go
package llm

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/anthropics/anthropic-sdk-go"
    "github.com/anthropics/anthropic-sdk-go/option"
)

type ClaudeClient struct {
    client *anthropic.Client
    model  string
}

func NewClaudeClient(apiKey, model string) *ClaudeClient {
    client := anthropic.NewClient(option.WithAPIKey(apiKey))

    if model == "" {
        model = "claude-sonnet-4-20250514"
    }

    return &ClaudeClient{
        client: client,
        model:  model,
    }
}

func (c *ClaudeClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
    // 1. Messages 변환
    messages := convertToClaudeMessages(req.Messages)

    // 2. Tools 변환
    tools := convertToClaudeTools(req.Tools)

    // 3. API 호출
    resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
        Model:    anthropic.Model(c.model),
        Messages: messages,
        Tools:    tools,
        MaxTokens: 4096,
    })
    if err != nil {
        return nil, fmt.Errorf("claude api error: %w", err)
    }

    // 4. 응답 변환
    return convertFromClaudeResponse(resp), nil
}

func convertToClaudeMessages(msgs []Message) []anthropic.MessageParam {
    var result []anthropic.MessageParam
    for _, m := range msgs {
        switch m.Role {
        case "user":
            result = append(result, anthropic.UserMessage(m.Content))
        case "assistant":
            result = append(result, anthropic.AssistantMessage(m.Content))
        case "tool":
            result = append(result, anthropic.ToolResultMessage(m.ToolCallID, m.Content))
        }
    }
    return result
}

func convertToClaudeTools(tools []ToolDefinition) []anthropic.ToolUnionParam {
    var result []anthropic.ToolUnionParam
    for _, t := range tools {
        result = append(result, anthropic.ToolParam{
            Name:        t.Function.Name,
            Description: anthropic.String(t.Function.Description),
            InputSchema: t.Function.Parameters,
        })
    }
    return result
}
```

#### 4.1.2 Config 업데이트: `internal/config/config.go`

```go
type Config struct {
    // 기존 필드들...

    // Claude 설정 추가
    ClaudeAPIKey     string
    ClaudeModel      string // claude-sonnet-4-20250514, claude-opus-4-20250514
    ClaudeMaxTokens  int
    EnableThinking   bool   // Extended Thinking 활성화
    ThinkingBudget   int    // Thinking token budget
}

func Load() (*Config, error) {
    cfg := &Config{
        // 기존 설정...

        // Claude 설정
        ClaudeAPIKey:    os.Getenv("CLAUDE_API_KEY"),
        ClaudeModel:     getEnvOrDefault("CLAUDE_MODEL", "claude-sonnet-4-20250514"),
        ClaudeMaxTokens: getEnvIntOrDefault("CLAUDE_MAX_TOKENS", 4096),
        EnableThinking:  os.Getenv("CLAUDE_ENABLE_THINKING") == "true",
        ThinkingBudget:  getEnvIntOrDefault("CLAUDE_THINKING_BUDGET", 10000),
    }
    // ...
}
```

#### 4.1.3 Main 업데이트: `cmd/eve/main.go`

```go
// LLM 클라이언트 생성 부분 수정
var llmClient llm.Client
switch cfg.LLMProvider {
case "ollama":
    llmClient = llm.NewOllamaClient(cfg.LLMBaseURL, cfg.LLMModel)
case "openai":
    llmClient = llm.NewOpenAICompatibleClient(cfg.LLMBaseURL, cfg.LLMModel, cfg.LLMAPIKey)
case "claude":
    llmClient = llm.NewClaudeClient(cfg.ClaudeAPIKey, cfg.ClaudeModel)
default:
    slog.Error("unknown LLM provider", "provider", cfg.LLMProvider)
    os.Exit(1)
}
```

### Phase 2: Extended Thinking 통합 (2단계)

#### 4.2.1 고급 추론이 필요한 케이스 감지

```go
// internal/agent/agent.go

func (a *Agent) requiresDeepReasoning(message string) bool {
    complexPatterns := []string{
        "왜", "why", "root cause", "근본 원인",
        "분석", "analyze", "diagnose", "진단",
        "계획", "plan", "strategy", "전략",
        "비교", "compare", "트레이드오프", "trade-off",
    }

    msg := strings.ToLower(message)
    for _, pattern := range complexPatterns {
        if strings.Contains(msg, pattern) {
            return true
        }
    }
    return false
}

func (a *Agent) Process(ctx context.Context, req *Request) (*Response, error) {
    if a.cfg.EnableThinking && a.requiresDeepReasoning(req.Message) {
        return a.ProcessWithThinking(ctx, req)
    }
    return a.processNormal(ctx, req)
}
```

### Phase 3: Vision 기능 통합 (3단계)

#### 4.3.1 이미지 처리 지원

```go
// internal/slack/handler.go

func (h *Handler) handleMentionWithFiles(ctx context.Context, event *slackevents.AppMentionEvent) {
    // Slack 파일 다운로드
    for _, file := range event.Files {
        if isImageFile(file.Mimetype) {
            imageData, err := h.downloadFile(file.URLPrivate)
            if err != nil {
                continue
            }

            // Vision 분석 요청
            req := &agent.Request{
                UserID:    event.User,
                ChannelID: event.Channel,
                Message:   extractTextFromMention(event.Text),
                Image:     imageData,
                ImageType: file.Mimetype,
            }

            resp, err := h.agent.ProcessWithVision(ctx, req)
            // ...
        }
    }
}
```

### Phase 4: Claude Agent SDK 전환 (4단계)

#### 4.4.1 고급 에이전트 아키텍처

```go
// internal/agent/claude_agent.go

package agent

import (
    "context"
    "github.com/anthropics/claude-agent-sdk-go"
)

type ClaudeAgent struct {
    agent    *claude.Agent
    registry *tools.Registry
    cfg      *config.Config
}

func NewClaudeAgent(cfg *config.Config, registry *tools.Registry) (*ClaudeAgent, error) {
    // MCP 도구들을 Claude Agent SDK 도구로 변환
    agentTools := convertRegistryToAgentTools(registry)

    agent := claude.NewAgent(claude.AgentConfig{
        Model:      cfg.ClaudeModel,
        Tools:      agentTools,
        MaxTurns:   10,
        SystemPrompt: systemPrompt,
    })

    return &ClaudeAgent{
        agent:    agent,
        registry: registry,
        cfg:      cfg,
    }, nil
}

func (a *ClaudeAgent) Process(ctx context.Context, req *Request) (*Response, error) {
    result, err := a.agent.Run(ctx, req.Message)
    if err != nil {
        return nil, err
    }

    return &Response{
        Text: result.FinalOutput,
    }, nil
}
```

---

## 5. 환경 설정

### 5.1 새로운 환경 변수

```bash
# .env.example 추가 내용

# --- Claude Configuration ---
LLM_PROVIDER=claude              # 'claude', 'openai', 'ollama' 중 선택
CLAUDE_API_KEY=sk-ant-...        # Anthropic API 키
CLAUDE_MODEL=claude-sonnet-4-20250514    # 모델 선택
CLAUDE_MAX_TOKENS=4096           # 최대 토큰 수

# --- Advanced Features ---
CLAUDE_ENABLE_THINKING=true      # Extended Thinking 활성화
CLAUDE_THINKING_BUDGET=10000     # Thinking 토큰 예산

# --- Hybrid Mode (Optional) ---
FALLBACK_TO_LOCAL=true           # Claude 실패 시 로컬 LLM 폴백
LOCAL_LLM_FOR_SIMPLE=true        # 단순 쿼리는 로컬 LLM 사용
```

### 5.2 Kubernetes Secret 업데이트

```yaml
# manifests/base/secrets.yaml
apiVersion: v1
kind: Secret
metadata:
  name: eve-secrets
  namespace: eve-system
type: Opaque
stringData:
  SLACK_APP_TOKEN: "xapp-..."
  SLACK_BOT_TOKEN: "xoxb-..."
  CLAUDE_API_KEY: "sk-ant-..."  # 추가
```

---

## 6. 성능 및 비용 최적화

### 6.1 하이브리드 라우팅 전략

```go
// internal/agent/router.go

type LLMRouter struct {
    claudeClient *llm.ClaudeClient
    localClient  llm.Client
    cfg          *config.Config
}

func (r *LLMRouter) Route(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
    // 1. 복잡도 평가
    complexity := r.assessComplexity(req.Messages[len(req.Messages)-1].Content)

    // 2. 라우팅 결정
    switch {
    case complexity == "high":
        // Extended Thinking이 필요한 복잡한 분석
        return r.claudeClient.ChatWithThinking(ctx, req)

    case complexity == "medium":
        // 일반 Claude 사용
        return r.claudeClient.Chat(ctx, req)

    case r.cfg.LocalLLMForSimple:
        // 단순 쿼리는 로컬 LLM
        return r.localClient.Chat(ctx, req)

    default:
        return r.claudeClient.Chat(ctx, req)
    }
}

func (r *LLMRouter) assessComplexity(message string) string {
    // 토큰 수, 키워드, 도구 요구사항 등으로 복잡도 평가
    wordCount := len(strings.Fields(message))

    if r.containsAnalysisKeywords(message) {
        return "high"
    }
    if wordCount > 50 || r.containsMultipleQuestions(message) {
        return "medium"
    }
    return "low"
}
```

### 6.2 캐싱 전략

```go
// internal/cache/response_cache.go

type ResponseCache struct {
    cache map[string]*CachedResponse
    mu    sync.RWMutex
    ttl   time.Duration
}

type CachedResponse struct {
    Response  *llm.ChatResponse
    CreatedAt time.Time
    HitCount  int
}

// 정적 정보 쿼리에 대한 캐싱
// 예: "네임스페이스 목록", "노드 상태" 등
```

### 6.3 비용 모니터링

```go
// internal/metrics/usage.go

type UsageTracker struct {
    inputTokens  int64
    outputTokens int64
    thinkingTokens int64
    requests     int64
}

func (u *UsageTracker) RecordUsage(resp *anthropic.Message) {
    atomic.AddInt64(&u.inputTokens, int64(resp.Usage.InputTokens))
    atomic.AddInt64(&u.outputTokens, int64(resp.Usage.OutputTokens))
    // ... 메트릭 수집 및 Prometheus 노출
}
```

---

## 7. 테스트 전략

### 7.1 단위 테스트

```go
// internal/llm/claude_test.go

func TestClaudeClient_Chat(t *testing.T) {
    // Mock Anthropic API 사용
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Mock 응답 반환
    }))
    defer server.Close()

    client := NewClaudeClientWithEndpoint(server.URL, "test-key", "claude-sonnet-4-20250514")

    resp, err := client.Chat(context.Background(), &ChatRequest{
        Messages: []Message{{Role: "user", Content: "Hello"}},
    })

    assert.NoError(t, err)
    assert.NotEmpty(t, resp.Message.Content)
}
```

### 7.2 통합 테스트

```go
// test/integration/claude_integration_test.go

func TestClaudeToolCalling(t *testing.T) {
    if os.Getenv("CLAUDE_API_KEY") == "" {
        t.Skip("CLAUDE_API_KEY not set")
    }

    // 실제 Claude API와 MCP 도구 호출 테스트
    registry := tools.NewRegistry()
    // ... 테스트 도구 등록

    agent := agent.NewClaudeAgent(cfg, registry)
    resp, err := agent.Process(ctx, &agent.Request{
        Message: "List all pods in the default namespace",
    })

    assert.NoError(t, err)
    assert.Contains(t, resp.Text, "pod")
}
```

---

## 8. 마이그레이션 계획

### 8.1 단계별 롤아웃

```
Phase 1 (Week 1-2): Claude 클라이언트 구현 및 기본 통합
    └── internal/llm/claude.go 구현
    └── Config 업데이트
    └── 단위 테스트 작성

Phase 2 (Week 3): 하이브리드 모드 구현
    └── LLM Router 구현
    └── 로컬 LLM 폴백 로직
    └── 복잡도 기반 라우팅

Phase 3 (Week 4): Extended Thinking 통합
    └── 심층 분석 케이스 감지
    └── Thinking 토큰 예산 관리
    └── 성능 테스트

Phase 4 (Week 5-6): Vision 기능 및 고급 기능
    └── 이미지 분석 통합
    └── Slack 파일 처리
    └── 메트릭 및 모니터링

Phase 5 (Week 7+): Claude Agent SDK 전면 전환 (선택적)
    └── 아키텍처 리팩토링
    └── 고급 에이전트 기능 활용
```

### 8.2 롤백 계획

```go
// 환경 변수로 빠른 롤백 가능
LLM_PROVIDER=ollama  # 즉시 로컬 LLM으로 전환
```

---

## 9. 예상 효과

### 9.1 성능 향상

| 메트릭 | 현재 (Qwen3-Coder) | Claude 통합 후 |
|--------|-------------------|----------------|
| Tool Calling 정확도 | ~85% | ~98% |
| 복잡 쿼리 처리 | 제한적 | Extended Thinking으로 대폭 향상 |
| 멀티스텝 추론 | 기본 | 고급 에이전틱 루프 |
| 이미지 분석 | 미지원 | Vision 지원 |

### 9.2 새로운 기능

1. **심층 장애 분석**: Extended Thinking으로 복잡한 근본 원인 분석
2. **대시보드 분석**: Grafana 스크린샷을 업로드하면 자동 해석
3. **계획 수립**: 복잡한 마이그레이션/업그레이드 계획 자동 생성
4. **문서 생성**: 인시던트 보고서 자동 작성

---

## 10. 리스크 및 완화

| 리스크 | 영향 | 완화 방안 |
|--------|------|----------|
| API 비용 증가 | 중 | 하이브리드 라우팅, 캐싱, 토큰 예산 |
| API 지연시간 | 중 | 타임아웃 설정, 비동기 처리 |
| API 가용성 | 고 | 로컬 LLM 폴백 메커니즘 |
| 보안 (API 키 노출) | 고 | K8s Secrets, 환경 변수 암호화 |

---

## 11. 결론

Claude SDK 통합은 Eve의 기능을 획기적으로 향상시킬 수 있는 전략적 투자입니다. 특히:

1. **Tool Calling 정확도 향상**: Claude의 우수한 도구 호출 능력
2. **Extended Thinking**: 복잡한 SRE 문제에 대한 심층 분석
3. **Vision**: 대시보드/로그 스크린샷 분석
4. **하이브리드 접근**: 비용 최적화와 성능의 균형

단계적 마이그레이션을 통해 리스크를 최소화하면서 Eve를 차세대 지능형 SRE 에이전트로 발전시킬 수 있습니다.

---

## 부록

### A. Claude SDK Go 설치

```bash
go get github.com/anthropics/anthropic-sdk-go
```

### B. 참고 문서

- [Anthropic Claude API Documentation](https://docs.anthropic.com)
- [Claude Tool Use Guide](https://docs.anthropic.com/claude/docs/tool-use)
- [Extended Thinking Guide](https://docs.anthropic.com/claude/docs/extended-thinking)
- [Claude Agent SDK](https://github.com/anthropics/claude-agent-sdk)

### C. 관련 파일 목록

구현 시 수정이 필요한 파일:
- `internal/llm/claude.go` (신규)
- `internal/llm/types.go` (수정)
- `internal/config/config.go` (수정)
- `internal/agent/agent.go` (수정)
- `internal/agent/router.go` (신규)
- `cmd/eve/main.go` (수정)
- `go.mod` (의존성 추가)
