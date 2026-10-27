package structural

import "github.com/bobcob7/workspaces/internal/review/schema"

// schemaValidator checks a decoded document against the embedded JSON Schema.
type schemaValidator interface {
	Validate(instance any) []schema.Failure
}
