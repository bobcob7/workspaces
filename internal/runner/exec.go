package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/llm"
	"github.com/bobcob7/workspaces/internal/proc"
	"github.com/bobcob7/workspaces/internal/telemetry"
)

const (
	// shutdownLimit bounds the telemetry flush before the process exits.
	shutdownLimit = 5 * time.Second
	// maxUpfront is the most MCP tools a run loads before tool_search.
	maxUpfront = 24
	// secretNamesEnv is the comma list of secrets.env's names the daemon sets.
	secretNamesEnv = "WS_RUNNER_SECRET_NAMES"
)

// Input is what Execute needs beyond the run's own files.
type Input struct {
	Files     Files
	Resume    bool
	Workspace string // the workspace directory: the agent's work and its project instructions
	ConfigDir string // CONFIG_DIR: config.toml, read for keep_git_tokens
	Environ   []string
	Store     resultStore
	// Sleep waits between retries; nil waits on the wall clock.
	Sleep func(ctx context.Context, d time.Duration) error
}

// runClock is the loop's clock: the wall clock, with Sleep as in Input.
type runClock struct {
	sleep func(context.Context, time.Duration) error
}

// Now is the wall clock.
func (runClock) Now() time.Time {
	return time.Now()
}

// Sleep waits d, through Input.Sleep when it is set.
func (c runClock) Sleep(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
	}
	return agent.SystemClock{}.Sleep(ctx, d)
}

// session is one process run's state.
type session struct {
	in        Input
	run       Run
	logOut    io.Writer
	logFile   *os.File
	log       *slog.Logger
	redactor  *tools.Redactor
	keepGit   bool
	restarts  int
	prior     []agent.EndpointUsage
	status    Status
	statusErr error
	step      int
	mcp       *mcpSet
	endRun    func(exit string)
	tel       *telemetry.Telemetry
	tr        *agent.Transcript
	started   time.Time
}

// Execute runs the agent loop for in.Files' run until it exits, then
// finalizes it. It returns an error only when run.toml can't be read, or a
// file the runner must write can't be (status.json, usage.json, the log).
// Every other failure is the run's exit.
func Execute(ctx context.Context, in *Input) error {
	run, err := in.Files.ReadRun()
	if err != nil {
		return err
	}
	s, err := openSession(in, &run)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.saveStatus("starting"); err != nil {
		return err
	}
	outcome := s.loop(ctx)
	return s.finish(ctx, &outcome)
}

