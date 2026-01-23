// Package main is the entry point for the Eve Slack bot.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/restack/eve/internal/agent"
	"github.com/restack/eve/internal/config"
	"github.com/restack/eve/internal/llm"
	"github.com/restack/eve/internal/mcp"
	"github.com/restack/eve/internal/memory"
	"github.com/restack/eve/internal/slack"
	"github.com/restack/eve/internal/tools"
)

func main() {
	// Configure structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("eve starting", "version", "0.3.0", "mode", "mcp-proxy")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Create LLM client
	var llmClient llm.Client
	switch cfg.LLMProvider {
	case "ollama":
		llmClient = llm.NewOllamaClient(cfg.LLMBaseURL, cfg.LLMModel)
	case "openai":
		llmClient = llm.NewOpenAICompatibleClient(cfg.LLMBaseURL, cfg.LLMModel, cfg.LLMAPIKey)
	default:
		slog.Error("unknown LLM provider", "provider", cfg.LLMProvider)
		os.Exit(1)
	}

	// Create and initialize tool registry
	registry := tools.NewRegistry()
	tools.RegisterKubernetesTools(registry) // Register fallback tools
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	// Discover tools from configured MCP servers
	for _, serverURL := range cfg.MCPServers {
		slog.Info("connecting to MCP server", "url", serverURL)
		mcpClient := mcp.NewClient(serverURL)

		if err := mcpClient.Initialize(ctx); err != nil {
			slog.Error("failed to initialize MCP client", "url", serverURL, "error", err)
			continue
		}

		if err := registry.LoadFromMCP(ctx, mcpClient); err != nil {
			slog.Error("failed to load tools from MCP server", "url", serverURL, "error", err)
			continue
		}
	}
	cancel()

	slog.Info("tool discovery complete", "total_tools", len(registry.List()))

	// Initialize memory store if enabled
	var memStore memory.MemoryStore = &memory.NoopMemoryStore{}
	if cfg.Memory != nil && cfg.Memory.Enabled {
		slog.Info("initializing memory store", "qdrant", cfg.Memory.QdrantAddr)

		memCfg := &memory.Config{
			QdrantAddr:       cfg.Memory.QdrantAddr,
			QdrantCollection: cfg.Memory.QdrantCollection,
			QdrantAPIKey:     cfg.Memory.QdrantAPIKey,
			SQLitePath:       cfg.Memory.SQLitePath,
			EmbedderType:     cfg.Memory.EmbedderType,
			EmbedderModel:    cfg.Memory.EmbedderModel,
			EmbedderBaseURL:  cfg.Memory.EmbedderURL,
			EmbedderAPIKey:   cfg.Memory.EmbedderAPIKey,
			SearchLimit:      cfg.Memory.SearchLimit,
			MinScore:         cfg.Memory.MinScore,
		}

		store, err := memory.NewStore(memCfg)
		if err != nil {
			slog.Error("failed to initialize memory store", "error", err)
			// Decide if this should be fatal. For now, degrad gracefully to Noop.
		} else {
			memStore = store
			defer store.Close()
		}
	}

	// Create agent
	var ag agent.Agent
	if cfg.Memory != nil && cfg.Memory.Enabled {
		slog.Info("creating memory-aware agent")
		ag = agent.NewMemoryAgent(llmClient, registry, memStore, cfg)
	} else {
		slog.Info("creating basic agent")
		ag = agent.NewAgent(llmClient, registry, cfg)
	}

	// Create Slack handler
	handler, err := slack.NewHandler(cfg, ag, registry)
	if err != nil {
		slog.Error("failed to create slack handler", "error", err)
		os.Exit(1)
	}

	// Setup graceful shutdown
	runCtx, stop := context.WithCancel(context.Background())
	defer stop()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		slog.Info("shutting down")
		stop()
	}()

	slog.Info("starting slack socket mode handler")
	if err := handler.Run(runCtx); err != nil {
		slog.Error("slack handler error", "error", err)
		os.Exit(1)
	}
}
