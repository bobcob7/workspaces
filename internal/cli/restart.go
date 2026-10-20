package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
)

type restartOptions struct {
	noWait bool
	force  bool
}

func (a *App) newRestartCmd() *cobra.Command {
	var opts restartOptions
	cmd := &cobra.Command{
		Use:   "restart [OPTIONS]",
		Short: "Restart Claude Code's background service and every running agent",
		Args:  noArgs,
		PreRunE: func(*cobra.Command, []string) error {
			if opts.noWait && opts.force {
				return usageErrorf("--no-wait and --force can't be used together")
			}
			return nil
		},
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.runRestart(cmd.Context(), &opts)
		})),
	}
	cmd.Flags().BoolVar(&opts.noWait, "no-wait", false, "respawn only sessions that aren't working, then exit")
	cmd.Flags().BoolVar(&opts.force, "force", false, "respawn working sessions at once, interrupting their turn")
	return cmd
}

// restartJSON is one session in ws restart's output
// (docs/tools/ws-restart.md).
type restartJSON struct {
	ID        string  `json:"id"`
	Workspace *string `json:"workspace"`
	// Outcome is the text output's first column: "restarted", "stale",
	// or "gone".
	Outcome   string         `json:"outcome"`
	Restarted bool           `json:"restarted"`
	Error     *itemErrorJSON `json:"error"`
}

// runRestart replaces Claude Code's background service and respawns every
// running background session (ADR 0018). Text output is printed as each
// session's outcome is known, since waiting for working sessions can take
// long. As for ws rm and ws restore, a session that wasn't restarted for a
// failure carries its own error, and the exit code is the first one's; a
// failure before any session was reported prints nothing on stdout (ADR
// 0016).
func (a *App) runRestart(ctx context.Context, opts *restartOptions) error {
	p := &restartPrinter{a: a, out: []restartJSON{}}
	_, err := a.lifetime.Restart(ctx, lifetime.RestartOptions{
		NoWait:   opts.noWait,
		Force:    opts.force,
		Recorded: p.recorded,
		Waiting:  p.waiting,
		Done:     p.done,
	})
	if err != nil {
		return domainError(err)
	}
	return p.finish()
}

// restartPrinter reports ws restart's sessions as they are restarted.
type restartPrinter struct {
	a *App
	// width is the workspace column's, over every session recorded.
	width int
	items itemResults
	out   []restartJSON
}

// recorded sizes the workspace column and, as progress, says that every
// background session is restarted, not only ws's (ADR 0018).
func (p *restartPrinter) recorded(sessions []lifetime.Restarted) {
	for i := range sessions {
		p.width = max(p.width, len(workspaceLabel(&sessions[i])))
	}
	if p.a.showProgress() {
		fmt.Fprintf(p.a.stderr, "ws: restarting Claude Code's background service and its %d running background sessions, including any ws didn't start\n", len(sessions))
	}
}

// waiting shows, as progress, the working sessions ws waits for.
func (p *restartPrinter) waiting(sessions []lifetime.Restarted) {
	if !p.a.showProgress() {
		return
	}
	for i := range sessions {
		fmt.Fprintln(p.a.stderr, p.line("waiting", &sessions[i], "working"))
	}
}

// done records a session's outcome, and prints it at once unless --json:
// on stdout, or a failure on stderr.
func (p *restartPrinter) done(s lifetime.Restarted) {
	itemErr := p.items.add("", s.Err)
	verb, note := restartVerb(&s)
	p.out = append(p.out, restartJSON{ID: s.SessionID, Workspace: optional(s.Workspace), Outcome: verb, Restarted: s.Restarted, Error: itemErr})
	if p.a.global.json {
		return
	}
	if itemErr != nil {
		fmt.Fprintf(p.a.stderr, "ws: %s\n", itemErr.Message)
		return
	}
	fmt.Fprintln(p.a.stdout, p.line(verb, &s, note))
}

// finish writes the JSON result, or the warnings after the text, and
// returns the first session's failure, marked reported.
func (p *restartPrinter) finish() error {
	if p.a.global.json {
		if err := p.a.writeJSON(map[string]any{"sessions": p.out, "warnings": p.a.warnings()}); err != nil {
			return err
		}
	} else {
		p.a.printWarnings()
	}
	if p.items.first == nil {
		return nil
	}
	p.items.first.reported = true
	return p.items.first
}

func (p *restartPrinter) line(verb string, s *lifetime.Restarted, note string) string {
	line := fmt.Sprintf("%-9s  %-*s  %s", verb, p.width, workspaceLabel(s), s.SessionID)
	if note == "" {
		return line
	}
	return line + "  " + note
}

// restartVerb is the first column of a session's line, and a note after
// it for one left stale.
func restartVerb(s *lifetime.Restarted) (verb, note string) {
	switch {
	case s.Restarted:
		return "restarted", ""
	case s.Gone:
		return "gone", ""
	case s.Working:
		return "stale", "working; run `ws restart` again later"
	}
	return "stale", "run `ws restart` again later"
}

// workspaceLabel is the session's workspace, or "-" for a session ws
// didn't start.
func workspaceLabel(s *lifetime.Restarted) string {
	if s.Workspace == "" {
		return "-"
	}
	return s.Workspace
}
