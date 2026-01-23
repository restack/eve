# Eve Documentation

Welcome to the documentation for **Eve**, your intelligent SRE Assistant. Use the guides below to set up, integrate, and understand the core systems of Eve.

---

## 🚀 Getting Started
- **[Local Setup Guide](./setup-guide.md)**: How to run Eve locally using `just`, configure environment variables, and start developing.

## 🔌 Integrations
- **[Slack Integration](./integrations/slack.md)**: Setting up the Slack App, Socket Mode, and Bot permissions.
- **[MCP Integration](./integrations/mcp.md)**: Connecting Eve to Kubernetes, AWS, and GitHub via MCP.

## 🧠 Core Architecture
- **[Memory System](./architecture/memory-system.md)**: Understanding how Eve remembers past incidents using Qdrant and SQLite.

---

## 🛠️ Operational Tasks
For manual deployment and infrastructure details:
- **Kubernetes Manifests**: Check the `/manifests` directory for Kustomize bases.
- **Task Automation**: Use the `Justfile` in the root directory for common development tasks.
