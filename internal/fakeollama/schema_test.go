package fakeollama

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgumentsFor(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{"no schema", nil, `{}`},
		{"non-object schema", map[string]any{"type": "string"}, `{}`},
		{"only required properties", map[string]any{
			"type":       "object",
			"properties": map[string]any{"a": map[string]any{"type": "string"}, "b": map[string]any{"type": "string"}},
			"required":   []any{"a"},
		}, `{"a":"example"}`},
		{"placeholders by type", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"i": map[string]any{"type": "integer"}, "n": map[string]any{"type": "number"}, "b": map[string]any{"type": "boolean"},
				"l": map[string]any{"type": "array"}, "z": map[string]any{"type": "null"}, "u": map[string]any{},
			},
			"required": []any{"i", "n", "b", "l", "z", "u"},
		}, `{"b":true,"i":1,"l":[],"n":1,"u":"example","z":null}`},
		{"defaults and enums", map[string]any{
			"properties": map[string]any{"d": map[string]any{"type": "string", "default": "x"}, "e": map[string]any{"type": "string", "enum": []any{"c", "f"}}},
			"required":   []any{"d", "e"},
		}, `{"d":"x","e":"c"}`},
		{"nullable type lists", map[string]any{
			"type":       "object",
			"properties": map[string]any{"s": map[string]any{"type": []any{"null", "integer"}}},
			"required":   []any{"s", 7},
		}, `{"s":1}`},
		{"nested objects", map[string]any{
			"type": "object",
			"properties": map[string]any{"o": map[string]any{
				"type": "object", "properties": map[string]any{"k": map[string]any{"type": "string"}}, "required": []any{"k"},
			}},
			"required": []any{"o"},
		}, `{"o":{"k":"example"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := argumentsFor(tt.schema)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
