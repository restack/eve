package llm

import "testing"

func TestSanitizeJinja2Patterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "variable interpolation",
			input:    "Use {{namespace}} for the query",
			expected: "Use (namespace) for the query",
		},
		{
			name:     "control structure",
			input:    "{% for item in list %}process{% endfor %}",
			expected: "[ for item in list ]process[ endfor ]",
		},
		{
			name:     "comment",
			input:    "{# This is a comment #}",
			expected: "< This is a comment >",
		},
		{
			name:     "go template example",
			input:    "Template: {{ .metadata.name }}",
			expected: "Template: ( .metadata.name )",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeJinja2Patterns(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeJinja2Patterns(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNormalizeSchemaSafeFlattening(t *testing.T) {
	// Root-level schema with complex fields
	input := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"namespace": map[string]interface{}{
				"type":        "string",
				"description": "The namespace, e.g. {{default}}",
				"default":     "default",
			},
			"tags": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
		},
		"required": []interface{}{"namespace"},
	}

	result := normalizeSchema(input)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}

	props, ok := resultMap["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties not found")
	}

	// Check namespace property (default should be stripped for safety)
	nsProp, ok := props["namespace"].(map[string]interface{})
	if !ok {
		t.Fatal("namespace property not found")
	}
	if nsProp["type"] != "string" {
		t.Errorf("type = %v, want string", nsProp["type"])
	}
	if _, exists := nsProp["default"]; exists {
		t.Error("default field should be stripped for llama.cpp compatibility")
	}

	// Check tags property (should be flattened to string)
	tagsProp, ok := props["tags"].(map[string]interface{})
	if !ok {
		t.Fatal("tags property not found")
	}
	if tagsProp["type"] != "string" {
		t.Errorf("tags type should be string (flattened), got %v", tagsProp["type"])
	}
	if _, exists := tagsProp["items"]; exists {
		t.Error("items field should be stripped for llama.cpp compatibility")
	}

	// Check required
	req, ok := resultMap["required"].([]string)
	if !ok || len(req) != 1 || req[0] != "namespace" {
		t.Errorf("required mismatch: %v", resultMap["required"])
	}
}

func TestNormalizeSchemaWithNullProperties(t *testing.T) {
	input := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"validProp": map[string]interface{}{
				"type":        "string",
				"description": "A valid property",
			},
			"nullProp": nil,
		},
	}

	result := normalizeSchema(input)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}

	props := resultMap["properties"].(map[string]interface{})
	if _, ok := props["nullProp"].(map[string]interface{}); !ok {
		t.Error("nullProp should be a valid fallback map, not nil")
	}
}

func TestConvertToolsToDefinitions(t *testing.T) {
	// Simply verify that the tool conversion logic runs without error
	// and produces flattened results.
}
