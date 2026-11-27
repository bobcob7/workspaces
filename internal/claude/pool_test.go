package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// poolClient is the method set of Client that Pool mirrors (ADR 0085).
type poolClient interface {
	Check(ctx context.Context) error
	Start(ctx context.Context, opts StartOptions) (Started, error)
	Trust(ctx context.Context, dir, parent string, record func(dir string, created bool) error) error
	Untrust(dir string, created bool) error
	Attach(ctx context.Context, id string) error
	Agents(ctx context.Context, opts ListOptions) ([]Session, error)
	Stop(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	Respawn(ctx context.Context, id string) error
	JobExists(id string) (bool, error)
	DaemonStatus(ctx context.Context) (DaemonStatus, error)
	StopDaemon(ctx context.Context) error
	RunDaemon(ctx context.Context) (int, error)
	UsageLimit(s *Session, message string) (*Limit, error)
	Model(s *Session) (string, error)
	Send(ctx context.Context, s *Session, message string) error
	Logs(ctx context.Context, id string) (string, error)
	SignedIn(ctx context.Context, dir string, extra []string) (bool, string, error)
	Recorded(s *Session, message string, since time.Time) (bool, error)
}

var _ poolClient = (*Pool)(nil)

// poolBody is the fake claude's body for a pool. Every call is logged to
// $LOG as "<config dir> <args>". `agents` prints listings[config dir], and
// fails for a config dir whose listing is "". `--bg` prints a session
// "ab12cd34", so Start's output parses.
func poolBody(listings map[string]string) string {
	var b strings.Builder
	b.WriteString(`echo "$CLAUDE_CONFIG_DIR $*" >> "$LOG"` + "\n")
	b.WriteString(`if [ "$1" = --bg ]; then echo "backgrounded · ab12cd34 · x"; exit 0; fi` + "\n")
	b.WriteString(`if [ "$1" = agents ]; then` + "\n" + `case "$CLAUDE_CONFIG_DIR" in` + "\n")
	for dir, out := range listings {
		if out == "" {
			fmt.Fprintf(&b, "'%s') echo boom >&2; exit 1 ;;\n", dir)
			continue
		}
		fmt.Fprintf(&b, "'%s') printf '%%s' '%s' ;;\n", dir, out)
	}
	b.WriteString("*) exit 1 ;;\nesac\nfi\n")
	return b.String()
}

// poolFixture is a Pool over a base config dir and two overlays, "a" and
// "b", whose workspaces are under root/workspaces.
type poolFixture struct {
	pool      *Pool
	base      string
	overlayA  Overlay
	overlayB  Overlay
	logFile   string
	attached  []string
	attachEnv []string
}

// newPoolFixture makes the fixture. listings maps "base", "a" or "b" to the
// `agents --json` output for that config dir ("" fails).
func newPoolFixture(t *testing.T, listings map[string]string) *poolFixture {
	t.Helper()
	root := t.TempDir()
	f := &poolFixture{
		base:     filepath.Join(root, "base"),
		overlayA: Overlay{ConfigDir: filepath.Join(root, "state", "a", "claude"), WorkspaceDir: filepath.Join(root, "workspaces", "a")},
		overlayB: Overlay{ConfigDir: filepath.Join(root, "state", "b", "claude"), WorkspaceDir: filepath.Join(root, "workspaces", "b")},
		logFile:  filepath.Join(t.TempDir(), "calls"),
	}
	byDir := map[string]string{
		f.base:               listings["base"],
		f.overlayA.ConfigDir: listings["a"],
		f.overlayB.ConfigDir: listings["b"],
	}
	environ := []string{"LOG=" + f.logFile, "CLAUDE_CONFIG_DIR=" + f.base}
	base := New(discardLogger(), fakeClaude(t, poolBody(byDir)), environ)
	base.replace = func(_ string, argv, envv []string) error {
		f.attached, f.attachEnv = argv, envv
		return nil
	}
	f.pool = NewPool(base, func() ([]Overlay, error) {
		return []Overlay{f.overlayB, f.overlayA}, nil
	})
	return f
}

// calls returns the logged calls, "<config dir> <args>", in order.
func (f *poolFixture) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.logFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func sessionIDs(sessions []Session) []string {
	ids := make([]string, 0, len(sessions))
	for i := range sessions {
		ids = append(ids, sessions[i].ID)
	}
	return ids
}

func TestPool_Agents(t *testing.T) {
	t.Parallel()
	t.Run("merges the base and overlays, marking overlay sessions", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, map[string]string{
			"base": `[{"id":"b1","kind":"background"}]`,
			"a":    `[{"id":"a1","kind":"background"}]`,
			"b":    `[{"id":"b2","kind":"background"}]`,
		})
		sessions, err := f.pool.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"b1", "a1", "b2"}, sessionIDs(sessions))
		configDirs := []string{sessions[0].ConfigDir, sessions[1].ConfigDir, sessions[2].ConfigDir}
		assert.Equal(t, []string{"", f.overlayA.ConfigDir, f.overlayB.ConfigDir}, configDirs)
	})
	t.Run("skips an overlay that fails", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, map[string]string{
			"base": `[{"id":"b1","kind":"background"}]`,
			"a":    "",
			"b":    `[{"id":"b2","kind":"background"}]`,
		})
		sessions, err := f.pool.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"b1", "b2"}, sessionIDs(sessions))
	})
	t.Run("returns the base's error", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, map[string]string{"base": "", "a": `[]`, "b": `[]`})
		_, err := f.pool.Agents(t.Context(), ListOptions{})
		require.Error(t, err)
	})
	t.Run("returns an error when the overlays can't be listed", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, map[string]string{"base": `[]`})
		broken := NewPool(f.pool.base, func() ([]Overlay, error) { return nil, errors.New("no state") })
		_, err := broken.Agents(t.Context(), ListOptions{})
		require.ErrorContains(t, err, "no state")
	})
}

