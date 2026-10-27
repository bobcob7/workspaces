package start

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/review/profile"
	"github.com/bobcob7/workspaces/internal/review/record"
)

// testProfiles mirror the built-ins' when and match.
func testProfiles() []profile.Profile {
	return []profile.Profile{
		{Name: "architecture", When: profile.WhenAlways},
		{Name: "compatibility", When: profile.WhenOff},
		{Name: "go", When: profile.WhenAuto, Match: []string{"*.go", "go.mod"}},
		{Name: "security", When: profile.WhenAlways},
		{Name: "typescript", When: profile.WhenAuto, Match: []string{"*.ts", "*.tsx"}},
	}
}

func TestParseChoice(t *testing.T) {
	t.Run("a name", func(t *testing.T) {
		c, err := ParseChoice("security")
		require.NoError(t, err)
		assert.Equal(t, Choice{Name: "security"}, c)
	})
	t.Run("a name and a model", func(t *testing.T) {
		c, err := ParseChoice("security:claude-opus-5-5")
		require.NoError(t, err)
		assert.Equal(t, Choice{Name: "security", Model: "claude-opus-5-5"}, c)
	})
	t.Run("rejects malformed values", func(t *testing.T) {
		for _, s := range []string{"", ":opus", "Security", "go:", "../x"} {
			_, err := ParseChoice(s)
			assert.ErrorIs(t, err, ErrInvalidChoice, s)
		}
	})
}

func TestChoose(t *testing.T) {
	t.Run("takes always profiles and matching auto ones", func(t *testing.T) {
		got, err := Choose(&Request{Profiles: testProfiles(), Files: []string{"cmd/ws/main.go", "README.md"}})
		require.NoError(t, err)
		assert.Equal(t, []record.Profile{{Name: "architecture"}, {Name: "go", Auto: true}, {Name: "security"}}, got)
	})
	t.Run("config overrides when and sets models", func(t *testing.T) {
		got, err := Choose(&Request{
			Profiles: testProfiles(),
			Config: map[string]config.ReviewProfile{
				"architecture":  {When: "off"},
				"compatibility": {When: "always", Model: "haiku"},
				"security":      {Model: "opus"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, []record.Profile{{Name: "compatibility", Model: "haiku"}, {Name: "security", Model: "opus"}}, got)
	})
	t.Run("flags replace the configured set", func(t *testing.T) {
		got, err := Choose(&Request{
			Profiles: testProfiles(),
			Config:   map[string]config.ReviewProfile{"security": {Model: "opus"}, "go": {Model: "sonnet"}},
			Explicit: []Choice{{Name: "security"}, {Name: "compatibility", Model: "haiku"}},
			Auto:     []Choice{{Name: "go"}, {Name: "typescript", Model: "fable"}},
			Files:    []string{"web/app.tsx"},
		})
		require.NoError(t, err)
		assert.Equal(t, []record.Profile{
			{Name: "security", Model: "opus"},
			{Name: "compatibility", Model: "haiku"},
			{Name: "typescript", Model: "fable", Auto: true},
		}, got)
	})
	t.Run("an unknown flag profile is ErrUnknownProfile", func(t *testing.T) {
		_, err := Choose(&Request{Profiles: testProfiles(), Explicit: []Choice{{Name: "nope"}}})
		assert.ErrorIs(t, err, ErrUnknownProfile)
	})
	t.Run("an unknown config profile is ErrUnknownProfile", func(t *testing.T) {
		_, err := Choose(&Request{Profiles: testProfiles(), Config: map[string]config.ReviewProfile{"nope": {}}})
		require.ErrorIs(t, err, ErrUnknownProfile)
		assert.ErrorContains(t, err, "[review.profiles.nope]")
	})
	t.Run("a profile chosen twice is ErrInvalidChoice", func(t *testing.T) {
		_, err := Choose(&Request{Profiles: testProfiles(), Explicit: []Choice{{Name: "go"}}, Auto: []Choice{{Name: "go"}}})
		assert.ErrorIs(t, err, ErrInvalidChoice)
	})
	t.Run("--auto without match patterns is ErrInvalidChoice", func(t *testing.T) {
		_, err := Choose(&Request{Profiles: testProfiles(), Auto: []Choice{{Name: "security"}}})
		assert.ErrorIs(t, err, ErrInvalidChoice)
	})
	t.Run("nothing chosen is ErrNoProfiles", func(t *testing.T) {
		_, err := Choose(&Request{Profiles: testProfiles(), Auto: []Choice{{Name: "go"}}, Files: []string{"README.md"}})
		assert.ErrorIs(t, err, ErrNoProfiles)
	})
}

func TestSubagentName(t *testing.T) {
	assert.Equal(t, "review-security", SubagentName("security"))
}

func TestAgents(t *testing.T) {
	t.Run("one entry per profile, model only when set", func(t *testing.T) {
		out, err := Agents([]Subagent{
			{Profile: record.Profile{Name: "security", Model: "opus"}, Prompt: "prime security"},
			{Profile: record.Profile{Name: "go", Auto: true}, Prompt: "prime go"},
		})
		require.NoError(t, err)
		var got map[string]map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		assert.Equal(t, map[string]map[string]any{
			"review-security": {
				"description": "Reviews this commit as the security profile and submits with ws review submit",
				"prompt":      "prime security",
				"tools":       []any{"Read", "Grep", "Glob", "Bash"},
				"model":       "opus",
			},
			"review-go": {
				"description": "Reviews this commit as the go profile and submits with ws review submit",
				"prompt":      "prime go",
				"tools":       []any{"Read", "Grep", "Glob", "Bash"},
			},
		}, got)
	})
}

func TestName(t *testing.T) {
	commit := "3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90"
	t.Run("a pull request", func(t *testing.T) {
		assert.Equal(t, "review-buff-bear-42-3f2a9c1", Name("buff-bear", 42, commit))
	})
	t.Run("a commit", func(t *testing.T) {
		assert.Equal(t, "review-buff-bear-3f2a9c1", Name("buff-bear", 0, commit))
	})
	t.Run("sanitizes the repo name", func(t *testing.T) {
		assert.Equal(t, "review-my-repo-name-7-3f2a9c1", Name("My_Repo.Name", 7, commit))
	})
	t.Run("shortens a long repo name, keeping the number and commit", func(t *testing.T) {
		name := Name(strings.Repeat("a", 60), 1, commit)
		assert.Len(t, name, 55)
		assert.Equal(t, "review-"+strings.Repeat("a", 38)+"-1-3f2a9c1", name)
	})
	t.Run("drops a dash left by shortening, as the desktop app does", func(t *testing.T) {
		assert.Equal(t, "review-"+strings.Repeat("a", 37)+"-1-3f2a9c1", Name(strings.Repeat("a", 37)+"-b", 1, commit))
	})
}
