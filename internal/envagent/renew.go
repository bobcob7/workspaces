package envagent

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// correlationBytes makes 16 hex characters of correlation data.
const correlationBytes = 8

var (
	errRenewRefused   = errors.New("the controller refused the renewal")
	errRequestTimeout = errors.New("the controller didn't answer in time")
	errDisconnected   = errors.New("the connection dropped while waiting for the controller")
)

// renewDue reports whether now is in the renewal window: at or after
// notAfter minus renewBeforeDays, and before notAfter.
func renewDue(notAfter, now time.Time, renewBeforeDays int) bool {
	window := time.Duration(renewBeforeDays) * 24 * time.Hour
	return !now.Before(notAfter.Add(-window)) && now.Before(notAfter)
}

// renewIfDue renews the certificate when renewDue says it is time. A failure
// is logged; the next check tries again.
func (a *Agent) renewIfDue(ctx context.Context, id *Identity) {
	if !renewDue(id.Cert.NotAfter, a.opts.Now(), a.opts.RenewBeforeDays) {
		return
	}
	if err := a.renew(ctx, id); err != nil {
		a.log.WarnContext(ctx, "certificate not renewed", "error", err)
	}
}

// renew asks the controller for a certificate for a new key, checks that it
// is for id and that key, saves both, and republishes the hello.
func (a *Agent) renew(ctx context.Context, id *Identity) error {
	key, csrPEM, err := newKeyAndCSR(a.random)
	if err != nil {
		return err
	}
	cert, err := a.requestCertificate(ctx, id, csrPEM)
	if err != nil {
		return err
	}
	if err := checkIdentity(cert, id.ID, key); err != nil {
		return err
	}
	if err := a.files.SaveCertificate(key, cert); err != nil {
		return err
	}
	id.Key, id.Cert = key, cert
	if err := a.publishHello(ctx, id); err != nil {
		a.log.WarnContext(ctx, "hello not published", "error", err)
	}
	a.log.InfoContext(ctx, "certificate renewed", "id", id.ID)
	return nil
}

// requestCertificate sends cert.renew for csrPEM on env/<id>/up, asking for
// the reply on env/<id>/req, and returns the certificate in the reply.
func (a *Agent) requestCertificate(ctx context.Context, id *Identity, csrPEM []byte) (*x509.Certificate, error) {
	correlation, err := newCorrelation(a.random)
	if err != nil {
		return nil, err
	}
	args, err := json.Marshal(envproto.RenewArgs{CSR: string(csrPEM)})
	if err != nil {
		return nil, fmt.Errorf("encoding the renewal: %w", err)
	}
	payload, err := json.Marshal(envproto.Request{Op: envproto.OpCertRenew, Args: args})
	if err != nil {
		return nil, fmt.Errorf("encoding the renewal: %w", err)
	}
	replies := make(chan envproto.Message, 1)
	a.pending[correlation] = replies
	defer delete(a.pending, correlation)
	err = a.client.Publish(ctx, envproto.Message{
		Topic:           envproto.Topic(id.ID, envproto.LeafUp),
		Payload:         payload,
		QoS:             1,
		ResponseTopic:   envproto.Topic(id.ID, envproto.LeafReq),
		CorrelationData: []byte(correlation),
	})
	if err != nil {
		return nil, err
	}
	reply, err := a.awaitReply(ctx, id, replies)
	if err != nil {
		return nil, err
	}
	return decodeRenewal(&reply)
}

// awaitReply waits for the reply on replies, meanwhile handling the other
// messages that arrive, for at most RequestTimeout.
func (a *Agent) awaitReply(ctx context.Context, id *Identity, replies <-chan envproto.Message) (envproto.Message, error) {
	timeout := a.opts.After(a.opts.RequestTimeout)
	done := a.client.Done()
	for {
		select {
		case reply := <-replies:
			return reply, nil
		case msg := <-a.inbox:
			a.handle(ctx, id, &msg)
		case <-timeout:
			return envproto.Message{}, errRequestTimeout
		case <-done:
			return envproto.Message{}, errDisconnected
		case <-ctx.Done():
			return envproto.Message{}, ctx.Err()
		}
	}
}

// decodeRenewal returns the certificate in a cert.renew reply, or
// errRenewRefused with the controller's message.
func decodeRenewal(msg *envproto.Message) (*x509.Certificate, error) {
	var reply envproto.Reply
	if err := json.Unmarshal(msg.Payload, &reply); err != nil {
		return nil, fmt.Errorf("reading the controller's reply: %w", err)
	}
	if !reply.OK {
		return nil, fmt.Errorf("%w: %s", errRenewRefused, replyMessage(reply.Error))
	}
	var result envproto.RenewResult
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		return nil, fmt.Errorf("reading the renewal: %w", err)
	}
	return decodeCertPEM([]byte(result.Cert))
}

// replyMessage is the message of a reply's error, or a fixed text when there is none.
func replyMessage(e *envproto.ReplyError) string {
	if e == nil || e.Message == "" {
		return "the controller gave no reason"
	}
	return e.Message
}

// newCorrelation returns 16 random hex characters to correlate a request with its reply.
func newCorrelation(random io.Reader) (string, error) {
	buf := make([]byte, correlationBytes)
	if _, err := io.ReadFull(random, buf); err != nil {
		return "", fmt.Errorf("reading a request id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
