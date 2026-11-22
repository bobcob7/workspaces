# Tasks: PR 2, "Core"

PR 2 of [ADR 0076](../../adrs/0076-environment-platforms-and-phasing.md)'s
stack: `internal/envproto`, `internal/environment` (records, CA,
enrollment, CA rotation, revocation, the controller), the broker embedded in
the `ws daemon`, `ws env add|list|rm|rotate`, `ws env ca rotate|status`,
`ws env agent enroll|run|status|unenroll` with the agent's hello and
snapshot, `[environments]` in `config.toml`, and the docs. It accepts ADRs
0073, 0074 and 0075.

**Not in this PR:** workspace operations (`workspace.*`, `agent.*`,
`files.*`, `config.*` requests; `ws claude --env`, `ws list --env`;
`environment` on records), terminal and log streams, the desktop app and
Workspace Online, Windows, and the `ws-online` image's ports.

**Read first:** [constraints.md](constraints.md) (pinned counts, lists and
golden values, lint rules, and what the pod can't run). Every task below
names the rows of it that it touches.

## Rules for every task

- Go conventions: no blank lines inside function bodies; interfaces at the
  consumer, all of a package's interfaces in its one `interfaces.go` with
  one `//go:generate go tool moq -out moq_test.go . <names>` line; mocks
  only from moq, never hand-written; unexported by default; constructor
  injection with `*slog.Logger` passed in; tool versions only in `go.mod`'s
  `tool` directive (moq and golangci-lint are already there); tests use
  `t.Context()`, `t.TempDir()`, `require` for preconditions and `assert`
  for verifications, `slog.New(slog.NewJSONHandler(io.Discard, nil))`; one
  test file per source file, `Test<Receiver>_<Method>` with `t.Run`
  subtests; `t.Parallel()` only on tests that block on I/O.
- Comments end with a period; every exported name has a doc comment.
- Time in files and JSON is RFC 3339 UTC. JSON fields are `snake_case`;
  a field with no value is `null`, never missing; lists are `[]`, never
  `null`.
- Keys and secrets: private keys are written with mode 0600 into
  directories with mode 0700; no private key, enrollment secret or code is
  ever logged.
- Verify with exactly the commands listed, one at a time, `go test` with
  `-p 2`. `make lint` lints the whole repo and runs last. Commit and push
  after each file. Never wait on a permission prompt: list a check you
  couldn't run as unverified.
- Fingerprints are the lowercase hex SHA-256 of a certificate's DER
  (`envproto.Fingerprint`), 64 characters, no prefix. Certificate serials
  are `cert.SerialNumber.Text(16)`.

## Decisions this design adds to the ADRs

T8 and T9 write these into the ADRs and docs.

1. **Agent-initiated requests use a new topic `ws/v1/env/<id>/up`.**
   ADR 0074's ACL lets an agent publish only replies, so `cert.renew`
   (ADR 0075: "the agent sends `cert.renew`") had nowhere to go. The agent
   publishes `{op, args}` to `env/<id>/up` with Response Topic
   `ws/v1/env/<id>/req` and Correlation Data; the controller replies there.
   The agent tells a reply from a request by its Correlation Data. ACL:
   an agent may also publish to its own `up`. ADR 0074's topic table gains
   the row.
2. **`ca.update`** is a request from the controller on `env/<id>/req`
   (`{"op": "ca.update", "args": {"ca_bundle": "<PEM>"}}`), answered on
   `env/<id>/res/<req>`. "Retained until acknowledged" is done statelessly:
   whenever an agent's hello lists `ca_fingerprints` that don't include
   every root the controller trusts, the controller sends `ca.update`.
3. **`renew_before_days` on the agent** is the agent's own
   `[environments] renew_before_days` (its own `config.toml`, same key and
   default 30). The controller's value drives only the CA-expiry warning.
4. **Records:** `environments/<id>.toml` has `state = "pending"` or
   `"enrolled"`. `ws env rm` revokes and deletes the record; `ws env
   rotate` revokes the current certificate, if any, and makes the record
   `pending` with a new code.
5. **New error codes** besides ADR 0075's two: `environment_unknown` (3,
   from ADR 0072, needed by `ws env rm|rotate`), `environment_exists` (4,
   a name taken, or `enroll` on a machine already enrolled) and `ca_in_use`
   (4, `ws env ca rotate --finish` while environments hold old-CA
   certificates). `environment_offline`, `_outdated` and `_mismatch` wait
   for PR 3.
6. **`ws env agent` is hidden** from help and `ws llm` (ADR 0073), so it has
   no guide; the people commands `ws env …` and `ws env ca …` have guides.
7. **Listeners need `environments.public_name`.** The daemon listens only
   when a CA exists and `public_name` is set; otherwise its status says why.
   `ws env add` refuses without `public_name` (`usage`). A daemon whose
   listeners are open doesn't idle-exit. Config changes take effect at the
   daemon's next start; `ws env` changes take effect at once through
   `POST /v1/environments/reload` on the daemon's socket.
8. **Until T10**, the daemon has no broker (`daemon.NopEnvironments` with a
   warning) and `ws env agent run` has no MQTT client (`internal`). The
   broker and client are reached only through interfaces, and everything
   else is tested against `internal/environment/envtest`, an in-memory
   broker that does a real TLS handshake over `net.Pipe`.

## Order

```
T1 ── T2 ── T3 ── T4 ──┬── T5 ── T7 ──┬── T9 ── T10 (needs broker-spike.md)
                       └── T6 ── T8 ──┘
```

No task is `small`: each touches more than two files.

| Task | Depends on | Parallel with |
| --- | --- | --- |
| T1 Protocol types and the in-memory broker | – | – |
| T2 CA, certificates and TLS configs | T1 | – |
| T3 Store: records, codes, revocation, CA rotation | T2 | – |
| T4 Controller: policy, enrollment listener, presence, renewal | T3 | – |
| T5 Agent: files and enrollment client | T4 | T5 ∥ T6 |
| T6 Config `[environments]` and the daemon host | T4 | T6 ∥ T5, T6 ∥ T7 |
| T7 Agent: run loop, hello, snapshot, renewal, CA update | T5 | T7 ∥ T6, T7 ∥ T8 |
| T8 `ws env` and `ws env ca` for people, error codes, guides, ws-env.md | T6 | T8 ∥ T7 |
| T9 `ws env agent`, `claude.Version`, state and ADR docs | T7, T8 | – |
| T10 Broker adapter and paho client (**needs the spike**) | T9, broker-spike.md | – |

T1 to T9 need nothing from the broker spike.

---

## T1. Protocol types and the in-memory broker

- **Depends on:** nothing.
- **Pinned values touched:** constraints.md › Lint rules (`Makefile` grep
  for `envtest`, new).

### Files

Create:

- `internal/envproto/envproto.go`, `envproto_test.go`: package doc, `Message`, `ConnectOptions`, `Peer`, `Access`, `Rules`, `PeerFromState`, `Fingerprint`.
- `internal/envproto/topics.go`, `topics_test.go`
- `internal/envproto/payloads.go`, `payloads_test.go`
- `internal/envproto/code.go`, `code_test.go`
- `internal/environment/envtest/broker.go`, `broker_test.go`

Change: `Makefile` (one lint line).

### `internal/envproto` (imports only the standard library)

```go
// Package envproto is the wire contract between ws's controller and its
// environment agents (ADRs 0074, 0075): topics, payloads, enrollment codes.
package envproto

// Message is one MQTT 5 application message.
type Message struct {
	Topic           string
	Payload         []byte
	QoS             byte
	Retain          bool
	ResponseTopic   string
	CorrelationData []byte
	Expiry          time.Duration // 0: none
}

// ConnectOptions are what an agent connects with.
type ConnectOptions struct {
	Address   string // host:port of the broker
	TLS       *tls.Config
	ClientID  string
	KeepAlive time.Duration
	Will      *Message
	WillDelay time.Duration
}

// Peer is a connected client as its verified certificate names it.
type Peer struct {
	ClientID string
	CN       string // the environment id
	Serial   string // cert.SerialNumber.Text(16)
	Issuer   string // Fingerprint of the CA that signed it
}

// Access is what a client asks to do with a topic.
type Access int

const (
	AccessPublish Access = iota + 1
	AccessSubscribe
)

// Rules are what a broker asks before it lets a client connect, publish
// or subscribe. The controller's in-process client isn't checked.
type Rules struct {
	Connect func(p Peer) error
	Allow   func(p Peer, access Access, topic string) bool
}

var ErrNoVerifiedChain = errors.New("no verified client certificate chain")

// PeerFromState reads the verified leaf (VerifiedChains[0][0]) and its
// issuer (VerifiedChains[0][1]). ClientID is left empty for the broker to
// fill. ErrNoVerifiedChain when there is no chain of at least 2.
func PeerFromState(cs *tls.ConnectionState) (Peer, error)

// Fingerprint is the lowercase hex SHA-256 of cert.Raw.
func Fingerprint(cert *x509.Certificate) string
```

`topics.go`:

```go
const Prefix = "ws/v1/env/"
const (
	LeafStatus   = "status"
	LeafSnapshot = "snapshot"
	LeafReq      = "req"
	LeafRes      = "res"
	LeafUp       = "up"
	LeafStream   = "stream"
)

// Topic is Prefix + id + "/" + strings.Join(levels, "/").
func Topic(id string, levels ...string) string
// ParseTopic splits "ws/v1/env/<id>/<rest...>"; ok is false for any other
// topic, an id that isn't ValidID, or an empty rest. A "+" id is not valid.
func ParseTopic(topic string) (id string, rest []string, ok bool)
// ValidID reports whether id matches ^env-[0-9a-f]{8}$.
func ValidID(id string) bool
// Match reports whether an MQTT topic filter matches topic: "+" is one
// level, "#" (last level only) is zero or more levels.
func Match(filter, topic string) bool
```

`payloads.go` (all JSON shapes are fixed here; other tasks only use them):

