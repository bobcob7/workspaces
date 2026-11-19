package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/machine"
)

type recordResultArgs struct {
	Verdict string `json:"verdict"`
	Report  string `json:"report"`
}

func recordResultTool(cfg *Config) Tool {
	return Tool{
		Name:        "record_result",
		Description: "Record the verdict with its markdown report and end the run.",
		Parameters: objectSchema(map[string]map[string]any{
			"verdict": field("string", "one of the verdicts the preamble lists"),
			"report":  field("string", "markdown report with the evidence for the verdict"),
		}, "verdict", "report"),
		Call: typed(func(ctx context.Context, args recordResultArgs) (Result, error) {
			return recordResult(ctx, cfg, args)
		}),
	}
}

func recordResult(ctx context.Context, cfg *Config, args recordResultArgs) (Result, error) {
	if args.Verdict == "" {
		return Result{}, errors.New("missing verdict")
	}
	if args.Report == "" {
		return Result{}, errors.New("missing report")
	}
	verdict, err := machine.NormalizeVerdict(args.Verdict)
	if err != nil {
		return Result{}, err
	}
	if !slices.Contains(cfg.Verdicts, verdict) {
		return Result{}, fmt.Errorf("verdict %s is not one of %s", verdict, strings.Join(cfg.Verdicts, ", "))
	}
	if err := cfg.Recorder.Record(ctx, verdict, []byte(args.Report)); err != nil {
		return Result{}, fmt.Errorf("recording %s: %w", verdict, err)
	}
	return Result{Text: "recorded " + verdict, Done: true, Verdict: verdict}, nil
}
