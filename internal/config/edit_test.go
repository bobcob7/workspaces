package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"90 minutes", 90 * time.Minute, "1h30m"},
		{"60 minutes", 60 * time.Minute, "1h"},
		{"2 hours", 2 * time.Hour, "2h"},
		{"45 seconds", 45 * time.Second, "45s"},
		{"1m30s", 90 * time.Second, "1m30s"},
		{"zero", 0, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatDuration(tt.d))
		})
	}
}

func TestSettings(t *testing.T) {
	t.Run("default file gives the four fixed keys", func(t *testing.T) {
		settings, err := Settings([]byte(defaultFile))
		require.NoError(t, err)
		assert.Equal(t, []Setting{
			{Key: "defaults.host", Type: TypeString, Value: "github.com", Default: "github.com", Set: true},
			{Key: "defaults.owners", Type: TypeStrings, Value: []string{}, Default: []string{}, Set: true},
			{Key: "review.timeout", Type: TypeDuration, Value: "1h", Default: "1h", Set: false},
			{Key: "review.remove_finished", Type: TypeBool, Value: true, Default: true, Set: false},
		}, settings)
	})
	t.Run("profile rows follow the fixed keys", func(t *testing.T) {
		settings, err := Settings([]byte(defaultFile + "[review.profiles.security]\nmodel = \"opus\"\n"))
		require.NoError(t, err)
		require.Len(t, settings, 6)
		assert.Equal(t, Setting{Key: "review.profiles.security.when", Type: TypeString, Value: "", Default: "", Set: false}, settings[4])
		assert.Equal(t, Setting{Key: "review.profiles.security.model", Type: TypeString, Value: "opus", Default: "", Set: true}, settings[5])
	})
	t.Run("invalid file is ErrInvalid", func(t *testing.T) {
		_, err := Settings([]byte("[defaults]\nhost = \"a/b\"\n"))
		assert.ErrorIs(t, err, ErrInvalid)
	})
}

func TestLookup(t *testing.T) {
	t.Run("fixed key", func(t *testing.T) {
		setting, err := Lookup([]byte(defaultFile), "defaults.host")
		require.NoError(t, err)
		assert.Equal(t, Setting{Key: "defaults.host", Type: TypeString, Value: "github.com", Default: "github.com", Set: true}, setting)
	})
	t.Run("profile key without a table is unset with the default", func(t *testing.T) {
		setting, err := Lookup([]byte(defaultFile), "review.profiles.security.model")
		require.NoError(t, err)
		assert.Equal(t, Setting{Key: "review.profiles.security.model", Type: TypeString, Value: "", Default: "", Set: false}, setting)
	})
	tests := []string{"defaults.protocol", "review.profiles.Bad.when", "review.profiles.x.color"}
	for _, key := range tests {
		t.Run("unknown key "+key, func(t *testing.T) {
			_, err := Lookup([]byte(defaultFile), key)
			require.ErrorIs(t, err, ErrKeyUnknown)
			assert.ErrorContains(t, err, key)
		})
	}
}

