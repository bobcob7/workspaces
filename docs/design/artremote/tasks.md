# artremote: tasks

Artifacts for agents on remote environments, as
[ADR 0087](../../adrs/0087-artifacts-from-remote-environments.md) decides:
HTTPS with mutual TLS on a third controller listener (`api_listen`, 8885),
the enrolment certificate as the credential, the address delivered by an
`api.update` request over MQTT, protocol 5. Five tasks.

**Every task:** read [constraints.md](constraints.md) first (pod limits,
lint rules, pinned values, no blank lines in function bodies), then ADR
0087. Run heavy checks one at a time with `-p 2`. Commit and push after
each file. Tests first where a task says so: commit the failing test
(`test: ...`), run it, record that it fails, then the code. **No
`Co-Authored-By` trailer or any AI attribution in commits.** Never start a
real Claude Code session in a test. Bind only `127.0.0.1:0`. List anything
you couldn't run under `## Not run here` in your report. Before reporting,
mutate each new test's target on a scratch copy (flip a check, drop a
line) and confirm a test fails; report any survivor.

**Both sides need the new `ws`.** Each task says whether it is
**controller side** (runs in the `ws` daemon and `ws` on the machine the
desktop uses) or **agent side** (runs in `ws` on the enrolled remote).

## Order

```
T1  controller: protocol 5, the API listener, identity, api.update, api_listen
 ├── T2  controller: the /v1/artifacts handlers over the store   ┐ parallel (disjoint files:
 └── T3  agent: api.toml, the api.update handler, the HTTPS client ┘ internal/cli+artifact vs internal/envagent)
        ↓ (both)
       T4  agent: ws machine artifact's remote path, @artifact: hints, the end-to-end test
        ↓
       T5  small  docs (may start beside T4: disjoint files)
```

The common verify block, `VERIFY(pkgs)`, is:

```bash
go build ./... && go vet ./... \
&& go test -p 2 -count=1 <pkgs> \
&& go tool golangci-lint run ./... \
&& go test -count=1 -run '^TestRepository$' ./internal/blanklines/
```

---

## T1: protocol 5, the API listener, identity and `api.update` (controller side)

- **Depends on:** none. **Not parallel** (everything else needs it).
- **Side:** controller. The `envproto` constants are shared by both sides
  but only declared here.
- **Files:**
  - `internal/envproto/payloads.go`, `internal/envproto/ops.go`,
    `internal/envproto/api.go` (new), `internal/envproto/api_test.go` (new),
    `internal/envproto/scope_test.go`, `internal/envproto/ops_test.go`
  - `internal/environment/api.go` (new), `internal/environment/api_test.go`
    (new), `internal/environment/controller.go`,
    `internal/environment/controller_test.go`
  - `internal/config/config.go`, `internal/config/edit.go`,
    `internal/config/config_test.go`, `internal/config/edit_test.go`
  - `internal/daemon/environments.go` (struct fields only)

### envproto

`payloads.go`:

```go
// Protocol is the newest operation protocol this ws speaks (ADR 0073). Reviews and loops make it 3; scopes and machine.record 4; the artifact API 5.
const Protocol = 5

// ProtocolArtifacts is the protocol api.update and the artifact API need (ADR 0087).
const ProtocolArtifacts = 5

const OpAPIUpdate = "api.update" // beside OpCertRenew, OpCAUpdate

// Hello gains, after Conn:
	// APIEndpoint is the controller's artifact API address the agent holds
	// (ADR 0087); "" when it has none, and from an agent older than protocol 5.
	APIEndpoint string `json:"api_endpoint,omitempty"`

// APIUpdateArgs are the arguments of api.update.
type APIUpdateArgs struct {
	Endpoint string `json:"endpoint"` // host:port, CheckEndpoint
}

// APIUpdateResult is the result of api.update.
type APIUpdateResult struct {
	Endpoint string `json:"endpoint"`
}
```

`ops.go` `LookupOp`: `case OpAPIUpdate: return OpSpec{Name: name, Caller: CallerInternal, Since: ProtocolArtifacts, Timeout: 30 * time.Second}, true`.

`api.go` (new): the HTTP wire both sides share.

```go
// The artifact API's routes and headers (ADR 0087).
const (
	ArtifactsPath       = "/v1/artifacts"
	HeaderWorkspace     = "Ws-Workspace"      // the calling workspace's id, url.QueryEscape'd
	HeaderWorkspaceName = "Ws-Workspace-Name" // its name, url.QueryEscape'd
	HeaderRoot          = "Ws-Root"           // its tree's root on the remote, url.QueryEscape'd
	HeaderRev           = "Ws-Rev"            // content: the revision served, decimal
	HeaderSHA256        = "Ws-Sha256"         // content: its SHA-256, 64 lowercase hex
	HeaderContentType   = "Ws-Content-Type"   // content: the stored content type
	MaxEndpointBytes    = 255
	// MaxAPIJSONBytes caps a JSON request body (comments) and a JSON answer the client reads.
	MaxAPIJSONBytes = 8 << 20
)

// Reasons in artifact_unreachable's details.reason (ADR 0087).
const (
	ReasonIdentity    = "identity"    // this machine's key, certificate or agent.toml can't be read
	ReasonNoEndpoint  = "no_endpoint" // no api.toml: the controller never sent api.update
	ReasonOffline     = "offline"     // the dial failed or timed out
	ReasonCertificate = "certificate" // the TLS handshake refused a certificate
	ReasonRefused     = "refused"     // 403 from the controller's identity check
)

// CheckEndpoint is nil for "host:port" with a non-empty host, a decimal
// port from 1 to 65535 and at most MaxEndpointBytes bytes; otherwise an
// error naming what is wrong.
func CheckEndpoint(s string) error

// CommentAddBody is POST /v1/artifacts/{id}/threads' JSON body. Reply and
// Quote aren't both set; Line is 0 or at least 1 and needs Quote.
type CommentAddBody struct {
	Body  string `json:"body"`
	Reply string `json:"reply"`
	Quote string `json:"quote"`
	Line  int    `json:"line"`
}

// CommentResolveBody is POST /v1/artifacts/{id}/threads/{thread}/resolve's body.
type CommentResolveBody struct {
	Reopen bool `json:"reopen"`
}

// ArtifactRemoved is DELETE /v1/artifacts/{id}'s answer.
type ArtifactRemoved struct {
	ID      string `json:"id"`
	Removed bool   `json:"removed"`
}
```

