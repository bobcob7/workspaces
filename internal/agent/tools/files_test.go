package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/gate"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	t.Run("numbers the lines", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "a.txt", "one\ntwo\n")
		res, err := readFile(&cfg, readFileArgs{Path: "a.txt"})
		require.NoError(t, err)
		assert.Equal(t, "1\tone\n2\ttwo", res.Text)
	})
	t.Run("shows the window from offset and says how many lines follow", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "a.txt", "1\n2\n3\n4\n5\n")
		res, err := readFile(&cfg, readFileArgs{Path: "a.txt", Offset: 2, Limit: 2})
		require.NoError(t, err)
		assert.Equal(t, "2\t2\n3\t3\n[file has 5 lines; use offset to read more]", res.Text)
	})
	t.Run("refuses a binary file", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "b.bin", "a\x00b")
		_, err := readFile(&cfg, readFileArgs{Path: "b.bin"})
		require.EqualError(t, err, "binary file")
	})
	t.Run("requires a path", func(t *testing.T) {
		t.Parallel()
		_, err := readFile(new(testConfig(t)), readFileArgs{})
		require.EqualError(t, err, "missing path")
	})
	t.Run("refuses a path outside the workspace", func(t *testing.T) {
		t.Parallel()
		_, err := readFile(new(testConfig(t)), readFileArgs{Path: "../x"})
		require.ErrorContains(t, err, "outside the workspace")
	})
}

func TestReadWindow(t *testing.T) {
	lines := []string{"a", "b", "c"}
	assert.Equal(t, "1\ta\n2\tb\n3\tc", readWindow(lines, 0, 0))
	assert.Equal(t, "[file has 3 lines; nothing at offset 9]", readWindow(lines, 9, 1))
}

func TestListFiles(t *testing.T) {
	t.Run("runs git ls-files in the session directory and filters by glob", func(t *testing.T) {
		cfg := testConfig(t)
		var got gate.Request
		cfg.Shell = &shellRunnerMock{RunFunc: func(_ context.Context, req gate.Request) (gate.Result, error) {
			got = req
			return gate.Result{Stdout: "b/x.go\na.md\na.go\n"}, nil
		}}
		res, err := listFiles(t.Context(), &cfg, listFilesArgs{Glob: "*.go"})
		require.NoError(t, err)
		assert.Equal(t, cfg.Dir, got.Dir)
		assert.Equal(t, "git ls-files --cached --others --exclude-standard -- '.'", got.Script)
		assert.Equal(t, "a.go\nb/x.go", res.Text)
	})
	t.Run("reports a directory outside a git repository", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.Shell = &shellRunnerMock{RunFunc: func(context.Context, gate.Request) (gate.Result, error) {
			return gate.Result{ExitCode: 128, Stderr: "fatal: not a git repository"}, nil
		}}
		_, err := listFiles(t.Context(), &cfg, listFilesArgs{})
		require.EqualError(t, err, "not a git repository")
	})
}

func TestListText(t *testing.T) {
	t.Run("reports when nothing matches", func(t *testing.T) {
		assert.Equal(t, "no files", listText("", ""))
	})
	t.Run("caps the list and says how many were left out", func(t *testing.T) {
		var out []string
		for i := range listMaxFiles + 2 {
			out = append(out, fmt.Sprintf("f%04d", i))
		}
		text := listText(strings.Join(out, "\n"), "")
		assert.True(t, strings.HasSuffix(text, "[2 more not shown]"))
	})
}

func TestSearch(t *testing.T) {
	t.Parallel()
	t.Run("prints path, line and text for each match", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "sub/a.go", "package a\nfunc Hit() {}\n")
		writeTestFile(t, cfg.Dir, ".git/config", "Hit\n")
		res, err := search(&cfg, searchArgs{Pattern: "Hit"})
		require.NoError(t, err)
		assert.Equal(t, "sub/a.go:2: func Hit() {}", res.Text)
	})
	t.Run("reports no matches", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "a.txt", "x\n")
		res, err := search(&cfg, searchArgs{Pattern: "nope"})
		require.NoError(t, err)
		assert.Equal(t, "no matches", res.Text)
	})
	t.Run("rejects an invalid pattern", func(t *testing.T) {
		t.Parallel()
		_, err := search(new(testConfig(t)), searchArgs{Pattern: "("})
		require.ErrorContains(t, err, "pattern:")
	})
	t.Run("rejects an invalid glob", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "a.txt", "x\n")
		_, err := search(&cfg, searchArgs{Pattern: "x", Glob: "["})
		require.ErrorContains(t, err, "glob:")
	})
	t.Run("stops after the match limit", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "a.txt", strings.Repeat("hit\n", searchMaxHits+5))
		res, err := search(&cfg, searchArgs{Pattern: "hit"})
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(res.Text, "[stopped after 100 matches]"))
	})
	t.Run("does not say it stopped when there are exactly the limit", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "a.txt", strings.Repeat("hit\n", searchMaxHits))
		res, err := search(&cfg, searchArgs{Pattern: "hit"})
		require.NoError(t, err)
		assert.NotContains(t, res.Text, "[stopped")
	})
}

