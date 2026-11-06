package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// writeReportFile writes content to a file in t's temp dir and returns its path.
func writeReportFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "r.md")
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

func TestApp_runMachineResult(t *testing.T) {
	reportPath := func(e *mockedApp, root, child string) string {
		return filepath.Join(machine.NewStore(e.app.paths.State).Dir(root, child), "report.md")
	}
	t.Run("records a verdict and report from plan, and prints it", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.global.json = true
		file := writeReportFile(t, []byte("# Report\n"))
		require.NoError(t, e.app.runMachineResult("pass", file))
		want := fmt.Sprintf(`{"id":"plan","root":"boss","role":null,"result":{"verdict":"PASS","outcome":"pass","report":%q,"recorded_at":%q},"warnings":[]}`,
			reportPath(e, "boss", "plan"), formatTime(testNow()))
		assert.JSONEq(t, want, e.stdout.String())
		kept, err := os.ReadFile(reportPath(e, "boss", "plan"))
		require.NoError(t, err)
		assert.Equal(t, "# Report\n", string(kept))
	})
	t.Run("a verdict without a report deletes the earlier one", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.global.json = true
		require.NoError(t, e.app.runMachineResult("pass", writeReportFile(t, []byte("first"))))
		e.stdout.Reset()
		require.NoError(t, e.app.runMachineResult("FAIL", ""))
		assert.JSONEq(t, `{"verdict":"FAIL","outcome":"fail","report":null,"recorded_at":"`+formatTime(testNow())+`"}`,
			jsonField(t, e.stdout.String(), "result"))
		_, err := os.Stat(reportPath(e, "boss", "plan"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("reads the report from stdin", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.stdin = strings.NewReader("ok")
		require.NoError(t, e.app.runMachineResult("pass", "-"))
		kept, err := os.ReadFile(reportPath(e, "boss", "plan"))
		require.NoError(t, err)
		assert.Equal(t, "ok", string(kept))
	})
	t.Run("a verdict that isn't a verdict is result_invalid", func(t *testing.T) {
		e := newMachineApp(t)
		requireCode(t, codeResultInvalid, e.app.runMachineResult("no way", ""))
	})
	t.Run("a verdict outside the role's lists is result_invalid", func(t *testing.T) {
		e := newMachineApp(t)
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteSpawn("boss", "plan", &machine.Spawn{
			Role: "reviewer",
			Pass: []string{"PASS", "FLAKY"},
			Fail: []string{"FAIL"},
		}))
		err := e.app.runMachineResult("APPROVED", "")
		requireCode(t, codeResultInvalid, err)
		require.ErrorContains(t, err, "verdict APPROVED isn't one of role reviewer's: PASS, FLAKY, FAIL")
		require.NoError(t, e.app.runMachineResult("flaky", ""))
	})
	t.Run("an empty report is result_invalid", func(t *testing.T) {
		e := newMachineApp(t)
		requireCode(t, codeResultInvalid, e.app.runMachineResult("PASS", writeReportFile(t, nil)))
	})
	t.Run("a report over the limit is result_invalid", func(t *testing.T) {
		e := newMachineApp(t)
		big := make([]byte, machine.MaxReportSize+1)
		requireCode(t, codeResultInvalid, e.app.runMachineResult("PASS", writeReportFile(t, big)))
	})
	t.Run("a missing report is result_invalid", func(t *testing.T) {
		e := newMachineApp(t)
		requireCode(t, codeResultInvalid, e.app.runMachineResult("PASS", filepath.Join(t.TempDir(), "nope.md")))
	})
	t.Run("outside a workspace", func(t *testing.T) {
		e := newMachineApp(t)
		e.outside()
		requireCode(t, codeOutsideWorkspace, e.app.runMachineResult("PASS", ""))
	})
	t.Run("a listing that fails", func(t *testing.T) {
		e := newMachineApp(t)
		e.store.ListFunc = func() ([]workspace.Workspace, error) { return nil, errTest }
		assert.ErrorIs(t, e.app.runMachineResult("PASS", ""), errTest)
	})
}