Tests: `api_test.go` `TestCheckEndpoint` (good `ws.example.com:8885`,
`127.0.0.1:1`, `[::1]:65535`; bad: empty, no port, port 0, 65536, `+80`,
empty host `:8885`, 256 bytes). `ops_test.go`: add the `OpAPIUpdate` row to
`TestLookupOp`. `scope_test.go` `TestProtocol_scopes`: `assert.Equal(t, 5,
Protocol)` and `assert.Equal(t, 4, ProtocolScopes)`; add
`TestProtocol_artifacts` (`5, ProtocolArtifacts`). A payloads test: a hello
without `api_endpoint` decodes to `""`, and `""` is omitted when encoding.

### environment: `api.go` (new)

```go
// APICaller is the environment an artifact API request comes from: the
// record its client certificate's common name names (ADR 0087).
type APICaller struct {
	EnvironmentID   string
	EnvironmentName string
}

// APICallerFrom is the caller the identity check stored in ctx.
func APICallerFrom(ctx context.Context) (APICaller, bool)

// WithAPICaller returns ctx carrying caller. The listener uses it; handler tests may too.
func WithAPICaller(ctx context.Context, caller APICaller) context.Context

// SetArtifactAPI sets the handler for /v1/artifacts and below (internal/cli's).
// Call it before Start. Without one, every route answers 404.
func (c *Controller) SetArtifactAPI(h http.Handler)

// APIEndpoint is host:port agents reach the API on: public_name with the
// API listener's bound port; "" while not listening.
func (c *Controller) APIEndpoint() string

// apiHandler is what the API listener serves: the identity check, the
// in-flight cap, then the artifact handler.
func (c *Controller) apiHandler() http.Handler

// identify is r's caller, from its verified client certificate. It runs
// policy.connect on envproto.PeerFromState(r.TLS) (ClientID = CN), then
// reads the record by id for its name. Any failure is errAPIRefused.
func (c *Controller) identify(r *http.Request) (APICaller, error)
```

Constants: `apiReadHeaderTimeout = 10 * time.Second`, `apiReadTimeout =
2 * time.Minute`, `apiWriteTimeout = 2 * time.Minute`, `apiIdleTimeout =
60 * time.Second`, `apiMaxHeaderBytes = 16 << 10`, `apiMaxInFlight = 4`.

`apiHandler` answers, in order:

1. a path that isn't `envproto.ArtifactsPath` or under `ArtifactsPath + "/"`:
   404 `{"error":{"code":"usage","message":"no such path"}}` (use the
   existing `writeError`);
2. `identify` fails (no peer certificate, policy refuses, record missing):
   403 `{"error":{"code":"artifact_unreachable","message":"the controller refused this machine's certificate: it is revoked or its environment was removed; enroll it again","details":{"reason":"refused"}}}`, and log `"artifact api refused"` with the remote address and the CN, never the certificate;
3. a `POST` while `apiMaxInFlight` `POST`s run (a buffered-channel
   semaphore, non-blocking acquire): 503 `{"error":{"code":"operation_limited","message":"the controller is busy with other uploads; try again","details":null}}`;
4. no handler set: 404 as in 1;
5. otherwise the handler, with `r.WithContext(WithAPICaller(r.Context(), caller))`.

`writeError` takes no details today: add `writeErrorDetails(w, status,
code, message string, details map[string]any)` beside it and use it for 2.

### environment: `controller.go`

- `Settings` gains `APIListen string // "0.0.0.0:8885"`.
- **`APIListen == ""` starts no API listener** (`Status().APIListen` and
  `APIEndpoint()` are `""`, and no `api.update` is sent). The daemon always
  sets it (T2, from `APIAddr()`), and the existing tests' `Settings`
  (`testSettings`, `startEnrollFixture`, `startEnvController`) leave it
  empty, so they never bind a port they don't expect.
- `listenAll` binds `APIListen` after the enrolment listener (error
  `"listening for the artifact API: %w"`, closing what is open, like the
  other failures), computes the endpoint with `brokerEndpoint(PublicName,
  apiLn.Addr().String())` (it only joins public_name with a port), and
  serves `tls.NewListener(apiLn, brokerTLS(c.currentState))` with an
  `http.Server{Handler: c.apiHandler(), ReadHeaderTimeout, ReadTimeout,
  WriteTimeout, IdleTimeout, MaxHeaderBytes}` from the constants, in a
  goroutine like `serveEnrollment` (log `"artifact api listener stopped"`).
  `listeners` and `publishListeners` record `apiAddr`, `apiServer` and
  `apiEndpoint`; `Close` closes the API server too (`errors.Join`) and
  clears them.
- `Status` gains `APIListen string` (the bound address, `""` when not
  listening).
- **`api.update`.** `onStatus`, after the `ca.update` check: when
  `hello.Protocol >= envproto.ProtocolArtifacts && hello.APIEndpoint !=
  c.APIEndpoint()` and the endpoint isn't `""`, call
  `c.requestAPIUpdate(ctx, id)`. Generalise the pending updates: `updates
  map[string]pendingUpdate` with `type pendingUpdate struct{ env, op string }`;
  `addUpdate(id, reqID, op)`, `takeUpdate(id, reqID) (op string, ok bool)`;
  `sendCAUpdate` and a new `sendAPIUpdate` share `sendUpdate(ctx, id, op
  string, args any) error` (the body of today's `sendCAUpdate` with the op
  and args as parameters). `onRes` logs `"<op> acknowledged"` or
  `"<op> refused"` with the code. Expiry stays `caUpdateExpiry` (30 s).

