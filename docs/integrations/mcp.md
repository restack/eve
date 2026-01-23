# MCP Integration Guide for Eve

This document outlines the Model Context Protocol (MCP) servers integrated with Eve to provide a full-spectrum SRE capability, covering Kubernetes, GitHub, and AWS infrastructure.

---

## 1. Overview

Eve acts as an MCP client that orchestrates multiple MCP servers. By connecting to these servers, Eve gains specific "tools" to observe, diagnose, and remediate issues across different platforms.

---

## 2. Recommended MCP Servers

### 2.1 Core Infrastructure (Kubernetes)
- **Repo**: [kubernetes-mcp-server](https://github.com/containers/kubernetes-mcp-server)
- **Role**: Essential cluster visibility and control.
- **Key Tools**: `list_pods`, `get_logs`, `describe_resource`, `scale_deployment`.
- **Scenario**: "Why is the api-gateway pod pending?"

### 2.2 Incident Management (GitHub)
- **Repo**: [server-github](https://github.com/modelcontextprotocol/servers/tree/main/src/github)
- **Role**: Bridging communication between Slack triage and developer-facing issues.
- **Key Tools**: `create_issue`, `search_code`, `list_pull_requests`.
- **Scenario**: "Create a GitHub issue for this OOMKill incident and link the logs."

### 2.3 AWS Operations (Official AWSLabs)
The following servers from the [awslabs/mcp](https://github.com/awslabs/mcp) repository are highly recommended for AWS-native SRE tasks.

| Server | Primary Role | Key Capabilities |
| :--- | :--- | :--- |
| **`billing-cost-management`** | Cost Control | Query cost/usage, monitor budgets, detect pricing anomalies. |
| **`aws-network`** | Connectivity Triage | Find IP locations in VPC, trace network paths, analyze ENIs. |
| **`cloudtrail`** | Audit & Change Tracking | Look up API call events to find "Who did what and when." |
| **`cost-explorer`** | Detailed Cost Analysis | Deep dive into cost dimensions and tags for project-based billing. |

---

## 3. Configuration

Eve supports two ways to define MCP server endpoints.

### 3.1 Environment Variable (`.env`)
Best for simple setups or local development.
```bash
MCP_SERVERS=http://k8s-mcp:8080,http://mcp-github:8081,http://aws-billing:8083
```

### 3.2 Configuration File (`mcp.json`)
Best for structured management and production deployments.
```json
{
  "mcpServers": {
    "kubernetes": {
      "url": "http://localhost:8080"
    },
    "github": {
      "url": "http://mcp-github:8081"
    }
  }
}
```

---

## 4. Setup & Authentication Requirements

Each MCP server requires its own set of credentials to interact with the target API.

| Platform | Authentication Requirement |
| :--- | :--- |
| **Kubernetes** | `KUBECONFIG` file or In-cluster ServiceAccount with RBAC. |
| **GitHub** | Personal Access Token (PAT) with `repo` scopes. |
| **AWS** | IAM User/Role with appropriate policies (e.g., `ReadOnlyAccess`, `ce:*`, `cloudtrail:*`). |

### Running Servers via Docker
Most servers are provided as container images. Example for GitHub:
```bash
docker run -e GITHUB_PERSONAL_ACCESS_TOKEN=xxx mcp/github-server
```

---

## 5. SRE Triage Workflow with Integrated MCP

With all three layers (K8s, GitHub, AWS) integrated, Eve can perform advanced triage:

1.  **Detect**: Kubernetes MCP reports a Pod is crashing.
2.  **Analyze**: CloudTrail MCP reveals a recent ConfigMap change; Billing MCP shows a sudden spike in specific resource costs.
3.  **Act**: Eve suggests scaling the deployment via Kubernetes MCP or creating an incident issue via GitHub MCP.
4.  **Record**: All findings are automatically stored in **Eve's Memory System** for future reference.
