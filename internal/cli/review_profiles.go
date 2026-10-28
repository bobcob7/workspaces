package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/review/profile"
)

// newReviewProfilesCmd lists the profiles, and holds the operations that
// manage them (ADR 0042).
func (a *App) newReviewProfilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiles [show | add | edit | rm]",
		Short: "List review profiles with their settings, or manage them",
		Args:  knownOperation,
		RunE: runE(a.withConfig(func(*cobra.Command, []string) error {
			return a.runReviewProfiles()
		})),
	}
	cmd.AddCommand(
		a.newReviewProfilesShowCmd(),
		a.newReviewProfilesAddCmd(),
		a.newReviewProfilesEditCmd(),
		a.newReviewProfilesRmCmd(),
	)
	return cmd
}

func (a *App) newReviewProfilesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <NAME>",
		Short: "Print one review profile, with its guide",
		Args:  exactlyOneArg("a profile name"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			return a.runReviewProfilesShow(args[0])
		})),
	}
}

// profileOptions are the content flags of ws review profiles add and edit.
type profileOptions struct {
	description string
	when        string
	match       []string
	noMatch     bool
	guideFile   string
}

func (a *App) newReviewProfilesAddCmd() *cobra.Command {
	var opts profileOptions
	cmd := &cobra.Command{
		Use:   "add <NAME> --description <D> [--when <W>] [--match <GLOB>]... (--guide-file <F> | -)",
		Short: "Add a review profile",
		Args:  profileArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			p, err := a.newProfile(cmd, args, &opts)
			if err != nil {
				return err
			}
			return a.runReviewProfilesAdd(cmd.Context(), &p)
		})),
	}
	addProfileFlags(cmd, &opts)
	return cmd
}

func (a *App) newReviewProfilesEditCmd() *cobra.Command {
	var opts profileOptions
	cmd := &cobra.Command{
		Use:   "edit <NAME> [--description <D>] [--when <W>] [--match <GLOB>]... [--no-match] [--guide-file <F> | -]",
		Short: "Edit a review profile; a built-in is copied, then edited",
		Args:  profileArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			changes, err := a.profileChanges(cmd, args, &opts)
			if err != nil {
				return err
			}
			return a.runReviewProfilesEdit(cmd.Context(), args[0], changes)
		})),
	}
	addProfileFlags(cmd, &opts)
	cmd.Flags().BoolVar(&opts.noMatch, "no-match", false, "remove every file pattern")
	return cmd
}

func (a *App) newReviewProfilesRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <NAME>",
		Short: "Remove your review profile, or your edits to a built-in",
		Args:  exactlyOneArg("a profile name"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runReviewProfilesRm(cmd.Context(), args[0])
		})),
	}
}

func addProfileFlags(cmd *cobra.Command, opts *profileOptions) {
	cmd.Flags().StringVar(&opts.description, "description", "", "one line saying what the profile reviews")
	cmd.Flags().StringVar(&opts.when, "when", "", "when the profile joins a review: always, auto, or off")
	cmd.Flags().StringArrayVar(&opts.match, "match", nil, "a file pattern that pulls an auto profile in; repeatable")
	cmd.Flags().StringVar(&opts.guideFile, "guide-file", "", "read the reviewer's guide from this file; - as an argument reads stdin")
}

func exactlyOneArg(what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return usageErrorf("%s needs %s, got %d arguments", commandName(cmd), what, len(args))
		}
		return nil
	}
}

// profileArgs is a profile name, then optionally -, for the guide on stdin.
func profileArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || len(args) > 2 {
		return usageErrorf("%s needs a profile name, then optionally -", commandName(cmd))
	}
	if len(args) == 2 && args[1] != "-" {
		return usageErrorf("%s takes - after the name, for the guide on stdin, not %q", commandName(cmd), args[1])
	}
	return nil
}

// newProfile is the profile ws review profiles add writes.
func (a *App) newProfile(cmd *cobra.Command, args []string, opts *profileOptions) (profile.Profile, error) {
	if !cmd.Flags().Changed("description") {
		return profile.Profile{}, usageErrorf("%s needs --description", commandName(cmd))
	}
	changes, err := a.profileChanges(cmd, args, opts)
	if err != nil {
		return profile.Profile{}, err
	}
	if changes.Body == nil {
		return profile.Profile{}, usageErrorf("%s needs the guide: --guide-file <F>, or - for stdin", commandName(cmd))
	}
	p := profile.Profile{Name: args[0], Description: opts.description, Body: *changes.Body}
	if changes.When != nil {
		p.When = *changes.When
	}
	if changes.Match != nil {
		p.Match = *changes.Match
	}
	return p, nil
}

