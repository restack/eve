package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"crypto/tls"

	"github.com/google/uuid"
	pb "github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	_ "github.com/mattn/go-sqlite3"
)

// Config is the memory store configuration
type Config struct {
	// Qdrant
	QdrantAddr       string `json:"qdrant_addr"`
	QdrantCollection string `json:"qdrant_collection"`
	QdrantAPIKey     string `json:"qdrant_api_key"`

	// SQLite
	SQLitePath string `json:"sqlite_path"`

	// Embedder
	EmbedderType    string `json:"embedder_type"` // ollama, openai
	EmbedderModel   string `json:"embedder_model"`
	EmbedderBaseURL string `json:"embedder_base_url,omitempty"`
	EmbedderAPIKey  string `json:"embedder_api_key,omitempty"`

	// Options
	SearchLimit int     `json:"search_limit"`
	MinScore    float64 `json:"min_score"`
	BatchSize   int     `json:"batch_size"`
	EnableCache bool    `json:"enable_cache"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		QdrantAddr:       "localhost:6334",
		QdrantCollection: "eve",
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

// Store is the MemoryStore implementation
type Store struct {
	config     *Config
	qdrant     pb.PointsClient
	qdrantConn *grpc.ClientConn
	sqlite     *sql.DB
	embedder   Embedder

	mu sync.RWMutex
}

// NewStore creates a new Store
func NewStore(cfg *Config) (*Store, error) {
	// Connect to Qdrant
	var dialOpts []grpc.DialOption
	if strings.HasSuffix(cfg.QdrantAddr, ":443") {
		// Use TLS for port 443
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true, // Allow self-signed certs if needed
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Add API key if provided
	if cfg.QdrantAPIKey != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(qdrantCreds{apiKey: cfg.QdrantAPIKey}))
	}

	conn, err := grpc.Dial(cfg.QdrantAddr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to qdrant: %w", err)
	}

	// Connect to SQLite
	db, err := sql.Open("sqlite3", cfg.SQLitePath)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	// Initialize schema
	if err := initSchema(db); err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	// Create Embedder
	embedder, err := NewEmbedder(cfg)
	if err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("failed to create embedder: %w", err)
	}

	// Ensure Qdrant collection exists
	collectionsClient := pb.NewCollectionsClient(conn)
	if err := ensureCollection(collectionsClient, cfg.QdrantCollection, embedder.Dimension()); err != nil {
		slog.Warn("failed to ensure qdrant collection", "error", err)
		// Continue anyway, collection might already exist
	}

	return &Store{
		config:     cfg,
		qdrant:     pb.NewPointsClient(conn),
		qdrantConn: conn,
		sqlite:     db,
		embedder:   embedder,
	}, nil
}

// Store saves an observation
func (s *Store) Store(ctx context.Context, obs *Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate ID
	if obs.ID == "" {
		obs.ID = uuid.New().String()
	}
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = time.Now().UTC()
	}
	obs.UpdatedAt = time.Now().UTC()

	// Generate summary if missing
	if obs.Summary == "" {
		obs.Summary = generateSummary(obs)
	}

	// Generate vector
	textToEmbed := fmt.Sprintf("%s %s %s", obs.Title, obs.Summary, obs.Content)
	vector, err := s.embedder.Embed(ctx, textToEmbed)
	if err != nil {
		return fmt.Errorf("failed to embed: %w", err)
	}
	obs.Vector = vector

	// Store to Qdrant
	if err := s.storeToQdrant(ctx, obs); err != nil {
		return fmt.Errorf("failed to store to qdrant: %w", err)
	}

	// Store summary to SQLite
	if err := s.storeSummaryToSQLite(ctx, obs); err != nil {
		slog.Warn("failed to store summary to sqlite", "error", err)
		// Continue, Qdrant storage succeeded
	}

	return nil
}

// StoreBatch saves multiple observations
func (s *Store) StoreBatch(ctx context.Context, observations []*Observation) error {
	for _, obs := range observations {
		if err := s.Store(ctx, obs); err != nil {
			return fmt.Errorf("failed to store observation %s: %w", obs.ID, err)
		}
	}
	return nil
}

// Search searches for related observations
func (s *Store) Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error) {
	start := time.Now()

	// Generate query vector
	vector, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// Build filter
	filter := s.buildFilter(opts)

	// Set limit
	limit := opts.Limit
	if limit == 0 {
		limit = s.config.SearchLimit
	}

	minScore := opts.MinScore
	if minScore == 0 {
		minScore = s.config.MinScore
	}

	// Search Qdrant
	resp, err := s.qdrant.Search(ctx, &pb.SearchPoints{
		CollectionName: s.config.QdrantCollection,
		Vector:         vector,
		Limit:          uint64(limit),
		Filter:         filter,
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
		ScoreThreshold: float32Ptr(float32(minScore)),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant search failed: %w", err)
	}

	// Convert results
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

// GetByID retrieves an observation by ID
func (s *Store) GetByID(ctx context.Context, id string) (*Observation, error) {
	resp, err := s.qdrant.Get(ctx, &pb.GetPoints{
		CollectionName: s.config.QdrantCollection,
		Ids:            []*pb.PointId{{PointIdOptions: &pb.PointId_Uuid{Uuid: id}}},
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant get failed: %w", err)
	}

	if len(resp.Result) == 0 {
		return nil, nil
	}

	obs := s.retrievedPointToObservation(resp.Result[0])
	return &obs, nil
}

// GetByIDs retrieves multiple observations by IDs
func (s *Store) GetByIDs(ctx context.Context, ids []string) ([]*Observation, error) {
	pointIds := make([]*pb.PointId, len(ids))
	for i, id := range ids {
		pointIds[i] = &pb.PointId{PointIdOptions: &pb.PointId_Uuid{Uuid: id}}
	}

	resp, err := s.qdrant.Get(ctx, &pb.GetPoints{
		CollectionName: s.config.QdrantCollection,
		Ids:            pointIds,
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant get failed: %w", err)
	}

	observations := make([]*Observation, len(resp.Result))
	for i, point := range resp.Result {
		obs := s.retrievedPointToObservation(point)
		observations[i] = &obs
	}

	return observations, nil
}

// GetTimeline returns temporal context around an observation
func (s *Store) GetTimeline(ctx context.Context, observationID string, windowMinutes int) ([]*Observation, error) {
	// Get target observation
	obs, err := s.GetByID(ctx, observationID)
	if err != nil {
		return nil, err
	}
	if obs == nil {
		return nil, fmt.Errorf("observation not found: %s", observationID)
	}

	// Calculate time range
	start := obs.CreatedAt.Add(-time.Duration(windowMinutes) * time.Minute)
	end := obs.CreatedAt.Add(time.Duration(windowMinutes) * time.Minute)

	// Query observations in the same session
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

	// Filter by time range
	var timeline []*Observation
	for _, point := range resp.Result {
		o := s.retrievedPointToObservation(point)
		if o.CreatedAt.After(start) && o.CreatedAt.Before(end) {
			timeline = append(timeline, &o)
		}
	}

	return timeline, nil
}

// GetSessionObservations returns all observations for a session
func (s *Store) GetSessionObservations(ctx context.Context, sessionID string) ([]*Observation, error) {
	filter := &pb.Filter{
		Must: []*pb.Condition{
			{
				ConditionOneOf: &pb.Condition_Field{
					Field: &pb.FieldCondition{
						Key: "session_id",
						Match: &pb.Match{
							MatchValue: &pb.Match_Keyword{Keyword: sessionID},
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
		Limit:          uint32Ptr(1000),
	})
	if err != nil {
		return nil, err
	}

	observations := make([]*Observation, len(resp.Result))
	for i, point := range resp.Result {
		obs := s.retrievedPointToObservation(point)
		observations[i] = &obs
	}

	return observations, nil
}

// CreateSession creates a new session
func (s *Store) CreateSession(ctx context.Context, session *Session) error {
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = time.Now().UTC()
	}

	topicsJSON, _ := json.Marshal(session.Topics)
	techsJSON, _ := json.Marshal(session.Technologies)

	_, err := s.sqlite.ExecContext(ctx, `
		INSERT INTO sessions (id, started_at, channel_id, user_id, thread_ts, summary, topics, technologies, message_count, tool_call_count, observation_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, session.ID, session.StartedAt, session.ChannelID, session.UserID, session.ThreadTS,
		session.Summary, string(topicsJSON), string(techsJSON),
		session.MessageCount, session.ToolCallCount, session.ObservationCount)

	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// UpdateSession updates a session
func (s *Store) UpdateSession(ctx context.Context, session *Session) error {
	topicsJSON, _ := json.Marshal(session.Topics)
	techsJSON, _ := json.Marshal(session.Technologies)

	_, err := s.sqlite.ExecContext(ctx, `
		UPDATE sessions SET
			summary = ?,
			topics = ?,
			technologies = ?,
			message_count = ?,
			tool_call_count = ?,
			observation_count = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, session.Summary, string(topicsJSON), string(techsJSON),
		session.MessageCount, session.ToolCallCount, session.ObservationCount, session.ID)

	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by ID
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	row := s.sqlite.QueryRowContext(ctx, `
		SELECT id, started_at, ended_at, channel_id, user_id, thread_ts, summary, topics, technologies, message_count, tool_call_count, observation_count
		FROM sessions WHERE id = ?
	`, id)

	var session Session
	var endedAt sql.NullTime
	var topicsJSON, techsJSON string

	err := row.Scan(&session.ID, &session.StartedAt, &endedAt, &session.ChannelID, &session.UserID,
		&session.ThreadTS, &session.Summary, &topicsJSON, &techsJSON,
		&session.MessageCount, &session.ToolCallCount, &session.ObservationCount)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	if endedAt.Valid {
		session.EndedAt = endedAt.Time
	}
	json.Unmarshal([]byte(topicsJSON), &session.Topics)
	json.Unmarshal([]byte(techsJSON), &session.Technologies)

	return &session, nil
}

// EndSession ends a session with a summary
func (s *Store) EndSession(ctx context.Context, id string, summary string) error {
	_, err := s.sqlite.ExecContext(ctx, `
		UPDATE sessions SET ended_at = CURRENT_TIMESTAMP, summary = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, summary, id)

	if err != nil {
		return fmt.Errorf("failed to end session: %w", err)
	}
	return nil
}

// GetStats returns statistics
func (s *Store) GetStats(ctx context.Context, channelID string, days int) (*Stats, error) {
	stats := &Stats{
		ObservationsByType: make(map[string]int64),
		TopTechnologies:    []TechCount{},
	}

	// Count sessions
	row := s.sqlite.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions")
	row.Scan(&stats.TotalSessions)

	// Count observations from sqlite summaries
	row = s.sqlite.QueryRowContext(ctx, "SELECT COUNT(*) FROM observation_summaries")
	row.Scan(&stats.TotalObservations)

	// Count by type
	rows, err := s.sqlite.QueryContext(ctx, "SELECT type, COUNT(*) FROM observation_summaries GROUP BY type")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var obsType string
			var count int64
			rows.Scan(&obsType, &count)
			stats.ObservationsByType[obsType] = count
		}
	}

	return stats, nil
}

