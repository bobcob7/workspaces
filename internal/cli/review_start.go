package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/gh"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review"
	"github.com/bobcob7/workspaces/internal/review/guide"
	"github.com/bobcob7/workspaces/internal/review/profile"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/start"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// requesterNone is --requester's value for a review nobody asked for.
const requesterNone = "none"

type reviewStartOptions struct {
	name       string
	offline    bool
	pr         int
	profiles   []string
	auto       []string
	requester  string
	spec       string
	claudeArgs []string
	focus      string
}

func (a *App) newReviewStartCmd() *cobra.Command {
	var opts reviewStartOptions
	cmd := &cobra.Command{
		Use:   "start [OPTIONS] <SPEC> [-- <CLAUDE ARGS>... [<FOCUS>]]",
		Short: "Start a review of a pull request or commit",
		Args:  allArgs(oneSpecBeforeDash, claudeArgsAfterDash),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			specs, after := splitAtDash(cmd, args)
			opts.spec = specs[0]
			opts.claudeArgs, opts.focus = splitFocus(after)
			return a.runReviewStart(cmd.Context(), &opts)
		})),
	}
	addCreateFlags(cmd, &opts.name, &opts.offline)
	cmd.Flags().IntVar(&opts.pr, "pr", 0, "review this pull request's head; the spec has no @ref")
	cmd.Flags().StringArrayVar(&opts.profiles, "profile", nil, "review with this profile, NAME or NAME:MODEL; repeatable")
	cmd.Flags().StringArrayVar(&opts.auto, "auto", nil, "review with this profile if the changed files match it, NAME or NAME:MODEL; repeatable")
	cmd.Flags().StringVar(&opts.requester, "requester", "", "the workspace asking for the review, or none; default: the one ws runs in")
	return cmd
}

// oneSpecBeforeDash requires exactly one repo spec before "--".
func oneSpecBeforeDash(cmd *cobra.Command, args []string) error {
	specs, _ := splitAtDash(cmd, args)
	return oneSpec(cmd, specs)
}

// splitFocus separates the requester's focus, the last argument after
// "--" when it isn't an option, from the options for claude.
func splitFocus(after []string) (claudeArgs []string, focus string) {
	if len(after) == 0 || strings.HasPrefix(after[len(after)-1], "-") {
		return after, ""
	}
	return after[:len(after)-1], after[len(after)-1]
}

// reviewStartJSON is ws review start's output: ws claude's, plus the
// record and the command that waits for the report (ADR 0039).
type reviewStartJSON struct {
	claudeResult

	Review      record.Record `json:"review"`
	WaitCommand string        `json:"wait_command"`
}

// reviewTarget is what a review covers, resolved before anything is
// created.
type reviewTarget struct {
	id     repo.ID
	commit string
	// base is the pull request's base branch, or empty for a commit.
	base string
	pr   *gh.PR
}

// runReviewStart resolves what to review, chooses its profiles, and
// starts its agent in a new workspace whose review record is written
// before the workspace is moved into place (ADRs 0022, 0038, 0039).
func (a *App) runReviewStart(ctx context.Context, opts *reviewStartOptions) error {
	explicit, auto, err := parseChoices(opts.profiles, opts.auto)
	if err != nil {
		return err
	}
	if err = a.claude.Check(ctx); err != nil {
		return domainError(err)
	}
	requester, err := a.reviewRequester(opts.requester)
	if err != nil {
		return err
	}
	target, err := a.reviewTarget(ctx, opts)
	if err != nil {
		return err
	}
	rec, err := a.chooseProfiles(ctx, &target, explicit, auto)
	if err != nil {
		return err
	}
	rec.Requester, rec.StartedAt = requester, a.now()
	args, err := a.reviewAgentArgs(&rec, &target, opts)
	if err != nil {
		return err
	}
	return a.startReview(ctx, &rec, &target, opts, args)
}

// parseChoices reads --profile's and --auto's values.
func parseChoices(profiles, auto []string) (explicit, autoChoices []start.Choice, err error) {
	parse := func(values []string) ([]start.Choice, error) {
		choices := make([]start.Choice, 0, len(values))
		for _, v := range values {
			c, parseErr := start.ParseChoice(v)
			if parseErr != nil {
				return nil, newError(codeUsage, parseErr)
			}
			choices = append(choices, c)
		}
		return choices, nil
	}
	if explicit, err = parse(profiles); err != nil {
		return nil, nil, err
	}
	if autoChoices, err = parse(auto); err != nil {
		return nil, nil, err
	}
	return explicit, autoChoices, nil
}

