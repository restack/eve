# Eve Memory System Specification

## 1. Overview

### 1.1 Purpose

Eve Memory System은 SRE 에이전트의 세션 간 컨텍스트를 영구적으로 저장하고 검색하는 시스템입니다. 과거 인시던트 해결 경험, 사용자 선호도, 클러스터 특성 등을 학습하여 더 스마트한 응답을 제공합니다.

### 1.2 Goals

- **영구 메모리**: 세션 종료 후에도 컨텍스트 유지
- **의미론적 검색**: 자연어 쿼리로 관련 정보 검색
- **토큰 효율성**: 계층적 검색으로 LLM 토큰 비용 최소화
- **프로덕션 준비**: Kubernetes 네이티브, 고가용성 지원
- **라이선스 자유**: Apache 2.0 기반으로 상업적 사용 가능

### 1.3 Non-Goals

- 실시간 스트리밍 처리
- 멀티 클러스터 동기화 (v1)
- 자동 데이터 만료/정리 (v1)

---

## 2. Architecture

### 2.1 System Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              Eve Agent                                   │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                    MemoryAwareAgent                              │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │   │
│  │  │ LLM Client   │  │ Tool Registry│  │ Memory Manager       │  │   │
│  │  │ (Claude)     │  │ (MCP Tools)  │  │                      │  │   │
│  │  └──────────────┘  └──────────────┘  │ ┌──────────────────┐ │  │   │
│  │                                       │ │ MemoryReader     │ │  │   │
│  │                                       │ │ MemoryWriter     │ │  │   │
│  │                                       │ │ MemorySearcher   │ │  │   │
│  │                                       │ └──────────────────┘ │  │   │
│  │                                       └──────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ gRPC / HTTP
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                           Memory Backend                                 │
│  ┌─────────────────────┐  ┌─────────────────────┐  ┌─────────────────┐ │
│  │     Qdrant          │  │     Embedder        │  │   SQLite        │ │
│  │  (Vector Search)    │  │  (Text → Vector)    │  │  (Metadata)     │ │
│  │                     │  │                     │  │                 │ │
│  │  - Collections      │  │  - Ollama           │  │  - Sessions     │ │
│  │  - Points           │  │  - OpenAI           │  │  - Summaries    │ │
│  │  - Filters          │  │  - Local Models     │  │  - Statistics   │ │
│  └─────────────────────┘  └─────────────────────┘  └─────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Data Flow

```
[User Message]
      │
      ▼
┌─────────────────┐
│ 1. Query        │ ───────────────────────────────────────┐
│    Analysis     │                                         │
└────────┬────────┘                                         │
         │                                                  │
         ▼                                                  ▼
┌─────────────────┐                              ┌─────────────────┐
│ 2. Memory       │                              │ 3. Generate     │
│    Search       │ ─────────────────────────────│    Response     │
└────────┬────────┘        Context               └────────┬────────┘
         │                                                │
         ▼                                                ▼
┌─────────────────┐                              ┌─────────────────┐
│ 4. Tool         │                              │ 5. Record       │
│    Execution    │ ─────────────────────────────│    Observation  │
└─────────────────┘        Results               └─────────────────┘
```

---

## 3. Data Model

### 3.1 Core Entities

#### 3.1.1 Observation

관찰 데이터의 기본 단위입니다.

```go
// internal/memory/types.go

package memory

import (
    "time"
)

// ObservationType은 관찰 유형을 정의합니다
type ObservationType string

const (
    ObservationTypeToolExecution ObservationType = "tool_execution"
    ObservationTypeIncident      ObservationType = "incident"
    ObservationTypeUserFeedback  ObservationType = "user_feedback"
    ObservationTypeResolution    ObservationType = "resolution"
    ObservationTypeConfig        ObservationType = "config_change"
    ObservationTypeAlert         ObservationType = "alert"
)

// Observation은 저장되는 관찰 데이터입니다
type Observation struct {
    // 기본 식별자
    ID        string    `json:"id"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`

    // 분류
    Type     ObservationType `json:"type"`
    Category string          `json:"category,omitempty"` // kubernetes, github, argo, etc.

    // 컨텍스트
    SessionID string `json:"session_id"`
    ChannelID string `json:"channel_id"`
    UserID    string `json:"user_id"`
    ThreadTS  string `json:"thread_ts,omitempty"`

    // 콘텐츠
    Title   string `json:"title"`
    Content string `json:"content"`
    Summary string `json:"summary,omitempty"`

    // 메타데이터
    Metadata ObservationMetadata `json:"metadata"`

    // 검색용
    Technologies []string `json:"technologies"`
    Keywords     []string `json:"keywords"`

    // 벡터 (저장 시 생성)
    Vector []float32 `json:"-"`
    Score  float64   `json:"score,omitempty"` // 검색 결과에만 포함
}

// ObservationMetadata는 유형별 추가 정보입니다
type ObservationMetadata struct {
    // Tool Execution
    ToolName   string `json:"tool_name,omitempty"`
    ToolInput  string `json:"tool_input,omitempty"`
    ToolOutput string `json:"tool_output,omitempty"`
    Success    bool   `json:"success,omitempty"`
    Duration   int64  `json:"duration_ms,omitempty"`

    // Incident
    Severity    string `json:"severity,omitempty"`    // critical, warning, info
    Namespace   string `json:"namespace,omitempty"`
    Resource    string `json:"resource,omitempty"`
    ResourceKind string `json:"resource_kind,omitempty"`
    Resolution  string `json:"resolution,omitempty"`
    MTTR        int64  `json:"mttr_minutes,omitempty"` // Mean Time To Resolve

    // Alert
    AlertName  string `json:"alert_name,omitempty"`
    AlertState string `json:"alert_state,omitempty"` // firing, resolved

    // Config Change
    ConfigKey   string `json:"config_key,omitempty"`
    OldValue    string `json:"old_value,omitempty"`
    NewValue    string `json:"new_value,omitempty"`
}
```

#### 3.1.2 Session

세션 정보를 저장합니다.

