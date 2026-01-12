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

## 11. Claude-Mem 영구 메모리 연동

### 11.1 Claude-Mem 개요

[Claude-Mem](https://github.com/thedotmack/claude-mem)은 Claude 세션 간 컨텍스트를 영구적으로 보존하는 메모리 시스템입니다. Eve와 통합하면 다음과 같은 SRE 워크플로우 개선이 가능합니다.

**핵심 기능:**
- **영구 메모리**: 세션 종료 후에도 대화 컨텍스트 유지
- **계층적 검색**: 토큰 효율적인 3단계 검색 워크플로우
- **의미론적 검색**: Chroma 벡터 DB 기반 하이브리드 검색
- **개인정보 제어**: `<private>` 태그로 민감 정보 제외

**아키텍처:**
```
┌─────────────────────────────────────────────────────────────┐
│                      Claude-Mem Stack                        │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │  SQLite DB  │  │  Chroma DB  │  │  Worker Service     │  │
│  │  (Sessions, │  │  (Vector    │  │  (HTTP API on       │  │
│  │   Summaries)│  │   Search)   │  │   port 37777)       │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 11.2 Eve + Claude-Mem 연동 시나리오

#### 시나리오 1: 인시던트 히스토리 검색

```
사용자: "지난번에 OOMKilled 문제 어떻게 해결했지?"
Eve: [claude-mem 검색] → 과거 해결 방법 컨텍스트 로드 → 답변 생성
```

#### 시나리오 2: 채널별 컨텍스트 유지

```
#sre-alerts 채널의 과거 인시던트 패턴 → 자동 연관 분석
```

#### 시나리오 3: 사용자별 선호도 기억

```
사용자 A: "항상 YAML 형식으로 보여줘" → 영구 저장 → 다음 세션에서 자동 적용
```

### 11.3 MCP 검색 도구 통합

Claude-Mem은 3단계 검색 워크플로우로 토큰 사용량을 최적화합니다:

| 도구 | 목적 | 토큰 비용 |
|------|------|----------|
| `mem.search` | 메모리 인덱스 검색 | ~50-100 토큰 |
| `mem.timeline` | 시간적 컨텍스트 조회 | ~200-300 토큰 |
| `mem.get_observations` | 상세 정보 조회 | ~500-1,000 토큰 |

```go
// internal/mcp/memory_client.go

package mcp

import (
    "context"
    "encoding/json"
)

// MemoryClient는 Claude-Mem MCP 서버와 통신합니다
type MemoryClient struct {
    *Client
    baseURL string
}

func NewMemoryClient(baseURL string) *MemoryClient {
    return &MemoryClient{
        Client:  NewClient(baseURL),
        baseURL: baseURL,
    }
}

// Search는 메모리에서 관련 컨텍스트를 검색합니다
func (m *MemoryClient) Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error) {
    resp, err := m.callRPC(ctx, "mem.search", map[string]interface{}{
        "query": query,
        "limit": limit,
    })
    if err != nil {
        return nil, err
    }

    var entries []MemoryEntry
    json.Unmarshal(resp, &entries)
    return entries, nil
}

// GetObservations는 특정 ID의 상세 관찰 데이터를 조회합니다
func (m *MemoryClient) GetObservations(ctx context.Context, ids []string) ([]Observation, error) {
    resp, err := m.callRPC(ctx, "mem.get_observations", map[string]interface{}{
        "ids": ids,
    })
    if err != nil {
        return nil, err
    }

    var observations []Observation
    json.Unmarshal(resp, &observations)
    return observations, nil
}

// MemoryEntry는 검색 결과 항목입니다
type MemoryEntry struct {
    ID          string   `json:"id"`
    Summary     string   `json:"summary"`
    Timestamp   string   `json:"timestamp"`
    SessionID   string   `json:"session_id"`
    Technologies []string `json:"technologies"`
    Score       float64  `json:"score"`
}

