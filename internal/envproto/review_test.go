package envproto

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidReviewID(t *testing.T) {
	t.Run("accepts an id that starts with a letter or digit", func(t *testing.T) {
		assert.True(t, ValidReviewID("review-r-42-3f2a9c1-d8efb24f"))
		assert.True(t, ValidReviewID("9lives"))
	})
	t.Run("refuses an empty id, a leading dot and a slash", func(t *testing.T) {
		assert.False(t, ValidReviewID(""))
		assert.False(t, ValidReviewID(".hidden"))
		assert.False(t, ValidReviewID("a/b"))
	})
	t.Run("refuses an id longer than 128 bytes", func(t *testing.T) {
		assert.True(t, ValidReviewID("a"+strings.Repeat("b", 127)))
		assert.False(t, ValidReviewID("a"+strings.Repeat("b", 128)))
	})
}

func TestValidFindingID(t *testing.T) {
	t.Run("accepts profile:id", func(t *testing.T) {
		assert.True(t, ValidFindingID("correctness:F3.a_b-1"))
	})
	t.Run("refuses an id without a profile", func(t *testing.T) {
		assert.False(t, ValidFindingID("F3"))
	})
	t.Run("refuses upper case profiles and an empty finding", func(t *testing.T) {
		assert.False(t, ValidFindingID("Correct:F3"))
		assert.False(t, ValidFindingID("correct:"))
	})
}