func openSession(in *Input, run *Run) (*session, error) {
	keepGit, err := keepsGitTokens(in.ConfigDir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(in.Files.Dir, dirMode); err != nil {
		return nil, fmt.Errorf("creating runner directory: %w", err)
	}
	logFile, err := os.OpenFile(in.Files.Log(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return nil, fmt.Errorf("opening runner.log: %w", err)
	}
	s := &session{in: *in, run: *run, keepGit: keepGit, logFile: logFile, started: time.Now().UTC()}
	s.redactor = tools.NewRedactor(s.secretValues()...)
	s.logOut = redactWriter{w: logFile, r: s.redactor}
	s.log = slog.New(slog.NewJSONHandler(s.logOut, nil))
	s.restarts, s.prior = priorStatus(in.Files, in.Resume)
	s.status = s.initialStatus()
	return s, nil
}

// secretValues are the values the runner's tools never see: the scrubbed
// environment's secrets, and GH_TOKEN and GITHUB_TOKEN when keep_git_tokens
// leaves them in the tools' environment.
func (s *session) secretValues() []string {
	values := tools.Secrets(s.in.Environ, s.keepGit, secretNames(s.in.Environ))
	if s.keepGit {
		values = append(values, envValue(s.in.Environ, "GH_TOKEN"), envValue(s.in.Environ, "GITHUB_TOKEN"))
	}
	return values
}

func (s *session) close() {
	_ = s.logFile.Close() // the log is only written, never read back; a close error can't change the run
}

// priorStatus is the restart count of the previous process run, and on a
// resume its endpoint usage too; zero values when there is no status.
func priorStatus(f Files, resume bool) (int, []agent.EndpointUsage) {
	st, err := f.ReadStatus()
	if err != nil {
		return 0, nil
	}
	if !resume {
		return st.Restarts, nil
	}
	return st.Restarts, st.Endpoints
}

func (s *session) initialStatus() Status {
	current := s.run.Current()
	start, err := (proc.Reader{}).StartTime(os.Getpid())
	if err != nil {
		start = s.started
	}
	return Status{
		Session:          s.run.Session,
		PID:              os.Getpid(),
		ProcessStartedAt: start.Truncate(time.Second),
		MaxSteps:         s.run.MaxSteps,
		Restarts:         s.restarts,
		Endpoint:         current.Endpoint,
		Model:            current.Model,
		UpdatedAt:        time.Now().UTC(),
	}
}

// loop sets the run up and runs it. A setup failure is the run's exit.
func (s *session) loop(ctx context.Context) agent.Outcome {
	ctx, p, err := s.setup(ctx)
	if err != nil {
		return agent.Outcome{Exit: agent.ExitInternal, Message: err.Error()}
	}
	defer s.teardown()
	return p.agent.Run(ctx, p.history, p.step, s.prior)
}

// prepared is a run ready to go: the loop and the conversation it continues.
type prepared struct {
	agent   *agent.Agent
	history []llm.Message
	step    int
}

// finish ends the run's telemetry, finalizes its files and returns the
// first error writing them.
func (s *session) finish(ctx context.Context, outcome *agent.Outcome) error {
	verdict := s.recordedVerdict(outcome)
	exit, message := exitOf(ctx, outcome, verdict)
	if s.endRun != nil {
		s.endRun(exit)
	}
	if s.tel != nil {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownLimit)
		defer cancel()
		if err := s.tel.Shutdown(shutdownCtx); err != nil {
			s.log.WarnContext(ctx, "telemetry shutdown", "error", err)
		}
	}
	err := Finalize(s.in.Files, &s.run, exit, message, s.finalizeInput(verdict, outcome), s.in.Store)
	if err != nil {
		return err
	}
	return s.statusErr
}

// recordedVerdict is the outcome's verdict, else the one the run recorded
// through ws machine result; a read failure is logged and reads as none.
func (s *session) recordedVerdict(outcome *agent.Outcome) string {
	if outcome.Verdict != "" {
		return outcome.Verdict
	}
	verdict, err := s.in.Files.recordedVerdict(&s.run, s.in.Store)
	if err != nil {
		s.log.Warn("reading recorded verdict", "error", err)
	}
	return verdict
}

// exitOf is the exit reason the run ends with. A run that was asked to stop
// (SIGTERM from the daemon) without a verdict is stopped, so it resumes.
func exitOf(ctx context.Context, outcome *agent.Outcome, verdict string) (exit, message string) {
	if ctx.Err() != nil && verdict == "" && outcome.Exit != agent.ExitDone {
		return agent.ExitStopped, ""
	}
	return outcome.Exit, outcome.Message
}

func (s *session) finalizeInput(verdict string, outcome *agent.Outcome) *FinalizeInput {
	return &FinalizeInput{
		Verdict:   verdict,
		Steps:     outcome.Steps,
		Restarts:  s.restarts,
		Usage:     outcome.Usage,
		Endpoints: outcome.Endpoints,
		MCPCalls:  s.mcpCalls(),
		StartedAt: s.run.CreatedAt,
		EndedAt:   time.Now().UTC(),
	}
}

func (s *session) mcpCalls() map[string]int {
	if s.mcp == nil {
		return nil
	}
	return s.mcp.calls
}

// saveStatus writes status.json in state, with the run's current totals.
func (s *session) saveStatus(state string) error {
	s.status.State = state
	s.status.UpdatedAt = time.Now().UTC()
	s.syncMCP()
	return s.in.Files.WriteStatus(&s.status)
}

// onStatus takes the loop's status and writes it. A failed write is kept
// for the exit code.
//
//nolint:gocritic // hugeParam: the loop passes its status by value (agent.Config.OnStatus).
func (s *session) onStatus(st agent.Status) {
	s.step = st.Step
	s.status.Step = st.Step
	s.status.Usage = st.Usage
	s.status.Endpoint = st.Endpoint
	s.status.Model = st.Model
	s.status.EndpointChanges = st.EndpointChanges
	s.status.Endpoints = st.Endpoints
	if err := s.saveStatus(st.State); err != nil {
		s.statusErr = err
	}
}

func (s *session) syncMCP() {
	if s.mcp == nil {
		return
	}
	s.status.MCP = s.mcp.statuses()
}

// teardown closes what setup opened: the MCP servers and the transcript.
func (s *session) teardown() {
	if s.mcp != nil {
		if err := s.mcp.manager.Close(); err != nil {
			s.log.Warn("closing MCP servers", "error", err)
		}
	}
	if s.tr != nil {
		if err := s.tr.Close(); err != nil {
			s.log.Warn("closing transcript", "error", err)
		}
	}
}

// setup builds everything the loop needs: telemetry, the provider, the
// tools, the MCP servers, the transcript and the prompts. On resume it also
// rebuilds the history from the transcript.
func (s *session) setup(ctx context.Context) (context.Context, prepared, error) {
	tel, err := telemetry.New(ctx, s.logOut, func(k string) string { return envValue(s.in.Environ, k) })
	if err != nil {
		return ctx, prepared{}, fmt.Errorf("setting up telemetry: %w", err)
	}
	s.tel = tel
	s.log = tel.Logger
	ctx, s.endRun = tel.StartRun(ctx, s.run.TraceID, telemetry.RunInfo{Role: s.run.Role, ModelID: s.run.Model, WorkspaceID: s.in.Files.ID, Session: s.run.Session})
	tr, err := agent.OpenTranscript(s.in.Files.Transcript(), s.redactor)
	if err != nil {
		return ctx, prepared{}, fmt.Errorf("opening transcript: %w", err)
	}
	s.tr = tr
	history, step, err := s.resumeHistory()
	if err != nil {
		return ctx, prepared{}, err
	}
	provider, err := s.provider()
	if err != nil {
		return ctx, prepared{}, err
	}
	toolset, err := s.toolset(ctx)
	if err != nil {
		return ctx, prepared{}, err
	}
	cfg, err := s.agentConfig(provider, toolset)
	if err != nil {
		return ctx, prepared{}, err
	}
	return ctx, prepared{agent: agent.New(runClock{sleep: s.in.Sleep}, tr, cfg, s.log), history: history, step: step}, nil
}

// resumeHistory is the conversation to continue, with an unanswered tail
// dropped, and the step to continue from. A fresh run has none.
func (s *session) resumeHistory() ([]llm.Message, int, error) {
	if !s.in.Resume {
		return nil, 0, nil
	}
	history, step, err := agent.ReadHistory(s.in.Files.Transcript())
	if err != nil {
		return nil, 0, fmt.Errorf("reading transcript: %w", err)
	}
	history = dropUnanswered(history)
	s.prior = resumePrior(s.prior, &s.run)
	s.writeEvent(step, "restart", restartEvent{Restarts: s.restarts, Reason: "resume", TraceID: s.run.TraceID})
	return history, step, nil
}

// agentConfig is the loop's settings: the target, budgets, tools and prompts.
func (s *session) agentConfig(provider llm.Provider, toolset agent.Toolset) (agent.Config, error) {
	current := s.run.Current()
	system, err := s.systemPrompt(toolset)
	if err != nil {
		return agent.Config{}, err
	}
	return agent.Config{
		Target: agent.Target{
			Chat:          provider,
			API:           current.API,
			Endpoint:      current.Endpoint,
			Model:         current.Model,
			ContextTokens: s.run.ContextTokens,
			MaxOutput:     s.run.MaxOutput,
			Temperature:   s.run.Temperature,
			Reasoning:     s.run.Reasoning,
		},
		ModelAlias:    s.run.ModelAlias,
		Budget:        agent.Budget{MaxSteps: s.run.MaxSteps, Timeout: s.run.Timeout, MaxTokens: s.run.MaxTokens},
		Tools:         toolset,
		System:        system,
		Task:          s.run.Prompt,
		TraceID:       s.run.TraceID,
		Stop:          s.in.Files.StopRequested,
		ResultWritten: s.resultWritten,
		OnStatus:      s.onStatus,
	}, nil
}

func (s *session) resultWritten() bool {
	verdict, err := s.in.Files.recordedVerdict(&s.run, s.in.Store)
	return err == nil && verdict != ""
}

// systemPrompt is the preamble with the workspace's project instructions and
// the user's, when the run takes them.
func (s *session) systemPrompt(toolset agent.Toolset) (string, error) {
	userFile := ""
	if s.run.UserInstructions {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding home directory: %w", err)
		}
		userFile = filepath.Join(home, ".claude", "CLAUDE.md")
	}
	instructions, err := agent.LoadInstructions(s.in.Workspace, s.in.Workspace, userFile)
	if err != nil {
		return "", fmt.Errorf("loading project instructions: %w", err)
	}
	deferred := ""
	if ts, ok := toolset.(interface{ Deferred() string }); ok {
		deferred = ts.Deferred()
	}
	return agent.Preamble(agent.PreambleData{
		Dir:          s.in.Workspace,
		StepsLeft:    s.run.MaxSteps,
		MinutesLeft:  int(s.run.Timeout.Minutes()),
		Verdicts:     verdictList(&s.run),
		MCP:          s.mcp != nil && len(s.mcp.allowed) > 0,
		Deferred:     deferred,
		Unavailable:  s.mcpFailed(),
		Instructions: instructions,
	})
}

