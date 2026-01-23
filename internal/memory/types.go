// Package memory provides persistent memory storage for Eve's observations and context.
package memory

import (
	"time"
)

// ObservationType defines the type of observation
type ObservationType string

const (
	// ObservationTypeToolExecution represents a tool execution record
	ObservationTypeToolExecution ObservationType = "tool_execution"
	// ObservationTypeIncident represents an incident record
	ObservationTypeIncident ObservationType = "incident"
	// ObservationTypeUserFeedback represents user feedback
	ObservationTypeUserFeedback ObservationType = "user_feedback"
	// ObservationTypeResolution represents a resolution record
	ObservationTypeResolution ObservationType = "resolution"
	// ObservationTypeConfig represents a configuration change
	ObservationTypeConfig ObservationType = "config_change"
	// ObservationTypeAlert represents an alert
	ObservationTypeAlert ObservationType = "alert"
	// ObservationTypeChatMessage represents a chat message (user or assistant)
	ObservationTypeChatMessage ObservationType = "chat_message"
)

// Observation represents a stored observation data unit
type Observation struct {
	// Basic identifiers
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Classification
	Type     ObservationType `json:"type"`
	Category string          `json:"category,omitempty"` // kubernetes, github, argo, etc.

	// Context
	SessionID string `json:"session_id"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	ThreadTS  string `json:"thread_ts,omitempty"`

	// Content
	Title   string `json:"title"`
	Content string `json:"content"`
	Summary string `json:"summary,omitempty"`

	// Metadata
	Metadata ObservationMetadata `json:"metadata"`

	// For search
	Technologies []string `json:"technologies"`
	Keywords     []string `json:"keywords"`

	// Vector (generated during storage)
	Vector []float32 `json:"-"`
	Score  float64   `json:"score,omitempty"` // Only included in search results
}

// ObservationMetadata contains type-specific additional information
type ObservationMetadata struct {
	// Tool Execution
	ToolName   string `json:"tool_name,omitempty"`
	ToolInput  string `json:"tool_input,omitempty"`
	ToolOutput string `json:"tool_output,omitempty"`
	Success    bool   `json:"success,omitempty"`
	Duration   int64  `json:"duration_ms,omitempty"`

	// Incident
	Severity     string `json:"severity,omitempty"` // critical, warning, info
	Namespace    string `json:"namespace,omitempty"`
	Resource     string `json:"resource,omitempty"`
	ResourceKind string `json:"resource_kind,omitempty"`
	Resolution   string `json:"resolution,omitempty"`
	MTTR         int64  `json:"mttr_minutes,omitempty"` // Mean Time To Resolve

	// Alert
	AlertName  string `json:"alert_name,omitempty"`
	AlertState string `json:"alert_state,omitempty"` // firing, resolved

	// Config Change
	ConfigKey string `json:"config_key,omitempty"`
	OldValue  string `json:"old_value,omitempty"`
	NewValue  string `json:"new_value,omitempty"`

	// Chat Message
	Role string `json:"role,omitempty"` // user, assistant
}

// Session represents a conversation session
type Session struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`

	// Context
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	ThreadTS  string `json:"thread_ts,omitempty"`

	// Summary
	Summary      string   `json:"summary,omitempty"`
	Topics       []string `json:"topics,omitempty"`
	Technologies []string `json:"technologies,omitempty"`

	// Statistics
	MessageCount     int `json:"message_count"`
	ToolCallCount    int `json:"tool_call_count"`
	ObservationCount int `json:"observation_count"`
}

// SearchResult wraps search results
type SearchResult struct {
	Observations []Observation `json:"observations"`
	TotalCount   int           `json:"total_count"`
	SearchTime   time.Duration `json:"search_time"`
	Query        string        `json:"query"`
}

// SearchOptions defines search options
type SearchOptions struct {
	Limit          int               `json:"limit"`
	MinScore       float64           `json:"min_score"`
	Types          []ObservationType `json:"types,omitempty"`
	Categories     []string          `json:"categories,omitempty"`
	ChannelID      string            `json:"channel_id,omitempty"`
	UserID         string            `json:"user_id,omitempty"`
	Technologies   []string          `json:"technologies,omitempty"`
	TimeRangeStart time.Time         `json:"time_range_start,omitempty"`
	TimeRangeEnd   time.Time         `json:"time_range_end,omitempty"`
	IncludeContent bool              `json:"include_content"`
}

// Stats contains statistics information
type Stats struct {
	TotalObservations  int64            `json:"total_observations"`
	TotalSessions      int64            `json:"total_sessions"`
	ObservationsByType map[string]int64 `json:"observations_by_type"`
	TopTechnologies    []TechCount      `json:"top_technologies"`
	AvgMTTR            float64          `json:"avg_mttr_minutes"`
	RecentIncidents    int              `json:"recent_incidents"`
}

// TechCount represents technology usage count
type TechCount struct {
	Technology string `json:"technology"`
	Count      int64  `json:"count"`
}
