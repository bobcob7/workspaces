package cli

import (
	"context"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/artifact"
)

// maxCommentBytes bounds a body read from --body-file: the most bytes that
// can hold MaxBodyRunes characters. The store checks the rune count.
const maxCommentBytes = artifact.MaxBodyRunes * utf8.UTFMax

// commentJSON is a comment in a thread (README.md › Thread).
type commentJSON struct {
	ID        string         `json:"id"`
	Author    artifact.Actor `json:"author"`
	Body      string         `json:"body"`
	CreatedAt string         `json:"created_at"`
}

// threadJSON is a thread as ws prints it (README.md › Thread).
type threadJSON struct {
	ID         string           `json:"id"`
	Anchor     *artifact.Anchor `json:"anchor"`
	Resolved   bool             `json:"resolved"`
	ResolvedAt *string          `json:"resolved_at"`
	ResolvedBy *artifact.Actor  `json:"resolved_by"`
	Comments   []commentJSON    `json:"comments"`
}

// commentListJSON is the output of comment list.
type commentListJSON struct {
	ArtifactID string       `json:"artifact_id"`
	Latest     int          `json:"latest"`
	Threads    []threadJSON `json:"threads"`
	Warnings   []string     `json:"warnings"`
}

// commentAddedJSON is the output of comment add: the thread, and the comment
// it got.
type commentAddedJSON struct {
	ArtifactID string      `json:"artifact_id"`
	Thread     threadJSON  `json:"thread"`
	Comment    commentJSON `json:"comment"`
	Warnings   []string    `json:"warnings"`
}

// commentResolvedJSON is the output of comment resolve.
type commentResolvedJSON struct {
	ArtifactID string     `json:"artifact_id"`
	Thread     threadJSON `json:"thread"`
	Warnings   []string   `json:"warnings"`
}

// commentAddOptions are comment add's flags. prefix and suffix are only the
// people's tree's (ADR 0079).
type commentAddOptions struct {
	body     string
	bodyFile string
	reply    string
	quote    string
	line     int
	prefix   string
	suffix   string
}

// anchorNotFound is codeAnchorNotFound with the quote's occurrences and the
// lines they start on (ADR 0079).
func anchorNotFound(err error, anchor *artifact.AnchorError) *cliError {
	lines := anchor.Lines
	if lines == nil {
		lines = []int{}
	}
	return &cliError{code: codeAnchorNotFound, err: err, details: map[string]any{"occurrences": anchor.Occurrences, "lines": lines}}
}

// commentToJSON is c as ws prints it.
func commentToJSON(c *artifact.Comment) commentJSON {
	return commentJSON{ID: c.ID, Author: c.Author, Body: c.Body, CreatedAt: formatTime(c.CreatedAt)}
}

// threadToJSON is t as ws prints it: its comments in order, and its times
// as RFC 3339.
func threadToJSON(t *artifact.Thread) threadJSON {
	comments := make([]commentJSON, 0, len(t.Comments))
	for i := range t.Comments {
		comments = append(comments, commentToJSON(&t.Comments[i]))
	}
	return threadJSON{
		ID:         t.ID,
		Anchor:     t.Anchor,
		Resolved:   t.Resolved,
		ResolvedAt: formatTimePtr(t.ResolvedAt),
		ResolvedBy: t.ResolvedBy,
		Comments:   comments,
	}
}

// formatTimePtr is t as RFC 3339, or nil for no time.
func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)
	return &s
}

// threadsJSON is threads as ws prints them, without the resolved ones when
// openOnly is set.
func threadsJSON(threads []artifact.Thread, openOnly bool) []threadJSON {
	out := make([]threadJSON, 0, len(threads))
	for i := range threads {
		if openOnly && threads[i].Resolved {
			continue
		}
		out = append(out, threadToJSON(&threads[i]))
	}
	return out
}

// newArtifactCommentCmd is `comment list|add|resolve` for either tree: people
// (ws artifact) or the calling agent (ws machine artifact, ADR 0079).
func (a *App) newArtifactCommentCmd(people bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment <OP>",
		Short: "List, add to and resolve comment threads",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newCommentListCmd(people), a.newCommentAddCmd(people), a.newCommentResolveCmd(people))
	return cmd
}

