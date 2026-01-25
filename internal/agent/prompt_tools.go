// Package agent provides prompt-based tool calling utilities
package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ToolCallRequest represents a parsed tool call from LLM text output
type ToolCallRequest struct {
	ToolName  string
	Arguments string
}

// ParseToolCallsFromText extracts tool calls from LLM text output
func ParseToolCallsFromText(content string) []ToolCallRequest {
	var calls []ToolCallRequest

	// Pattern 1: [TOOL:name]{"args"}[/TOOL]
	pattern1 := regexp.MustCompile(`(?i)\[TOOL:([a-zA-Z0-9_\.\-]+)\](.*?)\[/TOOL\]`)
	matches := pattern1.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			toolName := strings.TrimSpace(match[1])
			rawArgs := strings.TrimSpace(match[2])
			jsonArgs := extractJSON(rawArgs)
			if jsonArgs != "" {
				calls = append(calls, ToolCallRequest{ToolName: toolName, Arguments: jsonArgs})
			}
		}
	}

	// Pattern 2: <function=name><parameter=key>value</parameter>...</function>
	// Used by some LLMs like Qwen
	pattern2 := regexp.MustCompile(`(?is)<function=([a-zA-Z0-9_\.\-]+)>(.*?)</function>`)
	matches2 := pattern2.FindAllStringSubmatch(content, -1)

	for _, match := range matches2 {
		if len(match) >= 3 {
			toolName := strings.TrimSpace(match[1])
			paramsContent := match[2]

			// Extract parameters from <parameter=key>value</parameter> format
			paramPattern := regexp.MustCompile(`<parameter=([^>]+)>([^<]*)</parameter>`)
			paramMatches := paramPattern.FindAllStringSubmatch(paramsContent, -1)

			args := make(map[string]interface{})
			for _, pm := range paramMatches {
				if len(pm) >= 3 {
					key := strings.TrimSpace(pm[1])
					value := strings.TrimSpace(pm[2])
					args[key] = value
				}
			}

			if len(args) > 0 {
				jsonBytes, _ := json.Marshal(args)
				calls = append(calls, ToolCallRequest{ToolName: toolName, Arguments: string(jsonBytes)})
			}
		}
	}

	// Pattern 3: <function=name> without closing tag (incomplete)
	// Handle case like: <function=pods_list_in_namespace><parameter=namespace>fft-os</parameter>
	if len(calls) == 0 {
		pattern3 := regexp.MustCompile(`(?is)<function=([a-zA-Z0-9_\.\-]+)>(.*)`)
		matches3 := pattern3.FindAllStringSubmatch(content, -1)

		for _, match := range matches3 {
			if len(match) >= 3 {
				toolName := strings.TrimSpace(match[1])
				paramsContent := match[2]

				paramPattern := regexp.MustCompile(`<parameter=([^>]+)>([^<]*)(?:</parameter>)?`)
				paramMatches := paramPattern.FindAllStringSubmatch(paramsContent, -1)

				args := make(map[string]interface{})
				for _, pm := range paramMatches {
					if len(pm) >= 3 {
						key := strings.TrimSpace(pm[1])
						value := strings.TrimSpace(pm[2])
						args[key] = value
					}
				}

				if len(args) > 0 {
					jsonBytes, _ := json.Marshal(args)
					calls = append(calls, ToolCallRequest{ToolName: toolName, Arguments: string(jsonBytes)})
				}
			}
		}
	}

	return calls
}

// extractJSON finds and returns a balanced JSON object from the input string
func extractJSON(rawArgs string) string {
	start := strings.Index(rawArgs, "{")
	if start == -1 {
		return ""
	}

	count := 0
	for i := start; i < len(rawArgs); i++ {
		if rawArgs[i] == '{' {
			count++
		}
		if rawArgs[i] == '}' {
			count--
		}
		if count == 0 {
			jsonArgs := rawArgs[start : i+1]
			var test map[string]interface{}
			if json.Unmarshal([]byte(jsonArgs), &test) == nil {
				return jsonArgs
			}
			return ""
		}
	}
	return ""
}

// StripToolCallMarkers removes tool call markers from conversational text
func StripToolCallMarkers(content string) string {
	// Remove [TOOL:...]...[/TOOL] format
	pattern1 := regexp.MustCompile(`(?is)\[TOOL:.*?\].*?\[/TOOL\]`)
	content = pattern1.ReplaceAllString(content, "")

	// Remove <function=...>...</function> format
	pattern2 := regexp.MustCompile(`(?is)<function=[^>]*>.*?</function>`)
	content = pattern2.ReplaceAllString(content, "")

	// Remove incomplete <function=...> format
	pattern3 := regexp.MustCompile(`(?is)<function=[^>]*>.*`)
	content = pattern3.ReplaceAllString(content, "")

	// Remove hallucination patterns
	pattern4 := regexp.MustCompile(`(?is)<details>.*?</details>`)
	content = pattern4.ReplaceAllString(content, "")

	pattern5 := regexp.MustCompile(`\{\s*"status"\s*:\s*"[^"]*"\s*\}`)
	content = pattern5.ReplaceAllString(content, "")

	// Remove kubectl commands in code blocks
	pattern6 := regexp.MustCompile("(?is)```(?:bash|shell|sh)?\\s*kubectl[^`]*```")
	content = pattern6.ReplaceAllString(content, "")

	// Remove inline kubectl commands
	pattern7 := regexp.MustCompile("`kubectl[^`]*`")
	content = pattern7.ReplaceAllString(content, "")

	return strings.TrimSpace(content)
}

