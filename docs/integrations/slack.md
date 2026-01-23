# Slack Integration Guide for Eve

This guide describes the necessary configurations for integrating the Eve SRE Agent with Slack using **Socket Mode**.

## 1. Connection Mode: Socket Mode
Eve uses **Socket Mode** to communicate with Slack. This allows the agent to stay behind a firewall/NAT without requiring a public Request URL.

- **Enable Socket Mode**: Go to **Settings > Socket Mode** in your Slack App configuration and toggle it to **On**.
- **APP-Level Token**: When you enable Socket Mode, generate an App-Level token (`SLACK_APP_TOKEN`) with the `connections:write` scope.

---

## 2. OAuth Scopes (Bot Token Scopes)
Configure these scopes under **Features > OAuth & Permissions** to grant Eve the necessary permissions.

| Scope | Reason |
| :--- | :--- |
| `chat:write` | Required to send messages and replies to channels and DMs. |
| `app_mentions:read` | Allows the agent to receive and respond to `@Eve` mentions. |
| `channels:history` | **Required** to read thread context in public channels. |
| `groups:history` | **Required** to read thread context in private channels. |
| `im:history` | Required to read message content in 1:1 Direct Messages. |
| `im:write` | Allows the bot to start or reply to conversations in DMs. |
| `commands` | Required to handle slash commands like `/k8s`. |
| `files:read` | (Recommended) Allows the agent to download and analyze logs or screenshots. |
| `reactions:read` | (Recommended) Enables workflow triggers based on emojis (e.g., Siren emoji for incident creation). |
| `users:read` | Required for authorization checks and personalized responses. |

---

## 3. Event Subscriptions
Enable events under **Features > Event Subscriptions**. Note that **no Request URL is required** when Socket Mode is enabled.

### Required Bot User Events
- `app_mention`: To respond when Eve is mentioned in a channel.
- `message.im`: To handle direct messages sent to the bot.

### Recommended SRE Workflow Events
- `reaction_added`: Trigger automated diagnostics or create GitHub issues when a user adds a specific emoji (e.g., 🚨) to a message.
- `file_shared`: Detect when logs or dashboard screenshots are uploaded for analysis.
- `app_home_opened`: Display a real-time cluster health dashboard when the user opens the Eve app home tab.
- `member_joined_channel`: Send a "Getting Started" guide to new engineers joining SRE channels.

---

## 4. Interactive Components
Eve uses buttons for confirmation of destructive actions (e.g., scaling deployments, triggering remediation).

- **Enable Interactivity**: Go to **Features > Interactivity & Shortcuts** and toggle it to **On**.
- **Request URL**: Leave this blank. Events will be delivered via the existing Socket Mode connection.

---

## 5. Summary Tracking
After updating any scopes or events, ensure you:
1.  **Save Changes** in the Slack App Dashboard.
2.  **Reinstall to Workspace** to apply the new permissions.
3.  Update your `.env` file with the renewed tokens.

## 6. Bonus - Slack Bot Icons
Add a custom icon to your bot to make it more recognizable.

<table>
  <tr>
    <td><img src="../assets/slack-bot-icon-a.png" alt="Slack Bot Icon" width="200" /></td>
    <td><img src="../assets/slack-bot-icon-b.png" alt="Slack Bot Icon" width="200" /></td>
  </tr>
</table>