// Observation은 상세 관찰 데이터입니다
type Observation struct {
    ID        string                 `json:"id"`
    Type      string                 `json:"type"`
    Content   string                 `json:"content"`
    Metadata  map[string]interface{} `json:"metadata"`
    CreatedAt string                 `json:"created_at"`
}
```

### 11.4 Eve 에이전트에 메모리 통합

```go
// internal/agent/memory_agent.go

package agent

import (
    "context"
    "fmt"
    "log/slog"
    "strings"

    "github.com/restack/eve/internal/config"
    "github.com/restack/eve/internal/llm"
    "github.com/restack/eve/internal/mcp"
    "github.com/restack/eve/internal/tools"
)

// MemoryAwareAgent는 영구 메모리를 활용하는 에이전트입니다
type MemoryAwareAgent struct {
    *Agent
    memClient *mcp.MemoryClient
}

func NewMemoryAwareAgent(
    llmClient llm.Client,
    registry *tools.Registry,
    cfg *config.Config,
    memClient *mcp.MemoryClient,
) *MemoryAwareAgent {
    return &MemoryAwareAgent{
        Agent:     NewAgent(llmClient, registry, cfg),
        memClient: memClient,
    }
}

// Process는 메모리 컨텍스트를 포함하여 요청을 처리합니다
func (a *MemoryAwareAgent) Process(ctx context.Context, req *Request) (*Response, error) {
    // 1. 관련 메모리 검색
    memories, err := a.searchRelevantMemory(ctx, req)
    if err != nil {
        slog.Warn("memory search failed", "error", err)
        // 메모리 실패해도 계속 진행
    }

    // 2. 시스템 프롬프트에 메모리 컨텍스트 추가
    enhancedReq := a.enrichWithMemory(req, memories)

    // 3. 기본 에이전트 로직 실행
    return a.Agent.Process(ctx, enhancedReq)
}

func (a *MemoryAwareAgent) searchRelevantMemory(ctx context.Context, req *Request) ([]mcp.MemoryEntry, error) {
    if a.memClient == nil {
        return nil, nil
    }

    // 사용자 메시지 + 채널 컨텍스트로 검색
    query := fmt.Sprintf("channel:%s %s", req.ChannelID, req.Message)

    entries, err := a.memClient.Search(ctx, query, 5)
    if err != nil {
        return nil, err
    }

    // 관련성 높은 항목만 필터링
    var relevant []mcp.MemoryEntry
    for _, entry := range entries {
        if entry.Score > 0.7 {
            relevant = append(relevant, entry)
        }
    }

    return relevant, nil
}

func (a *MemoryAwareAgent) enrichWithMemory(req *Request, memories []mcp.MemoryEntry) *Request {
    if len(memories) == 0 {
        return req
    }

    // 메모리 컨텍스트 문자열 생성
    var memContext strings.Builder
    memContext.WriteString("\n\n---\n[Relevant Past Context]\n")
    for _, m := range memories {
        memContext.WriteString(fmt.Sprintf("- [%s] %s\n", m.Timestamp, m.Summary))
    }
    memContext.WriteString("---\n")

    // 메시지에 컨텍스트 추가
    enrichedReq := *req
    enrichedReq.Message = req.Message + memContext.String()

    return &enrichedReq
}
```

### 11.5 세션 관찰 저장

Eve의 도구 실행 결과를 Claude-Mem에 자동 저장합니다:

```go
// internal/mcp/memory_writer.go

package mcp

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "time"

    "github.com/restack/eve/internal/tools"
)

// MemoryWriter는 관찰 데이터를 메모리에 저장합니다
type MemoryWriter struct {
    endpoint   string
    httpClient *http.Client
    sessionID  string
}

func NewMemoryWriter(endpoint, sessionID string) *MemoryWriter {
    return &MemoryWriter{
        endpoint: endpoint,
        httpClient: &http.Client{
            Timeout: 10 * time.Second,
        },
        sessionID: sessionID,
    }
}