func TestSet(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		key    string
		values []string
		want   string
	}{
		{
			name:   "replaces host in the default file",
			input:  defaultFile,
			key:    "defaults.host",
			values: []string{"b.example"},
			want:   strings.Replace(defaultFile, `host = "github.com"`, `host = "b.example"`, 1),
		},
		{
			name:   "replaces a multi-line array with one line",
			input:  "[defaults]\nowners = [\n  \"a\",\n  \"b\", # two\n]\n",
			key:    "defaults.owners",
			values: []string{"c"},
			want:   "[defaults]\nowners = [\"c\"]\n",
		},
		{
			name:   "keeps a trailing comment",
			input:  "[defaults]\nhost = \"a\" # mine\n",
			key:    "defaults.host",
			values: []string{"b"},
			want:   "[defaults]\nhost = \"b\" # mine\n",
		},
		{
			name:   "adds a key after the last key of an existing table",
			input:  "[review]\nremove_finished = false\n",
			key:    "review.timeout",
			values: []string{"90m"},
			want:   "[review]\nremove_finished = false\ntimeout = \"1h30m\"\n",
		},
		{
			name:   "adds the table at the end of the file",
			input:  defaultFile,
			key:    "review.remove_finished",
			values: []string{"false"},
			want:   defaultFile + "\n[review]\nremove_finished = false\n",
		},
		{
			name:   "adds a profile table",
			input:  defaultFile,
			key:    "review.profiles.security.model",
			values: []string{"opus"},
			want:   defaultFile + "\n[review.profiles.security]\nmodel = \"opus\"\n",
		},
		{
			name:   "keeps CRLF line endings",
			input:  "[defaults]\r\nhost = \"a\"\r\nowners = []\r\n",
			key:    "defaults.host",
			values: []string{"b"},
			want:   "[defaults]\r\nhost = \"b\"\r\nowners = []\r\n",
		},
		{
			name:   "handles a file with no trailing newline",
			input:  "[defaults]\nhost = \"a\"",
			key:    "defaults.host",
			values: []string{"b"},
			want:   "[defaults]\nhost = \"b\"",
		},
		{
			name:   "normalizes the host",
			input:  defaultFile,
			key:    "defaults.host",
			values: []string{"Gitlab.Example.COM"},
			want:   strings.Replace(defaultFile, `host = "github.com"`, `host = "gitlab.example.com"`, 1),
		},
		{
			name:   "normalizes owners, dropping repeats",
			input:  defaultFile,
			key:    "defaults.owners",
			values: []string{"A", "a", "b"},
			want:   strings.Replace(defaultFile, "owners = []", `owners = ["a", "b"]`, 1),
		},
		{
			name:   "writes a timeout in short form",
			input:  defaultFile,
			key:    "review.timeout",
			values: []string{"90m"},
			want:   defaultFile + "\n[review]\ntimeout = \"1h30m\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := Set([]byte(tt.input), tt.key, tt.values)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
			assert.True(t, changed)
		})
	}
	t.Run("same value again is not a change", func(t *testing.T) {
		got, changed, err := Set([]byte(defaultFile), "defaults.host", []string{"github.com"})
		require.NoError(t, err)
		assert.Equal(t, defaultFile, string(got))
		assert.False(t, changed)
	})
	t.Run("rejects values that break the key's rule", func(t *testing.T) {
		tests := []struct {
			name   string
			key    string
			values []string
		}{
			{"host with a scheme", "defaults.host", []string{"https://x"}},
			{"host with a space", "defaults.host", []string{"a b"}},
			{"host with a quote", "defaults.host", []string{`a"b`}},
			{"two values for host", "defaults.host", []string{"a", "b"}},
			{"negative timeout", "review.timeout", []string{"-1m"}},
			{"zero timeout", "review.timeout", []string{"0s"}},
			{"yes for remove_finished", "review.remove_finished", []string{"yes"}},
			{"sometimes for when", "review.profiles.security.when", []string{"sometimes"}},
			{"model with a space", "review.profiles.security.model", []string{"gpt 4"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, _, err := Set([]byte(defaultFile), tt.key, tt.values)
				assert.ErrorIs(t, err, ErrValueInvalid)
			})
		}
	})
	t.Run("rejects an unknown key", func(t *testing.T) {
		_, _, err := Set([]byte(defaultFile), "defaults.protocol", []string{"ssh"})
		assert.ErrorIs(t, err, ErrKeyUnknown)
	})
	t.Run("refuses a dotted key it can't edit", func(t *testing.T) {
		_, _, err := Set([]byte("[review]\nprofiles.security.when = \"auto\"\n"), "review.profiles.security.when", []string{"always"})
		assert.ErrorIs(t, err, ErrUneditable)
	})
	t.Run("refuses a multi-line string value", func(t *testing.T) {
		_, _, err := Set([]byte("[defaults]\nhost = \"\"\"\na\n\"\"\"\n"), "defaults.host", []string{"b"})
		assert.ErrorIs(t, err, ErrUneditable)
	})
}

