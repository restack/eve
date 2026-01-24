# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Eve** is a Kubernetes operations agent that acts as an MCP (Model Context Protocol) Proxy between Slack, a local LLM, and external MCP servers. It provides autonomous SRE capabilities through natural language interaction.

**Core Philosophy**: Don't reinvent the wheel - Eve focuses on orchestration and gateway functionality while delegating domain-specific logic to standardized MCP servers.

## Development Commands

### Build & Run
```bash
# Build the binary
just build
# or: go build -v -o eve ./cmd/eve

# Run locally (requires .env configuration)
just run

# Run tests
just test
# or: go test -v ./...

# Run a specific test
go test -v ./internal/memory -run TestStoreName

# Clean build artifacts
just clean

# Full dev cycle (tidy deps + build + run)
just dev
```

### Docker
```bash
# Build Docker image
just docker-build
# or: docker build -t eve:latest .
```

### Dependencies
```bash
# Tidy Go modules
just tidy
# or: go mod tidy
```

## Architecture

### High-Level Design

Eve operates as a **Supervisor Agent** with three main integration points:
1. **Slack** (via Socket Mode) - User interface and notifications
2. **LLM** (OpenAI-compatible API) - Intelligence layer for tool orchestration
3. **MCP Servers** (via JSON-RPC over HTTP) - Tool providers (K8s, GitHub, Argo, AWS, etc.)

### Key Components

**`cmd/eve/main.go`** - Entry point that:
- Loads configuration from environment variables and `mcp.json`
- Initializes LLM client (Ollama or OpenAI-compatible)
- Discovers and registers tools from MCP servers
- Initializes memory store (optional)
- Creates agent (basic or memory-aware)
- Starts Slack Socket Mode handler

**`internal/agent/`** - Agentic loop implementation:
- **`agent.go`**: BaseAgent with ReAct-style loop (max 10 iterations)
- **`memory_agent.go`**: MemoryAgent with context retrieval and session tracking
  - Implements selective tool schema injection based on query classification (casual vs SRE)
  - Manages short-term (SQLite) and long-term (Qdrant) memory
  - Records tool executions and chat messages for future reference

**`internal/mcp/`** - MCP protocol client:
- Implements JSON-RPC over HTTP/SSE
- Handles `initialize`, `tools/list`, and `tools/call` methods
- Supports both JSON and SSE response formats
- Tool discovery happens at startup via handshake

**`internal/slack/`** - Slack integration:
- Socket Mode event handling (mentions, DMs, slash commands)
- Thread context fetching for conversational continuity
- Markdown-to-Slack formatting (converts `**bold**` to `*bold*`, removes fake tool call JSON)
- Interactive buttons for confirmations

**`internal/memory/`** - Dual memory system:
- **SQLite**: Short-term conversational memory (sessions, observations)
- **Qdrant**: Long-term vector memory with semantic search
- **Embedder**: Supports Ollama, OpenAI, and llama.cpp for embeddings
- Stores observations: chat messages, tool executions, incidents

**`internal/tools/`** - Tool registry:
- Central registry for all tools (from MCP servers + fallback K8s tools)
- Permission checks for destructive operations
- Converts MCP tool schemas to LLM function definitions

**`internal/llm/`** - LLM clients:
- **`openai.go`**: OpenAI-compatible client (used for vLLM, llama.cpp, etc.)
- **`ollama.go`**: Native Ollama client
- Tool calling support via function definitions

**`internal/config/`** - Configuration management:
- Loads from environment variables and `mcp.json`
- Merges MCP servers from both sources
- Access control (allowed users/channels for destructive operations)

### Agent Behavior

The agent uses a **two-mode system** defined in its system prompt:

**Category A (Casual)**: Friendly, conversational responses without tools
- Greetings, small talk, general questions
- Responds in the same language as user (Korean/English)
- No tool calls, no SRE jargon

**Category B (SRE/Infrastructure)**: Technical responses with tool usage
- Kubernetes, AWS, GitHub, Argo queries
- Immediately calls tools (no "I'll check..." messages)
- Uses Slack markdown formatting

**Memory-aware behavior** (`MemoryAgent`):
- Searches long-term memory for relevant past context
- Maintains session history (last 10 messages)
- Records all tool executions for future learning
- Selective tool schema injection to reduce context size and prevent hallucinations

### MCP Integration

**Tool Discovery Flow**:
1. Eve connects to each MCP server URL (from `MCP_SERVERS` env or `mcp.json`)
2. Sends `initialize` handshake with protocol version
3. Sends `notifications/initialized` notification
4. Calls `tools/list` to fetch available tools
5. Registers tools in the central registry with closures that proxy back to MCP server

