// Package config handles configuration loading from environment variables.
package config

import (
	"encoding/json"
	"fmt"
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

	// Parse MCP servers from environment variable
	if mcpServers := os.Getenv("MCP_SERVERS"); mcpServers != "" {
		cfg.MCPServers = append(cfg.MCPServers, parseCSV(mcpServers)...)
	}

	// Also try to load from mcp.json if it exists
	if mcpJsonPath := getEnvOrDefault("MCP_JSON_PATH", "mcp.json"); mcpJsonPath != "" {
		if data, err := os.ReadFile(mcpJsonPath); err == nil {
			var mcpConfig struct {
				MCPServers map[string]struct {
					URL string `json:"url"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(data, &mcpConfig); err == nil {
				for _, server := range mcpConfig.MCPServers {
					if server.URL != "" {
						cfg.MCPServers = append(cfg.MCPServers, server.URL)
					}
				}
			}
		}
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

	return cfg, nil
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
