package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/environment"
)

// newEnvCACmd is ws env ca: rotate the environments CA and show its status (ADR 0075).
func (a *App) newEnvCACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ca <OP>",
		Short: "Rotate the environments CA, and show its status",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newEnvCARotateCmd(), a.newEnvCAStatusCmd())
	return cmd
}

// envCARotateOptions are ws env ca rotate's flags.
type envCARotateOptions struct {
	finish bool
	force  bool
}

func (a *App) newEnvCARotateCmd() *cobra.Command {
	var opts envCARotateOptions
	cmd := &cobra.Command{
		Use:   "rotate [--finish] [--force]",
		Short: "Make a new environments CA, or finish a rotation",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.runEnvCARotate(cmd.Context(), opts)
		})),
	}
	cmd.Flags().BoolVar(&opts.finish, "finish", false, "drop the old CA once no environment holds its certificates")
	cmd.Flags().BoolVar(&opts.force, "force", false, "with --finish, drop the old CA even while environments hold its certificates")
	return cmd
}

func (a *App) newEnvCAStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the environments CA and how many environments hold each",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.runEnvCAStatus(cmd.Context())
		})),
	}
}

// caJSON is one CA in ws env ca's output.
type caJSON struct {
	Fingerprint string `json:"fingerprint"`
	NotAfter    string `json:"not_after"`
}

// envCARotateJSON is ws env ca rotate's output.
type envCARotateJSON struct {
	Current  *caJSON  `json:"current"`
	Old      *caJSON  `json:"old"`
	Dropped  *caJSON  `json:"dropped"`
	Stranded []string `json:"stranded"`
	Warnings []string `json:"warnings"`
}

// envCAStatusJSON is ws env ca status's output.
type envCAStatusJSON struct {
	Current               *caJSON  `json:"current"`
	Old                   *caJSON  `json:"old"`
	EnvironmentsOnCurrent int      `json:"environments_on_current"`
	EnvironmentsOnOld     int      `json:"environments_on_old"`
	Warnings              []string `json:"warnings"`
}

// ensureCA finishes a CA rotation that an interruption left unfinished. It
// runs before ws env ca reads or rotates the CA, which need ca.crt in place.
func (a *App) ensureCA(ctx context.Context) error {
	finished, err := a.envStore().EnsureCA(ctx)
	if err != nil {
		return envError(err)
	}
	if finished {
		a.warned = append(a.warned, "finished a CA rotation that an interruption had left unfinished")
		a.warned = append(a.warned, a.reloadEnvironments(ctx)...)
	}
	return nil
}

// runEnvCARotate makes a new CA, which the old one stays trusted beside until
// --finish. With --finish it drops the old CA instead.
func (a *App) runEnvCARotate(ctx context.Context, opts envCARotateOptions) error {
	if opts.force && !opts.finish {
		return usageErrorf("--force needs --finish")
	}
	if err := a.ensureCA(ctx); err != nil {
		return err
	}
	if opts.finish {
		return a.runEnvCAFinish(ctx, opts.force)
	}
	rot, err := a.envStore().RotateCA(ctx)
	if err != nil {
		return envError(err)
	}
	a.warned = append(a.warned, a.reloadEnvironments(ctx)...)
	if len(rot.Stranded) > 0 {
		a.warned = append(a.warned, strandedWarning(rot.Stranded))
	}
	if a.global.json {
		return a.writeJSON(envCARotateJSON{Current: caJSONOf(rot.Current), Old: caJSONOf(rot.Old), Dropped: caJSONOf(rot.Dropped), Stranded: rot.Stranded, Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "Rotated: new CA %s; the old CA stays trusted until ws env ca rotate --finish.\n", shortFingerprint(rot.Current.Fingerprint))
	a.printWarnings()
	return nil
}

// runEnvCAFinish drops the old CA. Environments still on it block that unless
// force is set, and then they are listed as stranded.
func (a *App) runEnvCAFinish(ctx context.Context, force bool) error {
	fin, err := a.envStore().FinishCA(ctx, force)
	if err != nil {
		return envError(err)
	}
	a.warned = append(a.warned, a.reloadEnvironments(ctx)...)
	if len(fin.Stranded) > 0 {
		a.warned = append(a.warned, strandedWarning(fin.Stranded))
	}
	if a.global.json {
		return a.writeJSON(envCARotateJSON{Current: caJSONOf(fin.Current), Dropped: caJSONOf(fin.Dropped), Stranded: fin.Stranded, Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "Finished: dropped the old CA %s; the current CA is %s.\n", shortFingerprint(fin.Dropped.Fingerprint), shortFingerprint(fin.Current.Fingerprint))
	a.printWarnings()
	return nil
}

// runEnvCAStatus shows the current and old CA and how many environments hold
// a certificate from each.
func (a *App) runEnvCAStatus(ctx context.Context) error {
	if err := a.ensureCA(ctx); err != nil {
		return err
	}
	st, err := a.envStore().CAStatus(a.config.Environments.RenewDays())
	if err != nil {
		return envError(err)
	}
	a.warned = append(a.warned, st.Warnings...)
	if a.global.json {
		return a.writeJSON(envCAStatusJSON{
			Current:               caJSONOf(st.Current),
			Old:                   caJSONOf(st.Old),
			EnvironmentsOnCurrent: st.OnCurrent,
			EnvironmentsOnOld:     st.OnOld,
			Warnings:              a.warnings(),
		})
	}
	a.printCAStatus(st)
	a.printWarnings()
	return nil
}

// printCAStatus is the text form of ws env ca status.
func (a *App) printCAStatus(st environment.CAStatus) {
	if st.Current == nil {
		fmt.Fprintln(a.stdout, "no environments CA yet; ws env add creates one")
		return
	}
	line := fmt.Sprintf("current CA %s expires %s", shortFingerprint(st.Current.Fingerprint), st.Current.NotAfter.UTC().Format(time.DateOnly))
	if st.Old != nil {
		line += fmt.Sprintf("; old CA %s (%s still on it)", shortFingerprint(st.Old.Fingerprint), plural(st.OnOld, "environment"))
	}
	fmt.Fprintln(a.stdout, line)
}

// caJSONOf is ca as JSON, or nil.
func caJSONOf(ca *environment.CA) *caJSON {
	if ca == nil {
		return nil
	}
	return &caJSON{Fingerprint: ca.Fingerprint, NotAfter: formatTime(ca.NotAfter)}
}

// shortFingerprint is the first four characters of fingerprint and an ellipsis.
func shortFingerprint(fingerprint string) string {
	if len(fingerprint) < 4 {
		return fingerprint
	}
	return fingerprint[:4] + "…"
}