func (s *session) mcpFailed() []string {
	if s.mcp == nil {
		return nil
	}
	return s.mcp.failed
}

func verdictList(run *Run) string {
	return strings.Join(append(append([]string{}, run.Pass...), run.Fail...), " or ")
}

// provider is the endpoint's provider, wrapped in telemetry. The token is
// read from the runner's environment, where the daemon put it.
func (s *session) provider() (llm.Provider, error) {
	current := s.run.Current()
	ep := llm.Endpoint{
		Name:           current.Endpoint,
		URL:            current.URL,
		API:            current.API,
		Token:          envValue(s.in.Environ, config.TokenEnv(current.Endpoint)),
		RequestTimeout: s.run.RequestTimeout,
		IdleTimeout:    s.run.IdleTimeout,
	}
	p, err := newProvider(&ep, s.log)
	if err != nil {
		return nil, err
	}
	return s.tel.Provider(p, ep), nil
}

// writeEvent appends one event to the transcript. A failed write is logged:
// the transcript is the run's record, but the loop has nothing to do about it.
func (s *session) writeEvent(step int, kind string, payload any) {
	err := s.tr.Write(agent.Event{Time: time.Now().UTC(), Step: step, Kind: kind, Payload: payload})
	if err != nil {
		s.log.Warn("writing transcript event", "kind", kind, "error", err)
	}
}

