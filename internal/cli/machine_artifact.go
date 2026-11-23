package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// machineArtifactCaller is who a ws machine artifact command acts for: the
// actor it writes as, its tree's root, and its workspace's path.
type machineArtifactCaller struct {
	actor artifact.Actor
	root  string
	path  string
}

// machineCaller is the caller of a ws machine artifact command. It refuses
// on an enrolled environment's agent machine first (ADR 0078).
func (a *App) machineCaller() (machineArtifactCaller, error) {
	caller, err := a.caller()
	if err != nil {
		return machineArtifactCaller{}, err
	}
	if a.enrolledAgent() {
		return machineArtifactCaller{}, newError(codeArtifactUnreachable, errors.New("artifacts live on the machine this environment is enrolled with, and this ws can't reach them yet (ADR 0078)"))
	}
	list, err := a.workspaces.List()
	if err != nil {
		return machineArtifactCaller{}, fmt.Errorf("listing workspaces: %w", err)
	}
	ws := workspaceByID(list, caller)
	name := ws.Name
	if name == "" {
		name = caller
	}
	return machineArtifactCaller{actor: artifact.Agent(caller, name), root: machine.Root(list, caller), path: ws.Path}, nil
}

// workspaceByID is the workspace in list with id, or the zero Workspace.
func workspaceByID(list []workspace.Workspace, id string) workspace.Workspace {
	for i := range list {
		if list[i].ID == id {
			return list[i]
		}
	}
	return workspace.Workspace{}
}

// enrolledAgent reports whether this CONFIG_DIR is an enrolled environment's
// agent, which has agent.toml (ADR 0075). Artifacts there aren't this
// ws's to reach (ADR 0078).
func (a *App) enrolledAgent() bool {
	_, err := os.Stat(filepath.Join(a.paths.Environment, "agent.toml"))
	return err == nil
}

// checkOwner is removeArtifacts' check for agents: an artifact is removed
// only by the tree that created it.
func (c machineArtifactCaller) checkOwner(e *artifact.Entry) error {
	if e.Record.Root != nil && *e.Record.Root == c.root {
		return nil
	}
	return newError(codeArtifactForbidden, fmt.Errorf("%s was created by another workspace tree; ask a person to remove it", e.Record.ID))
}

// newMachineArtifactCmd is ws machine artifact, for agents (ADR 0078).
func (a *App) newMachineArtifactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifact <OP>",
		Short: "Share files with people, and read the ones they shared",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(
		a.newMachineArtifactAddCmd(),
		a.newMachineArtifactListCmd(),
		a.newMachineArtifactShowCmd(),
		a.newMachineArtifactGetCmd(),
		a.newMachineArtifactPutCmd(),
		a.newMachineArtifactRmCmd(),
		a.newArtifactCommentCmd(false),
	)
	return cmd
}

// withMachineCaller runs fn with the caller, after config is loaded.
func (a *App) withMachineCaller(fn func(ctx context.Context, c machineArtifactCaller, args []string, cmd *cobra.Command) error) func(*cobra.Command, []string) error {
	return runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
		c, err := a.machineCaller()
		if err != nil {
			return err
		}
		return fn(cmd.Context(), c, args, cmd)
	}))
}

func (a *App) newMachineArtifactAddCmd() *cobra.Command {
	var opts artifactAddOptions
	cmd := &cobra.Command{
		Use:   "add <PATH|-> [--name <name>] [--title <text>] [--note <text>]",
		Short: "Store a file as a new artifact, for people to read",
		Args:  artifactArgs(1, "a file to add, or - for stdin"),
		RunE: a.withMachineCaller(func(ctx context.Context, c machineArtifactCaller, args []string, _ *cobra.Command) error {
			rec, err := a.addArtifact(ctx, args[0], &opts, c.actor, c.root)
			if err != nil {
				return err
			}
			return a.writeArtifactResult(&artifact.Entry{Record: rec})
		}),
	}
	addArtifactFlags(cmd, &opts)
	return cmd
}

func (a *App) newMachineArtifactListCmd() *cobra.Command {
	var f artifactFilter
	var mine bool
	cmd := &cobra.Command{
		Use:   "list [--kind image|markdown|text] [--mine] [--limit <n>]",
		Short: "List artifacts, latest first",
		Args:  noArgs,
		RunE: a.withMachineCaller(func(_ context.Context, c machineArtifactCaller, _ []string, _ *cobra.Command) error {
			if err := errors.Join(checkArtifactKind(f.kind), checkListLimit(f.limit)); err != nil {
				return err
			}
			f.mine, f.root = mine, c.root
			return a.listArtifacts(&f)
		}),
	}
	cmd.Flags().StringVar(&f.kind, "kind", "", "only artifacts of this kind: image, markdown or text")
	cmd.Flags().BoolVar(&mine, "mine", false, "only artifacts this workspace tree created")
	cmd.Flags().IntVar(&f.limit, "limit", defaultListLimit, "how many to list, from 1 to 1000")
	return cmd
}

func (a *App) newMachineArtifactShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <ID>",
		Short: "Show an artifact and its revisions",
		Args:  artifactArgs(1, "an artifact id"),
		RunE: a.withMachineCaller(func(_ context.Context, _ machineArtifactCaller, args []string, _ *cobra.Command) error {
			return a.showArtifact(args[0])
		}),
	}
}

// machineGetOptions are get's flags.
type machineGetOptions struct {
	rev int
	out string
}

