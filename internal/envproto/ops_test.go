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
