package envproto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupOp(t *testing.T) {
	t.Run("returns the spec of each operation", func(t *testing.T) {
		specs := map[string]OpSpec{
			OpCertRenew:       {Name: OpCertRenew, Caller: CallerAgent, Timeout: 30 * time.Second},
			OpCAUpdate:        {Name: OpCAUpdate, Caller: CallerInternal, Timeout: 30 * time.Second},
			OpAPIUpdate:       {Name: OpAPIUpdate, Caller: CallerInternal, Since: ProtocolArtifacts, Timeout: 30 * time.Second},
			OpWorkspaceCreate: {Name: OpWorkspaceCreate, Caller: CallerController, Since: 1, Timeout: 10 * time.Minute, Changes: true},
			OpWorkspaceList:   {Name: OpWorkspaceList, Caller: CallerController, Since: 1, Timeout: 30 * time.Second},
			OpWorkspaceRm:     {Name: OpWorkspaceRm, Caller: CallerController, Since: 1, Timeout: 2 * time.Minute, Changes: true},
			OpWorkspaceRename: {Name: OpWorkspaceRename, Caller: CallerController, Since: 1, Timeout: 30 * time.Second, Changes: true},
			OpWorkspaceHide:   {Name: OpWorkspaceHide, Caller: CallerController, Since: 1, Timeout: 30 * time.Second, Changes: true},
			OpAgentStatus:     {Name: OpAgentStatus, Caller: CallerController, Since: 1, Timeout: 30 * time.Second},
			OpAgentLogs:       {Name: OpAgentLogs, Caller: CallerController, Since: 1, Timeout: 30 * time.Second},
			OpAgentSend:       {Name: OpAgentSend, Caller: CallerController, Since: 1, Timeout: 90 * time.Second},
			OpAgentStop:       {Name: OpAgentStop, Caller: CallerController, Since: 1, Timeout: 30 * time.Second, Changes: true},
		}
		for name, want := range specs {
			got, ok := LookupOp(name)
			require.True(t, ok, name)
			assert.Equal(t, want, got, name)
		}
	})
	t.Run("terminal.open is CallerTerminal since 2", func(t *testing.T) {
		got, ok := LookupOp(OpTerminalOpen)
		require.True(t, ok)
		assert.Equal(t, OpSpec{Name: OpTerminalOpen, Caller: CallerTerminal, Since: 2, Timeout: 30 * time.Second, Changes: true}, got)
	})
	t.Run("terminal.close is CallerTerminal since 2", func(t *testing.T) {
		got, ok := LookupOp(OpTerminalClose)
		require.True(t, ok)
		assert.Equal(t, OpSpec{Name: OpTerminalClose, Caller: CallerTerminal, Since: 2, Timeout: 30 * time.Second}, got)
	})
	t.Run("review ops have their full spec", func(t *testing.T) {
		specs := []OpSpec{
			{Name: OpReviewStart, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 10 * time.Minute, Changes: true},
			{Name: OpReviewStatus, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			{Name: OpReviewList, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			{Name: OpReviewReport, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			{Name: OpReviewDecide, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			{Name: OpReviewActed, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			{Name: OpReviewProfiles, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
		}
		for _, want := range specs {
			got, ok := LookupOp(want.Name)
			require.True(t, ok, want.Name)
			assert.Equal(t, want, got, want.Name)
		}
	})
	t.Run("refuses names outside the table", func(t *testing.T) {
		for _, name := range []string{"files.read", "", "workspace.create "} {
			_, ok := LookupOp(name)
			assert.False(t, ok, name)
		}
	})
}

func TestDecodeArgs(t *testing.T) {
	t.Run("decodes a claude create into CreateArgs", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"claude","specs":["buff-bear@main"],"name":null,"parent":null,"hidden":false,"offline":false,"prompt":"hi","model":null,"endpoint":null}`)
		got, err := DecodeArgs(OpWorkspaceCreate, raw)
		require.NoError(t, err)
		args, ok := got.(*CreateArgs)
		require.True(t, ok)
		assert.Equal(t, KindClaude, args.Kind)
		assert.Equal(t, []string{"buff-bear@main"}, args.Specs)
		assert.Equal(t, "hi", *args.Prompt)
	})
	t.Run("treats a missing or null argument as an empty object", func(t *testing.T) {
		got, err := DecodeArgs(OpWorkspaceList, nil)
		require.NoError(t, err)
		assert.Equal(t, &ListArgs{}, got)
		got, err = DecodeArgs(OpWorkspaceList, json.RawMessage(`null`))
		require.NoError(t, err)
		assert.Equal(t, &ListArgs{}, got)
	})
	t.Run("refuses an unknown key with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"claude","specs":[],"x":1}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses trailing data after the object", func(t *testing.T) {
		_, err := DecodeArgs(OpWorkspaceList, json.RawMessage(`{} {}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses an unknown operation with usage", func(t *testing.T) {
		_, err := DecodeArgs("files.read", json.RawMessage(`{}`))
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a shell kind with the terminal streams message", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"shell","specs":[]}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
		assert.EqualError(t, err, "a shell on an environment needs terminal streams")
	})
	t.Run("refuses a spec that starts with a dash with spec_invalid", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"claude","specs":["-rf"]}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "spec_invalid")
	})
	t.Run("refuses a runner without a prompt", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"runner","specs":[]}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a model that starts with a dash", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"claude","specs":[],"model":"--dangerously-skip-permissions"}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses a claude prompt that starts with a dash with usage", func(t *testing.T) {
		for _, prompt := range []string{"--dangerously-skip-permissions", "  -p x", "\n-x"} {
			assertArgsError(t, Validate(&CreateArgs{Kind: KindClaude, Specs: []string{}, Prompt: &prompt}), "usage")
		}
		prompt := "- a list"
		assert.NoError(t, Validate(&CreateArgs{Kind: KindRunner, Specs: []string{}, Prompt: &prompt}))
	})
	t.Run("refuses an endpoint that starts with a dash with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"runner","specs":[],"prompt":"x","endpoint":"--foo"}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("refuses an empty endpoint with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"runner","specs":[],"prompt":"x","endpoint":""}`)
		_, err := DecodeArgs(OpWorkspaceCreate, raw)
		assertArgsError(t, err, "usage")
	})
	t.Run("decodes a runner with a valid endpoint", func(t *testing.T) {
		raw := json.RawMessage(`{"kind":"runner","specs":[],"prompt":"x","endpoint":"lab-gpu"}`)
		got, err := DecodeArgs(OpWorkspaceCreate, raw)
		require.NoError(t, err)
		args, ok := got.(*CreateArgs)
		require.True(t, ok)
		assert.Equal(t, "lab-gpu", *args.Endpoint)
	})
	t.Run("refuses an unknown key in review.report with usage", func(t *testing.T) {
		raw := json.RawMessage(`{"review":"r1","section":"comments","offset":0,"extra":1}`)
		_, err := DecodeArgs(OpReviewReport, raw)
		assertArgsError(t, err, "usage")
	})
}

func TestValidate(t *testing.T) {
	t.Run("accepts a rename to a valid name", func(t *testing.T) {
		assert.NoError(t, Validate(&RenameArgs{Workspace: "a", Name: "new-name"}))
	})
	t.Run("refuses a name longer than 64 bytes with name_invalid", func(t *testing.T) {
		err := Validate(&RenameArgs{Workspace: "a", Name: strings.Repeat("n", 65)})
		assertArgsError(t, err, "name_invalid")
	})
	t.Run("refuses a name with a control character with name_invalid", func(t *testing.T) {
		err := Validate(&RenameArgs{Workspace: "a", Name: "bad\nname"})
		assertArgsError(t, err, "name_invalid")
	})
	t.Run("refuses an empty workspace reference with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&WorkspaceArgs{}), "usage")
	})
	t.Run("refuses a workspace reference with a NUL byte with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&RmArgs{Workspace: "a\x00b"}), "usage")
	})
	t.Run("refuses a tail above MaxTail with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LogsArgs{Workspace: "a", Tail: MaxTail + 1}), "usage")
	})
	t.Run("refuses a negative tail with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&LogsArgs{Workspace: "a", Tail: -1}), "usage")
	})
	t.Run("refuses an empty message with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&SendArgs{Workspace: "a"}), "usage")
	})
	t.Run("refuses a message above MaxMessageBytes with usage", func(t *testing.T) {
		msg := strings.Repeat("m", MaxMessageBytes+1)
		assertArgsError(t, Validate(&SendArgs{Workspace: "a", Message: msg}), "usage")
	})
	t.Run("refuses an endpoint on a claude create with usage", func(t *testing.T) {
		endpoint := "http://localhost:1"
		assertArgsError(t, Validate(&CreateArgs{Kind: KindClaude, Endpoint: &endpoint}), "usage")
	})
	t.Run("refuses a prompt above MaxPromptBytes with usage", func(t *testing.T) {
		prompt := strings.Repeat("p", MaxPromptBytes+1)
		assertArgsError(t, Validate(&CreateArgs{Kind: KindRunner, Prompt: &prompt}), "usage")
	})
	t.Run("refuses an unknown kind with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&CreateArgs{Kind: "files"}), "usage")
	})
	t.Run("refuses an endpoint that starts with a dash with usage", func(t *testing.T) {
		prompt, endpoint := "x", "--foo"
		assertArgsError(t, Validate(&CreateArgs{Kind: KindRunner, Prompt: &prompt, Endpoint: &endpoint}), "usage")
	})
	t.Run("refuses an empty endpoint with usage", func(t *testing.T) {
		prompt, endpoint := "x", ""
		assertArgsError(t, Validate(&CreateArgs{Kind: KindRunner, Prompt: &prompt, Endpoint: &endpoint}), "usage")
	})
	t.Run("refuses an endpoint longer than 128 bytes with usage", func(t *testing.T) {
		prompt, endpoint := "x", strings.Repeat("e", 129)
		assertArgsError(t, Validate(&CreateArgs{Kind: KindRunner, Prompt: &prompt, Endpoint: &endpoint}), "usage")
	})
	t.Run("refuses an endpoint with a control character with usage", func(t *testing.T) {
		prompt, endpoint := "x", "lab\x00gpu"
		assertArgsError(t, Validate(&CreateArgs{Kind: KindRunner, Prompt: &prompt, Endpoint: &endpoint}), "usage")
	})
	t.Run("refuses a spec with leading whitespace with spec_invalid", func(t *testing.T) {
		assertArgsError(t, Validate(&CreateArgs{Kind: KindClaude, Specs: []string{" -x"}}), "spec_invalid")
	})
	t.Run("refuses an empty spec with spec_invalid", func(t *testing.T) {
		assertArgsError(t, Validate(&CreateArgs{Kind: KindClaude, Specs: []string{""}}), "spec_invalid")
	})
	t.Run("refuses a spec with a control character with spec_invalid", func(t *testing.T) {
		assertArgsError(t, Validate(&CreateArgs{Kind: KindClaude, Specs: []string{"buff\x00bear"}}), "spec_invalid")
	})
	t.Run("refuses a nil argument pointer with usage", func(t *testing.T) {
		assertArgsError(t, Validate((*CreateArgs)(nil)), "usage")
	})
	t.Run("accepts a create with a valid name and parent", func(t *testing.T) {
		assert.NoError(t, Validate(&CreateArgs{Kind: KindClaude, Name: new("new-name"), Parent: new("parent")}))
	})
	t.Run("refuses a create name with a control character with name_invalid", func(t *testing.T) {
		assertArgsError(t, Validate(&CreateArgs{Kind: KindClaude, Name: new("bad\nname")}), "name_invalid")
	})
	t.Run("refuses an empty parent with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&CreateArgs{Kind: KindClaude, Parent: new("")}), "usage")
	})
	t.Run("refuses a parent with a NUL byte with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&CreateArgs{Kind: KindClaude, Parent: new("a\x00b")}), "usage")
	})
	t.Run("refuses an empty prompt on a runner with usage", func(t *testing.T) {
		assertArgsError(t, Validate(&CreateArgs{Kind: KindRunner, Prompt: new("")}), "usage")
	})
	t.Run("accepts a model that matches the model pattern", func(t *testing.T) {
		assert.NoError(t, Validate(&CreateArgs{Kind: KindClaude, Model: new("claude-opus-5-5")}))
	})
}

func TestValidRequestID(t *testing.T) {
	t.Run("accepts 16 lowercase hex characters", func(t *testing.T) {
		assert.True(t, ValidRequestID("0123456789abcdef"))
	})
	t.Run("refuses other lengths", func(t *testing.T) {
		assert.False(t, ValidRequestID("0123456789abcde"))
		assert.False(t, ValidRequestID("0123456789abcdef0"))
	})
	t.Run("refuses upper case and non-hex characters", func(t *testing.T) {
		assert.False(t, ValidRequestID("0123456789ABCDEF"))
		assert.False(t, ValidRequestID("0123456789abcdeg"))
	})
}

func assertArgsError(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var argsErr *ArgsError
	require.ErrorAs(t, err, &argsErr)
	assert.Equal(t, code, argsErr.Code)
	assert.ErrorIs(t, err, ErrArgs)
}

func TestLookupOp_scopes(t *testing.T) {
	t.Run("scope and machine.record ops have their spec", func(t *testing.T) {
		specs := []OpSpec{
			{Name: OpMachineRecord, Caller: CallerController, Since: ProtocolScopes, Timeout: 30 * time.Second},
			{Name: OpScopeEnvs, Caller: CallerAgent, Timeout: 30 * time.Second},
			{Name: OpScopeSpawn, Caller: CallerAgent, Timeout: 10 * time.Minute},
			{Name: OpScopeChildren, Caller: CallerAgent, Timeout: 30 * time.Second},
			{Name: OpScopeChild, Caller: CallerAgent, Timeout: 2 * time.Minute},
		}
		for _, want := range specs {
			got, ok := LookupOp(want.Name)
			require.True(t, ok, want.Name)
			assert.Equal(t, want, got, want.Name)
		}
	})
}

func TestLookupOp_loops(t *testing.T) {
	t.Run("loop ops are controller ops since 3", func(t *testing.T) {
		specs := map[string]OpSpec{
			OpLoopRun:          {Name: OpLoopRun, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 15 * time.Minute, Changes: true},
			OpLoopAdd:          {Name: OpLoopAdd, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second, Changes: true},
			OpLoopEdit:         {Name: OpLoopEdit, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second, Changes: true},
			OpLoopPause:        {Name: OpLoopPause, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second, Changes: true},
			OpLoopResume:       {Name: OpLoopResume, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second, Changes: true},
			OpLoopRm:           {Name: OpLoopRm, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second, Changes: true},
			OpLoopList:         {Name: OpLoopList, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopShow:         {Name: OpLoopShow, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopHistory:      {Name: OpLoopHistory, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopHistoryRun:   {Name: OpLoopHistoryRun, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopHistoryClear: {Name: OpLoopHistoryClear, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopMemoryList:   {Name: OpLoopMemoryList, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopMemoryGet:    {Name: OpLoopMemoryGet, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopMemorySet:    {Name: OpLoopMemorySet, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopMemoryRm:     {Name: OpLoopMemoryRm, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
			OpLoopMemoryClear:  {Name: OpLoopMemoryClear, Caller: CallerController, Since: ProtocolReviewsLoops, Timeout: 30 * time.Second},
		}
		require.Len(t, specs, 16)
		for name, want := range specs {
			got, ok := LookupOp(name)
			require.True(t, ok, name)
			assert.Equal(t, want, got, name)
		}
	})
}
