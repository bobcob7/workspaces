# Tasks: artifacts

Builds [ADR 0078](../../adrs/0078-artifacts.md) and
[ADR 0079](../../adrs/0079-artifact-comments-and-anchors.md) as specified
in [README.md](README.md). **Read first:** README.md (commands, JSON
shapes, limits, layout, UI) and [constraints.md](constraints.md) (pinned
values `C1`-`C9` and what the pod can't run). Every task names the rows it
touches.

**Not in these tasks:** reaching the store from a remote environment, PDF,
image region comments, fuzzy re-anchoring, notifications, expiry, editing
content in the desktop app, references in loop prompts.

## Rules for every task

- **Go standards** (Agent Handbook › Language Standards › Go): `gofumpt`
  formatting through `make fmt`; **no blank lines inside function bodies**
  (enforced by `internal/blanklines` in `make lint`); early returns;
  functions under ~40 lines; `context.Context` first; no `init()` and no
  package-level mutable state; every exported name has a godoc comment
  ending in a period; errors wrapped as `fmt.Errorf("reading %s: %w", …)`,
  matched with `errors.Is`/`errors.As`; unexported sentinels unless another
  package matches them; `encoding/json/v2` in new packages (`internal/artifact`),
  the existing `encoding/json` in `internal/cli`.
- **Tests** (Handbook › Go: Testing): one `_test.go` per source file;
  `Test<Receiver>_<Method>` (or `Test<Func>`) with `t.Run` subtests per
  behaviour; tables only for 3+ simple input→output cases; testify
  `require` for preconditions, `assert` for checks; `t.Context()`,
  `t.TempDir()`; `t.Parallel()` only on tests that touch the filesystem,
  network or processes; mocks only from moq.
- **git, claude and gh run only through `internal/git`, `internal/claude`
  and `internal/gh`** (`make lint` greps for it). These tasks run none.
- `make lint` must pass. Run it once, last.
- Output JSON: `snake_case`, RFC 3339 UTC to the second, `null` for no
  value (never a missing field), `[]` for an empty list, `warnings` on
  every top-level object.
- Verify with exactly the commands listed, one at a time; `go test` with
  `-p 2`. Commit and push after each file you finish and before the
  slowest suite. Never wait on a permission prompt: if a command is
  refused, try another way or list the check as unverified in your report,
  and say so if a command was killed rather than claiming it passed.
- Commit messages: imperative, conventional prefix (`feat:`, `test:`,
  `docs:`), first line under 72 characters.

## Order

```
T1 ──┬── T2 ──┐
     └── T3 ──┴── T4 ──┬── T5 (docs)
                       └── T6 (desktop)
```

| Task | Depends on | Parallel with | Size |
| --- | --- | --- | --- |
| T1 `internal/artifact`: ids, references, sniffing, the store | – | – | |
| T2 Comments and anchors in `internal/artifact` | T1 | T2 ∥ T3 | |
| T3 `ws machine artifact` and hidden `ws artifact` (no comments), error codes, guides | T1 | T3 ∥ T2 | |
| T4 Comment commands, `@artifact` hints in prompts, the `ws llm` rule | T2, T3 | – | |
| T5 Docs | T4 | T5 ∥ T6 | |
| T6 Desktop and Workspace Online: commands, Artifacts section, viewers, comments, prompt attachments, mock | T4 | T6 ∥ T5 | largest; split point marked |

No task is `small`. T2 and T3 touch disjoint files (`internal/artifact`
vs `internal/cli`, `internal/config`, `internal/agent`, `docs/adrs/0066`);
T5 and T6 too (`docs/`, `README.md` vs `desktop/`).

---

## T1. `internal/artifact`: ids, references, sniffing, the store

- **Depends on:** nothing.
- **Constraints:** C5 (lint), C9 (pod).

### Files

Create, each with its `_test.go`:

- `internal/artifact/artifact.go`: package doc, types, limits, errors.
- `internal/artifact/ref.go`: ids and `@artifact:` references, the hint line.
- `internal/artifact/sniff.go`: kind and content type from name and bytes.
- `internal/artifact/files.go`: atomic writes, record and comments file I/O.
- `internal/artifact/store.go`: `Store` and its methods.

No other file changes.

### `artifact.go`

```go
// Package artifact stores files that people and agents share, with their
// revisions and comment threads (ADRs 0078, 0079).
package artifact

// Kind is what an artifact holds, decided from its bytes.
type Kind string

const (
	KindImage    Kind = "image"
	KindMarkdown Kind = "markdown"
	KindText     Kind = "text"
)

// Limits (README.md › Kinds, types and limits).
const (
	MaxImageSize     = 10 << 20
	MaxTextSize      = 1 << 20 // markdown, text and SVG
	MaxRevisions     = 100
	MaxStoreBytes    = 2 << 30
	MaxThreads       = 200
	MaxComments      = 50
	MaxNameRunes     = 128
	MaxLineRunes     = 200 // title, revision note
	MaxBodyRunes     = 10_000
	MaxQuoteRunes    = 2_000
	ContextRunes     = 32
	FormatVersion    = 1
)

var (
	ErrInvalid   = errors.New("invalid artifact")        // -> artifact_invalid
	ErrNotFound  = errors.New("no such artifact")        // -> artifact_unknown
	ErrConflict  = errors.New("artifact changed")        // -> artifact_conflict
	ErrFull      = errors.New("artifact limit reached")  // -> artifact_full
	ErrNewer     = errors.New("written by a newer ws")   // -> artifact_invalid
)

// ConflictError is ErrConflict with the latest revision, for details.
type ConflictError struct{ Latest int }
func (e *ConflictError) Error() string   // "the latest revision is r<n>, not r<base>"; keep base in the struct too if you like
func (e *ConflictError) Is(target error) bool // target == ErrConflict

// Actor is who created a revision or wrote a comment.
type Actor struct {
	Kind      string  `json:"kind"`      // "person" or "agent"
	Workspace *string `json:"workspace"`
	Name      *string `json:"name"`
}

// Person is the actor for every people command and the desktop app.
func Person() Actor
// Agent is the actor for a ws machine command run in workspace id.
func Agent(workspace, name string) Actor

// Revision is one immutable version of an artifact's bytes.
type Revision struct {
	Rev         int       `json:"rev"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256    string    `json:"sha256"` // lowercase hex
	CreatedAt time.Time `json:"created_at"`
	CreatedBy Actor     `json:"created_by"`
	Note      *string   `json:"note"`
}

