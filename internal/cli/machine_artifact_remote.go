package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// artifactRequestTimeout bounds one remote artifact command.
const artifactRequestTimeout = 3 * time.Minute

// remoteArtifacts is the controller's store, reached over the artifact API
// from an enrolled agent machine (ADR 0087).
type remoteArtifacts struct{ client *envagent.APIClient }

// newRemoteArtifacts builds the client from CONFIG_DIR/environment, or
// artifact_unreachable from its *envagent.APIError.
func (a *App) newRemoteArtifacts() (*remoteArtifacts, error) {
	client, err := envagent.NewAPIClient(envagent.NewFiles(a.paths.Environment))
	if err != nil {
		return nil, unreachableError(err)
	}
	return &remoteArtifacts{client: client}, nil
}

// unreachableError is artifact_unreachable for an *envagent.APIError, with
// details {"reason": e.Reason, "endpoint": e.Endpoint or null}; any other
// error is returned as it is.
func unreachableError(err error) error {
	apiErr, ok := errors.AsType[*envagent.APIError](err)
	if !ok {
		return err
	}
	var endpoint any
	if apiErr.Endpoint != "" {
		endpoint = apiErr.Endpoint
	}
	return &cliError{
		code:    codeArtifactUnreachable,
		err:     apiErr,
		details: map[string]any{"reason": apiErr.Reason, "endpoint": endpoint},
	}
}

// offline is artifact_unreachable with reason offline, for a failure while
// the controller's answer was being read.
func (r *remoteArtifacts) offline(err error) error {
	return unreachableError(&envagent.APIError{Reason: envproto.ReasonOffline, Endpoint: r.client.Endpoint(), Err: err})
}

// callerHeaders are the Ws-* headers that name c's workspace and its tree's
// root on the remote (ADR 0087).
func callerHeaders(c *machineArtifactCaller) http.Header {
	h := http.Header{}
	workspace := ""
	if c.actor.Workspace != nil {
		workspace = *c.actor.Workspace
	}
	name := workspace
	if c.actor.Name != nil {
		name = *c.actor.Name
	}
	h.Set(envproto.HeaderWorkspace, url.QueryEscape(workspace))
	h.Set(envproto.HeaderWorkspaceName, url.QueryEscape(name))
	h.Set(envproto.HeaderRoot, url.QueryEscape(c.root))
	return h
}

// send sends one request as c and returns the answer for a 2xx status; any
// other status is its error envelope, as an error.
func (r *remoteArtifacts) send(ctx context.Context, c *machineArtifactCaller, method, path string, query url.Values, body io.Reader, size int64) (*http.Response, error) {
	resp, err := r.client.Do(ctx, method, path, query, callerHeaders(c), body, size)
	if err != nil {
		return nil, unreachableError(err)
	}
	if resp.StatusCode/100 == 2 {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return nil, remoteStatusError(resp)
}

// remoteStatusError is a non-2xx answer's error: its envelope's code, message
// and details, as scopeReplyError keeps them, or codeInternal when the body
// isn't an envelope.
func remoteStatusError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, envproto.MaxAPIJSONBytes+1))
	var env envproto.ErrorEnvelope
	if err != nil || json.Unmarshal(body, &env) != nil || env.Error.Code == "" {
		return newError(codeInternal, fmt.Errorf("the controller answered %s without an error", resp.Status))
	}
	return scopeReplyError(&env.Error)
}

// call sends one request as c and returns the answer's body for a 2xx
// status: at most envproto.MaxAPIJSONBytes, codeInternal beyond that.
func (r *remoteArtifacts) call(ctx context.Context, c *machineArtifactCaller, method, path string, query url.Values, body io.Reader, size int64) ([]byte, error) {
	resp, err := r.send(ctx, c, method, path, query, body, size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, envproto.MaxAPIJSONBytes+1))
	if err != nil {
		return nil, r.offline(err)
	}
	if len(data) > envproto.MaxAPIJSONBytes {
		return nil, newError(codeInternal, errors.New("the controller's answer is too large"))
	}
	return data, nil
}

// writeRemoteJSON prints the controller's answer: as it is when ws has no
// warnings of its own, else with them appended to its "warnings".
func (a *App) writeRemoteJSON(body []byte) error {
	own := a.warnings()
	if len(own) == 0 {
		return a.writeJSON(json.RawMessage(body))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return newError(codeInternal, fmt.Errorf("reading the controller's answer: %w", err))
	}
	var warnings []string
	if raw, ok := fields["warnings"]; ok {
		if err := json.Unmarshal(raw, &warnings); err != nil {
			return newError(codeInternal, fmt.Errorf("reading the controller's warnings: %w", err))
		}
	}
	merged, err := json.Marshal(append(warnings, own...))
	if err != nil {
		return err
	}
	fields["warnings"] = merged
	return a.writeJSON(fields)
}

