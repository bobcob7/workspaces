package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/gate"
)

const (
	readMaxLines   = 400
	listMaxFiles   = 500
	searchMaxHits  = 100
	searchMaxWidth = 200
	binarySniff    = 8 << 10
	shellTimeout   = 120 * time.Second
)

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func readFileTool(cfg *Config) Tool {
	return Tool{
		Name:        "read_file",
		Description: "Read a text file as numbered lines, at most 400 per call.",
		Parameters: objectSchema(map[string]map[string]any{
			"path":   field("string", "file path, relative to the session directory"),
			"offset": field("integer", "first line to show, 1-based"),
			"limit":  field("integer", "lines to show, at most 400"),
		}, "path"),
		Call: typed(func(_ context.Context, args readFileArgs) (Result, error) {
			return readFile(cfg, args)
		}),
	}
}

func readFile(cfg *Config, args readFileArgs) (Result, error) {
	if args.Path == "" {
		return Result{}, errors.New("missing path")
	}
	resolved, err := CheckPath(cfg.Root, cfg.Dir, args.Path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", args.Path, err)
	}
	if isBinary(data) {
		return Result{}, errors.New("binary file")
	}
	return Result{Text: readWindow(splitLines(string(data)), args.Offset, args.Limit)}, nil
}

// readWindow numbers the lines from offset (1-based), at most limit of
// them, and says how many the file has when more follow.
func readWindow(lines []string, offset, limit int) string {
	start := max(offset, 1) - 1
	if limit <= 0 || limit > readMaxLines {
		limit = readMaxLines
	}
	if start >= len(lines) {
		return fmt.Sprintf("[file has %d lines; nothing at offset %d]", len(lines), offset)
	}
	end := min(start+limit, len(lines))
	out := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		out = append(out, fmt.Sprintf("%d\t%s", i+1, lines[i]))
	}
	if end < len(lines) {
		out = append(out, fmt.Sprintf("[file has %d lines; use offset to read more]", len(lines)))
	}
	return strings.Join(out, "\n")
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), binarySniff)], 0) >= 0
}

type listFilesArgs struct {
	Path string `json:"path"`
	Glob string `json:"glob"`
}

func listFilesTool(cfg *Config) Tool {
	return Tool{
		Name:        "list_files",
		Description: "List tracked and untracked, non-ignored files under a path, optionally filtered by a glob.",
		Parameters: objectSchema(map[string]map[string]any{
			"path": field("string", "directory to list, relative to the session directory; default ."),
			"glob": field("string", "pattern matched against each path and its base name"),
		}),
		Call: typed(func(ctx context.Context, args listFilesArgs) (Result, error) {
			return listFiles(ctx, cfg, args)
		}),
	}
}

func listFiles(ctx context.Context, cfg *Config, args listFilesArgs) (Result, error) {
	dir := args.Path
	if dir == "" {
		dir = "."
	}
	if _, err := CheckPath(cfg.Root, cfg.Dir, dir); err != nil {
		return Result{}, err
	}
	if _, err := path.Match(args.Glob, ""); err != nil {
		return Result{}, fmt.Errorf("glob: %w", err)
	}
	res, err := cfg.Shell.Run(ctx, gate.Request{
		Script:  "git ls-files --cached --others --exclude-standard -- " + shellQuote(dir),
		Dir:     cfg.Dir,
		Timeout: shellTimeout,
	})
	if err != nil {
		return Result{}, err
	}
	if res.ExitCode != 0 {
		return Result{}, gitListError(res)
	}
	return Result{Text: listText(res.Stdout, args.Glob)}, nil
}

func gitListError(res gate.Result) error {
	if strings.Contains(res.Stderr, "not a git repository") {
		return errors.New("not a git repository")
	}
	return fmt.Errorf("git ls-files exited %d: %s", res.ExitCode, res.Stderr)
}

// listText keeps the paths of out that match glob, sorted, at most
// listMaxFiles of them.
func listText(out, glob string) string {
	var matched []string
	for line := range strings.SplitSeq(out, "\n") {
		if line != "" && matchesGlob(glob, line) {
			matched = append(matched, line)
		}
	}
	slices.Sort(matched)
	if len(matched) == 0 {
		return "no files"
	}
	if len(matched) <= listMaxFiles {
		return strings.Join(matched, "\n")
	}
	return fmt.Sprintf("%s\n[%d more not shown]", strings.Join(matched[:listMaxFiles], "\n"), len(matched)-listMaxFiles)
}

// matchesGlob reports whether glob is empty or matches p or its base name.
func matchesGlob(glob, p string) bool {
	if glob == "" {
		return true
	}
	if ok, _ := path.Match(glob, p); ok {
		return true
	}
	ok, _ := path.Match(glob, path.Base(p))
	return ok
}

// shellQuote quotes s for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type searchArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Glob    string `json:"glob"`
}

func searchTool(cfg *Config) Tool {
	return Tool{
		Name:        "search",
		Description: "Search text files for a Go regular expression, printing path:line: text.",
		Parameters: objectSchema(map[string]map[string]any{
			"pattern": field("string", "Go regular expression"),
			"path":    field("string", "file or directory, relative to the session directory; default ."),
			"glob":    field("string", "pattern matched against each path and its base name"),
		}, "pattern"),
		Call: typed(func(_ context.Context, args searchArgs) (Result, error) {
			return search(cfg, args)
		}),
	}
}