```go
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
	ClaudeMissing   = "missing"
	ClaudePresent   = "present"
	ClaudeSignedOut = "signed_out"
	OpCertRenew = "cert.renew"
	OpCAUpdate  = "ca.update"
	EnrollPath      = "/v1/enroll"
	MaxRequestBytes = 256 << 10
)

// Hello is the retained status an agent publishes on connect (ADR 0073).
type Hello struct {
	Status         string    `json:"status"`
	At             time.Time `json:"at"`
	WSVersion      string    `json:"ws_version"`
	OS             string    `json:"os"`
	Arch           string    `json:"arch"`
	Claude         string    `json:"claude"`
	ClaudeVersion  *string   `json:"claude_version"`
	CertSerial     string    `json:"cert_serial"`
	CertNotAfter   time.Time `json:"cert_not_after"`
	CAFingerprints []string  `json:"ca_fingerprints"`
}

// OfflinePayload is the Last Will: exactly {"status":"offline"}.
func OfflinePayload() []byte

// Snapshot is what an agent publishes on env/<id>/snapshot.
type Snapshot struct {
	At         time.Time       `json:"at"`
	Workspaces json.RawMessage `json:"workspaces"` // ws list --json's "workspaces" array
}

type Request struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
}
type Reply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *ReplyError     `json:"error"`
}
type ReplyError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}
// OKReply marshals {ok: true, result, error: null}.
func OKReply(result any) ([]byte, error)
// ErrorReply marshals {ok: false, result: null, error: {code, message, details: null}}.
func ErrorReply(code, message string) []byte

type RenewArgs struct{ CSR string `json:"csr"` } // PEM "CERTIFICATE REQUEST"
type RenewResult struct {
	Cert     string `json:"cert"`      // PEM
	CABundle string `json:"ca_bundle"` // PEM, every trusted root, current first
}
type CAUpdateArgs struct{ CABundle string `json:"ca_bundle"` }
type CAUpdateResult struct{ Fingerprints []string `json:"fingerprints"` }

// EnrollRequest is POST /v1/enroll's body.
type EnrollRequest struct {
	Secret string `json:"secret"`
	CSR    string `json:"csr"`
}
// EnrollResponse is its 200 answer.
type EnrollResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Broker     string `json:"broker"`      // host:port agents dial (public_name and listen's port)
	ServerName string `json:"server_name"` // public_name
	Cert       string `json:"cert"`
	CABundle   string `json:"ca_bundle"`
}
// ErrorEnvelope is every non-200 answer: {"error": {code, message, details}}.
type ErrorEnvelope struct{ Error ReplyError `json:"error"` }
```

`code.go`:

```go
const CodePrefix = "ws-enroll-v1."

var ErrCodeInvalid = errors.New("invalid enrollment code")

// Code is the decoded enrollment code (ADR 0075).
type Code struct {
	Host        string `json:"h"` // host:port of the enrollment listener
	Fingerprint string `json:"f"` // the CA's Fingerprint
	Secret      string `json:"s"` // NewSecret's output
}
// String is CodePrefix + base64.RawURLEncoding of the JSON {"h","f","s"}.
func (c Code) String() string
// ParseCode trims spaces and checks the prefix, base64url, JSON (unknown
// keys refused), that h splits into host and a port 1-65535, f is 64 hex,
// and s decodes (RawURLEncoding) to 16 bytes; else ErrCodeInvalid wrapped
// with the reason.
func ParseCode(s string) (Code, error)
// NewSecret reads 16 bytes from random and returns them RawURLEncoded (22 chars).
func NewSecret(random io.Reader) (string, error)
// HashSecret is the lowercase hex SHA-256 of secret's bytes.
func HashSecret(secret string) string
```

### `internal/environment/envtest`

