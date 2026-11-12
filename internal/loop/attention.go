package loop

// Attention is what a loop run asks of a person (ADR 0057).
type Attention string

// The attentions a run can ask for. AttentionNone is no attention.
const (
	AttentionNone           Attention = ""
	AttentionBlocked        Attention = "blocked"
	AttentionNeedsAttention Attention = "needs_attention"
)

// VerdictBlocked is the verdict a run records to ask a person for help.
const VerdictBlocked = "BLOCKED"

// AttentionOf is what a run whose session is in state (a lifetime state:
// "working", "starting", "idle", "stopped", "unknown", or "" without a
// session) and whose recorded verdict is verdict ("" for none) asks of a
// person.
func AttentionOf(state, verdict string) Attention {
	switch {
	case state == "working" || state == "starting":
		return AttentionNone
	case verdict == VerdictBlocked:
		return AttentionBlocked
	case state == "idle" && verdict == "":
		return AttentionNeedsAttention
	default:
		return AttentionNone
	}
}