// RecordToolExecution은 도구 실행 결과를 기록합니다
func (w *MemoryWriter) RecordToolExecution(ctx context.Context, toolName string, input json.RawMessage, result *tools.Result) error {
    observation := map[string]interface{}{
        "type":       "tool_execution",
        "session_id": w.sessionID,
        "tool_name":  toolName,
        "input":      string(input),
        "output":     result.Output,
        "success":    result.Success,
        "timestamp":  time.Now().UTC().Format(time.RFC3339),
        "metadata": map[string]interface{}{
            "technologies": extractTechnologies(toolName, result.Output),
        },
    }

    body, _ := json.Marshal(observation)
    req, err := http.NewRequestWithContext(ctx, "POST", w.endpoint+"/api/observations", strings.NewReader(string(body)))
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/json")

    resp, err := w.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
        return fmt.Errorf("failed to record observation: %d", resp.StatusCode)
    }

    return nil
}

// RecordIncident는 인시던트 정보를 기록합니다
func (w *MemoryWriter) RecordIncident(ctx context.Context, incident *Incident) error {
    observation := map[string]interface{}{
        "type":        "incident",
        "session_id":  w.sessionID,
        "title":       incident.Title,
        "severity":    incident.Severity,
        "namespace":   incident.Namespace,
        "resource":    incident.Resource,
        "description": incident.Description,
        "resolution":  incident.Resolution,
        "timestamp":   time.Now().UTC().Format(time.RFC3339),
        "metadata": map[string]interface{}{
            "technologies": incident.Technologies,
            "duration_min": incident.DurationMinutes,
        },
    }

    body, _ := json.Marshal(observation)
    req, err := http.NewRequestWithContext(ctx, "POST", w.endpoint+"/api/observations", strings.NewReader(string(body)))
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/json")

    resp, err := w.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    return nil
}

// Incident는 인시던트 정보를 나타냅니다
type Incident struct {
    Title           string
    Severity        string
    Namespace       string
    Resource        string
    Description     string
    Resolution      string
    Technologies    []string
    DurationMinutes int
}

// extractTechnologies는 도구 이름과 출력에서 기술 태그를 추출합니다
func extractTechnologies(toolName, output string) []string {
    techs := []string{}

    // 도구 이름에서 기술 추출
    if strings.HasPrefix(toolName, "kubernetes.") {
        techs = append(techs, "kubernetes")
    }
    if strings.HasPrefix(toolName, "github.") {
        techs = append(techs, "github")
    }
    if strings.HasPrefix(toolName, "argo.") {
        techs = append(techs, "argo-workflows")
    }

    // 출력에서 추가 기술 감지
    techKeywords := map[string]string{
        "deployment":  "kubernetes-deployment",
        "pod":         "kubernetes-pod",
        "service":     "kubernetes-service",
        "ingress":     "kubernetes-ingress",
        "configmap":   "kubernetes-configmap",
        "secret":      "kubernetes-secret",
        "oomkilled":   "memory-issue",
        "crashloop":   "crashloop",
        "prometheus":  "prometheus",
        "grafana":     "grafana",
    }

    lowerOutput := strings.ToLower(output)
    for keyword, tech := range techKeywords {
        if strings.Contains(lowerOutput, keyword) {
            techs = append(techs, tech)
        }
    }

    return techs
}
```

### 11.6 Kubernetes 배포 구성

Claude-Mem을 Eve와 함께 사이드카로 배포합니다:

```yaml
# manifests/base/deployment.yaml (업데이트)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: eve
  namespace: eve-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: eve
  template:
    metadata:
      labels:
        app: eve
    spec:
      serviceAccountName: eve
      containers:
        # --- Eve: The Slack Agent & MCP Proxy ---
        - name: eve
          image: harbor.home.lab/restack/eve:latest
          envFrom:
            - secretRef:
                name: eve-secrets
          env:
            - name: MCP_SERVERS
              value: "http://localhost:8080,http://localhost:37777"
            - name: MEMORY_ENDPOINT
              value: "http://localhost:37777"
          resources:
            limits:
              cpu: 200m
              memory: 128Mi

        # --- Kubernetes MCP Server (Sidecar) ---
        - name: mcp-kubernetes
          image: quay.io/podman/kubernetes-mcp-server:latest
          ports:
            - containerPort: 8080
          args:
            - --port=8080
          resources:
            limits:
              cpu: 100m
              memory: 64Mi

        # --- Claude-Mem Worker (Sidecar) ---
        - name: claude-mem
          image: ghcr.io/thedotmack/claude-mem:latest
          ports:
            - containerPort: 37777
          env:
            - name: DATABASE_PATH
              value: /data/memory.db
            - name: CHROMA_PERSIST_DIR
              value: /data/chroma
          volumeMounts:
            - name: memory-data
              mountPath: /data
          resources:
            limits:
              cpu: 200m
              memory: 512Mi

      volumes:
        - name: memory-data
          persistentVolumeClaim:
            claimName: eve-memory-pvc
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: eve-memory-pvc
  namespace: eve-system
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

