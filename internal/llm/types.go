// Package llm provides the local LLM client interface for Eve.
package llm

import (
	"context"
	"strings"

	"github.com/restack/eve/internal/tools"
)

// Client interface for LLM providers
type Client interface {
	// Chat sends a conversation and returns the response with optional tool calls
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}

// ChatRequest represents a chat request to the LLM
type ChatRequest struct {
	Messages   []Message        `json:"messages"`
	Tools      []ToolDefinition `json:"tools"`
	Model      string           `json:"model,omitempty"`
	ToolChoice interface{}      `json:"tool_choice,omitempty"` // "auto", "none", "required"
}

// Message represents a chat message
type Message struct {
	Role       string     `json:"role"` // system, user, assistant, tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents a tool invocation requested by the LLM
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolDefinition defines a tool for the LLM
type ToolDefinition struct {
	Type     string   `json:"type"` // "function"
	Function Function `json:"function"`
}

// Function defines a callable function
type Function struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"` // JSON Schema
}

// ChatResponse represents the LLM response
type ChatResponse struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop", "tool_calls"
}

// sanitizeJinja2Patterns escapes Jinja2-like patterns that would cause
// vLLM's internal template engine to fail.
func sanitizeJinja2Patterns(input string) string {
	if input == "" {
		return ""
	}
	// Replace newlines and tabs with spaces to prevent breaking LLM server JSON/Jinja2 templates
	s := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"\t", " ",
		"{{", "{ {",
		"}}", "} }",
		"{%", "{ %",
		"%}", "% }",
		"{#", "{ #",
		"#}", "# }",
	).Replace(input)

	// Trim multiple spaces
	return strings.Join(strings.Fields(s), " ")
}

// createMinimalSchema creates a minimal, vLLM-safe schema from any input.
// This ensures that 'properties' is NEVER null, which is the primary cause of vLLM 500 errors.
func createMinimalSchema(input interface{}) map[string]interface{} {
	// Initialize with guaranteed non-null fields
	result := map[string]interface{}{
		"type":       "object",
		"properties": make(map[string]interface{}),
		"required":   make([]string, 0),
	}

	if input == nil {
		return result
	}

	m, ok := input.(map[string]interface{})
	if !ok {
		return result
	}

	// Extract and rebuild properties
	if props, ok := m["properties"].(map[string]interface{}); ok && props != nil {
		newProps := make(map[string]interface{})
		for propName, propValue := range props {
			// Skip internal/empty property names
			if propName == "" {
				continue
			}

			prop := map[string]interface{}{
				"type": "string",
			}

			if pv, ok := propValue.(map[string]interface{}); ok && pv != nil {
				if t, ok := pv["type"].(string); ok && t != "" {
					prop["type"] = t
				}
				if desc, ok := pv["description"].(string); ok && desc != "" {
					prop["description"] = sanitizeJinja2Patterns(desc)
				}
				// Support enum if present
				if enum, ok := pv["enum"].([]interface{}); ok && len(enum) > 0 {
					prop["enum"] = enum
				}
			}
			newProps[propName] = prop
		}
		result["properties"] = newProps
	}

	// Extract required fields
	if req, ok := m["required"].([]interface{}); ok && req != nil {
		required := make([]string, 0, len(req))
		for _, r := range req {
			if s, ok := r.(string); ok && s != "" {
				required = append(required, s)
			}
		}
		result["required"] = required
	} else if req, ok := m["required"].([]string); ok && req != nil {
		result["required"] = req
	}

	return result
}

// normalizeSchema is a compatibility alias for createMinimalSchema for tests
func normalizeSchema(input interface{}) interface{} {
	return createMinimalSchema(input)
}

// ConvertToolsToDefinitions converts tool registry entries to LLM tool definitions
func ConvertToolsToDefinitions(registry *tools.Registry) []ToolDefinition {
	var defs []ToolDefinition
	for _, name := range registry.List() {
		tool, _ := registry.Get(name)

		var parameters interface{}
		if tool.RawInputSchema != nil {
			parameters = createMinimalSchema(tool.RawInputSchema)
		} else {
			props := make(map[string]interface{})
			for pName, p := range tool.InputSchema.Properties {
				pType := p.Type
				if pType == "" {
					pType = "string"
				}
				props[pName] = map[string]interface{}{
					"type": pType,
				}
			}
			required := tool.InputSchema.Required
			if required == nil {
				required = make([]string, 0)
			}
			parameters = map[string]interface{}{
				"type":       "object",
				"properties": props,
				"required":   required,
			}
		}

		// Absolute fallback to ensure no null parameters
		if parameters == nil {
			parameters = map[string]interface{}{
				"type":       "object",
				"properties": make(map[string]interface{}),
				"required":   make([]string, 0),
			}
		}

		defs = append(defs, ToolDefinition{
			Type: "function",
			Function: Function{
				Name:        tool.Name,
				Description: sanitizeJinja2Patterns(tool.Description),
				Parameters:  parameters,
			},
		})
	}

	if defs == nil {
		return []ToolDefinition{}
	}
	return defs
}
