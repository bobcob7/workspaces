package loop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAttentionOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		state   string
		verdict string
		want    Attention
	}{
		{name: "working with BLOCKED is none", state: "working", verdict: VerdictBlocked, want: AttentionNone},
		{name: "starting with no verdict is none", state: "starting", want: AttentionNone},
		{name: "idle with BLOCKED is blocked", state: "idle", verdict: VerdictBlocked, want: AttentionBlocked},
		{name: "stopped with BLOCKED is blocked", state: "stopped", verdict: VerdictBlocked, want: AttentionBlocked},
		{name: "unknown with BLOCKED is blocked", state: "unknown", verdict: VerdictBlocked, want: AttentionBlocked},
		{name: "idle with no verdict needs attention", state: "idle", want: AttentionNeedsAttention},
		{name: "stopped with no verdict is none", state: "stopped", want: AttentionNone},
		{name: "idle with PASS is none", state: "idle", verdict: "PASS", want: AttentionNone},
		{name: "idle with FAIL is none", state: "idle", verdict: "FAIL", want: AttentionNone},
		{name: "no session and no verdict is none", want: AttentionNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, AttentionOf(tt.state, tt.verdict))
		})
	}
}
