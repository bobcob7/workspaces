package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/machine/role"
)

// newMachineRolesCmd lists the roles children can be started as, and holds
// the operations that manage them (ADR 0050).
func (a *App) newMachineRolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "List the roles children can be started as, or manage them",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(*cobra.Command, []string) error {
			return a.runMachineRoles()
		})),
	}
	cmd.AddCommand(
		a.newMachineRolesShowCmd(),
		a.newMachineRolesAddCmd(),
		a.newMachineRolesEditCmd(),
		a.newMachineRolesRmCmd(),
	)
	return cmd
}

func (a *App) newMachineRolesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <NAME>",
		Short: "Print one role, with its prompt template",
		Args:  exactlyOneArg("a role name"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			return a.runMachineRolesShow(args[0])
		})),
	}
}

// roleOptions are the content flags of ws machine roles add and edit.
type roleOptions struct {
	description, model, promptFile string
	pass, fail, vars               []string // StringSliceVar: repeatable or comma-separated
	claudeArgs                     []string // StringArrayVar "claude-arg": one argument each, kept in order
	noVars, noClaudeArgs           bool
}

func (a *App) newMachineRolesAddCmd() *cobra.Command {
	var opts roleOptions
	cmd := &cobra.Command{
		Use:   "add <NAME> --description <D> [--model <M>] [--pass <V>]... [--fail <V>]... [--vars <K>]... [--claude-arg <A>]... (--prompt-file <F> | -)",
		Short: "Add a role",
		Args:  roleArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			ro, err := a.newRole(cmd, args, &opts)
			if err != nil {
				return err
			}
			return a.runMachineRolesAdd(cmd.Context(), &ro)
		})),
	}
	addRoleFlags(cmd, &opts)
	return cmd
}

func (a *App) newMachineRolesEditCmd() *cobra.Command {
	var opts roleOptions
	cmd := &cobra.Command{
		Use:   "edit <NAME> [--description <D>] [--model <M>] [--pass <V>]... [--fail <V>]... [--vars <K>]... [--no-vars] [--claude-arg <A>]... [--no-claude-args] [--prompt-file <F> | -]",
		Short: "Edit a role; a built-in is copied, then edited",
		Args:  roleArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			changes, err := a.roleChanges(cmd, args, &opts)
			if err != nil {
				return err
			}
			return a.runMachineRolesEdit(cmd.Context(), args[0], changes)
		})),
	}
	addRoleFlags(cmd, &opts)
	cmd.Flags().BoolVar(&opts.noVars, "no-vars", false, "remove every var")
	cmd.Flags().BoolVar(&opts.noClaudeArgs, "no-claude-args", false, "remove every claude argument")
	return cmd
}

func (a *App) newMachineRolesRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <NAME>",
		Short: "Remove your role, or your edits to a built-in",
		Args:  exactlyOneArg("a role name"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runMachineRolesRm(cmd.Context(), args[0])
		})),
	}
}

func addRoleFlags(cmd *cobra.Command, opts *roleOptions) {
	flags := cmd.Flags()
	flags.StringVar(&opts.description, "description", "", "one line saying what the role does")
	flags.StringVar(&opts.model, "model", "", "passed as claude --model; empty for claude's default")
	flags.StringSliceVar(&opts.pass, "pass", nil, "a verdict that means pass; repeatable or comma-separated")
	flags.StringSliceVar(&opts.fail, "fail", nil, "a verdict that means fail; repeatable or comma-separated")
	flags.StringSliceVar(&opts.vars, "vars", nil, "a var name the prompt uses; repeatable or comma-separated")
	flags.StringArrayVar(&opts.claudeArgs, "claude-arg", nil, "one more claude argument; repeatable, kept in order")
	flags.StringVar(&opts.promptFile, "prompt-file", "", "read the prompt template from this file; - as an argument reads stdin")
}

// roleArgs is a role name, then optionally -, for the prompt on stdin.
func roleArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || len(args) > 2 {
		return usageErrorf("%s needs a role name, then optionally -", commandName(cmd))
	}
	if len(args) == 2 && args[1] != "-" {
		return usageErrorf("%s takes - after the name, for the prompt on stdin, not %q", commandName(cmd), args[1])
	}
	return nil
}

