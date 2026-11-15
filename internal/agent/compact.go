package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/llm"
)

const (
	compactAbovePercent   = 70
	compactToPercent      = 50
	keepRecent            = 6
	elideArgsBytes        = 80
	bytesPerToken         = 3
	elidedPrefix          = "[elided by ws:"
	progressNoteOpening   = "Progress so far (written by ws, not by you):"
	progressNoneListedMsg = "- none"
)

// ToolRecord is one finished tool call, for the progress note.
type ToolRecord struct {
	Name, Summary string
	ExitCode      *int
}

// CompactionEvent is what one compaction did; it is also the transcript's
// compaction line.
type CompactionEvent struct {
	BeforeTokens int `json:"before_tokens"`
	AfterTokens  int `json:"after_tokens"`
	Elided       int `json:"elided"`
	DroppedTurns int `json:"dropped_turns"`
}

// EstimateTokens estimates a prompt: the last measured prompt plus the bytes
// added since, at 3 bytes a token.
func EstimateTokens(lastPrompt, bytesSince int) int {
	return lastPrompt + (bytesSince+bytesPerToken-1)/bytesPerToken
}

// Compact fits messages under limit (contextTokens - maxOutput): above 70%,
// elide tool results outside the last 6 messages, oldest first, to under
// 50%; still above 70%, drop whole assistant+tool turns after the task and
// insert one user message "Progress so far (written by ws, not by you):"
// listing written/edited paths and run commands with exit codes. ok=false
// when the task and last turn alone don't fit.
func Compact(msgs []llm.Message, system string, estimate, limit int, done []ToolRecord) ([]llm.Message, CompactionEvent, bool) {
	return compact(msgs, system, estimate, limit, done, false)
}

// compact is Compact; force elides even under 70%, for a context_length
// error the estimate missed.
func compact(msgs []llm.Message, system string, estimate, limit int, done []ToolRecord, force bool) ([]llm.Message, CompactionEvent, bool) {
	ev := CompactionEvent{BeforeTokens: estimate, AfterTokens: estimate}
	if !force && !overPercent(estimate, limit, compactAbovePercent) {
		return msgs, ev, true
	}
	out, est, elided := elideOldest(slices.Clone(msgs), estimate, limit)
	ev.Elided = elided
	if overPercent(est, limit, compactAbovePercent) {
		out, est, ev.DroppedTurns = dropTurns(out, est, limit, done)
	}
	ev.AfterTokens = est
	return out, ev, fitsLastTurn(out, system, limit)
}

func overPercent(est, limit, percent int) bool {
	return est*100 > limit*percent
}

func elideOldest(msgs []llm.Message, est, limit int) ([]llm.Message, int, int) {
	elided := 0
	for i := range len(msgs) - keepRecent {
		if !overPercent(est, limit, compactToPercent) {
			break
		}
		if !elidable(&msgs[i]) {
			continue
		}
		before := messageBytes(&msgs[i])
		note := elision(msgs[:i+1])
		if len(note) >= len(msgs[i].Content) {
			continue
		}
		msgs[i].Content = note
		est -= (before - messageBytes(&msgs[i])) / bytesPerToken
		elided++
	}
	return msgs, est, elided
}

func elidable(m *llm.Message) bool {
	return m.Role == llm.RoleTool && !strings.HasPrefix(m.Content, elidedPrefix)
}

// elision is the note that replaces the last message's content; the call
// that produced it is found among the messages before it.
func elision(msgs []llm.Message) string {
	last := msgs[len(msgs)-1]
	args := argumentsFor(msgs[:len(msgs)-1], last.ToolCallID)
	return fmt.Sprintf("%s %s %s, %d lines; run it again if you need it]", elidedPrefix, last.ToolName, firstBytes(args, elideArgsBytes), strings.Count(last.Content, "\n")+1)
}

func argumentsFor(msgs []llm.Message, id string) string {
	for j := range slices.Backward(msgs) {
		for _, tc := range msgs[j].ToolCalls {
			if tc.ID == id {
				return tc.Arguments
			}
		}
	}
	return ""
}

