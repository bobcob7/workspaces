package cli

import (
	"embed"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// guideFiles are the agent guides (ADR 0014): guides/ws.md is the
// overview, and guides/ws-<command path>.md each command's guide, such as
// guides/ws-cache-search.md. They're built into the binary, so ws llm
// never touches CONFIG_DIR.
//
//go:embed guides/*.md
var guideFiles embed.FS

// guideTopics are the command paths that have a guide, in the order ws llm
// --all prints them; "" is the overview. The coverage test keeps this list,
// the guide files, and the command tree in step.
func guideTopics() []string {
	return []string{
		"",
		"claude",
		"shell",
		"list",
		"rm",
		"rename",
		"restore",
		"restart",
		"cache",
		"cache add",
		"cache update",
		"cache list",
		"cache search",
		"cache refs",
		"cache resolve",
		"cache path",
		"cache remove",
		"cache prune",
		"llm",
	}
}

func (a *App) newLLMCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "llm [<COMMAND>...]",
		Short: "Print guidance for AI agents",
		Args: func(cmd *cobra.Command, args []string) error {
			return checkGuideArgs(cmd, all, args)
		},
		PreRunE: a.noJSON,
		RunE: runE(func(_ *cobra.Command, args []string) error {
			return a.runLLM(all, args)
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "print the overview and every command's guide")
	return cmd
}

// checkGuideArgs refuses a command with --all, and a command without a
// guide, as usage errors (ADR 0016).
func checkGuideArgs(cmd *cobra.Command, all bool, args []string) error {
	if all && len(args) > 0 {
		return usageErrorf("%s --all takes no command, got %q", commandName(cmd), args[0])
	}
	topic := guideTopic(args)
	if !slices.Contains(guideTopics(), topic) {
		return usageErrorf("no guide for %q; run `ws llm` for the list of commands", topic)
	}
	return nil
}

// guideTopic is the command path args name, whether given as separate
// arguments (cache search) or as one ("cache search").
func guideTopic(args []string) string {
	return strings.Join(strings.Fields(strings.Join(args, " ")), " ")
}

// guideFile is the embedded file holding topic's guide.
func guideFile(topic string) string {
	return "guides/" + strings.Join(append([]string{"ws"}, strings.Fields(topic)...), "-") + ".md"
}

func readGuide(topic string) (string, error) {
	data, err := guideFiles.ReadFile(guideFile(topic))
	if err != nil {
		return "", fmt.Errorf("reading the guide for %q: %w", topic, err)
	}
	return string(data), nil
}

// runLLM prints the guide args name, the overview without args, or every
// guide with all.
func (a *App) runLLM(all bool, args []string) error {
	if all {
		return a.printGuides(guideTopics())
	}
	return a.printGuides([]string{guideTopic(args)})
}

// printGuides writes each topic's guide to stdout, with a blank line
// between them.
func (a *App) printGuides(topics []string) error {
	for i, topic := range topics {
		text, err := readGuide(topic)
		if err != nil {
			return err
		}
		if i > 0 {
			text = "\n" + text
		}
		if _, err := io.WriteString(a.stdout, text); err != nil {
			return fmt.Errorf("writing the guide: %w", err)
		}
	}
	return nil
}
