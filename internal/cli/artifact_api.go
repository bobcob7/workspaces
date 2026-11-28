package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// artifactAPI is the controller's /v1/artifacts handler (ADR 0087): the ws
// machine artifact commands for an agent on a remote environment, answering
// the JSON those commands print.
func (a *App) artifactAPI() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/artifacts", withAPICaller(a.apiList))
	mux.Handle("POST /v1/artifacts", withAPICaller(a.apiAdd))
	mux.Handle("GET /v1/artifacts/{id}", withAPICaller(a.apiShow))
	mux.Handle("GET /v1/artifacts/{id}/content", withAPICaller(a.apiContent))
	mux.Handle("POST /v1/artifacts/{id}/revisions", withAPICaller(a.apiPut))
	mux.Handle("DELETE /v1/artifacts/{id}", withAPICaller(a.apiRemove))
	mux.Handle("GET /v1/artifacts/{id}/threads", withAPICaller(a.apiThreads))
	mux.Handle("POST /v1/artifacts/{id}/threads", withAPICaller(a.apiAddThread))
	mux.Handle("POST /v1/artifacts/{id}/threads/{thread}/resolve", withAPICaller(a.apiResolveThread))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, usageErrorf("no such artifact route"))
	}))
	return mux
}

// remoteAPICaller is who a request acts for: the environment the controller
// identified, and the workspace its headers name.
type remoteAPICaller struct {
	env       environment.APICaller
	workspace string
	name      string
	root      string // the remote's root, as sent
}

// apiHandlerFunc is a route's handler, given the caller its headers name.
type apiHandlerFunc func(w http.ResponseWriter, r *http.Request, c *remoteAPICaller)

// withAPICaller runs fn with the request's caller, or answers the error reading it.
func withAPICaller(fn apiHandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := apiCaller(r)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		fn(w, r, &c)
	})
}

// apiCaller reads and checks the request's caller. Each header is
// url.QueryUnescape'd and must be artifact.ValidWorkspaceID; a missing
// Ws-Workspace-Name is the workspace id. A missing caller in the context is
// codeInternal.
func apiCaller(r *http.Request) (remoteAPICaller, error) {
	env, ok := environment.APICallerFrom(r.Context())
	if !ok {
		return remoteAPICaller{}, newError(codeInternal, errors.New("the artifact API request has no verified caller"))
	}
	workspace, err := apiHeader(r, envproto.HeaderWorkspace)
	if err != nil {
		return remoteAPICaller{}, err
	}
	name := workspace
	if r.Header.Get(envproto.HeaderWorkspaceName) != "" {
		if name, err = apiHeader(r, envproto.HeaderWorkspaceName); err != nil {
			return remoteAPICaller{}, err
		}
	}
	root, err := apiHeader(r, envproto.HeaderRoot)
	if err != nil {
		return remoteAPICaller{}, err
	}
	return remoteAPICaller{env: env, workspace: workspace, name: name, root: root}, nil
}

// apiHeader is header key, unescaped, when it is a workspace id; usage otherwise.
func apiHeader(r *http.Request, key string) (string, error) {
	value, err := url.QueryUnescape(r.Header.Get(key))
	if err != nil || !artifact.ValidWorkspaceID(value) {
		return "", usageErrorf("%s must be a workspace id", key)
	}
	return value, nil
}

// actor is the caller as an artifact actor, with its environment's name.
func (c *remoteAPICaller) actor() artifact.Actor {
	return artifact.RemoteAgent(c.workspace, c.name, c.env.EnvironmentName)
}

// storedRoot is the root the store records: "<environment id>/<root>".
func (c *remoteAPICaller) storedRoot() string {
	return c.env.EnvironmentID + "/" + c.root
}

// writeAPIError answers err: a *cliError keeps its code, message and details;
// anything else is 500 "internal" with the message "internal error" (logged,
// not sent).
func writeAPIError(w http.ResponseWriter, err error) {
	ce := asCLIError(err)
	status := apiStatus(ce.code)
	if status == http.StatusInternalServerError {
		slog.Default().Error("artifact api", "error", err)
		writeAPIFailure(w, status, codeInternal, "internal error", nil)
		return
	}
	writeAPIFailure(w, status, ce.code, ce.Error(), ce.details)
}