An in-memory broker for tests (like ADR 0067's fakes). It is imported only
by `_test.go` files. It does a **real TLS handshake** over `net.Pipe` so
certificate checks are exercised, then routes messages in memory.

```go
// Package envtest is an in-memory MQTT broker for tests of the controller
// and the agent. Never imported outside _test.go files.
package envtest

var (
	ErrNotServing   = errors.New("envtest: broker not started")
	ErrRefused      = errors.New("envtest: connection refused")
	ErrNotConnected = errors.New("envtest: not connected")
	ErrDisconnected = errors.New("envtest: disconnected by the broker")
)

type Broker struct{ /* mutex, tls config, rules, clients, retained, controller subs */ }

func NewBroker() *Broker
// Start records tlsConfig and rules and returns addr unchanged. A second Start fails.
func (b *Broker) Start(ctx context.Context, addr string, tlsConfig *tls.Config, rules envproto.Rules) (string, error)
// Publish publishes as the controller: no ACL check.
func (b *Broker) Publish(ctx context.Context, msg envproto.Message) error
// Subscribe subscribes the controller; retained messages matching filter
// are delivered first. The returned func unsubscribes.
func (b *Broker) Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) (func(), error)
// Clients lists connected peers, ClientID set.
func (b *Broker) Clients() []envproto.Peer
// Disconnect drops every client with serial, publishing each one's will;
// it returns how many.
func (b *Broker) Disconnect(serial string) int
// Retained returns the retained message on topic.
func (b *Broker) Retained(topic string) (envproto.Message, bool)
// Close stops the broker; clients' Done channels close, wills aren't published.
func (b *Broker) Close() error
// NewClient returns an unconnected client of b.
func (b *Broker) NewClient() *Client

type Client struct{ /* ... */ }
// Connect: ErrNotServing before Start. Runs tls.Server (b's config) and
// tls.Client (opts.TLS) handshakes over net.Pipe concurrently; any failure
// is ErrRefused wrapping it. Then PeerFromState(server side), ClientID =
// opts.ClientID, rules.Connect(peer) (error → ErrRefused). Keeps opts.Will.
// A client may Connect again after Done closed; Done is then a new channel.
func (c *Client) Connect(ctx context.Context, opts envproto.ConnectOptions) error
// Publish: rules.Allow(peer, AccessPublish, topic) false → the client is
// disconnected (will published) and ErrDisconnected is returned.
func (c *Client) Publish(ctx context.Context, msg envproto.Message) error
// Subscribe: rules.Allow(peer, AccessSubscribe, filter) false → as Publish.
func (c *Client) Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) error
// Done closes when the broker drops the client or Close is called.
func (c *Client) Done() <-chan struct{}
// Close is a normal disconnect: no will.
func (c *Client) Close() error
```

Delivery is synchronous: the broker copies the matching subscriber list
under its lock, releases it, then calls each handler in subscription order,
so a handler may publish. A retained message with an empty payload clears
the topic. Will Delay is ignored: a will is published at once (documented
in the package comment).

`Makefile` `lint`, after the fakes rule:

```make
	@# envtest is a test broker: imported only from _test.go files.
	@! grep -rn '"github.com/bobcob7/workspaces/internal/environment/envtest"' --include='*.go' . | grep -v '_test\.go:'
```

### Acceptance

- `Topic("env-1a2b3c4d", "res", "r1")` = `ws/v1/env/env-1a2b3c4d/res/r1`;
  `ParseTopic` round-trips it and refuses `ws/v1/env/+/status`,
  `ws/v1/env/env-XYZ/status`, `ws/v1/other`.
- `Match`: `ws/v1/env/+/status` matches `ws/v1/env/env-1a2b3c4d/status`,
  not `…/snapshot`; `ws/v1/#` matches everything under `ws/v1/`;
  `a/+/c` doesn't match `a/b/d/c`.
- A `Code` round-trips through `String`/`ParseCode`; each malformed form
  (no prefix, bad base64, bad JSON, unknown key, no port, short `f`,
  secret not 16 bytes) is `ErrCodeInvalid`.
- `OfflinePayload()` is exactly `{"status":"offline"}`; `OKReply` and
  `ErrorReply` produce the documented JSON, with `null`s.
- `PeerFromState` returns CN, serial and issuer fingerprint from a real
  handshake (test builds a CA and a client certificate with
  `crypto/x509`), and `ErrNoVerifiedChain` without client certificates.
- envtest: a client with a certificate the server config trusts connects;
  one without, or from another CA, gets `ErrRefused`; `rules.Connect`
  returning an error refuses; retained messages reach later subscribers;
  an ACL-violating publish disconnects the client and publishes its will;
  `Disconnect(serial)` closes `Done` and publishes the will; `Close` on a
  client publishes no will; a handler that publishes from inside a
  delivery doesn't deadlock.

### Verify

```sh
go test -p 2 ./internal/envproto/... ./internal/environment/envtest/...
make lint
```

---

## T2. CA, certificates and TLS configs

- **Depends on:** T1.
- **Pinned values touched:** none outside the new package.

### Files

Create `internal/environment/authority.go`, `authority_test.go`,
`tlsconfig.go`, `tlsconfig_test.go`, `files.go`, `files_test.go`.

### Files on disk

`CONFIG_DIR/environments/ca/` (mode 0700):

| File | Content | Mode |
| --- | --- | --- |
| `ca.key` | current CA key, PEM `PRIVATE KEY` (PKCS #8), ECDSA P-256 | 0600 |
| `ca.crt` | current CA certificate, PEM | 0644 |
| `old.crt` | the previous CA's certificate during a rotation; absent otherwise. Its key is deleted at rotation | 0644 |
| `server.key`, `server.crt` | the listeners' certificate for `public_name`, signed by the current CA | 0600, 0644 |

### Signatures (all unexported)

```go
// files.go
// writeFileAtomic writes data to a temp file in path's directory with
// perm, syncs, and renames it into place.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error
func encodeCert(cert *x509.Certificate) []byte
func encodeKey(key *ecdsa.PrivateKey) ([]byte, error)
func readCert(path string) (*x509.Certificate, error)
func readKey(path string) (*ecdsa.PrivateKey, error)
func randomSerial(random io.Reader) (*big.Int, error) // 128 bits, > 0

// authority.go
type authority struct {
	dir    string
	now    func() time.Time
	random io.Reader
}
func newAuthority(dir string, now func() time.Time, random io.Reader) *authority
func (a *authority) exists() (bool, error)
// ensure creates the CA when ca.crt is missing: P-256, CN
// "ws environments CA <8 hex>", IsCA, BasicConstraintsValid,
// MaxPathLenZero, KeyUsage CertSign|CRLSign, valid now-5m to now+365d.
func (a *authority) ensure() (created bool, err error)
// trust reads ca.crt and old.crt; errNoCA (wrapped by T3's ErrNoCA) when ca.crt is missing.
func (a *authority) trust() (trust, error)
// rotate: errNoCA without a CA. When old.crt exists it's dropped (returned).
// Writes the new key and cert to ca.key.new/ca.crt.new, renames ca.crt to
// old.crt, renames the new files into place, removes server.* so the next
// serverCertificate makes one from the new CA.
func (a *authority) rotate() (current, old, dropped *x509.Certificate, err error)
// dropOld removes old.crt; returns the dropped cert, or nil when none.
func (a *authority) dropOld() (*x509.Certificate, error)
// signClient checks the CSR's signature and that its key is ECDSA P-256
// (else errCSRInvalid), ignores its subject, and issues CN=cn, serial
// randomSerial, now-5m to now+90d, KeyUsage DigitalSignature, ExtKeyUsage
// ClientAuth, signed by the current CA.
func (a *authority) signClient(csrPEM []byte, cn string) (*x509.Certificate, error)
// serverCertificate returns server.crt/server.key when signed by the
// current CA, valid for publicName (VerifyHostname) and not within 30 days
// of expiry; otherwise it makes a new one: CN publicName, an IP SAN when
// net.ParseIP(publicName) != nil else a DNS SAN, ExtKeyUsage ServerAuth,
// NotAfter min(now+365d, CA NotAfter). tls.Certificate.Certificate is
// [leaf DER, current CA DER] so clients see the CA in the chain.
func (a *authority) serverCertificate(publicName string) (tls.Certificate, error)

type trust struct {
	current *x509.Certificate
	old     *x509.Certificate // nil outside a rotation
}
func (t trust) pool() *x509.CertPool
func (t trust) bundlePEM() []byte        // current first, then old
func (t trust) fingerprints() []string   // same order
func (t trust) contains(fp string) bool
// caExpiryWarning is "" unless now is within renewBeforeDays of
// ca.NotAfter, then: "the environments CA expires on 2027-11-22, within 30
// days; run ws env ca rotate".
func caExpiryWarning(ca *x509.Certificate, now time.Time, renewBeforeDays int) string

var (
	errNoCA         = errors.New("no environments CA")
	errCSRInvalid   = errors.New("invalid certificate request")
)

// tlsconfig.go
// tlsState is what the listeners serve with, swapped whole on reload.
type tlsState struct {
	trust  trust
	server tls.Certificate
}
// brokerTLS: MinVersion TLS 1.3; GetConfigForClient returns, from
// state() at each handshake, a config with Certificates {server},
// ClientAuth RequireAndVerifyClientCert, ClientCAs trust.pool().
func brokerTLS(state func() *tlsState) *tls.Config
// enrollTLS: MinVersion TLS 1.3, no client certificate;
// GetCertificate returns state().server.
func enrollTLS(state func() *tlsState) *tls.Config
```

### Acceptance

Tests use a fixed clock and `crypto/rand.Reader`, and real handshakes over
`net.Pipe` (`tls.Server` with `brokerTLS`, `tls.Client` with a client cert).

- `ensure` creates the four files with the modes above, and is a no-op the
  second time; the CA is valid 365 days.
- `signClient` keeps CN = the given id whatever the CSR says; refuses an
  RSA and a P-384 CSR, and a CSR whose signature is wrong.
- ADR 0075 rotation test **(h)**: rotating twice in a row keeps at most two
  roots: after the second rotation `trust().fingerprints()` has 2 entries,
  the first rotation's `old` is returned as `dropped`, and `ca.key` is only
  the newest key.
- **(g)**: `caExpiryWarning` is empty at 31 days before expiry with 30,
  non-empty at exactly 30 days and at 29, and follows a changed setting
  (non-empty at 40 days with 45).
- **(b), TLS level**: after `rotate`, a client certificate signed by the old
  CA still completes the handshake while `old.crt` exists; a client
  certificate from the new CA does too.
- **(e), TLS level**: after `dropOld`, the old-CA client certificate's
  handshake fails.
- A client with no certificate fails the `brokerTLS` handshake; `enrollTLS`
  accepts it and presents `[leaf, CA]`.
- `serverCertificate` is reused when valid and regenerated after `rotate`
  (its issuer is the new CA), and covers an IP `public_name` (`127.0.0.1`).

### Verify

```sh
go test -p 2 ./internal/environment/...
make lint
```

---

## T3. Store: records, codes, revocation, CA rotation

- **Depends on:** T2.
- **Pinned values touched:** none outside the new package (state.md rows
  are written in T9).

### Files

Create `internal/environment/errors.go`, `store.go`, `store_test.go`,
`revoked.go`, `revoked_test.go`, `rotation.go`, `rotation_test.go`.

### On disk

`CONFIG_DIR/environments/` (0700):

- `<id>.toml`, one per environment, written with `writeFileAtomic` (0600):

  ```toml
  version = 1
  id = "env-1a2b3c4d"
  name = "linux-server"
  created_at = "2026-11-22T14:02:00Z"
  state = "enrolled"            # or "pending"
  secret_hash = "9f86d0…"       # HashSecret of the current code's secret
  enrolled_at = "2026-11-22T14:05:11Z"   # "" while pending
  cert_serial = "5e0c…"         # "" while pending
  cert_not_after = "2027-02-20T14:05:11Z"
  cert_ca = "3b1f…"             # Fingerprint of the issuing CA
  ```

  Reading refuses unknown keys and `version != 1`.
- `revoked`: one line per revoked certificate,
  `<serial> <env id> <RFC 3339 time>\n`; blank lines and `#` lines ignored;
  rewritten whole with `writeFileAtomic` (0600).
- `.lock`: taken with `lockfile.Exclusive(ctx, path)` around every
  read-modify-write (CLI and daemon both use it).
- `ca/`: T2's authority.

`CONFIG_DIR/state/.environments/<name>/snapshot.json` (dir 0700, file
0600): written by the controller (T4) through `WriteSnapshot`; deleted by
`Remove`.

### Signatures

```go
// errors.go
var (
	ErrUnknown          = errors.New("no such environment")
	ErrExists           = errors.New("an environment with that name exists")
	ErrNameInvalid      = errors.New(`an environment name is 1-32 of a-z, 0-9 and "-", starts with a letter, and isn't "local"`)
	ErrEnrollmentUsed   = errors.New("that code was already used")
	ErrEnrollmentFailed = errors.New("enrollment failed")
	ErrCAInUse          = errors.New("environments still hold certificates from the old CA")
	ErrNoCA             = errors.New("there's no environments CA yet; run ws env add first")
	ErrNoRotation       = errors.New("there's no CA rotation to finish")
)

// store.go
type State string

const (
	StatePending  State = "pending"
	StateEnrolled State = "enrolled"
)

// Record is environments/<id>.toml.
type Record struct {
	Version      int    `toml:"version"`
	ID           string `toml:"id"`
	Name         string `toml:"name"`
	CreatedAt    string `toml:"created_at"`
	State        State  `toml:"state"`
	SecretHash   string `toml:"secret_hash"`
	EnrolledAt   string `toml:"enrolled_at"`
	CertSerial   string `toml:"cert_serial"`
	CertNotAfter string `toml:"cert_not_after"`
	CertCA       string `toml:"cert_ca"`
}

// Store is the controller's environments: records, CA, revocations and snapshots.
type Store struct {
	dir      string
	stateDir string
	ca       *authority
	now      func() time.Time
	random   io.Reader
}
// NewStore: dir is CONFIG_DIR/environments, stateDir CONFIG_DIR/state. No I/O.
func NewStore(dir, stateDir string, now func() time.Time, random io.Reader) *Store
// Add validates name (ErrNameInvalid), refuses a taken name (ErrExists),
// ensures the CA, makes id "env-<8 hex>" (retry on a clash), a secret, and
// a pending record; returns the record, the code
// (envproto.Code{Host: enrollHost, Fingerprint: current CA, Secret}.String())
// and whether the CA was created.
func (s *Store) Add(ctx context.Context, name, enrollHost string) (rec Record, code string, caCreated bool, err error)
// Rotate gives an existing environment a new code: revokes its current
// certificate if enrolled, clears the cert_* and enrolled_at fields, sets
// pending and the new secret_hash. Returns the revoked serials ([] if none).
func (s *Store) Rotate(ctx context.Context, name, enrollHost string) (rec Record, code string, revoked []string, err error)
// Remove revokes the current certificate, deletes the record and
// state/.environments/<name>/. ErrUnknown for an unknown name.
func (s *Store) Remove(ctx context.Context, name string) (rec Record, revoked []string, err error)
func (s *Store) Get(name string) (Record, error)   // ErrUnknown
func (s *Store) ByID(id string) (Record, error)    // ErrUnknown
func (s *Store) List() ([]Record, error)           // sorted by name; [] when none
// Enroll (under the lock) hashes secret and compares it with every
// record's secret_hash with subtle.ConstantTimeCompare, without stopping
// at the first match. A pending match is signed (CN = id) and recorded
// enrolled; the secret_hash is kept so a second use is ErrEnrollmentUsed.
// No match, or a CSR signClient refuses, is ErrEnrollmentFailed.
// broker and serverName are copied into the response.
func (s *Store) Enroll(ctx context.Context, secret string, csrPEM []byte, broker, serverName string) (envproto.EnrollResponse, error)
// Renew signs csrPEM for an enrolled id with the current CA, keeps CN = id,
// records the new serial, not_after and cert_ca. ErrUnknown for a missing
// or pending id; ErrEnrollmentFailed for a bad CSR.
func (s *Store) Renew(ctx context.Context, id string, csrPEM []byte) (envproto.RenewResult, error)
// Revoked is the set of revoked serials.
func (s *Store) Revoked() (map[string]bool, error)
func (s *Store) WriteSnapshot(name string, data []byte) error
func (s *Store) ReadSnapshot(name string) ([]byte, error) // fs.ErrNotExist when none
// caExists, trust and serverCertificate expose T2's authority to the controller.
func (s *Store) caExists() (bool, error)
func (s *Store) trust() (trust, error)
func (s *Store) serverCertificate(publicName string) (tls.Certificate, error)

// rotation.go
// CA is one root as ws env ca shows it.
type CA struct {
	Fingerprint string
	NotAfter    time.Time
}
type CAStatus struct {
	Current  *CA // nil without a CA
	Old      *CA // nil outside a rotation
	OnCurrent int // enrolled environments whose cert_ca is the current CA
	OnOld     int // ... the old CA
	Warnings  []string // caExpiryWarning of the current CA
}
type CARotation struct {
	Current  *CA
	Old      *CA
	Dropped  *CA      // the root no longer trusted, or nil
	Stranded []string // names of enrolled environments whose cert_ca is Dropped
}
func (s *Store) CAStatus(renewBeforeDays int) (CAStatus, error)
// RotateCA: ErrNoCA without a CA.
func (s *Store) RotateCA(ctx context.Context) (CARotation, error)
// FinishCA: ErrNoRotation without an old CA; ErrCAInUse (wrapped with the
// count) when OnOld > 0 and !force.
func (s *Store) FinishCA(ctx context.Context, force bool) (CARotation, error)
```

### Acceptance

- A code works once and not twice (`ErrEnrollmentUsed`), and a wrong
  secret is `ErrEnrollmentFailed` (ADR 0075 conformance).
- Two concurrent `Enroll` calls with the same secret: exactly one succeeds
  (single use under the lock).
- `Rotate` on an enrolled environment revokes its serial and makes the old
  code fail and the new one work; on a pending one the old code fails.
- `Remove` revokes, deletes the record and the snapshot directory, and an
  unknown name is `ErrUnknown`.
- `Add` refuses `local`, `Local`, `-x`, a 33-char name, and a taken name.
- Records refuse unknown keys and version 2.
- `Renew` keeps CN = id, and after `RotateCA` the renewed certificate's
  issuer is the new CA (ADR 0075 rotation test **(c)**, store level).
- **(d)**: `CAStatus` counts `OnOld` for environments enrolled before
  `RotateCA`; `FinishCA(false)` returns `ErrCAInUse` while any remain and
  succeeds after each has renewed; `FinishCA(true)` succeeds with them in
  `Stranded`.
- **(f), store level**: a code from `Add` after `RotateCA` carries the new
  CA's fingerprint.
- `revoked` survives a reread; comments and blank lines are ignored.

### Verify

```sh
go test -p 2 ./internal/environment/...
make lint
```

---

## T4. Controller: policy, enrollment listener, presence, renewal

- **Depends on:** T3.
- **Pinned values touched:** none outside the package.

### Files

Create `internal/environment/interfaces.go`, `policy.go`,
`policy_test.go`, `enroll.go`, `enroll_test.go`, `controller.go`,
`controller_test.go`, and the generated `moq_test.go`.

### Signatures

```go
// interfaces.go
//go:generate go tool moq -out moq_test.go . broker

// broker is the MQTT 5 broker the daemon embeds (ADR 0074). envtest.Broker
// and T10's adapter implement it.
type broker interface {
	// Start listens on addr for MQTT over TLS with tlsConfig, checks every
	// client with rules, serves until Close, and returns the address it
	// listens on.
	Start(ctx context.Context, addr string, tlsConfig *tls.Config, rules envproto.Rules) (string, error)
	// Publish and Subscribe act as the in-process controller, unchecked.
	Publish(ctx context.Context, msg envproto.Message) error
	Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) (func(), error)
	Clients() []envproto.Peer
	// Disconnect drops every client whose certificate has serial.
	Disconnect(serial string) int
	Close() error
}