```go
// Session은 대화 세션 정보입니다
type Session struct {
    ID        string    `json:"id"`
    StartedAt time.Time `json:"started_at"`
    EndedAt   time.Time `json:"ended_at,omitempty"`

    // 컨텍스트
    ChannelID   string `json:"channel_id"`
    UserID      string `json:"user_id"`
    ThreadTS    string `json:"thread_ts,omitempty"`

    // 요약
    Summary         string   `json:"summary,omitempty"`
    Topics          []string `json:"topics,omitempty"`
    Technologies    []string `json:"technologies,omitempty"`

    // 통계
    MessageCount    int `json:"message_count"`
    ToolCallCount   int `json:"tool_call_count"`
    ObservationCount int `json:"observation_count"`
}
```

#### 3.1.3 SearchResult

검색 결과를 래핑합니다.

```go
// SearchResult는 검색 결과입니다
type SearchResult struct {
    Observations []Observation `json:"observations"`
    TotalCount   int           `json:"total_count"`
    SearchTime   time.Duration `json:"search_time"`
    Query        string        `json:"query"`
}

// SearchOptions는 검색 옵션입니다
type SearchOptions struct {
    Limit           int               `json:"limit"`
    MinScore        float64           `json:"min_score"`
    Types           []ObservationType `json:"types,omitempty"`
    Categories      []string          `json:"categories,omitempty"`
    ChannelID       string            `json:"channel_id,omitempty"`
    UserID          string            `json:"user_id,omitempty"`
    Technologies    []string          `json:"technologies,omitempty"`
    TimeRangeStart  time.Time         `json:"time_range_start,omitempty"`
    TimeRangeEnd    time.Time         `json:"time_range_end,omitempty"`
    IncludeContent  bool              `json:"include_content"`
}
```

### 3.2 Qdrant Collection Schema

```json
{
  "collection_name": "eve_observations",
  "vectors": {
    "size": 768,
    "distance": "Cosine"
  },
  "payload_schema": {
    "id": "keyword",
    "type": "keyword",
    "category": "keyword",
    "session_id": "keyword",
    "channel_id": "keyword",
    "user_id": "keyword",
    "title": "text",
    "summary": "text",
    "technologies": "keyword[]",
    "keywords": "keyword[]",
    "created_at": "datetime",
    "severity": "keyword",
    "namespace": "keyword",
    "resource_kind": "keyword",
    "success": "bool"
  },
  "optimizers_config": {
    "indexing_threshold": 10000
  },
  "hnsw_config": {
    "m": 16,
    "ef_construct": 100
  }
}
```

### 3.3 SQLite Schema

```sql
-- sessions 테이블
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    started_at DATETIME NOT NULL,
    ended_at DATETIME,
    channel_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    thread_ts TEXT,
    summary TEXT,
    topics TEXT,  -- JSON array
    technologies TEXT,  -- JSON array
    message_count INTEGER DEFAULT 0,
    tool_call_count INTEGER DEFAULT 0,
    observation_count INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_sessions_channel ON sessions(channel_id);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_started ON sessions(started_at);

-- observation_summaries 테이블 (벡터 검색 결과 캐싱)
CREATE TABLE observation_summaries (
    observation_id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    summary TEXT NOT NULL,
    type TEXT NOT NULL,
    category TEXT,
    technologies TEXT,  -- JSON array
    created_at DATETIME NOT NULL,
    FOREIGN KEY (observation_id) REFERENCES observations(id)
);

-- statistics 테이블
CREATE TABLE statistics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    date DATE NOT NULL,
    channel_id TEXT,
    metric_name TEXT NOT NULL,
    metric_value REAL NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(date, channel_id, metric_name)
);

CREATE INDEX idx_statistics_date ON statistics(date);
CREATE INDEX idx_statistics_metric ON statistics(metric_name);
```

---

## 4. API Specification

### 4.1 Memory Interface

```go
// internal/memory/interface.go

package memory

import (
    "context"
)

// MemoryStore는 메모리 시스템의 메인 인터페이스입니다
type MemoryStore interface {
    // 관찰 저장
    Store(ctx context.Context, obs *Observation) error
    StoreBatch(ctx context.Context, observations []*Observation) error

    // 검색
    Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error)
    GetByID(ctx context.Context, id string) (*Observation, error)
    GetByIDs(ctx context.Context, ids []string) ([]*Observation, error)

    // 타임라인
    GetTimeline(ctx context.Context, observationID string, windowMinutes int) ([]*Observation, error)
    GetSessionObservations(ctx context.Context, sessionID string) ([]*Observation, error)

    // 세션 관리
    CreateSession(ctx context.Context, session *Session) error
    UpdateSession(ctx context.Context, session *Session) error
    GetSession(ctx context.Context, id string) (*Session, error)
    EndSession(ctx context.Context, id string, summary string) error

    // 통계
    GetStats(ctx context.Context, channelID string, days int) (*Stats, error)

    // 유지보수
    Close() error
    HealthCheck(ctx context.Context) error
}

// Embedder는 텍스트를 벡터로 변환합니다
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}

// Stats는 통계 정보입니다
type Stats struct {
    TotalObservations  int64            `json:"total_observations"`
    TotalSessions      int64            `json:"total_sessions"`
    ObservationsByType map[string]int64 `json:"observations_by_type"`
    TopTechnologies    []TechCount      `json:"top_technologies"`
    AvgMTTR            float64          `json:"avg_mttr_minutes"`
    RecentIncidents    int              `json:"recent_incidents"`
}

type TechCount struct {
    Technology string `json:"technology"`
    Count      int64  `json:"count"`
}
```

### 4.2 Store Implementation

