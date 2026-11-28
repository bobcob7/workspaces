package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// apiTestCaller is the environment the API tests' requests come from.
var apiTestCaller = environment.APICaller{EnvironmentID: "env-1a2b3c4d", EnvironmentName: "linux-server"}

// apiReq is a request to the artifact API as workspace plan (named planner)
// in the tree boss, from apiTestCaller.
func apiReq(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	ctx := environment.WithAPICaller(t.Context(), apiTestCaller)
	req := httptest.NewRequestWithContext(ctx, method, target, body)
	req.Header.Set(envproto.HeaderWorkspace, "plan")
	req.Header.Set(envproto.HeaderWorkspaceName, "planner")
	req.Header.Set(envproto.HeaderRoot, "boss")
	return req
}

// serveAPI answers req with h.
func serveAPI(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// apiErrorOf is the code and details of an error answer.
func apiErrorOf(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return env.Error.Code, env.Error.Details
}

// apiAddFile adds data as name through the API and returns the new id.
func apiAddFile(t *testing.T, h http.Handler, name string, data []byte) string {
	t.Helper()
	rec := serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts?name="+name, bytes.NewReader(data)))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return jsonString(t, jsonObject(t, jsonObjectOf(t, rec.Body.String())["artifact"])["id"])
}

// apiThreadsOf answers the artifact's thread list for the test caller.
func apiThreadsOf(t *testing.T, h http.Handler, id string) []any {
	t.Helper()
	rec := serveAPI(h, apiReq(t, http.MethodGet, "/v1/artifacts/"+id+"/threads", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Threads []any `json:"threads"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Threads
}

func TestApp_artifactAPI(t *testing.T) {
	t.Run("add then show answers the local JSON shape, with the remote actor", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		id := apiAddFile(t, h, "notes.txt", []byte("hello"))
		entry, err := e.app.artifactStore().Show(id)
		require.NoError(t, err)
		require.NotNil(t, entry.Record.Root)
		assert.Equal(t, "env-1a2b3c4d/boss", *entry.Record.Root)
		createdBy := entry.Record.CreatedBy
		assert.Equal(t, "agent", createdBy.Kind)
		assert.Equal(t, "plan", *createdBy.Workspace)
		assert.Equal(t, "planner", *createdBy.Name)
		assert.Equal(t, "linux-server", *createdBy.Environment)
		rec := serveAPI(h, apiReq(t, http.MethodGet, "/v1/artifacts/"+id, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, exitOK, runMachine(t, e, "artifact", "show", id, "--json"), e.stderr.String())
		local := jsonObject(t, jsonObjectOf(t, e.stdout.String())["artifact"])
		remote := jsonObject(t, jsonObjectOf(t, rec.Body.String())["artifact"])
		assert.ElementsMatch(t, mapKeys(local), mapKeys(remote))
	})
	t.Run("a body of exactly the image limit is stored, one byte more is 413", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, artifact.MaxImageSize-8)...)
		rec := serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts?name=big.png", bytes.NewReader(png)))
		assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		over := append(bytes.Clone(png), 0)
		rec = serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts?name=over.png", bytes.NewReader(over)))
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		code, _ := apiErrorOf(t, rec)
		assert.Equal(t, string(codeArtifactInvalid), code)
		list, _, err := e.app.artifactStore().List()
		require.NoError(t, err)
		assert.Len(t, list, 1)
	})
	t.Run("a Content-Length over the image limit is 413 before the body is read", func(t *testing.T) {
		e := newArtifactApp(t)
		req := apiReq(t, http.MethodPost, "/v1/artifacts?name=a.png", strings.NewReader("x"))
		req.ContentLength = artifact.MaxImageSize + 1
		rec := serveAPI(e.app.artifactAPI(), req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		code, _ := apiErrorOf(t, rec)
		assert.Equal(t, string(codeArtifactInvalid), code)
	})
	t.Run("the store's limits and an empty body are 400", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		text := bytes.Repeat([]byte("a"), artifact.MaxTextSize+1)
		rec := serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts?name=a.txt", bytes.NewReader(text)))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		code, _ := apiErrorOf(t, rec)
		assert.Equal(t, string(codeArtifactInvalid), code)
		rec = serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts?name=a.txt", nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("content answers the bytes, the revision headers, and records a read", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		data := []byte("the pitch")
		person, err := e.app.artifactStore().Add(t.Context(), &artifact.NewArtifact{Name: "pitch.txt", Data: data, By: artifact.Person()})
		require.NoError(t, err)
		id := person.ID
		rec := serveAPI(h, apiReq(t, http.MethodGet, "/v1/artifacts/"+id+"/content", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, data, rec.Body.Bytes())
		assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
		assert.Equal(t, "1", rec.Header().Get(envproto.HeaderRev))
		sum := sha256.Sum256(data)
		assert.Equal(t, hex.EncodeToString(sum[:]), rec.Header().Get(envproto.HeaderSHA256))
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		entry, err := e.app.artifactStore().Show(id)
		require.NoError(t, err)
		var read bool
		for _, c := range entry.Conversations {
			if c.Workspace == "plan" && c.Environment != nil && *c.Environment == "linux-server" && slices.Contains(c.Actions, artifact.ActionRead) {
				read = true
			}
		}
		assert.True(t, read, "a read link with the environment")
	})
	t.Run("content of a revision that doesn't exist is 404, and a bad rev is 400", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		id := apiAddFile(t, h, "a.txt", []byte("a"))
		rec := serveAPI(h, apiReq(t, http.MethodGet, "/v1/artifacts/"+id+"/content?rev=2", nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)
		code, _ := apiErrorOf(t, rec)
		assert.Equal(t, string(codeArtifactUnknown), code)
		for _, rev := range []string{"0", "x"} {
			rec = serveAPI(h, apiReq(t, http.MethodGet, "/v1/artifacts/"+id+"/content?rev="+rev, nil))
			assert.Equal(t, http.StatusBadRequest, rec.Code, "rev=%s", rev)
		}
	})
	t.Run("put on a stale base_rev is 409 with the latest", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		id := apiAddFile(t, h, "a.txt", []byte("one"))
		rec := serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts/"+id+"/revisions?base_rev=5", strings.NewReader("two")))
		assert.Equal(t, http.StatusConflict, rec.Code)
		code, details := apiErrorOf(t, rec)
		assert.Equal(t, string(codeArtifactConflict), code)
		require.Contains(t, details, "latest")
		assert.InDelta(t, 1, details["latest"], 0)
		rec = serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts/"+id+"/revisions?base_rev=1", strings.NewReader("two")))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Len(t, jsonObject(t, jsonObjectOf(t, rec.Body.String())["artifact"])["revisions"], 2)
	})
	t.Run("rm is for the caller's tree only", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		person, err := e.app.artifactStore().Add(t.Context(), &artifact.NewArtifact{Name: "p.txt", Data: []byte("p"), By: artifact.Person()})
		require.NoError(t, err)
		rec := serveAPI(h, apiReq(t, http.MethodDelete, "/v1/artifacts/"+person.ID, nil))
		assert.Equal(t, http.StatusForbidden, rec.Code)
		other, err := e.app.artifactStore().Add(t.Context(), &artifact.NewArtifact{Name: "o.txt", Data: []byte("o"), By: artifact.RemoteAgent("plan", "planner", "win-rig"), Root: "env-9f9f9f9f/boss"})
		require.NoError(t, err)
		rec = serveAPI(h, apiReq(t, http.MethodDelete, "/v1/artifacts/"+other.ID, nil))
		assert.Equal(t, http.StatusForbidden, rec.Code)
		own := apiAddFile(t, h, "own.txt", []byte("own"))
		rec = serveAPI(h, apiReq(t, http.MethodDelete, "/v1/artifacts/"+own, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"id":"`+own+`","removed":true}`, rec.Body.String())
	})
	t.Run("list keeps the caller's tree with mine=1", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		mine := apiAddFile(t, h, "mine.txt", []byte("m"))
		_, err := e.app.artifactStore().Add(t.Context(), &artifact.NewArtifact{Name: "theirs.txt", Data: []byte("t"), By: artifact.Person()})
		require.NoError(t, err)
		rec := serveAPI(h, apiReq(t, http.MethodGet, "/v1/artifacts?mine=1", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out struct {
			Artifacts []struct {
				ID string `json:"id"`
			} `json:"artifacts"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		require.Len(t, out.Artifacts, 1)
		assert.Equal(t, mine, out.Artifacts[0].ID)
	})
	t.Run("comments: start a thread, reply, list open ones, resolve", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		id := apiAddFile(t, h, "doc.md", []byte("alpha beta gamma"))
		rec := serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts/"+id+"/threads", strings.NewReader(`{"body":"check this","quote":"beta"}`)))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		thread := jsonString(t, jsonObject(t, jsonObjectOf(t, rec.Body.String())["thread"])["id"])
		rec = serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts/"+id+"/threads", strings.NewReader(`{"body":"agreed","reply":"`+thread+`"}`)))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Len(t, apiThreadsOf(t, h, id), 1)
		rec = serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts/"+id+"/threads/"+thread+"/resolve", strings.NewReader(`{"reopen":false}`)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Len(t, apiThreadsOf(t, h, id), 1, "all threads, resolved or not")
		rec = serveAPI(h, apiReq(t, http.MethodGet, "/v1/artifacts/"+id+"/threads?open=1", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Empty(t, jsonObjectOf(t, rec.Body.String())["threads"])
	})
	t.Run("a quote that isn't in the text is 404 anchor_not_found with its details", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		id := apiAddFile(t, h, "doc.md", []byte("alpha"))
		rec := serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts/"+id+"/threads", strings.NewReader(`{"body":"x","quote":"zeta"}`)))
		assert.Equal(t, http.StatusNotFound, rec.Code)
		code, details := apiErrorOf(t, rec)
		assert.Equal(t, string(codeAnchorNotFound), code)
		assert.Contains(t, details, "occurrences")
	})
	t.Run("a comment body with an unknown field, two objects or 9 MiB is 400", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		id := apiAddFile(t, h, "doc.md", []byte("alpha"))
		bodies := []string{
			`{"body":"x","bogus":1}`,
			`{"body":"x"}{"body":"y"}`,
			`{"body":"` + strings.Repeat("a", 9<<20) + `"}`,
		}
		for _, body := range bodies {
			rec := serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts/"+id+"/threads", strings.NewReader(body)))
			assert.Equal(t, http.StatusBadRequest, rec.Code, "body of %d bytes", len(body))
		}
	})
	t.Run("contradictory comment fields are 400", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		id := apiAddFile(t, h, "doc.md", []byte("alpha"))
		bodies := []string{
			`{"body":"x","reply":"t1","quote":"alpha"}`,
			`{"body":"x","line":2}`,
			`{"body":"x","quote":"alpha","line":-1}`,
		}
		for _, body := range bodies {
			rec := serveAPI(h, apiReq(t, http.MethodPost, "/v1/artifacts/"+id+"/threads", strings.NewReader(body)))
			assert.Equal(t, http.StatusBadRequest, rec.Code, body)
			code, _ := apiErrorOf(t, rec)
			assert.Equal(t, string(codeUsage), code, body)
		}
	})
	t.Run("hostile workspace headers are usage, and a missing caller is internal", func(t *testing.T) {
		e := newArtifactApp(t)
		h := e.app.artifactAPI()
		for _, value := range []string{"", "a/b", "..x", "a\x01b"} {
			req := apiReq(t, http.MethodGet, "/v1/artifacts", nil)
			req.Header.Set(envproto.HeaderWorkspace, value)
			rec := serveAPI(h, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "Ws-Workspace %q", value)
			code, _ := apiErrorOf(t, rec)
			assert.Equal(t, string(codeUsage), code)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/artifacts", nil)
		req.Header.Set(envproto.HeaderWorkspace, "plan")
		req.Header.Set(envproto.HeaderRoot, "boss")
		rec := serveAPI(h, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		code, _ := apiErrorOf(t, rec)
		assert.Equal(t, string(codeInternal), code)
	})
	t.Run("a malformed id is artifact_unknown", func(t *testing.T) {
		e := newArtifactApp(t)
		rec := serveAPI(e.app.artifactAPI(), apiReq(t, http.MethodGet, "/v1/artifacts/art-ZZ", nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)
		code, _ := apiErrorOf(t, rec)
		assert.Equal(t, string(codeArtifactUnknown), code)
	})
}

func TestApiStatus(t *testing.T) {
	cases := map[errorCode]int{
		codeUsage:             http.StatusBadRequest,
		codeArtifactInvalid:   http.StatusBadRequest,
		codeArtifactUnknown:   http.StatusNotFound,
		codeAnchorNotFound:    http.StatusNotFound,
		codeArtifactConflict:  http.StatusConflict,
		codeArtifactFull:      http.StatusConflict,
		codeArtifactForbidden: http.StatusForbidden,
		codeInternal:          http.StatusInternalServerError,
		codeClaudeFailed:      http.StatusInternalServerError,
	}
	for code, want := range cases {
		assert.Equal(t, want, apiStatus(code), string(code))
	}
}

func TestWriteAPIError(t *testing.T) {
	t.Run("a full store answers 409 artifact_full", func(t *testing.T) {
		e := newArtifactApp(t)
		rec := httptest.NewRecorder()
		e.app.writeAPIError(rec, artifactError(fmt.Errorf("%w: x", artifact.ErrFull)))
		assert.Equal(t, http.StatusConflict, rec.Code)
		code, _ := apiErrorOf(t, rec)
		assert.Equal(t, string(codeArtifactFull), code)
	})
	t.Run("an error that isn't a ws error is 500 internal, not sent", func(t *testing.T) {
		e := newArtifactApp(t)
		rec := httptest.NewRecorder()
		e.app.writeAPIError(rec, errors.New("disk on fire at /secret/path"))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		code, _ := apiErrorOf(t, rec)
		assert.Equal(t, string(codeInternal), code)
		assert.NotContains(t, rec.Body.String(), "secret")
	})
}