// Close closes all connections
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

// HealthCheck checks system health
func (s *Store) HealthCheck(ctx context.Context) error {
	// Qdrant health check
	collectionsClient := pb.NewCollectionsClient(s.qdrantConn)
	_, err := collectionsClient.Get(ctx, &pb.GetCollectionInfoRequest{
		CollectionName: s.config.QdrantCollection,
	})
	if err != nil {
		return fmt.Errorf("qdrant health check failed: %w", err)
	}

	// SQLite health check
	if err := s.sqlite.PingContext(ctx); err != nil {
		return fmt.Errorf("sqlite health check failed: %w", err)
	}

	return nil
}

// Helper functions

func (s *Store) buildFilter(opts SearchOptions) *pb.Filter {
	var conditions []*pb.Condition

	if len(opts.Types) > 0 {
		typeStrings := make([]string, len(opts.Types))
		for i, t := range opts.Types {
			typeStrings[i] = string(t)
		}
		conditions = append(conditions, &pb.Condition{
			ConditionOneOf: &pb.Condition_Field{
				Field: &pb.FieldCondition{
					Key: "type",
					Match: &pb.Match{
						MatchValue: &pb.Match_Keywords{Keywords: &pb.RepeatedStrings{Strings: typeStrings}},
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

	if opts.UserID != "" {
		conditions = append(conditions, &pb.Condition{
			ConditionOneOf: &pb.Condition_Field{
				Field: &pb.FieldCondition{
					Key: "user_id",
					Match: &pb.Match{
						MatchValue: &pb.Match_Keyword{Keyword: opts.UserID},
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

func (s *Store) storeSummaryToSQLite(ctx context.Context, obs *Observation) error {
	techsJSON, _ := json.Marshal(obs.Technologies)
	_, err := s.sqlite.ExecContext(ctx, `
		INSERT OR REPLACE INTO observation_summaries
		(observation_id, title, summary, type, category, technologies, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, obs.ID, obs.Title, obs.Summary, obs.Type, obs.Category, string(techsJSON), obs.CreatedAt)
	return err
}

func (s *Store) pointToObservation(point *pb.ScoredPoint) Observation {
	payload := point.Payload
	return Observation{
		ID:           point.Id.GetUuid(),
		Type:         ObservationType(getStringValue(payload, "type")),
		Category:     getStringValue(payload, "category"),
		SessionID:    getStringValue(payload, "session_id"),
		ChannelID:    getStringValue(payload, "channel_id"),
		UserID:       getStringValue(payload, "user_id"),
		Title:        getStringValue(payload, "title"),
		Summary:      getStringValue(payload, "summary"),
		Content:      getStringValue(payload, "content"),
		Technologies: getStringList(payload["technologies"]),
		Keywords:     getStringList(payload["keywords"]),
		Score:        float64(point.Score),
		CreatedAt:    parseTime(getStringValue(payload, "created_at")),
	}
}

func (s *Store) retrievedPointToObservation(point *pb.RetrievedPoint) Observation {
	payload := point.Payload
	return Observation{
		ID:           point.Id.GetUuid(),
		Type:         ObservationType(getStringValue(payload, "type")),
		Category:     getStringValue(payload, "category"),
		SessionID:    getStringValue(payload, "session_id"),
		ChannelID:    getStringValue(payload, "channel_id"),
		UserID:       getStringValue(payload, "user_id"),
		Title:        getStringValue(payload, "title"),
		Summary:      getStringValue(payload, "summary"),
		Content:      getStringValue(payload, "content"),
		Technologies: getStringList(payload["technologies"]),
		Keywords:     getStringList(payload["keywords"]),
		CreatedAt:    parseTime(getStringValue(payload, "created_at")),
	}
}

func observationToPayload(obs *Observation) map[string]*pb.Value {
	return map[string]*pb.Value{
		"type":          stringValue(string(obs.Type)),
		"category":      stringValue(obs.Category),
		"session_id":    stringValue(obs.SessionID),
		"channel_id":    stringValue(obs.ChannelID),
		"user_id":       stringValue(obs.UserID),
		"title":         stringValue(obs.Title),
		"summary":       stringValue(obs.Summary),
		"content":       stringValue(obs.Content),
		"technologies":  stringListValue(obs.Technologies),
		"keywords":      stringListValue(obs.Keywords),
		"created_at":    stringValue(obs.CreatedAt.Format(time.RFC3339)),
		"severity":      stringValue(obs.Metadata.Severity),
		"namespace":     stringValue(obs.Metadata.Namespace),
		"resource_kind": stringValue(obs.Metadata.ResourceKind),
		"success":       boolValue(obs.Metadata.Success),
	}
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

func ensureCollection(client pb.CollectionsClient, name string, dimension int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check if collection exists
	_, err := client.Get(ctx, &pb.GetCollectionInfoRequest{
		CollectionName: name,
	})
	if err == nil {
		return nil // Collection exists
	}

	// Create collection
	_, err = client.Create(ctx, &pb.CreateCollection{
		CollectionName: name,
		VectorsConfig: &pb.VectorsConfig{
			Config: &pb.VectorsConfig_Params{
				Params: &pb.VectorParams{
					Size:     uint64(dimension),
					Distance: pb.Distance_Cosine,
				},
			},
		},
	})
	return err
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
func uint32Ptr(u uint32) *uint32    { return &u }

func getStringValue(payload map[string]*pb.Value, key string) string {
	if v, ok := payload[key]; ok {
		return v.GetStringValue()
	}
	return ""
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

func joinStrings(ss []string) string {
	return strings.Join(ss, ",")
}

// Ensure Store implements MemoryStore
var _ MemoryStore = (*Store)(nil)

// qdrantCreds implements grpc.PerRPCCredentials for Qdrant API key auth
type qdrantCreds struct {
	apiKey string
}

func (c qdrantCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"api-key": c.apiKey,
	}, nil
}

func (c qdrantCreds) RequireTransportSecurity() bool {
	return false // API key can be sent over insecure gRPC if needed, but usually used with TLS
}
