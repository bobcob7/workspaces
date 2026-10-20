package cli

import (
	"encoding/json"
	"fmt"
	"os"
)

type errorBody struct {
	Code    errorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// writeError reports a failure on stderr: "ws: <message>", or with --json a
// single {"error": {...}} line (ADR 0016). stdout stays empty.
func (a *App) writeError(e *cliError) {
	if !a.global.json {
		fmt.Fprintf(a.stderr, "ws: %s\n", e.Error())
		return
	}
	body := map[string]errorBody{"error": {Code: e.code, Message: e.Error(), Details: e.details}}
	if err := json.NewEncoder(a.stderr).Encode(body); err != nil {
		fmt.Fprintf(a.stderr, "ws: %s\n", e.Error())
	}
}

// writeJSON prints one result object on stdout: compact, or indented when
// stdout is a terminal (ADR 0016).
func (a *App) writeJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	if a.indentJSON {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("writing JSON output: %w", err)
	}
	return nil
}

// isTerminal reports whether stream, a reader or writer, is a terminal.
func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// showProgress reports whether progress goes to stderr: only when it is a
// terminal and --quiet isn't set (ADR 0016).
func (a *App) showProgress() bool {
	return a.progress && !a.global.quiet
}