// policy.go
type policy struct {
	store *Store
	trust func() trust
}
// connect refuses (errors, never panics) a peer whose ClientID != CN, whose
// CN isn't envproto.ValidID, that has no enrolled record, whose serial is
// in Revoked(), or whose Issuer isn't trusted.
func (p *policy) connect(peer envproto.Peer) error
// allowed is ADR 0074's ACL plus "up", for the peer's own id (= CN) only.
// Publish: <id>/status, <id>/snapshot, <id>/up, <id>/res/<x>,
// <id>/stream/<x>/out, where <x> is one level with no "+" or "#".
// Subscribe: <id>/req and <id>/stream/<x>/in where <x> may be "+".
// Everything else, any "#", and any topic outside ws/v1/env/ is refused.
func allowed(peer envproto.Peer, access envproto.Access, topic string) bool
func (p *policy) rules() envproto.Rules

// enroll.go
// newEnrollHandler serves POST /v1/enroll and nothing else (404 for other
// paths, 405 for other methods). Body at most 64 KiB, JSON
// EnrollRequest with unknown fields refused. Answers:
// 200 EnrollResponse; 400 {"error":{"code":"usage",…}} for a malformed
// body; 403 "enrollment_failed"; 409 "enrollment_used"; 500 "internal".
// Logs "environment enrolled" with id, or "enrollment refused" with the
// remote address and code; never the secret, CSR or code.
func newEnrollHandler(log *slog.Logger, enroll func(ctx context.Context, secret string, csrPEM []byte) (envproto.EnrollResponse, error)) http.Handler

// controller.go
// Settings are [environments] as the daemon read it.
type Settings struct {
	Listen          string // "0.0.0.0:8883"
	EnrollListen    string // "0.0.0.0:8884"
	PublicName      string
	RenewBeforeDays int
}

// Presence is one environment as the daemon sees it.
type Presence struct {
	ID       string
	Name     string
	State    State
	Status   string // "pending", "online" or "offline"
	LastSeen *time.Time
	Hello    *envproto.Hello // the last online hello, or nil
}

type Status struct {
	Listening    bool
	Listen       string // the bound address, "" when not listening
	EnrollListen string
	Environments []Presence // sorted by name; [] when none
}

// Controller runs the broker and the enrollment listener in the daemon.
type Controller struct{ /* log, store, broker, settings, now, mu, presence, state atomic.Pointer[tlsState], enroll server, unsubscribe funcs, warnings */ }

func NewController(log *slog.Logger, store *Store, b broker, settings Settings, now func() time.Time) *Controller
// Start does nothing when there's no CA, and adds a warning instead of
// listening when PublicName is "". Otherwise it loads trust and the server
// certificate, starts the enrollment listener (net.ListenConfig.Listen +
// tls.NewListener(enrollTLS) + http.Server, ReadHeaderTimeout 10 s), starts
// the broker with brokerTLS and policy.rules(), and subscribes
// envproto.Topic("+", "status"), ("+", "snapshot"), ("+", "up"),
// ("+", "res", "+").
func (c *Controller) Start(ctx context.Context) error
// Reload rereads the CA and server certificate (and Starts when the CA is
// new), swaps the TLS state, disconnects every client policy.connect now
// refuses (revoked, removed, untrusted CA), and sends ca.update to every
// online environment whose hello lacks a trusted root.
func (c *Controller) Reload(ctx context.Context) error
func (c *Controller) Status() Status
// Warnings: why it isn't listening (no public_name, a Start error) and
// caExpiryWarning(current CA, now, RenewBeforeDays).
func (c *Controller) Warnings() []string
func (c *Controller) Close() error
```

Message handling (unexported methods, each under 60 lines):

Enrollment answers carry `broker` = `net.JoinHostPort(PublicName, <port
of the broker's bound address>)` and `server_name` = `PublicName`; the
handler's `enroll` func is `store.Enroll` with those two.

| Topic | Handling |
| --- | --- |
| `env/<id>/status` | ignore an id with no record. `online`: keep the hello, `LastSeen = now`, status online; if `hello.ca_fingerprints` lacks any trusted root, send `ca.update`. `offline`: status offline, `LastSeen = now`. |
| `env/<id>/snapshot` | must parse as `envproto.Snapshot`; write `{"version":1,"id":…,"name":…,"received_at":…,"snapshot":<payload>}` with `store.WriteSnapshot(name, …)` |
| `env/<id>/up` | parse `Request` (≤ `MaxRequestBytes`). `cert.renew` → `store.Renew` → `OKReply(RenewResult)`; an error → `ErrorReply("enrollment_failed", …)`; unknown op → `ErrorReply("usage", "unknown operation …")`. Reply only when `ResponseTopic == Topic(id, "req")`, with the same Correlation Data, QoS 1. |
| `env/<id>/res/<req>` | log `ca.update` acknowledged or refused |

`ca.update` request: topic `Topic(id, "req")`, payload
`{"op":"ca.update","args":{"ca_bundle":"<PEM>"}}`, Response Topic
`Topic(id, "res", <req>)`, Correlation Data `<req>` (16 random hex chars),
QoS 1, Expiry 30 s.

### Acceptance (controller tests use `envtest.Broker`, a fixed clock and certs from the store)

- `allowed`: table test of every allowed row, another environment's topics,
  `#`, `+` in a publish, `ctl/events`, and a topic outside `ws/v1/env/`.
- An agent client (envtest, its own cert) publishing to another
  environment's `status` is disconnected (ADR 0074 conformance).
- `connect` refuses a revoked serial, a pending id, a removed id, and
  `ClientID != CN`.
- Enrollment over a real loopback listener (`EnrollListen =
  "127.0.0.1:0"`, `PublicName = "127.0.0.1"`): 200 then 409 for the same
  secret, 403 for a wrong one, 405 for GET, 404 for `/v1/other`; the
  secret never appears in the captured log.
- Presence: an `online` hello makes the environment online with its hello;
  `Disconnect(serial)` publishes the will and it turns offline.
- Snapshot messages are written to `state/.environments/<name>/snapshot.json`.
- `cert.renew` on `up` returns a certificate with CN = id from the current
  CA; after `RotateCA` + `Reload`, from the new CA (**(c)**).
- After `store.Remove` + `Reload`, the removed environment's client is
  disconnected and can't reconnect (revoked serial refused and
  disconnected, ADR 0075 conformance).
- **(e)**: after `FinishCA(true)` + `Reload`, a connected old-CA client is
  disconnected and refused on reconnect.
- **(a), controller side**: after `RotateCA` + `Reload` a connected agent is
  **not** disconnected and receives `ca.update` with both roots.
- **(g)**: `Warnings()` holds the CA-expiry warning within
  `RenewBeforeDays`.
- No CA: `Start` listens on nothing and `Status().Listening` is false.

### Verify

```sh
go generate ./internal/environment/
go test -p 2 ./internal/environment/...
make lint
```

---

## T5. Agent: files and enrollment client

- **Depends on:** T4. **Parallel:** T5 ∥ T6.
- **Pinned values touched:** constraints.md › Lint rules (`gosec` G402
  `nolint` with the given text).

### Files

Create `internal/envagent/files.go`, `files_test.go`, `enroll.go`,
`enroll_test.go`, `status.go`, `status_test.go`.

### On disk (the agent's `CONFIG_DIR/environment/`, mode 0700)

| File | Content | Mode |
| --- | --- | --- |
| `agent.toml` | `version = 1`, `id`, `name`, `broker` (host:port), `server_name`, `enrolled_at` (RFC 3339) | 0600 |
| `key.pem` | the agent's ECDSA P-256 key, PEM `PRIVATE KEY` | 0600 |
| `cert.pem` | its client certificate | 0600 |
| `ca.pem` | the trusted roots, 1 or 2, current first | 0600 |

### Signatures

