# eve

LLM-powered Kubernetes operations agent (Go, Socket Mode).  
Runs inside the cluster. Connects to Slack. Routes requests through a local LLM to MCP-style tools.  
Optional integrations: GitHub Issue creation and Argo Workflows remediation.

<img width="50%" alt="image" src="https://github.com/user-attachments/assets/09b110dc-e775-4462-9e16-83c9d60f56eb" />

---

## Overview

Eve is an agentic Slack bot that:
- Connects via Slack **Socket Mode** (no inbound endpoint required)
- Operates as a Pod within a Kubernetes cluster
- Routes all user messages through a **local LLM** (Ollama, vLLM, etc.)
- LLM decides which **MCP-style tools** to invoke
- Executes tools and returns formatted responses to Slack

**Architecture:**
```
┌─────────────────────────────────────────────────────────────────┐
│                         Kubernetes Cluster                       │
│                                                                  │
│  ┌────────────┐    ┌──────────────┐    ┌────────────────────┐   │
│  │   Slack    │───▶│     Eve      │───▶│    Local LLM       │   │
│  │ Socket Mode│◀───│   (Agent)    │◀───│ (Ollama/vLLM)      │   │
│  └────────────┘    └──────┬───────┘    └────────────────────┘   │
│                           │                                      │
│                           ▼ Tool Calls                          │
│         ┌─────────────────┼─────────────────┐                   │
│         ▼                 ▼                 ▼                   │
│  ┌────────────┐   ┌────────────┐   ┌────────────┐              │
│  │ kubernetes │   │   github   │   │    argo    │              │
│  │   tools    │   │   tools    │   │   tools    │              │
│  └─────┬──────┘   └─────┬──────┘   └─────┬──────┘              │
│        │                │                │                      │
│        ▼                ▼                ▼                      │
│   K8s API Server   GitHub API     Argo Workflows               │
└─────────────────────────────────────────────────────────────────┘
```

---

## Features

- **Natural Language Interface**: Ask questions in plain English
  - "Show me all pods in the production namespace"
  - "What's the status of the nginx deployment?"
  - "Scale the api-server to 5 replicas"

- **MCP-Style Tool Architecture**: Extensible tool interface
  - Kubernetes operations (pods, deployments, nodes, events)
  - GitHub issue creation and comments
  - Argo Workflows execution

- **Local LLM Integration**: No external API dependencies
  - Supports Ollama (recommended)
  - Supports any OpenAI-compatible API (vLLM, LocalAI, text-generation-webui)

- **Access Control**: Restrict destructive operations
  - Allowed user whitelist
  - Allowed channel whitelist

---

## Quick Start

### 1. Prerequisites

- Kubernetes cluster with in-cluster access
- Local LLM server (Ollama recommended)
- Slack App with Socket Mode enabled

### 2. Slack App Setup