// artifactPath is the API route of artifact id.
func artifactPath(id string) string {
	return envproto.ArtifactsPath + "/" + id
}

// setIfSet sets key in q to value when value isn't empty.
func setIfSet(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

// remoteArtifact is artifact id's show answer from the controller.
func remoteArtifact(ctx context.Context, c *machineArtifactCaller, id string) (artifactResultJSON, error) {
	body, err := c.remote.call(ctx, c, http.MethodGet, artifactPath(id), nil, nil, 0)
	if err != nil {
		return artifactResultJSON{}, err
	}
	var res artifactResultJSON
	if err = json.Unmarshal(body, &res); err != nil {
		return artifactResultJSON{}, newError(codeInternal, fmt.Errorf("reading the controller's answer: %w", err))
	}
	return res, nil
}

func (a *App) remoteAdd(ctx context.Context, c *machineArtifactCaller, path string, opts *artifactAddOptions) error {
	name, data, err := a.readArtifactInput(path, opts.name)
	if err != nil {
		return err
	}
	query := url.Values{"name": {name}}
	setIfSet(query, "title", opts.title)
	setIfSet(query, "note", opts.note)
	body, err := c.remote.call(ctx, c, http.MethodPost, envproto.ArtifactsPath, query, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	return a.writeRemoteJSON(body)
}

func (a *App) remoteList(ctx context.Context, c *machineArtifactCaller, kind string, mine bool, limit int) error {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	setIfSet(query, "kind", kind)
	if mine {
		query.Set("mine", "1")
	}
	body, err := c.remote.call(ctx, c, http.MethodGet, envproto.ArtifactsPath, query, nil, 0)
	if err != nil {
		return err
	}
	return a.writeRemoteJSON(body)
}

func (a *App) remoteShow(ctx context.Context, c *machineArtifactCaller, arg string) error {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return err
	}
	body, err := c.remote.call(ctx, c, http.MethodGet, artifactPath(id), nil, nil, 0)
	if err != nil {
		return err
	}
	return a.writeRemoteJSON(body)
}

// remoteGet copies revision rev of id (0 for the latest) from the controller
// into out, as getArtifact does locally. The controller records the read.
func (a *App) remoteGet(ctx context.Context, c *machineArtifactCaller, id string, rev int, out string) error {
	art, err := remoteArtifact(ctx, c, id)
	if err != nil {
		return err
	}
	want := rev
	if want == 0 {
		want = art.Artifact.Latest
	}
	revision, ok := revisionNumbered(art.Artifact.Revisions, want)
	if !ok {
		return newError(codeArtifactUnknown, fmt.Errorf("%s has no revision %d", id, want))
	}
	if revision.Size < 0 || revision.Size > artifact.MaxImageSize {
		return newError(codeInternal, fmt.Errorf("the controller says revision %d is %d bytes, over the %d byte limit", revision.Rev, revision.Size, artifact.MaxImageSize))
	}
	dir, err := artifactDir(c, out, id, revision.Rev)
	if err != nil {
		return err
	}
	resp, err := c.remote.send(ctx, c, http.MethodGet, artifactPath(id)+"/content", url.Values{"rev": {strconv.Itoa(revision.Rev)}}, nil, 0)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err = checkContentAnswer(resp, revision.Rev); err != nil {
		return err
	}
	path, err := writeArtifactStream(dir, art.Artifact.Name, resp.Body, revision.Size, resp.Header.Get(envproto.HeaderSHA256))
	if err != nil {
		return err
	}
	return a.writeJSON(machineGetJSON{
		Artifact:    art.Artifact.artifactSummaryJSON,
		Rev:         revision.Rev,
		Path:        path,
		ContentType: resp.Header.Get(envproto.HeaderContentType),
		Size:        revision.Size,
		Warnings:    a.artifactWarnings(art.Warnings),
	})
}

// revisionNumbered is the revision of revs numbered rev.
func revisionNumbered(revs []revisionJSON, rev int) (revisionJSON, bool) {
	for _, r := range revs {
		if r.Rev == rev {
			return r, true
		}
	}
	return revisionJSON{}, false
}

// checkContentAnswer refuses a content answer that isn't revision rev, or
// whose body is over the size limit.
func checkContentAnswer(resp *http.Response, rev int) error {
	if got := resp.Header.Get(envproto.HeaderRev); got != strconv.Itoa(rev) {
		return newError(codeInternal, fmt.Errorf("the controller answered revision %q, not %d", got, rev))
	}
	if resp.ContentLength > artifact.MaxImageSize {
		return newError(codeInternal, fmt.Errorf("the controller's file is over %d bytes", artifact.MaxImageSize))
	}
	return nil
}

// writeArtifactStream writes r to dir/name as get does locally (dir 0700,
// a temp file 0600 renamed into place), reading at most size+1 bytes, and
// keeps it only when exactly size bytes with SHA-256 sum arrived; otherwise
// it removes the temp file and is codeInternal "the download didn't match
// the revision". name must pass artifact.CheckName.
func writeArtifactStream(dir, name string, r io.Reader, size int64, sum string) (string, error) {
	if err := artifact.CheckName(name); err != nil {
		return "", newError(codeInternal, fmt.Errorf("the artifact's name %q isn't a file name: %w", name, err))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("making %s: %w", dir, err)
	}
	dst := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".get-*")
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(r, size+1))
	if err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != sum {
		_ = tmp.Close()
		return "", newError(codeInternal, errors.New("the download didn't match the revision"))
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	if err = os.Rename(tmp.Name(), dst); err != nil {
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	return dst, nil
}

func (a *App) remotePut(ctx context.Context, c *machineArtifactCaller, arg, path string, opts *machinePutOptions) error {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return err
	}
	art, err := remoteArtifact(ctx, c, id)
	if err != nil {
		return err
	}
	_, data, err := a.readArtifactInput(path, art.Artifact.Name)
	if err != nil {
		return err
	}
	query := url.Values{}
	if opts.baseRev > 0 {
		query.Set("base_rev", strconv.Itoa(opts.baseRev))
	}
	setIfSet(query, "note", opts.note)
	body, err := c.remote.call(ctx, c, http.MethodPost, artifactPath(id)+"/revisions", query, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	return a.writeRemoteJSON(body)
}

// remoteRemove deletes each artifact the arguments name, as removeArtifacts
// does locally: one failure doesn't stop the others.
func (a *App) remoteRemove(ctx context.Context, c *machineArtifactCaller, args []string) error {
	var items itemResults
	results := make([]artifactRemoveJSON, 0, len(args))
	for _, arg := range args {
		id, err := remoteRemoveOne(ctx, c, arg)
		results = append(results, artifactRemoveJSON{ID: id, Removed: err == nil, Error: items.add(id, err)})
	}
	return a.finishItems("artifacts", results, &items)
}

// remoteRemoveOne deletes the artifact arg names and returns its id, or arg
// when it names none.
func remoteRemoveOne(ctx context.Context, c *machineArtifactCaller, arg string) (string, error) {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return arg, err
	}
	_, err = c.remote.call(ctx, c, http.MethodDelete, artifactPath(id), nil, nil, 0)
	return id, err
}