func search(cfg *Config, args searchArgs) (Result, error) {
	if args.Pattern == "" {
		return Result{}, errors.New("missing pattern")
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return Result{}, fmt.Errorf("pattern: %w", err)
	}
	start, err := CheckPath(cfg.Root, cfg.Dir, orDot(args.Path))
	if err != nil {
		return Result{}, err
	}
	base, err := filepath.EvalSymlinks(cfg.Dir)
	if err != nil {
		base = cfg.Dir
	}
	s := &searcher{re: re, start: start, base: base, glob: args.Glob}
	if err := filepath.WalkDir(start, s.visit); err != nil {
		return Result{}, fmt.Errorf("searching %s: %w", args.Path, err)
	}
	return Result{Text: s.text()}, nil
}

func orDot(p string) string {
	if p == "" {
		return "."
	}
	return p
}

// searcher collects matches across the files it visits.
type searcher struct {
	re      *regexp.Regexp
	start   string
	base    string
	glob    string
	matches []string
}

func (s *searcher) visit(p string, d fs.DirEntry, err error) error {
	if err != nil {
		if p == s.start {
			return err
		}
		return nil
	}
	if d.IsDir() && d.Name() == ".git" {
		return filepath.SkipDir
	}
	rel, err := filepath.Rel(s.base, p)
	if err != nil {
		return err
	}
	if !d.Type().IsRegular() || !matchesGlob(s.glob, rel) {
		return nil
	}
	s.scanFile(p, rel)
	if len(s.matches) >= searchMaxHits {
		return fs.SkipAll
	}
	return nil
}

func (s *searcher) scanFile(p, rel string) {
	data, err := os.ReadFile(p)
	if err != nil || isBinary(data) {
		return
	}
	for i, line := range splitLines(string(data)) {
		if !s.re.MatchString(line) {
			continue
		}
		s.matches = append(s.matches, fmt.Sprintf("%s:%d: %s", rel, i+1, cutWidth(line)))
		if len(s.matches) >= searchMaxHits {
			return
		}
	}
}

func cutWidth(line string) string {
	if len(line) <= searchMaxWidth {
		return line
	}
	return strings.ToValidUTF8(line[:searchMaxWidth], "")
}

func (s *searcher) text() string {
	switch {
	case len(s.matches) == 0:
		return "no matches"
	case len(s.matches) >= searchMaxHits:
		return strings.Join(s.matches, "\n") + fmt.Sprintf("\n[stopped after %d matches]", searchMaxHits)
	}
	return strings.Join(s.matches, "\n")
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func writeFileTool(cfg *Config) Tool {
	return Tool{
		Name:        "write_file",
		Description: "Create or replace a file with the given content, making its parent directories.",
		Parameters: objectSchema(map[string]map[string]any{
			"path":    field("string", "file path, relative to the session directory"),
			"content": field("string", "the whole new content of the file"),
		}, "path", "content"),
		Call: typed(func(_ context.Context, args writeFileArgs) (Result, error) {
			return writeFile(cfg, args)
		}),
	}
}

func writeFile(cfg *Config, args writeFileArgs) (Result, error) {
	if args.Path == "" {
		return Result{}, errors.New("missing path")
	}
	target, err := CheckPath(cfg.Root, cfg.Dir, args.Path)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // parents are 0755 by spec (ADR 0065)
		return Result{}, fmt.Errorf("creating directories for %s: %w", args.Path, err)
	}
	if err := os.WriteFile(target, []byte(args.Content), 0o644); err != nil { //nolint:gosec // model-written files are 0644 by spec (ADR 0065)
		return Result{}, fmt.Errorf("writing %s: %w", args.Path, err)
	}
	return Result{Text: fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path)}, nil
}

type editFileArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
	All  bool   `json:"all"`
}

func editFileTool(cfg *Config) Tool {
	return Tool{
		Name:        "edit_file",
		Description: "Replace an exact text in a file; old must occur once unless all is set.",
		Parameters: objectSchema(map[string]map[string]any{
			"path": field("string", "file path, relative to the session directory"),
			"old":  field("string", "exact text to replace"),
			"new":  field("string", "replacement text"),
			"all":  field("boolean", "replace every occurrence"),
		}, "path", "old", "new"),
		Call: typed(func(_ context.Context, args editFileArgs) (Result, error) {
			return editFile(cfg, args)
		}),
	}
}

func editFile(cfg *Config, args editFileArgs) (Result, error) {
	if args.Path == "" {
		return Result{}, errors.New("missing path")
	}
	if args.Old == "" {
		return Result{}, errors.New("missing old")
	}
	target, err := CheckPath(cfg.Root, cfg.Dir, args.Path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", args.Path, err)
	}
	text, count, err := replaceText(string(data), args)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(target, []byte(text), 0o644); err != nil { //nolint:gosec // model-written files are 0644 by spec (ADR 0065)
		return Result{}, fmt.Errorf("writing %s: %w", args.Path, err)
	}
	return Result{Text: fmt.Sprintf("replaced %d occurrence(s) in %s", count, args.Path)}, nil
}

// replaceText applies the edit to text and returns it with the number of
// occurrences replaced.
func replaceText(text string, args editFileArgs) (string, int, error) {
	count := strings.Count(text, args.Old)
	switch {
	case count == 0:
		return "", 0, fmt.Errorf("old not found in %s", args.Path)
	case count > 1 && !args.All:
		return "", 0, fmt.Errorf("old occurs %d times; add context or set all", count)
	case args.All:
		return strings.ReplaceAll(text, args.Old, args.New), count, nil
	}
	return strings.Replace(text, args.Old, args.New, 1), 1, nil
}
