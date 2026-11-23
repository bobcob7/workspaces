package envagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// workspaceIDPattern is a workspace's id, the name ws gives its directory.
var workspaceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// NoOperations answers every operation with usage "this agent runs no
// operations"; ws env agent run uses it until T4.
type NoOperations struct{}

// Run refuses every operation.
func (NoOperations) Run(context.Context, string, any) (json.RawMessage, error) {
	return nil, &envproto.ReplyError{Code: "usage", Message: "this agent runs no operations"}
}

// dispatch handles a request from env/<id>/req that isn't a reply: decodes
// the envelope, looks the op up, checks its caller is CallerController,
// CallerInternal or CallerTerminal (the agent can't tell the controller's
// terminal path from its call path; the controller refuses terminal ops on call), decodes and validates the arguments, checks workspace
// references with CheckWorkspaceRef, then either replies at once (a
// refusal, or ca.update, which runs on the session loop as today) or hands
// the operation to runOp on its own goroutine. It never blocks the session
// loop on an operation.
func (a *Agent) dispatch(ctx context.Context, id *Identity, msg *envproto.Message) {
	var req envproto.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		a.reply(ctx, id, msg, envproto.ErrorReply("usage", "malformed request"))
		return
	}
	if req.Op == envproto.OpCAUpdate {
		a.reply(ctx, id, msg, a.updateRoots(ctx, id, req.Args))
		return
	}
	spec, args, err := a.admit(&req)
	if err != nil {
		a.reply(ctx, id, msg, refusalReply(err))
		return
	}
	if !a.startOp(ctx, id, msg, &req, spec, args) {
		a.reply(ctx, id, msg, envproto.ErrorReply("internal", "the agent has too many operations waiting"))
	}
}

// startOp runs the operation on a goroutine the agent tracks, reporting false
// when queueSlots operations are already running or waiting for a slot.
func (a *Agent) startOp(ctx context.Context, id *Identity, msg *envproto.Message, req *envproto.Request, spec envproto.OpSpec, args any) bool {
	select {
	case a.queue <- struct{}{}:
	default:
		return false
	}
	a.running.Go(func() {
		defer func() { <-a.queue }()
		a.runOp(ctx, id, msg, req, spec, args)
	})
	return true
}

// remembered reports whether the reply of op is kept for duplicates. The
// read-only operations are not: agent.logs can hold secrets, and the
// controller asks again for a fresh answer.
func remembered(op string) bool {
	switch op {
	case envproto.OpAgentLogs, envproto.OpAgentStatus, envproto.OpWorkspaceList:
		return false
	}
	return true
}

// requestSum is the hash of a request's op and arguments.
func requestSum(req *envproto.Request) string {
	h := sha256.New()
	h.Write([]byte(req.Op))
	h.Write([]byte{0})
	h.Write(req.Args)
	return hex.EncodeToString(h.Sum(nil))
}

// admit checks a request for an operation the controller may send: its
// caller, its deadline, its request id and its arguments. It returns the
// operation's spec and the decoded arguments.
func (a *Agent) admit(req *envproto.Request) (envproto.OpSpec, any, error) {
	spec, ok := envproto.LookupOp(req.Op)
	if !ok || (spec.Caller != envproto.CallerController && spec.Caller != envproto.CallerInternal && spec.Caller != envproto.CallerTerminal) {
		return envproto.OpSpec{}, nil, &envproto.ReplyError{Code: "usage", Message: "unknown operation " + req.Op}
	}
	if req.Deadline == nil {
		return envproto.OpSpec{}, nil, &envproto.ReplyError{Code: "usage", Message: req.Op + " needs a deadline"}
	}
	if req.Deadline.After(a.opts.Now().Add(spec.Timeout + envproto.Grace)) {
		return envproto.OpSpec{}, nil, &envproto.ReplyError{Code: "usage", Message: req.Op + " has a deadline too far away"}
	}
	if !envproto.ValidRequestID(req.RequestID) {
		return envproto.OpSpec{}, nil, &envproto.ReplyError{Code: "usage", Message: "malformed request id"}
	}
	args, err := envproto.DecodeArgs(req.Op, req.Args)
	if err != nil {
		return envproto.OpSpec{}, nil, err
	}
	for _, ref := range workspaceRefs(args) {
		if err := CheckWorkspaceRef(a.opts.WorkspacesRoot, ref); err != nil {
			return envproto.OpSpec{}, nil, err
		}
	}
	return spec, args, nil
}

// workspaceRefs returns the workspace references in args.
func workspaceRefs(args any) []string {
	switch a := args.(type) {
	case *envproto.CreateArgs:
		if a.Parent != nil {
			return []string{*a.Parent}
		}
	case *envproto.WorkspaceArgs:
		return []string{a.Workspace}
	case *envproto.RmArgs:
		return []string{a.Workspace}
	case *envproto.RenameArgs:
		return []string{a.Workspace}
	case *envproto.HideArgs:
		return []string{a.Workspace}
	case *envproto.LogsArgs:
		return []string{a.Workspace}
	case *envproto.SendArgs:
		return []string{a.Workspace}
	case *envproto.TerminalOpenArgs:
		if a.Workspace != nil {
			return []string{*a.Workspace}
		}
	}
	return nil
}