func TestWriteFileTool(t *testing.T) {
	cfg := testConfig(t)
	write := writeFileTool(&cfg)
	t.Run("requires a path", func(t *testing.T) {
		_, err := write.Call(t.Context(), `{"content":"x"}`)
		require.EqualError(t, err, "missing path")
	})
	t.Run("rejects content that is not a string", func(t *testing.T) {
		_, err := write.Call(t.Context(), `{"path":"a.txt","content":3}`)
		require.ErrorContains(t, err, "arguments:")
	})
}

func TestEditFileTool(t *testing.T) {
	cfg := testConfig(t)
	edit := editFileTool(&cfg)
	t.Run("requires old", func(t *testing.T) {
		_, err := edit.Call(t.Context(), `{"path":"a.txt","new":"y"}`)
		require.EqualError(t, err, "missing old")
	})
	t.Run("rejects all that is not a boolean", func(t *testing.T) {
		_, err := edit.Call(t.Context(), `{"path":"a.txt","old":"x","new":"y","all":"yes"}`)
		require.ErrorContains(t, err, "arguments:")
	})
}

func TestWriteFile(t *testing.T) {
	t.Parallel()
	t.Run("creates parent directories and the file", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		res, err := writeFile(&cfg, writeFileArgs{Path: "new/dir/f.txt", Content: "hello"})
		require.NoError(t, err)
		assert.Equal(t, "wrote 5 bytes to new/dir/f.txt", res.Text)
		data, err := os.ReadFile(filepath.Join(cfg.Dir, "new", "dir", "f.txt"))
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
	})
	t.Run("refuses writes under .git", func(t *testing.T) {
		t.Parallel()
		_, err := writeFile(new(testConfig(t)), writeFileArgs{Path: ".git/HEAD", Content: "x"})
		require.ErrorContains(t, err, "under .git")
	})
	t.Run("requires a path", func(t *testing.T) {
		t.Parallel()
		_, err := writeFile(new(testConfig(t)), writeFileArgs{})
		require.EqualError(t, err, "missing path")
	})
}

func TestEditFile(t *testing.T) {
	t.Parallel()
	t.Run("replaces a single occurrence", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		p := writeTestFile(t, cfg.Dir, "a.txt", "alpha beta")
		res, err := editFile(&cfg, editFileArgs{Path: "a.txt", Old: "alpha", New: "gamma"})
		require.NoError(t, err)
		assert.Equal(t, "replaced 1 occurrence(s) in a.txt", res.Text)
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		assert.Equal(t, "gamma beta", string(data))
	})
	t.Run("refuses an ambiguous old without all", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "a.txt", "x x")
		_, err := editFile(&cfg, editFileArgs{Path: "a.txt", Old: "x", New: "y"})
		require.EqualError(t, err, "old occurs 2 times; add context or set all")
	})
	t.Run("replaces every occurrence with all", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		p := writeTestFile(t, cfg.Dir, "a.txt", "x x")
		res, err := editFile(&cfg, editFileArgs{Path: "a.txt", Old: "x", New: "y", All: true})
		require.NoError(t, err)
		assert.Equal(t, "replaced 2 occurrence(s) in a.txt", res.Text)
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		assert.Equal(t, "y y", string(data))
	})
	t.Run("refuses old that does not occur", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		writeTestFile(t, cfg.Dir, "a.txt", "x")
		_, err := editFile(&cfg, editFileArgs{Path: "a.txt", Old: "zzz", New: "y"})
		require.EqualError(t, err, "old not found in a.txt")
	})
	t.Run("requires old", func(t *testing.T) {
		t.Parallel()
		_, err := editFile(new(testConfig(t)), editFileArgs{Path: "a.txt"})
		require.EqualError(t, err, "missing old")
	})
}

func TestReplaceText(t *testing.T) {
	args := editFileArgs{Path: "f", Old: "a", New: "b"}
	got, n, err := replaceText("a a", args)
	require.Error(t, err)
	assert.Empty(t, got)
	assert.Zero(t, n)
	args.All = true
	got, n, err = replaceText("a a", args)
	require.NoError(t, err)
	assert.Equal(t, "b b", got)
	assert.Equal(t, 2, n)
}

func TestFileToolsCallDecodesArguments(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	writeTestFile(t, cfg.Dir, "a.txt", "hi\n")
	read := toolByName(t, New(cfg), "read_file")
	res, err := read.Call(t.Context(), `{"path":"a.txt"}`)
	require.NoError(t, err)
	assert.Equal(t, "1\thi", res.Text)
	_, err = read.Call(t.Context(), `{"offset":2}`)
	require.EqualError(t, err, "missing path")
	_, err = read.Call(t.Context(), `{`)
	require.ErrorContains(t, err, "arguments:")
}

func TestMatchesGlob(t *testing.T) {
	assert.True(t, matchesGlob("", "anything"))
	assert.True(t, matchesGlob("*.go", "pkg/a.go"))
	assert.False(t, matchesGlob("*.md", "pkg/a.go"))
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, `'a'\''b'`, shellQuote("a'b"))
}
