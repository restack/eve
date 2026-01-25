// Package slack provides the Slack Socket Mode handler for Eve.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/restack/eve/internal/agent"
	"github.com/restack/eve/internal/config"
	"github.com/restack/eve/internal/tools"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// Handler manages Slack Socket Mode connections.
type Handler struct {
	client   *slack.Client
	socket   *socketmode.Client
	agent    agent.Agent
	registry *tools.Registry
	cfg      *config.Config

	// Event deduplication to prevent processing the same event multiple times
	seenEvents   map[string]time.Time
	seenEventsMu sync.RWMutex
}

// NewHandler creates a new Slack handler.
func NewHandler(cfg *config.Config, ag agent.Agent, registry *tools.Registry) (*Handler, error) {
	client := slack.New(
		cfg.SlackBotToken,
		slack.OptionAppLevelToken(cfg.SlackAppToken),
	)

	socket := socketmode.New(
		client,
		socketmode.OptionDebug(false),
	)

	return &Handler{
		client:     client,
		socket:     socket,
		agent:      ag,
		registry:   registry,
		cfg:        cfg,
		seenEvents: make(map[string]time.Time),
	}, nil
}

// Run starts the Socket Mode event loop.
func (h *Handler) Run(ctx context.Context) error {
	// Start cleanup goroutine for seen events cache
	go h.cleanupSeenEvents(ctx)

	go func() {
		for evt := range h.socket.Events {
			h.handleEvent(ctx, evt)
		}
	}()

	go func() {
		<-ctx.Done()
		slog.Info("shutting down socket mode client")
	}()

	return h.socket.Run()
}

// markEventSeen records an event as processed. Returns false if already seen.
func (h *Handler) markEventSeen(eventKey string) bool {
	h.seenEventsMu.Lock()
	defer h.seenEventsMu.Unlock()

	if _, exists := h.seenEvents[eventKey]; exists {
		return false
	}
	h.seenEvents[eventKey] = time.Now()
	return true
}

// cleanupSeenEvents periodically removes old entries from the seen events cache.
func (h *Handler) cleanupSeenEvents(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.seenEventsMu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for key, ts := range h.seenEvents {
				if ts.Before(cutoff) {
					delete(h.seenEvents, key)
				}
			}
			h.seenEventsMu.Unlock()
		}
	}
}

func (h *Handler) handleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeSlashCommand:
		cmd, ok := evt.Data.(slack.SlashCommand)
		if !ok {
			return
		}
		h.socket.Ack(*evt.Request)
		go h.handleSlashCommand(ctx, cmd)

	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		h.socket.Ack(*evt.Request)
		go h.handleEventsAPI(ctx, eventsAPIEvent)

	case socketmode.EventTypeInteractive:
		callback, ok := evt.Data.(slack.InteractionCallback)
		if !ok {
			return
		}
		h.socket.Ack(*evt.Request)
		go h.handleInteraction(ctx, callback)
	}
}

func (h *Handler) handleSlashCommand(ctx context.Context, cmd slack.SlashCommand) {
	// Deduplicate: use trigger_id as unique key for slash commands
	eventKey := fmt.Sprintf("slash:%s", cmd.TriggerID)
	if !h.markEventSeen(eventKey) {
		slog.Debug("duplicate slash command ignored", "command", cmd.Command, "trigger", cmd.TriggerID)
		return
	}

	slog.Info("received slash command",
		"command", cmd.Command,
		"text", cmd.Text,
		"user", cmd.UserID,
		"channel", cmd.ChannelID,
	)

	// Send typing indicator
	h.sendMessage(cmd.ChannelID, "🤔 Thinking...")

	// Create agent request from slash command
	message := cmd.Text
	mode := "auto"

	switch cmd.Command {
	case "/k8s", "/sre":
		mode = "sre"
		if !strings.HasPrefix(strings.ToLower(message), "k8s") {
			message = "kubernetes " + message
		}
	case "/chat", "/ask":
		mode = "chat"
	}

	req := &agent.Request{
		UserID:    cmd.UserID,
		ChannelID: cmd.ChannelID,
		Message:   message,
		Mode:      mode,
	}

	// Process through agent
	resp, err := h.agent.Process(ctx, req)
	if err != nil {
		slog.Error("agent error", "error", err)
		h.sendErrorBlock(cmd.ChannelID, "", err.Error())
		return
	}

	h.sendMessage(cmd.ChannelID, resp.Text)
}