```go
var (
	ErrNotEnrolled      = errors.New("this machine isn't enrolled; run ws env agent enroll <code>")
	ErrAlreadyEnrolled  = errors.New("this machine is already enrolled; run ws env agent unenroll first")
	ErrPinMismatch      = errors.New("the server's CA doesn't match the code's fingerprint; nothing was sent")
	ErrEnrollmentUsed   = errors.New("that code was already used")
	ErrEnrollmentFailed = errors.New("enrollment failed")
)

// Identity is an enrolled agent.
type Identity struct {
	ID, Name, Broker, ServerName string
	EnrolledAt                   time.Time
	Key                          *ecdsa.PrivateKey
	Cert                         *x509.Certificate
	Roots                        []*x509.Certificate
}
// TLSConfig: MinVersion TLS 1.3, Certificates {Cert, Key}, RootCAs Roots,
// ServerName.
func (id *Identity) TLSConfig() *tls.Config
func (id *Identity) Fingerprints() []string

type Files struct{ dir string }
func NewFiles(dir string) *Files
func (f *Files) Load() (Identity, error) // ErrNotEnrolled when agent.toml is missing
func (f *Files) Save(id *Identity) error
// SaveCertificate replaces key.pem and cert.pem (each atomically, key first).
func (f *Files) SaveCertificate(key *ecdsa.PrivateKey, cert *x509.Certificate) error
func (f *Files) SaveRoots(roots []*x509.Certificate) error
func (f *Files) Remove() (Identity, error) // ErrNotEnrolled when nothing is there
// parseRoots: 1 or 2 PEM CERTIFICATE blocks, each IsCA, else an error.
func parseRoots(data []byte) ([]*x509.Certificate, error)

type Enroller struct {
	log    *slog.Logger
	files  *Files
	random io.Reader
}
func NewEnroller(log *slog.Logger, files *Files, random io.Reader) *Enroller
// Enroll: ErrAlreadyEnrolled if Load succeeds; envproto.ParseCode; a new
// P-256 key and CSR (CN "ws-environment-agent"); POST
// https://<code.Host>/v1/enroll through an http.Client whose transport uses
// pinTLS(code); maps 409 → ErrEnrollmentUsed, other non-200 →
// ErrEnrollmentFailed with the server's message; checks the returned
// certificate has CN = response id, the CSR's public key, and chains to a
// returned root whose fingerprint is code.Fingerprint; then Save.
func (e *Enroller) Enroll(ctx context.Context, code string) (Identity, error)
func newKeyAndCSR(random io.Reader) (*ecdsa.PrivateKey, []byte, error)
// pinTLS: InsecureSkipVerify (with the nolint in constraints.md) and
// VerifyConnection = verifyPin(fingerprint, host): some presented
// certificate with IsCA has the fingerprint, and the leaf verifies
// against it alone for DNSName host and ExtKeyUsageServerAuth; else
// ErrPinMismatch. Runs during the handshake, before any request byte.
func pinTLS(fingerprint, host string) *tls.Config
func verifyPin(fingerprint, host string) func(tls.ConnectionState) error

// status.go
// AgentStatus is what ws env agent status prints.
type AgentStatus struct {
	Enrolled       bool
	ID, Name       string
	Broker         string
	CertSerial     string
	CertNotAfter   time.Time
	RenewAt        time.Time // CertNotAfter - renewBeforeDays
	CAFingerprints []string
}
func StatusOf(files *Files, renewBeforeDays int) (AgentStatus, error) // Enrolled false, no error, when not enrolled
```

### Acceptance (tests run a real `environment.Controller` with `envtest.Broker`, `EnrollListen` and `Listen` `127.0.0.1:0`, `PublicName` `127.0.0.1`, and the code from `store.Add`)

- Enroll succeeds; the four files exist with mode 0600 in a 0700 directory;
  `Load` round-trips; the record on the controller is `enrolled`.
- A second `Enroll` on the same files is `ErrAlreadyEnrolled`; the same code
  from fresh files is `ErrEnrollmentUsed`.
- A code with a wrong fingerprint is `ErrPinMismatch` **and the enroll
  handler received no request** (count requests with a wrapping handler or
  the controller's log) (ADR 0075 conformance).
- **(f)**: a code made before `RotateCA` + `Reload` fails with
  `ErrPinMismatch`, cleanly (no file written); a code made after works.
- The private key's PEM and DER never appear in the request body (capture
  it with an `httptest` server that proxies, or a transport wrapper).
- `StatusOf` gives `RenewAt` = not-after − days; `Enrolled: false` with
  nothing on disk. `Remove` deletes the directory's files.

### Verify

```sh
go test -p 2 ./internal/envagent/...
make lint
```

---

## T6. Config `[environments]` and the daemon host

- **Depends on:** T4. **Parallel:** T6 ∥ T5, T6 ∥ T7.
- **Pinned values touched:** constraints.md › Config keys (all rows),
  Daemon (all rows), `internal/cli/interfaces.go`, Docs with lists
  (`docs/config.md`, `docs/tools/ws-config.md`, `docs/tools/ws-daemon.md`,
  `guides/ws-daemon-status.md`).

### Files

Change: `internal/config/config.go`, `config_test.go`, `edit.go`,
`edit_test.go`, `paths.go`, `paths_test.go`; `internal/daemon/interfaces.go`,
`server.go`, `server_test.go`, `client.go`, `client_test.go`,
`autostart_test.go`, `moq_test.go` (generated); `internal/cli/app.go`,
`daemon.go`, `daemon_test.go`, `runner_test.go`, `interfaces.go`,
`moq_test.go` (generated), `guides/ws-daemon-status.md`; `docs/config.md`,
`docs/tools/ws-config.md`, `docs/tools/ws-daemon.md`.
Create: `internal/daemon/environments.go`, `environments_test.go`;
`internal/cli/env_daemon.go`, `env_daemon_test.go`.

### Config

```toml
[environments]
listen = "0.0.0.0:8883"        # agents, mutual TLS (ADR 0074)
enroll_listen = "0.0.0.0:8884" # first contact (ADR 0075)
public_name = "ws.example.com" # DNS name or IP in the server certificate and codes; required to listen
renew_before_days = 30         # 1 to 60
```

```go
// config.go: Config gains
Environments Environments `toml:"environments"`
// and coreFile gains the same field (a strict, core table).

// Environments is [environments] (ADRs 0074, 0075).
type Environments struct {
	Listen          string `toml:"listen" json:"listen"`
	EnrollListen    string `toml:"enroll_listen" json:"enroll_listen"`
	PublicName      string `toml:"public_name" json:"public_name"`
	RenewBeforeDays *int   `toml:"renew_before_days" json:"renew_before_days"`
}
var ErrNoPublicName = errors.New("environments.public_name isn't set; set it with ws config set environments.public_name <dns name or IP>")
func (e *Environments) ListenAddr() string  // Listen or "0.0.0.0:8883"
func (e *Environments) EnrollAddr() string  // EnrollListen or "0.0.0.0:8884"
func (e *Environments) RenewDays() int      // RenewBeforeDays or 30
// EnrollHost is net.JoinHostPort(PublicName, port of EnrollAddr()); ErrNoPublicName when unset.
func (e *Environments) EnrollHost() (string, error)
// BrokerHost is net.JoinHostPort(PublicName, port of ListenAddr()); ErrNoPublicName when unset.
func (e *Environments) BrokerHost() (string, error)
// check (called from normalize): listen and enroll_listen split into a host
// and a port 1-65535; public_name is an IP or a DNS name (labels of
// [A-Za-z0-9-], 1-63 chars, not starting or ending with "-") with no port;
// renew_before_days 1-60. Any failure wraps ErrInvalid naming the key.
func (e *Environments) check() error
```

`paths.go`: `Paths` gains `Environments string` (`<root>/environments`, the
controller's) and `Environment string` (`<root>/environment`, the agent's).

`edit.go`: `environmentRules()` returns, in this order,
`environments.listen` (string), `environments.enroll_listen` (string),
`environments.public_name` (string), `environments.renew_before_days`
(int). `Settings` appends them **last**, only when
`meta.IsDefined("environments")`. `valueOf` gives the effective values,
`defaultOf` gives `"0.0.0.0:8883"`, `"0.0.0.0:8884"`, `""`, `30`; `ws config
set` and `unset` accept the four keys (follow how `daemon.resume_on_start`
and the `TypeInt` keys are parsed and verified). `defaultFile` is **not**
changed.

### Daemon

```go
// interfaces.go (go:generate list gains EnvironmentHost)
// EnvironmentHost is what serves remote environments in the daemon (ADR
// 0074): the controller, or NopEnvironments.
type EnvironmentHost interface {
	Start(ctx context.Context) error
	Reload(ctx context.Context) error
	Status() EnvironmentsStatus
	Warnings() []string
	Close() error
}

// environments.go
type EnvironmentsStatus struct {
	Listening    bool                  `json:"listening"`
	Listen       string                `json:"listen"`
	EnrollListen string                `json:"enroll_listen"`
	Environments []EnvironmentPresence `json:"environments"`
}
type EnvironmentPresence struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	State    string            `json:"state"`  // pending, enrolled
	Status   string            `json:"status"` // pending, online, offline
	LastSeen *time.Time        `json:"last_seen"`
	Hello    *EnvironmentHello `json:"hello"`
}
type EnvironmentHello struct {
	WSVersion     string  `json:"ws_version"`
	OS            string  `json:"os"`
	Arch          string  `json:"arch"`
	Claude        string  `json:"claude"`
	ClaudeVersion *string `json:"claude_version"`
	CertNotAfter  string  `json:"cert_not_after"`
}
// NopEnvironments serves no environments; Reason, when set, is its one warning.
type NopEnvironments struct{ Reason string }
// (methods: Start/Reload/Close nil; Status{Environments: []}; Warnings [Reason] or [])

// client.go
// Status gains, after Runners:
Environments *EnvironmentsStatus `json:"environments"` // null when no daemon runs
// ReloadEnvironments asks the daemon to reread the environments (POST
// /v1/environments/reload). ErrNotRunning when nothing answers.
func (c *Client) ReloadEnvironments(ctx context.Context) error

// server.go
func Serve(ctx context.Context, p Paths, origin string, sup RunnerSupervisor, envs EnvironmentHost, log *slog.Logger) error
func serve(ctx context.Context, p Paths, origin string, sup RunnerSupervisor, envs EnvironmentHost, log *slog.Logger, opts options) error
```

- `serve` calls `envs.Start(ctx)` after the socket listens and `daemon.json`
  is written; a Start error is logged (`"environments didn't start"`) and
  kept as a warning, and the daemon keeps running. `envs.Close()` runs on
  exit.
- `GET /v1/status` sets `Environments` to `envs.Status()` and appends
  `envs.Warnings()` (and the Start error) to `Warnings`.
