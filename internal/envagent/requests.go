package envagent

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// handle routes a message from env/<id>/req. A reply goes to the request
// waiting for it; anything else is a request, dispatched to be answered on its
// response topic.
func (a *Agent) handle(ctx context.Context, id *Identity, msg *envproto.Message) {
	if a.deliverReply(msg) {
		return
	}
	a.dispatch(ctx, id, msg)
}

// deliverReply hands msg to the request it answers, reporting whether one was waiting.
func (a *Agent) deliverReply(msg *envproto.Message) bool {
	if len(msg.CorrelationData) == 0 {
		return false
	}
	waiting, ok := a.pending[string(msg.CorrelationData)]
	if !ok {
		return false
	}
	select {
	case waiting <- *msg:
	default:
	}
	return true
}

// updateRoots saves the roots in args, updates id, and republishes the hello
// so the controller sees the new fingerprints.
func (a *Agent) updateRoots(ctx context.Context, id *Identity, args json.RawMessage) []byte {
	var in envproto.CAUpdateArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return envproto.ErrorReply("usage", "malformed ca.update arguments")
	}
	roots, err := parseRoots([]byte(in.CABundle))
	if err != nil {
		return envproto.ErrorReply("usage", err.Error())
	}
	if err = a.files.SaveRoots(roots); err != nil {
		a.log.ErrorContext(ctx, "roots not saved", "id", id.ID, "error", err)
		return envproto.ErrorReply("internal", "the roots weren't saved")
	}
	id.Roots = roots
	reply, err := envproto.OKReply(envproto.CAUpdateResult{Fingerprints: id.Fingerprints()})
	if err != nil {
		a.log.ErrorContext(ctx, "reply not encoded", "id", id.ID, "error", err)
		return envproto.ErrorReply("internal", "reply not encoded")
	}
	if err := a.publishHello(ctx, id); err != nil {
		a.log.WarnContext(ctx, "hello not published", "error", err)
	}
	a.log.InfoContext(ctx, "ca roots updated", "id", id.ID)
	return reply
}

// reply publishes payload to msg's response topic with its correlation data.
// A response topic outside env/<id>/res is never published to.
func (a *Agent) reply(ctx context.Context, id *Identity, msg *envproto.Message, payload []byte) {
	if !replyTopicOK(id.ID, msg.ResponseTopic) {
		a.log.WarnContext(ctx, "reply not sent: response topic isn't the agent's", "id", id.ID)
		return
	}
	err := a.client.Publish(ctx, envproto.Message{
		Topic:           msg.ResponseTopic,
		Payload:         payload,
		QoS:             1,
		CorrelationData: msg.CorrelationData,
	})
	if err != nil {
		a.log.WarnContext(ctx, "reply not published", "id", id.ID, "error", err)
	}
}

// replyTopicOK reports whether topic is under env/<id>/res.
func replyTopicOK(id, topic string) bool {
	return strings.HasPrefix(topic, envproto.Topic(id, envproto.LeafRes)+"/")
}

// hello is the online status for id: the ws version, the platform, Claude's
// state from the probe, the certificate and the roots' fingerprints (ADR 0073).
func (a *Agent) hello(ctx context.Context, id *Identity) envproto.Hello {
	state, version := a.claude.Probe(ctx)
	return envproto.Hello{
		Status:         envproto.StatusOnline,
		At:             a.opts.Now().UTC(),
		Protocol:       envproto.Protocol,
		WSVersion:      a.opts.Version,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Claude:         state,
		ClaudeVersion:  version,
		CertSerial:     id.Cert.SerialNumber.Text(16),
		CertNotAfter:   id.Cert.NotAfter.UTC(),
		CAFingerprints: id.Fingerprints(),
		Conn:           a.connID,
	}
}
