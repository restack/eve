// Package slack provides the Slack Socket Mode handler for Eve.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

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
		client:   client,
		socket:   socket,
		agent:    ag,
		registry: registry,
		cfg:      cfg,
	}, nil
}

// Run starts the Socket Mode event loop.
func (h *Handler) Run(ctx context.Context) error {
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
	if cmd.Command == "/k8s" {
		message = "kubernetes " + message
	}

	req := &agent.Request{
		UserID:    cmd.UserID,
		ChannelID: cmd.ChannelID,
		Message:   message,
	}

	// Process through agent
	resp, err := h.agent.Process(ctx, req)
	if err != nil {
		slog.Error("agent error", "error", err)
		h.sendMessage(cmd.ChannelID, fmt.Sprintf("❌ Error: %v", err))
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
	slog.Info("received mention",
		"text", event.Text,
		"user", event.User,
		"channel", event.Channel,
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
			for _, m := range msgs {
				// Skip the current message
				if m.Timestamp == event.TimeStamp {
					continue
				}
				// Format: "message"
				threadContext = append(threadContext, m.Text)
			}
		}
	} else {
		slog.Info("new thread started, skipping previous channel context")
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
		h.sendThreadedMessage(event.Channel, replyThread, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	// Log the response for debugging
	slog.Info("sending response to slack",
		"channel", event.Channel,
		"thread", replyThread,
		"response_length", len(resp.Text),
		"response_preview", truncate(resp.Text, 100),
	)

	h.sendThreadedMessage(event.Channel, replyThread, resp.Text)
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
		h.sendMessage(event.Channel, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	h.sendMessage(event.Channel, resp.Text)
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
	// Match various fake tool call JSON patterns
	reToolCallJSON  = regexp.MustCompile(`(?s)\{\s*"tool_name"[^}]+\}(\s*\})?`)
	reFunctionCall  = regexp.MustCompile(`(?s)\{\s*"type"\s*:\s*"function"[^}]+\}(\s*\})*`)
	reFunctionBlock = regexp.MustCompile(`(?s)\{\s*"type"\s*:\s*"function",\s*"function"\s*:\s*\{[^}]+\}[^}]*\}`)
	reGenericTool   = regexp.MustCompile(`(?s)\{\s*"name"\s*:\s*"[^"]+",\s*"arguments"\s*:[^}]+\}`)
)

func formatForSlack(text string) string {
	// 0. Remove "Eve:" prefix if present (LLM sometimes adds this)
	text = reEvePrefix.ReplaceAllString(text, "")

	// 1. Convert headers (# Header) to bold (*Header*)
	text = reHeader.ReplaceAllString(text, "*$1*")

	// 2. Convert standard markdown bold (**bold**) to slack bold (*bold*)
	text = strings.ReplaceAll(text, "**", "*")

	// 3. Ensure code blocks use triple backticks properly (Slack format)
	// Replace ```language with just ``` for Slack compatibility
	text = regexp.MustCompile("(?m)^```\\w*\n").ReplaceAllString(text, "```\n")

	// 4. Remove fake tool call JSONs that LLM outputs when tools aren't being invoked properly
	// This regex looks for JSON objects that contain tool-related keys
	reAggressiveTool := regexp.MustCompile(`(?s)\n?\{\s*"(?:tool_name|name|function|arguments|type)"[^}]+\}(\s*\})*`)
	text = reAggressiveTool.ReplaceAllString(text, "")

	// 5. Clean up multiple newlines and trim
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