**Tool Execution Flow**:
1. LLM requests tool call during agentic loop
2. Registry validates permissions (destructive operations)
3. MCP client sends `tools/call` JSON-RPC request
4. MCP server executes and returns result
5. Result fed back into LLM conversation

### Configuration Files

**`.env`** - Primary configuration source:
- Slack tokens (`SLACK_APP_TOKEN`, `SLACK_BOT_TOKEN`)
- LLM settings (`LLM_PROVIDER`, `LLM_BASE_URL`, `LLM_MODEL`)
- MCP servers (comma-separated URLs)
- Access control, memory settings

**`mcp.json`** - Alternative/supplementary MCP configuration:
- Supports `url` field for each server
- Can include `env` variables (expanded with `$HOME` etc.)
- Merged with `MCP_SERVERS` environment variable

## Code Patterns

### Adding a New Tool Category

Tools are discovered from MCP servers, not hardcoded. To add capabilities:
1. Deploy a new MCP server (K8s sidecar or external service)
2. Add its URL to `MCP_SERVERS` or `mcp.json`
3. Restart Eve - tools auto-register

### Memory Observation Types

When storing observations, use these types:
- `ObservationTypeChatMessage`: User/assistant messages
- `ObservationTypeToolExecution`: Tool call results
- `ObservationTypeIncident`: SRE incidents with metadata

### Permission Checking

Destructive tools require:
1. Tool marked with `IsDestructive: true`
2. User ID in `ALLOWED_USER_IDS`
3. Channel ID in `ALLOWED_CHANNEL_IDS`
4. Both checks pass in `agent.executeTool()`

### LLM Tool Schema

MCP tool schemas are converted to LLM function definitions in `llm.ConvertToolsToDefinitions()`. The original schema is stored in `RawInputSchema` and passed to the LLM unchanged.

## Common Development Tasks

### Running with a new MCP server
1. Start your MCP server on a local port or deploy as sidecar
2. Add URL to `.env`: `MCP_SERVERS=http://localhost:8080,http://localhost:8081`
3. Restart Eve - check logs for successful tool discovery

### Testing Memory Integration
```bash
# Deploy Qdrant locally
just deploy-mem

# Enable memory in .env
MEMORY_ENABLED=true
MEMORY_QDRANT_ADDR=localhost:6334

# Run and verify memory storage in logs
just run
```

### Debugging Tool Calls
- LLM sometimes outputs tool calls as text instead of actual function calls
- `MemoryAgent.runAgentLoop()` detects this and prompts LLM to retry
- Check `formatForSlack()` which strips fake JSON tool outputs

### Slack Thread Handling
- Threads are identified by `ThreadTS` (thread timestamp)
- Session ID format: `{channelID}:{threadTS}` for threaded conversations
- Thread context fetched via `GetConversationReplies` (up to 20 messages)

## Testing

The codebase uses Go's standard testing framework:
```bash
# Run all tests
go test -v ./...

# Run specific package tests
go test -v ./internal/memory

# Run specific test
go test -v ./internal/memory -run TestStoreName
```

Currently, tests exist in:
- `internal/memory/store_test.go`

## Recommended LLM

Eve is optimized for models with strong **function calling** capabilities:
- **Primary**: `qwen3-coder` (excellent at tool selection and SRE tasks)
- **Alternative**: `qwen2.5:14b`, `llama3.1:8b`

Configure via `LLM_MODEL` environment variable.

## Deployment

**Sidecar Pattern** (Recommended):
```yaml
containers:
  - name: eve
    image: harbor.home.lab/restack/eve:latest
    env:
      - name: MCP_SERVERS
        value: "http://localhost:8080"
  - name: mcp-kubernetes
    image: quay.io/podman/kubernetes-mcp-server:latest
    args: ["--port=8080"]
```

**CI/CD**: `.github/workflows/build-image.yml` builds and pushes to Harbor registry.

## Special Considerations

### CGO and SQLite
- SQLite requires CGO, but Dockerfile uses `CGO_ENABLED=0`
- This may cause issues if memory is enabled
- Consider using `CGO_ENABLED=1` for builds with memory support

### Slack Formatting
- Use Slack markdown (`*bold*`, not `**bold**`)
- No markdown headers (`#`) - use `*Header*` instead
- Code blocks: triple backticks without language specifier

### Language Support
- Default language: Korean (한국어)
- Agent matches user's language automatically
- System prompt contains bilingual instructions

### Security
- Zero-ingress design (Slack Socket Mode = outbound-only)
- Destructive operations gated by user/channel allowlists
- No secrets in code (use environment variables)
