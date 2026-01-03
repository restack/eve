package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// AlertManager handles alert deduplication and aggregation.
type AlertManager struct {
	handler     *Handler
	alerts      map[string]*Alert
	mu          sync.Mutex
	dedupWindow time.Duration
}

// Alert represents a cluster alert.
type Alert struct {
	ID        string
	Type      string
	Resource  string
	Namespace string
	Message   string
	Count     int
	FirstSeen time.Time
	LastSeen  time.Time
	MessageTS string
	ChannelID string
}

// NewAlertManager creates a new alert manager.
func NewAlertManager(handler *Handler, dedupWindow time.Duration) *AlertManager {
	return &AlertManager{
		handler:     handler,
		alerts:      make(map[string]*Alert),
		dedupWindow: dedupWindow,
	}
}

// SendAlert sends or updates an alert in Slack.
func (am *AlertManager) SendAlert(ctx context.Context, channel string, alert *Alert) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	key := fmt.Sprintf("%s/%s/%s", alert.Type, alert.Namespace, alert.Resource)

	if existing, ok := am.alerts[key]; ok {
		if time.Since(existing.LastSeen) < am.dedupWindow {
			existing.Count++
			existing.LastSeen = time.Now()
			am.updateAlertMessage(ctx, existing)
			return nil
		}
	}

	alert.FirstSeen = time.Now()
	alert.LastSeen = time.Now()
	alert.Count = 1
	alert.ChannelID = channel

	ts, err := am.postAlertMessage(ctx, channel, alert)
	if err != nil {
		return err
	}

	alert.MessageTS = ts
	am.alerts[key] = alert
	return nil
}

func (am *AlertManager) postAlertMessage(ctx context.Context, channel string, alert *Alert) (string, error) {
	blocks := am.buildAlertBlocks(alert)

	_, ts, err := am.handler.client.PostMessage(channel, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		slog.Error("failed to post alert", "error", err)
		return "", err
	}

	return ts, nil
}

func (am *AlertManager) updateAlertMessage(ctx context.Context, alert *Alert) {
	blocks := am.buildAlertBlocks(alert)

	_, _, _, err := am.handler.client.UpdateMessage(
		alert.ChannelID,
		alert.MessageTS,
		slack.MsgOptionBlocks(blocks...),
	)
	if err != nil {
		slog.Error("failed to update alert", "error", err)
	}
}

func (am *AlertManager) buildAlertBlocks(alert *Alert) []slack.Block {
	emoji := "⚠️"
	if strings.Contains(strings.ToLower(alert.Type), "error") {
		emoji = "🔴"
	}

	header := fmt.Sprintf("%s *%s*", emoji, alert.Type)
	if alert.Count > 1 {
		header += fmt.Sprintf(" (×%d)", alert.Count)
	}

	details := fmt.Sprintf("*Resource:* `%s/%s`\n*Message:* %s\n*First seen:* %s",
		alert.Namespace, alert.Resource, alert.Message, alert.FirstSeen.Format(time.RFC3339))

	issuePayload, _ := json.Marshal(map[string]string{
		"title": fmt.Sprintf("[Eve Alert] %s: %s/%s", alert.Type, alert.Namespace, alert.Resource),
		"body": fmt.Sprintf("## Alert Details\n\n- **Type:** %s\n- **Resource:** %s/%s\n- **Message:** %s\n- **Count:** %d\n- **First seen:** %s\n- **Last seen:** %s",
			alert.Type, alert.Namespace, alert.Resource, alert.Message, alert.Count,
			alert.FirstSeen.Format(time.RFC3339), alert.LastSeen.Format(time.RFC3339)),
	})

	recipePayload, _ := json.Marshal(map[string]interface{}{
		"template":  alert.Type,
		"namespace": alert.Namespace,
		"params":    map[string]string{"resource": alert.Resource},
	})

	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", header, false, false),
			nil, nil,
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", details, false, false),
			nil, nil,
		),
		slack.NewActionBlock("alert_actions",
			slack.NewButtonBlockElement("create_github_issue", string(issuePayload),
				slack.NewTextBlockObject("plain_text", "📝 Create Issue", false, false)),
			slack.NewButtonBlockElement("run_recipe", string(recipePayload),
				slack.NewTextBlockObject("plain_text", "🔧 Run Recipe", false, false)),
			slack.NewButtonBlockElement("dismiss_alert", "",
				slack.NewTextBlockObject("plain_text", "🔕 Dismiss", false, false)),
		),
	}
}

// Cleanup removes old alerts from the cache.
func (am *AlertManager) Cleanup() {
	am.mu.Lock()
	defer am.mu.Unlock()

	cutoff := time.Now().Add(-am.dedupWindow * 2)
	for key, alert := range am.alerts {
		if alert.LastSeen.Before(cutoff) {
			delete(am.alerts, key)
		}
	}
}