func TestRunLimited(t *testing.T) {
	t.Run("runs every index, at most limit at a time", func(t *testing.T) {
		var running, peak, done atomic.Int32
		runLimited(20, 3, func(int) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			done.Add(1)
		})
		assert.Equal(t, int32(20), done.Load())
		assert.LessOrEqual(t, peak.Load(), int32(3))
	})
}

func TestPool_Routing(t *testing.T) {
	t.Parallel()
	listings := map[string]string{
		"base": `[{"id":"b1","kind":"background"}]`,
		"a":    `[{"id":"a1","kind":"background"}]`,
		"b":    `[{"id":"b2","kind":"background"}]`,
	}
	t.Run("stop goes to the overlay that listed the id", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, listings)
		_, err := f.pool.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		require.NoError(t, f.pool.Stop(t.Context(), "a1"))
		calls := f.calls(t)
		assert.Equal(t, f.overlayA.ConfigDir+" stop a1", calls[len(calls)-1])
	})
	t.Run("remove, respawn, and logs go to the base for a base id", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, listings)
		_, err := f.pool.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		require.NoError(t, f.pool.Remove(t.Context(), "b1"))
		require.NoError(t, f.pool.Respawn(t.Context(), "b1"))
		_, err = f.pool.Logs(t.Context(), "b1")
		require.NoError(t, err)
		calls := f.calls(t)
		assert.Equal(t, []string{f.base + " rm b1", f.base + " respawn b1", f.base + " logs b1"}, calls[len(calls)-3:])
	})
	t.Run("an unknown id lists once, then goes to the base", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, listings)
		require.NoError(t, f.pool.Stop(t.Context(), "zz99"))
		calls := f.calls(t)
		assert.Equal(t, f.base+" stop zz99", calls[len(calls)-1])
		agents := 0
		for _, c := range calls {
			if strings.HasSuffix(c, " agents --json --all") {
				agents++
			}
		}
		assert.Equal(t, 3, agents)
	})
	t.Run("a route to an overlay that is gone is dropped and the call goes to the base", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, listings)
		_, err := f.pool.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		f.pool.overlays = func() ([]Overlay, error) { return []Overlay{f.overlayB}, nil }
		require.NoError(t, f.pool.Stop(t.Context(), "a1"))
		calls := f.calls(t)
		assert.Equal(t, f.base+" stop a1", calls[len(calls)-1])
		_, routed := f.pool.route("a1")
		assert.False(t, routed, "the stale route to the removed overlay is evicted")
	})
	t.Run("listing drops the routes of overlays that are no longer listed", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, listings)
		_, err := f.pool.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		f.pool.overlays = func() ([]Overlay, error) { return []Overlay{f.overlayB}, nil }
		_, err = f.pool.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		_, routed := f.pool.route("a1")
		assert.False(t, routed)
		dir, routed := f.pool.route("b2")
		assert.True(t, routed)
		assert.Equal(t, f.overlayB.ConfigDir, dir)
	})
	t.Run("send goes to the config dir the session carries", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, listings)
		f.pool.base.sendTimings = sendTimings{prompt: 10 * time.Millisecond, settle: time.Millisecond, record: 50 * time.Millisecond, poll: time.Millisecond, hangUp: 50 * time.Millisecond}
		// The fake never records the message, so Send fails; only the
		// attach it ran matters here.
		_ = f.pool.Send(t.Context(), &Session{ID: "x1", ConfigDir: f.overlayB.ConfigDir}, "hi")
		calls := f.calls(t)
		assert.Equal(t, f.overlayB.ConfigDir+" attach x1", calls[len(calls)-1])
	})
	t.Run("attach runs under the overlay that listed the id", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, listings)
		_, err := f.pool.Agents(t.Context(), ListOptions{})
		require.NoError(t, err)
		require.NoError(t, f.pool.Attach(t.Context(), "a1"))
		assert.Equal(t, []string{"attach", "a1"}, f.attached[len(f.attached)-2:])
		assert.Contains(t, f.attachEnv, "CLAUDE_CONFIG_DIR="+f.overlayA.ConfigDir)
	})
}