// profileChanges are the fields the flags and arguments set.
func (a *App) profileChanges(cmd *cobra.Command, args []string, opts *profileOptions) (*profile.Changes, error) {
	flags := cmd.Flags()
	var changes profile.Changes
	if flags.Changed("description") {
		changes.Description = &opts.description
	}
	if flags.Changed("when") {
		when, err := profile.ParseWhen(opts.when)
		if err != nil {
			return nil, usageErrorf("--when: %v", err)
		}
		changes.When = &when
	}
	if opts.noMatch && len(opts.match) > 0 {
		return nil, usageErrorf("--no-match removes the patterns and --match sets them; use one")
	}
	if opts.noMatch || len(opts.match) > 0 {
		match := append([]string{}, opts.match...)
		changes.Match = &match
	}
	guide, ok, err := a.readProfileGuide(flags.Changed("guide-file"), opts.guideFile, len(args) == 2)
	if err != nil {
		return nil, err
	}
	if ok {
		changes.Body = &guide
	}
	if changes == (profile.Changes{}) {
		return nil, usageErrorf("%s needs something to change", commandName(cmd))
	}
	return &changes, nil
}

// readProfileGuide reads the guide from file, or stdin; ok is false for
// neither.
func (a *App) readProfileGuide(fromFile bool, file string, fromStdin bool) (guide string, ok bool, err error) {
	var raw []byte
	switch {
	case fromFile && fromStdin:
		return "", false, usageErrorf("--guide-file and - both give the guide; use one")
	case fromFile:
		raw, err = os.ReadFile(file)
	case fromStdin:
		raw, err = io.ReadAll(a.stdin)
	default:
		return "", false, nil
	}
	if err != nil {
		return "", false, usageErrorf("reading the guide: %v", err)
	}
	return string(raw), true, nil
}

// reviewProfilesJSON is ws review profiles' output (ADR 0039).
type reviewProfilesJSON struct {
	Profiles []reviewProfileInfoJSON `json:"profiles"`
	Warnings []string                `json:"warnings"`
}

// reviewProfileInfoJSON is one profile as config.toml leaves it: when is
// config.toml's override, if any, and model "" is the session's. Builtin
// is kept beside source for compatibility (ADR 0042).
type reviewProfileInfoJSON struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	When        profile.When   `json:"when"`
	Match       []string       `json:"match"`
	Model       string         `json:"model"`
	Builtin     bool           `json:"builtin"`
	Source      profile.Source `json:"source"`
}

// reviewProfileDetailJSON is ws review profiles show's, add's, and edit's
// output: the profile, its guide, and its file, null for a built-in.
type reviewProfileDetailJSON struct {
	reviewProfileInfoJSON
	Guide    string   `json:"guide"`
	Path     *string  `json:"path"`
	Warnings []string `json:"warnings"`
}

// reviewProfileRemovedJSON is ws review profiles rm's output.
type reviewProfileRemovedJSON struct {
	Name string `json:"name"`
	// Path is the file removed; Restored is set when a built-in of the
	// name is back.
	Path     string   `json:"path"`
	Restored bool     `json:"restored"`
	Warnings []string `json:"warnings"`
}

// runReviewProfiles lists every profile, built-in and the user's, with
// config.toml's when and model applied.
func (a *App) runReviewProfiles() error {
	profiles, err := a.reviewProfiles()
	if err != nil {
		return err
	}
	out := make([]reviewProfileInfoJSON, 0, len(profiles))
	for i := range profiles {
		out = append(out, a.profileInfo(&profiles[i]))
	}
	if a.global.json {
		return a.writeJSON(reviewProfilesJSON{Profiles: out, Warnings: a.warnings()})
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSOURCE\tWHEN\tMATCH\tMODEL\tDESCRIPTION")
	for _, p := range out {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, p.Source, p.When, dashIfEmpty(strings.Join(p.Match, " ")), dashIfEmpty(p.Model), p.Description)
	}
	_ = tw.Flush()
	a.printWarnings()
	return nil
}

