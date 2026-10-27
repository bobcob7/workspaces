package submit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/review"
)

func TestNewReport(t *testing.T) {
	result := &review.Result{
		Comments:     2,
		Verification: review.Verification{Source: "repo", Anchors: 3, Verified: 2},
		Diagnostics: []review.Diagnostic{
			{Severity: review.SeverityError, Name: "schema", Path: "/summary", Message: "too short"},
			{Severity: review.SeverityAdvisory, Name: "body-thin", Comment: "a-1", Message: "thin"},
		},
		Skipped: []review.Skipped{{Name: "x", Reason: "r1"}, {Name: "y", Reason: "r2"}, {Name: "z", Reason: "r1"}},
	}
	r := NewReport(result)
	assert.Equal(t, Counts{Comments: 2, Errors: 1, Advisories: 1, Skipped: 3}, r.Counts)
	assert.Equal(t, []Skipped{{Reason: "r1", Checks: []string{"x", "z"}}, {Reason: "r2", Checks: []string{"y"}}}, r.Skipped)
	assert.Equal(t, []string{"schema", "body-thin"}, r.Lenses)
	data, err := json.Marshal(r)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	verification, ok := decoded["verification"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, verification, "reason", "a field with no value is null, never omitted (ADR 0016)")
	assert.Nil(t, verification["reason"])
	diagnostics, ok := decoded["diagnostics"].([]any)
	require.True(t, ok)
	first, ok := diagnostics[0].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, first["comment"])
	assert.Equal(t, "/summary", first["path"])
}

func TestNewReport_Empty(t *testing.T) {
	data, err := json.Marshal(NewReport(&review.Result{Valid: true}))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"diagnostics":[]`)
	assert.Contains(t, string(data), `"skipped":[]`)
	assert.Contains(t, string(data), `"lenses":[]`)
}