### 11.7 환경 변수 추가

```bash
# .env.example 추가 내용

# --- Claude-Mem Configuration ---
MEMORY_ENABLED=true
MEMORY_ENDPOINT=http://localhost:37777
MEMORY_SEARCH_LIMIT=5
MEMORY_MIN_RELEVANCE=0.7

# --- Memory Recording ---
RECORD_TOOL_EXECUTIONS=true
RECORD_INCIDENTS=true
```

### 11.8 Config 업데이트

```go
// internal/config/config.go 추가

type Config struct {
    // 기존 필드들...

    // Claude-Mem 설정
    MemoryEnabled      bool
    MemoryEndpoint     string
    MemorySearchLimit  int
    MemoryMinRelevance float64
    RecordToolExecs    bool
    RecordIncidents    bool
}

func Load() (*Config, error) {
    cfg := &Config{
        // 기존 설정...

        // Memory 설정
        MemoryEnabled:      os.Getenv("MEMORY_ENABLED") == "true",
        MemoryEndpoint:     getEnvOrDefault("MEMORY_ENDPOINT", "http://localhost:37777"),
        MemorySearchLimit:  getEnvIntOrDefault("MEMORY_SEARCH_LIMIT", 5),
        MemoryMinRelevance: getEnvFloatOrDefault("MEMORY_MIN_RELEVANCE", 0.7),
        RecordToolExecs:    os.Getenv("RECORD_TOOL_EXECUTIONS") == "true",
        RecordIncidents:    os.Getenv("RECORD_INCIDENTS") == "true",
    }
    // ...
}
```

### 11.9 활용 시나리오

#### 시나리오 A: 반복 장애 패턴 감지

```
1. 사용자: "api-gateway가 또 죽었어"
2. Eve: [메모리 검색] "api-gateway" 관련 과거 인시던트 조회
3. 메모리: "지난 3번의 api-gateway 장애는 모두 메모리 부족으로 발생"
4. Eve: "과거 기록을 보면 api-gateway는 메모리 부족으로 자주 죽습니다.
        현재 메모리 사용량을 확인해보겠습니다..."
```

#### 시나리오 B: 해결책 재사용

```
1. 사용자: "prometheus가 느려"
2. Eve: [메모리 검색] prometheus 성능 관련 과거 해결책
3. 메모리: "2024-12-15: retention 기간을 30d→15d로 줄여 해결"
4. Eve: "과거에 비슷한 문제가 있었습니다. retention 기간 조정으로
        해결했던 기록이 있네요. 현재 설정을 확인해볼까요?"
```

#### 시나리오 C: 팀 지식 축적

```
시간이 지남에 따라 축적되는 지식:
- 인시던트 해결 패턴
- 클러스터별 특성
- 자주 발생하는 문제와 해결책
- 팀원별 선호하는 응답 형식
```

### 11.10 벡터 DB 대안: Qdrant