type restartEvent struct {
	Restarts int    `json:"restarts"`
	Reason   string `json:"reason"`
	TraceID  string `json:"trace_id"`
}

// keepsGitTokens is runner.keep_git_tokens from config.toml, true by default.
func keepsGitTokens(configDir string) (bool, error) {
	cfg, err := config.ReadFile(config.NewPaths(configDir).ConfigFile)
	if errors.Is(err, fs.ErrNotExist) {
		cfg = config.Default()
	} else if err != nil {
		return false, err
	}
	if err := cfg.RunnerError(); err != nil {
		return false, err
	}
	return cfg.Runner.KeepsGitTokens(), nil
}

func secretNames(environ []string) []string {
	var names []string
	for name := range strings.SplitSeq(envValue(environ, secretNamesEnv), ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func envValue(environ []string, name string) string {
	for _, kv := range environ {
		if key, value, ok := strings.Cut(kv, "="); ok && key == name {
			return value
		}
	}
	return ""
}

// redactWriter writes to w with every registered value replaced.
type redactWriter struct {
	w io.Writer
	r *tools.Redactor
}

func (w redactWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.w, w.r.Redact(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// dropUnanswered cuts a history that ends with tool calls the transcript
// never answered: the step that made them is dropped, and the run repeats it.
func dropUnanswered(history []llm.Message) []llm.Message {
	i := lastToolCallTurn(history)
	if i < 0 || answered(history[i:]) {
		return history
	}
	return history[:i]
}

func lastToolCallTurn(history []llm.Message) int {
	for i := range slices.Backward(history) {
		if history[i].Role == llm.RoleAssistant && len(history[i].ToolCalls) > 0 {
			return i
		}
	}
	return -1
}

// answered reports whether every call of turn[0] has a tool result after it.
func answered(turn []llm.Message) bool {
	for _, call := range turn[0].ToolCalls {
		if !hasResult(turn[1:], call.ID) {
			return false
		}
	}
	return true
}

func hasResult(messages []llm.Message, callID string) bool {
	for _, m := range messages {
		if m.Role == llm.RoleTool && m.ToolCallID == callID {
			return true
		}
	}
	return false
}

// resumePrior pads the saved endpoint usage to the endpoint history and
// makes its last entry the run's current endpoint, so usage lands there.
func resumePrior(prior []agent.EndpointUsage, run *Run) []agent.EndpointUsage {
	out := append([]agent.EndpointUsage{}, prior...)
	for len(out) < len(run.EndpointHistory) {
		e := run.EndpointHistory[len(out)]
		out = append(out, agent.EndpointUsage{Endpoint: e.Endpoint, Model: e.Model})
	}
	current := run.Current()
	if len(out) == 0 || out[len(out)-1].Endpoint != current.Endpoint {
		out = append(out, agent.EndpointUsage{Endpoint: current.Endpoint, Model: current.Model})
	}
	return out
}