- `POST /v1/environments/reload` calls `envs.Reload(r.Context())`: `200
  {"reloaded": true}` or the existing error envelope.
- `idleExit` is false while `envs.Status().Listening`.
- Every existing caller passes `NopEnvironments{}` (constraints.md lists
  them).

### CLI wiring

```go
// interfaces.go (go:generate list gains envBroker): the same method set as
// internal/environment's broker, so the cli can hold one.
type envBroker interface {
	Start(ctx context.Context, addr string, tlsConfig *tls.Config, rules envproto.Rules) (string, error)
	Publish(ctx context.Context, msg envproto.Message) error
	Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) (func(), error)
	Clients() []envproto.Peer
	Disconnect(serial string) int
	Close() error
}

// app.go: App gains
// newEnvBroker builds the daemon's MQTT broker (ADR 0074). Until T10 it
// returns errNoBroker.
newEnvBroker func(log *slog.Logger) (envBroker, error)

// env_daemon.go
var errNoBroker = errors.New("this ws was built without an MQTT broker, so remote environments are off")
// envStore is the controller's store for this config directory.
func (a *App) envStore() *environment.Store // NewStore(a.paths.Environments, a.paths.State, a.now, rand.Reader)
// daemonEnvironments is the daemon's host: envHost around a Controller, or
// NopEnvironments{Reason: err.Error()} when newEnvBroker fails.
func (a *App) daemonEnvironments(log *slog.Logger) daemon.EnvironmentHost
// envHost adapts *environment.Controller to daemon.EnvironmentHost.
type envHost struct{ ctrl *environment.Controller }
// reloadEnvironments asks a running daemon to reload. With no daemon it
// returns the warning "no ws daemon runs for this config directory; remote
// environments connect once one does (ws daemon run, or the desktop app)";
// on another error, "the ws daemon didn't reload the environments: <err>".
func (a *App) reloadEnvironments(ctx context.Context) []string
```

`runDaemon` passes `a.daemonEnvironments(log)` to `daemon.Serve`.
`notRunningStatus` leaves `Environments` nil. `printDaemonStatus` text adds
one line: `environments: off` or `environments: listening on 0.0.0.0:8883,
enroll on 0.0.0.0:8884; 1 online, 1 offline, 1 pending`.

`ws daemon status --json` example (guide and `docs/tools/ws-daemon.md`):

```json
"environments": {
  "listening": true,
  "listen": "0.0.0.0:8883",
  "enroll_listen": "0.0.0.0:8884",
  "environments": [
    {"id": "env-1a2b3c4d", "name": "linux-server", "state": "enrolled",
     "status": "online", "last_seen": "2026-11-22T14:02:00Z",
     "hello": {"ws_version": "v0.31.0", "os": "linux", "arch": "amd64",
               "claude": "present", "claude_version": "2.4.1",
               "cert_not_after": "2027-02-20T14:05:11Z"}}
  ]
}
```

### Acceptance

- Config: each bad value (`listen = "8883"`, port 0 or 70000,
  `public_name = "ws.example.com:8884"`, `renew_before_days = 0` or `61`, an
  unknown key in `[environments]`) is `config_invalid`; defaults apply when
  the table is absent; `EnrollHost` and `BrokerHost` join `public_name` with
  the right ports, and `ErrNoPublicName` without it.
- `ws config list` on the default file still prints exactly the five fixed
  keys; with an `[environments]` table, the four keys come last;
  `ws config set environments.renew_before_days 61` is
  `config_value_invalid`; set and unset of each key round-trip.
- Every pinned test in constraints.md › Config keys still passes unchanged.
- Daemon: with a fake `EnvironmentHost` (moq), status includes its status
  and warnings; reload calls `Reload`; a `Start` error is a warning, not a
  failure; a transient daemon doesn't idle-exit while `Listening`.
- CLI: `daemonEnvironments` returns `NopEnvironments` with `errNoBroker`'s
  text by default; with a test `newEnvBroker` returning `envtest.Broker` and
  a store with a CA, the daemon's status (`ws daemon status --json` through
  `daemon.Serve` in-process, as `daemon_test.go` does) shows
  `listening: true` (use `127.0.0.1:0` addresses and `public_name
  127.0.0.1`).

### Verify

```sh
go generate ./internal/daemon/ ./internal/cli/
go test -p 2 ./internal/config/... ./internal/daemon/...
go test -p 2 ./internal/cli/...
make lint
```

---

## T7. Agent: run loop, hello, snapshot, renewal, CA update

- **Depends on:** T5. **Parallel:** T7 ∥ T6, T7 ∥ T8.
- **Pinned values touched:** constraints.md › Lint rules (`math/rand/v2`).

### Files

Create `internal/envagent/interfaces.go`, `agent.go`, `agent_test.go`,
`renew.go`, `renew_test.go`, `requests.go`, `requests_test.go`,
`moq_test.go` (generated).

### Signatures

```go
// interfaces.go
//go:generate go tool moq -out moq_test.go . mqttClient snapshotSource claudeProbe

// mqttClient is the agent's MQTT 5 connection (envtest.Client in tests,
// T10's paho adapter in ws). Connect may be called again after Done.
type mqttClient interface {
	Connect(ctx context.Context, opts envproto.ConnectOptions) error
	Publish(ctx context.Context, msg envproto.Message) error
	Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) error
	Done() <-chan struct{}
	Close() error
}
// snapshotSource is ws list --json's "workspaces" array on this machine.
type snapshotSource interface {
	Workspaces(ctx context.Context) (json.RawMessage, error)
}
// claudeProbe reports envproto.ClaudeMissing or ClaudePresent, and the version when present.
type claudeProbe interface {
	Probe(ctx context.Context) (state string, version *string)
}

// agent.go
type Options struct {
	Version         string        // ws's version, for the hello
	RenewBeforeDays int           // the agent's own [environments] renew_before_days
	SnapshotCheck   time.Duration // 10 s: rebuild the snapshot
	SnapshotEvery   time.Duration // 30 s: publish even if unchanged
	RenewCheck      time.Duration // 1 h
	KeepAlive       time.Duration // 30 s
	WillDelay       time.Duration // 10 s
	RequestTimeout  time.Duration // 30 s
	BackoffMin      time.Duration // 1 s
	BackoffMax      time.Duration // 60 s
	Now             func() time.Time
	After           func(time.Duration) <-chan time.Time
	Jitter          func() float64 // math/rand/v2 Float64
}
func DefaultOptions(version string, renewBeforeDays int) Options

type Agent struct{ /* log, files, client, snapshots, claude, random, opts, pending replies */ }
func NewAgent(log *slog.Logger, files *Files, client mqttClient, snapshots snapshotSource, claude claudeProbe, random io.Reader, opts Options) *Agent
// Run loads the identity (ErrNotEnrolled), then connects and serves until
// ctx ends, reconnecting with backoff and never giving up, except when the
// certificate has expired (ErrCertificateExpired: "the certificate expired
// on <date>; run ws env rotate <name> on the controller, then ws env agent
// enroll <code>"). Returns nil when ctx ends.
func (a *Agent) Run(ctx context.Context) error
var ErrCertificateExpired = errors.New("the agent's certificate has expired")

// backoff is min(BackoffMax, BackoffMin<<attempt) scaled into [d/2, d] by jitter.
func backoff(attempt int, minimum, maximum time.Duration, jitter float64) time.Duration

// renew.go
// renewDue: now is at or after notAfter - days*24h, and before notAfter.
func renewDue(notAfter, now time.Time, renewBeforeDays int) bool
// renew sends cert.renew on Topic(id, "up") with Response Topic
// Topic(id, "req") and Correlation Data (16 random hex chars), waits up to
// RequestTimeout for the matching reply, checks CN and public key, saves
// key and cert, and republishes the hello.
func (a *Agent) renew(ctx context.Context, id *Identity) error

// requests.go
// handle routes a message on Topic(id, "req"): a reply when its
// Correlation Data matches a pending request; else a Request. ca.update →
// parseRoots, SaveRoots, update the identity, reply OKReply(CAUpdateResult),
// republish the hello. Any other op → ErrorReply("usage", "unknown
// operation <op>"). A reply goes only to a Response Topic under
// Topic(id, "res", …), with the request's Correlation Data, QoS 1.
func (a *Agent) handle(ctx context.Context, id *Identity, msg envproto.Message)
func (a *Agent) hello(ctx context.Context, id *Identity) envproto.Hello
```

One connection: `Connect` with `{Address: id.Broker, TLS: id.TLSConfig(),
ClientID: id.ID, KeepAlive, Will: {Topic(id, "status"), OfflinePayload(),
QoS 1, Retain true}, WillDelay}`; subscribe `Topic(id, "req")`; publish the
hello (retained, QoS 1) and a snapshot (`Snapshot{At, Workspaces}`,
retained, QoS 1); then select on ctx, `Done()`, the snapshot check (publish
when the bytes changed or `SnapshotEvery` passed), the renew check, and
incoming messages (handled off the subscription callback through a
buffered channel of 16; a full channel drops and logs). On ctx end:
publish `OfflinePayload()` retained, then `Close`. The attempt counter
resets after a successful connect.

### Acceptance (unit tests with moq; integration tests with `environment.Controller` + `envtest.Broker` + `Enroller` from T5, short durations, fixed `Now`)

- `renewDue` at exactly 30 days before expiry is true, at 31 days false, at
  29 true, after expiry false, and with 45 days true at 40 (ADR 0075:
  boundary, a day either side, a changed setting).
- `backoff` grows 1 s, 2 s, 4 s … and is capped at 60 s; jitter 0 gives
  d/2, 1 gives d.
- A client whose `Connect` fails 8 times is retried 8 times with growing
  waits and then connects (never gives up).
- Integration: after `Run` starts, the controller shows the environment
  online with the hello (`ws_version`, `os` = `runtime.GOOS`, `claude`
  from the probe, `ca_fingerprints`), and `snapshot.json` holds the
  workspaces from the source.
- With `Now` inside the renewal window, the agent renews; the new cert keeps
  CN = id; `cert.pem` and `key.pem` change.
- **(a)**: after `store.RotateCA` + `controller.Reload`, the connected agent
  stays connected (its `Done` stays open), `ca.pem` then holds both roots,
  and after `broker.Disconnect(serial)` it reconnects to the broker that now
  serves a certificate from the new CA (so it verifies against the new
  root).
- **(c)**: a renewal after rotation is signed by the new CA and keeps its CN.
- A controller subscription to `ws/v1/#` sees every message of a session
  with a renewal; none contains the agent's old or new private key (PEM or
  DER) (ADR 0075 conformance).