Claude-Mem은 기본적으로 Chroma DB를 사용하지만, **프로덕션 환경에서는 Qdrant가 더 적합**할 수 있습니다.

#### Chroma vs Qdrant 비교

| 항목 | Chroma | Qdrant |
|------|--------|--------|
| 구현 언어 | Python | Rust |
| 프로덕션 준비도 | 프로토타이핑 적합 | 프로덕션 준비 완료 |
| 확장성 | 제한적 (샤딩 미지원) | 수평 확장 지원 |
| 필터링 | 기본 | 고급 필터링, 멀티테넌시 |
| K8s 배포 | 복잡 | Helm 차트 제공 |
| 메모리 최적화 | 제한적 | 양자화 지원 (8bit, binary) |

#### Qdrant 기반 자체 구현

Claude-Mem의 AGPL 라이선스 제약을 피하고 더 유연한 구현을 위해 Qdrant 기반 자체 메모리 시스템을 구축할 수 있습니다:

```go
// internal/memory/qdrant_client.go

package memory

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    pb "github.com/qdrant/go-client/qdrant"
    "google.golang.org/grpc"
)

type QdrantMemory struct {
    client     pb.PointsClient
    collection string
    embedder   Embedder
}

type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
}

func NewQdrantMemory(addr, collection string, embedder Embedder) (*QdrantMemory, error) {
    conn, err := grpc.Dial(addr, grpc.WithInsecure())
    if err != nil {
        return nil, err
    }

    return &QdrantMemory{
        client:     pb.NewPointsClient(conn),
        collection: collection,
        embedder:   embedder,
    }, nil
}

// Store는 관찰 데이터를 벡터 DB에 저장합니다
func (q *QdrantMemory) Store(ctx context.Context, obs *Observation) error {
    // 텍스트를 벡터로 변환
    vector, err := q.embedder.Embed(ctx, obs.Content)
    if err != nil {
        return err
    }

    // Qdrant에 저장
    _, err = q.client.Upsert(ctx, &pb.UpsertPoints{
        CollectionName: q.collection,
        Points: []*pb.PointStruct{{
            Id: &pb.PointId{PointIdOptions: &pb.PointId_Uuid{Uuid: obs.ID}},
            Vectors: &pb.Vectors{
                VectorsOptions: &pb.Vectors_Vector{
                    Vector: &pb.Vector{Data: vector},
                },
            },
            Payload: map[string]*pb.Value{
                "type":       {Kind: &pb.Value_StringValue{StringValue: obs.Type}},
                "content":    {Kind: &pb.Value_StringValue{StringValue: obs.Content}},
                "session_id": {Kind: &pb.Value_StringValue{StringValue: obs.SessionID}},
                "channel_id": {Kind: &pb.Value_StringValue{StringValue: obs.ChannelID}},
                "timestamp":  {Kind: &pb.Value_StringValue{StringValue: obs.Timestamp}},
            },
        }},
    })

    return err
}

// Search는 유사한 관찰 데이터를 검색합니다
func (q *QdrantMemory) Search(ctx context.Context, query string, limit int, filters map[string]string) ([]Observation, error) {
    // 쿼리를 벡터로 변환
    vector, err := q.embedder.Embed(ctx, query)
    if err != nil {
        return nil, err
    }

    // 필터 구성
    var filter *pb.Filter
    if len(filters) > 0 {
        conditions := make([]*pb.Condition, 0, len(filters))
        for key, value := range filters {
            conditions = append(conditions, &pb.Condition{
                ConditionOneOf: &pb.Condition_Field{
                    Field: &pb.FieldCondition{
                        Key: key,
                        Match: &pb.Match{
                            MatchValue: &pb.Match_Keyword{Keyword: value},
                        },
                    },
                },
            })
        }
        filter = &pb.Filter{Must: conditions}
    }

    // 검색 실행
    resp, err := q.client.Search(ctx, &pb.SearchPoints{
        CollectionName: q.collection,
        Vector:         vector,
        Limit:          uint64(limit),
        Filter:         filter,
        WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
    })
    if err != nil {
        return nil, err
    }

    // 결과 변환
    var results []Observation
    for _, point := range resp.Result {
        obs := Observation{
            ID:        point.Id.GetUuid(),
            Score:     float64(point.Score),
            Type:      point.Payload["type"].GetStringValue(),
            Content:   point.Payload["content"].GetStringValue(),
            SessionID: point.Payload["session_id"].GetStringValue(),
            ChannelID: point.Payload["channel_id"].GetStringValue(),
            Timestamp: point.Payload["timestamp"].GetStringValue(),
        }
        results = append(results, obs)
    }

    return results, nil
}

type Observation struct {
    ID        string
    Type      string
    Content   string
    SessionID string
    ChannelID string
    Timestamp string
    Score     float64
}
```