func (a *App) remoteCommentList(ctx context.Context, c *machineArtifactCaller, arg string, openOnly bool) error {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return err
	}
	query := url.Values{}
	if openOnly {
		query.Set("open", "1")
	}
	body, err := c.remote.call(ctx, c, http.MethodGet, artifactPath(id)+"/threads", query, nil, 0)
	if err != nil {
		return err
	}
	return a.writeRemoteJSON(body)
}

func (a *App) remoteCommentAdd(ctx context.Context, c *machineArtifactCaller, arg string, opts *commentAddOptions, cmd *cobra.Command) error {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return err
	}
	if err = checkCommentFlags(cmd, opts); err != nil {
		return err
	}
	text, err := a.commentBody(cmd, opts)
	if err != nil {
		return err
	}
	req := envproto.CommentAddBody{Body: text}
	if cmd.Flags().Changed("reply") {
		req.Reply = opts.reply
	}
	if cmd.Flags().Changed("quote") {
		req.Quote, req.Line = opts.quote, opts.line
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encoding the comment: %w", err)
	}
	body, err := c.remote.call(ctx, c, http.MethodPost, artifactPath(id)+"/threads", nil, bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	return a.writeRemoteJSON(body)
}

func (a *App) remoteCommentResolve(ctx context.Context, c *machineArtifactCaller, args []string, reopen bool) error {
	id, _, err := artifactRef(args[0], 0, false)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(envproto.CommentResolveBody{Reopen: reopen})
	if err != nil {
		return fmt.Errorf("encoding the resolution: %w", err)
	}
	path := artifactPath(id) + "/threads/" + url.PathEscape(args[1]) + "/resolve"
	body, err := c.remote.call(ctx, c, http.MethodPost, path, nil, bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	return a.writeRemoteJSON(body)
}