```go
// internal/memory/store.go

package memory

import (
    "context"
    "database/sql"
    "fmt"
    "sync"
    "time"

    "github.com/google/uuid"
    pb "github.com/qdrant/go-client/qdrant"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
)

// Config는 메모리 스토어 설정입니다
type Config struct {
    // Qdrant
    QdrantAddr       string `json:"qdrant_addr"`
    QdrantCollection string `json:"qdrant_collection"`

    // SQLite
    SQLitePath string `json:"sqlite_path"`

    // Embedder
    EmbedderType    string `json:"embedder_type"` // ollama, openai
    EmbedderModel   string `json:"embedder_model"`
    EmbedderBaseURL string `json:"embedder_base_url,omitempty"`
    EmbedderAPIKey  string `json:"embedder_api_key,omitempty"`

    // Options
    SearchLimit     int     `json:"search_limit"`
    MinScore        float64 `json:"min_score"`
    BatchSize       int     `json:"batch_size"`
    EnableCache     bool    `json:"enable_cache"`
}

func DefaultConfig() *Config {
    return &Config{
        QdrantAddr:       "localhost:6334",
        QdrantCollection: "eve_observations",
        SQLitePath:       "/data/eve_memory.db",
        EmbedderType:     "ollama",
        EmbedderModel:    "nomic-embed-text",
        EmbedderBaseURL:  "http://localhost:11434",
        SearchLimit:      10,
        MinScore:         0.7,
        BatchSize:        100,
        EnableCache:      true,
    }
}

// Store는 MemoryStore의 구현체입니다
type Store struct {
    config   *Config
    qdrant   pb.PointsClient
    qdrantConn *grpc.ClientConn
    sqlite   *sql.DB
    embedder Embedder

    mu sync.RWMutex
}

// NewStore는 새 Store를 생성합니다
func NewStore(cfg *Config) (*Store, error) {
    // Qdrant 연결
    conn, err := grpc.Dial(cfg.QdrantAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        return nil, fmt.Errorf("failed to connect to qdrant: %w", err)
    }

    // SQLite 연결
    db, err := sql.Open("sqlite3", cfg.SQLitePath)
    if err != nil {
        conn.Close()
        return nil, fmt.Errorf("failed to open sqlite: %w", err)
    }

    // 스키마 초기화
    if err := initSchema(db); err != nil {
        conn.Close()
        db.Close()
        return nil, fmt.Errorf("failed to init schema: %w", err)
    }

    // Embedder 생성
    embedder, err := NewEmbedder(cfg)
    if err != nil {
        conn.Close()
        db.Close()
        return nil, fmt.Errorf("failed to create embedder: %w", err)
    }

    return &Store{
        config:     cfg,
        qdrant:     pb.NewPointsClient(conn),
        qdrantConn: conn,
        sqlite:     db,
        embedder:   embedder,
    }, nil
}

// Store는 관찰 데이터를 저장합니다
func (s *Store) Store(ctx context.Context, obs *Observation) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    // ID 생성
    if obs.ID == "" {
        obs.ID = uuid.New().String()
    }
    if obs.CreatedAt.IsZero() {
        obs.CreatedAt = time.Now().UTC()
    }
    obs.UpdatedAt = time.Now().UTC()

    // 요약 생성 (없는 경우)
    if obs.Summary == "" {
        obs.Summary = generateSummary(obs)
    }

    // 벡터 생성
    textToEmbed := fmt.Sprintf("%s %s %s", obs.Title, obs.Summary, obs.Content)
    vector, err := s.embedder.Embed(ctx, textToEmbed)
    if err != nil {
        return fmt.Errorf("failed to embed: %w", err)
    }
    obs.Vector = vector

    // Qdrant에 저장
    if err := s.storeToQdrant(ctx, obs); err != nil {
        return fmt.Errorf("failed to store to qdrant: %w", err)
    }

    // SQLite에 요약 저장
    if err := s.storeSummaryToSQLite(ctx, obs); err != nil {
        // Qdrant 저장은 성공했으므로 경고만
        // log warning
    }

    return nil
}

// Search는 관련 관찰 데이터를 검색합니다
func (s *Store) Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error) {
    start := time.Now()

    // 쿼리 벡터 생성
    vector, err := s.embedder.Embed(ctx, query)
    if err != nil {
        return nil, fmt.Errorf("failed to embed query: %w", err)
    }

    // 필터 구성
    filter := s.buildFilter(opts)

    // Qdrant 검색
    limit := opts.Limit
    if limit == 0 {
        limit = s.config.SearchLimit
    }

    resp, err := s.qdrant.Search(ctx, &pb.SearchPoints{
        CollectionName: s.config.QdrantCollection,
        Vector:         vector,
        Limit:          uint64(limit),
        Filter:         filter,
        WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: opts.IncludeContent}},
        ScoreThreshold: float32Ptr(float32(opts.MinScore)),
    })
    if err != nil {
        return nil, fmt.Errorf("qdrant search failed: %w", err)
    }

    // 결과 변환
    observations := make([]Observation, 0, len(resp.Result))
    for _, point := range resp.Result {
        obs := s.pointToObservation(point)
        observations = append(observations, obs)
    }

    return &SearchResult{
        Observations: observations,
        TotalCount:   len(observations),
        SearchTime:   time.Since(start),
        Query:        query,
    }, nil
}

// GetTimeline은 특정 관찰 주변의 시간적 컨텍스트를 반환합니다
func (s *Store) GetTimeline(ctx context.Context, observationID string, windowMinutes int) ([]*Observation, error) {
    // 대상 관찰 조회
    obs, err := s.GetByID(ctx, observationID)
    if err != nil {
        return nil, err
    }

    // 시간 범위 계산
    start := obs.CreatedAt.Add(-time.Duration(windowMinutes) * time.Minute)
    end := obs.CreatedAt.Add(time.Duration(windowMinutes) * time.Minute)

    // 같은 세션의 관찰들 조회
    filter := &pb.Filter{
        Must: []*pb.Condition{
            {
                ConditionOneOf: &pb.Condition_Field{
                    Field: &pb.FieldCondition{
                        Key: "session_id",
                        Match: &pb.Match{
                            MatchValue: &pb.Match_Keyword{Keyword: obs.SessionID},
                        },
                    },
                },
            },
        },
    }

    resp, err := s.qdrant.Scroll(ctx, &pb.ScrollPoints{
        CollectionName: s.config.QdrantCollection,
        Filter:         filter,
        WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
        Limit:          uint32Ptr(100),
    })
    if err != nil {
        return nil, err
    }

    // 시간 범위 필터링
    var timeline []*Observation
    for _, point := range resp.Result {
        o := s.pointToObservation(point)
        if o.CreatedAt.After(start) && o.CreatedAt.Before(end) {
            timeline = append(timeline, &o)
        }
    }

    return timeline, nil
}

// Close는 연결을 정리합니다
func (s *Store) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.qdrantConn != nil {
        s.qdrantConn.Close()
    }
    if s.sqlite != nil {
        s.sqlite.Close()
    }
    return nil
}

// HealthCheck는 시스템 상태를 확인합니다
func (s *Store) HealthCheck(ctx context.Context) error {
    // Qdrant 헬스체크
    collectionsClient := pb.NewCollectionsClient(s.qdrantConn)
    _, err := collectionsClient.Get(ctx, &pb.GetCollectionInfoRequest{
        CollectionName: s.config.QdrantCollection,
    })
    if err != nil {
        return fmt.Errorf("qdrant health check failed: %w", err)
    }

    // SQLite 헬스체크
    if err := s.sqlite.PingContext(ctx); err != nil {
        return fmt.Errorf("sqlite health check failed: %w", err)
    }

    return nil
}

// Helper functions

func (s *Store) buildFilter(opts SearchOptions) *pb.Filter {
    var conditions []*pb.Condition

    if len(opts.Types) > 0 {
        values := make([]*pb.Value, len(opts.Types))
        for i, t := range opts.Types {
            values[i] = &pb.Value{Kind: &pb.Value_StringValue{StringValue: string(t)}}
        }
        conditions = append(conditions, &pb.Condition{
            ConditionOneOf: &pb.Condition_Field{
                Field: &pb.FieldCondition{
                    Key: "type",
                    Match: &pb.Match{
                        MatchValue: &pb.Match_Keywords{Keywords: &pb.RepeatedStrings{Strings: toStringSlice(opts.Types)}},
                    },
                },
            },
        })
    }

    if opts.ChannelID != "" {
        conditions = append(conditions, &pb.Condition{
            ConditionOneOf: &pb.Condition_Field{
                Field: &pb.FieldCondition{
                    Key: "channel_id",
                    Match: &pb.Match{
                        MatchValue: &pb.Match_Keyword{Keyword: opts.ChannelID},
                    },
                },
            },
        })
    }

    if len(opts.Technologies) > 0 {
        conditions = append(conditions, &pb.Condition{
            ConditionOneOf: &pb.Condition_Field{
                Field: &pb.FieldCondition{
                    Key: "technologies",
                    Match: &pb.Match{
                        MatchValue: &pb.Match_Keywords{Keywords: &pb.RepeatedStrings{Strings: opts.Technologies}},
                    },
                },
            },
        })
    }

    if len(conditions) == 0 {
        return nil
    }

    return &pb.Filter{Must: conditions}
}

func (s *Store) storeToQdrant(ctx context.Context, obs *Observation) error {
    _, err := s.qdrant.Upsert(ctx, &pb.UpsertPoints{
        CollectionName: s.config.QdrantCollection,
        Points: []*pb.PointStruct{{
            Id: &pb.PointId{PointIdOptions: &pb.PointId_Uuid{Uuid: obs.ID}},
            Vectors: &pb.Vectors{
                VectorsOptions: &pb.Vectors_Vector{
                    Vector: &pb.Vector{Data: obs.Vector},
                },
            },
            Payload: observationToPayload(obs),
        }},
    })
    return err
}

func (s *Store) pointToObservation(point *pb.ScoredPoint) Observation {
    payload := point.Payload
    return Observation{
        ID:           point.Id.GetUuid(),
        Type:         ObservationType(payload["type"].GetStringValue()),
        Category:     payload["category"].GetStringValue(),
        SessionID:    payload["session_id"].GetStringValue(),
        ChannelID:    payload["channel_id"].GetStringValue(),
        UserID:       payload["user_id"].GetStringValue(),
        Title:        payload["title"].GetStringValue(),
        Summary:      payload["summary"].GetStringValue(),
        Technologies: getStringList(payload["technologies"]),
        Score:        float64(point.Score),
        CreatedAt:    parseTime(payload["created_at"].GetStringValue()),
    }
}

func observationToPayload(obs *Observation) map[string]*pb.Value {
    return map[string]*pb.Value{
        "type":         stringValue(string(obs.Type)),
        "category":     stringValue(obs.Category),
        "session_id":   stringValue(obs.SessionID),
        "channel_id":   stringValue(obs.ChannelID),
        "user_id":      stringValue(obs.UserID),
        "title":        stringValue(obs.Title),
        "summary":      stringValue(obs.Summary),
        "content":      stringValue(obs.Content),
        "technologies": stringListValue(obs.Technologies),
        "keywords":     stringListValue(obs.Keywords),
        "created_at":   stringValue(obs.CreatedAt.Format(time.RFC3339)),
        "severity":     stringValue(obs.Metadata.Severity),
        "namespace":    stringValue(obs.Metadata.Namespace),
        "resource_kind": stringValue(obs.Metadata.ResourceKind),
        "success":      boolValue(obs.Metadata.Success),
    }
}

// Utility functions
func stringValue(s string) *pb.Value {
    return &pb.Value{Kind: &pb.Value_StringValue{StringValue: s}}
}

func boolValue(b bool) *pb.Value {
    return &pb.Value{Kind: &pb.Value_BoolValue{BoolValue: b}}
}

func stringListValue(ss []string) *pb.Value {
    values := make([]*pb.Value, len(ss))
    for i, s := range ss {
        values[i] = stringValue(s)
    }
    return &pb.Value{Kind: &pb.Value_ListValue{ListValue: &pb.ListValue{Values: values}}}
}

func float32Ptr(f float32) *float32 { return &f }
func uint32Ptr(u uint32) *uint32   { return &u }

func toStringSlice(types []ObservationType) []string {
    result := make([]string, len(types))
    for i, t := range types {
        result[i] = string(t)
    }
    return result
}

func getStringList(v *pb.Value) []string {
    if v == nil {
        return nil
    }
    list := v.GetListValue()
    if list == nil {
        return nil
    }
    result := make([]string, len(list.Values))
    for i, val := range list.Values {
        result[i] = val.GetStringValue()
    }
    return result
}

func parseTime(s string) time.Time {
    t, _ := time.Parse(time.RFC3339, s)
    return t
}

func generateSummary(obs *Observation) string {
    switch obs.Type {
    case ObservationTypeToolExecution:
        if obs.Metadata.Success {
            return fmt.Sprintf("Executed %s successfully", obs.Metadata.ToolName)
        }
        return fmt.Sprintf("Failed to execute %s", obs.Metadata.ToolName)
    case ObservationTypeIncident:
        return fmt.Sprintf("[%s] %s in %s/%s", obs.Metadata.Severity, obs.Title, obs.Metadata.Namespace, obs.Metadata.Resource)
    default:
        if len(obs.Content) > 100 {
            return obs.Content[:100] + "..."
        }
        return obs.Content
    }
}

func initSchema(db *sql.DB) error {
    schema := `
    CREATE TABLE IF NOT EXISTS sessions (
        id TEXT PRIMARY KEY,
        started_at DATETIME NOT NULL,
        ended_at DATETIME,
        channel_id TEXT NOT NULL,
        user_id TEXT NOT NULL,
        thread_ts TEXT,
        summary TEXT,
        topics TEXT,
        technologies TEXT,
        message_count INTEGER DEFAULT 0,
        tool_call_count INTEGER DEFAULT 0,
        observation_count INTEGER DEFAULT 0,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );

    CREATE INDEX IF NOT EXISTS idx_sessions_channel ON sessions(channel_id);
    CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

    CREATE TABLE IF NOT EXISTS observation_summaries (
        observation_id TEXT PRIMARY KEY,
        title TEXT NOT NULL,
        summary TEXT NOT NULL,
        type TEXT NOT NULL,
        category TEXT,
        technologies TEXT,
        created_at DATETIME NOT NULL
    );

    CREATE TABLE IF NOT EXISTS statistics (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        date DATE NOT NULL,
        channel_id TEXT,
        metric_name TEXT NOT NULL,
        metric_value REAL NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        UNIQUE(date, channel_id, metric_name)
    );
    `
    _, err := db.Exec(schema)
    return err
}

func (s *Store) storeSummaryToSQLite(ctx context.Context, obs *Observation) error {
    _, err := s.sqlite.ExecContext(ctx, `
        INSERT OR REPLACE INTO observation_summaries
        (observation_id, title, summary, type, category, technologies, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, obs.ID, obs.Title, obs.Summary, obs.Type, obs.Category,
       joinStrings(obs.Technologies), obs.CreatedAt)
    return err
}