func TestUnset(t *testing.T) {
	tests := []struct {
		name  string
		input string
		key   string
		want  string
	}{
		{
			name:  "removes a key line",
			input: "[defaults]\nhost = \"a\"\nowners = []\n",
			key:   "defaults.host",
			want:  "[defaults]\nowners = []\n",
		},
		{
			name:  "removes a multi-line array",
			input: "[defaults]\nowners = [\n  \"a\",\n]\nhost = \"b\"\n",
			key:   "defaults.owners",
			want:  "[defaults]\nhost = \"b\"\n",
		},
		{
			name:  "removes an emptied profile header with its blank line",
			input: "[defaults]\nhost = \"a\"\n\n[review.profiles.security]\nmodel = \"opus\"\n",
			key:   "review.profiles.security.model",
			want:  "[defaults]\nhost = \"a\"\n",
		},
		{
			name:  "keeps a profile table that still has when",
			input: "[review.profiles.security]\nwhen = \"auto\"\nmodel = \"opus\"\n",
			key:   "review.profiles.security.model",
			want:  "[review.profiles.security]\nwhen = \"auto\"\n",
		},
		{
			name:  "keeps comment lines in the table",
			input: "[review.profiles.security]\nwhen = \"auto\"\n# keep me\nmodel = \"opus\"\n",
			key:   "review.profiles.security.model",
			want:  "[review.profiles.security]\nwhen = \"auto\"\n# keep me\n",
		},
		{
			name:  "removes a key from a profile name Set refuses",
			input: "[defaults]\nhost = \"a\"\n\n[review.profiles.Security]\nwhen = \"auto\"\n",
			key:   "review.profiles.Security.when",
			want:  "[defaults]\nhost = \"a\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := Unset([]byte(tt.input), tt.key)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
			assert.True(t, changed)
		})
	}
	t.Run("missing key is not a change", func(t *testing.T) {
		got, changed, err := Unset([]byte(defaultFile), "review.timeout")
		require.NoError(t, err)
		assert.Equal(t, defaultFile, string(got))
		assert.False(t, changed)
	})
	t.Run("unknown key is ErrKeyUnknown", func(t *testing.T) {
		tests := []string{"defaults.protocol", "review.profiles.x.color"}
		for _, key := range tests {
			_, _, err := Unset([]byte(defaultFile), key)
			assert.ErrorIs(t, err, ErrKeyUnknown, key)
		}
	})
}

func TestWriteFile(t *testing.T) {
	t.Parallel()
	t.Run("writes a new file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, WriteFile(path, []byte("host = \"a\"\n")))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "host = \"a\"\n", string(data))
	})
	t.Run("replaces a file and keeps its mode", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
		require.NoError(t, os.Chmod(path, 0o600))
		require.NoError(t, WriteFile(path, []byte("new\n")))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new\n", string(data))
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
	t.Run("writes through a symlink", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "real.toml")
		link := filepath.Join(dir, "config.toml")
		require.NoError(t, os.WriteFile(target, []byte("old\n"), 0o644))
		require.NoError(t, os.Symlink(target, link))
		require.NoError(t, WriteFile(link, []byte("new\n")))
		info, err := os.Lstat(link)
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&os.ModeSymlink)
		data, err := os.ReadFile(target)
		require.NoError(t, err)
		assert.Equal(t, "new\n", string(data))
	})
	t.Run("leaves no temp file behind", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		require.NoError(t, WriteFile(path, []byte("a\n")))
		require.NoError(t, WriteFile(path, []byte("b\n")))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "config.toml", entries[0].Name())
	})
}