// profileInfo is p with config.toml's when and model applied.
func (a *App) profileInfo(p *profile.Profile) reviewProfileInfoJSON {
	settings := a.config.Review.Profiles[p.Name]
	when := p.When
	if settings.When != "" {
		when = profile.When(settings.When)
	}
	return reviewProfileInfoJSON{
		Name: p.Name, Description: p.Description, When: when, Match: nonNil(p.Match), Model: settings.Model,
		Builtin: p.Builtin, Source: p.Source,
	}
}

// runReviewProfilesShow prints one profile and its guide.
func (a *App) runReviewProfilesShow(name string) error {
	p, ok, err := profile.NewSet(a.paths.ReviewProfiles).Load(name)
	if err != nil {
		return newError(codeConfigInvalid, err)
	}
	if !ok {
		return newError(codeProfileUnknown, fmt.Errorf("no profile named %q; ws review profiles lists them", name))
	}
	return a.printProfile(&p, "")
}

func (a *App) runReviewProfilesAdd(ctx context.Context, p *profile.Profile) error {
	added, err := profile.NewWriter(a.paths.ReviewProfiles).Add(ctx, *p)
	if err != nil {
		return profileWriteError(err)
	}
	return a.printProfile(&added, "added review profile")
}

func (a *App) runReviewProfilesEdit(ctx context.Context, name string, changes *profile.Changes) error {
	edited, err := profile.NewWriter(a.paths.ReviewProfiles).Edit(ctx, name, changes)
	if err != nil {
		return profileWriteError(err)
	}
	return a.printProfile(&edited, "edited review profile")
}

func (a *App) runReviewProfilesRm(ctx context.Context, name string) error {
	w := profile.NewWriter(a.paths.ReviewProfiles)
	source, err := w.Remove(ctx, name)
	if err != nil {
		return profileWriteError(err)
	}
	out := reviewProfileRemovedJSON{Name: name, Path: w.Path(name), Restored: source == profile.SourceOverride, Warnings: a.warnings()}
	if a.global.json {
		return a.writeJSON(out)
	}
	if out.Restored {
		fmt.Fprintf(a.stdout, "removed your edits to review profile %s; the built-in is back\n", name)
	} else {
		fmt.Fprintf(a.stdout, "removed review profile %s\n", name)
	}
	a.printWarnings()
	return nil
}

// printProfile prints p; done, when set, says what was just done to it.
func (a *App) printProfile(p *profile.Profile, done string) error {
	out := reviewProfileDetailJSON{reviewProfileInfoJSON: a.profileInfo(p), Guide: p.Body, Warnings: a.warnings()}
	if p.Source != profile.SourceBuiltin {
		out.Path = optional(profile.NewWriter(a.paths.ReviewProfiles).Path(p.Name))
	}
	if a.global.json {
		return a.writeJSON(out)
	}
	if done != "" {
		fmt.Fprintf(a.stdout, "%s %s at %s\n", done, p.Name, deref(out.Path))
		if p.Source == profile.SourceOverride {
			fmt.Fprintf(a.stdout, "it overrides the built-in; ws review profiles rm %s restores it\n", p.Name)
		}
		a.printWarnings()
		return nil
	}
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintf(tw, "name:\t%s\nsource:\t%s\nwhen:\t%s\nmatch:\t%s\nmodel:\t%s\ndescription:\t%s\n",
		out.Name, out.Source, out.When, dashIfEmpty(strings.Join(out.Match, " ")), dashIfEmpty(out.Model), out.Description)
	_ = tw.Flush()
	fmt.Fprintf(a.stdout, "\n%s\n", out.Guide)
	a.printWarnings()
	return nil
}

// profileWriteError gives a profile.Writer error its code (ADR 0042).
func profileWriteError(err error) error {
	switch {
	case errors.Is(err, profile.ErrInvalid):
		return newError(codeProfileInvalid, err)
	case errors.Is(err, profile.ErrExists):
		return newError(codeProfileExists, err)
	case errors.Is(err, profile.ErrBuiltin):
		return newError(codeProfileBuiltin, err)
	case errors.Is(err, profile.ErrUnknown):
		return newError(codeProfileUnknown, err)
	case errors.Is(err, profile.ErrBroken):
		return newError(codeConfigInvalid, err)
	}
	return err
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