func joinStrings(ss []string) string {
    if len(ss) == 0 {
        return ""
    }
    result := ss[0]
    for i := 1; i < len(ss); i++ {
        result += "," + ss[i]
    }
    return result
}
```

### 4.3 Embedder Implementation

```go
// internal/memory/embedder.go

package memory

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

// NewEmbedder는 설정에 따라 적절한 Embedder를 생성합니다
func NewEmbedder(cfg *Config) (Embedder, error) {
    switch cfg.EmbedderType {
    case "ollama":
        return NewOllamaEmbedder(cfg.EmbedderBaseURL, cfg.EmbedderModel)
    case "openai":
        return NewOpenAIEmbedder(cfg.EmbedderAPIKey, cfg.EmbedderModel)
    default:
        return nil, fmt.Errorf("unknown embedder type: %s", cfg.EmbedderType)
    }
}

// OllamaEmbedder는 Ollama를 사용한 임베딩 생성기입니다
type OllamaEmbedder struct {
    baseURL    string
    model      string
    httpClient *http.Client
    dimension  int
}

func NewOllamaEmbedder(baseURL, model string) (*OllamaEmbedder, error) {
    e := &OllamaEmbedder{
        baseURL: baseURL,
        model:   model,
        httpClient: &http.Client{
            Timeout: 30 * time.Second,
        },
    }

    // 차원 감지
    dim, err := e.detectDimension()
    if err != nil {
        return nil, err
    }
    e.dimension = dim

    return e, nil
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    req := map[string]interface{}{
        "model":  e.model,
        "prompt": text,
    }

    body, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }

    httpReq, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embeddings", bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := e.httpClient.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        bodyBytes, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("ollama error: %s", string(bodyBytes))
    }

    var result struct {
        Embedding []float32 `json:"embedding"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }

    return result.Embedding, nil
}

func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    results := make([][]float32, len(texts))
    for i, text := range texts {
        embedding, err := e.Embed(ctx, text)
        if err != nil {
            return nil, fmt.Errorf("failed to embed text %d: %w", i, err)
        }
        results[i] = embedding
    }
    return results, nil
}

func (e *OllamaEmbedder) Dimension() int {
    return e.dimension
}

func (e *OllamaEmbedder) detectDimension() (int, error) {
    embedding, err := e.Embed(context.Background(), "test")
    if err != nil {
        return 0, err
    }
    return len(embedding), nil
}

// OpenAIEmbedder는 OpenAI API를 사용한 임베딩 생성기입니다
type OpenAIEmbedder struct {
    apiKey     string
    model      string
    httpClient *http.Client
    dimension  int
}

func NewOpenAIEmbedder(apiKey, model string) (*OpenAIEmbedder, error) {
    if model == "" {
        model = "text-embedding-3-small"
    }

    dim := 1536 // text-embedding-3-small default
    if model == "text-embedding-3-large" {
        dim = 3072
    }

    return &OpenAIEmbedder{
        apiKey: apiKey,
        model:  model,
        httpClient: &http.Client{
            Timeout: 30 * time.Second,
        },
        dimension: dim,
    }, nil
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    req := map[string]interface{}{
        "model": e.model,
        "input": text,
    }

    body, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }

    httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)

    resp, err := e.httpClient.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        bodyBytes, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("openai error: %s", string(bodyBytes))
    }

    var result struct {
        Data []struct {
            Embedding []float32 `json:"embedding"`
        } `json:"data"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }

    if len(result.Data) == 0 {
        return nil, fmt.Errorf("no embedding returned")
    }

    return result.Data[0].Embedding, nil
}

