package cli

import (
	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/loop"
)

type loopEditOptions struct {
	every, prompt, model, permissionMode string
	script, scriptTimeout                string
	vars                                 []string
	keep                                 int
	noScript, noVars, noSpecs            bool
}

func (a *App) newLoopEditCmd() *cobra.Command {
	var opts loopEditOptions
	cmd := &cobra.Command{
		Use:   "edit [OPTIONS] <NAME> [<SPEC>...]",
		Short: "Change a loop in place",
		Args:  cobra.MinimumNArgs(1),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runLoopEdit(cmd, args, &opts)
		})),
	}
	cmd.Flags().StringVar(&opts.every, "every", "", "how often a run starts: whole minutes from 5m to 168h, such as 15m or 24h")
	cmd.Flags().StringVar(&opts.prompt, "prompt", "", "the prompt each run starts with")
	cmd.Flags().IntVar(&opts.keep, "keep", 0, "how many runs' workspaces to keep, 1 to 50")
	cmd.Flags().StringVar(&opts.model, "model", "", "passed to claude --model; \"\" clears it")
	cmd.Flags().StringVar(&opts.permissionMode, "permission-mode", "", "passed to claude --permission-mode; \"\" clears it")
	cmd.Flags().StringVar(&opts.script, "script", "", "a shell script that must exit 0 for a run to start; blank is the same as --no-script")
	cmd.Flags().StringVar(&opts.scriptTimeout, "script-timeout", "", "how long the script may run, 1s to 10m (default 1m for a script the loop didn't have)")
	cmd.Flags().StringArrayVar(&opts.vars, "var", nil, "KEY=VALUE for {{.Vars.KEY}} in the prompt and $WS_LOOP_VAR_KEY in the script; repeatable; replaces all the variables")
	cmd.Flags().BoolVar(&opts.noScript, "no-script", false, "remove the script, its timeout, and its last gate")
	cmd.Flags().BoolVar(&opts.noVars, "no-vars", false, "remove every variable")
	cmd.Flags().BoolVar(&opts.noSpecs, "no-specs", false, "remove every spec, so runs get an empty workspace")
	return cmd
}

// loopEditOf builds the loop.Edit from the flags that were set
// (cmd.Flags().Changed(name)) and the specs. It returns a usage error for
// conflicting flags or when nothing would change, spec_invalid for a bad
// spec (validSpecs), and loop_invalid for a bad --var (loop.ParseVars,
// wrapped with domainError).
func loopEditOf(cmd *cobra.Command, specs []string, opts *loopEditOptions) (loop.Edit, error) {
	if err := checkLoopEditFlags(cmd, specs); err != nil {
		return loop.Edit{}, err
	}
	e := loopEditFields(cmd.Flags().Changed, opts)
	if err := setLoopEditVars(cmd.Flags().Changed, opts, &e); err != nil {
		return loop.Edit{}, err
	}
	if cmd.Flags().Changed("no-specs") {
		e.Specs = &[]string{}
	} else if len(specs) > 0 {
		e.Specs = &specs
	}
	if e.Empty() {
		return loop.Edit{}, usageErrorf("nothing to change: give an option or a spec")
	}
	return e, nil
}

// loopEditFields sets the fields of the edit that were given as flags: the
// period, prompt, keep, model, permission mode, and script.
func loopEditFields(set func(string) bool, opts *loopEditOptions) loop.Edit {
	var e loop.Edit
	if set("every") {
		e.Every = &opts.every
	}
	if set("prompt") {
		e.Prompt = &opts.prompt
	}
	if set("keep") {
		e.Keep = &opts.keep
	}
	if set("model") {
		e.Model = &opts.model
	}
	if set("permission-mode") {
		e.PermissionMode = &opts.permissionMode
	}
	if set("script-timeout") {
		e.ScriptTimeout = &opts.scriptTimeout
	}
	if set("no-script") {
		blank := ""
		e.Script = &blank
	} else if set("script") {
		e.Script = &opts.script
	}
	return e
}

// setLoopEditVars sets the vars of e from --no-vars or --var.
func setLoopEditVars(set func(string) bool, opts *loopEditOptions, e *loop.Edit) error {
	if set("no-vars") {
		e.Vars = &map[string]string{}
		return nil
	}
	if !set("var") {
		return nil
	}
	vars, err := loop.ParseVars(opts.vars)
	if err != nil {
		return domainError(err)
	}
	e.Vars = &vars
	return nil
}

// checkLoopEditFlags refuses the flag combinations that contradict each
// other, and specs that can't be parsed.
func checkLoopEditFlags(cmd *cobra.Command, specs []string) error {
	set := cmd.Flags().Changed
	switch {
	case set("no-script") && (set("script") || set("script-timeout")):
		return usageErrorf("--no-script can't be given with --script or --script-timeout")
	case set("no-vars") && set("var"):
		return usageErrorf("--no-vars can't be given with --var")
	case set("no-specs") && len(specs) > 0:
		return usageErrorf("--no-specs can't be given with specs")
	}
	return validSpecs(cmd, specs)
}

func (a *App) runLoopEdit(cmd *cobra.Command, args []string, opts *loopEditOptions) error {
	edit, err := loopEditOf(cmd, args[1:], opts)
	if err != nil {
		return err
	}
	l, err := a.changeLoop(cmd.Context(), args[0], func(l *loop.Loop) (bool, error) {
		return true, l.Apply(&edit, a.now().UTC())
	})
	if err != nil {
		return err
	}
	return a.writeLoop(&l)
}