// newRole is the role ws machine roles add writes.
func (a *App) newRole(cmd *cobra.Command, args []string, opts *roleOptions) (role.Role, error) {
	if !cmd.Flags().Changed("description") {
		return role.Role{}, usageErrorf("%s needs --description", commandName(cmd))
	}
	prompt, ok, err := a.readRolePrompt(cmd, args, opts)
	if err != nil {
		return role.Role{}, err
	}
	if !ok {
		return role.Role{}, usageErrorf("%s needs the prompt: --prompt-file <F>, or - for stdin", commandName(cmd))
	}
	pass, fail := []string{"PASS"}, []string{"FAIL"}
	if cmd.Flags().Changed("pass") {
		pass = upperVerdicts(opts.pass)
	}
	if cmd.Flags().Changed("fail") {
		fail = upperVerdicts(opts.fail)
	}
	return role.Role{
		Name:        args[0],
		Description: opts.description,
		Model:       opts.model,
		Pass:        pass,
		Fail:        fail,
		Vars:        nonNil(opts.vars),
		ClaudeArgs:  nonNil(opts.claudeArgs),
		Prompt:      prompt,
	}, nil
}

// roleChanges are the fields the flags and arguments set.
func (a *App) roleChanges(cmd *cobra.Command, args []string, opts *roleOptions) (*role.Changes, error) {
	flags := cmd.Flags()
	var changes role.Changes
	if flags.Changed("description") {
		changes.Description = &opts.description
	}
	if flags.Changed("model") {
		changes.Model = &opts.model
	}
	roleVerdictChanges(flags.Changed("pass"), opts.pass, flags.Changed("fail"), opts.fail, &changes)
	if err := roleListChanges(cmd, opts); err != nil {
		return nil, err
	}
	if opts.noVars || flags.Changed("vars") {
		changes.Vars = roleListFlag(opts.noVars, opts.vars)
	}
	if opts.noClaudeArgs || flags.Changed("claude-arg") {
		changes.ClaudeArgs = roleListFlag(opts.noClaudeArgs, opts.claudeArgs)
	}
	prompt, ok, err := a.readRolePrompt(cmd, args, opts)
	if err != nil {
		return nil, err
	}
	if ok {
		changes.Prompt = &prompt
	}
	if changes == (role.Changes{}) {
		return nil, usageErrorf("%s needs something to change", commandName(cmd))
	}
	return &changes, nil
}

// roleVerdictChanges sets changes' pass and fail when their flags are given.
func roleVerdictChanges(passed bool, pass []string, failed bool, fail []string, changes *role.Changes) {
	if passed {
		verdicts := upperVerdicts(pass)
		changes.Pass = &verdicts
	}
	if failed {
		verdicts := upperVerdicts(fail)
		changes.Fail = &verdicts
	}
}

// roleListChanges refuses --vars with --no-vars, and --claude-arg with
// --no-claude-args.
func roleListChanges(cmd *cobra.Command, opts *roleOptions) error {
	flags := cmd.Flags()
	if opts.noVars && flags.Changed("vars") {
		return usageErrorf("%s: --vars and --no-vars can't both be given", commandName(cmd))
	}
	if opts.noClaudeArgs && flags.Changed("claude-arg") {
		return usageErrorf("%s: --claude-arg and --no-claude-args can't both be given", commandName(cmd))
	}
	return nil
}

// roleListFlag is the list a flag sets: empty for a --no-… flag.
func roleListFlag(empty bool, list []string) *[]string {
	if empty {
		return &[]string{}
	}
	list = nonNil(list)
	return &list
}

// upperVerdicts is verdicts in upper case, as role files store them.
func upperVerdicts(verdicts []string) []string {
	out := make([]string, 0, len(verdicts))
	for _, v := range verdicts {
		out = append(out, strings.ToUpper(v))
	}
	return out
}

// readRolePrompt reads the prompt from --prompt-file, or stdin when the
// arguments end in -; ok is false for neither.
func (a *App) readRolePrompt(cmd *cobra.Command, args []string, opts *roleOptions) (prompt string, ok bool, err error) {
	fromFile := cmd.Flags().Changed("prompt-file")
	fromStdin := len(args) == 2
	var raw []byte
	switch {
	case fromFile && fromStdin:
		return "", false, usageErrorf("%s: --prompt-file and - both give the prompt; use one", commandName(cmd))
	case fromFile:
		raw, err = os.ReadFile(opts.promptFile)
	case fromStdin:
		raw, err = io.ReadAll(a.stdin)
	default:
		return "", false, nil
	}
	if err != nil {
		return "", false, usageErrorf("reading the prompt: %v", err)
	}
	return string(raw), true, nil
}

// machineRoleInfoJSON is one role as ws machine roles lists it.
type machineRoleInfoJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Model       string   `json:"model"`
	Pass        []string `json:"pass"`
	Fail        []string `json:"fail"`
	Vars        []string `json:"vars"`
	ClaudeArgs  []string `json:"claude_args"`
	Source      string   `json:"source"`
}

// machineRolesJSON is ws machine roles' output.
type machineRolesJSON struct {
	Roles    []machineRoleInfoJSON `json:"roles"`
	Warnings []string              `json:"warnings"`
}

