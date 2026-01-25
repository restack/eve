package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// Registry maintains a collection of registered tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]*Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool *Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tool.Name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}
	if tool.Handler == nil {
		return fmt.Errorf("tool handler cannot be nil: %s", tool.Name)
	}

	r.tools[tool.Name] = tool
	slog.Debug("registered tool", "name", tool.Name)
	return nil
}

// LoadFromMCP fetches tools from an MCP client and registers them.
func (r *Registry) LoadFromMCP(ctx context.Context, mcpClient interface {
	ListTools(ctx context.Context) ([]*Tool, error)
}) error {
	tools, err := mcpClient.ListTools(ctx)
	if err != nil {
		return err
	}

	for _, t := range tools {
		if err := r.Register(t); err != nil {
			slog.Warn("failed to register MCP tool", "name", t.Name, "error", err)
		}
	}
	return nil
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Try exact match
	if tool, ok := r.tools[name]; ok {
		return tool, ok
	}

	// Try underscore-to-dot match (for LLM compatibility)
	dotName := strings.ReplaceAll(name, "_", ".")
	if tool, ok := r.tools[dotName]; ok {
		return tool, ok
	}

	return nil, false
}

// List returns all registered tool names sorted alphabetically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListByPrefix returns tools matching a prefix (e.g., "kubernetes.").
func (r *Registry) ListByPrefix(prefix string) []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matched []*Tool
	for name, tool := range r.tools {
		if strings.HasPrefix(name, prefix) {
			matched = append(matched, tool)
		}
	}
	return matched
}

// Execute runs a tool by name with the given input.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (*Result, error) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	actualName := tool.Name // Use the registered name

	slog.Info("executing tool",
		"name", actualName,
		"requires_confirmation", tool.RequiresConfirmation,
		"is_destructive", tool.IsDestructive,
	)

	result, err := tool.Handler(ctx, input)
	if err != nil {
		slog.Error("tool execution failed", "name", actualName, "error", err)
		return NewErrorResult(err.Error()), nil
	}

	slog.Info("tool execution completed",
		"name", actualName,
		"success", result.Success,
	)

	return result, nil
}

// GetToolInfo returns a formatted description of a tool.
func (r *Registry) GetToolInfo(name string) string {
	tool, ok := r.Get(name)
	if !ok {
		return fmt.Sprintf("Tool not found: %s", name)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*%s*\n", tool.Name))
	sb.WriteString(fmt.Sprintf("%s\n\n", tool.Description))

	if len(tool.InputSchema.Properties) > 0 {
		sb.WriteString("*Parameters:*\n")
		for name, prop := range tool.InputSchema.Properties {
			required := ""
			for _, req := range tool.InputSchema.Required {
				if req == name {
					required = " (required)"
					break
				}
			}
			sb.WriteString(fmt.Sprintf("• `%s` (%s)%s: %s\n", name, prop.Type, required, prop.Description))
		}
	}

	if tool.RequiresConfirmation {
		sb.WriteString("\n⚠️ This tool requires confirmation before execution.")
	}
	if tool.IsDestructive {
		sb.WriteString("\n🔴 This tool performs destructive operations.")
	}

	return sb.String()
}

// GetAllToolsInfo returns a summary of all registered tools.
func (r *Registry) GetAllToolsInfo() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("*Available Tools*\n\n")

	// Group by prefix
	groups := make(map[string][]*Tool)
	for _, tool := range r.tools {
		parts := strings.SplitN(tool.Name, ".", 2)
		group := parts[0]
		groups[group] = append(groups[group], tool)
	}

	// Sort group names
	groupNames := make([]string, 0, len(groups))
	for name := range groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		tools := groups[groupName]
		sb.WriteString(fmt.Sprintf("*%s*\n", strings.Title(groupName)))

		// Sort tools within group
		sort.Slice(tools, func(i, j int) bool {
			return tools[i].Name < tools[j].Name
		})

		for _, tool := range tools {
			marker := ""
			if tool.IsDestructive {
				marker = " 🔴"
			}
			sb.WriteString(fmt.Sprintf("• `%s`%s - %s\n", tool.Name, marker, tool.Description))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