Create a Slack App at [api.slack.com/apps](https://api.slack.com/apps):

1. Enable **Socket Mode** and get `xapp-` token
2. Add **Bot Token Scopes**:
   - `app_mentions:read`
   - `chat:write`
   - `commands`
   - `im:history`
   - `im:read`
   - `im:write`
3. Install to workspace and get `xoxb-` token
4. (Optional) Add slash commands: `/k8s`, `/eve`

### 3. Deploy

```bash
# Create namespace
kubectl create namespace eve-system

# Update secrets with your tokens
kubectl apply -f manifests/base/secrets.yaml

# Deploy eve
kubectl apply -k manifests/base/
```

### 4. Configure LLM

Set the LLM endpoint in the ConfigMap:

```yaml
data:
  LLM_PROVIDER: "openai"  # llama.cpp uses OpenAI-compatible API
  LLM_BASE_URL: "http://qwen.home.lab:8003"
  LLM_MODEL: "qwen3-coder"
```

Recommended models with tool calling support:
- `qwen3-coder` (llama.cpp)
- `qwen2.5:14b` (Ollama)
- `llama3.1:8b` (Ollama)
- `mistral:7b` (Ollama)

---

## Configuration

| Variable              | Description                                      | Default                  |
|-----------------------|--------------------------------------------------|--------------------------|
| `SLACK_APP_TOKEN`     | Socket Mode token (xapp-)                        | **required**             |
| `SLACK_BOT_TOKEN`     | Bot token (xoxb-)                                | **required**             |
| `LLM_PROVIDER`        | LLM provider: `ollama` or `openai`               | `ollama`                 |
| `LLM_BASE_URL`        | LLM API base URL                                 | `http://localhost:11434` |
| `LLM_MODEL`           | Model name                                       | `qwen2.5:14b`            |
| `LLM_API_KEY`         | API key (for OpenAI-compatible)                  | -                        |
| `GITHUB_TOKEN`        | GitHub PAT for issue creation                    | -                        |
| `GITHUB_OWNER`        | GitHub org/user                                  | -                        |
| `GITHUB_REPO`         | Repository for issues                            | -                        |
| `ARGO_SERVER_URL`     | Argo Workflows API server                        | -                        |
| `ARGO_AUTH_TOKEN`     | Argo authentication token                        | -                        |
| `DEFAULT_NAMESPACE`   | Default K8s namespace                            | `default`                |
| `ALLOWED_USER_IDS`    | Comma-separated Slack user IDs                   | -                        |
| `ALLOWED_CHANNEL_IDS` | Comma-separated Slack channel IDs                | -                        |

---

## MCP-Style Tool Interface

Tools follow the Model Context Protocol pattern:

```go
type Tool struct {
    Name                 string        // e.g., "kubernetes.list_pods"
    Description          string        // Human-readable description
    InputSchema          InputSchema   // JSON Schema for parameters
    RequiresConfirmation bool          // Prompt before execution
    IsDestructive        bool          // Marks state-changing operations
    Handler              func(ctx, input) (*Result, error)
}
```

### Available Tools

**Kubernetes:**
- `kubernetes.list_pods` - List pods in a namespace
- `kubernetes.get_pod` - Get pod details
- `kubernetes.list_nodes` - List cluster nodes
- `kubernetes.list_deployments` - List deployments
- `kubernetes.get_deployment` - Get deployment details
- `kubernetes.rollout_status` - Check rollout status
- `kubernetes.scale_deployment` 🔴 - Scale a deployment
- `kubernetes.list_events` - List namespace events
- `kubernetes.list_namespaces` - List namespaces

**GitHub:**
- `github.create_issue` - Create a GitHub issue
- `github.comment_issue` - Comment on an issue
- `github.list_issues` - List repository issues

**Argo:**
- `argo.run_workflow` 🔴 - Trigger a workflow
- `argo.get_workflow` - Get workflow status
- `argo.list_workflows` - List workflows

🔴 = Destructive operation (requires authorization)

---

## Adding Custom Tools

1. Create a new package under `internal/tools/`:

```go
package mytool

import (
    "github.com/restack/eve/internal/tools"
)

type Tools struct {
    toolsList []*tools.Tool
}

func NewTools(cfg *config.Config) *Tools {
    t := &Tools{}
    t.toolsList = []*tools.Tool{
        t.myCustomTool(),
    }
    return t
}

func (t *Tools) All() []*tools.Tool { return t.toolsList }

func (t *Tools) myCustomTool() *tools.Tool {
    return &tools.Tool{
        Name:        "mytool.action",
        Description: "Performs a custom action",
        InputSchema: tools.InputSchema{
            Type: "object",
            Properties: map[string]tools.Property{
                "param1": {Type: "string", Description: "..."},
            },
            Required: []string{"param1"},
        },
        Handler: func(ctx context.Context, input json.RawMessage) (*tools.Result, error) {
            // Implementation
            return tools.NewSuccessResult("Done"), nil
        },
    }
}
```

2. Register in `cmd/eve/main.go`:

```go
myTools := mytool.NewTools(cfg)
for _, tool := range myTools.All() {
    registry.Register(tool)
}
```

---

## RBAC

Eve requires scoped permissions:
- **Read**: pods, deployments, nodes, events, namespaces
- **Write** (optional): patch/update deployments
- **Argo** (optional): create Workflow CRDs

See `manifests/base/rbac.yaml` for the complete RBAC configuration.

---

## Development

```bash
# Build
go build -o eve ./cmd/eve

# Run locally (requires kubeconfig and Slack tokens)
export SLACK_APP_TOKEN=xapp-...
export SLACK_BOT_TOKEN=xoxb-...
export LLM_BASE_URL=http://localhost:11434
export LLM_MODEL=qwen2.5:7b
./eve

# Build container
docker build -t eve:latest .
```

---

## Agent Loop

The agent follows a ReAct-style loop:

1. Receive user message from Slack
2. Send to LLM with system prompt and tool definitions
3. If LLM returns tool calls:
   - Execute each tool
   - Append results to conversation
   - Loop back to step 2
4. If LLM returns final response:
   - Send to Slack

Maximum iterations: 10 (prevents infinite loops)

---

## License

MIT