func (h *Handler) handleEventsAPI(ctx context.Context, event slackevents.EventsAPIEvent) {
	switch event.Type {
	case slackevents.CallbackEvent:
		innerEvent := event.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			h.handleMention(ctx, ev)
		case *slackevents.MessageEvent:
			// Handle DMs
			if ev.ChannelType == "im" {
				h.handleDirectMessage(ctx, ev)
			}
		}
	}
}

func (h *Handler) handleMention(ctx context.Context, event *slackevents.AppMentionEvent) {
	// Deduplicate: use channel + timestamp as unique key
	eventKey := fmt.Sprintf("mention:%s:%s", event.Channel, event.TimeStamp)
	if !h.markEventSeen(eventKey) {
		slog.Debug("duplicate mention event ignored", "channel", event.Channel, "ts", event.TimeStamp)
		return
	}

	slog.Info("received mention",
		"text", event.Text,
		"user", event.User,
		"channel", event.Channel,
		"thread_ts", event.ThreadTimeStamp,
		"event_ts", event.TimeStamp,
	)

	// Remove the mention from the text
	text := strings.TrimSpace(event.Text)
	parts := strings.SplitN(text, " ", 2)
	message := ""
	if len(parts) > 1 {
		message = strings.TrimSpace(parts[1])
	}

	if message == "" {
		h.sendMessage(event.Channel, "How can I help? Just ask me about your Kubernetes cluster.")
		return
	}

	// Determine reply thread: use existing thread or start a new one on the mention message
	replyThread := event.ThreadTimeStamp
	if replyThread == "" {
		replyThread = event.TimeStamp
	}

	// Add eyes reaction to indicate processing
	h.client.AddReaction("eyes", slack.NewRefToMessage(event.Channel, event.TimeStamp))

	// Fetch thread context ONLY if this is a reply in an existing thread
	var threadContext []string
	if event.ThreadTimeStamp != "" {
		slog.Info("fetching thread context", "thread_ts", event.ThreadTimeStamp)
		// Get previous messages in this thread
		params := &slack.GetConversationRepliesParameters{
			ChannelID: event.Channel,
			Timestamp: event.ThreadTimeStamp,
			Limit:     20, // Fetch up to 20 messages for deep context within a thread
		}
		msgs, _, _, err := h.client.GetConversationReplies(params)
		if err != nil {
			slog.Warn("failed to fetch thread replies", "error", err)
		} else {
			slog.Info("fetched thread messages", "count", len(msgs))
			for _, m := range msgs {
				// Skip the current message
				if m.Timestamp == event.TimeStamp {
					continue
				}
				// Format: "message"
				threadContext = append(threadContext, m.Text)
				slog.Debug("thread message", "ts", m.Timestamp, "text_preview", truncate(m.Text, 100))
			}
			slog.Info("thread context prepared", "context_count", len(threadContext))
		}
	} else {
		slog.Info("no thread_ts - this is a new thread or top-level mention", "event_ts", event.TimeStamp)
	}

	req := &agent.Request{
		UserID:        event.User,
		ChannelID:     event.Channel,
		Message:       message,
		ThreadTS:      replyThread,
		ThreadContext: threadContext,
	}

	resp, err := h.agent.Process(ctx, req)
	if err != nil {
		slog.Error("agent error", "error", err)
		h.sendErrorBlock(event.Channel, replyThread, err.Error())
		return
	}

	// Prepare blocks for the response
	var blocks []slack.Block

	// 1. Add tool call info if available
	if len(resp.ToolCalls) > 0 {
		for _, tc := range resp.ToolCalls {
			sourceInfo := ""
			if tc.ToolSource != "" {
				sourceInfo = fmt.Sprintf(" with *%s*", tc.ToolSource)
			}

			statusEmoji := "✅"
			if !tc.Success {
				statusEmoji = "❌"
			}

			// Use context block for the "Called MCP tool" line
			blocks = append(blocks, slack.NewContextBlock(
				"",
				slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("> Called MCP tool `%s`%s  %s", tc.ToolName, sourceInfo, statusEmoji), false, false),
			))
		}
	}

	// 2. Add the main response text
	if resp.Text != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", formatForSlack(resp.Text), false, false),
			nil, nil,
		))
	}

	// 3. Fallback to raw text if no blocks (shouldn't happen with valid response)
	if len(blocks) == 0 {
		h.sendThreadedMessage(event.Channel, replyThread, "I'm sorry, I couldn't generate a response.")
		return
	}

	// Send as blocks
	options := []slack.MsgOption{slack.MsgOptionBlocks(blocks...)}
	if replyThread != "" {
		options = append(options, slack.MsgOptionTS(replyThread))
	}

	_, _, err = h.client.PostMessage(event.Channel, options...)
	if err != nil {
		slog.Error("failed to send block message", "error", err)
		// Fallback to text if blocks fail
		h.sendThreadedMessage(event.Channel, replyThread, resp.Text)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (h *Handler) handleDirectMessage(ctx context.Context, event *slackevents.MessageEvent) {
	// Ignore bot's own messages
	if event.BotID != "" {
		return
	}

	// Deduplicate: use channel + timestamp as unique key
	eventKey := fmt.Sprintf("dm:%s:%s", event.Channel, event.TimeStamp)
	if !h.markEventSeen(eventKey) {
		slog.Debug("duplicate DM event ignored", "channel", event.Channel, "ts", event.TimeStamp)
		return
	}

	slog.Info("received DM",
		"text", event.Text,
		"user", event.User,
		"channel", event.Channel,
	)

	// Add eyes reaction
	h.client.AddReaction("eyes", slack.NewRefToMessage(event.Channel, event.TimeStamp))

	req := &agent.Request{
		UserID:    event.User,
		ChannelID: event.Channel,
		Message:   event.Text,
		ThreadTS:  event.ThreadTimeStamp,
	}

	resp, err := h.agent.Process(ctx, req)
	if err != nil {
		slog.Error("agent error", "error", err)
		h.sendErrorBlock(event.Channel, event.ThreadTimeStamp, err.Error())
		return
	}

	// Prepare blocks for the response
	var blocks []slack.Block

	// 1. Add tool call info
	if len(resp.ToolCalls) > 0 {
		for _, tc := range resp.ToolCalls {
			sourceInfo := ""
			if tc.ToolSource != "" {
				sourceInfo = fmt.Sprintf(" with *%s*", tc.ToolSource)
			}

			statusEmoji := "✅"
			if !tc.Success {
				statusEmoji = "❌"
			}

			blocks = append(blocks, slack.NewContextBlock(
				"",
				slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("> Called MCP tool `%s`%s  %s", tc.ToolName, sourceInfo, statusEmoji), false, false),
			))
		}
	}

	// 2. Add main text
	if resp.Text != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", formatForSlack(resp.Text), false, false),
			nil, nil,
		))
	}

	if len(blocks) == 0 {
		h.sendMessage(event.Channel, "No response generated.")
		return
	}

	_, _, err = h.client.PostMessage(event.Channel, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		slog.Error("failed to send block message in DM", "error", err)
		h.sendMessage(event.Channel, resp.Text)
	}
}