#### Qdrant Kubernetes 배포

```yaml
# manifests/base/qdrant.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: qdrant
  namespace: eve-system
spec:
  serviceName: qdrant
  replicas: 1
  selector:
    matchLabels:
      app: qdrant
  template:
    metadata:
      labels:
        app: qdrant
    spec:
      containers:
        - name: qdrant
          image: qdrant/qdrant:v1.7.4
          ports:
            - containerPort: 6333  # HTTP
            - containerPort: 6334  # gRPC
          volumeMounts:
            - name: qdrant-storage
              mountPath: /qdrant/storage
          resources:
            limits:
              cpu: 500m
              memory: 1Gi
  volumeClaimTemplates:
    - metadata:
        name: qdrant-storage
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 10Gi
---
apiVersion: v1
kind: Service
metadata:
  name: qdrant
  namespace: eve-system
spec:
  selector:
    app: qdrant
  ports:
    - name: http
      port: 6333
    - name: grpc
      port: 6334
```

#### 임베딩 모델 선택

| 모델 | 차원 | 특징 |
|------|------|------|
| `text-embedding-3-small` (OpenAI) | 1536 | 고품질, API 비용 |
| `all-MiniLM-L6-v2` (Sentence Transformers) | 384 | 로컬 실행, 빠름 |
| `nomic-embed-text` (Ollama) | 768 | 로컬 LLM과 통합 |

```go
// internal/memory/embedder.go

// OllamaEmbedder는 로컬 Ollama를 사용한 임베딩 생성
type OllamaEmbedder struct {
    baseURL string
    model   string
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    req := map[string]string{
        "model":  e.model,
        "prompt": text,
    }
    // Ollama /api/embeddings 호출
    // ...
}
```

### 11.11 토큰 최적화 전략

Claude-Mem의 3단계 검색으로 토큰 사용량을 최소화합니다:

```go
// internal/agent/memory_search.go

// OptimizedMemorySearch는 단계적 검색으로 토큰을 최적화합니다
func (a *MemoryAwareAgent) OptimizedMemorySearch(ctx context.Context, query string) ([]mcp.Observation, error) {
    // Step 1: 인덱스 검색 (~50-100 토큰)
    entries, err := a.memClient.Search(ctx, query, 10)
    if err != nil {
        return nil, err
    }

    if len(entries) == 0 {
        return nil, nil
    }

    // Step 2: 상위 결과만 상세 조회 (~500-1000 토큰)
    var ids []string
    for i, entry := range entries {
        if i >= 3 || entry.Score < 0.7 {
            break
        }
        ids = append(ids, entry.ID)
    }

    observations, err := a.memClient.GetObservations(ctx, ids)
    if err != nil {
        return nil, err
    }

    return observations, nil
}
```

**토큰 절약 효과:**
- 기존: 모든 관련 데이터 로드 → ~5,000 토큰
- 최적화: 인덱스 → 필터 → 상세조회 → ~600 토큰
- **약 88% 토큰 절감**

---

## 12. 결론

Claude SDK 통합은 Eve의 기능을 획기적으로 향상시킬 수 있는 전략적 투자입니다. 특히:

1. **Tool Calling 정확도 향상**: Claude의 우수한 도구 호출 능력
2. **Extended Thinking**: 복잡한 SRE 문제에 대한 심층 분석
3. **Vision**: 대시보드/로그 스크린샷 분석
4. **하이브리드 접근**: 비용 최적화와 성능의 균형
5. **영구 메모리 (Claude-Mem)**: 세션 간 컨텍스트 유지 및 팀 지식 축적

Claude-Mem 연동을 통해 Eve는 단순한 질의응답 봇을 넘어, **학습하고 기억하는 SRE 동료**로 진화합니다:
- 과거 인시던트 해결 경험 활용
- 반복 장애 패턴 자동 감지
- 팀 전체의 운영 지식 축적

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
- [Claude-Mem GitHub](https://github.com/thedotmack/claude-mem)
- [Chroma Vector Database](https://www.trychroma.com/)
- [Qdrant Vector Database](https://qdrant.tech/)
- [Qdrant Go Client](https://github.com/qdrant/go-client)
- **[Eve Memory System Spec](./eve-memory-system-spec.md)** - Qdrant 기반 자체 구현 상세 스펙

### C. 관련 파일 목록

구현 시 수정이 필요한 파일:

**Claude SDK 통합:**
- `internal/llm/claude.go` (신규)
- `internal/llm/types.go` (수정)
- `internal/config/config.go` (수정)
- `internal/agent/agent.go` (수정)
- `internal/agent/router.go` (신규)
- `cmd/eve/main.go` (수정)
- `go.mod` (의존성 추가)

**Claude-Mem 연동:**
- `internal/mcp/memory_client.go` (신규)
- `internal/mcp/memory_writer.go` (신규)
- `internal/agent/memory_agent.go` (신규)
- `internal/agent/memory_search.go` (신규)
- `manifests/base/deployment.yaml` (수정 - 사이드카 추가)
- `manifests/base/pvc.yaml` (신규 - 영구 스토리지)

**Qdrant 자체 구현 (대안):**
- `internal/memory/qdrant_client.go` (신규)
- `internal/memory/embedder.go` (신규)
- `internal/memory/types.go` (신규)
- `manifests/base/qdrant.yaml` (신규 - StatefulSet + Service)

### D. Claude-Mem MCP 도구 스키마

```json
{
  "tools": [
    {
      "name": "mem.search",
      "description": "Search memory index for relevant context",
      "inputSchema": {
        "type": "object",
        "properties": {
          "query": { "type": "string", "description": "Natural language search query" },
          "limit": { "type": "integer", "default": 10 }
        },
        "required": ["query"]
      }
    },
    {
      "name": "mem.timeline",
      "description": "Get temporal context around an observation",
      "inputSchema": {
        "type": "object",
        "properties": {
          "observation_id": { "type": "string" },
          "window_minutes": { "type": "integer", "default": 30 }
        },
        "required": ["observation_id"]
      }
    },
    {
      "name": "mem.get_observations",
      "description": "Retrieve full details for specific observation IDs",
      "inputSchema": {
        "type": "object",
        "properties": {
          "ids": { "type": "array", "items": { "type": "string" } }
        },
        "required": ["ids"]
      }
    }
  ]
}
```

### E. 라이선스 고려사항

| 컴포넌트 | 라이선스 | 상업적 사용 |
|----------|----------|------------|
| Eve | MIT | 가능 |
| Claude SDK | Apache 2.0 | 가능 |
| Claude-Mem | AGPL-3.0 | 수정 시 소스 공개 필요 |
| Chroma DB | Apache 2.0 | 가능 |
| **Qdrant** | **Apache 2.0** | **가능 (권장)** |

**주의**: Claude-Mem은 AGPL-3.0 라이선스로, 수정 후 네트워크 서비스로 배포 시 소스 코드 공개 의무가 있습니다. 상업적 사용 시 법적 검토가 필요합니다.