### config

`[environments] api_listen`, exactly as `enroll_listen` is done (grep
`EnrollListen`, `enroll_listen`, `defaultEnvEnrollListen`, `EnrollAddr` in
`internal/config/` and copy each place):

- `Environments.APIListen string \`toml:"api_listen" json:"api_listen"\``;
  `defaultEnvAPIListen = "0.0.0.0:8885"`;
  `func (e *Environments) APIAddr() string`; `check()` validates it with
  `checkListenAddr("environments.api_listen", …)`; the list at
  `config.go:258` gains it.
- `edit.go`: append `{key: "environments.api_listen", typ: TypeString,
  table: "environments", leaf: "api_listen"}` **last** in
  `environmentRules()` (after `default_scope`), and its cases in the
  value, default and set switches.
- Tests: `TestEnvironmentKeys` length 11 → 12 and `settings[11]` is
  `environments.api_listen` with default `"0.0.0.0:8885"`; a parse test
  with `api_listen = "0.0.0.0:9003"`; the invalid-address table gains
  `api_listen` without a port and with port 70000.

### daemon

`internal/daemon/environments.go`: `EnvironmentsStatus.APIListen string
\`json:"api_listen"\`` (after `EnrollListen`), and `EnvironmentHello.Protocol
int \`json:"protocol"\``. Nothing fills them yet (T2 does), so no test
changes here.

### Tests (environment)

`api_test.go`, against a **real `httptest` mTLS server**:
`srv := httptest.NewUnstartedServer(ctrl.apiHandler()); srv.TLS =
brokerTLS(ctrl.currentState); srv.StartTLS()`, with a controller started
by `newController(t, store, b, log, settings)` where `settings :=
testSettings(); settings.APIListen = "127.0.0.1:0"` (so its TLS state is
loaded; leave `testSettings` itself unchanged) and a stub artifact handler that answers
200 with the caller's id and name. Clients use `enrollAgent`'s certificate
and `agentTLS`. `TestController_apiHandler`:

- an enrolled agent's certificate: 200, and the stub saw its id and name;
- **wrong CA**: a certificate issued by a second store's CA (a second
  `NewStore` in another temp dir, enrolled there): the request fails with a
  `*net.OpError` whose `Op` is `"remote error"`; the stub never ran;
- **expired**: enroll through a store whose `now` is 100 days ago (the CA
  and server certificate stay valid, the 90-day client certificate has
  expired): the request fails the handshake as above;
- no client certificate: fails the handshake;
- **revoked**: after `store.Rotate(name, …)` (which revokes the serial and
  makes the record pending): 403, `code` `artifact_unreachable`,
  `details.reason` `refused`; after `store.Remove`: the same;
- a path outside `/v1/artifacts` (`/v1/enroll`, `/`): 404;
- with no handler set: 404;
- the in-flight cap: a stub that blocks `POST`s on a channel; the 5th
  concurrent `POST` gets 503 `operation_limited`; a `GET` still passes.

`controller_test.go`:

- `TestController_Start`: with `APIListen: "127.0.0.1:0"`, `Status().APIListen`
  is a bound address and `APIEndpoint()` is `127.0.0.1:<that port>`; after
  `Close` a dial to it fails and `APIEndpoint()` is `""`.
- `TestController_onStatus`: a protocol-5 hello with `api_endpoint` `""`
  publishes one `api.update` on `env/<id>/req` with `{"endpoint":
  APIEndpoint()}` and a response topic under `res/`; a protocol-5 hello with
  the same endpoint publishes none; a protocol-4 hello publishes none.
- `TestController_onRes`: an OK reply to a pending `api.update` is taken
  (logged as acknowledged); a later duplicate is "for no pending call".

### Acceptance

- Protocol 5; `api.update` in the table; the listener serves only
  `/v1/artifacts`, refuses as above, closes with the controller.
- Every test above passes; no other package's test changes, except the
  pinned ones named here.

### Verify

```bash
VERIFY(./internal/envproto/ ./internal/environment/ ./internal/config/ ./internal/daemon/ ./internal/envagent/ ./internal/cli/)
```

`internal/envagent` and `internal/cli` are in the list because the
protocol bump reaches their tests (they compare against `envproto.Protocol`
symbolically and must still pass).

---

## T2: the `/v1/artifacts` handlers over the store (controller side)

- **Depends on:** T1. **Parallel with:** T3 (disjoint files).
- **Side:** controller.
- **Files:** `internal/cli/artifact_api.go` (new),
  `internal/cli/artifact_api_test.go` (new), `internal/cli/artifact.go`
  (one extraction, below), `internal/cli/env_daemon.go`,
  `internal/cli/env_daemon_test.go`, `internal/cli/daemon.go`,
  `internal/cli/daemon_test.go`, `internal/artifact/artifact.go`,
  `internal/artifact/links.go`, `internal/artifact/links_test.go`.

### artifact

- `Actor` gains `Environment *string \`json:"environment,omitempty"\``
  (the environment's name; nil for local actors, so their JSON is
  unchanged). Add `func RemoteAgent(workspace, name, environment string)
  Actor`.
- `links.go` `appendActor` passes `a.Environment` as the event's
  `environment`, so a remote creator's conversation row has its
  environment (and `removed: null`, not `true`). Test it in
  `links_test.go` `TestConversations`.

### cli: `artifact_api.go` (new)