// commentRun is a comment subcommand's RunE: fn runs after config loads, with
// the actor it writes as. An agent's actor comes after the checks of ws
// machine artifact.
func (a *App) commentRun(people bool, fn func(ctx context.Context, by artifact.Actor, args []string, cmd *cobra.Command) error) func(*cobra.Command, []string) error {
	if people {
		return runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return fn(cmd.Context(), artifact.Person(), args, cmd)
		}))
	}
	return a.withMachineCaller(func(ctx context.Context, c machineArtifactCaller, args []string, cmd *cobra.Command) error {
		return fn(ctx, c.actor, args, cmd)
	})
}

func (a *App) newCommentListCmd(people bool) *cobra.Command {
	var openOnly bool
	cmd := &cobra.Command{
		Use:   "list <ID> [--open]",
		Short: "List an artifact's comment threads",
		Args:  artifactArgs(1, "an artifact id"),
		RunE: a.commentRun(people, func(_ context.Context, _ artifact.Actor, args []string, _ *cobra.Command) error {
			return a.listComments(args[0], openOnly)
		}),
	}
	cmd.Flags().BoolVar(&openOnly, "open", false, "only the threads not resolved")
	return cmd
}

// listComments prints the threads of the artifact arg names, dropping the
// resolved ones when openOnly is set.
func (a *App) listComments(arg string, openOnly bool) error {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return err
	}
	store := a.artifactStore()
	entry, err := store.Show(id)
	if err != nil {
		return artifactError(err)
	}
	threads, err := store.Threads(id)
	if err != nil {
		return artifactError(err)
	}
	return a.writeJSON(commentListJSON{
		ArtifactID: id,
		Latest:     entry.Record.Latest().Rev,
		Threads:    threadsJSON(threads, openOnly),
		Warnings:   a.artifactWarnings(nil),
	})
}

func (a *App) newCommentAddCmd(people bool) *cobra.Command {
	var opts commentAddOptions
	cmd := &cobra.Command{
		Use:   "add <ID> (--body <text> | --body-file <path|->) [--reply <thread> | --quote <text> [--line <n>]]",
		Short: "Start a comment thread on an artifact, or reply to one",
		Args:  artifactArgs(1, "an artifact id"),
		RunE: a.commentRun(people, func(ctx context.Context, by artifact.Actor, args []string, cmd *cobra.Command) error {
			return a.addComment(ctx, args[0], by, &opts, cmd)
		}),
	}
	addCommentFlags(cmd, &opts, people)
	return cmd
}

// addCommentFlags sets comment add's flags on cmd; the context flags are the
// people's.
func addCommentFlags(cmd *cobra.Command, opts *commentAddOptions, people bool) {
	f := cmd.Flags()
	f.StringVar(&opts.body, "body", "", "the comment's text, up to 10,000 characters")
	f.StringVar(&opts.bodyFile, "body-file", "", "read the comment's text from this file, or - for stdin")
	f.StringVar(&opts.reply, "reply", "", "reply to this thread, such as t1")
	f.StringVar(&opts.quote, "quote", "", "anchor a new thread to this text in the latest revision")
	f.IntVar(&opts.line, "line", 0, "the line the quote starts on, when it occurs more than once")
	if people {
		f.StringVar(&opts.prefix, "prefix", "", "the text before the quote, as the desktop sends it")
		f.StringVar(&opts.suffix, "suffix", "", "the text after the quote, as the desktop sends it")
	}
}

// checkCommentFlags is usage unless the flags agree: --reply starts no new
// thread, so it isn't given with --quote; --line and the context name a quote.
func checkCommentFlags(cmd *cobra.Command, opts *commentAddOptions) error {
	f := cmd.Flags()
	switch {
	case f.Changed("reply") && f.Changed("quote"):
		return usageErrorf("--reply and --quote can't be given together")
	case !f.Changed("quote") && (f.Changed("line") || f.Changed("prefix") || f.Changed("suffix")):
		return usageErrorf("--line, --prefix and --suffix need --quote")
	case f.Changed("line") && opts.line < 1:
		return usageErrorf("--line must be at least 1, not %d", opts.line)
	}
	return nil
}

