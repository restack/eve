// Package llm provides the local LLM client interface for Eve.
package llm

import (
	"context"

	"github.com/restack/eve/internal/tools"
)

// Client interface for LLM providers
type Client interface {
	// Chat sends a conversation and returns the response with optional tool calls
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}

// ChatRequest represents a chat request to the LLM
type ChatRequest struct {
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools"`
	Model    string           `json:"model,omitempty"`
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

// ConvertToolsToDefinitions converts tool registry entries to LLM tool definitions
func ConvertToolsToDefinitions(registry *tools.Registry) []ToolDefinition {
	var defs []ToolDefinition
	for _, name := range registry.List() {
		tool, _ := registry.Get(name)

		// Create a clean, token-efficient parameters map
		properties := make(map[string]interface{})
		for propName, prop := range tool.InputSchema.Properties {
			pType := prop.Type
			if pType == "" {
				pType = "string" // Default to string if type is missing
			}

			p := map[string]interface{}{
				"type": pType,
			}
			if prop.Description != "" {
				p["description"] = prop.Description
			}
			if len(prop.Enum) > 0 {
				p["enum"] = prop.Enum
			}
			properties[propName] = p
		}

		// Ensure required is never nil
		required := tool.InputSchema.Required
		if required == nil {
			required = []string{}
		}

		// Default type to "object"
		schemaType := tool.InputSchema.Type
		if schemaType == "" {
			schemaType = "object"
		}

		defs = append(defs, ToolDefinition{
			Type: "function",
			Function: Function{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters: map[string]interface{}{
					"type":       schemaType,
					"properties": properties,
					"required":   required,
				},
			},
		})
	}

	// Always return at least an empty slice, never nil
	if defs == nil {
		return []ToolDefinition{}
	}
	return defs
}