// dropTurns removes whole turns after the task, oldest first, never the last
// one, and puts one progress note in their place.
func dropTurns(msgs []llm.Message, est, limit int, done []ToolRecord) ([]llm.Message, int, int) {
	hadNote := len(msgs) > 1 && isProgressNote(&msgs[1])
	if hadNote {
		est -= messageBytes(&msgs[1]) / bytesPerToken
		msgs = slices.Delete(msgs, 1, 2)
	}
	dropped := 0
	for overPercent(est, limit, compactAbovePercent) {
		end := nextAssistant(msgs, 2)
		if end < 0 || end >= lastAssistant(msgs) {
			break
		}
		est -= bytesOf(msgs[1:end]) / bytesPerToken
		msgs = slices.Delete(msgs, 1, end)
		dropped++
	}
	if dropped == 0 && !hadNote {
		return msgs, est, 0
	}
	note := progressNote(done)
	msgs = slices.Insert(msgs, 1, note)
	return msgs, est + messageBytes(&note)/bytesPerToken, dropped
}

func progressNote(done []ToolRecord) llm.Message {
	var b strings.Builder
	b.WriteString(progressNoteOpening)
	if len(done) == 0 {
		b.WriteString("\n" + progressNoneListedMsg)
	}
	for _, rec := range done {
		fmt.Fprintf(&b, "\n- %s %s", rec.Name, rec.Summary)
		if rec.ExitCode != nil {
			fmt.Fprintf(&b, " (exit %d)", *rec.ExitCode)
		}
	}
	return llm.Message{Role: llm.RoleUser, Content: b.String()}
}

func isProgressNote(m *llm.Message) bool {
	return m.Role == llm.RoleUser && strings.HasPrefix(m.Content, progressNoteOpening)
}

func nextAssistant(msgs []llm.Message, from int) int {
	for i := from; i < len(msgs); i++ {
		if msgs[i].Role == llm.RoleAssistant {
			return i
		}
	}
	return -1
}

func lastAssistant(msgs []llm.Message) int {
	for i := range slices.Backward(msgs) {
		if msgs[i].Role == llm.RoleAssistant {
			return i
		}
	}
	return -1
}

// fitsLastTurn reports whether the system prompt, the task and the last turn
// fit under limit.
func fitsLastTurn(msgs []llm.Message, system string, limit int) bool {
	if len(msgs) == 0 {
		return true
	}
	start := max(lastAssistant(msgs), 1)
	bytes := len(system) + messageBytes(&msgs[0]) + bytesOf(msgs[start:])
	return EstimateTokens(0, bytes) <= limit
}

func bytesOf(msgs []llm.Message) int {
	total := 0
	for i := range msgs {
		total += messageBytes(&msgs[i])
	}
	return total
}

func messageBytes(m *llm.Message) int {
	total := len(m.Content)
	for _, tc := range m.ToolCalls {
		total += len(tc.Arguments)
	}
	return total
}

func firstBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// limit is the working window: the model's, lowered by two truncations.
func (r *run) limit() int {
	limit := r.target.ContextTokens - r.target.MaxOutput
	if r.truncatedTo > 0 && r.truncatedTo < limit {
		return r.truncatedTo
	}
	return limit
}

// estimate is the prompt's size now: the last measured prompt plus what was
// added since, and the system prompt when nothing was measured.
func (r *run) estimate() int {
	bytes := bytesOf(r.history[r.measured:])
	if r.lastPrompt == 0 {
		bytes += len(r.agent.cfg.System)
	}
	return EstimateTokens(r.lastPrompt, bytes)
}

func (r *run) compactTo(limit int, force bool) ([]llm.Message, CompactionEvent, bool) {
	return compact(r.history, r.agent.cfg.System, r.estimate(), limit, r.done, force)
}

// compactNow compacts the history for the working limit; false means it
// doesn't fit.
func (r *run) compactNow(force bool) bool {
	msgs, ev, ok := r.compactTo(r.limit(), force)
	if ok {
		r.applyCompaction(msgs, ev)
	}
	return ok
}

func (r *run) fitHistory() *exitInfo {
	if r.compactNow(false) {
		return nil
	}
	return &exitInfo{reason: ExitContextOverflow, message: "the system prompt, task and last turn don't fit the context window"}
}

func (r *run) applyCompaction(msgs []llm.Message, ev CompactionEvent) {
	if ev.Elided == 0 && ev.DroppedTurns == 0 {
		return
	}
	r.history = msgs
	r.lastPrompt = 0
	r.measured = 0
	r.record(kindCompaction, ev)
}
