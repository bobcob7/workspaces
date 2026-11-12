package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/machine"
)

// machineResultInfoJSON is a child's recorded result (ADR 0050).
type machineResultInfoJSON struct {
	Verdict    string  `json:"verdict"`
	Outcome    string  `json:"outcome"`
	Report     *string `json:"report"`
	RecordedAt string  `json:"recorded_at"`
}

// resultInfo is rec's result, or nil when it has none.
func resultInfo(rec *machine.Record) *machineResultInfoJSON {
	if rec.Result == nil {
		return nil
	}
	outcome, _ := rec.Outcome()
	return &machineResultInfoJSON{
		Verdict:    rec.Result.Verdict,
		Outcome:    string(outcome),
		Report:     optional(rec.ReportPath),
		RecordedAt: formatTime(rec.Result.RecordedAt),
	}
}

// machineResultJSON is ws machine result's output.
type machineResultJSON struct {
	ID       string                `json:"id"`
	Root     string                `json:"root"`
	Role     *string               `json:"role"`
	Result   machineResultInfoJSON `json:"result"`
	Warnings []string              `json:"warnings"`
}

// machineChildJSON is ws list's workspace plus its role and result.
type machineChildJSON struct {
	listWorkspaceJSON
	Role   *string                `json:"role"`
	Result *machineResultInfoJSON `json:"result"`
}

// machineChildrenJSON is ws machine children's output.
type machineChildrenJSON struct {
	Workspaces  []machineChildJSON `json:"workspaces"`
	HiddenCount int                `json:"hidden_count"`
	Warnings    []string           `json:"warnings"`
}

func (a *App) newMachineResultCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "result <VERDICT> [--file <PATH> | --file -]",
		Short: "Record your verdict and report for the agent that started you",
		Args:  exactlyOneArg("a verdict"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			return a.runMachineResult(args[0], file)
		})),
	}
	cmd.Flags().StringVar(&file, "file", "", "copy this file as the report; - reads it from stdin")
	return cmd
}

// runMachineResult records the caller's verdict and report under its root,
// and prints the result as it now reads back.
func (a *App) runMachineResult(verdict, file string) error {
	caller, err := a.caller()
	if err != nil {
		return err
	}
	list, err := a.workspaces.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	root := machine.Root(list, caller)
	v, err := machine.NormalizeVerdict(verdict)
	if err != nil {
		return newError(codeResultInvalid, err)
	}
	if v == machine.VerdictBlocked {
		if err = a.checkBlocked(caller, root, file); err != nil {
			return err
		}
	}
	store := machine.NewStore(a.paths.State)
	rec, err := store.Read(root, caller)
	if err != nil {
		return fmt.Errorf("reading %s's machine record: %w", caller, err)
	}
	if rec.Spawn != nil && !rec.Spawn.Allows(v) {
		allowed := strings.Join(slices.Concat(rec.Spawn.Pass, rec.Spawn.Fail), ", ")
		return newError(codeResultInvalid, fmt.Errorf("verdict %s isn't one of role %s's: %s", v, rec.Spawn.Role, allowed))
	}
	report, err := a.readReport(file)
	if err != nil {
		return err
	}
	result := &machine.Result{
		Verdict:    v,
		Ancestors:  machine.Ancestors(list, caller),
		RecordedAt: a.now().UTC().Truncate(time.Second),
	}
	if err = store.WriteResult(root, caller, result, report); err != nil {
		return fmt.Errorf("recording %s's result: %w", caller, err)
	}
	rec, err = store.Read(root, caller)
	if err != nil {
		return fmt.Errorf("reading %s's machine record: %w", caller, err)
	}
	info := resultInfo(&rec)
	if info == nil {
		return fmt.Errorf("reading back %s's result: none recorded", caller)
	}
	return a.writeJSON(machineResultJSON{ID: caller, Root: root, Role: optional(rec.Role()), Result: *info, Warnings: a.warnings()})
}

// errBlockedNotRun is the refusal of BLOCKED outside a loop run's own verdict.
var errBlockedNotRun = errors.New("BLOCKED is only for a loop run's own verdict; record FAIL instead")

// checkBlocked allows caller to record BLOCKED only as a loop run's root,
// with the question in file (ADR 0057).
func (a *App) checkBlocked(caller, root, file string) error {
	if root != caller {
		return newError(codeResultInvalid, errBlockedNotRun)
	}
	_, found, err := a.loopData.ReadRunMark(caller)
	if err != nil {
		return fmt.Errorf("reading %s's run mark: %w", caller, err)
	}
	if !found {
		return newError(codeResultInvalid, errBlockedNotRun)
	}
	if file == "" {
		return newError(codeResultInvalid, errors.New("BLOCKED needs --file with your question"))
	}
	return nil
}

// readReport reads --file: "" is no report (nil, nil); "-" is a.stdin;
// anything else the file. Empty, over machine.MaxReportSize, or
// unreadable is result_invalid.
func (a *App) readReport(file string) ([]byte, error) {
	if file == "" {
		return nil, nil
	}
	src := a.stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, newError(codeResultInvalid, fmt.Errorf("reading the report: %w", err))
		}
		defer f.Close()
		src = f
	}
	report, err := io.ReadAll(io.LimitReader(src, machine.MaxReportSize+1))
	if err != nil {
		return nil, newError(codeResultInvalid, fmt.Errorf("reading the report: %w", err))
	}
	switch {
	case len(report) == 0:
		return nil, newError(codeResultInvalid, errors.New("the report is empty"))
	case len(report) > machine.MaxReportSize:
		return nil, newError(codeResultInvalid, fmt.Errorf("the report is over %d bytes", machine.MaxReportSize))
	}
	return report, nil
}

// machineChild is ws list's workspace with its role and result from store.
// A record that can't be read is a warning, and leaves both null.
func (a *App) machineChild(store *machine.Store, root string, ws *listWorkspaceJSON) machineChildJSON {
	out := machineChildJSON{listWorkspaceJSON: *ws}
	rec, err := store.Read(root, *ws.ID)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read %s's machine record: %v", *ws.ID, err))
		return out
	}
	out.Role = optional(rec.Role())
	out.Result = resultInfo(&rec)
	return out
}