// Record is artifact.json.
type Record struct {
	Version     int        `json:"version"`
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Title       *string    `json:"title"`
	Kind        Kind       `json:"kind"`
	ContentType string     `json:"content_type"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   Actor      `json:"created_by"`
	Root        *string    `json:"root"`
	Revisions   []Revision `json:"revisions"`
}

// Latest is the newest revision. A stored record always has one.
func (r *Record) Latest() Revision

// ThreadCounts summarises a record's threads (README.md › Artifact summary).
type ThreadCounts struct {
	Open     int `json:"open"`
	Resolved int `json:"resolved"`
	Detached int `json:"detached"`
}

// Anchor, Comment and Thread are comments.json's shapes (ADR 0079); T1
// only reads and counts them, T2 writes them.
type Anchor struct {
	Rev       int    `json:"rev"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Quote     string `json:"quote"`
	Prefix    string `json:"prefix"`
	Suffix    string `json:"suffix"`
	State     string `json:"state"` // "anchored" or "detached"
}
type Comment struct {
	ID        string    `json:"id"`
	Author    Actor     `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}
type Thread struct {
	ID         string     `json:"id"`
	Anchor     *Anchor    `json:"anchor"`
	Resolved   bool       `json:"resolved"`
	ResolvedAt *time.Time `json:"resolved_at"`
	ResolvedBy *Actor     `json:"resolved_by"`
	Comments   []Comment  `json:"comments"`
}

// Counts is the thread counts of threads.
func Counts(threads []Thread) ThreadCounts
```

All times are stored `UTC().Truncate(time.Second)`.

### `ref.go`

```go
// NewID is "art-" and 8 lowercase hex digits from random.
func NewID(random io.Reader) (string, error)
// ValidID reports whether id matches ^art-[0-9a-f]{8}$.
func ValidID(id string) bool
// ParseRef accepts "art-…", "@artifact:art-…" and "@artifact:art-…/r<n>";
// rev is 0 when no /r<n> is given. Anything else is ErrInvalid.
func ParseRef(s string) (id string, rev int, err error)
// FindRefs is the distinct artifact ids referenced in text, in first-seen
// order (regexp `@artifact:(art-[0-9a-f]{8})(?:/r([1-9][0-9]{0,2}))?`).
func FindRefs(text string) []string
// HintLine is `(Artifacts <id>, <id>: run "ws machine artifact get <id>" for each, then read the file at the "path" it prints.)`.
func HintLine(ids []string) string
// AppendHint returns prompt with "\n\n" and HintLine appended when it has
// references and doesn't already contain `run "ws machine artifact get <id>"`;
// otherwise prompt unchanged. ids is FindRefs(prompt).
func AppendHint(prompt string) (string, []string)
```

The pattern is a package-level `regexp.MustCompile` (allowed by
`gochecknoglobals`).

### `sniff.go`

```go
// Detect returns the kind and content type of data named name, or
// ErrInvalid (wrapped, with a reason a person can act on) when the type
// isn't allowed, data is empty, or it is over its kind's limit.
func Detect(name string, data []byte) (Kind, string, error)
// CheckName is nil for a valid artifact name (1-128 runes, one path
// element, no '/', '\\', control characters or leading '.').
func CheckName(name string) error
// CheckLine is nil for a title or note: up to MaxLineRunes, no newline or
// control character. Empty is allowed.
func CheckLine(what, s string) error
```

Order in `Detect`: empty → invalid; `http.DetectContentType(data)` in
`image/png|jpeg|gif|webp` → image (limit 10 MiB); else if not
`utf8.Valid` or contains `0x00` → invalid ("only PNG, JPEG, GIF, WebP,
SVG, Markdown and UTF-8 text are allowed"); name ends `.svg` (case
insensitive) → must pass `looksLikeSVG` (skip leading whitespace, `<?xml
…?>`, `<!-- … -->` and `<!DOCTYPE …>`; next must be `<svg` followed by
whitespace, `>` or `/`) → `image`, `image/svg+xml`, else invalid; name
ends `.md`/`.markdown` → markdown; else text. Text kinds and SVG: limit
1 MiB.

Tests (table, by name and bytes): each image magic; SVG with prolog,
comment and doctype; `.svg` that isn't SVG; Markdown; plain text; HTML
(text); empty; NUL byte; invalid UTF-8; PNG of 10 MiB + 1 byte; text of
1 MiB + 1 byte.

### `files.go`

```go
// writeAtomic writes data to a new file in tmpDir, syncs it, sets mode
// 0600, and renames it to path.
func writeAtomic(tmpDir, path string, data []byte) error
// readRecord reads <dir>/artifact.json; a version above FormatVersion is
// ErrNewer, bad JSON ErrInvalid, both wrapped with the path.
func readRecord(dir string) (Record, error)
// readThreads reads <dir>/comments.json; a missing file is no threads.
func readThreads(dir string) ([]Thread, error)
// writeRecord, writeThreads marshal with encoding/json/v2 and writeAtomic.
func writeRecord(tmpDir, dir string, r *Record) error
func writeThreads(tmpDir, dir string, threads []Thread) error
```

`comments.json` is `{"version": 1, "threads": [...]}`.

### `store.go`

```go
// Store is CONFIG_DIR/artifacts (config.Paths.Artifacts).
type Store struct { /* dir string; random io.Reader; now func() time.Time */ }

// NewStore returns a Store over dir. random is crypto/rand.Reader in ws.
func NewStore(dir string, random io.Reader, now func() time.Time) *Store

// NewArtifact is what Add stores.
type NewArtifact struct {
	Name  string
	Title string // "" for none
	Note  string // "" for none
	Data  []byte
	By    Actor
	Root  string // the creating agent's tree root; "" for a person
}

// NewRevision is what Put appends.
type NewRevision struct {
	Data    []byte
	Note    string
	By      Actor
	BaseRev int // 0: append whatever the latest is
}

// PutResult is Put's record and how many threads were anchored and
// detached after moving anchors (both 0 until T2).
type PutResult struct {
	Record   Record
	Anchored int
	Detached int
}

// Entry is one artifact in List, with its thread counts.
type Entry struct {
	Record  Record
	Threads ThreadCounts
}

func (s *Store) Add(ctx context.Context, n *NewArtifact) (Record, error)
func (s *Store) Put(ctx context.Context, id string, n *NewRevision) (PutResult, error)
// Show is the record and its thread counts.
func (s *Store) Show(id string) (Entry, error)
// Read is the record, revision rev (0 = latest) and its bytes.
func (s *Store) Read(id string, rev int) (Record, Revision, []byte, error)
// List is every artifact, latest revision's created_at first (ties by
// id); an artifact whose record can't be read is skipped and named in
// the warnings.
func (s *Store) List() ([]Entry, []string, error)
// Remove deletes the artifact (README.md › On disk: rename into .tmp,
// then RemoveAll).
func (s *Store) Remove(ctx context.Context, id string) error
// Threads is the artifact's threads (for T2 and the CLI).
func (s *Store) Threads(id string) ([]Thread, error)
```

Behaviour:

- Every method checks `ValidID(id)` first (`ErrNotFound` when invalid or
  missing). `Read` with a rev that doesn't exist is `ErrNotFound`.
- `Add`: `CheckName`, `CheckLine` title and note, `Detect`; quota: sum of
  every record's revision sizes plus `len(Data)` over `MaxStoreBytes` is
  `ErrFull`. `MkdirAll(dir, 0o700)` and `.tmp/`; draw an id, `os.Mkdir`
  (`0o700`), retry up to 5 times on `fs.ErrExist`. Take
  `lockfile.Exclusive(ctx, <id>/.lock)`; write `r1` then `artifact.json`.
  On a failure after the `Mkdir`, `RemoveAll` the directory.
- `Put`: lock; read the record; `BaseRev != 0 && BaseRev != latest` →
  `&ConflictError{Latest: latest}`; `len(Revisions) == MaxRevisions` →
  `ErrFull`; `Detect(record.Name, data)` must give the record's kind
  (else `ErrInvalid`, "r<n> must be <kind> like the artifact"); quota as
  above; write `r<n+1>`, then the record. `Anchored`/`Detached` are 0 in
  T1 (T2 fills them).
- Every write first deletes files in `.tmp/` older than one hour (ignore
  errors).
- Each revision records its own `ContentType` (a PNG artifact may get a
  JPEG revision; the kind can't change). The record's `ContentType` is
  always the latest revision's; `Put` updates it.

Tests (`store_test.go`, `t.Parallel()` allowed: filesystem): Add then Show
round trip with `Root` and `Person()`; ids from a fixed `bytes.Reader`
collide once and retry; Put appends r2 with the right sha256 and size;
Put with a stale BaseRev is `ErrConflict` and `errors.As` gives `Latest`;
Put of a different kind is `ErrInvalid`; the 101st revision is `ErrFull`;
the quota (use a test-only smaller limit via an unexported field
`maxStoreBytes` set by `NewStore` to `MaxStoreBytes`) is `ErrFull`; Read
r1 after r2 returns r1's bytes; List orders by latest revision time and
warns about a record with `"version": 2`; Remove then Show is
`ErrNotFound`; files are 0600 and directories 0700.

### Verify

```
go test -p 2 -race ./internal/artifact/...
make lint
```

### Acceptance

- The verify commands pass; `go test -cover ./internal/artifact/` above 85%.
- Nothing outside `internal/artifact/` changed.

---

## T2. Comments and anchors in `internal/artifact`

- **Depends on:** T1. **Parallel with:** T3.
- **Constraints:** C5, C9.

### Files

Create `internal/artifact/anchor.go`, `anchor_test.go`,
`internal/artifact/comments.go`, `comments_test.go`. Change
`internal/artifact/store.go` (Put moves anchors) and `store_test.go`.

### `anchor.go`

```go
// ErrAnchorNotFound is a quote that isn't in the text, or isn't unique.
var ErrAnchorNotFound = errors.New("quote not found")

// AnchorError is ErrAnchorNotFound with the occurrences, for details.
type AnchorError struct {
	Occurrences int
	Lines       []int // the 1-based line each occurrence starts on
}
func (e *AnchorError) Error() string // "the quote occurs 3 times, on lines 4, 9, 12; pass --line" / "the quote isn't in the latest revision"
func (e *AnchorError) Is(target error) bool

// AnchorRequest is how a caller names the text a new thread is about.
type AnchorRequest struct {
	Quote  string
	Line   int    // 0: any
	Prefix string // context before, from the desktop; may be ""
	Suffix string
}

// Locate anchors req in text, revision rev (ADR 0079 › Making an anchor):
// the only occurrence; else the one starting on req.Line; else the one
// with the strictly highest context score; else *AnchorError.
func Locate(text string, rev int, req AnchorRequest) (Anchor, error)

// Move re-anchors a into text, revision rev (ADR 0079 › New revisions
// move anchors). ok is false when the quote is gone; the caller then
// keeps a with State "detached".
func Move(text string, rev int, a Anchor) (moved Anchor, ok bool)
```

Helpers (unexported, each small and table-tested):

- `occurrences(text, quote string) []int`: every byte offset where
  `quote` starts, overlapping allowed (`strings.Index` in a loop advancing
  one rune).
- `normalizedOccurrences(text, quote string) [][2]int`: collapse every run
  of Unicode whitespace to one space in both, search, and map each match
  back to `[start, end)` byte offsets in `text`.
- `contextScore(text string, start, end int, prefix, suffix string) int`:
  runes of the common suffix of `prefix` and `text[:start]` plus runes of
  the common prefix of `suffix` and `text[end:]`.
- `lineAt(text string, offset int) int`: 1 + count of `\n` before offset.
- `contextAround(text string, start, end int) (prefix, suffix string)`: up
  to `ContextRunes` runes before and after.
- `anchorAt(text string, rev, start, end int) Anchor`: fills every field,
  `State: "anchored"`, `LineEnd` = `lineAt(text, end-1)` (or `LineStart`
  when empty).

`Locate` checks the quote (`ErrInvalid` when empty, only whitespace, or
over `MaxQuoteRunes`). `Move`'s tie-break after the context score: the
occurrence whose `lineAt` is closest to `a.LineStart`, then the earliest.

Tests (`anchor_test.go`): unique quote; quote twice picked by `Line`;
twice picked by prefix; twice with equal scores → `*AnchorError` with
`Occurrences: 2` and both lines; missing → `Occurrences: 0`; multi-byte
text (é, emoji) gives byte offsets that slice back to the quote and
prefix/suffix of at most 32 runes; Move after an unrelated edit above
(offsets shift, lines shift, same quote); Move after the quote's own text
changed → `ok == false`; Move with whitespace reflowed (a line break
inserted inside the quote) still finds it through normalisation; Move with
two occurrences picks by context, then by line.

### `comments.go`

```go
// NewThread is a thread's first comment and its optional anchor.
type NewThread struct {
	Body   string
	By     Actor
	Anchor *AnchorRequest // nil: about the whole artifact
}

// AddThread anchors (on the latest revision's text) and appends a thread
// t<len+1> with comment c1. An anchor on an image is ErrInvalid.
func (s *Store) AddThread(ctx context.Context, id string, n *NewThread) (Thread, Comment, error)
// Reply appends comment c<len+1> to thread threadID ("t3").
func (s *Store) Reply(ctx context.Context, id, threadID, body string, by Actor) (Thread, Comment, error)
// Resolve sets or, with reopen, clears resolved, resolved_at and resolved_by.
func (s *Store) Resolve(ctx context.Context, id, threadID string, by Actor, reopen bool) (Thread, error)
```

All three hold the artifact's lock for read-modify-write of
`comments.json`. Body: 1-`MaxBodyRunes` runes, not only whitespace
(`ErrInvalid`). Unknown thread id → `ErrNotFound` ("no thread t9 on
art-…"). `MaxThreads` / `MaxComments` → `ErrFull`. Resolving a resolved
thread (or reopening an open one) is not an error: it returns the thread
unchanged.

### `store.go` change

`Put`, after writing the new revision and record and still under the
lock: read threads; for each thread with an anchor, `Move(newText,
newRev, *anchor)`; moved → replace, count `Anchored`; not found → keep
the old anchor with `State = "detached"`, count `Detached`; write
`comments.json` only when there were threads. Only `markdown` and `text`
artifacts have anchors; skip for images. Also: when a thread's anchor is
already detached, still try `Move` (the quote may be back).

Also in `Show`/`List`/`Threads`, set every anchor's `State` from
`anchor.Rev == record.Latest().Rev` when reading, so a stale file can't
say `anchored` on an old revision.

### Verify

```
go test -p 2 -race ./internal/artifact/...
make lint
```

### Acceptance

- The tests above pass; `Locate` and `Move` are deterministic (run the
  table twice in one test and compare).
- `go test -cover ./internal/artifact/` above 85%.
- Nothing outside `internal/artifact/` changed.

---

## T3. `ws machine artifact` and hidden `ws artifact` (no comments), error codes, guides

- **Depends on:** T1. **Parallel with:** T2.
- **Constraints:** C1, C2, C3, C4, C5, C9.

### Files

Create:

- `internal/cli/artifact.go`, `artifact_test.go`: the shared JSON types,
  error mapping, input reading, the store, and the people command
  `ws artifact` (`add`, `list`, `show`, `cat`, `rm`).
- `internal/cli/machine_artifact.go`, `machine_artifact_test.go`: `ws
  machine artifact` (`add`, `list`, `show`, `get`, `put`, `rm`).
- Guides (C2), each in the house format (copy `ws-machine-progress-step.md`'s
  sections: title, synopsis block, description, `## Options` table,
  `## JSON output`, `## Exit codes`, `## Examples`):
  `internal/cli/guides/ws-machine-artifact.md` (the lifecycle text from
  README.md › Guides and the synopsis of the six T3 subcommands; T4 adds
  the `comment` lines),
  `ws-machine-artifact-add.md`, `-list.md`, `-show.md`, `-get.md`,
  `-put.md`, `-rm.md`.

Change: `internal/cli/errors.go`, `errors_test.go` (C1);
`internal/cli/app.go`, `app_test.go`, `machine.go`, `llm.go`,
`guides/ws.md`, `guides/ws-machine.md` (C2); `internal/config/paths.go`,
`paths_test.go` (C3); `internal/agent/tools/guard.go`, `guard_test.go`,
`internal/agent/preamble.md`, `docs/adrs/0066-runner-security-boundaries.md`
(C4); `docs/adrs/0016-output-contract.md` (C1, for the six T3 codes;
T4 adds `anchor_not_found`).

### Error codes (C1)

```go
	// ADR 0078: artifacts.
	codeArtifactInvalid     errorCode = "artifact_invalid"     // exit 2
	codeArtifactUnknown     errorCode = "artifact_unknown"     // exit 3
	codeArtifactConflict    errorCode = "artifact_conflict"    // exit 4
	codeArtifactFull        errorCode = "artifact_full"        // exit 4
	codeArtifactForbidden   errorCode = "artifact_forbidden"   // exit 4
	codeArtifactUnreachable errorCode = "artifact_unreachable" // exit 1
```

Add each to `exitCode()`, `known()`, `TestErrorCode_exitCode`, and the
overview's exit table in `guides/ws.md`.

### `artifact.go` (shared)

```go
// actorJSON, artifactSummaryJSON, revisionJSON, artifactJSON mirror
// README.md › JSON shapes.
type artifactSummaryJSON struct {
	ID          string              `json:"id"`
	Reference   string              `json:"reference"`
	Name        string              `json:"name"`
	Title       *string             `json:"title"`
	Kind        string              `json:"kind"`
	ContentType string              `json:"content_type"`
	Latest      int                 `json:"latest"`
	Size        int64               `json:"size"`
	CreatedAt   string              `json:"created_at"`
	UpdatedAt   string              `json:"updated_at"`
	CreatedBy   artifact.Actor      `json:"created_by"`
	Threads     artifact.ThreadCounts `json:"threads"`
}
type artifactJSON struct {
	artifactSummaryJSON
	Revisions []revisionJSON `json:"revisions"`
}

func summaryJSON(e *artifact.Entry) artifactSummaryJSON
func fullJSON(e *artifact.Entry) artifactJSON

// artifactStore is the store over a.paths.Artifacts, crypto/rand and a.now.
func (a *App) artifactStore() *artifact.Store

// artifactError maps the store's errors to codes: ErrInvalid, ErrNewer ->
// artifact_invalid; ErrNotFound -> artifact_unknown; ErrConflict ->
// artifact_conflict with details {"latest": n}; ErrFull -> artifact_full.
// Anything else is returned as is (internal).
func artifactError(err error) error

// readArtifactInput reads path, or a.stdin for "-". name is --name, else
// filepath.Base(path); "-" without --name is usage. A read over
// MaxImageSize+1 bytes stops early and is artifact_invalid.
func (a *App) readArtifactInput(path, name string) (string, []byte, error)

// artifactRef parses an <ID> argument with --rev: a reference's /r<n> and a
// different --rev is usage.
func artifactRef(arg string, rev int, revSet bool) (id string, r int, err error)
```

Details go in `cliError.details` (a `map[string]any`), as
`internal/cli/env_call.go` sets `details["remote_code"]`; use the same for
`{"latest": n}`.

### People: `ws artifact` (hidden)

`newArtifactCmd()` with `Hidden: true`, `Use: "artifact <OP>"`, `Short:
"Share files with agents, for people and the desktop app"`, `Args:
knownOperation`, subcommands `add`, `list`, `show`, `cat`, `rm` with the
flags of README.md › `ws artifact`. Added to the people slice in
`newRootCmd` after `a.newEnvCmd()` (C2). The actor is `artifact.Person()`,
`Root` "". Without `--json`, print one line per artifact (`<id>  <kind>
<size>  <title or name>`) for `list`, and the id for `add`; `cat` always
writes raw bytes to `a.stdout`. `rm` may remove anything; its JSON is
`{"artifacts": [{"id", "removed", "error"}], "warnings": []}` and the exit
code is the first failure's (follow `ws rm`'s multi-item pattern in
`internal/cli/rm.go`). `list --workspace <id>` keeps entries whose
`created_by.workspace` or `root` equals `<id>`. `--limit` 1-1000, default
100 (`usage` otherwise).

### Agents: `ws machine artifact`

`newMachineArtifactCmd()` (added last in `newMachineCmd`, C2), subcommands
`add`, `list`, `show`, `get`, `put`, `rm`, flags as README.md says. Each
`RunE` first:

1. `caller, err := a.caller()` (`outside_workspace`);
2. `a.refuseOnAgentMachine()`: when `filepath.Join(a.paths.Environment,
   "agent.toml")` exists (`os.Stat` succeeds; ADR 0078 says enrolled means
   that file exists; don't call `envagent.Files.Load`, which also needs a
   valid key and certificate) → `newError(codeArtifactUnreachable,
   errors.New("artifacts live on the machine this environment is enrolled
   with, and this ws can't reach them yet (ADR 0078)"))`; otherwise
   continue.
3. The actor: `artifact.Agent(caller, name)`, where `name` is the
   caller's `Workspace.Name` from `a.workspaces.List()` (the id when it is
   empty); the root: `machine.Root(list, caller)` from the same list, as
   `progressCaller` in `machine_progress.go` does.

- `add`: `Root` = the caller's root. Prints `{"artifact": <artifact>, "warnings": []}`.
- `list --mine`: keep entries whose `Root` equals the caller's root.
- `get <ID> [--rev n] [--out dir]`: `dir` = `--out` (made absolute against
  the working directory) or `<workspace path>/.ws-artifacts/<id>/r<n>`;
  `MkdirAll(dir, 0o700)`; write `<dir>/<record.Name>` with mode 0600
  through a temp file in `dir` renamed into place (overwrite). Print
  `{"artifact": <summary>, "rev": n, "path": "<abs>", "content_type":
  "…", "size": n, "warnings": []}`.
- `put <ID> <PATH|-> [--base-rev n] [--note t]`: prints `{"artifact":
  <artifact>, "anchors": {"anchored": n, "detached": n}, "warnings": []}`.
- `rm <ID>...`: each target's record `Root` must equal the caller's root,
  else that item's error is `artifact_forbidden` ("art-… was created by
  another workspace tree; ask a person to remove it"); same multi-item
  JSON as people `rm`.

### Guard and preamble (C4)

`isAllowedWS`: also allow `machine artifact` followed by one of `add list
show get put rm`, and `machine artifact comment` followed by one of `list
add resolve` (T4 adds the commands; allowing them now is harmless).
Update the refusal message and the preamble line, and ADR 0066 (one line,
see C4).

### Tests

`machine_artifact_test.go` (follow `machine_progress_test.go`'s setup:
a test app over `t.TempDir()` with a workspace created inside it):

- `add` a PNG from a file and from stdin (`-` with `--name`); `-` without
  `--name` is `usage`; a `.exe`-like binary is `artifact_invalid` exit 2.
- `list` shows it; `--mine` hides an artifact added by `ws artifact add`.
- `show` an unknown id is `artifact_unknown` exit 3; a reference
  `@artifact:<id>/r1` is accepted; `@artifact:<id>/r1 --rev 2` is `usage`.
- `get` writes `<ws>/.ws-artifacts/<id>/r1/<name>` with the same bytes
  and prints the absolute path; `--out` writes there.
- `put --base-rev 1` after another `put` is `artifact_conflict` exit 4
  with `details.latest == 2`.
- `rm` of a person's artifact is `artifact_forbidden` exit 4; of its own
  succeeds.
- Outside a workspace: `outside_workspace`. With any file written at
  `<CONFIG_DIR>/environment/agent.toml` first: every subcommand is
  `artifact_unreachable` exit 1.

`artifact_test.go`: people `add`/`list --workspace`/`show`/`cat` (raw
bytes, also with `--json`)/`rm`; `ws artifact` is hidden
(`app_test.go` subtest, C2).

### Verify

```
go test -p 2 ./internal/cli/ ./internal/config/ ./internal/agent/...
go run ./cmd/ws llm machine artifact get
go run ./cmd/ws artifact --help
make lint
```

### Acceptance

- `TestGuideTopics`, `TestApp_newRootCmd`, `TestErrorCode_*`,
  `TestNewPaths` and the guard tests pass with the new values.
- `ws llm` lists the seven `machine artifact` guides; `ws --help` doesn't
  show `artifact`; `ws artifact --help` does.
- A smoke test in a scratch `WS_CONFIG_DIR`: `printf 'hi' | ws artifact
  add - --name a.txt --json` then `ws artifact cat <id>` prints `hi`.

---

## T4. Comment commands, `@artifact` hints in prompts, the `ws llm` rule

- **Depends on:** T2, T3.
- **Constraints:** C1 (`anchor_not_found`), C2 (four topics), C5, C9.

### Files

Create: `internal/cli/artifact_comment.go`, `artifact_comment_test.go`;
guides `ws-machine-artifact-comment.md`, `-comment-list.md`,
`-comment-add.md`, `-comment-resolve.md`.

Change: `internal/cli/errors.go`, `errors_test.go`, `guides/ws.md` (the
code, the four `ws llm` lines, the `## Rules for agents` bullet from
README.md › Guides); `internal/cli/artifact.go` and
`machine_artifact.go` (add the `comment` subcommand to both trees);
`guides/ws-machine-artifact.md` (synopsis lines for `comment`);
`internal/cli/llm.go` (four topics after `"machine artifact rm"`);
`internal/cli/claude.go`, `claude_test.go`, `machine.go`,
`machine_test.go` (hints); `docs/adrs/0016-output-contract.md` (add
`anchor_not_found`).

### Error code

```go
	// ADR 0079: comment anchors.
	codeAnchorNotFound errorCode = "anchor_not_found" // exit 3
```

`artifactError` maps `artifact.ErrAnchorNotFound` to it with details
`{"occurrences": n, "lines": [...]}` from `*artifact.AnchorError`.

### Commands

One builder used by both trees, so the code is shared:

```go
// newArtifactCommentCmd is `comment list|add|resolve` for either tree;
// actor returns the person, or the calling agent after the checks of
// ws machine artifact (caller, refuseOnAgentMachine).
func (a *App) newArtifactCommentCmd(people bool) *cobra.Command
```

- `comment list <ID> [--open]`: `{"artifact_id", "latest", "threads",
  "warnings"}`; threads in id order; `--open` drops resolved ones.
- `comment add <ID> (--body <t> | --body-file <path|->) [--reply t<n> |
  --quote <t> [--line n] [--prefix t] [--suffix t]]`: `--prefix` and
  `--suffix` exist only on the people tree. Flag rules in README.md (`usage`
  otherwise). Prints `{"artifact_id", "thread", "comment", "warnings"}`.
- `comment resolve <ID> <THREAD> [--reopen]`: `{"artifact_id", "thread",
  "warnings"}`.

Times in thread JSON are RFC 3339 UTC strings; `anchor` `null` or the
object; keys exactly as README.md › Thread.

### Hints in prompts

```go
// artifactHints appends artifact.AppendHint to the prompt in args (the
// first element, when it doesn't start with "-"). Every referenced id must
// exist (artifact_unknown otherwise). remote: leave args alone and add the
// warning "artifacts aren't reachable from <env> yet; @artifact references were sent as plain text".
func (a *App) artifactHints(args []string, remote string) ([]string, error)
```

Call it in `runClaude` (`internal/cli/claude.go`) right after
`remoteParent` returns, before anything is created:
`opts.claudeArgs, err = a.artifactHints(opts.claudeArgs, runOn)`. This
covers `ws claude` (Claude and ws runner) and `ws machine spawn` with the
Claude runner. Call it also in `runMachineSpawnRunner` on its `prompt`
(wrap it as a one-element slice). Loop prompts are out of scope.

### Tests

`artifact_comment_test.go`: both trees — a whole-artifact thread; an
anchored thread by `--quote`; a duplicate quote without `--line` is
`anchor_not_found` exit 3 with `details.occurrences == 2`; with `--line`
it works; `--reply t1` adds `c2`; `--reply` with `--quote` is `usage`;
`resolve` then `list --open` hides it; `--reopen` shows it again;
`--quote` on a PNG is `artifact_invalid`; `put` after comments moves one
anchor and detaches another (the counts in `put`'s JSON).

`claude_test.go` / `machine_test.go`: a prompt with `@artifact:<id>` gets
the exact hint line appended (assert against `artifact.HintLine`); an
unknown reference is `artifact_unknown` exit 3 and creates no workspace;
a prompt with no reference is unchanged; a role spawn's prompt gets it.

### Verify

```
go test -p 2 ./internal/cli/ ./internal/artifact/...
go run ./cmd/ws llm machine artifact comment add
make lint
```

### Acceptance

- The tests pass; `ws llm` lists all eleven `machine artifact` guides and
  the overview has the `@artifact:` rule.
- End to end in a scratch `WS_CONFIG_DIR` with a workspace: `ws artifact
  add pitch.md`, `ws artifact comment add <id> --quote "…" --body "…"`,
  then from inside the workspace `ws machine artifact comment list <id>
  --open` shows it, `ws machine artifact put <id> pitch2.md --base-rev 1`
  moves it.

---

## T5. Docs

- **Depends on:** T4. **Parallel with:** T6.
- **Constraints:** C3 (`docs/config.md`), C6.

### Files

Create `docs/tools/ws-artifact.md`. Change `docs/tools/README.md`,
`docs/tools/ws-machine.md`, `docs/state.md`, `docs/config.md`,
`README.md`.

### Content

- `docs/tools/ws-artifact.md`: the house format of `docs/tools/ws-env.md`
  (synopsis, description, one `##` per subcommand with flags and JSON,
  errors table, examples). Say first that it's hidden from `ws --help` and
  `ws llm`, is used by the desktop app, and that agents use `ws machine
  artifact`. Include kinds and limits, the on-disk layout (from
  README.md › On disk), references and the hint line, and the remote
  environment limit.
- `docs/tools/ws-machine.md`: `## ws machine artifact` before `##
  Errors`, with each subcommand (`###`) and the lifecycle paragraph from
  README.md › Guides; the synopsis lines; the seven new codes in `##
  Errors`.
- `docs/state.md`: the C6 rows; amend "Workspace contents".
- `docs/config.md`: the `artifacts/` line in `### Layout` (C3).
- `README.md`: in `## Commands`, one line ("Share screenshots and
  documents with agents: the desktop app's Artifacts section, or `ws
  artifact add`; agents use `ws machine artifact`"); in `## Roadmap`, an
  entry "Artifacts ([0078](docs/adrs/0078-artifacts.md),
  [0079](docs/adrs/0079-artifact-comments-and-anchors.md)): done"; and,
  if `desktop/docs/artifacts-*.png` exist on the branch, link them from
  `desktop/README.md` (otherwise leave a `TODO(screenshots)` line out —
  don't link missing files).

### Verify

```
go test -p 2 -run 'TestGuideTopics' ./internal/cli/
grep -c "artifact" docs/tools/README.md docs/state.md docs/config.md README.md
for f in $(grep -oE '\(([^)]+\.md)(#[^)]*)?\)' docs/tools/ws-artifact.md | tr -d '()' | cut -d'#' -f1 | sort -u); do test -e "docs/tools/$f" || echo "missing $f"; done
make lint
```

### Acceptance

- Every command and flag in README.md's two synopses appears in the docs;
  every relative link resolves (the loop prints nothing).
- No code changes.

---

## T6. Desktop and Workspace Online: commands, Artifacts section, viewers, comments, prompt attachments, mock

- **Depends on:** T4. **Parallel with:** T5.
- **Constraints:** C7, C8, C9.

This is the largest task. **Split point:** if you approach your context
budget, stop after part A, commit, push, and report `FAIL` with "part B
not started" — the orchestrator will spawn part B on your branch.

### Node for the frontend (C9)

Before any `npm` command, install Node 24.12.0 into
`$CLAUDE_JOB_DIR/tmp/node` from
`https://nodejs.org/dist/v24.12.0/node-v24.12.0-linux-x64.tar.xz`, check
its SHA-256 is
`bdebee276e58d0ef5448f3d5ac12c67daa963dd5e0a9bb621a53d1cefbc852fd`, and
`export PATH=$CLAUDE_JOB_DIR/tmp/node/bin:$PATH`. If you can't, list every
frontend check as unverified.

### Part A: backend and the list

Files — create `desktop/src-tauri/src/artifacts.rs`; change
`desktop/src-tauri/src/lib.rs`, `online/dispatch.rs`, `online/server.rs`,
`desktop/src-tauri/Cargo.toml` (C7); `desktop/src/api.ts`; create
`desktop/src/features/artifacts/artifactsSlice.ts` (+ test),
`ArtifactsList.tsx` (+ test, + `.module.css`), `ArtifactsActions.tsx`,
`upload.ts` (+ test), `references.ts` (+ test); change
`desktop/src/features/ui/uiSlice.ts`, `features/rail/Rail.tsx`,
`Rail.test.tsx`, `desktop/src/App.tsx`, `desktop/src/store.ts` (C8).

`artifacts.rs`:

```rust
//! Artifacts: what `ws artifact` stores, for the Artifacts section, the
//! viewers and prompt attachments (ADR 0078). The app never reads or
//! writes the store itself.

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct Actor { pub kind: String, pub workspace: Option<String>, pub name: Option<String> }
// ThreadCounts, ArtifactSummary, Revision, Artifact (summary flattened + revisions),
// Anchor, ThreadComment, Thread, CommentList: the README.md shapes; deserialize
// snake_case from ws, serialize camelCase to the frontend.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ArtifactContent { pub rev: u32, pub content_type: String, pub size: u64, pub data_base64: String }
#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct NewAnchor { pub quote: String, pub line: u32, pub prefix: String, pub suffix: String }
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct CommentAdded { pub thread: Thread, pub comment: ThreadComment }

pub const MAX_UPLOAD: usize = 10 * 1024 * 1024;

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_list(state: State<'_, AppState>, kind: Option<String>, workspace: Option<String>) -> CmdResult<Vec<ArtifactSummary>>;
pub async fn artifact_show(state: State<'_, AppState>, id: String) -> CmdResult<Artifact>;
pub async fn artifact_read(state: State<'_, AppState>, id: String, rev: Option<u32>) -> CmdResult<ArtifactContent>;
pub async fn artifact_add(state: State<'_, AppState>, name: String, title: Option<String>, data_base64: String) -> CmdResult<Artifact>;
pub async fn artifact_remove(state: State<'_, AppState>, id: String) -> CmdResult<()>;
pub async fn artifact_comments(state: State<'_, AppState>, id: String) -> CmdResult<CommentList>;
pub async fn artifact_comment_add(state: State<'_, AppState>, id: String, body: String, reply: Option<String>, anchor: Option<NewAnchor>) -> CmdResult<CommentAdded>;
pub async fn artifact_comment_resolve(state: State<'_, AppState>, id: String, thread: String, reopen: bool) -> CmdResult<Thread>;
```

Each delegates to a private `async fn …(runner: &Runner, …)` that the
tests call, as `roles.rs` does. Arguments exactly as README.md ›
Desktop and Workspace Online commands. `artifact_read` runs `ws artifact
cat` with `Runner::run` (raw stdout; a non-success outcome goes through
`runner::ws_error(&stderr)`), then `artifact_show` for the revision's
content type (the `revisions[]` entry's `content_type` for that rev), and
base64-encodes with `base64ct::Base64` (standard,
padded). `artifact_add` decodes with `base64ct::Base64`; a bad encoding is
`AppError::new("bad_args", …)`; over `MAX_UPLOAD` is
`AppError::new("artifact_invalid", "<name> is over the 10 MiB limit")`
without running `ws`. `artifact_comment_add` sends the body on stdin with
`--body-file -`. `artifact_list` passes `--limit 1000` and unwraps
`{"artifacts": [...]}`; `artifact_remove` checks the item's `error` and
returns it as an `AppError` when not null.

Tests (`#[cfg(test)]`, the `fake_ws` helper copied from `roles.rs`):
each command's exact argument list; `artifact_add` passes the decoded
bytes on stdin and refuses 10 MiB + 1 without running `ws` (no `args`
file); `artifact_read` returns base64 of the script's stdout; a `ws`
error JSON on stderr becomes that code.

`dispatch.rs`: 8 `match` arms with `args!` (camelCase: `dataBase64`,
`reopen`, …) and 8 `COMMANDS` entries appended (C7). `server.rs`: the
16 MiB limit on `/api/invoke/{command}` and a test posting 3 MiB of JSON
to an unknown command that gets 404, not 413. `Cargo.toml`: `base64ct`
non-optional (C7).

Frontend part A:

- `api.ts`: the types of README.md › TypeScript and 8 wrappers
  (`artifactList`, `artifactShow`, `artifactRead`, `artifactAdd`,
  `artifactRemove`, `artifactComments`, `artifactCommentAdd`,
  `artifactCommentResolve`).
- `references.ts`: `findRefs(text): string[]`, `hintLine(ids): string`,
  `appendHint(text): string` — the same regex and exact strings as Go's
  `artifact.FindRefs`, `HintLine`, `AppendHint` (copy the Go test cases
  into `references.test.ts`).
- `upload.ts`: `fileToBase64(file: File): Promise<string>` (`FileReader`
  `readAsDataURL`, strip the prefix); `uploadFiles(files: readonly File[]):
  Promise<Artifact[]>` that refuses files over 10 MiB with an `AppError`
  `{code: "artifact_invalid", message: "<name> is over the 10 MiB limit"}`
  before calling `api.artifactAdd`; `imagesFromClipboard(event:
  ClipboardEvent): File[]` (items of kind `file` with an `image/` type;
  name a nameless one `pasted-<yyyymmdd-hhmmss>.png`).
- `artifactsSlice.ts`: `{items: ArtifactSummary[], loading, error,
  warnings: string[], filter: "all" | "image" | "markdown" | "text",
  search: string, selected: string | null, uploads: {name: string}[]}`
  with thunks `loadArtifacts`, `uploadArtifacts(files)` (adds to
  `uploads`, then reloads and selects the last added), `removeArtifact`.
  Use `thunk.ts`'s helpers as `loopDataSlice.ts` does.
- `ArtifactsList.tsx`: chips (All, Images, Documents, Text), search field,
  the drop zone (shown while a drag with files is over the list), rows as
  README.md's mockup; paste of images anywhere in the list uploads.
  `ArtifactsActions.tsx`: the Upload button with a hidden
  `<input type="file" multiple accept=".png,.jpg,.jpeg,.gif,.webp,.svg,.md,.markdown,.txt,.log,.json,.diff,.patch,.csv,.yaml,.yml,image/*">`.
- Section wiring (C8): `"artifacts"` in `Section`, `railOrder`,
  `shownSections`, the rail icon and label, `sectionViews`, the
  `Warnings` switch, the reducer in `store.ts`. `ArtifactsPane` may be a
  placeholder ("Select an artifact") until part B.

### Part B: viewers, comments, prompt attachments, mock

Files — create in `desktop/src/features/artifacts/`: `ArtifactsPane.tsx`
(+ test, + css), `ImageViewer.tsx` (+ test), `TextViewer.tsx`,
`MarkdownViewer.tsx` (+ test, + css), `anchors.ts` (+ test),
`ThreadList.tsx` (+ test, + css), `CommentComposer.tsx`, `AttachButton.tsx`
(+ test), `useAttachments.ts`. Change
`desktop/src/features/create/NewAgentDialog.tsx` (+ test),
`desktop/src/features/terminals/ReplyBox.tsx` (+ test),
`desktop/test/tauri-mock.js`.

- `ArtifactsPane`: header (title or name, Copy reference, revision picker,
  thread counts, Delete with a confirm dialog), then the viewer by kind,
  then (image, text) a whole-artifact `ThreadList` and composer. Loads
  `artifactShow`, `artifactRead(id, rev)` and `artifactComments`.
- `ImageViewer`: `<img src={`data:${contentType};base64,${dataBase64}`}
  alt={name}>`, Fit / 100% toggle, natural size in the caption. Never
  `dangerouslySetInnerHTML`, never an `<object>`/`<iframe>`/inline SVG.
- `TextViewer`: `<pre>` with `white-space: pre-wrap`.
- `MarkdownViewer`: `react-markdown` with `remarkPlugins={[remarkGfm]}`,
  `skipHtml`, `urlTransform` keeping only `http:`, `https:`, `mailto:` and
  `#…` (else `""`), an `a` component that calls `api.openExternal(href)`
  on click (preventDefault), an `img` component rendering
  `[image: {alt}]({src})` as text, and block components (`p`, `h1`-`h6`,
  `li`, `blockquote`, `pre`, `tr`) that add `data-line-start` /
  `data-line-end` from `node.position`. **First test:** render
  `"# A\n\nB **c**\n"` and assert the `p` has `data-line-start="3"` (this
  verifies ADR 0079's unverified fact; if it fails, stop and report).
  The highlight: a rehype plugin from `anchors.ts`,
  `rehypeAnchors(threads: readonly Thread[], latest: number)`, that for
  each open thread whose `anchor.rev === latest` finds the innermost
  element whose `position` covers `anchor.lineStart`, and if one of its
  text nodes contains `anchor.quote`, splits it to wrap the quote in
  `{type: "element", tagName: "mark", properties: {dataThread: id}}`;
  otherwise adds `className: ["anchoredBlock"]` and `dataThread` to the
  element. Selecting text inside the article shows a floating Comment
  button; it opens `CommentComposer` with `selectionAnchor(...)`'s quote.
- `anchors.ts`: `selectionAnchor(source: string, lineStart: number,
  lineEnd: number, selected: string): NewAnchor` exactly as ADR 0079 ›
  From a rendered selection: block text = source lines `lineStart..lineEnd`
  joined by `\n`; if `selected.trim()` is non-empty and in the block,
  quote it, `line` = `lineStart` + newlines before it in the block,
  prefix/suffix = up to 32 code points (`Array.from`) of block text before
  and after; else quote = the block text trimmed, first 2,000 code points,
  `line = lineStart`, `prefix = suffix = ""`. Plus `rehypeAnchors`.
  Tests: both cases, multi-byte text, a selection crossing `**bold**`
  (falls back to the block), the plugin wrapping a quote and marking a
  block.
- `ThreadList`: anchored open threads in `lineStart` order, then
  "Detached (n)" (quote, "on r<rev>"), then "Resolved (n)" collapsed. Each
  thread: its quote (if anchored), comments (author `you` for a person,
  else the workspace name), Reply field, Resolve / Reopen. Clicking a
  thread scrolls its `mark[data-thread]` or `[data-thread]` element into
  view and outlines it; clicking a mark scrolls the thread into view.
  Layout: margin column at ≥ 900px, below the document under 900px,
  buttons at least 40px tall under 720px.
- `AttachButton` + `useAttachments(insert: (text: string) => void)`:
  file picker, drop and paste handlers that call `uploadFiles` and insert
  `@artifact:<id> ` at the cursor; chips of the references in the text
  (`findRefs`), ✕ removes the reference text; `busy` while uploading.
  `NewAgentDialog`: Attach under the prompt, drop/paste on the field,
  Create disabled while busy. `ReplyBox`: the same, and on send
  `text = appendHint(message)`. Disabling Attach for an agent on a remote
  environment needs the entry's environment, which the desktop's `Entry`
  doesn't carry yet (`grep -n environment desktop/src/api.ts` finds only
  `settingsEnvironment`); leave it out and say so in the report.
- `tauri-mock.js` (C8): sample data for the 8 commands: a PNG screenshot
  (a small valid PNG in base64, ~1-5 KB, drawn as a "login error" banner
  is fine but any valid PNG works), a Markdown pitch with two revisions
  and four threads (two anchored open, one detached, one resolved), and a
  text log; `artifact_add` appends to the list; comment commands mutate
  the threads in memory. These are what the screenshot child photographs.

### Verify

```
cd desktop/src-tauri && cargo test -j 2 artifacts && cargo test -j 2 online::dispatch
cd desktop/src-tauri && cargo clippy --all-targets -- -D warnings
cd desktop/src-tauri && cargo test -j 2 --no-default-features --features online && cargo clippy --no-default-features --features online --all-targets -- -D warnings
cd desktop && npm ci && npm run typecheck && npm test -- --maxWorkers=2 --testTimeout=60000
```

(The last line needs Node 24 from above.)

### Acceptance

- Every verify command passes, or, for the frontend, is listed as
  unverified with the reason.
- `commands_match_generate_handler` and `every_command_is_handled` pass
  with 89 commands.
- The rail shows Artifacts after Loops; uploading by picker, drop and
  paste works against the mock (component tests); a Markdown artifact
  renders with highlights and threads; HTML in Markdown renders as
  nothing (`skipHtml`), a `javascript:` link has an empty `href`, and an
  `<img>` in Markdown renders as text (tests).
- `desktop/test/tauri-mock.js` handles all 8 commands, so the screenshot
  child can photograph: the list, the image viewer, the Markdown viewer
  with threads, the New agent dialog with an attachment, and the phone
  layout.