- After `store.Remove` + `Reload`, the agent is disconnected and every
  reconnect is refused; it keeps backing off.
- An expired certificate makes `Run` return `ErrCertificateExpired`
  without connecting.
- An unknown op gets a `usage` error reply; a reply aimed at another
  environment's topic isn't sent.

### Verify

```sh
go generate ./internal/envagent/
go test -p 2 ./internal/envagent/...
make lint
```

---

## T8. `ws env` and `ws env ca` for people, error codes, guides, ws-env.md

- **Depends on:** T6. **Parallel:** T8 ∥ T7.
- **Pinned values touched:** constraints.md › Error codes (all rows),
  Command tree and `ws llm` guides (all rows), Docs with lists
  (`docs/tools/README.md`, `docs/adrs/0016-output-contract.md` status line).

### Files

Create `internal/cli/env.go`, `env_test.go`, `env_ca.go`,
`env_ca_test.go`, the guides `internal/cli/guides/ws-env.md`,
`ws-env-add.md`, `ws-env-list.md`, `ws-env-rm.md`, `ws-env-rotate.md`,
`ws-env-ca.md`, `ws-env-ca-rotate.md`, `ws-env-ca-status.md`, and
`docs/tools/ws-env.md`.
Change `internal/cli/errors.go`, `errors_test.go`, `app.go`, `app_test.go`,
`llm.go`, `guides/ws.md`, `docs/tools/README.md`,
`docs/adrs/0016-output-contract.md` (status line only).

### Error codes

```go
// From ADRs 0072 and 0075.
codeEnvironmentUnknown errorCode = "environment_unknown" // exit 3
codeEnvironmentExists  errorCode = "environment_exists"  // exit 4
codeEnrollmentFailed   errorCode = "enrollment_failed"   // exit 1
codeEnrollmentUsed     errorCode = "enrollment_used"     // exit 4
codeCAInUse            errorCode = "ca_in_use"           // exit 4
```

Add each to `exitCode()`'s `case`, to `TestErrorCode_exitCode`, and to the
overview's row for its exit code in `guides/ws.md` (`## Output and exit
codes`).

```go
// envError maps the environment package's errors to codes:
// ErrUnknown → environment_unknown; ErrExists → environment_exists;
// ErrNameInvalid → name_invalid; ErrCAInUse → ca_in_use;
// ErrNoCA, ErrNoRotation, config.ErrNoPublicName → usage;
// ErrEnrollmentUsed → enrollment_used; ErrEnrollmentFailed → enrollment_failed;
// anything else as it is (internal).
func envError(err error) error
```

### Commands

All under `newEnvCmd()` (`Use: "env"`, `Short: "Add, list, rotate and
remove remote environments, and rotate their CA"`), added to
`newRootCmd`'s people list after `a.newDaemonCmd()`. Each runs through
`a.withConfig`, prints JSON with `--json` and text otherwise, and every
command that changes something appends `a.reloadEnvironments(ctx)` to its
warnings. Topics added to `guideTopicsBeforeReview()` after `"daemon
stop"`: `"env", "env add", "env list", "env rm", "env rotate", "env ca",
"env ca rotate", "env ca status"`.

| Command | Args, flags | JSON | Text | Errors |
| --- | --- | --- | --- | --- |
| `ws env add <NAME>` | exactly 1 | `{"id": "env-1a2b3c4d", "name": "linux-server", "state": "pending", "code": "ws-enroll-v1.…", "ca_created": true, "warnings": []}` | the README's "Enrolling" block: `Code (works once, shown once):`, the code, then `On linux-server:` with `ws env agent enroll <code>` and `ws env agent run` | `name_invalid`, `environment_exists`, `usage` (no `public_name`) |
| `ws env list` | none | `{"environments": [{"id", "name", "state", "status", "last_seen", "created_at", "enrolled_at", "cert_not_after", "cert_ca", "hello", "workspaces"}], "warnings": []}` | a table: NAME, STATUS, WS, PLATFORM, CLAUDE, WORKSPACES, LAST SEEN | – |
| `ws env rm <NAME>` | exactly 1 | `{"id", "name", "revoked_serials": ["5e0c…"], "warnings": []}` | `Removed linux-server (env-1a2b3c4d); its certificate is revoked.` | `environment_unknown` |
| `ws env rotate <NAME>` | exactly 1 | as `add`, plus `"revoked_serials"`, without `ca_created` | as `add` | `environment_unknown`, `usage` |
| `ws env ca status` | none | `{"current": {"fingerprint", "not_after"} \| null, "old": … \| null, "environments_on_current": 3, "environments_on_old": 1, "warnings": []}` | `current CA 3b1f… expires 2027-11-22; old CA 9a0c… (1 environment still on it)` | – |
| `ws env ca rotate` | `--finish`, `--force` (only with `--finish`, else `usage`) | `{"current", "old", "dropped", "stranded": ["win-rig"], "warnings": []}` | `Rotated: new CA 3b1f…; the old CA stays trusted until ws env ca rotate --finish.` or `Finished: …` | `usage` (no CA, nothing to finish, `--force` alone), `ca_in_use` |

`ws env list` fields: `state` `pending|enrolled`; `status` from the
daemon's status (`daemon.Dial(a.daemonPaths()).Status`, never starting
one): `pending`, `online`, `offline`, or `unknown` with a warning when no
daemon runs; `last_seen` and `hello` from it (`null` when unknown);
`cert_ca` `current`, `old` or `null` (pending); `workspaces` is the length
of the `snapshot.workspaces` array in the stored snapshot, `null` without
one. Times RFC 3339 or `null`.

The code is printed once, on stdout only, and never logged (not even with
`-v`).

### Guides and `docs/tools/ws-env.md`

Follow `guides/ws-daemon.md` and its subcommand guides for structure: `#
ws env add` heading, a synopsis block starting `ws env add`, `## Flags`
(every flag, and `--json`), `## JSON output` with a ```` ```json ```` block
(every command here supports `--json`), `## Exit codes` naming only declared
codes, `## Examples`. The group guides `ws-env.md` and `ws-env-ca.md` list
their subcommands and have `## Exit codes` and `## Examples` too. The
overview `guides/ws.md` gains, in its people-commands list, a `ws llm env`
entry with indented `ws llm env add` … `ws llm env ca status` entries
(every topic as `` `ws llm env …` ``).