```go
// artifactAPI is the controller's /v1/artifacts handler (ADR 0087): the
// ws machine artifact commands for an agent on a remote environment,
// answering the JSON those commands print.
func (a *App) artifactAPI() http.Handler

// remoteAPICaller is who a request acts for: the environment the
// controller identified, and the workspace its headers name.
type remoteAPICaller struct {
	env       environment.APICaller
	workspace string
	name      string
	root      string // the remote's root, as sent
}

// apiCaller reads and checks the request's caller. Each header is
// url.QueryUnescape'd and must be artifact.ValidWorkspaceID; a missing
// Ws-Workspace-Name is the workspace id. A missing caller in the context
// is codeInternal.
func apiCaller(r *http.Request) (remoteAPICaller, error)

// actor is the caller as an artifact actor, with its environment's name.
func (c *remoteAPICaller) actor() artifact.Actor // artifact.RemoteAgent(c.workspace, c.name, c.env.EnvironmentName)

// storedRoot is the root the store records: "<environment id>/<root>".
func (c *remoteAPICaller) storedRoot() string

// writeAPIError answers err: a *cliError keeps its code, message and
// details; anything else is 500 "internal" with the message "internal error"
// (logged, not sent).
func writeAPIError(w http.ResponseWriter, err error)

// apiStatus is the HTTP status for a code: usage 400, artifact_invalid 400
// (413 when the body was too large), artifact_unknown 404, anchor_not_found
// 404, artifact_conflict 409, artifact_full 409, artifact_forbidden 403,
// anything else 500.
func apiStatus(code errorCode) int
```

Routes (`http.NewServeMux` patterns; every answer `Content-Type:
application/json` except content; every JSON body is exactly the struct the
local command prints, `Warnings` = the store's warnings or `[]string{}`,
never null):

