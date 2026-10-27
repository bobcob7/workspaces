package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const commit = "3f2a9c1e3b7d5f0c8a1e2d4b6c8f0a2e4d6b8c0f"

// adrExample is ADR 0039's example record.
const adrExample = `version = 1
repo = "github.com/bobcob7/buff-bear"
ref = "3f2a9c1e3b7d5f0c8a1e2d4b6c8f0a2e4d6b8c0f"
requester = "fix-login-d8efb24f"
started_at = 2026-10-26T18:00:00Z
profiles = [
  { name = "security", model = "opus", auto = false },
  { name = "go", model = "", auto = true },
]
[pr]
number = 42
url = "https://github.com/bobcob7/buff-bear/pull/42"
base = "main"
head_branch = "feat/login"
cross_repository = false
`

func sample() Record {
	return Record{
		Repo:      "github.com/bobcob7/buff-bear",
		Ref:       commit,
		Requester: "fix-login-d8efb24f",
		StartedAt: time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC),
		Profiles:  []Profile{{Name: "security", Model: "opus"}, {Name: "go", Auto: true}},
		PR:        &PR{Number: 42, URL: "https://github.com/bobcob7/buff-bear/pull/42", Base: "main", HeadBranch: "feat/login"},
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestPath(t *testing.T) {
	state := filepath.Join("c", "state")
	assert.Equal(t, filepath.Join(state, "rev-1", "review.toml"), Path(state, "rev-1"))
}

func TestRecord_HasProfile(t *testing.T) {
	rec := sample()
	assert.True(t, rec.HasProfile("go"))
	assert.False(t, rec.HasProfile("tests"))
}

func TestRecord_ProfileNames(t *testing.T) {
	rec := sample()
	assert.Equal(t, []string{"security", "go"}, rec.ProfileNames())
}

func TestRead(t *testing.T) {
	t.Parallel()
	t.Run("reads ADR 0039's example", func(t *testing.T) {
		t.Parallel()
		rec, err := Read(writeFile(t, adrExample))
		require.NoError(t, err)
		want := sample()
		want.Version = 1
		assert.Equal(t, want, rec)
	})
	t.Run("without a pr table, PR is nil", func(t *testing.T) {
		t.Parallel()
		content, _, _ := strings.Cut(adrExample, "[pr]")
		rec, err := Read(writeFile(t, content))
		require.NoError(t, err)
		assert.Nil(t, rec.PR)
	})
	t.Run("a missing file is ErrNotFound", func(t *testing.T) {
		t.Parallel()
		_, err := Read(filepath.Join(t.TempDir(), FileName))
		require.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("a directory is a read error", func(t *testing.T) {
		t.Parallel()
		_, err := Read(t.TempDir())
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNotFound)
	})
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"a newer version", strings.Replace(adrExample, "version = 1", "version = 2", 1), "written by a newer ws (version 2; this ws reads up to 1); upgrade ws"},
		{"no version", strings.Replace(adrExample, "version = 1\n", "", 1), "unsupported version 0"},
		{"an unknown key", adrExample + "extra = true\n", "unknown key pr.extra"},
		{"not TOML", "version = = 1", "parsing"},
		{"no repo", strings.Replace(adrExample, `repo = "github.com/bobcob7/buff-bear"`, `repo = ""`, 1), "no repo"},
		{"a short ref", strings.Replace(adrExample, commit, "3f2a9c1", 1), `ref "3f2a9c1" isn't a full commit`},
		{"no profiles", "version = 1\nrepo = \"r\"\nref = \"" + commit + "\"\nprofiles = []\n", "no profiles"},
		{"a bad profile name", strings.Replace(adrExample, `name = "go"`, `name = "Go!"`, 1), `profile name "Go!" is invalid`},
		{"a profile twice", strings.Replace(adrExample, `name = "go"`, `name = "security"`, 1), `profile "security" listed twice`},
		{"a bad pr number", strings.Replace(adrExample, "number = 42", "number = 0", 1), "pr number 0 is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Read(writeFile(t, tc.content))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestWrite(t *testing.T) {
	t.Parallel()
	t.Run("round trips through Read as version 1, in UTC seconds", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), FileName)
		rec := sample()
		rec.StartedAt = time.Date(2026, 10, 26, 13, 0, 0, 500, time.FixedZone("CDT", -5*3600))
		require.NoError(t, Write(path, &rec))
		got, err := Read(path)
		require.NoError(t, err)
		assert.Equal(t, 1, got.Version)
		assert.Equal(t, time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC), got.StartedAt)
		assert.Equal(t, rec.Profiles, got.Profiles)
		assert.Equal(t, rec.PR, got.PR)
		assert.Equal(t, 0, rec.Version, "the caller's record is left alone")
	})
	t.Run("leaves no temp file behind", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		rec := sample()
		rec.PR = nil
		require.NoError(t, Write(filepath.Join(dir, FileName), &rec))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		data, err := os.ReadFile(filepath.Join(dir, FileName))
		require.NoError(t, err)
		assert.NotContains(t, string(data), "[pr]")
	})
	t.Run("refuses a record it couldn't read back", func(t *testing.T) {
		t.Parallel()
		rec := sample()
		rec.Ref = "main"
		err := Write(filepath.Join(t.TempDir(), FileName), &rec)
		require.ErrorContains(t, err, "isn't a full commit")
	})
	t.Run("fails in a missing directory", func(t *testing.T) {
		t.Parallel()
		rec := sample()
		require.Error(t, Write(filepath.Join(t.TempDir(), "missing", FileName), &rec))
	})
}

func TestRecord_JSON(t *testing.T) {
	rec := sample()
	rec.PR = nil
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"pr":null`, "a field with no value is null, never omitted (ADR 0016)")
	assert.Contains(t, string(data), `"started_at":"2026-10-26T18:00:00Z"`)
}
