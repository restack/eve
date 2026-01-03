// Package tools provides the MCP-style tool interface for Eve.
package tools

import (
	"context"
	"encoding/json"
)

// InputSchema defines the JSON Schema for tool input parameters.
type InputSchema struct {
	Type        string                 `json:"type"`
	Description string                 `json:"description,omitempty"`
	Properties  map[string]Property    `json:"properties,omitempty"`
	Required    []string               `json:"required,omitempty"`
	Default     map[string]interface{} `json:"default,omitempty"`
}

// Property defines a single property in the input schema.
type Property struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

// Tool represents an MCP-style tool with metadata and execution handler.
type Tool struct {
	// Name is the unique identifier for the tool (e.g., "kubernetes.list_pods")
	Name string `json:"name"`

	// Description explains what the tool does
	Description string `json:"description"`

	// InputSchema defines the expected input parameters
	InputSchema InputSchema `json:"input_schema"`

	// RequiresConfirmation indicates if the tool needs user confirmation
	RequiresConfirmation bool `json:"requires_confirmation"`

	// IsDestructive marks tools that modify state
	IsDestructive bool `json:"is_destructive"`

	// Handler is the function that executes the tool
	Handler func(ctx context.Context, input json.RawMessage) (*Result, error) `json:"-"`
}

// Result represents the output of a tool execution.
type Result struct {
	// Success indicates if the tool executed successfully
	Success bool `json:"success"`

	// Output is the primary output data
	Output string `json:"output,omitempty"`

	// Data contains structured output data
	Data map[string]interface{} `json:"data,omitempty"`

	// Error contains error information if Success is false
	Error string `json:"error,omitempty"`

	// Actions contains follow-up actions that can be taken
	Actions []Action `json:"actions,omitempty"`
}

// Action represents a follow-up action that can be offered to the user.
type Action struct {
	// ID is a unique identifier for the action
	ID string `json:"id"`

	// Label is the display text for the action
	Label string `json:"label"`

	// Style indicates the button style (primary, danger, default)
	Style string `json:"style"`

	// ToolName is the tool to invoke if this action is selected
	ToolName string `json:"tool_name"`

	// ToolInput is the input to pass to the tool
	ToolInput json.RawMessage `json:"tool_input"`
}

// NewSuccessResult creates a successful result with output text.
func NewSuccessResult(output string) *Result {
	return &Result{
		Success: true,
		Output:  output,
	}
}

// NewSuccessResultWithData creates a successful result with structured data.
func NewSuccessResultWithData(output string, data map[string]interface{}) *Result {
	return &Result{
		Success: true,
		Output:  output,
		Data:    data,
	}
}

// NewErrorResult creates an error result.
func NewErrorResult(err string) *Result {
	return &Result{
		Success: false,
		Error:   err,
	}
}

// WithActions adds follow-up actions to a result.
func (r *Result) WithActions(actions ...Action) *Result {
	r.Actions = actions
	return r
}
