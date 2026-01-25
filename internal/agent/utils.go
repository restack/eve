// Package agent provides common utilities for agent implementations
package agent

import (
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
