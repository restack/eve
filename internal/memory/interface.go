package memory

import (
	"context"
)

// MemoryStore is the main interface for the memory system
type MemoryStore interface {
	// Store observations
	Store(ctx context.Context, obs *Observation) error
	StoreBatch(ctx context.Context, observations []*Observation) error

	// Search
	Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error)
	GetByID(ctx context.Context, id string) (*Observation, error)
	GetByIDs(ctx context.Context, ids []string) ([]*Observation, error)

	// Timeline
	GetTimeline(ctx context.Context, observationID string, windowMinutes int) ([]*Observation, error)
	GetSessionObservations(ctx context.Context, sessionID string) ([]*Observation, error)

	// Session management
	CreateSession(ctx context.Context, session *Session) error
	UpdateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	EndSession(ctx context.Context, id string, summary string) error

	// Statistics
	GetStats(ctx context.Context, channelID string, days int) (*Stats, error)

	// Maintenance
	Close() error
	HealthCheck(ctx context.Context) error
}

// Embedder converts text to vectors
type Embedder interface {
	// Embed converts a single text to a vector
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch converts multiple texts to vectors
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// Dimension returns the vector dimension
	Dimension() int
}

// NoopMemoryStore is a no-op implementation for when memory is disabled
type NoopMemoryStore struct{}

// Ensure NoopMemoryStore implements MemoryStore
var _ MemoryStore = (*NoopMemoryStore)(nil)

func (n *NoopMemoryStore) Store(ctx context.Context, obs *Observation) error {
	return nil
}

func (n *NoopMemoryStore) StoreBatch(ctx context.Context, observations []*Observation) error {
	return nil
}

func (n *NoopMemoryStore) Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error) {
	return &SearchResult{
		Observations: []Observation{},
		TotalCount:   0,
		Query:        query,
	}, nil
}

func (n *NoopMemoryStore) GetByID(ctx context.Context, id string) (*Observation, error) {
	return nil, nil
}

func (n *NoopMemoryStore) GetByIDs(ctx context.Context, ids []string) ([]*Observation, error) {
	return nil, nil
}

func (n *NoopMemoryStore) GetTimeline(ctx context.Context, observationID string, windowMinutes int) ([]*Observation, error) {
	return nil, nil
}

func (n *NoopMemoryStore) GetSessionObservations(ctx context.Context, sessionID string) ([]*Observation, error) {
	return nil, nil
}

func (n *NoopMemoryStore) CreateSession(ctx context.Context, session *Session) error {
	return nil
}

func (n *NoopMemoryStore) UpdateSession(ctx context.Context, session *Session) error {
	return nil
}

func (n *NoopMemoryStore) GetSession(ctx context.Context, id string) (*Session, error) {
	return nil, nil
}

func (n *NoopMemoryStore) EndSession(ctx context.Context, id string, summary string) error {
	return nil
}

func (n *NoopMemoryStore) GetStats(ctx context.Context, channelID string, days int) (*Stats, error) {
	return &Stats{
		ObservationsByType: make(map[string]int64),
		TopTechnologies:    []TechCount{},
	}, nil
}

func (n *NoopMemoryStore) Close() error {
	return nil
}

func (n *NoopMemoryStore) HealthCheck(ctx context.Context) error {
	return nil
}