// machineRoleDetailJSON is ws machine roles show's, add's, and edit's
// output: the role, its prompt, and its file, null for a built-in.
type machineRoleDetailJSON struct {
	machineRoleInfoJSON
	Prompt   string   `json:"prompt"`
	Path     *string  `json:"path"`
	Warnings []string `json:"warnings"`
}

// machineRoleRemovedJSON is ws machine roles rm's output.
type machineRoleRemovedJSON struct {
	Name string `json:"name"`
	// Path is the file removed; Restored is set when a built-in of the
	// name is back.
	Path     string   `json:"path"`
	Restored bool     `json:"restored"`
	Warnings []string `json:"warnings"`
}

// roleInfo is ro as the JSON output lists it.
func roleInfo(ro *role.Role) machineRoleInfoJSON {
	return machineRoleInfoJSON{
		Name:        ro.Name,
		Description: ro.Description,
		Model:       ro.Model,
		Pass:        nonNil(ro.Pass),
		Fail:        nonNil(ro.Fail),
		Vars:        nonNil(ro.Vars),
		ClaudeArgs:  nonNil(ro.ClaudeArgs),
		Source:      string(ro.Source),
	}
}

// runMachineRoles lists every role, built in and the user's.
func (a *App) runMachineRoles() error {
	roles, broken, err := role.NewSet(a.paths.MachineRoles).List()
	if err != nil {
		return err
	}
	a.warnBrokenRoles(broken)
	out := make([]machineRoleInfoJSON, 0, len(roles))
	for i := range roles {
		out = append(out, roleInfo(&roles[i]))
	}
	return a.writeJSON(machineRolesJSON{Roles: out, Warnings: a.warnings()})
}

// warnBrokenRoles adds a warning for each role file that couldn't be parsed.
func (a *App) warnBrokenRoles(broken []string) {
	for _, file := range broken {
		a.warned = append(a.warned, fmt.Sprintf("skipped role file %s: it can't be parsed", file))
	}
}

// runMachineRolesShow prints one role and its prompt.
func (a *App) runMachineRolesShow(name string) error {
	ro, ok, err := role.NewSet(a.paths.MachineRoles).Load(name)
	if err != nil {
		return newError(codeConfigInvalid, err)
	}
	if !ok {
		return newError(codeRoleUnknown, fmt.Errorf("no role named %q; ws machine roles lists them", name))
	}
	return a.printRole(&ro)
}

// runMachineRolesAdd writes a new role and prints it.
func (a *App) runMachineRolesAdd(ctx context.Context, ro *role.Role) error {
	added, err := role.NewWriter(a.paths.MachineRoles).Add(ctx, *ro)
	if err != nil {
		return roleWriteError(err)
	}
	return a.printRole(&added)
}

// runMachineRolesEdit changes a role and prints it.
func (a *App) runMachineRolesEdit(ctx context.Context, name string, changes *role.Changes) error {
	edited, err := role.NewWriter(a.paths.MachineRoles).Edit(ctx, name, changes)
	if err != nil {
		return roleWriteError(err)
	}
	return a.printRole(&edited)
}

// runMachineRolesRm removes the user's file for a role, and says whether a
// built-in is back.
func (a *App) runMachineRolesRm(ctx context.Context, name string) error {
	w := role.NewWriter(a.paths.MachineRoles)
	source, err := w.Remove(ctx, name)
	if err != nil {
		return roleWriteError(err)
	}
	return a.writeJSON(machineRoleRemovedJSON{Name: name, Path: w.Path(name), Restored: source == role.SourceOverride, Warnings: a.warnings()})
}

// printRole writes ro with its prompt and file.
func (a *App) printRole(ro *role.Role) error {
	out := machineRoleDetailJSON{machineRoleInfoJSON: roleInfo(ro), Prompt: ro.Prompt, Warnings: a.warnings()}
	if ro.Source != role.SourceBuiltin {
		path := role.NewWriter(a.paths.MachineRoles).Path(ro.Name)
		out.Path = &path
	}
	return a.writeJSON(out)
}

// roleWriteError gives a role.Writer or role.Set error its code (ADR 0050).
func roleWriteError(err error) error {
	switch {
	case errors.Is(err, role.ErrInvalid):
		return newError(codeRoleInvalid, err)
	case errors.Is(err, role.ErrExists):
		return newError(codeRoleExists, err)
	case errors.Is(err, role.ErrBuiltin):
		return newError(codeRoleBuiltin, err)
	case errors.Is(err, role.ErrUnknown):
		return newError(codeRoleUnknown, err)
	case errors.Is(err, role.ErrBroken):
		return newError(codeConfigInvalid, err)
	}
	return err
}