// reviewRequester is the workspace asking for the review (ADR 0039):
// --requester's, "" for none, or by default the workspace ws runs in, ""
// outside one.
func (a *App) reviewRequester(flag string) (string, error) {
	if flag == requesterNone {
		return "", nil
	}
	if flag != "" {
		ws, err := a.workspaces.Find(flag)
		if err != nil {
			return "", domainError(err)
		}
		return ws.ID, nil
	}
	dir, err := a.getwd()
	if err != nil {
		return "", fmt.Errorf("finding the working directory: %w", err)
	}
	ws, err := a.workspaces.Containing(dir)
	if errors.Is(err, workspace.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ws.ID, nil
}

// reviewTarget resolves the repo, brings its cache up to date unless
// --offline, and resolves the commit: the pull request's head with --pr,
// else the spec's ref.
func (a *App) reviewTarget(ctx context.Context, opts *reviewStartOptions) (reviewTarget, error) {
	sp, err := repo.ParseSpec(opts.spec)
	if err != nil {
		return reviewTarget{}, newError(codeSpecInvalid, err)
	}
	if opts.pr != 0 && sp.Ref != "" {
		return reviewTarget{}, usageErrorf("--pr reviews the pull request's head; leave @%s off the spec", sp.Ref)
	}
	if opts.pr < 0 {
		return reviewTarget{}, usageErrorf("--pr needs a pull request number, got %d", opts.pr)
	}
	res, err := a.cachedRepo(ctx, &sp, opts.offline)
	if err != nil {
		return reviewTarget{}, err
	}
	target := reviewTarget{id: res.ID}
	ref := sp.Ref
	if opts.pr != 0 {
		pr, viewErr := a.gh.PRView(ctx, res.ID.String(), opts.pr)
		if viewErr != nil {
			return reviewTarget{}, domainError(viewErr)
		}
		target.pr, target.base, ref = &pr, pr.BaseRefName, pr.HeadRefOid
	}
	resolved, err := a.cache.ResolveRef(ctx, res.ID, ref)
	if err != nil {
		return reviewTarget{}, domainError(err)
	}
	target.commit = resolved.Commit
	return target, nil
}

// cachedRepo resolves sp, then clones it into the cache or fetches it,
// unless offline, which only looks it up.
func (a *App) cachedRepo(ctx context.Context, sp *repo.Spec, offline bool) (cache.Resolution, error) {
	if offline {
		res, err := a.cache.Lookup(ctx, sp)
		return res, domainErrorOrNil(err)
	}
	res, err := a.cache.Resolve(ctx, sp)
	if err != nil {
		return cache.Resolution{}, domainError(err)
	}
	if !res.Cached {
		_, err = a.cache.Add(ctx, res.ID, res.Upstream)
	} else {
		err = a.cache.Fetch(ctx, res.ID)
	}
	return res, domainErrorOrNil(err)
}

func domainErrorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return domainError(err)
}

// chooseProfiles lists the files the review covers and chooses its
// profiles from them, the profiles' settings, and the flags (ADR 0038).
// The record it returns has everything but the requester and start time.
func (a *App) chooseProfiles(ctx context.Context, target *reviewTarget, explicit, auto []start.Choice) (record.Record, error) {
	files, err := a.cache.ChangedFiles(ctx, target.id, target.base, target.commit)
	if err != nil {
		return record.Record{}, domainError(err)
	}
	profiles, err := a.reviewProfiles()
	if err != nil {
		return record.Record{}, err
	}
	chosen, err := start.Choose(&start.Request{Profiles: profiles, Config: a.config.Review.Profiles, Explicit: explicit, Auto: auto, Files: files})
	if errors.Is(err, start.ErrUnknownProfile) {
		return record.Record{}, newError(codeProfileUnknown, err)
	}
	if err != nil {
		return record.Record{}, newError(codeUsage, err)
	}
	rec := record.Record{Repo: target.id.String(), Ref: target.commit, Profiles: chosen}
	if pr := target.pr; pr != nil {
		rec.PR = &record.PR{Number: pr.Number, URL: pr.URL, Base: pr.BaseRefName, HeadBranch: pr.HeadRefName, CrossRepository: pr.IsCrossRepository}
	}
	return rec, nil
}

// reviewProfiles lists every profile, warning about the user's files that
// couldn't be read.
func (a *App) reviewProfiles() ([]profile.Profile, error) {
	profiles, broken, err := profile.NewSet(a.paths.ReviewProfiles).List()
	if err != nil {
		return nil, err
	}
	for _, file := range broken {
		a.warned = append(a.warned, fmt.Sprintf("skipped review profile %s: it couldn't be read or parsed", file))
	}
	return profiles, nil
}