func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    req := map[string]interface{}{
        "model": e.model,
        "input": texts,
    }

    body, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }

    httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)

    resp, err := e.httpClient.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var result struct {
        Data []struct {
            Embedding []float32 `json:"embedding"`
            Index     int       `json:"index"`
        } `json:"data"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }

    // 인덱스 순으로 정렬
    embeddings := make([][]float32, len(texts))
    for _, d := range result.Data {
        embeddings[d.Index] = d.Embedding
    }

    return embeddings, nil
}

func (e *OpenAIEmbedder) Dimension() int {
    return e.dimension
}
```

---

## 5. Integration with Eve Agent

### 5.1 Memory-Aware Agent

```go
// internal/agent/memory_agent.go

package agent

import (
    "context"
    "fmt"
    "log/slog"
    "strings"
    "time"

    "github.com/restack/eve/internal/config"
    "github.com/restack/eve/internal/llm"
    "github.com/restack/eve/internal/memory"
    "github.com/restack/eve/internal/tools"
)

// MemoryAgent는 메모리 기능이 통합된 에이전트입니다
type MemoryAgent struct {
    llmClient llm.Client
    registry  *tools.Registry
    memory    memory.MemoryStore
    cfg       *config.Config

    sessionID string
}

func NewMemoryAgent(
    llmClient llm.Client,
    registry *tools.Registry,
    memStore memory.MemoryStore,
    cfg *config.Config,
) *MemoryAgent {
    return &MemoryAgent{
        llmClient: llmClient,
        registry:  registry,
        memory:    memStore,
        cfg:       cfg,
    }
}

// Process는 메모리를 활용하여 요청을 처리합니다
func (a *MemoryAgent) Process(ctx context.Context, req *Request) (*Response, error) {
    // 세션 시작/복원
    if err := a.ensureSession(ctx, req); err != nil {
        slog.Warn("failed to ensure session", "error", err)
    }

    // 1. 관련 메모리 검색
    memories, err := a.searchMemory(ctx, req.Message, req.ChannelID)
    if err != nil {
        slog.Warn("memory search failed", "error", err)
    }

    // 2. 메시지에 컨텍스트 추가
    enrichedMessages := a.enrichMessages(req, memories)

    // 3. LLM 호출 (ReAct 루프)
    response, toolCalls, err := a.runAgentLoop(ctx, enrichedMessages)
    if err != nil {
        return nil, err
    }

    // 4. 도구 실행 결과 기록
    for _, tc := range toolCalls {
        a.recordToolExecution(ctx, tc, req)
    }

    return &Response{Text: response}, nil
}

func (a *MemoryAgent) searchMemory(ctx context.Context, query, channelID string) (*memory.SearchResult, error) {
    opts := memory.SearchOptions{
        Limit:          5,
        MinScore:       0.7,
        ChannelID:      channelID,
        IncludeContent: false, // 요약만 가져오기
    }

    return a.memory.Search(ctx, query, opts)
}

func (a *MemoryAgent) enrichMessages(req *Request, memories *memory.SearchResult) []llm.Message {
    messages := []llm.Message{
        {Role: "system", Content: a.cfg.SystemPrompt},
    }

    // 메모리 컨텍스트 추가
    if memories != nil && len(memories.Observations) > 0 {
        var memCtx strings.Builder
        memCtx.WriteString("\n\n## Relevant Past Context\n")
        memCtx.WriteString("The following information from past interactions may be relevant:\n\n")

        for _, obs := range memories.Observations {
            memCtx.WriteString(fmt.Sprintf("- **[%s]** %s (relevance: %.0f%%)\n",
                obs.CreatedAt.Format("2006-01-02"),
                obs.Summary,
                obs.Score*100,
            ))
        }

        memCtx.WriteString("\nUse this context if helpful, but prioritize current information.\n")

        messages[0].Content += memCtx.String()
    }

    // 사용자 메시지 추가
    messages = append(messages, llm.Message{
        Role:    "user",
        Content: req.Message,
    })

    return messages
}

func (a *MemoryAgent) recordToolExecution(ctx context.Context, tc *ToolCall, req *Request) {
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
    // 세션 ID 생성 (채널 + 스레드 기반)
    sessionKey := req.ChannelID
    if req.ThreadTS != "" {
        sessionKey += ":" + req.ThreadTS
    }

    a.sessionID = sessionKey

    // 세션 조회 또는 생성
    session, err := a.memory.GetSession(ctx, sessionKey)
    if err != nil || session == nil {
        session = &memory.Session{
            ID:        sessionKey,
            StartedAt: time.Now().UTC(),
            ChannelID: req.ChannelID,
            UserID:    req.UserID,
            ThreadTS:  req.ThreadTS,
        }
        return a.memory.CreateSession(ctx, session)
    }

    // 세션 업데이트
    session.MessageCount++
    return a.memory.UpdateSession(ctx, session)
}

// RecordIncident는 인시던트를 메모리에 기록합니다
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

    // 도구 이름에서 추출
    if strings.HasPrefix(toolName, "kubernetes.") {
        techs["kubernetes"] = true
    }
    if strings.HasPrefix(toolName, "github.") {
        techs["github"] = true
    }
    if strings.HasPrefix(toolName, "argo.") {
        techs["argo"] = true
    }

    // 출력에서 추출
    keywords := map[string]string{
        "deployment":  "kubernetes",
        "pod":         "kubernetes",
        "service":     "kubernetes",
        "configmap":   "kubernetes",
        "secret":      "kubernetes",
        "ingress":     "kubernetes",
        "prometheus":  "prometheus",
        "grafana":     "grafana",
        "oomkilled":   "memory-issue",
        "crashloop":   "crashloop",
        "postgres":    "postgresql",
        "redis":       "redis",
        "kafka":       "kafka",
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

type ToolCall struct {
    ToolName string
    Input    string
    Result   string
    Success  bool
    Duration time.Duration
}

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
```

---

## 6. Deployment

### 6.1 Kubernetes Manifests

```yaml
# manifests/memory/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: eve-system

resources:
  - qdrant.yaml
  - pvc.yaml
  - configmap.yaml
```

```yaml
# manifests/memory/qdrant.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: qdrant
  labels:
    app: qdrant
    component: memory
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
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "6333"
        prometheus.io/path: "/metrics"
    spec:
      securityContext:
        fsGroup: 1000
      containers:
        - name: qdrant
          image: qdrant/qdrant:v1.7.4
          ports:
            - name: http
              containerPort: 6333
            - name: grpc
              containerPort: 6334
          env:
            - name: QDRANT__SERVICE__GRPC_PORT
              value: "6334"
            - name: QDRANT__CLUSTER__ENABLED
              value: "false"
          volumeMounts:
            - name: qdrant-storage
              mountPath: /qdrant/storage
            - name: qdrant-config
              mountPath: /qdrant/config/production.yaml
              subPath: production.yaml
          resources:
            requests:
              cpu: 100m
              memory: 256Mi
            limits:
              cpu: 1000m
              memory: 2Gi
          livenessProbe:
            httpGet:
              path: /
              port: 6333
            initialDelaySeconds: 10
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: 6333
            initialDelaySeconds: 5
            periodSeconds: 5
  volumeClaimTemplates:
    - metadata:
        name: qdrant-storage
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 20Gi
---
apiVersion: v1
kind: Service
metadata:
  name: qdrant
  labels:
    app: qdrant
spec:
  type: ClusterIP
  selector:
    app: qdrant
  ports:
    - name: http
      port: 6333
      targetPort: 6333
    - name: grpc
      port: 6334
      targetPort: 6334
```

```yaml
# manifests/memory/pvc.yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: eve-memory-sqlite
  labels:
    app: eve
    component: memory
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
```

```yaml
# manifests/memory/configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: qdrant-config
  labels:
    app: qdrant
data:
  production.yaml: |
    storage:
      storage_path: /qdrant/storage

    service:
      host: 0.0.0.0
      http_port: 6333
      grpc_port: 6334
      max_request_size_mb: 32
      enable_cors: true

    log_level: INFO

    telemetry_disabled: true

    optimizers:
      indexing_threshold: 10000
      flush_interval_sec: 30
      max_optimization_threads: 2

    hnsw_index:
      m: 16
      ef_construct: 100
      full_scan_threshold: 10000
```

### 6.2 Eve Deployment Update

```yaml
# manifests/base/deployment.yaml (update)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: eve
spec:
  template:
    spec:
      containers:
        - name: eve
          env:
            # Memory System
            - name: MEMORY_ENABLED
              value: "true"
            - name: MEMORY_QDRANT_ADDR
              value: "qdrant.eve-system.svc.cluster.local:6334"
            - name: MEMORY_QDRANT_COLLECTION
              value: "eve_observations"
            - name: MEMORY_SQLITE_PATH
              value: "/data/sqlite/eve_memory.db"
            - name: MEMORY_EMBEDDER_TYPE
              value: "ollama"
            - name: MEMORY_EMBEDDER_MODEL
              value: "nomic-embed-text"
            - name: MEMORY_EMBEDDER_URL
              value: "http://ollama.eve-system.svc.cluster.local:11434"
          volumeMounts:
            - name: sqlite-data
              mountPath: /data/sqlite
      volumes:
        - name: sqlite-data
          persistentVolumeClaim:
            claimName: eve-memory-sqlite
```

---

## 7. Configuration

### 7.1 Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `MEMORY_ENABLED` | Enable memory system | `false` | No |
| `MEMORY_QDRANT_ADDR` | Qdrant gRPC address | `localhost:6334` | Yes* |
| `MEMORY_QDRANT_COLLECTION` | Collection name | `eve_observations` | No |
| `MEMORY_SQLITE_PATH` | SQLite database path | `/data/eve_memory.db` | No |
| `MEMORY_EMBEDDER_TYPE` | Embedder type (ollama/openai) | `ollama` | No |
| `MEMORY_EMBEDDER_MODEL` | Embedding model name | `nomic-embed-text` | No |
| `MEMORY_EMBEDDER_URL` | Embedder API URL | `http://localhost:11434` | Yes* |
| `MEMORY_EMBEDDER_API_KEY` | API key for OpenAI | - | If OpenAI |
| `MEMORY_SEARCH_LIMIT` | Default search limit | `10` | No |
| `MEMORY_MIN_SCORE` | Minimum similarity score | `0.7` | No |

### 7.2 Config Struct

```go
// internal/config/memory.go

package config

type MemoryConfig struct {
    Enabled bool `json:"enabled"`

    // Qdrant
    QdrantAddr       string `json:"qdrant_addr"`
    QdrantCollection string `json:"qdrant_collection"`

    // SQLite
    SQLitePath string `json:"sqlite_path"`

    // Embedder
    EmbedderType   string `json:"embedder_type"`
    EmbedderModel  string `json:"embedder_model"`
    EmbedderURL    string `json:"embedder_url"`
    EmbedderAPIKey string `json:"embedder_api_key"`

    // Search
    SearchLimit int     `json:"search_limit"`
    MinScore    float64 `json:"min_score"`
}

func LoadMemoryConfig() *MemoryConfig {
    return &MemoryConfig{
        Enabled:          os.Getenv("MEMORY_ENABLED") == "true",
        QdrantAddr:       getEnvOrDefault("MEMORY_QDRANT_ADDR", "localhost:6334"),
        QdrantCollection: getEnvOrDefault("MEMORY_QDRANT_COLLECTION", "eve_observations"),
        SQLitePath:       getEnvOrDefault("MEMORY_SQLITE_PATH", "/data/eve_memory.db"),
        EmbedderType:     getEnvOrDefault("MEMORY_EMBEDDER_TYPE", "ollama"),
        EmbedderModel:    getEnvOrDefault("MEMORY_EMBEDDER_MODEL", "nomic-embed-text"),
        EmbedderURL:      getEnvOrDefault("MEMORY_EMBEDDER_URL", "http://localhost:11434"),
        EmbedderAPIKey:   os.Getenv("MEMORY_EMBEDDER_API_KEY"),
        SearchLimit:      getEnvIntOrDefault("MEMORY_SEARCH_LIMIT", 10),
        MinScore:         getEnvFloatOrDefault("MEMORY_MIN_SCORE", 0.7),
    }
}
```

---

## 8. Testing

### 8.1 Unit Tests

```go
// internal/memory/store_test.go

package memory

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestStore_StoreAndSearch(t *testing.T) {
    store := setupTestStore(t)
    defer store.Close()

    ctx := context.Background()

    // Store observation
    obs := &Observation{
        Type:      ObservationTypeIncident,
        Category:  "kubernetes",
        SessionID: "test-session",
        ChannelID: "C123",
        UserID:    "U456",
        Title:     "Pod OOMKilled",
        Content:   "The api-gateway pod was killed due to memory exhaustion",
        Metadata: ObservationMetadata{
            Severity:     "critical",
            Namespace:    "production",
            Resource:     "api-gateway",
            ResourceKind: "Deployment",
        },
        Technologies: []string{"kubernetes", "memory-issue"},
    }

    err := store.Store(ctx, obs)
    require.NoError(t, err)
    assert.NotEmpty(t, obs.ID)

    // Wait for indexing
    time.Sleep(100 * time.Millisecond)

    // Search
    result, err := store.Search(ctx, "memory issue pod killed", SearchOptions{
        Limit:    10,
        MinScore: 0.5,
    })

    require.NoError(t, err)
    assert.GreaterOrEqual(t, len(result.Observations), 1)
    assert.Equal(t, "Pod OOMKilled", result.Observations[0].Title)
}

func TestStore_FilteredSearch(t *testing.T) {
    store := setupTestStore(t)
    defer store.Close()

    ctx := context.Background()

    // Store multiple observations
    obs1 := &Observation{
        Type:         ObservationTypeIncident,
        ChannelID:    "C123",
        Title:        "Database connection failed",
        Technologies: []string{"postgresql"},
    }
    obs2 := &Observation{
        Type:         ObservationTypeIncident,
        ChannelID:    "C456",
        Title:        "Redis timeout",
        Technologies: []string{"redis"},
    }

    store.Store(ctx, obs1)
    store.Store(ctx, obs2)

    time.Sleep(100 * time.Millisecond)

    // Search with channel filter
    result, err := store.Search(ctx, "connection issue", SearchOptions{
        ChannelID: "C123",
        MinScore:  0.3,
    })

    require.NoError(t, err)
    for _, obs := range result.Observations {
        assert.Equal(t, "C123", obs.ChannelID)
    }
}

func setupTestStore(t *testing.T) *Store {
    cfg := &Config{
        QdrantAddr:       "localhost:6334",
        QdrantCollection: "test_observations",
        SQLitePath:       ":memory:",
        EmbedderType:     "ollama",
        EmbedderModel:    "nomic-embed-text",
        EmbedderBaseURL:  "http://localhost:11434",
    }

    store, err := NewStore(cfg)
    require.NoError(t, err)

    return store
}
```

### 8.2 Integration Tests

```go
// test/integration/memory_integration_test.go

//go:build integration

package integration

import (
    "context"
    "testing"
    "time"

    "github.com/restack/eve/internal/memory"
    "github.com/stretchr/testify/require"
)

func TestMemoryIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    cfg := memory.DefaultConfig()
    store, err := memory.NewStore(cfg)
    require.NoError(t, err)
    defer store.Close()

    ctx := context.Background()

    // Health check
    err = store.HealthCheck(ctx)
    require.NoError(t, err)

    // Store incident
    incident := &memory.Observation{
        Type:      memory.ObservationTypeIncident,
        Category:  "kubernetes",
        SessionID: "integration-test",
        ChannelID: "C-TEST",
        UserID:    "U-TEST",
        Title:     "Integration Test Incident",
        Content:   "This is a test incident for integration testing",
        Metadata: memory.ObservationMetadata{
            Severity:  "warning",
            Namespace: "test",
            Resource:  "test-pod",
        },
        Technologies: []string{"kubernetes", "test"},
    }

    err = store.Store(ctx, incident)
    require.NoError(t, err)

    // Wait and search
    time.Sleep(500 * time.Millisecond)

    result, err := store.Search(ctx, "integration test incident", memory.SearchOptions{
        Limit:    5,
        MinScore: 0.5,
    })
    require.NoError(t, err)
    require.GreaterOrEqual(t, len(result.Observations), 1)
}
```

---

## 9. Monitoring

### 9.1 Metrics

```go
// internal/memory/metrics.go

package memory

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    observationsStored = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "eve_memory_observations_stored_total",
            Help: "Total number of observations stored",
        },
        []string{"type", "channel_id"},
    )

    searchRequests = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "eve_memory_search_requests_total",
            Help: "Total number of search requests",
        },
        []string{"channel_id"},
    )

    searchLatency = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "eve_memory_search_latency_seconds",
            Help:    "Search latency in seconds",
            Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
        },
        []string{"channel_id"},
    )

    embedLatency = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "eve_memory_embed_latency_seconds",
            Help:    "Embedding generation latency in seconds",
            Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0},
        },
    )

    qdrantHealthy = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "eve_memory_qdrant_healthy",
            Help: "Qdrant health status (1 = healthy, 0 = unhealthy)",
        },
    )
)
```

### 9.2 Grafana Dashboard

```json
{
  "title": "Eve Memory System",
  "panels": [
    {
      "title": "Observations Stored",
      "type": "stat",
      "targets": [
        {
          "expr": "sum(increase(eve_memory_observations_stored_total[24h]))"
        }
      ]
    },
    {
      "title": "Search Latency (p95)",
      "type": "graph",
      "targets": [
        {
          "expr": "histogram_quantile(0.95, sum(rate(eve_memory_search_latency_seconds_bucket[5m])) by (le))"
        }
      ]
    },
    {
      "title": "Qdrant Health",
      "type": "stat",
      "targets": [
        {
          "expr": "eve_memory_qdrant_healthy"
        }
      ]
    }
  ]
}
```

---

## 10. Future Enhancements (v2)

### 10.1 Planned Features

- [ ] Multi-cluster memory synchronization
- [ ] Automatic memory summarization (LLM-based)
- [ ] Memory decay/expiration policies
- [ ] User preference learning
- [ ] Incident pattern detection
- [ ] Proactive alerting based on historical patterns

### 10.2 Performance Optimizations

- [ ] Batch embedding with queue
- [ ] Read replicas for Qdrant
- [ ] Redis caching layer
- [ ] Async observation recording

---

## Appendix

### A. Dependencies

```go
// go.mod additions
require (
    github.com/qdrant/go-client v1.7.0
    github.com/mattn/go-sqlite3 v1.14.22
    github.com/google/uuid v1.6.0
    github.com/prometheus/client_golang v1.19.0
    google.golang.org/grpc v1.62.0
)
```

### B. File Structure

```
internal/memory/
├── interface.go      # MemoryStore interface
├── types.go          # Data types
├── store.go          # Main implementation
├── embedder.go       # Embedding generation
├── metrics.go        # Prometheus metrics
└── store_test.go     # Unit tests

manifests/memory/
├── kustomization.yaml
├── qdrant.yaml
├── pvc.yaml
└── configmap.yaml
```

### C. References

- [Qdrant Documentation](https://qdrant.tech/documentation/)
- [Qdrant Go Client](https://github.com/qdrant/go-client)
- [Ollama Embeddings API](https://github.com/ollama/ollama/blob/main/docs/api.md#generate-embeddings)
- [OpenAI Embeddings API](https://platform.openai.com/docs/api-reference/embeddings)