// GenerateDynamicToolPrompt creates a technical spec for the model to follow
func GenerateDynamicToolPrompt(tools []string) string {
	var sb strings.Builder

	sb.WriteString("\n## 🛠️ Category B: SRE Tool Calling Protocol\n\n")

	sb.WriteString("### ⚠️ ABSOLUTE RULE (NO EXCEPTIONS)\n")
	sb.WriteString("For SRE requests, your response MUST start with the character `[`\n")
	sb.WriteString("- First character of your response: `[`\n")
	sb.WriteString("- NOT \"안녕\", NOT \"확인\", NOT any Korean/English text\n")
	sb.WriteString("- ONLY `[TOOL:...]` is allowed as the first output\n\n")

	sb.WriteString("### 🚫 FORBIDDEN (will cause system failure)\n")
	sb.WriteString("- \"확인해볼게요\" ← FORBIDDEN\n")
	sb.WriteString("- \"잠시만 기다려주세요\" ← FORBIDDEN\n")
	sb.WriteString("- \"조회하겠습니다\" ← FORBIDDEN\n")
	sb.WriteString("- \"I'll check\" ← FORBIDDEN\n")
	sb.WriteString("- Any text before [TOOL:...] ← FORBIDDEN\n\n")

	sb.WriteString("### Tool Call Format\n")
	sb.WriteString("[TOOL:tool_name]{\"param\":\"value\"}[/TOOL]\n\n")

	sb.WriteString("### 🔍 Error Investigation Protocol (CRITICAL)\n")
	sb.WriteString("When asked about an error or issue, follow this sequence:\n\n")
	sb.WriteString("**STEP 1: Extract context from thread/message**\n")
	sb.WriteString("- Identify: namespace, pod name, app name, error type\n")
	sb.WriteString("- Example: 'Error in qdrant' → namespace=qdrant\n")
	sb.WriteString("- Example: 'pod qdrant-0 error' → namespace=qdrant, pod=qdrant-0\n\n")
	sb.WriteString("**STEP 2: Choose the RIGHT first tool**\n")
	sb.WriteString("- If pod name is known → `pods_log` (check logs first)\n")
	sb.WriteString("- If only namespace is known → `pods_list_in_namespace` (find the pod)\n")
	sb.WriteString("- If asking 'what error?' → `pods_log` with the specific pod\n")
	sb.WriteString("- Use `events_list` only AFTER checking pod status/logs\n\n")
	sb.WriteString("**STEP 3: ALWAYS specify namespace**\n")
	sb.WriteString("- NEVER call tools without namespace when it's mentioned in context\n")
	sb.WriteString("- Example: Thread says 'qdrant error' → use namespace='qdrant'\n\n")

	sb.WriteString("### 🎯 Tool Selection Guide\n")
	sb.WriteString("Match keywords to the correct tool:\n\n")
	sb.WriteString("| Keywords | Tool to Use |\n")
	sb.WriteString("|----------|-------------|\n")
	sb.WriteString("| error, 에러, 문제, issue, 무슨 에러 | `pods_log` (with namespace+pod) |\n")
	sb.WriteString("| metrics, cpu, memory, top, 메트릭 | `pods_top` or `nodes_top` |\n")
	sb.WriteString("| logs, 로그 | `pods_log` |\n")
	sb.WriteString("| status, list, 상태, 목록, 파드 | `pods_list_in_namespace` |\n")
	sb.WriteString("| events, 이벤트 | `events_list` (with namespace) |\n")
	sb.WriteString("| describe, details, 상세 | `pods_get` or `resources_get` |\n")
	sb.WriteString("| delete, 삭제 | `pods_delete` or `resources_delete` |\n")
	sb.WriteString("| helm, chart | `helm_list`, `helm_install` |\n")
	sb.WriteString("| namespaces, ns 목록 | `namespaces_list` |\n")
	sb.WriteString("| node, 노드 | `nodes_top`, `nodes_stats_summary` |\n\n")

	sb.WriteString("### Correct Response Pattern\n\n")
	sb.WriteString("**Error Investigation (with thread context):**\n")
	sb.WriteString("Thread: 'Error detected in qdrant, Pod: qdrant-0'\n")
	sb.WriteString("User: '이건 무슨 에러야?'\n")
	sb.WriteString("Assistant: [TOOL:pods_log]{\"namespace\":\"qdrant\",\"name\":\"qdrant-0\"}[/TOOL]\n\n")

	sb.WriteString("**Error Investigation (namespace only):**\n")
	sb.WriteString("Thread: 'Error in postgres namespace'\n")
	sb.WriteString("User: '무슨 문제야?'\n")
	sb.WriteString("Assistant: [TOOL:pods_list_in_namespace]{\"namespace\":\"postgres\"}[/TOOL]\n\n")

	sb.WriteString("**General queries:**\n")
	sb.WriteString("User: \"fft-os 파드 상태 확인해줘\"\n")
	sb.WriteString("Assistant: [TOOL:pods_list_in_namespace]{\"namespace\":\"fft-os\"}[/TOOL]\n\n")

	sb.WriteString("User: \"postgres namespace pod metrics 보여줘\"\n")
	sb.WriteString("Assistant: [TOOL:pods_top]{\"namespace\":\"postgres\"}[/TOOL]\n\n")

	sb.WriteString("### Available Tools\n")
	for _, t := range tools {
		sb.WriteString("- " + t + "\n")
	}

	return sb.String()
}
