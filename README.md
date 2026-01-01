# eve
Slack-driven Kubernetes operations bot (Go, Socket Mode).  
Runs inside the cluster. Connects outward to Slack. Executes controlled actions against Kubernetes.  
Optional integrations: GitHub Issue creation and Argo Workflows–based remediation.

<img width="50%" alt="image" src="https://github.com/user-attachments/assets/09b110dc-e775-4462-9e16-83c9d60f56eb" />



---

## Overview
eve is a Slack bot designed to:
- Connect via Slack **Socket Mode** (no inbound public endpoint).
- Operate as a Pod within a Kubernetes cluster.
- Monitor cluster state (pods, nodes, deployments, events).
- Perform restricted operational tasks on request.
- Create GitHub Issues for tracked incidents.
- Trigger predefined remediation “recipes” via Argo Workflows.

eve does not aim to replace observability, CI/CD, or paging systems.  
It exists to bridge Slack, Kubernetes, and operational workflows with minimal assumptions.

---

## Features
- `/k8s pods [-n <namespace>]`  
  Returns pod list and basic state information.
- `/k8s rollout-status <deployment>`  
  Summarizes rollout progress.
- `/k8s scale <deployment> --replicas <n>`  
  Confirms before applying changes.
- Slack alert → “Create GitHub Issue” button  
  Generates a ticket with contextual metadata.
- Slack alert → “Run Recipe” button  
  Executes mapped Argo Workflow templates for repetitive issues.

---

## Architecture
- **Go** (`slack-go/slack`, `slack-go/slack/socketmode`, `client-go`)
- **Slack Socket Mode** (outbound-only WebSocket)
- **Kubernetes In-Cluster Config** (`rest.InClusterConfig`)
- **RBAC-scoped operations**
- **Optional** GitHub + Argo API calls

No ingress resource is required. A Service or LoadBalancer is not required.  
Outbound access to Slack and GitHub endpoints is required.

---

## Configuration

| Variable              | Description                                      |
|-----------------------|--------------------------------------------------|
| `SLACK_APP_TOKEN`     | Socket Mode token (xapp-)                        |
| `SLACK_BOT_TOKEN`     | Bot token (xoxb-)                                |
| `GITHUB_TOKEN`        | PAT or App token for issue creation              |
| `GITHUB_OWNER`        | GitHub org/user                                  |
| `GITHUB_REPO`         | Repository for issue creation                    |
| `DEFAULT_NAMESPACE`   | Namespace used when none specified               |
| `ALLOWED_USER_IDS`    | Comma-separated list of permitted Slack users    |
| `ALLOWED_CHANNEL_IDS` | Comma-separated list of permitted Slack channels |
| `ARGO_SERVER_URL`     | Optional; Argo Workflows API server              |
| `ARGO_AUTH_TOKEN`     | Optional; Argo authentication token              |

Tokens and secrets must be mounted from a Kubernetes `Secret`.

---

## RBAC
eve requires scoped permissions:
- read-only: pods, deployments, nodes, events
- write (optional): patch/update deployments
- optional: create Argo Workflow CRDs, or call Argo API

Administrative privileges are not required and not recommended.

---

## Deployment (minimal example)
- `Deployment` using a dedicated `ServiceAccount`
- `Secret` for Slack/GitHub tokens
- `ConfigMap` for namespace defaults and recipe mappings

A Helm chart or Kustomize overlay is recommended for production environments.

---

## Workflow (alert → action)
1. Kubernetes event or repetition of known error is detected.
2. eve posts a Slack message with contextual data.
3. User chooses:
   - **Create GitHub Issue**
   - **Run Recipe**
   - **Dismiss**
4. eve executes the chosen path:
   - Issue created → link returned in Slack thread
   - Workflow triggered → status updates posted

---

## Status
- Early-stage implementation
- Functionality is modular and incremental
- No backward-compatibility guarantees at this stage
- Contributions for handlers, recipes, and RBAC profiles welcome

---

## License
MIT (or any permissive license; TBD)