func TestPool_Start(t *testing.T) {
	t.Parallel()
	t.Run("runs under the overlay and routes the new id there without listing", func(t *testing.T) {
		t.Parallel()
		f := newPoolFixture(t, map[string]string{"base": `[]`, "a": `[]`, "b": `[]`})
		started, err := f.pool.Start(t.Context(), StartOptions{Dir: t.TempDir(), ConfigDir: f.overlayA.ConfigDir})
		require.NoError(t, err)
		assert.Equal(t, "ab12cd34", started.ID)
		require.NoError(t, f.pool.Stop(t.Context(), started.ID))
		calls := f.calls(t)
		assert.Equal(t, f.overlayA.ConfigDir+" stop ab12cd34", calls[len(calls)-1])
		for _, c := range calls {
			assert.NotContains(t, c, "agents")
		}
	})
}

func TestPool_Trust(t *testing.T) {
	t.Parallel()
	f := newPoolFixture(t, map[string]string{"base": `[]`, "a": `[]`, "b": `[]`})
	record := func(string, bool) error { return nil }
	seed := func(dir, parent string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(dir, 0o700))
		body := fmt.Sprintf(`{"projects":{%q:{"hasTrustDialogAccepted":true}}}`, resolved(parent))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600))
	}
	workspace := filepath.Join(f.overlayA.WorkspaceDir, "repo")
	parentA := f.overlayA.WorkspaceDir
	seed(f.overlayA.ConfigDir, parentA)
	require.NoError(t, os.MkdirAll(workspace, 0o700))
	t.Run("a directory under an overlay's workspace writes that overlay's config", func(t *testing.T) {
		require.NoError(t, f.pool.Trust(t.Context(), workspace, parentA, record))
		data, err := os.ReadFile(filepath.Join(f.overlayA.ConfigDir, ".claude.json"))
		require.NoError(t, err)
		assert.Contains(t, string(data), resolved(workspace))
		_, err = os.Stat(filepath.Join(f.base, ".claude.json"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("other directories write the base's config", func(t *testing.T) {
		elsewhere := filepath.Join(t.TempDir(), "elsewhere")
		require.NoError(t, os.MkdirAll(elsewhere, 0o700))
		seed(f.base, filepath.Dir(elsewhere))
		require.NoError(t, f.pool.Trust(t.Context(), elsewhere, filepath.Dir(elsewhere), record))
		data, err := os.ReadFile(filepath.Join(f.base, ".claude.json"))
		require.NoError(t, err)
		assert.Contains(t, string(data), resolved(elsewhere))
	})
}

func TestPool_StopDaemon(t *testing.T) {
	t.Parallel()
	t.Run("stops the base and every overlay, reporting the first error", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		logFile := filepath.Join(root, "calls")
		base := filepath.Join(root, "base")
		a := filepath.Join(root, "a")
		b := filepath.Join(root, "b")
		body := `echo "$CLAUDE_CONFIG_DIR $*" >> "$LOG"
case "$CLAUDE_CONFIG_DIR" in
` + "'" + base + "'" + `|` + "'" + a + "'" + `) echo "stop-failed-$CLAUDE_CONFIG_DIR" >&2; exit 1 ;;
esac`
		c := New(discardLogger(), fakeClaude(t, body), []string{"LOG=" + logFile, "CLAUDE_CONFIG_DIR=" + base})
		pool := NewPool(c, func() ([]Overlay, error) {
			return []Overlay{{ConfigDir: b}, {ConfigDir: a}}, nil
		})
		err := pool.StopDaemon(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stop-failed-"+base)
		data, readErr := os.ReadFile(logFile)
		require.NoError(t, readErr)
		assert.Equal(t, 3, strings.Count(string(data), " daemon stop --any --keep-workers\n"))
	})
}