func TestApp_newMachineResultCmd(t *testing.T) {
	e := newMachineApp(t)
	cmd := e.app.newMachineResultCmd()
	t.Run("takes exactly one verdict", func(t *testing.T) {
		requireCode(t, codeUsage, cmd.Args(cmd, nil))
		requireCode(t, codeUsage, cmd.Args(cmd, []string{"PASS", "FAIL"}))
		assert.NoError(t, cmd.Args(cmd, []string{"PASS"}))
	})
	t.Run("has a --file flag", func(t *testing.T) {
		assert.NotNil(t, cmd.Flags().Lookup("file"))
	})
}

func TestApp_readReport(t *testing.T) {
	t.Run("no file is no report", func(t *testing.T) {
		e := newMachineApp(t)
		report, err := e.app.readReport("")
		require.NoError(t, err)
		assert.Nil(t, report)
	})
	t.Run("- reads stdin", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.stdin = strings.NewReader("from stdin")
		report, err := e.app.readReport("-")
		require.NoError(t, err)
		assert.Equal(t, "from stdin", string(report))
	})
	t.Run("a file is read", func(t *testing.T) {
		e := newMachineApp(t)
		report, err := e.app.readReport(writeReportFile(t, []byte("from file")))
		require.NoError(t, err)
		assert.Equal(t, "from file", string(report))
	})
	t.Run("a report of exactly the limit is kept", func(t *testing.T) {
		e := newMachineApp(t)
		report, err := e.app.readReport(writeReportFile(t, make([]byte, machine.MaxReportSize)))
		require.NoError(t, err)
		assert.Len(t, report, machine.MaxReportSize)
	})
	t.Run("empty stdin is result_invalid", func(t *testing.T) {
		e := newMachineApp(t)
		e.app.stdin = strings.NewReader("")
		_, err := e.app.readReport("-")
		requireCode(t, codeResultInvalid, err)
	})
	t.Run("a directory is result_invalid", func(t *testing.T) {
		e := newMachineApp(t)
		_, err := e.app.readReport(t.TempDir())
		requireCode(t, codeResultInvalid, err)
	})
}

func TestResultInfo(t *testing.T) {
	recordedAt := testNow()
	t.Run("nil without a result", func(t *testing.T) {
		assert.Nil(t, resultInfo(&machine.Record{Child: "a"}))
	})
	t.Run("a default verdict passes", func(t *testing.T) {
		info := resultInfo(&machine.Record{Result: &machine.Result{Verdict: "APPROVED", RecordedAt: recordedAt}})
		require.NotNil(t, info)
		assert.Equal(t, "APPROVED", info.Verdict)
		assert.Equal(t, "pass", info.Outcome)
		assert.Nil(t, info.Report)
		assert.Equal(t, formatTime(recordedAt), info.RecordedAt)
	})
	t.Run("a role's fail verdict fails, and keeps its report path", func(t *testing.T) {
		rec := &machine.Record{
			Spawn:      &machine.Spawn{Pass: []string{"PASS"}, Fail: []string{"FAIL"}},
			Result:     &machine.Result{Verdict: "FAIL", RecordedAt: recordedAt, Report: true},
			ReportPath: "/state/root/machine/a/report.md",
		}
		info := resultInfo(rec)
		require.NotNil(t, info)
		assert.Equal(t, "fail", info.Outcome)
		require.NotNil(t, info.Report)
		assert.Equal(t, "/state/root/machine/a/report.md", *info.Report)
	})
}

// jsonField is the JSON value of key in the object out.
func jsonField(t *testing.T, out, key string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &fields))
	raw, ok := fields[key]
	require.True(t, ok, "no %s in %s", key, out)
	return string(raw)
}
