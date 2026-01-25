// Package agent provides common utilities for agent implementations
package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// isSREAndInfraRelated checks if a message likely requires active SRE tools.
func isSREAndInfraRelated(message string) bool {
	m := strings.ToLower(message)

	actions := []string{
		"확인", "조회", "보여", "리스트", "로그", "체크", "상태", "재시작", "삭제", "생성", "부하", "이벤트",
		"check", "list", "get", "show", "status", "logs", "event", "restart", "describe", "diff", "monitor",
	}

	concepts := []string{
		"방법", "어떻게", "설명", "개념", "무엇", "차이", "의미", "정의", "가이드", "비교", "인가요",
		"how to", "what is", "difference", "explain", "tutorial", "guide", "define",
	}

	resources := []string{
		"pod", "k8s", "namespace", "node", "deployment", "pvc", "aws", "argo", "cluster",
		"ingress", "gateway", "service", "replica", "configmap", "secret",
		"파드", "노드", "배포", "네임스페이스", "클러스터", "서비스", "인그레스", "시크릿",
	}

	hasResource := containsAny(m, resources)
	hasAction := containsAny(m, actions)
	hasConcept := containsAny(m, concepts)
	hasSpecificName := strings.Contains(m, "-") || strings.Contains(m, "_")

	// If it's a concept question, it's not an SRE action
	if hasConcept {
		return false
	}

	// If it has both resource and action, it's an SRE request
	if hasResource && hasAction {
		return true
	}

	// If it has resource and a specific name (e.g., "fft-os"), it's likely an SRE request
	if hasResource && hasSpecificName {
		return true
	}

	return false
}