// reviewAgentArgs are the review agent's arguments for claude: --agents
// with a subagent per profile, the caller's options, and the prompt last
// (ADR 0038).
func (a *App) reviewAgentArgs(rec *record.Record, target *reviewTarget, opts *reviewStartOptions) ([]string, error) {
	if slices.ContainsFunc(opts.claudeArgs, func(arg string) bool { return arg == "--agents" || strings.HasPrefix(arg, "--agents=") }) {
		return nil, usageErrorf("--agents can't be passed to claude through ws review start; ws defines the review's subagents")
	}
	prompt := &review.Prompt{Repo: rec.Repo, Ref: rec.Ref, Focus: opts.focus}
	if pr := target.pr; pr != nil {
		prompt.PR = &review.PromptPR{Number: pr.Number, Title: pr.Title, URL: pr.URL, Base: pr.BaseRefName, HeadBranch: pr.HeadRefName}
	}
	loaded := make([]profile.Profile, 0, len(rec.Profiles))
	for _, p := range rec.Profiles {
		lp, err := a.loadReviewProfile(p.Name)
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, lp)
		prompt.Subagents = append(prompt.Subagents, start.SubagentName(p.Name))
		prompt.Profiles = append(prompt.Profiles, review.PromptProfile{Name: lp.Name, Description: lp.Description})
	}
	subagents := make([]start.Subagent, 0, len(rec.Profiles))
	for i, p := range rec.Profiles {
		text, err := subagentPrompt(prompt, &loaded[i])
		if err != nil {
			return nil, err
		}
		subagents = append(subagents, start.Subagent{Profile: p, Prompt: text})
	}
	agents, err := start.Agents(subagents)
	if err != nil {
		return nil, err
	}
	agentPrompt, err := review.AgentPrompt(prompt)
	if err != nil {
		return nil, err
	}
	return slices.Concat([]string{"--agents", agents}, opts.claudeArgs, []string{agentPrompt}), nil
}

// loadReviewProfile reads the profile named name, the user's or the
// built-in.
func (a *App) loadReviewProfile(name string) (profile.Profile, error) {
	p, ok, err := profile.NewSet(a.paths.ReviewProfiles).Load(name)
	if err != nil {
		return profile.Profile{}, newError(codeConfigInvalid, err)
	}
	if !ok {
		return profile.Profile{}, newError(codeProfileUnknown, fmt.Errorf("no profile named %q", name))
	}
	return p, nil
}

// subagentPrompt is what ws review prime --profile prints for p, followed
// by the subagent's task.
func subagentPrompt(prompt *review.Prompt, p *profile.Profile) (string, error) {
	task, err := review.SubagentTask(prompt, p.Name)
	if err != nil {
		return "", err
	}
	return guide.PrimeWithProfile(p) + "\n" + task, nil
}

// startReview creates the review workspace at the commit, writing rec
// before the workspace is moved into place, and hands it to the review
// agent as ws claude does. The cache is already up to date, so creating
// doesn't fetch again.
func (a *App) startReview(ctx context.Context, rec *record.Record, target *reviewTarget, opts *reviewStartOptions, args []string) error {
	name := opts.name
	if name == "" {
		number := 0
		if target.pr != nil {
			number = target.pr.Number
		}
		name = start.Name(target.id.Name, number, target.commit)
	}
	sp, err := repo.ParseSpec(target.id.String() + "@" + target.commit)
	if err != nil {
		return err
	}
	prepare := func(ws *workspace.Workspace) error {
		return record.Write(record.Path(a.paths.State, ws.ID), rec)
	}
	ws, lock, err := a.workspaces.Create(ctx, workspace.CreateOptions{Name: name, Specs: []repo.Spec{sp}, Offline: true, Prepare: prepare})
	if err != nil {
		a.printWarnings()
		return domainError(err)
	}
	defer lock.Unlock()
	started, _, err := a.handOff(ctx, &ws, args)
	if err != nil {
		return err
	}
	lock.Unlock()
	if written, err := record.Read(record.Path(a.paths.State, ws.ID)); err == nil {
		rec = &written
	} else {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read the review record back: %v", err))
	}
	return a.printReviewStart(&ws, started.ID, rec)
}

// printReviewStart prints ws claude's line and the wait command, or with
// --json the result object.
func (a *App) printReviewStart(ws *workspace.Workspace, session string, rec *record.Record) error {
	if !a.global.json {
		if err := a.printClaudeResult(ws, session); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "wait for the report: ws review wait %s\n", ws.ID)
		return nil
	}
	return a.writeJSON(reviewStartJSON{
		claudeResult: a.claudeResultOf(ws, session),
		Review:       *rec,
		WaitCommand:  "ws review wait --json " + ws.ID,
	})
}
