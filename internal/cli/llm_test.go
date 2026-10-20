package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errTest
}

// commandTopics maps the guide topic of every command in ws's tree, ""
// for the root, to its command. Cobra's own help and completion commands
// aren't ws's, and have no guide.
func commandTopics(root *cobra.Command) map[string]*cobra.Command {
	topics := map[string]*cobra.Command{}
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Hidden || cmd.Name() == "help" || cmd.Name() == "completion" {
			return
		}
		topics[strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), root.Name()))] = cmd
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return topics
}

// mentions reports whether text names the flag as a whole word, so --all
// doesn't count for --all-repos.
func mentions(text, flag string) bool {
	return regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(flag) + `($|[^\w-])`).MatchString(text)
}

// isOperation reports whether cmd does something of its own: it runs and
// isn't a group, such as ws cache, which only prints its help.
func isOperation(cmd *cobra.Command) bool {
	return cmd.Runnable() && !cmd.HasSubCommands()
}

// supportsJSON reports whether cmd prints a result that --json applies to:
// it's an operation, and its PreRunE doesn't refuse --json.
func supportsJSON(app *App, cmd *cobra.Command) bool {
	if !isOperation(cmd) {
		return false
	}
	app.global.json = true
	defer func() { app.global.json = false }()
	return cmd.PreRunE == nil || cmd.PreRunE(cmd, nil) == nil
}

// declaredErrorCodes reads every errorCode constant from the package's
// source, so a new code can't be left out of the overview. Go can't list a
// type's constants at run time, and a hand-kept list could miss one.
func declaredErrorCodes(t *testing.T) []errorCode {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	var codes []errorCode
	for _, name := range files {
		if !strings.HasSuffix(name, "_test.go") {
			codes = append(codes, fileErrorCodes(t, name)...)
		}
	}
	require.NotEmpty(t, codes)
	return codes
}

// fileErrorCodes is every constant declared with type errorCode in one
// source file.
func fileErrorCodes(t *testing.T, name string) []errorCode {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	require.NoError(t, err)
	var codes []errorCode
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		if typ, ok := spec.Type.(*ast.Ident); !ok || typ.Name != "errorCode" {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.BasicLit)
			require.True(t, ok, "errorCode constants are string literals")
			s, err := strconv.Unquote(lit.Value)
			require.NoError(t, err)
			codes = append(codes, errorCode(s))
		}
		return true
	})
	return codes
}

// section is the text of a guide's "## <heading>" section, up to the next
// heading of that level.
func section(text, heading string) string {
	_, after, ok := strings.Cut(text, "\n## "+heading+"\n")
	if !ok {
		return ""
	}
	before, _, _ := strings.Cut(after, "\n## ")
	return before
}

func mustGuide(t *testing.T, topic string) string {
	t.Helper()
	text, err := readGuide(topic)
	require.NoError(t, err)
	return text
}