func TestValidate_review(t *testing.T) {
	manyFindings := make([]string, MaxFindings+1)
	for i := range manyFindings {
		manyFindings[i] = "correctness:F1"
	}
	cases := []struct {
		name string
		args any
		code string // "" accepts the arguments
	}{
		{"accepts a start by pull request", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(42)}, ""},
		{"accepts a start by head commit", &ReviewStartArgs{Repo: "github.com/o/r", Head: new(strings.Repeat("a", 40))}, ""},
		{"accepts profiles with a model", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Profiles: []string{"security:claude-sonnet-5-5"}}, ""},
		{"accepts a permission mode the claude CLI documents", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), PermissionMode: new("acceptEdits")}, ""},
		{"refuses a repo with @ with spec_invalid", &ReviewStartArgs{Repo: "github.com/o/r@main", PR: new(1)}, "spec_invalid"},
		{"refuses an empty repo with spec_invalid", &ReviewStartArgs{PR: new(1)}, "spec_invalid"},
		{"refuses pr and head together with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Head: new(strings.Repeat("a", 40))}, "usage"},
		{"refuses a start with neither pr nor head with usage", &ReviewStartArgs{Repo: "github.com/o/r"}, "usage"},
		{"refuses a pr of 0 with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(0)}, "usage"},
		{"refuses a pr above 2^31-1 with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(math.MaxInt32 + 1)}, "usage"},
		{"refuses a head with upper case hex with usage", &ReviewStartArgs{Repo: "github.com/o/r", Head: new(strings.Repeat("A", 40))}, "usage"},
		{"refuses a head of 39 characters with usage", &ReviewStartArgs{Repo: "github.com/o/r", Head: new(strings.Repeat("a", 39))}, "usage"},
		{"refuses a name with a control character with name_invalid", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Name: new("bad\nname")}, "name_invalid"},
		{"refuses a profile with a space with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Profiles: []string{"Bad Name"}}, "usage"},
		{"refuses 17 profiles with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Profiles: make([]string, 17)}, "usage"},
		{"refuses an auto profile that is not a name with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Auto: []string{"-x"}}, "usage"},
		{"refuses a model starting with a dash with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Model: new("-x")}, "usage"},
		{"refuses a permission mode outside the set with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), PermissionMode: new("yolo")}, "usage"},
		{"refuses a focus starting with a dash with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Focus: new("-x")}, "usage"},
		{"refuses a focus starting with a dash after spaces with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Focus: new("  -x")}, "usage"},
		{"refuses an empty focus with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Focus: new("")}, "usage"},
		{"refuses a focus above MaxFocusBytes with usage", &ReviewStartArgs{Repo: "github.com/o/r", PR: new(1), Focus: new(strings.Repeat("f", MaxFocusBytes+1))}, "usage"},
		{"refuses an empty review id with usage", &ReviewRef{}, "usage"},
		{"refuses a review id with a slash with usage", &ReviewRef{Review: "a/b"}, "usage"},
		{"accepts a list without a filter", &ReviewListArgs{}, ""},
		{"accepts a list of one repo and pull request", &ReviewListArgs{Repo: new("github.com/o/r"), PR: new(3)}, ""},
		{"refuses a list repo that is not a cache id with usage", &ReviewListArgs{Repo: new("bad")}, "usage"},
		{"refuses a list pull request without a repo with usage", &ReviewListArgs{PR: new(3)}, "usage"},
		{"refuses a list limit above MaxListLimit with usage", &ReviewListArgs{Limit: MaxListLimit + 1}, "usage"},
		{"refuses a negative list limit with usage", &ReviewListArgs{Limit: -1}, "usage"},
		{"accepts a report of the comments section", &ReviewReportArgs{Review: "r1", Section: SectionComments}, ""},
		{"refuses an unknown report section with usage", &ReviewReportArgs{Review: "r1", Section: "all"}, "usage"},
		{"refuses a negative report offset with usage", &ReviewReportArgs{Review: "r1", Section: SectionComments, Offset: -1}, "usage"},
		{"accepts a fix with a suggestion", &ReviewDecideArgs{Review: "r1", Findings: []string{"correctness:F1"}, Decision: DecideFix, Suggestion: new(1)}, ""},
		{"refuses a decide with no findings with usage", &ReviewDecideArgs{Review: "r1", Decision: DecideSkip}, "usage"},
		{"refuses 201 findings with usage", &ReviewDecideArgs{Review: "r1", Findings: manyFindings, Decision: DecideSkip}, "usage"},
		{"refuses a finding without a profile with usage", &ReviewDecideArgs{Review: "r1", Findings: []string{"F1"}, Decision: DecideSkip}, "usage"},
		{"refuses an unknown decision with usage", &ReviewDecideArgs{Review: "r1", Findings: []string{"correctness:F1"}, Decision: "maybe"}, "usage"},
		{"refuses a suggestion with a comment with usage", &ReviewDecideArgs{Review: "r1", Findings: []string{"correctness:F1"}, Decision: DecideComment, Suggestion: new(1)}, "usage"},
		{"refuses a suggestion below 1 with usage", &ReviewDecideArgs{Review: "r1", Findings: []string{"correctness:F1"}, Decision: DecideFix, Suggestion: new(0)}, "usage"},
		{"refuses a note with clear with usage", &ReviewDecideArgs{Review: "r1", Findings: []string{"correctness:F1"}, Decision: DecideClear, Note: new("why")}, "usage"},
		{"refuses an empty note with usage", &ReviewDecideArgs{Review: "r1", Findings: []string{"correctness:F1"}, Decision: DecideSkip, Note: new("")}, "usage"},
		{"accepts a commented URL over https", &ReviewActedArgs{Review: "r1", Findings: []string{"correctness:F1"}, Commented: new("https://github.com/o/r/pull/3#r1")}, ""},
		{"accepts a fix sent to a workspace", &ReviewActedArgs{Review: "r1", Findings: []string{"correctness:F1"}, FixSent: new("fix-r-3")}, ""},
		{"refuses a commented URL over http with usage", &ReviewActedArgs{Review: "r1", Findings: []string{"correctness:F1"}, Commented: new("http://example.com/x")}, "usage"},
		{"refuses a commented URL with whitespace with usage", &ReviewActedArgs{Review: "r1", Findings: []string{"correctness:F1"}, Commented: new("https://example.com/a b")}, "usage"},
		{"refuses acted with both commented and fix_sent with usage", &ReviewActedArgs{Review: "r1", Findings: []string{"correctness:F1"}, Commented: new("https://example.com/x"), FixSent: new("fix-r-3")}, "usage"},
		{"refuses acted with neither commented nor fix_sent with usage", &ReviewActedArgs{Review: "r1", Findings: []string{"correctness:F1"}}, "usage"},
		{"refuses a fix_sent name with a control character with name_invalid", &ReviewActedArgs{Review: "r1", Findings: []string{"correctness:F1"}, FixSent: new("bad\nname")}, "name_invalid"},
		{"accepts review.profiles with no arguments", &ReviewProfilesArgs{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.args)
			if tc.code == "" {
				assert.NoError(t, err)
				return
			}
			assertArgsError(t, err, tc.code)
		})
	}
}
