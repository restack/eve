package config

import (
	"os"
	"strconv"
)

// MemoryConfig holds configuration for the memory system
type MemoryConfig struct {
	// Enable/disable memory system
	Enabled bool `json:"enabled"`

	// Qdrant configuration
	QdrantAddr       string `json:"qdrant_addr"`
	QdrantCollection string `json:"qdrant_collection"`
	QdrantAPIKey     string `json:"qdrant_api_key"`

	// SQLite configuration
	SQLitePath string `json:"sqlite_path"`

	// Embedder configuration
	EmbedderType   string `json:"embedder_type"` // ollama or openai
	EmbedderModel  string `json:"embedder_model"`
	EmbedderURL    string `json:"embedder_url"`
	EmbedderAPIKey string `json:"embedder_api_key"`

	// Search configuration
	SearchLimit int     `json:"search_limit"`
	MinScore    float64 `json:"min_score"`
}

// LoadMemoryConfig loads memory configuration from environment variables
func LoadMemoryConfig() *MemoryConfig {
	return &MemoryConfig{
		Enabled:          getEnvBool("MEMORY_ENABLED", false),
		QdrantAddr:       getEnvOrDefault("MEMORY_QDRANT_ADDR", "localhost:6334"),
		QdrantCollection: getEnvOrDefault("MEMORY_QDRANT_COLLECTION", "eve_observations"),
		QdrantAPIKey:     os.Getenv("MEMORY_QDRANT_API_KEY"),
		SQLitePath:       getEnvOrDefault("MEMORY_SQLITE_PATH", "/data/eve_memory.db"),
		EmbedderType:     getEnvOrDefault("MEMORY_EMBEDDER_TYPE", "ollama"),
		EmbedderModel:    getEnvOrDefault("MEMORY_EMBEDDER_MODEL", "nomic-embed-text"),
		EmbedderURL:      getEnvOrDefault("MEMORY_EMBEDDER_URL", "http://localhost:11434"),
		EmbedderAPIKey:   os.Getenv("MEMORY_EMBEDDER_API_KEY"),
		SearchLimit:      getEnvInt("MEMORY_SEARCH_LIMIT", 10),
		MinScore:         getEnvFloat("MEMORY_MIN_SCORE", 0.7),
	}
}

// ToMemoryStoreConfig converts MemoryConfig to memory.Config
func (m *MemoryConfig) ToMemoryStoreConfig() interface{} {
	// Returns a map that can be used to initialize memory.Config
	return map[string]interface{}{
		"qdrant_addr":       m.QdrantAddr,
		"qdrant_collection": m.QdrantCollection,
		"qdrant_api_key":    m.QdrantAPIKey,
		"sqlite_path":       m.SQLitePath,
		"embedder_type":     m.EmbedderType,
		"embedder_model":    m.EmbedderModel,
		"embedder_base_url": m.EmbedderURL,
		"embedder_api_key":  m.EmbedderAPIKey,
		"search_limit":      m.SearchLimit,
		"min_score":         m.MinScore,
	}
}

// Helper functions

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}
