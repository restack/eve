# Local Setup Guide

This guide will help you get Eve up and running on your local machine for development and testing.

---

## 1. Prerequisites
- **Go**: v1.23 or later
- **Just**: A command runner (optional but recommended)
- **Ollama** or **OpenAI API Key**: For LLM and Embedding capabilities.
- **Docker**: For running infrastructure like Qdrant (optional).

---

## 2. Environment Configuration

Eve relies on environment variables for configuration.

1.  Create a `.env` file from the example:
    ```bash
    cp .env.example .env
    ```
2.  Fill in the required tokens:
    - `SLACK_APP_TOKEN`: Your Slack App-level token (starts with `xapp-`).
    - `SLACK_BOT_TOKEN`: Your Slack Bot-user OAuth token (starts with `xoxb-`).
    - `LLM_API_KEY`: If using OpenAI.

Refer to the **[Slack Integration Guide](./integrations/slack.md)** for detailed token generation steps.

---

## 3. Running Eve

We use a `Justfile` to automate common tasks.

### Basic Commands
- **Build and Run**: `just run` (Automatically loads `.env`)
- **Run Tests**: `just test`
- **Tidy Dependencies**: `just tidy`

### Full Infrastructure Setup
If you want to initialize the full environment including the memory system:
```bash
just setup
```
*Note: This might require `kubectl` and a working Kubernetes context to deploy the memory infrastructure.*

---

## 4. MCP Servers
Eve needs at least one MCP server to be useful. Ensure your `MCP_SERVERS` or `mcp.json` is configured correctly.

See **[MCP Integration Guide](./integrations/mcp.md)** for more details on available servers and how to run them.

---

## 5. Directory Structure
- `/cmd/eve`: The main entry point.
- `/internal/agent`: Agentic loop and prompts.
- `/internal/memory`: Persistent memory storage logic.
- `/internal/tools`: Registry and tool execution.
- `/manifests`: Kubernetes deployment files.
