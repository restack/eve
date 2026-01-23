package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_StoreAndSearch(t *testing.T) {
	// This test requires a running Qdrant instance.
	// In a real CI environment, we would use testcontainers or a mock.
	// For now, we skip if QDRANT_ADDR is not set or we use a more unit-testable approach if possible.
	t.Skip("Skipping integration test that requires Qdrant")

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

	err = store.Store(ctx, obs)
	require.NoError(t, err)
	assert.NotEmpty(t, obs.ID)

	// Wait for indexing (Qdrant is eventually consistent)
	time.Sleep(200 * time.Millisecond)

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
	t.Skip("Skipping integration test that requires Qdrant")

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

	time.Sleep(200 * time.Millisecond)

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
