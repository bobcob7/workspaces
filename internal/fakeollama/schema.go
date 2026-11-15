package fakeollama

import (
	"encoding/json/v2"
	"fmt"
)

// placeholderString is the value given to string arguments.
const placeholderString = "example"

// argumentsFor builds tool call arguments from a JSON schema: every required
// property gets its default, its first enum value, or a placeholder of its
// type. Keys are sorted so the arguments are the same on every run.
func argumentsFor(schema map[string]any) (string, error) {
	args, ok := valueFor(schema).(map[string]any)
	if !ok {
		args = map[string]any{}
	}
	b, err := json.Marshal(args, json.Deterministic(true))
	if err != nil {
		return "", fmt.Errorf("encoding tool arguments: %w", err)
	}
	return string(b), nil
}

func valueFor(schema map[string]any) any {
	if v, ok := schema["default"]; ok {
		return v
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	switch schemaType(schema) {
	case "object":
		return objectFor(schema)
	case "array":
		return []any{}
	case "integer", "number":
		return 1
	case "boolean":
		return true
	case "null":
		return nil
	default:
		return placeholderString
	}
}

// schemaType returns the schema's type, the first non-null one if it lists
// several, or "object" if it has properties but no type.
func schemaType(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return t
	case []any:
		for _, v := range t {
			if s, ok := v.(string); ok && s != "null" {
				return s
			}
		}
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	return ""
}

func objectFor(schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	obj := make(map[string]any, len(required))
	for _, r := range required {
		name, ok := r.(string)
		if !ok {
			continue
		}
		prop, _ := props[name].(map[string]any)
		obj[name] = valueFor(prop)
	}
	return obj
}
