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
			expected: "Use { {namespace} } for the query",
		},
		{
			name:     "control structure",
			input:    "{% for item in list %}process{% endfor %}",
			expected: "{ % for item in list % }process{ % endfor % }",
		},
		{
			name:     "comment",
			input:    "{# This is a comment #}",
			expected: "{ # This is a comment # }",
		},
		{
			name:     "no patterns",
			input:    "Normal description without patterns",
			expected: "Normal description without patterns",
		},
		{
			name:     "json example with braces",
			input:    `Example: {"key": "value"}`,
			expected: `Example: {"key": "value"}`,
		},
		{
			name:     "helm template example",
			input:    "Use {{ .Values.replicas }} to set replicas",
			expected: "Use { { .Values.replicas } } to set replicas",
		},
		{
			name:     "go template example",
			input:    "Template: {{ .metadata.name }}",
			expected: "Template: { { .metadata.name } }",
		},
		{
			name:     "multiple patterns",
			input:    "{{a}} and {{b}} with {% if c %}d{% endif %}",
			expected: "{ {a} } and { {b} } with { % if c % }d{ % endif % }",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
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

func TestNormalizeSchemaWithJinja2Patterns(t *testing.T) {
	input := map[string]interface{}{
		"type":        "object",
		"description": "Use {{namespace}} for filtering",
		"properties": map[string]interface{}{
			"namespace": map[string]interface{}{
				"type":        "string",
				"description": "The namespace, e.g. {{default}}",
			},
		},
	}

	result := normalizeSchema(input)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}

	// Check top-level description
	if desc, ok := resultMap["description"].(string); ok {
		if desc != "Use { {namespace} } for filtering" {
			t.Errorf("top-level description not sanitized: %q", desc)
		}
	} else {
		t.Error("description not found or not a string")
	}

	// Check nested property description
	props, ok := resultMap["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties not found")
	}
	nsProp, ok := props["namespace"].(map[string]interface{})
	if !ok {
		t.Fatal("namespace property not found")
	}
	if desc, ok := nsProp["description"].(string); ok {
		if desc != "The namespace, e.g. { {default} }" {
			t.Errorf("nested description not sanitized: %q", desc)
		}
	} else {
		t.Error("nested description not found or not a string")
	}
}

func TestNormalizeSchemaWithEnumValues(t *testing.T) {
	input := map[string]interface{}{
		"type": "string",
		"enum": []interface{}{
			"{{default}}",
			"kube-system",
			"{% if prod %}production{% endif %}",
		},
	}

	result := normalizeSchema(input)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}

	enum, ok := resultMap["enum"].([]interface{})
	if !ok {
		t.Fatal("enum not found")
	}

	expected := []string{
		"{ {default} }",
		"kube-system",
		"{ % if prod % }production{ % endif % }",
	}

	for i, exp := range expected {
		if enum[i].(string) != exp {
			t.Errorf("enum[%d] = %q, want %q", i, enum[i], exp)
		}
	}
}

func TestNormalizeSchemaWithNullProperties(t *testing.T) {
	// This test ensures that null property values are handled properly
	// vLLM's Jinja2 template calls .keys() on each property, which fails on null
	input := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"validProp": map[string]interface{}{
				"type":        "string",
				"description": "A valid property",
			},
			"nullProp": nil, // This would cause vLLM to fail
			"emptyProp": map[string]interface{}{
				// No type specified
			},
		},
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

	// Check validProp is preserved
	if validProp, ok := props["validProp"].(map[string]interface{}); ok {
		if validProp["type"] != "string" {
			t.Errorf("validProp type = %v, want string", validProp["type"])
		}
	} else {
		t.Error("validProp not found or not a map")
	}

	// Check nullProp is converted to a valid schema (not null)
	if nullProp, ok := props["nullProp"].(map[string]interface{}); ok {
		if nullProp["type"] == nil {
			t.Error("nullProp should have a type, got nil")
		}
	} else {
		t.Error("nullProp should be a valid map, not nil")
	}

	// Check emptyProp has a default type
	if emptyProp, ok := props["emptyProp"].(map[string]interface{}); ok {
		if emptyProp["type"] == nil {
			t.Error("emptyProp should have a default type")
		}
	} else {
		t.Error("emptyProp not found or not a map")
	}
}

func TestNormalizeSchemaWithArrays(t *testing.T) {
	input := map[string]interface{}{
		"type": "array",
		"items": map[string]interface{}{
			"type":        "string",
			"description": "An item with {{template}}",
		},
	}

	result := normalizeSchema(input)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}

	if resultMap["type"] != "array" {
		t.Errorf("type = %v, want array", resultMap["type"])
	}

	items, ok := resultMap["items"].(map[string]interface{})
	if !ok {
		t.Fatal("items not found or not a map")
	}

	if items["type"] != "string" {
		t.Errorf("items type = %v, want string", items["type"])
	}

	desc, ok := items["description"].(string)
	if !ok || desc != "An item with { {template} }" {
		t.Errorf("items description = %q, want %q", desc, "An item with { {template} }")
	}
}

func TestNormalizeSchemaWithNilInput(t *testing.T) {
	result := normalizeSchema(nil)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{} for nil input")
	}

	if resultMap["type"] != "object" {
		t.Errorf("nil input should produce type=object, got %v", resultMap["type"])
	}
}

func TestCreateMinimalSchema(t *testing.T) {
	// Test with complex MCP-like schema
	input := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"namespace": map[string]interface{}{
				"type":        "string",
				"description": "The namespace with {{template}} syntax",
				"default":     "default",
				"enum":        []interface{}{"default", "kube-system"},
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Resource name",
			},
			"nullProp": nil, // This would cause vLLM to fail
		},
		"required": []interface{}{"namespace", "name"},
	}

	result := createMinimalSchema(input)

	// Check structure
	if result["type"] != "object" {
		t.Errorf("type = %v, want object", result["type"])
	}

	props, ok := result["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties not found")
	}

	// Check namespace property
	if nsProp, ok := props["namespace"].(map[string]interface{}); ok {
		if nsProp["type"] != "string" {
			t.Errorf("namespace type = %v, want string", nsProp["type"])
		}
		// Description should be sanitized
		if desc, ok := nsProp["description"].(string); ok {
			if desc != "The namespace with { {template} } syntax" {
				t.Errorf("description not sanitized: %q", desc)
			}
		}
	} else {
		t.Error("namespace property not found")
	}

	// Check required
	req, ok := result["required"].([]string)
	if !ok {
		t.Fatal("required not found or wrong type")
	}
	if len(req) != 2 {
		t.Errorf("required length = %d, want 2", len(req))
	}
}

func TestCreateMinimalSchemaWithNil(t *testing.T) {
	result := createMinimalSchema(nil)

	if result["type"] != "object" {
		t.Errorf("type = %v, want object", result["type"])
	}

	props, ok := result["properties"].(map[string]interface{})
	if !ok || len(props) != 0 {
		t.Error("properties should be empty map for nil input")
	}
}