func (h *Handler) handleInteraction(ctx context.Context, callback slack.InteractionCallback) {
	slog.Info("received interaction",
		"type", callback.Type,
		"user", callback.User.ID,
	)

	for _, action := range callback.ActionCallback.BlockActions {
		switch {
		case strings.HasPrefix(action.ActionID, "confirm_"):
			h.handleConfirmation(ctx, callback, action)
		case strings.HasPrefix(action.ActionID, "cancel_"):
			h.updateMessage(callback.Channel.ID, callback.Message.Timestamp, "❌ Action cancelled.")
		case action.ActionID == "create_github_issue":
			h.handleCreateIssue(ctx, callback, action)
		case action.ActionID == "run_recipe":
			h.handleRunRecipe(ctx, callback, action)
		case action.ActionID == "dismiss_alert":
			h.updateMessage(callback.Channel.ID, callback.Message.Timestamp, "🔕 Alert dismissed.")
		}
	}
}

func (h *Handler) handleConfirmation(ctx context.Context, cb slack.InteractionCallback, action *slack.BlockAction) {
	var payload struct {
		Tool  string                 `json:"tool"`
		Input map[string]interface{} `json:"input"`
	}
	json.Unmarshal([]byte(action.Value), &payload)

	h.updateMessage(cb.Channel.ID, cb.Message.Timestamp, "⏳ Executing...")

	// Execute tool directly (already confirmed)
	inputJSON, _ := json.Marshal(payload.Input)
	result, err := h.registry.Execute(ctx, payload.Tool, inputJSON)
	if err != nil {
		h.sendMessage(cb.Channel.ID, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	if result.Success {
		h.sendMessage(cb.Channel.ID, result.Output)
	} else {
		h.sendMessage(cb.Channel.ID, fmt.Sprintf("❌ %s", result.Error))
	}
}

func (h *Handler) handleCreateIssue(ctx context.Context, cb slack.InteractionCallback, action *slack.BlockAction) {
	var payload struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	json.Unmarshal([]byte(action.Value), &payload)

	input := map[string]interface{}{"title": payload.Title, "body": payload.Body}
	inputJSON, _ := json.Marshal(input)

	result, err := h.registry.Execute(ctx, "github.create_issue", inputJSON)
	if err != nil {
		h.sendMessage(cb.Channel.ID, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	if result.Success {
		h.sendMessage(cb.Channel.ID, result.Output)
	} else {
		h.sendMessage(cb.Channel.ID, fmt.Sprintf("❌ %s", result.Error))
	}
}

func (h *Handler) handleRunRecipe(ctx context.Context, cb slack.InteractionCallback, action *slack.BlockAction) {
	var payload struct {
		Template  string            `json:"template"`
		Namespace string            `json:"namespace"`
		Params    map[string]string `json:"params"`
	}
	json.Unmarshal([]byte(action.Value), &payload)

	input := map[string]interface{}{
		"template_name": payload.Template,
		"namespace":     payload.Namespace,
		"parameters":    payload.Params,
	}
	inputJSON, _ := json.Marshal(input)

	result, err := h.registry.Execute(ctx, "argo.run_workflow", inputJSON)
	if err != nil {
		h.sendMessage(cb.Channel.ID, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	if result.Success {
		h.sendMessage(cb.Channel.ID, result.Output)
	} else {
		h.sendMessage(cb.Channel.ID, fmt.Sprintf("❌ %s", result.Error))
	}
}

func (h *Handler) sendMessage(channel, text string) {
	formattedText := formatForSlack(text)
	_, _, err := h.client.PostMessage(channel, slack.MsgOptionText(formattedText, false))
	if err != nil {
		slog.Error("failed to send message", "error", err)
	}
}

func (h *Handler) sendThreadedMessage(channel, threadTS, text string) {
	formattedText := formatForSlack(text)
	_, ts, err := h.client.PostMessage(channel,
		slack.MsgOptionText(formattedText, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		slog.Error("failed to send threaded message", "error", err, "channel", channel, "thread", threadTS)
	} else {
		slog.Info("message sent successfully", "channel", channel, "thread", threadTS, "message_ts", ts)
	}
}

func (h *Handler) updateMessage(channel, ts, text string) {
	formattedText := formatForSlack(text)
	_, _, _, err := h.client.UpdateMessage(channel, ts, slack.MsgOptionText(formattedText, false))
	if err != nil {
		slog.Error("failed to update message", "error", err)
	}
}

func (h *Handler) sendErrorBlock(channel, threadTS, errorMessage string) {
	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", "⚠️ Execution Error", false, false)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Message:* %s", truncate(errorMessage, 500)), false, false),
			nil, nil,
		),
		slack.NewContextBlock("error_footer",
			slack.NewTextBlockObject("mrkdwn", "_Please check the logs for more details or try a different query._", false, false),
		),
	}

	options := []slack.MsgOption{slack.MsgOptionBlocks(blocks...)}
	if threadTS != "" {
		options = append(options, slack.MsgOptionTS(threadTS))
	}

	_, _, err := h.client.PostMessage(channel, options...)
	if err != nil {
		slog.Error("failed to send error block", "error", err)
	}
}

// SendConfirmation sends a confirmation dialog for destructive actions
func (h *Handler) SendConfirmation(channel, toolName string, input map[string]interface{}) {
	payload, _ := json.Marshal(map[string]interface{}{
		"tool":  toolName,
		"input": input,
	})

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("⚠️ *Confirm Action*\n\nTool: `%s`\nInput: ```%s```", toolName, string(payload)),
				false, false),
			nil, nil,
		),
		slack.NewActionBlock("confirm_actions",
			slack.NewButtonBlockElement("confirm_"+toolName, string(payload),
				slack.NewTextBlockObject("plain_text", "✅ Confirm", false, false)).WithStyle(slack.StyleDanger),
			slack.NewButtonBlockElement("cancel_"+toolName, "",
				slack.NewTextBlockObject("plain_text", "❌ Cancel", false, false)),
		),
	}

	_, _, err := h.client.PostMessage(channel, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		slog.Error("failed to send confirmation", "error", err)
	}
}