`docs/tools/ws-env.md` is the reference for people: every `ws env`, `ws
env ca` and `ws env agent` command (agent ones from T9's spec below), the
code's format, the files on each side, how renewal and CA rotation work, a
systemd user unit and a launchd plist example for `ws env agent run`, and
the ports to forward (8883, 8884). `docs/tools/README.md` gains a row after
`ws daemon`: `| [\`ws env\`](ws-env.md) | Enroll, list, rotate and remove
remote environments, and rotate their CA; \`ws env agent\` runs on the
remote |`. `docs/adrs/0016-output-contract.md`'s status line gains
"`enrollment_failed`, `enrollment_used`, `environment_unknown`,
`environment_exists` and `ca_in_use` added by
[0075](0075-environment-enrollment-and-certificates.md) (2026-11-22)".

### Acceptance

- `TestGuideTopics`, `TestApp_newRootCmd` (with `env` in the list) and
  `TestErrorCode_exitCode` pass with the new values.
- `ws env add linux-server --json` in a temp config dir with
  `public_name` set prints a code that `envproto.ParseCode` accepts, with
  the CA's fingerprint and `<public_name>:8884`; the record is pending; the
  stderr log at `-v` doesn't contain the code.
- Without `public_name`, `ws env add` is `usage` (exit 2) and writes nothing.
- A second `ws env add linux-server` is `environment_exists` (exit 4);
  `ws env add local` is `name_invalid` (exit 2); `ws env rm nope` is
  `environment_unknown` (exit 3).
- `ws env rotate` gives a different code and revokes an enrolled
  environment's serial (enroll it in the test through the store).
- `ws env ca rotate --finish` with an enrolled environment still on the old
  CA is `ca_in_use` (exit 4); with `--force` it succeeds and lists it in
  `stranded`; `--force` alone is `usage`.
- `ws env list --json` with no daemon gives `status: "unknown"` and a
  warning; with a stored snapshot of 2 workspaces, `workspaces: 2`.
- A mutating command with no daemon carries the "no ws daemon runs"
  warning; with a fake daemon (`a.dialDaemon` replaced or a socket server
  as in `daemon_test.go`), it calls reload.

### Verify

```sh
go test -p 2 ./internal/cli/...
make lint
```

---

## T9. `ws env agent`, `claude.Version`, state and ADR docs

- **Depends on:** T7, T8.
- **Pinned values touched:** constraints.md › `internal/cli/interfaces.go`,
  Lint rules (`exec.Command … "claude"` only in `internal/claude`), Docs
  with lists (`docs/state.md`, `docs/config.md` layout, `docs/adrs/README.md`,
  ADR status lines, `README.md` roadmap).

### Files

Create `internal/cli/env_agent.go`, `env_agent_test.go`.
Change `internal/cli/env.go` (add `a.newEnvAgentCmd()`), `app.go`,
`interfaces.go`, `moq_test.go` (generated), `internal/claude/client.go`,
`client_test.go`; docs: `docs/state.md`, `docs/config.md` (layout block),
`docs/adrs/0073-environment-agent-is-ws.md`,
`docs/adrs/0074-mqtt-transport-embedded-broker.md`,
`docs/adrs/0075-environment-enrollment-and-certificates.md`,
`docs/adrs/README.md`, `README.md`.

### Signatures

```go
// internal/claude/client.go
// Version runs claude --version once per call and returns the parsed
// version (version.String()). ErrNotInstalled when claude isn't on PATH.
func (c *Client) Version(ctx context.Context) (string, error)

// internal/cli/interfaces.go (go:generate list gains envClient)
// envClient is the agent's MQTT connection; same method set as
// envagent's mqttClient.
type envClient interface {
	Connect(ctx context.Context, opts envproto.ConnectOptions) error
	Publish(ctx context.Context, msg envproto.Message) error
	Subscribe(ctx context.Context, filter string, fn func(envproto.Message)) error
	Done() <-chan struct{}
	Close() error
}

// app.go: App gains
// newEnvClient builds the agent's MQTT client; errNoMQTTClient until T10.
newEnvClient func(log *slog.Logger) (envClient, error)

// env_agent.go
var errNoMQTTClient = errors.New("this ws was built without an MQTT client")
func (a *App) newEnvAgentCmd() *cobra.Command // Use "agent", Hidden: true
// agentSnapshots runs ws list's listing (a.lifetime.List, then
// a.listWorkspaces(entries, unknown, map)) and marshals the array.
type agentSnapshots struct{ app *App }
func (s agentSnapshots) Workspaces(ctx context.Context) (json.RawMessage, error)
// agentClaude maps claude.Client.Version: ErrNotInstalled → missing, nil
// version; any other error → present, nil version; else present, version.
type agentClaude struct{ claude *claude.Client }
func (c agentClaude) Probe(ctx context.Context) (string, *string)
```

`internal/cli` keeps its existing `claudeClient` interface; `agentClaude`
holds the concrete `*claude.Client` the App builds (`claude.New(log,
"claude", environ)`), so add a field `claudeExe *claude.Client` set in `New`
or build one in `newEnvAgentCmd`'s run function.

| Command | JSON | Text | Errors |
| --- | --- | --- | --- |
| `ws env agent enroll <CODE>` | `{"id", "name", "broker", "cert_not_after", "ca_fingerprints": [], "warnings": []}` | `Enrolled as linux-server (env-1a2b3c4d). Start it with: ws env agent run` | `usage` (`envproto.ErrCodeInvalid`), `environment_exists` (`ErrAlreadyEnrolled`), `enrollment_used`, `enrollment_failed` (`ErrPinMismatch`, `ErrEnrollmentFailed`, network errors with `details: {"cause": "…"}`) |
| `ws env agent run` | `--json` refused (`a.noJSON`) | logs JSON lines to stderr; exits 0 on SIGTERM or interrupt | `environment_unknown` (`ErrNotEnrolled`), `enrollment_failed` (`ErrCertificateExpired`), `internal` (`errNoMQTTClient`) |
| `ws env agent status` | `{"enrolled": true, "id", "name", "broker", "cert_serial", "cert_not_after", "renew_at", "ca_fingerprints": [], "warnings": []}`; not enrolled: `enrolled: false`, the others `null`, `ca_fingerprints: []` | `linux-server (env-1a2b3c4d) → ws.example.com:8883; certificate renews from 2027-01-21` | – |
| `ws env agent unenroll` | `{"id", "name", "unenrolled": true, "warnings": []}` | `Unenrolled linux-server; the controller lists it until ws env rm linux-server.` | `environment_unknown` |

`run` builds `envagent.NewAgent(log, envagent.NewFiles(a.paths.Environment),
client, agentSnapshots{a}, agentClaude{…}, rand.Reader,
envagent.DefaultOptions(a.version, a.config.Environments.RenewDays()))`
under `signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)`, logging
like `runDaemon` (JSON handler on stderr).

### Docs

- `docs/state.md` `## Layout`: rows for `environments/ca/` (the four
  files, modes, written by `ws env add` and `ws env ca rotate`, the server
  pair by the daemon), `environments/<id>.toml` (fields as in T3),
  `environments/revoked`, `environments/.lock`,
  `state/.environments/<name>/snapshot.json`, and on an agent
  `environment/` (`agent.toml`, `key.pem`, `cert.pem`, `ca.pem`); a
  `## Remote environments` section with the record's fields and states.
- `docs/config.md` `### Layout`: `environments/` and `environment/` lines.
- ADR 0073, 0074, 0075: `Status: Accepted (2026-11-22)`, plus an
  "Amended" note in 0074 for the `up` topic (add the row
  `env/<id>/up | agent | controller | agent-initiated requests
  (cert.renew), QoS 1, with a response topic` and "and `up`" in the ACL
  paragraph) and in 0075 for decisions 2-5 above (ca.update, the agent's
  own `renew_before_days`, record states, the three extra error codes).
  `docs/adrs/README.md`: their rows say `Accepted`.
- `README.md` `## Roadmap`: an unchecked entry "Remote environments, core:
  `ws env`, enrollment, the CA, the broker in the daemon
  ([0073](docs/adrs/0073-environment-agent-is-ws.md),
  [0074](docs/adrs/0074-mqtt-transport-embedded-broker.md),
  [0075](docs/adrs/0075-environment-enrollment-and-certificates.md))",
  checked by T10 when the broker lands.

### Acceptance

- `ws env agent` doesn't appear in `ws --help` or `ws llm --all`, and
  `TestGuideTopics` still passes with no guide for it.
- `enroll` against an in-process controller (store + `envtest.Broker`,
  `127.0.0.1:0`, the code from `ws env add`'s store call) writes the files
  and prints the JSON; enrolling again is `environment_exists` (exit 4); a
  used code from fresh config is `enrollment_used` (exit 4); a malformed
  code is `usage` (exit 2).
- `run` with the default `newEnvClient` exits `internal` with
  `errNoMQTTClient`'s text; with `newEnvClient` returning an
  `envtest.Client`, it connects, the controller sees it online, and
  cancelling ctx exits 0.
- `status` and `unenroll` give the shapes above; `unenroll` twice is
  `environment_unknown`.
- `claude.Version` returns the parsed version with the fake claude the
  package's tests already use, and `ErrNotInstalled` without one.

### Verify

```sh
go generate ./internal/cli/
go test -p 2 ./internal/claude/...
go test -p 2 ./internal/cli/...
make lint
```

---

## T10. Broker adapter and paho client (needs the spike)

- **Depends on:** T9, and `docs/design/remote-environments/broker-spike.md`
  (branch `feat/env-core-spike`). This is the only task that needs the
  spike. **Use the module, version and hook names in broker-spike.md.**
  If the spike says a required item below can't be done with its module,
  stop and report `FAIL` with that item.
- **Pinned values touched:** constraints.md › Lint rules (two new
  `depguard` rules), `go.mod`/`go.sum`, `README.md` roadmap checkbox.

### Files

Create `internal/mqttbroker/broker.go`, `broker_test.go`,
`conformance_test.go` (use the spike's package name if it gives one) and
`internal/mqttclient/client.go`, `client_test.go`.
Change `go.mod`, `go.sum`, `.golangci.yml` (depguard), `internal/cli/app.go`
(the defaults of `newEnvBroker` and `newEnvClient`), `README.md` (check the
roadmap entry).

### The broker adapter must implement

`environment`'s `broker` / the cli's `envBroker` method set (T4, T6):

1. `Start(ctx, addr, tlsConfig, rules) (string, error)`: one TLS listener
   (`net.ListenConfig.Listen` then the module's TLS listener, or a
   `tls.NewListener` it accepts) using `tlsConfig` as given (it requires
   and verifies client certificates through `GetConfigForClient`); returns
   the bound address; no other listener (no plain TCP, no websocket).
2. On connect, through the module's connect/auth hook: read the verified
   `tls.ConnectionState`, build `envproto.PeerFromState`, set `ClientID`
   from the CONNECT packet, call `rules.Connect`, refuse on error (CONNACK
   "not authorized"). Username and password are ignored.
3. Publish and subscribe ACL hooks calling `rules.Allow(peer,
   AccessPublish|AccessSubscribe, topic)`; a refusal **disconnects** the
   client and logs `"acl violation"` with the client id and topic.
4. `Publish`/`Subscribe` as the in-process controller (the module's inline
   client or equivalent), not checked by `rules`, with MQTT 5 Response
   Topic, Correlation Data, Message Expiry and Retain passed through both
   ways.
5. `Clients()` and `Disconnect(serial)` from the adapter's own map of
   connection → `Peer`, kept by the connect and disconnect hooks;
   `Disconnect` publishes the will (server-initiated disconnect).
6. Session expiry 0 for every client (clean start), retained messages,
   Last Will with Will Delay, maximum packet size `MaxRequestBytes` + 16
   KiB, and `Close` shutting the listener and every connection.

### The paho client must implement

`envagent`'s `mqttClient` / the cli's `envClient` with
`github.com/eclipse/paho.golang` (the version broker-spike.md names, else
v0.23.0), its `paho` package (not `autopaho`: the agent reconnects itself):
`Connect` dials with `tls.Dialer.DialContext(ctx, "tcp", opts.Address)`
using `opts.TLS`, sends CONNECT with `ClientID`, `KeepAlive` in seconds,
`CleanStart` true, Session Expiry 0, the will and `WillDelayInterval`;
`Publish` and `Subscribe` map `envproto.Message` both ways (QoS, Retain,
Response Topic, Correlation Data, Message Expiry); `Done` closes on a
client error or a server DISCONNECT; `Close` sends a normal DISCONNECT (no
will). `connectPacket(opts envproto.ConnectOptions) *paho.Connect` and
`toPublish`/`fromPublish` are pure and unit-tested.

### Lint

`.golangci.yml` `depguard` rules: the broker module only in
`**/internal/mqttbroker/**`; `github.com/eclipse/paho.golang` only in
`**/internal/mqttclient/**` and `**/internal/mqttbroker/*_test.go` (copy the
`cobra` rule's shape).

### Acceptance (`conformance_test.go`: the real broker on `127.0.0.1:0`, the real controller, store and agent with the paho client; ADR 0074 conformance)

- A client without a certificate, and one from another CA, can't connect.
- An enrolled agent connects; the controller sees it online, then a snapshot.
- An agent publishing to another environment's topic is disconnected.
- Presence after a killed connection: closing the agent's TCP connection
  without DISCONNECT turns it `offline` after the will delay (agent
  `WillDelay` 1 s in the test).
- A revoked serial is refused and disconnected (`store.Remove` +
  `Reload`).
- **(a), end to end**: `RotateCA` + `Reload` keeps the agent connected; it
  receives `ca.update`; after `Disconnect(serial)` it reconnects against the
  new server certificate.
- **(b)/(e), end to end**: an old-CA agent connects until `FinishCA(true)` +
  `Reload`, then is refused.
- `cert.renew` round-trips over real MQTT 5 (Response Topic and
  Correlation Data intact).
- Unit: `connectPacket` sets Clean Start, Session Expiry 0, keep-alive, will
  and Will Delay; `toPublish`/`fromPublish` keep every property.
- `ws daemon run` with a CA and `public_name 127.0.0.1` now reports
  `listening: true` without a test override.
- Deferred to PR 3 and 4 (not tested here): request expiry
  (`environment_offline`), stream gaps and redraw.

### Verify

```sh
go mod tidy
go build ./...
go test -p 2 ./internal/mqttclient/... ./internal/mqttbroker/...
go test -p 2 ./internal/environment/... ./internal/envagent/... ./internal/cli/...
make lint
```