// runOp runs the operation under context.WithDeadline(ctx, deadline)
// through the dedupe store, replies on msg's response topic, and when the
// op's spec has Changes and the reply is ok, signals a.changed (buffered 1,
// non-blocking send). Only the request that owns the id takes one of MaxOps
// slots, waiting for it until the deadline, else the reply is internal "the
// agent was busy until the deadline"; a duplicate waits for the owner without one.
func (a *Agent) runOp(ctx context.Context, id *Identity, msg *envproto.Message, req *envproto.Request, spec envproto.OpSpec, args any) {
	opCtx, cancel := context.WithDeadline(ctx, *req.Deadline)
	defer cancel()
	run := func() []byte {
		if !a.acquire(opCtx) {
			return envproto.ErrorReply("internal", "the agent was busy until the deadline")
		}
		defer a.release()
		return a.execute(opCtx, spec, args)
	}
	reply, err := a.requests.do(opCtx, req.RequestID, requestSum(req), remembered(req.Op), run)
	switch {
	case errors.Is(err, errRequestMismatch):
		a.reply(ctx, id, msg, envproto.ErrorReply("usage", "request id reused for a different request"))
		return
	case errors.Is(err, errAbandoned) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		a.reply(ctx, id, msg, envproto.ErrorReply("internal", "the operation didn't finish before its deadline"))
		return
	case err != nil:
		a.log.ErrorContext(ctx, "operation failed", "op", spec.Name, "error", err)
		a.reply(ctx, id, msg, envproto.ErrorReply("internal", "the operation failed"))
		return
	}
	a.reply(ctx, id, msg, reply)
	if spec.Changes && replyOK(reply) {
		a.signalChanged()
	}
}

// replyOK reports whether reply is a successful reply.
func replyOK(reply []byte) bool {
	var r envproto.Reply
	return json.Unmarshal(reply, &r) == nil && r.OK
}

// execute runs one operation and returns its reply, which must fit a message.
func (a *Agent) execute(ctx context.Context, spec envproto.OpSpec, args any) []byte {
	var result json.RawMessage
	var err error
	if spec.Caller == envproto.CallerTerminal {
		result, err = a.terminalOp(ctx, spec.Name, args)
	} else {
		result, err = a.ops.Run(ctx, spec.Name, args)
	}
	if err != nil {
		if _, ok := errors.AsType[*envproto.ReplyError](err); !ok {
			a.log.WarnContext(ctx, "operation failed", "op", spec.Name, "error", err)
		}
		return refusalReply(err)
	}
	reply, err := envproto.OKReply(result)
	if err != nil {
		a.log.ErrorContext(ctx, "reply not encoded", "op", spec.Name, "error", err)
		return envproto.ErrorReply("internal", "reply not encoded")
	}
	if len(reply) > envproto.MaxRequestBytes {
		return envproto.ErrorReply("internal", "the result is too large to send")
	}
	return reply
}

// refusalReply is the reply for err: the code and message of an ArgsError or a
// ReplyError, else "internal".
func refusalReply(err error) []byte {
	if ae, ok := errors.AsType[*envproto.ArgsError](err); ok {
		return envproto.ErrorReply(ae.Code, ae.Message)
	}
	if re, ok := errors.AsType[*envproto.ReplyError](err); ok {
		return envproto.ErrorReply(re.Code, re.Message)
	}
	return envproto.ErrorReply("internal", "the operation failed")
}

// acquire takes one of the MaxOps slots, reporting false when ctx has ended
// or ends first. A done ctx is checked first, since select picks a ready case
// at random.
func (a *Agent) acquire(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case a.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// release frees the slot acquire took.
func (a *Agent) release() {
	<-a.slots
}

// signalChanged asks the session loop to republish the snapshot, without blocking.
func (a *Agent) signalChanged() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

// CheckWorkspaceRef returns nil for an id (workspaceIDPattern), or for an
// absolute path under root after filepath.Clean that is not root itself; else
// an *envproto.ReplyError with code outside_workspace naming ref. It doesn't
// touch the file system.
func CheckWorkspaceRef(root, ref string) error {
	if workspaceIDPattern.MatchString(ref) {
		return nil
	}
	if filepath.IsAbs(ref) && underRoot(root, filepath.Clean(ref)) {
		return nil
	}
	return &envproto.ReplyError{
		Code:    "outside_workspace",
		Message: fmt.Sprintf("%q is neither a workspace id nor a path under the workspaces", ref),
	}
}

// underRoot reports whether clean is strictly inside root.
func underRoot(root, clean string) bool {
	return strings.HasPrefix(clean, filepath.Clean(root)+string(filepath.Separator))
}
