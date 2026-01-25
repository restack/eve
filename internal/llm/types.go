// Package llm provides the local LLM client interface for Eve.
package llm

import (
	"context"
	"encoding/json"
	"log/slog"
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

	// Sampling parameters
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`
	MaxTokens        *int     `json:"max_tokens,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	Seed             *int     `json:"seed,omitempty"`
	MinP             *float64 `json:"min_p,omitempty"`
	TypicalP         *float64 `json:"typical_p,omitempty"`

	// Generic extra body for provider-specific parameters
	ExtraBody map[string]interface{} `json:"extra_body,omitempty"`
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
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"` // JSON Schema
}

// ChatResponse represents the LLM response
type ChatResponse struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop", "tool_calls"
}

// sanitizeJinja2Patterns escapes Jinja2-like patterns and cleans text for LLM templates.
func sanitizeJinja2Patterns(input string) string {
	if input == "" {
		return "parameter"
	}
	s := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"\t", " ",
		"{{", "(",
		"}}", ")",
		"{%", "[",
		"%}", "]",
		"{#", "<",
		"#}", ">",
	).Replace(input)

	result := strings.Join(strings.Fields(s), " ")
	if result == "" {
		return "parameter"
	}
	return result
}

// createMinimalSchema creates the MOST MINIMAL schema to avoid llama.cpp Jinja2 crashes.
// It ONLY includes 'type' and 'description' for each property - nothing else.
func createMinimalSchema(input interface{}) map[string]interface{} {
	// Absolutely minimal result structure - ALWAYS return a valid schema
	// CRITICAL: properties must NEVER be null, empty map is OK
	defaultProps := map[string]interface{}{
		"_placeholder": map[string]interface{}{
			"type":        "string",
			"description": "placeholder parameter",
		},
	}

	result := map[string]interface{}{
		"type":       "object",
		"properties": defaultProps,
		"required":   []string{},
	}

	if input == nil {
		return result
	}

	m, ok := input.(map[string]interface{})
	if !ok {
		return result
	}

	// Process properties - ONLY type and description per property
	rawProps := m["properties"]
	if rawProps == nil {
		// If properties is null, return with placeholder
		return result
	}

	propsMap, ok := rawProps.(map[string]interface{})
	if !ok || propsMap == nil || len(propsMap) == 0 {
		// If properties is not a map or empty, return with placeholder
		return result
	}

	newProps := make(map[string]interface{})

	for propName, pValue := range propsMap {
		if propName == "" {
			continue
		}

		// CRITICAL: Every property MUST be a non-null map with exactly 2 keys
		// This is what llama.cpp expects and anything else causes .keys() to fail
		entry := map[string]interface{}{
			"type":        "string",
			"description": "parameter",
		}

		// Try to extract type and description if available
		if pValue != nil {
			if pvMap, isMap := pValue.(map[string]interface{}); isMap && pvMap != nil {
				// Extract type - flatten complex types (array, object) to string for llama.cpp
				if t, ok := pvMap["type"].(string); ok && t != "" {
					// Flatten array and object types to string for safety
					if t == "array" || t == "object" {
						entry["type"] = "string"
					} else {
						entry["type"] = t
					}
				}
				// Extract description
				if desc, ok := pvMap["description"].(string); ok && desc != "" {
					entry["description"] = sanitizeJinja2Patterns(desc)
				}
			}
		}

		// ABSOLUTELY ensure both keys exist and are non-null strings
		if entry["type"] == nil || entry["type"] == "" {
			entry["type"] = "string"
		}
		if entry["description"] == nil || entry["description"] == "" {
			entry["description"] = "parameter"
		}

		newProps[propName] = entry
	}

	// CRITICAL: properties must NEVER be empty - Jinja2 template iterates over it
	if len(newProps) == 0 {
		newProps["_placeholder"] = map[string]interface{}{
			"type":        "string",
			"description": "placeholder parameter",
		}
	}

	result["properties"] = newProps

	// Handle required array
	if req, ok := m["required"].([]interface{}); ok && len(req) > 0 {
		var reqs []string
		for _, v := range req {
			if s, ok := v.(string); ok && s != "" {
				reqs = append(reqs, s)
			}
		}
		if len(reqs) > 0 {
			result["required"] = reqs
		}
	} else if req, ok := m["required"].([]string); ok && len(req) > 0 {
		result["required"] = req
	}

	return result
}

// normalizeSchema is a compatibility alias for tests.
func normalizeSchema(input interface{}) interface{} {
	return createMinimalSchema(input)
}

// ConvertToolsToDefinitions converts tool registry entries to LLM tool definitions
func ConvertToolsToDefinitions(registry *tools.Registry) []ToolDefinition {
	var defs []ToolDefinition
	toolCount := 0

	for _, name := range registry.List() {
		tool, _ := registry.Get(name)

		var parameters map[string]interface{}
		if tool.RawInputSchema != nil {
			parameters = createMinimalSchema(tool.RawInputSchema)
		} else {
			// Build ultra-safe props for internal tools
			props := make(map[string]interface{})
			for pName, p := range tool.InputSchema.Properties {
				props[pName] = map[string]interface{}{
					"type":        "string",
					"description": sanitizeJinja2Patterns(p.Description),
				}
			}
			required := tool.InputSchema.Required
			if required == nil {
				required = []string{}
			}
			parameters = map[string]interface{}{
				"type":       "object",
				"properties": props,
				"required":   required,
			}
		}

		formattedName := strings.ReplaceAll(tool.Name, ".", "_")

		toolDef := ToolDefinition{
			Type: "function",
			Function: Function{
				Name:        formattedName,
				Description: sanitizeJinja2Patterns(tool.Description),
				Parameters:  parameters,
			},
		}

		// Debug log the first tool to verify schema structure
		if toolCount == 0 {
			if jsonData, err := json.MarshalIndent(toolDef, "", "  "); err == nil {
				slog.Debug("first tool schema", "tool", formattedName, "schema", string(jsonData))
			}
		}
		toolCount++

		defs = append(defs, toolDef)
	}

	if defs == nil {
		return []ToolDefinition{}
	}

	slog.Info("converted tools to definitions", "count", len(defs))
	return defs
}