func TestGuideTopics(t *testing.T) {
	app, _, _ := newTestApp()
	commands := commandTopics(app.newRootCmd())
	t.Run("every command has a guide", func(t *testing.T) {
		for topic := range commands {
			assert.Contains(t, guideTopics(), topic, "ws %s isn't in guideTopics", topic)
			_, err := readGuide(topic)
			assert.NoError(t, err, "ws %s has no guide", topic)
		}
	})
	t.Run("every guide is a command's", func(t *testing.T) {
		for _, topic := range guideTopics() {
			assert.Contains(t, commands, topic, "the guide %s has no command", guideFile(topic))
		}
		entries, err := guideFiles.ReadDir("guides")
		require.NoError(t, err)
		files := make([]string, 0, len(guideTopics()))
		for _, topic := range guideTopics() {
			files = append(files, guideFile(topic))
		}
		for _, e := range entries {
			assert.Contains(t, files, "guides/"+e.Name(), "no topic prints %s", e.Name())
		}
	})
	t.Run("topics are listed once", func(t *testing.T) {
		topics := guideTopics()
		slices.Sort(topics)
		assert.Len(t, slices.Compact(topics), len(guideTopics()))
	})
	t.Run("every flag is in its command's guide", func(t *testing.T) {
		for topic, cmd := range commands {
			text := mustGuide(t, topic)
			cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
				assert.True(t, mentions(text, "--"+f.Name), "the guide for ws %s doesn't mention --%s", topic, f.Name)
				if f.Shorthand != "" {
					assert.True(t, mentions(text, "-"+f.Shorthand), "the guide for ws %s doesn't mention -%s", topic, f.Shorthand)
				}
			})
		}
	})
	t.Run("commands with a result document their JSON", func(t *testing.T) {
		for topic, cmd := range commands {
			if !supportsJSON(app, cmd) {
				continue
			}
			text := mustGuide(t, topic)
			assert.True(t, mentions(text, "--json"), "the guide for ws %s doesn't mention --json", topic)
			assert.Contains(t, section(text, "JSON output"), "```json", "the guide for ws %s has no JSON example", topic)
		}
	})
	t.Run("commands without a result say --json is refused", func(t *testing.T) {
		for topic, cmd := range commands {
			if !isOperation(cmd) || supportsJSON(app, cmd) {
				continue
			}
			assert.Contains(t, mustGuide(t, topic), "`--json` is refused", "ws %s", topic)
		}
	})
	t.Run("every command guide has its heading, synopsis, exit codes, and examples", func(t *testing.T) {
		for _, topic := range guideTopics()[1:] {
			text := mustGuide(t, topic)
			assert.True(t, strings.HasPrefix(text, "# ws "+topic+"\n"), "the guide for ws %s starts with its name", topic)
			assert.Contains(t, text, "\nws "+topic, "the guide for ws %s has a synopsis", topic)
			assert.NotEmpty(t, section(text, "Exit codes"), "the guide for ws %s has exit codes", topic)
			if topic != "cache" {
				assert.NotEmpty(t, section(text, "Examples"), "the guide for ws %s has examples", topic)
			}
		}
	})
	t.Run("the overview points to every guide", func(t *testing.T) {
		overview := mustGuide(t, "")
		for _, topic := range guideTopics()[1:] {
			assert.Contains(t, overview, "`ws llm "+topic+"`", "the overview doesn't point to ws llm %s", topic)
		}
	})
	t.Run("the overview lists every error code under its exit code", func(t *testing.T) {
		rows := strings.Split(section(mustGuide(t, ""), "Output and exit codes"), "\n")
		for _, code := range declaredErrorCodes(t) {
			prefix := fmt.Sprintf("| %d ", code.exitCode())
			found := slices.ContainsFunc(rows, func(row string) bool {
				return strings.HasPrefix(row, prefix) && strings.Contains(row, "`"+string(code)+"`")
			})
			assert.True(t, found, "%s isn't in the overview's row for exit %d", code, code.exitCode())
		}
	})
	t.Run("exit code sections name only real error codes", func(t *testing.T) {
		codes := declaredErrorCodes(t)
		codeToken := regexp.MustCompile("`([a-z]+(?:_[a-z]+)+)`")
		for _, topic := range guideTopics()[1:] {
			for _, m := range codeToken.FindAllStringSubmatch(section(mustGuide(t, topic), "Exit codes"), -1) {
				assert.Contains(t, codes, errorCode(m[1]), "the guide for ws %s names an unknown error code", topic)
			}
		}
	})
}

func TestApp_newLLMCmd(t *testing.T) {
	app, stdout, _ := newTestApp()
	cmd := app.newLLMCmd()
	t.Run("has --all", func(t *testing.T) {
		assertFlag(t, cmd, "all", "false")
	})
	t.Run("refuses --json", func(t *testing.T) {
		app.global.json = true
		t.Cleanup(func() { app.global.json = false })
		requireUsageError(t, cmd.PreRunE(cmd, nil))
	})
	t.Run("refuses an unknown command", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"nope"}))
	})
	t.Run("prints the overview", func(t *testing.T) {
		stdout.Reset()
		require.NoError(t, cmd.RunE(cmd, nil))
		assert.Equal(t, mustGuide(t, ""), stdout.String())
	})
	t.Run("never loads the config", func(t *testing.T) {
		app, stdout, stderr := newTestApp()
		configs := &configLoaderMock{}
		app.configs = configs
		require.Equal(t, exitOK, app.Run(t.Context(), []string{"llm", "cache", "search"}), stderr.String())
		assert.Equal(t, mustGuide(t, "cache search"), stdout.String())
		assert.Empty(t, configs.LoadCalls())
	})
	t.Run("an unknown command exits 2 with nothing on stdout", func(t *testing.T) {
		app, stdout, stderr := newTestApp()
		require.Equal(t, exitUsage, app.Run(t.Context(), []string{"llm", "nope"}))
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), `no guide for "nope"`)
	})
}