// containsAny checks if s contains any of the substrings
func containsAny(s string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// isKorean checks if a message contains Korean characters
func isKorean(message string) bool {
	for _, r := range message {
		// Korean Unicode range: Hangul Syllables (AC00-D7AF), Hangul Jamo (1100-11FF), Hangul Compatibility Jamo (3130-318F)
		if (r >= 0xAC00 && r <= 0xD7AF) || (r >= 0x1100 && r <= 0x11FF) || (r >= 0x3130 && r <= 0x318F) {
			return true
		}
	}
	return false
}

// CleanNamespaceInput removes common markdown artifacts from namespace names
func CleanNamespaceInput(input string) string {
	// Remove leading/trailing dots and spaces
	cleaned := strings.Trim(input, ". \t")

	// Remove markdown list markers
	cleaned = strings.TrimPrefix(cleaned, "* ")
	cleaned = strings.TrimPrefix(cleaned, "- ")
	cleaned = strings.TrimPrefix(cleaned, "+ ")

	return cleaned
}

// ExtractNamespaceFromMessage attempts to extract a namespace from a message
func ExtractNamespaceFromMessage(message string) string {
	// Common patterns for namespace mentions in Korean and English
	patterns := []string{
		`(?:네임스페이스|namespace)\s*[:\s]?\s*\.?([a-z0-9-]+)`,
		`\.?([a-z0-9-]+)\s*(?:네임스페이스|namespace)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(message)
		if len(matches) > 1 {
			return CleanNamespaceInput(matches[1])
		}
	}

	return ""
}

// truncateForLog truncates a string for logging purposes
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// buildPostToolAnalysisPrompt creates an enhanced prompt for post-tool analysis
// This guides the LLM to properly analyze tool results instead of giving generic responses
func buildPostToolAnalysisPrompt(userMessage string) string {
	if isKorean(userMessage) {
		return `## 분석 지침 (CRITICAL - 반드시 따르세요)

당신은 방금 도구 결과를 받았습니다. 다음 단계를 반드시 수행하세요:

### STEP 1: 에러/경고 확인
- 위에 "[AUTO-DETECTED]" 섹션이 있다면, 해당 에러들을 반드시 언급하세요
- ERROR, WARN, failed, exception 등의 키워드를 찾아 나열하세요

### STEP 2: 분석 및 요약
- 발견된 문제가 있다면 구체적으로 설명하세요
- 에러 메시지의 의미를 해석하세요
- 문제의 빈도나 패턴을 파악하세요

### STEP 3: 응답 형식
- 발견된 내용을 bullet point로 정리하세요
- 필요시 권장 조치사항을 제안하세요

⚠️ 절대 금지사항:
- "정상적으로 동작하고 있습니다" ← 에러가 있는데 이렇게 말하면 안됨
- "확인했습니다" 같은 일반적인 응답 ← 구체적인 분석 결과를 말해야 함
- 도구 결과를 무시하고 추측하기 ← 실제 데이터 기반으로만 응답

지금 바로 도구 결과를 분석하여 응답하세요.`
	}

	return `## Analysis Instructions (CRITICAL - You MUST follow these)

You just received tool results. Follow these steps:

### STEP 1: Check for Errors/Warnings
- If there's an "[AUTO-DETECTED]" section above, you MUST mention those errors
- Look for ERROR, WARN, failed, exception keywords and list them

### STEP 2: Analyze and Summarize
- If issues are found, explain them specifically
- Interpret what the error messages mean
- Identify frequency or patterns of problems

### STEP 3: Response Format
- Present findings as bullet points
- Suggest recommended actions if needed

⚠️ FORBIDDEN:
- "Everything is running normally" ← Don't say this if there are errors
- Generic responses like "I checked it" ← You must provide specific analysis
- Ignoring tool results and guessing ← Only respond based on actual data

Analyze the tool results and respond now.`
}

// PreprocessToolResult analyzes tool output and highlights important patterns
// This helps the LLM focus on critical information instead of generic responses
func PreprocessToolResult(toolName, result string) string {
	// Define error/warning patterns to detect
	errorPatterns := []string{"ERROR", "Error", "error", "FATAL", "Fatal", "fatal", "FAILED", "Failed", "failed", "Exception", "exception", "panic", "Panic", "PANIC"}
	warnPatterns := []string{"WARN", "Warn", "warn", "WARNING", "Warning", "warning"}

	lines := strings.Split(result, "\n")
	var errors []string
	var warnings []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check for error patterns
		for _, pattern := range errorPatterns {
			if strings.Contains(line, pattern) {
				errors = append(errors, trimmed)
				break
			}
		}

		// Check for warning patterns
		for _, pattern := range warnPatterns {
			if strings.Contains(line, pattern) {
				warnings = append(warnings, trimmed)
				break
			}
		}
	}

	// If no issues found, return original result
	if len(errors) == 0 && len(warnings) == 0 {
		return result
	}

	// Build highlighted output
	var sb strings.Builder

	if len(errors) > 0 {
		sb.WriteString(fmt.Sprintf("⚠️ [AUTO-DETECTED] %d ERROR(s) found in output:\n", len(errors)))
		sb.WriteString("─────────────────────────────────────\n")
		for i, e := range errors {
			if i >= 10 { // Limit to 10 errors to avoid overwhelming
				sb.WriteString(fmt.Sprintf("... and %d more errors\n", len(errors)-10))
				break
			}
			sb.WriteString("• " + e + "\n")
		}
		sb.WriteString("─────────────────────────────────────\n\n")
	}

	if len(warnings) > 0 {
		sb.WriteString(fmt.Sprintf("⚡ [AUTO-DETECTED] %d WARNING(s) found in output:\n", len(warnings)))
		sb.WriteString("─────────────────────────────────────\n")
		for i, w := range warnings {
			if i >= 5 { // Limit to 5 warnings
				sb.WriteString(fmt.Sprintf("... and %d more warnings\n", len(warnings)-5))
				break
			}
			sb.WriteString("• " + w + "\n")
		}
		sb.WriteString("─────────────────────────────────────\n\n")
	}

	sb.WriteString("📋 Full output:\n")
	sb.WriteString(result)

	return sb.String()
}