// commentBody is the text comment add was given, by --body or --body-file;
// exactly one of them is needed.
func (a *App) commentBody(cmd *cobra.Command, opts *commentAddOptions) (string, error) {
	hasBody, hasFile := cmd.Flags().Changed("body"), cmd.Flags().Changed("body-file")
	switch {
	case hasBody == hasFile:
		return "", usageErrorf("comment add needs exactly one of --body and --body-file")
	case hasFile:
		return a.readCommentFile(opts.bodyFile)
	}
	return opts.body, nil
}

// readCommentFile reads a comment's text from path, or stdin for "-". A file
// that can't hold a comment is artifact_invalid.
func (a *App) readCommentFile(path string) (string, error) {
	src, closeSrc, err := a.artifactSource(path)
	if err != nil {
		return "", err
	}
	defer closeSrc()
	data, err := io.ReadAll(io.LimitReader(src, maxCommentBytes+1))
	if err != nil {
		return "", newError(codeArtifactInvalid, fmt.Errorf("reading %s: %w", path, err))
	}
	if len(data) > maxCommentBytes {
		return "", newError(codeArtifactInvalid, fmt.Errorf("%s is over %d bytes", path, maxCommentBytes))
	}
	return string(data), nil
}

// anchorRequest is the anchor comment add was given, or nil for a thread
// about the whole artifact.
func (opts *commentAddOptions) anchorRequest(cmd *cobra.Command) *artifact.AnchorRequest {
	if !cmd.Flags().Changed("quote") {
		return nil
	}
	return &artifact.AnchorRequest{Quote: opts.quote, Line: opts.line, Prefix: opts.prefix, Suffix: opts.suffix}
}

// addComment starts a thread on the artifact arg names, or replies to one
// when --reply is given, and prints the thread and the comment.
func (a *App) addComment(ctx context.Context, arg string, by artifact.Actor, opts *commentAddOptions, cmd *cobra.Command) error {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return err
	}
	if err = checkCommentFlags(cmd, opts); err != nil {
		return err
	}
	body, err := a.commentBody(cmd, opts)
	if err != nil {
		return err
	}
	store := a.artifactStore()
	if cmd.Flags().Changed("reply") {
		thread, comment, err := store.Reply(ctx, id, opts.reply, body, by)
		if err != nil {
			return artifactError(err)
		}
		return a.writeCommentAdded(id, &thread, &comment)
	}
	thread, comment, err := store.AddThread(ctx, id, &artifact.NewThread{Body: body, By: by, Anchor: opts.anchorRequest(cmd)})
	if err != nil {
		return artifactError(err)
	}
	return a.writeCommentAdded(id, &thread, &comment)
}

// writeCommentAdded prints the thread and the comment comment add made.
func (a *App) writeCommentAdded(id string, thread *artifact.Thread, comment *artifact.Comment) error {
	return a.writeJSON(commentAddedJSON{
		ArtifactID: id,
		Thread:     threadToJSON(thread),
		Comment:    commentToJSON(comment),
		Warnings:   a.artifactWarnings(nil),
	})
}

func (a *App) newCommentResolveCmd(people bool) *cobra.Command {
	var reopen bool
	cmd := &cobra.Command{
		Use:   "resolve <ID> <THREAD> [--reopen]",
		Short: "Resolve a comment thread, or open it again",
		Args:  artifactArgs(2, "an artifact id and a thread id"),
		RunE: a.commentRun(people, func(ctx context.Context, by artifact.Actor, args []string, _ *cobra.Command) error {
			return a.resolveComment(ctx, args, by, reopen)
		}),
	}
	cmd.Flags().BoolVar(&reopen, "reopen", false, "open the thread again")
	return cmd
}

// resolveComment resolves (or with reopen, reopens) the thread args[1] of the
// artifact args[0] names, and prints the thread.
func (a *App) resolveComment(ctx context.Context, args []string, by artifact.Actor, reopen bool) error {
	id, _, err := artifactRef(args[0], 0, false)
	if err != nil {
		return err
	}
	thread, err := a.artifactStore().Resolve(ctx, id, args[1], by, reopen)
	if err != nil {
		return artifactError(err)
	}
	return a.writeJSON(commentResolvedJSON{ArtifactID: id, Thread: threadToJSON(&thread), Warnings: a.artifactWarnings(nil)})
}