func TestCheckGuideArgs(t *testing.T) {
	cmd := newTestTree()
	t.Run("accepts no command", func(t *testing.T) {
		assert.NoError(t, checkGuideArgs(cmd, false, nil))
	})
	t.Run("accepts --all alone", func(t *testing.T) {
		assert.NoError(t, checkGuideArgs(cmd, true, nil))
	})
	t.Run("accepts a command path", func(t *testing.T) {
		assert.NoError(t, checkGuideArgs(cmd, false, []string{"cache", "search"}))
	})
	t.Run("refuses a command with --all", func(t *testing.T) {
		requireUsageError(t, checkGuideArgs(cmd, true, []string{"claude"}))
	})
	t.Run("refuses an unknown command", func(t *testing.T) {
		err := checkGuideArgs(cmd, false, []string{"cache", "nope"})
		requireUsageError(t, err)
		assert.ErrorContains(t, err, `no guide for "cache nope"`)
	})
	t.Run("refuses a path outside the guides", func(t *testing.T) {
		requireUsageError(t, checkGuideArgs(cmd, false, []string{"../llm"}))
	})
}

func TestGuideTopic(t *testing.T) {
	t.Run("no arguments is the overview", func(t *testing.T) {
		assert.Empty(t, guideTopic(nil))
	})
	t.Run("joins separate arguments", func(t *testing.T) {
		assert.Equal(t, "cache search", guideTopic([]string{"cache", "search"}))
	})
	t.Run("accepts one quoted argument", func(t *testing.T) {
		assert.Equal(t, "cache search", guideTopic([]string{" cache  search "}))
	})
}

func TestGuideFile(t *testing.T) {
	t.Run("overview", func(t *testing.T) {
		assert.Equal(t, "guides/ws.md", guideFile(""))
	})
	t.Run("command", func(t *testing.T) {
		assert.Equal(t, "guides/ws-claude.md", guideFile("claude"))
	})
	t.Run("subcommand", func(t *testing.T) {
		assert.Equal(t, "guides/ws-cache-search.md", guideFile("cache search"))
	})
}

func TestReadGuide(t *testing.T) {
	t.Run("reads a guide", func(t *testing.T) {
		text, err := readGuide("rm")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(text, "# ws rm\n"))
	})
	t.Run("missing guide", func(t *testing.T) {
		_, err := readGuide("nope")
		assert.ErrorContains(t, err, `"nope"`)
	})
}

func TestApp_runLLM(t *testing.T) {
	t.Run("overview without a command", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		require.NoError(t, app.runLLM(false, nil))
		assert.Equal(t, mustGuide(t, ""), stdout.String())
	})
	t.Run("one command's guide", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		require.NoError(t, app.runLLM(false, []string{"claude"}))
		assert.Equal(t, mustGuide(t, "claude"), stdout.String())
	})
	t.Run("--all prints every guide in order", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		require.NoError(t, app.runLLM(true, nil))
		guides := make([]string, 0, len(guideTopics()))
		for _, topic := range guideTopics() {
			guides = append(guides, mustGuide(t, topic))
		}
		assert.Equal(t, strings.Join(guides, "\n"), stdout.String())
	})
}

func TestApp_printGuides(t *testing.T) {
	t.Run("separates guides with a blank line", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		require.NoError(t, app.printGuides([]string{"rm", "list"}))
		assert.Contains(t, stdout.String(), "\n\n# ws list\n")
	})
	t.Run("missing guide", func(t *testing.T) {
		app, _, _ := newTestApp()
		assert.Error(t, app.printGuides([]string{"nope"}))
	})
	t.Run("write failure", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.stdout = failingWriter{}
		assert.ErrorIs(t, app.printGuides([]string{"rm"}), errTest)
	})
}