var (
	reHeader    = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	reEvePrefix = regexp.MustCompile(`(?mi)^Eve:\s*`)
)

func formatForSlack(text string) string {
	// 0. Remove "Eve:" prefix if present
	text = reEvePrefix.ReplaceAllString(text, "")

	// 1. Convert headers (# Header) to bold (*Header*)
	text = reHeader.ReplaceAllString(text, "*$1*")

	// 2. Convert standard markdown bold (**bold**) to slack bold (*bold*)
	text = strings.ReplaceAll(text, "**", "*")

	// 3. Ensure code blocks use triple backticks properly
	text = regexp.MustCompile("(?m)^```\\w*\n").ReplaceAllString(text, "```\n")

	// 4. Remove fake tool call JSONs and the surrounding code blocks that LLM outputs
	// Matches ```json { ... } ``` or just { ... } variations
	reCodeBlockJSON := regexp.MustCompile("(?s)[\\n\\s]*`{3}(?:json)?\\s*[\\n\\s]*\\{\\s*\"(?:tool_name|name|function|arguments|type)\"[\\s\\S]*?\\}\\s*[\\n\\s]*`{3}[\\n\\s]*")
	text = reCodeBlockJSON.ReplaceAllString(text, "\n")

	// Fallback for JSON without code blocks
	reAggressiveTool := regexp.MustCompile(`(?s)\n?\{\s*"(?:tool_name|name|function|arguments|type)"[^}]+\}(\s*\})*`)
	reToolArray := regexp.MustCompile(`(?s)\[\s*\{\s*"(?:tool_name|name|function|arguments|type)"[\s\S]*?\}\s*\]`)
	text = reToolArray.ReplaceAllString(text, "")
	text = reAggressiveTool.ReplaceAllString(text, "")

	// 5. Clean up dangling tool-call shards and excess whitespace
	text = regexp.MustCompile(`(?m)^\s*[\[\]]\s*$`).ReplaceAllString(text, "")
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}