func (a *App) newMachineArtifactGetCmd() *cobra.Command {
	var opts machineGetOptions
	cmd := &cobra.Command{
		Use:   "get <ID> [--rev <n>] [--out <dir>]",
		Short: "Copy an artifact's file into your workspace, and print its path",
		Args:  artifactArgs(1, "an artifact id"),
		RunE: a.withMachineCaller(func(_ context.Context, c machineArtifactCaller, args []string, cmd *cobra.Command) error {
			id, r, err := artifactRef(args[0], opts.rev, cmd.Flags().Changed("rev"))
			if err != nil {
				return err
			}
			return a.getArtifact(&c, id, r, opts.out)
		}),
	}
	cmd.Flags().IntVar(&opts.rev, "rev", 0, "the revision to copy, default the latest")
	cmd.Flags().StringVar(&opts.out, "out", "", "the directory to write into, default <workspace>/.ws-artifacts/<id>/r<n>")
	return cmd
}

// getArtifact copies revision rev of id (0 for the latest) into dir and
// prints where it went.
func (a *App) getArtifact(c *machineArtifactCaller, id string, rev int, out string) error {
	store := a.artifactStore()
	entry, err := store.Show(id)
	if err != nil {
		return artifactError(err)
	}
	rec, revision, data, err := store.Read(id, rev)
	if err != nil {
		return artifactError(err)
	}
	dir, err := artifactDir(c, out, id, revision.Rev)
	if err != nil {
		return err
	}
	path, err := writeArtifactFile(dir, rec.Name, data)
	if err != nil {
		return err
	}
	return a.writeJSON(machineGetJSON{
		Artifact:    summaryJSON(&entry),
		Rev:         revision.Rev,
		Path:        path,
		ContentType: revision.ContentType,
		Size:        revision.Size,
		Warnings:    a.artifactWarnings(nil),
	})
}

// machineGetJSON is get's output: the artifact, and the file it copied to.
type machineGetJSON struct {
	Artifact    artifactSummaryJSON `json:"artifact"`
	Rev         int                 `json:"rev"`
	Path        string              `json:"path"`
	ContentType string              `json:"content_type"`
	Size        int64               `json:"size"`
	Warnings    []string            `json:"warnings"`
}

// artifactDir is where get writes revision rev of id: --out made absolute,
// else the workspace's .ws-artifacts/<id>/r<n>.
func artifactDir(c *machineArtifactCaller, out, id string, rev int) (string, error) {
	if out != "" {
		return filepath.Abs(out)
	}
	return filepath.Join(c.path, ".ws-artifacts", id, fmt.Sprintf("r%d", rev)), nil
}

// writeArtifactFile writes data to dir/name, through a temp file renamed into
// place, so the file is the old one or the new one. dir is 0700, the file 0600.
func writeArtifactFile(dir, name string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("making %s: %w", dir, err)
	}
	dst := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".get-*")
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	if err = os.Rename(tmp.Name(), dst); err != nil {
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	return dst, nil
}

// machinePutOptions are put's flags.
type machinePutOptions struct {
	baseRev int
	note    string
}

func (a *App) newMachineArtifactPutCmd() *cobra.Command {
	var opts machinePutOptions
	cmd := &cobra.Command{
		Use:   "put <ID> <PATH|-> [--base-rev <n>] [--note <text>]",
		Short: "Add a revision to an artifact you shared, for people to compare",
		Args:  artifactArgs(2, "an artifact id and a file"),
		RunE: a.withMachineCaller(func(ctx context.Context, c machineArtifactCaller, args []string, cmd *cobra.Command) error {
			if cmd.Flags().Changed("base-rev") && opts.baseRev < 1 {
				return usageErrorf("--base-rev must be at least 1, not %d", opts.baseRev)
			}
			return a.putArtifact(ctx, &c, args[0], args[1], &opts)
		}),
	}
	cmd.Flags().IntVar(&opts.baseRev, "base-rev", 0, "the revision this one changes; conflict if it isn't the latest")
	cmd.Flags().StringVar(&opts.note, "note", "", "a note on the new revision, up to 200 characters")
	return cmd
}

// putArtifact appends the file at path as a new revision of artifact arg
// names, and prints the artifact with how its threads moved.
func (a *App) putArtifact(ctx context.Context, c *machineArtifactCaller, arg, path string, opts *machinePutOptions) error {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return err
	}
	store := a.artifactStore()
	entry, err := store.Show(id)
	if err != nil {
		return artifactError(err)
	}
	_, data, err := a.readArtifactInput(path, entry.Record.Name)
	if err != nil {
		return err
	}
	res, err := store.Put(ctx, id, &artifact.NewRevision{Data: data, Note: opts.note, By: c.actor, BaseRev: opts.baseRev})
	if err != nil {
		return artifactError(err)
	}
	updated, err := store.Show(id)
	if err != nil {
		return artifactError(err)
	}
	return a.writeJSON(machinePutJSON{
		Artifact: fullJSON(&updated),
		Anchors:  anchorCountsJSON{Anchored: res.Anchored, Detached: res.Detached},
		Warnings: a.artifactWarnings(nil),
	})
}

// machinePutJSON is put's output: the artifact, and how many threads moved.
type machinePutJSON struct {
	Artifact artifactJSON     `json:"artifact"`
	Anchors  anchorCountsJSON `json:"anchors"`
	Warnings []string         `json:"warnings"`
}

// anchorCountsJSON is how many anchors put kept and detached (ADR 0079).
type anchorCountsJSON struct {
	Anchored int `json:"anchored"`
	Detached int `json:"detached"`
}

func (a *App) newMachineArtifactRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <ID>...",
		Short: "Remove artifacts your workspace tree created",
		Args:  atLeastOneArg("an artifact"),
		RunE: a.withMachineCaller(func(ctx context.Context, c machineArtifactCaller, args []string, _ *cobra.Command) error {
			return a.removeArtifacts(ctx, args, c.checkOwner)
		}),
	}
}
