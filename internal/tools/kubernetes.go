// Package tools provides tool definitions and handlers.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RegisterKubernetesTools registers fallback kubernetes tools that use kubectl directly.
// These are used when MCP connection fails.
func RegisterKubernetesTools(registry *Registry) {
	// kubernetes.get_pods - List pods in a namespace
	registry.Register(&Tool{
		Name:        "kubernetes.get_pods",
		Description: "List pods in a Kubernetes namespace. Shows pod name, status, restarts, and age.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"namespace": {
					Type:        "string",
					Description: "Kubernetes namespace to list pods from. Use 'all' for all namespaces.",
				},
				"label_selector": {
					Type:        "string",
					Description: "Optional label selector to filter pods (e.g., 'app=qdrant').",
				},
			},
			Required: []string{"namespace"},
		},
		Handler: handleGetPods,
	})

	// kubernetes.get_pod_logs - Get logs from a pod
	registry.Register(&Tool{
		Name:        "kubernetes.get_pod_logs",
		Description: "Get logs from a Kubernetes pod. Returns the most recent log entries.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"namespace": {
					Type:        "string",
					Description: "Kubernetes namespace where the pod is running.",
				},
				"pod_name": {
					Type:        "string",
					Description: "Name of the pod to get logs from.",
				},
				"container": {
					Type:        "string",
					Description: "Optional container name if pod has multiple containers.",
				},
				"tail_lines": {
					Type:        "integer",
					Description: "Number of recent log lines to return (default: 100).",
				},
				"previous": {
					Type:        "boolean",
					Description: "If true, get logs from the previous container instance.",
				},
			},
			Required: []string{"namespace", "pod_name"},
		},
		Handler: handleGetPodLogs,
	})

	// kubernetes.get_events - Get events from a namespace
	registry.Register(&Tool{
		Name:        "kubernetes.get_events",
		Description: "Get Kubernetes events from a namespace. Shows warnings and errors.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"namespace": {
					Type:        "string",
					Description: "Kubernetes namespace to get events from.",
				},
				"field_selector": {
					Type:        "string",
					Description: "Optional field selector (e.g., 'involvedObject.name=qdrant-0').",
				},
			},
			Required: []string{"namespace"},
		},
		Handler: handleGetEvents,
	})

	// kubernetes.describe_pod - Describe a pod
	registry.Register(&Tool{
		Name:        "kubernetes.describe_pod",
		Description: "Get detailed information about a Kubernetes pod including events and conditions.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"namespace": {
					Type:        "string",
					Description: "Kubernetes namespace where the pod is running.",
				},
				"pod_name": {
					Type:        "string",
					Description: "Name of the pod to describe.",
				},
			},
			Required: []string{"namespace", "pod_name"},
		},
		Handler: handleDescribePod,
	})

	// kubernetes.get_deployments - List deployments
	registry.Register(&Tool{
		Name:        "kubernetes.get_deployments",
		Description: "List deployments in a Kubernetes namespace with their status.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"namespace": {
					Type:        "string",
					Description: "Kubernetes namespace to list deployments from. Use 'all' for all namespaces.",
				},
			},
			Required: []string{"namespace"},
		},
		Handler: handleGetDeployments,
	})
}

func handleGetPods(ctx context.Context, input json.RawMessage) (*Result, error) {
	var params struct {
		Namespace     string `json:"namespace"`
		LabelSelector string `json:"label_selector"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return NewErrorResult("Invalid input: " + err.Error()), nil
	}

	args := []string{"get", "pods", "-o", "wide"}
	if params.Namespace == "all" {
		args = append(args, "--all-namespaces")
	} else {
		args = append(args, "-n", params.Namespace)
	}
	if params.LabelSelector != "" {
		args = append(args, "-l", params.LabelSelector)
	}

	output, err := runKubectl(ctx, args...)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("kubectl error: %v\n%s", err, output)), nil
	}

	return NewSuccessResult(output), nil
}

func handleGetPodLogs(ctx context.Context, input json.RawMessage) (*Result, error) {
	var params struct {
		Namespace string `json:"namespace"`
		PodName   string `json:"pod_name"`
		Container string `json:"container"`
		TailLines int    `json:"tail_lines"`
		Previous  bool   `json:"previous"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return NewErrorResult("Invalid input: " + err.Error()), nil
	}

	if params.TailLines == 0 {
		params.TailLines = 100
	}

	args := []string{"logs", "-n", params.Namespace, params.PodName, "--tail", fmt.Sprintf("%d", params.TailLines)}
	if params.Container != "" {
		args = append(args, "-c", params.Container)
	}
	if params.Previous {
		args = append(args, "--previous")
	}

	output, err := runKubectl(ctx, args...)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("kubectl error: %v\n%s", err, output)), nil
	}

	return NewSuccessResult(output), nil
}

func handleGetEvents(ctx context.Context, input json.RawMessage) (*Result, error) {
	var params struct {
		Namespace     string `json:"namespace"`
		FieldSelector string `json:"field_selector"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return NewErrorResult("Invalid input: " + err.Error()), nil
	}

	args := []string{"get", "events", "-n", params.Namespace, "--sort-by=.lastTimestamp"}
	if params.FieldSelector != "" {
		args = append(args, "--field-selector", params.FieldSelector)
	}

	output, err := runKubectl(ctx, args...)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("kubectl error: %v\n%s", err, output)), nil
	}

	return NewSuccessResult(output), nil
}

func handleDescribePod(ctx context.Context, input json.RawMessage) (*Result, error) {
	var params struct {
		Namespace string `json:"namespace"`
		PodName   string `json:"pod_name"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return NewErrorResult("Invalid input: " + err.Error()), nil
	}

	args := []string{"describe", "pod", "-n", params.Namespace, params.PodName}
	output, err := runKubectl(ctx, args...)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("kubectl error: %v\n%s", err, output)), nil
	}

	return NewSuccessResult(output), nil
}

func handleGetDeployments(ctx context.Context, input json.RawMessage) (*Result, error) {
	var params struct {
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return NewErrorResult("Invalid input: " + err.Error()), nil
	}

	args := []string{"get", "deployments", "-o", "wide"}
	if params.Namespace == "all" {
		args = append(args, "--all-namespaces")
	} else {
		args = append(args, "-n", params.Namespace)
	}

	output, err := runKubectl(ctx, args...)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("kubectl error: %v\n%s", err, output)), nil
	}

	return NewSuccessResult(output), nil
}

func runKubectl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", args...)

	// Auto-detect config.home if regular config is missing
	homeDir, _ := os.UserHomeDir()
	configHome := homeDir + "/.kube/config.home"
	if _, err := os.Stat(configHome); err == nil {
		cmd.Env = append(os.Environ(), "KUBECONFIG="+configHome)
	}

	output, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(output))
	return result, err
}