// apiStatus is the HTTP status for a code: usage 400, artifact_invalid 400
// (413 when the body was too large), artifact_unknown 404, anchor_not_found
// 404, artifact_conflict 409, artifact_full 409, artifact_forbidden 403,
// anything else 500.
func apiStatus(code errorCode) int {
	statuses := map[errorCode]int{
		codeUsage:             http.StatusBadRequest,
		codeArtifactInvalid:   http.StatusBadRequest,
		codeArtifactUnknown:   http.StatusNotFound,
		codeAnchorNotFound:    http.StatusNotFound,
		codeArtifactConflict:  http.StatusConflict,
		codeArtifactFull:      http.StatusConflict,
		codeArtifactForbidden: http.StatusForbidden,
	}
	if status, ok := statuses[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// writeAPIFailure answers with the {"error": …} envelope.
func writeAPIFailure(w http.ResponseWriter, status int, code errorCode, message string, details map[string]any) {
	writeAPIJSON(w, status, envproto.ErrorEnvelope{Error: envproto.ReplyError{Code: string(code), Message: message, Details: details}})
}

// writeAPIJSON answers with v as JSON.
func writeAPIJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeAPIError(w, fmt.Errorf("encoding the answer: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

// apiWarnings is the store's warnings, never null. The API answers only the
// store's, not the daemon's own.
func apiWarnings(store []string) []string {
	if store == nil {
		return []string{}
	}
	return store
}

// apiRemoved reports which local workspaces are gone, or nil when the list
// can't be read.
func (a *App) apiRemoved() func(string) *bool {
	list, err := a.workspaces.List()
	if err != nil {
		return nil
	}
	return removedFunc(list)
}

// apiEntry is the artifact arg names, with its revisions, or the error the
// store gives.
func (a *App) apiEntry(arg string) (artifact.Entry, error) {
	id, err := apiID(arg)
	if err != nil {
		return artifact.Entry{}, err
	}
	entry, err := a.artifactStore().Show(id)
	if err != nil {
		return artifact.Entry{}, artifactError(err)
	}
	return entry, nil
}

// apiID is the artifact id in arg, which may not be a valid one (artifact_unknown).
func apiID(arg string) (string, error) {
	id, _, err := artifactRef(arg, 0, false)
	return id, err
}

// apiFlag is the query flag key: false when absent, true for "1", else usage.
func apiFlag(values url.Values, key string) (bool, error) {
	if !values.Has(key) {
		return false, nil
	}
	if values.Get(key) == "1" {
		return true, nil
	}
	return false, usageErrorf("%s must be 1 or absent, not %q", key, values.Get(key))
}

// apiRevQuery is the revision in the query's rev: 0 when absent, else at least 1.
func apiRevQuery(values url.Values) (int, error) {
	return apiPositiveQuery(values, "rev", 0)
}

// apiBaseRevQuery is base_rev: 0 when absent, else at least 1.
func apiBaseRevQuery(values url.Values) (int, error) {
	return apiPositiveQuery(values, "base_rev", 0)
}

// apiPositiveQuery is the decimal query key from 1 up, or def when absent.
func apiPositiveQuery(values url.Values, key string, def int) (int, error) {
	if !values.Has(key) {
		return def, nil
	}
	n, err := strconv.Atoi(values.Get(key))
	if err != nil || n < 1 {
		return 0, usageErrorf("%s must be a decimal number from 1, not %q", key, values.Get(key))
	}
	return n, nil
}

// readAPIFile reads a request's file body, at most artifact.MaxImageSize
// bytes. A larger one is answered 413 artifact_invalid, and false is returned.
func readAPIFile(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	message := fmt.Sprintf("the file is over %d bytes", artifact.MaxImageSize)
	if r.ContentLength > artifact.MaxImageSize {
		writeAPIFailure(w, http.StatusRequestEntityTooLarge, codeArtifactInvalid, message, nil)
		return nil, false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, artifact.MaxImageSize))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeAPIFailure(w, http.StatusRequestEntityTooLarge, codeArtifactInvalid, message, nil)
		return nil, false
	case err != nil:
		writeAPIError(w, newError(codeArtifactInvalid, fmt.Errorf("reading the file: %w", err)))
		return nil, false
	}
	return data, true
}

// decodeAPIJSON decodes the request's body into v: exactly one JSON object
// with no unknown fields, at most envproto.MaxAPIJSONBytes. Anything else is
// usage.
func decodeAPIJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, envproto.MaxAPIJSONBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return usageErrorf("the body must be one JSON object: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return usageErrorf("the body must be one JSON object")
	}
	return nil
}

// checkAPIComment is usage unless the comment's fields agree: a reply starts
// no new thread, so it isn't given with a quote; a line and a negative line
// need a quote.
func checkAPIComment(body *envproto.CommentAddBody) error {
	switch {
	case body.Reply != "" && body.Quote != "":
		return usageErrorf("reply and quote can't be given together")
	case body.Line < 0:
		return usageErrorf("line must be at least 1, not %d", body.Line)
	case body.Line != 0 && body.Quote == "":
		return usageErrorf("line needs quote")
	}
	return nil
}

// apiList answers GET /v1/artifacts: the list command's JSON.
func (a *App) apiList(w http.ResponseWriter, r *http.Request, c *remoteAPICaller) {
	q := r.URL.Query()
	mine, err := apiFlag(q, "mine")
	if err != nil {
		writeAPIError(w, err)
		return
	}
	limit, err := apiPositiveQuery(q, "limit", defaultListLimit)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	kind := q.Get("kind")
	if err = errors.Join(checkArtifactKind(kind), checkListLimit(limit)); err != nil {
		writeAPIError(w, err)
		return
	}
	f := artifactFilter{kind: kind, mine: mine, root: c.storedRoot(), limit: limit}
	kept, warnings, err := a.keptArtifacts(&f)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	summaries := make([]artifactSummaryJSON, 0, len(kept))
	for _, e := range kept {
		summaries = append(summaries, summaryJSON(e))
	}
	writeAPIJSON(w, http.StatusOK, artifactListJSON{Artifacts: summaries, Warnings: apiWarnings(warnings)})
}

// apiAdd answers POST /v1/artifacts: the file in the body, stored as a new
// artifact named by the name query.
func (a *App) apiAdd(w http.ResponseWriter, r *http.Request, c *remoteAPICaller) {
	q := r.URL.Query()
	name := q.Get("name")
	if name == "" {
		writeAPIError(w, usageErrorf("adding an artifact needs a name"))
		return
	}
	data, ok := readAPIFile(w, r)
	if !ok {
		return
	}
	rec, err := a.artifactStore().Add(r.Context(), &artifact.NewArtifact{
		Name: name, Title: q.Get("title"), Note: q.Get("note"), Data: data, By: c.actor(), Root: c.storedRoot(),
	})
	if err != nil {
		writeAPIError(w, artifactError(err))
		return
	}
	writeAPIJSON(w, http.StatusCreated, artifactResultJSON{
		Artifact: fullJSON(&artifact.Entry{Record: rec}, a.apiRemoved()),
		Warnings: apiWarnings(nil),
	})
}

// apiShow answers GET /v1/artifacts/{id}: the show command's JSON.
func (a *App) apiShow(w http.ResponseWriter, r *http.Request, _ *remoteAPICaller) {
	entry, err := a.apiEntry(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, artifactResultJSON{Artifact: fullJSON(&entry, a.apiRemoved()), Warnings: apiWarnings(entry.Warnings)})
}

// apiContent answers GET /v1/artifacts/{id}/content: the revision's bytes,
// and records that the caller read it (ADR 0084).
func (a *App) apiContent(w http.ResponseWriter, r *http.Request, c *remoteAPICaller) {
	rev, err := apiRevQuery(r.URL.Query())
	if err != nil {
		writeAPIError(w, err)
		return
	}
	id, rev, err := artifactRef(r.PathValue("id"), rev, rev != 0)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	_, revision, data, err := a.artifactStore().Read(id, rev)
	if err != nil {
		writeAPIError(w, artifactError(err))
		return
	}
	a.recordAPIRead(r, c, id, revision.Rev)
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set(envproto.HeaderRev, strconv.Itoa(revision.Rev))
	h.Set(envproto.HeaderSHA256, revision.SHA256)
	h.Set(envproto.HeaderContentType, revision.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) //nolint:gosec // raw bytes as application/octet-stream, nosniff set above
}

// recordAPIRead records that c read revision rev of id. A failure is only logged: a link never fails the read.
func (a *App) recordAPIRead(r *http.Request, c *remoteAPICaller, id string, rev int) {
	link := artifact.NewLink{
		Workspace: c.workspace, Name: c.name, Root: c.root, Environment: c.env.EnvironmentName,
		Action: artifact.ActionRead, Rev: rev,
	}
	if err := a.artifactStore().RecordLinks(r.Context(), id, link); err != nil {
		a.log.WarnContext(r.Context(), "artifact api: link not recorded", "artifact", id, "error", err)
	}
}

// apiPut answers POST /v1/artifacts/{id}/revisions: the file in the body, as a
// new revision.
func (a *App) apiPut(w http.ResponseWriter, r *http.Request, c *remoteAPICaller) {
	q := r.URL.Query()
	id, err := apiID(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	baseRev, err := apiBaseRevQuery(q)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	data, ok := readAPIFile(w, r)
	if !ok {
		return
	}
	store := a.artifactStore()
	res, err := store.Put(r.Context(), id, &artifact.NewRevision{Data: data, Note: q.Get("note"), By: c.actor(), BaseRev: baseRev})
	if err != nil {
		writeAPIError(w, artifactError(err))
		return
	}
	updated, err := store.Show(id)
	if err != nil {
		writeAPIError(w, artifactError(err))
		return
	}
	writeAPIJSON(w, http.StatusOK, machinePutJSON{
		Artifact: fullJSON(&updated, a.apiRemoved()),
		Anchors:  anchorCountsJSON{Anchored: res.Anchored, Detached: res.Detached},
		Warnings: apiWarnings(updated.Warnings),
	})
}

// apiRemove answers DELETE /v1/artifacts/{id}: only the tree that created the
// artifact may remove it.
func (a *App) apiRemove(w http.ResponseWriter, r *http.Request, c *remoteAPICaller) {
	entry, err := a.apiEntry(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if entry.Record.Root == nil || *entry.Record.Root != c.storedRoot() {
		writeAPIError(w, newError(codeArtifactForbidden, fmt.Errorf("%s was created by another workspace tree; ask a person to remove it", entry.Record.ID)))
		return
	}
	if err = a.artifactStore().Remove(r.Context(), entry.Record.ID); err != nil {
		writeAPIError(w, artifactError(err))
		return
	}
	writeAPIJSON(w, http.StatusOK, envproto.ArtifactRemoved{ID: entry.Record.ID, Removed: true})
}

// apiThreads answers GET /v1/artifacts/{id}/threads: the comment list's JSON.
func (a *App) apiThreads(w http.ResponseWriter, r *http.Request, _ *remoteAPICaller) {
	openOnly, err := apiFlag(r.URL.Query(), "open")
	if err != nil {
		writeAPIError(w, err)
		return
	}
	entry, err := a.apiEntry(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	threads, err := a.artifactStore().Threads(entry.Record.ID)
	if err != nil {
		writeAPIError(w, artifactError(err))
		return
	}
	writeAPIJSON(w, http.StatusOK, commentListJSON{
		ArtifactID: entry.Record.ID,
		Latest:     entry.Record.Latest().Rev,
		Threads:    threadsJSON(threads, openOnly),
		Warnings:   apiWarnings(nil),
	})
}

// apiAddThread answers POST /v1/artifacts/{id}/threads: a new thread, or a
// reply to one.
func (a *App) apiAddThread(w http.ResponseWriter, r *http.Request, c *remoteAPICaller) {
	id, err := apiID(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	var body envproto.CommentAddBody
	if err = decodeAPIJSON(w, r, &body); err != nil {
		writeAPIError(w, err)
		return
	}
	if err = checkAPIComment(&body); err != nil {
		writeAPIError(w, err)
		return
	}
	store := a.artifactStore()
	var thread artifact.Thread
	var comment artifact.Comment
	if body.Reply != "" {
		thread, comment, err = store.Reply(r.Context(), id, body.Reply, body.Body, c.actor())
	} else {
		thread, comment, err = store.AddThread(r.Context(), id, &artifact.NewThread{Body: body.Body, By: c.actor(), Anchor: apiAnchorRequest(&body)})
	}
	if err != nil {
		writeAPIError(w, artifactError(err))
		return
	}
	writeAPIJSON(w, http.StatusCreated, commentAddedJSON{
		ArtifactID: id,
		Thread:     threadToJSON(&thread),
		Comment:    commentToJSON(&comment),
		Warnings:   apiWarnings(nil),
	})
}

// apiAnchorRequest is the anchor a body names, or nil for a thread about the
// whole artifact.
func apiAnchorRequest(body *envproto.CommentAddBody) *artifact.AnchorRequest {
	if body.Quote == "" {
		return nil
	}
	return &artifact.AnchorRequest{Quote: body.Quote, Line: body.Line}
}

// apiResolveThread answers POST /v1/artifacts/{id}/threads/{thread}/resolve:
// the thread, resolved, or open again with reopen.
func (a *App) apiResolveThread(w http.ResponseWriter, r *http.Request, c *remoteAPICaller) {
	id, err := apiID(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	var body envproto.CommentResolveBody
	if err = decodeAPIJSON(w, r, &body); err != nil {
		writeAPIError(w, err)
		return
	}
	thread, err := a.artifactStore().Resolve(r.Context(), id, r.PathValue("thread"), c.actor(), body.Reopen)
	if err != nil {
		writeAPIError(w, artifactError(err))
		return
	}
	writeAPIJSON(w, http.StatusOK, commentResolvedJSON{ArtifactID: id, Thread: threadToJSON(&thread), Warnings: apiWarnings(nil)})
}
