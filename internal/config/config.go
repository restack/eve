// Package config handles configuration loading from environment variables.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config holds all configuration for Eve.
type Config struct {
	// Slack configuration
	SlackAppToken string
	SlackBotToken string

	// LLM configuration
	LLMProvider string // "ollama" or "openai"
	LLMBaseURL  string // e.g., "http://localhost:11434"
	LLMModel    string // e.g., "qwen3-coder"
	LLMAPIKey   string // Optional

	// MCP configuration
	MCPServers []string // List of MCP server endpoit URLs

	// Access control
	AllowedUserIDs    []string
	AllowedChannelIDs []string

	// Recipe mappings
	RecipeMappings map[string]string

	// Memory system configuration
	Memory *MemoryConfig

	// LLM Sampling configuration
	Sampling *LLMSamplingConfig
}

// LLMSamplingConfig holds parameters for LLM text generation
type LLMSamplingConfig struct {
	Temperature      *float64
	TopP             *float64
	TopK             *int
	MaxTokens        *int
	PresencePenalty  *float64
	FrequencyPenalty *float64
	Seed             *int
	MinP             *float64
	TypicalP         *float64
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		SlackAppToken: os.Getenv("SLACK_APP_TOKEN"),
		SlackBotToken: os.Getenv("SLACK_BOT_TOKEN"),
		LLMProvider:   getEnvOrDefault("LLM_PROVIDER", "openai"),
		LLMBaseURL:    getEnvOrDefault("LLM_BASE_URL", "http://qwen.home.lab:8003"),
		LLMModel:      getEnvOrDefault("LLM_MODEL", "qwen3-coder"),
		LLMAPIKey:     os.Getenv("LLM_API_KEY"),
	}

	// Track unique servers
	serverMap := make(map[string]bool)

	// Parse MCP servers from environment variable
	if mcpServers := os.Getenv("MCP_SERVERS"); mcpServers != "" {
		for _, s := range parseCSV(mcpServers) {
			serverMap[s] = true
		}
	}

	// Also try to load from mcp.json if it exists
	if mcpJsonPath := getEnvOrDefault("MCP_JSON_PATH", "mcp.json"); mcpJsonPath != "" {
		if data, err := os.ReadFile(mcpJsonPath); err == nil {
			var mcpConfig struct {
				MCPServers map[string]struct {
					URL string            `json:"url"`
					Env map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(data, &mcpConfig); err != nil {
				slog.Error("failed to parse mcp.json", "error", err)
			} else {
				for _, server := range mcpConfig.MCPServers {
					if server.URL != "" {
						serverMap[server.URL] = true
					}
					// Apply environment variables from mcp.json if any
					for k, v := range server.Env {
						// Only set if not already set in environment
						if os.Getenv(k) == "" {
							// Expand variables like $HOME
							expanded := os.ExpandEnv(v)
							os.Setenv(k, expanded)
						}
					}
				}
			}
		}
	}

	// Convert map back to slice
	for url := range serverMap {
		cfg.MCPServers = append(cfg.MCPServers, url)
	}

	// Parse allowed users/channels
	if userIDs := os.Getenv("ALLOWED_USER_IDS"); userIDs != "" {
		cfg.AllowedUserIDs = parseCSV(userIDs)
	}
	if channelIDs := os.Getenv("ALLOWED_CHANNEL_IDS"); channelIDs != "" {
		cfg.AllowedChannelIDs = parseCSV(channelIDs)
	}

	// Parse recipe mappings
	if mappings := os.Getenv("RECIPE_MAPPINGS"); mappings != "" {
		cfg.RecipeMappings = make(map[string]string)
		for _, mapping := range parseCSV(mappings) {
			parts := strings.SplitN(mapping, ":", 2)
			if len(parts) == 2 {
				cfg.RecipeMappings[parts[0]] = parts[1]
			}
		}
	}

	// Validation
	if cfg.SlackAppToken == "" || cfg.SlackBotToken == "" {
		return nil, fmt.Errorf("SLACK_APP_TOKEN and SLACK_BOT_TOKEN are required")
	}

	// Load memory configuration
	cfg.Memory = LoadMemoryConfig()

	// Load sampling configuration
	cfg.Sampling = loadSamplingConfig()

	return cfg, nil
}

func loadSamplingConfig() *LLMSamplingConfig {
	return &LLMSamplingConfig{
		Temperature:      getEnvFloat64Ptr("LLM_TEMPERATURE"),
		TopP:             getEnvFloat64Ptr("LLM_TOP_P"),
		TopK:             getEnvIntPtr("LLM_TOP_K"),
		MaxTokens:        getEnvIntPtr("LLM_MAX_TOKENS"),
		PresencePenalty:  getEnvFloat64Ptr("LLM_PRESENCE_PENALTY"),
		FrequencyPenalty: getEnvFloat64Ptr("LLM_FREQUENCY_PENALTY"),
		Seed:             getEnvIntPtr("LLM_SEED"),
		MinP:             getEnvFloat64Ptr("LLM_MIN_P"),
		TypicalP:         getEnvFloat64Ptr("LLM_TYPICAL_P"),
	}
}

func getEnvFloat64Ptr(key string) *float64 {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	var f float64
	if _, err := fmt.Sscanf(val, "%f", &f); err != nil {
		return nil
	}
	return &f
}

func getEnvIntPtr(key string) *int {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	var i int
	if _, err := fmt.Sscanf(val, "%d", &i); err != nil {
		return nil
	}
	return &i
}

// IsUserAllowed checks authorization
func (c *Config) IsUserAllowed(userID string) bool {
	if len(c.AllowedUserIDs) == 0 {
		return true
	}
	for _, id := range c.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// IsChannelAllowed checks authorization
func (c *Config) IsChannelAllowed(channelID string) bool {
	if len(c.AllowedChannelIDs) == 0 {
		return true
	}
	for _, id := range c.AllowedChannelIDs {
		if id == channelID {
			return true
		}
	}
	return false
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseCSV(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