| Pattern | Does | Answers |
| --- | --- | --- |
| `GET /v1/artifacts` | query `kind` (`checkArtifactKind`), `mine` (`"1"` or absent), `limit` (decimal, default `defaultListLimit`, `checkListLimit`); filter `artifactFilter{kind, mine, root: storedRoot(), limit}` | `artifactListJSON` |
| `POST /v1/artifacts` | query `name` (required, else usage), `title`, `note`; body ≤ `artifact.MaxImageSize` (below); `store.Add(…, By: actor(), Root: storedRoot())` | 201 `artifactResultJSON{fullJSON(&artifact.Entry{Record: rec}, a.liveWorkspaces())}` |
| `GET /v1/artifacts/{id}` | `artifactRef(id, 0, false)`, `store.Show` | `artifactResultJSON` |
| `GET /v1/artifacts/{id}/content` | query `rev` (absent = latest; else decimal ≥ 1); `store.Read(id, rev)`; then `store.RecordLinks(ctx, id, NewLink{Workspace, Name, Root: c.root, Environment: c.env.EnvironmentName, Action: artifact.ActionRead, Rev})`, a failure only logged | 200, the bytes, `Content-Type: application/octet-stream`, `Content-Length`, `Ws-Rev`, `Ws-Sha256` (the revision's `SHA256`), `Ws-Content-Type`, `X-Content-Type-Options: nosniff` |
| `POST /v1/artifacts/{id}/revisions` | query `base_rev` (absent or ≥ 1), `note`; body as for add; `store.Put(…, NewRevision{Data, Note, By: actor(), BaseRev})`, then `Show` | `machinePutJSON` |
| `DELETE /v1/artifacts/{id}` | `Show`, the owner check (`Record.Root` equals `storedRoot()`, else `artifact_forbidden` with the local message), `store.Remove` | `envproto.ArtifactRemoved{ID, true}` |
| `GET /v1/artifacts/{id}/threads` | query `open` (`"1"` or absent) | `commentListJSON` (as `listComments` builds it) |
| `POST /v1/artifacts/{id}/threads` | JSON `envproto.CommentAddBody` (`DisallowUnknownFields`, ≤ `envproto.MaxAPIJSONBytes`, exactly one object); reply and quote both set, or line without quote, or line < 0: usage; `store.Reply` or `store.AddThread` with `AnchorRequest{Quote, Line}` | 201 `commentAddedJSON` |
| `POST /v1/artifacts/{id}/threads/{thread}/resolve` | JSON `envproto.CommentResolveBody`, same decoding; `store.Resolve(…, actor(), reopen)` | `commentResolvedJSON` |

**Bodies (add, put).** A `Content-Length` over `artifact.MaxImageSize`:
413 `artifact_invalid` `"the file is over 10485760 bytes"` without reading.
Otherwise `io.ReadAll(http.MaxBytesReader(w, r.Body, artifact.MaxImageSize))`;
a `*http.MaxBytesError`: 413, same message. Then the store, which checks
the kind, the per-kind limit, emptiness and the quota.

Reuse, don't copy: extract from `listArtifacts` a
`func (a *App) keptArtifacts(f *artifactFilter) ([]*artifact.Entry, []string, error)`
(the store list, the `--conversation` sort and the filter loop) and have
`listArtifacts` call it; build the comment answers with the existing
`threadsJSON`, `threadToJSON`, `commentToJSON`; map store errors with
`artifactError`.

### cli: wiring

- `env_daemon.go` `daemonEnvironments`: `Settings.APIListen:
  env.APIAddr()`, and `ctrl.SetArtifactAPI(a.artifactAPI())` before
  returning. `environmentsStatus` copies `APIListen` and the hello's
  `Protocol`.
- `daemon.go:203`: `"environments: listening on %s, enroll on %s, api on %s; …"`;
  update `daemon_test.go:218`'s expected text and `env_daemon_test.go`
  `TestEnvironmentsStatus`.

### Tests (`artifact_api_test.go`)

Drive `a.artifactAPI()` with `httptest.NewRecorder` and requests whose
context carries `environment.WithAPICaller(ctx, APICaller{"env-1a2b3c4d",
"linux-server"})` and the three headers (`plan`, `planner`, `boss`), on
`newArtifactApp(t)`. `TestApp_artifactAPI`, a subtest each:

- add then show: 201; the stored record has `root` `env-1a2b3c4d/boss`,
  `created_by` `{"kind":"agent","workspace":"plan","name":"planner","environment":"linux-server"}`;
  the JSON's keys equal those of the local `ws machine artifact show`'s
  output for the same artifact (compare key sets, not values);
- size: a body of exactly `artifact.MaxImageSize` bytes of PNG (the 8-byte
  PNG signature then zeros) is 201; `MaxImageSize + 1` is 413
  `artifact_invalid` and the store is unchanged; a `Content-Length` header
  over the limit is 413 before the body is read; a 1 MiB + 1 byte text body
  is 400 `artifact_invalid` (the store's limit); an empty body 400;
- quota: the handler calls the same `Store.Add`/`Put`, whose quota check
  `internal/artifact/store_test.go:118` already covers (`maxStoreBytes`
  is unexported, and filling 2 GiB is too slow); test the mapping instead:
  `TestApiStatus` gives 409 for `artifact_full` and 413/400 as listed, and
  `writeAPIError(artifactError(fmt.Errorf("%w: x", artifact.ErrFull)))` answers
  409 `artifact_full`;
- content: bytes, `Ws-Rev`, `Ws-Sha256` match; the store's `links.json` has
  a `read` link for `plan` with `environment` `linux-server`; `rev=2` on a
  one-revision artifact is 404 `artifact_unknown`; `rev=0` and `rev=x` 400;
- put with a stale `base_rev`: 409 `artifact_conflict` with
  `details.latest`;
- rm: an artifact added by `artifact.Person()` is 403
  `artifact_forbidden`; one added under root `boss` by **another**
  environment (`env-9f9f9f9f`) is 403; the caller's own is removed;
- comments: add (thread), add with `reply`, list `open=1`, resolve; a quote
  that isn't in the text is 404 `anchor_not_found` with its details; an
  unknown JSON field, two objects, and a 9 MiB body are 400;
- hostile headers: `Ws-Workspace` with `/`, `..`-prefixed, a control
  character, or missing: 400 `usage`; no caller in the context: 500;
- a malformed id (`art-ZZ`): 404 `artifact_unknown`.

### Acceptance

The handler gives the same JSON as the local commands, enforces the size,
kind, quota and owner rules, records reads with the environment, and the
daemon serves it on `api_listen`.

### Verify

```bash
VERIFY(./internal/artifact/ ./internal/cli/ ./internal/daemon/)
```

---

## T3: `api.toml`, the `api.update` handler and the HTTPS client (agent side)

- **Depends on:** T1. **Parallel with:** T2 (disjoint files).
- **Side:** agent (the remote's `ws env agent run` and `ws machine artifact`).
- **Files:** `internal/envagent/api.go` (new), `internal/envagent/api_test.go`
  (new), `internal/envagent/requests.go`, `internal/envagent/requests_test.go`,
  `internal/envagent/ops.go`.

### `api.go`

```go
const apiFileName = "api.toml" // version 1

// ErrNoAPIEndpoint is returned when api.toml is missing: the controller
// hasn't sent api.update (it may run an older ws).
var ErrNoAPIEndpoint = errors.New("this machine doesn't know the controller's artifact API yet")

// apiFile is api.toml.
type apiFile struct {
	Version   int    `toml:"version"`
	Endpoint  string `toml:"endpoint"`
	UpdatedAt string `toml:"updated_at"` // RFC 3339
}

// APIEndpoint reads api.toml's endpoint. ErrNoAPIEndpoint when the file is
// missing; an error for unknown keys, another version, or an endpoint
// envproto.CheckEndpoint refuses.
func (f *Files) APIEndpoint() (string, error)

// SaveAPIEndpoint checks endpoint and writes api.toml with writeFileAtomic (0600), with now as updated_at.
func (f *Files) SaveAPIEndpoint(endpoint string, now time.Time) error

// APIError is a request that never got an HTTP answer, with why.
type APIError struct {
	Reason   string // envproto.ReasonIdentity, ReasonNoEndpoint, ReasonOffline or ReasonCertificate
	Endpoint string // "" for identity and no_endpoint
	Err      error
}

func (e *APIError) Error() string // "<what>: <Err>", per reason (messages below)
func (e *APIError) Unwrap() error

// APIClient calls the controller's artifact API with this machine's identity.
type APIClient struct{ /* endpoint string; client *http.Client */ }

// NewAPIClient loads the identity (Files.Load; on errKeyMismatch, as after
// a renewal mid-write, waits 100 ms and loads once more) and the endpoint,
// and builds a client over id.TLSConfig() (its ServerName is agent.toml's
// server_name): dial timeout 10 s, TLS handshake 10 s, response headers
// 2 min, no overall timeout (callers pass a context), at most 2 idle
// connections. *APIError ReasonIdentity or ReasonNoEndpoint on failure.
func NewAPIClient(files *Files) (*APIClient, error)

// Endpoint is the host:port the client calls.
func (c *APIClient) Endpoint() string

// Do sends method https://<endpoint><path>?<query> with header and body
// (size bytes; -1 unknown, 0 none). A transport failure is *APIError:
// ReasonCertificate for a *net.OpError with Op "remote error", a
// *tls.CertificateVerificationError or an x509 error; ReasonOffline for
// everything else (dial errors, timeouts, resets). Any HTTP answer is
// returned as it is, for the caller to read and close.
func (c *APIClient) Do(ctx context.Context, method, path string, query url.Values, header http.Header, body io.Reader, size int64) (*http.Response, error)
```

`APIError` messages (they reach the agent as the `artifact_unreachable`
message, so they say what to do):

- identity: `"can't read this machine's environment identity: <err>; run ws env agent status"`
- no_endpoint: `"this machine doesn't know the controller's artifact API yet: the controller sends it to ws env agent run when it connects, and only a controller running ws with protocol 5 or later has one; upgrade ws on the controller and check that ws env agent run is connected"`
- offline: `"can't reach the controller's artifact API at <endpoint>: <err>; check that the controller's ws daemon is running and that port <port> is reachable from this machine"`
- certificate: `"the TLS handshake with <endpoint> failed: <err>; if this machine's certificate expired, get a new code with ws env rotate on the controller and enroll again"`

### `api.update` on the agent

- `requests.go`: `func (a *Agent) updateAPI(ctx context.Context, id *Identity, args json.RawMessage) []byte`:
  decode `envproto.APIUpdateArgs` (malformed: `usage` "malformed api.update arguments"),
  `envproto.CheckEndpoint` (`usage` with its message), `a.files.SaveAPIEndpoint(endpoint, a.opts.Now())`
  (failure: `internal` "the endpoint wasn't saved", logged), reply
  `OKReply(APIUpdateResult{Endpoint})`, then `a.publishHello(ctx, id)` (a
  failure only warns), log `"artifact api endpoint updated"`.
- `hello()` sets `APIEndpoint` from `a.files.APIEndpoint()`; any error is
  `""`.
- `ops.go` `dispatch`: `if req.Op == envproto.OpAPIUpdate { a.reply(ctx, id, msg, a.updateAPI(ctx, id, req.Args)); return }`
  beside `ca.update`, before `admit`.

### Tests

`api_test.go`:

- `TestFiles_APIEndpoint`: missing → `ErrNoAPIEndpoint`; save then read
  round-trips; the file is 0600 (skip the mode check on Windows); an unknown
  key, `version = 2`, and an endpoint without a port are errors; `Save` of
  a bad endpoint writes nothing.
- `TestNewAPIClient`: no `agent.toml` → `*APIError` identity; enrolled but
  no `api.toml` → no_endpoint; both present → `Endpoint()`.
- `TestAPIClient_Do`, against a real controller listener: write
  `startAPIFixture(t, stub http.Handler)` in `api_test.go`, a copy of
  `enroll_test.go`'s `startEnrollFixture` with `APIListen: "127.0.0.1:0"` and
  `ctrl.SetArtifactAPI(stub)` before `Start` (leave `startEnrollFixture`
  alone); enroll `Files` through it (`newFilesAndEnroller`), then
  `SaveAPIEndpoint(ctrl.APIEndpoint(), time.Now())`:
  - a `GET` reaches the stub, which sees `environment.APICallerFrom` with
    this environment's id;
  - a 10 MiB `POST` body arrives whole (the stub hashes it);
  - after `ctrl.Close()`: `*APIError` offline, naming the endpoint;
  - roots that don't include the controller's CA (save a throwaway CA as
    `ca.pem` with `SaveRoots`): `*APIError` certificate;
  - after the controller's `store.Rotate` (revoked): the answer is a 403
    response, not an `*APIError` (the caller reads the envelope).
- `requests_test.go` `TestAgent_updateAPI` (follow the `ca.update` tests'
  setup): a good endpoint is saved, the reply is OK with it, and the next
  published hello has `api_endpoint`; a bad endpoint is a `usage` reply and
  no file; malformed args `usage`.

### Acceptance

The agent keeps the endpoint the controller sends, reports it in its hello,
and its client classifies every failure into a reason.

### Verify

```bash
VERIFY(./internal/envagent/ ./internal/environment/)
```

---

## T4: `ws machine artifact`'s remote path and `@artifact:` hints (agent side)

- **Depends on:** T2, T3.
- **Side:** agent for the commands; `artifactHints` runs on both (the
  controller for `ws claude --env` / `ws machine spawn --env`, the remote
  for a spawn on it).
- **Files:** `internal/cli/machine_artifact_remote.go` (new),
  `internal/cli/machine_artifact_remote_test.go` (new),
  `internal/cli/machine_artifact.go`, `internal/cli/machine_artifact_test.go`,
  `internal/cli/artifact_comment.go`, `internal/cli/claude.go`,
  `internal/cli/claude_test.go`, `internal/cli/machine.go`,
  `internal/cli/scope.go`.

**Test first.** Commit `machine_artifact_remote_test.go`'s end-to-end
subtests 1-3 first and record that they fail with `artifact_unreachable`
(on the T2+T3 branch the commands still refuse); then the code.

### `machine_artifact_remote.go`

```go
// artifactRequestTimeout bounds one remote artifact command.
const artifactRequestTimeout = 3 * time.Minute

// remoteArtifacts is the controller's store, reached over the artifact API
// from an enrolled agent machine (ADR 0087).
type remoteArtifacts struct{ client *envagent.APIClient }

// newRemoteArtifacts builds the client from CONFIG_DIR/environment, or
// artifact_unreachable from its *envagent.APIError.
func (a *App) newRemoteArtifacts() (*remoteArtifacts, error)

// unreachableError is artifact_unreachable for an *envagent.APIError, with
// details {"reason": e.Reason, "endpoint": e.Endpoint or null}; any other
// error is returned as it is.
func unreachableError(err error) error

// call sends one request as caller c with the Ws-* headers (url.QueryEscape'd)
// and returns the answer's body (at most envproto.MaxAPIJSONBytes; more is
// codeInternal "the controller's answer is too large") for a 2xx. A non-2xx
// answer is its envelope as an error: scopeReplyError(&env.Error), which
// keeps a known code and its details; a body that isn't an envelope is
// codeInternal naming the status.
func (r *remoteArtifacts) call(ctx context.Context, c *machineArtifactCaller, method, path string, query url.Values, body io.Reader, size int64) ([]byte, error)

// writeRemoteJSON prints the controller's answer: as it is when ws has no
// warnings of its own, else with them appended to its "warnings".
func (a *App) writeRemoteJSON(body []byte) error

func (a *App) remoteAdd(ctx context.Context, c *machineArtifactCaller, path string, opts *artifactAddOptions) error
func (a *App) remoteList(ctx context.Context, c *machineArtifactCaller, kind string, mine bool, limit int) error
func (a *App) remoteShow(ctx context.Context, c *machineArtifactCaller, arg string) error
func (a *App) remoteGet(ctx context.Context, c *machineArtifactCaller, id string, rev int, out string) error
func (a *App) remotePut(ctx context.Context, c *machineArtifactCaller, arg, path string, opts *machinePutOptions) error
func (a *App) remoteRemove(ctx context.Context, c *machineArtifactCaller, args []string) error
func (a *App) remoteCommentList(ctx context.Context, c *machineArtifactCaller, arg string, openOnly bool) error
func (a *App) remoteCommentAdd(ctx context.Context, c *machineArtifactCaller, arg string, opts *commentAddOptions, cmd *cobra.Command) error
func (a *App) remoteCommentResolve(ctx context.Context, c *machineArtifactCaller, args []string, reopen bool) error

// writeArtifactStream writes r to dir/name as get does locally (dir 0700,
// a temp file 0600 renamed into place), reading at most size+1 bytes, and
// keeps it only when exactly size bytes with SHA-256 sum arrived; otherwise
// it removes the temp file and is codeInternal "the download didn't match
// the revision". name must pass artifact.CheckName.
func writeArtifactStream(dir, name string, r io.Reader, size int64, sum string) (string, error)
```

Behaviour, each matching the local command:

- **add / put:** read the input as `readArtifactInput` does (the local 10
  MiB check, before anything is sent), send the bytes with their length.
  add: `POST /v1/artifacts?name=<name>&title=&note=`; put:
  `POST /v1/artifacts/<id>/revisions?base_rev=&note=` (omit empty or zero
  parameters).
- **list / show / comment list:** `GET`, print the answer.
- **get:** `GET /v1/artifacts/<id>` (the show JSON; decode its `artifact`
  into `artifactSummaryJSON` plus `revisions`), pick the revision (the
  reference's or `--rev`, else `latest`), `GET .../content?rev=<n>`;
  refuse an answer whose `Ws-Rev` isn't `n`, or whose `Content-Length` is
  over `artifact.MaxImageSize`; stream it with `writeArtifactStream` into
  `artifactDir(c, out, id, n)` using the revision's `size` and `Ws-Sha256`;
  print `machineGetJSON` (`content_type` from `Ws-Content-Type`). The read
  is recorded by the controller; don't call `recordRead`.
- **rm:** one `DELETE` per argument, collected with `itemResults` and
  `finishItems("artifacts", …)` as `removeArtifacts` does, so a refusal of
  one doesn't stop the others and the exit code is the first failure's.
- **comment add:** the local flag checks and `commentBody`, then `POST`
  `envproto.CommentAddBody`; **resolve:** `POST
  .../threads/<thread>/resolve` with `{"reopen"}`.

### `machine_artifact.go` and `artifact_comment.go`

- `machineArtifactCaller` gains `remote *remoteArtifacts` (nil on the
  controller). `machineCaller`: after `a.caller()` (so `outside_workspace`
  still comes first), when `a.enrolledAgent()`, set `remote` from
  `newRemoteArtifacts()` (its error is returned) instead of refusing.
  Fix the comment and the message naming ADR 0078.
- Each `newMachineArtifact*Cmd`'s `RunE`: `if c.remote != nil { return
  a.remote<Op>(ctx, &c, …) }` before the local path, with
  `context.WithTimeout(ctx, artifactRequestTimeout)`.
- `commentRun`'s agent branch passes the caller: change its `fn` to
  `func(ctx context.Context, by artifact.Actor, c *machineArtifactCaller, args []string, cmd *cobra.Command) error`
  (`c` nil for people), and the three comment commands branch on
  `c != nil && c.remote != nil`.

### `@artifact:` hints (`claude.go`, `machine.go`, `scope.go`)

```go
// artifactHints appends the hint line for the @artifact: references in the
// prompt (the first argument when it isn't a flag), once each referenced
// artifact is found: in the local store, or through the controller's API
// on an enrolled agent machine. An unknown one is artifact_unknown before
// anything is made. When the API can't be reached it warns
// ("couldn't check the @artifact references: <message>") and still appends
// the hint. For a prompt bound for environment remote it also warns when
// that environment's agent is known to speak a protocol below 5.
func (a *App) artifactHints(ctx context.Context, args []string, remote string) ([]string, error)

// checkArtifactsExist is artifact_unknown for the first id not found; on an
// enrolled agent machine it asks the API (GET /v1/artifacts/<id>), and an
// artifact_unreachable answer becomes a warning and nil.
func (a *App) checkArtifactsExist(ctx context.Context, ids []string) error

// environmentProtocol is the protocol of environment name's last hello,
// from the daemon's status; false when it isn't known (no daemon, offline,
// not enrolled, or on an agent machine). It never warns.
func (a *App) environmentProtocol(ctx context.Context, name string) (int, bool)
```

The remote-target warning, when `environmentProtocol` says N < 5:
`"<remote> runs ws with protocol <N>; its agents can't read artifacts until ws there is upgraded (protocol 5)"`.
Callers: `prepareClaude` gains `ctx` (from `runClaude`), `runMachineSpawnRunner`
(`machine.go:329`) and `runRemoteSpawn` (`scope.go:248`) pass theirs.

### Tests

`machine_artifact_remote_test.go`. Helper `newRemoteArtifactFixture(t)`:

- the **controller**: `ctl := newArtifactApp(t)`; an `environment.Store`
  over `ctl.app.paths.Environments`; a `Controller` with `Listen`,
  `EnrollListen`, `APIListen` all `"127.0.0.1:0"`, `PublicName:
  "127.0.0.1"`, on an `envtest.NewBroker()`;
  `ctrl.SetArtifactAPI(ctl.app.artifactAPI())`; `store.Add("linux-server",
  …)`, `Start`, `store.Rotate` for a code (as `startEnvController` does);
- the **agent**: `agent, dir := newMachineArtifactApp(t)` (workspace `plan`,
  root `boss`), `agent.app.runEnvAgentEnroll(ctx, code)`, then
  `envagent.NewFiles(agent.app.paths.Environment).SaveAPIEndpoint(ctrl.APIEndpoint(), time.Now())`.

`TestApp_machineArtifactRemote` (**end-to-end over the real mTLS listener**:
agent command → controller store → back):

1. `add` on the agent: exit 0; `ctl.app.artifactStore().Show(id)` has the
   bytes, `root` `<env id>/boss`, `created_by.environment` `linux-server`.
2. `get` on the agent: the file under `dir/.ws-artifacts/<id>/r1/`, mode
   0600, same bytes; the controller's links record a `read` by `plan` with
   environment `linux-server`.
3. `list --mine` and `show` print the artifact; `put` makes r2 and `get
   --rev 1` returns r1's bytes.
4. comments: `comment add --body`, `comment add --reply t1`, `comment list
   --open`, `comment resolve t1`.
5. `rm` of an artifact a person added on the controller: exit 4
   `artifact_forbidden`; `rm` of its own: removed.
6. size: a 10 MiB PNG (signature + zeros) adds; 10 MiB + 1 byte is
   `artifact_invalid` exit 2 and the controller's store is unchanged.
7. **offline**: after `ctrl.Close()`, `list` is exit 1 `artifact_unreachable`,
   `details.reason` `offline`.
8. **no endpoint** (an old controller never sent `api.update`): remove
   `api.toml`; `list` gives reason `no_endpoint`.
9. **revoked**: after the controller's `store.Rotate("linux-server", …)`,
   `list` gives reason `refused`.
10. **discovery**: without `api.toml`, run `agent.app.runEnvAgentRun` with
    `newEnvClient` returning `broker.NewClient()` (as `TestApp_runEnvAgentRun`
    does); `require.Eventually` `api.toml` holds `ctrl.APIEndpoint()`; then
    `list` works. Cancel the agent and wait for it to return. If
    `runEnvAgentRun` needs stores the mocked agent app lacks, build this
    subtest's agent with `newEnvTestApp` instead, and say so in the report.
11. **@artifact: on the remote**: `ws machine spawn -- "look at @artifact:<id>"`
    on the agent (its fake Claude client) starts with the prompt ending in
    `artifact.HintLine([]string{id})`; an unknown id is `artifact_unknown`
    with no workspace created; with the controller closed the spawn
    starts, the hint is appended, and the warning is printed.

Also:

- `TestWriteArtifactStream`: a short body, a long body, and a wrong
  checksum each leave no file in the directory and return an error; a name
  with `/` is refused.
- `TestApp_machineArtifactOutside` keeps its `agent.toml` = `"x"` subtest
  (now: reason `identity`), renamed to say so.
- `claude_test.go` `TestApp_artifactHints`: "on a remote environment the
  prompt is unchanged, with a warning" becomes "a prompt for a remote
  environment is checked and gets the hint" (and an unknown reference there
  is `artifact_unknown`); "an enrolled agent passes references with a
  warning" becomes "an enrolled agent without an API endpoint warns and
  still appends the hint".

### Acceptance

Every `ws machine artifact` command works from an enrolled agent with the
local command's output and exit codes; references are checked and hinted on
both sides; each failure is `artifact_unreachable` with its reason.

### Verify

```bash
VERIFY(./internal/cli/ ./internal/envagent/ ./internal/environment/)
make build && WS_CONFIG_DIR="$(mktemp -d)" ./bin/ws llm machine artifact >/dev/null
```

---

## T5 (small): docs

- **Depends on:** T4 (may start beside it; the files are disjoint).
- **Side:** both (documentation).
- **Files:** `docs/tools/ws-machine.md`, `docs/tools/ws-artifact.md`,
  `docs/tools/ws-env.md`, `docs/config.md`, `docs/state.md`,
  `internal/cli/guides/ws-machine-artifact.md`,
  `internal/cli/guides/ws-machine-artifact-get.md`,
  `docs/design/wsenv/audit.md`, `README.md`.

Changes:

- `ws-machine.md` (`## ws machine artifact`, line 505 on, and the error
  table at 632), `ws-artifact.md` (line 302 and the remote paragraph),
  `internal/cli/guides/ws-machine-artifact.md`: on an enrolled agent the
  commands reach the controller's store over its artifact API (ADR 0087);
  `artifact_unreachable` now means the API can't be reached, with
  `details.reason` (`identity`, `no_endpoint`, `offline`, `certificate`,
  `refused`) and what to do for each; both machines need protocol 5.
- `ws-machine-artifact-get.md`: the same `path` on a remote.
- `ws-env.md`: the third listener, `api.update`, and `api_endpoint` in the
  hello; `ws daemon status` shows `api on <addr>`.
- `docs/config.md` `[environments]`: an `api_listen` row (`"0.0.0.0:8885"`,
  "Where the daemon serves the artifact API to enrolled agents over mutual
  TLS (0087)").
- `docs/state.md`: `environment/api.toml` (agent side: `version`,
  `endpoint`, `updated_at`, 0600, written by `api.update`); in the
  artifacts rows, `root` may be `<environment id>/<root>` and actors may
  have `environment`.
- `docs/design/wsenv/audit.md`: the `artifact add/get/put/list/show/rm` row
  becomes "refused with `artifact_unreachable` until ADR 0087; then
  reached over the controller's artifact API" with verdict **Bug 12 (was
  marked OK in error)**, and the `@artifact:` row notes that ADR 0087
  replaces the plain-text fallback.
- `README.md` line 135: agents on remote environments use the same
  commands; the roadmap's artifacts line gains `0087`.
- **Deployment note** (in `ws-env.md`, under listeners): port 8885 must be
  reachable from remotes like 8883 and 8884; on Kubernetes it is a plain
  TCP port on the LoadBalancer Service, never an HTTP ingress, and the pod's
  NetworkPolicy must allow it.

### Verify

```bash
go test -count=1 -run 'TestGuideTopics|TestReadGuide|TestApp_runLLM|TestApp_printGuides' ./internal/cli/ \
&& grep -rn "can't reach them yet\|out of scope" docs/tools internal/cli/guides; test $? -eq 1
```

(The grep must find nothing.)
