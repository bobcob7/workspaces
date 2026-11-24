# 0078. Artifacts: files people and agents share, kept in one store under `CONFIG_DIR/artifacts/` that outlives workspaces, reached by agents through `ws machine artifact` and by the desktop app through a hidden `ws artifact`

- Status: Accepted (2026-11-23)
- Date: 2026-11-23

## Context

### Problem

People and agents need to hand each other files that aren't code. A person
triaging a bug has screenshots and wants an agent to look at them; an agent
asked to pitch a design writes a Markdown document and wants a person to
read it rendered and comment on it. Today the only channels are the prompt
(text only) and the agent's repos (a pull request is the wrong place for a
pitch, and a person can't drop a screenshot into a running agent's
checkout from the desktop app or Workspace Online). How do files get from a
person to an agent, and from an agent to a person, with the same commands on
every runner and from every front end, safely, and so that a pitch is still
there after the agent that wrote it is gone?

### What a solution needs

1. **Agents can read what people upload, as files.** Claude Code reads an
   image by path; an agent must end up with a readable path, through
   `ws machine` only ([0046](0046-ws-machine.md)).
2. **People can read what agents upload, rendered,** in the desktop app and
   in Workspace Online, with the same behaviour
   ([0053](0053-workspace-online.md)).
3. **Outlives the workspace.** A pitch stays readable, and its comments
   stay, after the pitching agent's workspace is removed.
4. **Safe to render.** Nothing uploaded runs script in the app; nothing
   uploaded is trusted for its declared type.
5. **Bounded.** Sizes, counts and types have limits a person can predict.
6. **Atomic and private.** A reader never sees half a file; files are the
   user's only (mode 0600), like every record under `CONFIG_DIR`.
7. **Local behaviour first, remote environments honest.** Works on the
   local machine now; on a remote environment ([0072](0072-remote-environments.md))
   it fails clearly rather than storing files where nobody looks.

### Relevant facts

- Every `ws` record is written through a temporary file renamed into place,
  and nothing `ws` writes goes inside `workspaces/<id>/`[^state].
- Data that must outlive a workspace already lives beside `state/`, not in
  it: `reviews/` ([0039](0039-ws-review-command.md)) and `loops/`
  ([0048](0048-agent-loop-scheduler.md)). `state/<id>/` is deleted with its
  workspace, and `state/<root>/machine/` with the root of its tree
  ([0050](0050-machine-wait-results-and-roles.md)).
- The desktop app never reads or writes `ws`'s files: it runs `ws … --json`
  (`desktop/src-tauri/src/roles.rs`: "The app never reads or writes a role
  file itself"), passing input on stdin with `Runner::ws_json_with_input`.
  Workspace Online runs the same command functions behind
  `POST /api/invoke/<command>`[^online].
- Workspace Online reads the body with axum's `Bytes` extractor and sets no
  `DefaultBodyLimit`, so a request body over axum's default 2 MB is refused
  today[^axum].
- The Tauri CSP and Workspace Online's CSP both allow `img-src 'self' data:`
  and no inline script[^csp]. Browsers run no script in an SVG loaded
  through `<img>`[^svgimg].
- `react-markdown`, already a dependency, "typically escapes HTML (or
  ignores it, with `skipHtml`)" and "is secure by default"[^rm].
- The Claude API takes images up to 10 MB base64-encoded and 8000×8000 px,
  and downscales larger ones[^vision].
- Go's `http.DetectContentType` recognises PNG, JPEG, GIF and WebP by their
  bytes, and reports SVG as `text/plain` or `text/xml`[^measured-sniff].
- A remote environment's agent runs its own `ws` with its own `CONFIG_DIR`
  ([0073](0073-environment-agent-is-ws.md)); the controller and the desktop
  app see the remote only through MQTT operations
  ([0074](0074-mqtt-transport-embedded-broker.md)).

## Decision

We will add **artifacts**: immutable revisions of a named file, with
comment threads ([0079](0079-artifact-comments-and-anchors.md)), in one
store per `CONFIG_DIR`.

**Scope and lifetime: one global store.** `CONFIG_DIR/artifacts/` holds
every artifact. An artifact records who created it (a person, or an agent's
workspace id and the root of its tree) but isn't owned by a workspace:
removing a workspace removes no artifact. Only `ws artifact rm` (a person)
or `ws machine artifact rm` (an agent, for artifacts its own tree created)
removes one. No automatic expiry in this version; the store has a quota.

**Layout.** Private to `ws`; `--json` output is the contract.

```
artifacts/
  <id>/                  mode 0700
    .lock                the artifact's lock (flock), for every write
    artifact.json        the record: version 1, id, name, kind, title, created_*, revisions[]
    r1, r2, …            each revision's bytes, mode 0600, never rewritten
    comments.json        threads and anchors, version 1 (ADR 0079); absent until the first comment
  .tmp/                  temporary files, renamed into place; leftovers older than an hour are deleted by the next write
```

An id is `art-` and 8 lowercase hex digits from `crypto/rand`, made with
`os.Mkdir` so a collision retries. A revision is written to `.tmp/`,
synced, renamed to `r<n>`, and only then added to `artifact.json` (itself
written through `.tmp/` and renamed), all under the artifact's lock, so a
reader sees the old record or the new one and never a revision without its
bytes. Readers take no lock.

**Kinds and types, decided by the bytes.** Allowed:

| Kind | Content types | Detected by | Limit |
| --- | --- | --- | --- |
| `image` | `image/png`, `image/jpeg`, `image/gif`, `image/webp` | `http.DetectContentType` on the bytes | 10 MiB |
| `image` | `image/svg+xml` | name ends `.svg`, valid UTF-8, and `<svg` is the first element after an optional XML declaration, comments and doctype | 1 MiB |
| `markdown` | `text/markdown; charset=utf-8` | name ends `.md` or `.markdown`, valid UTF-8, no NUL | 1 MiB |
| `text` | `text/plain; charset=utf-8` | any other valid UTF-8 with no NUL (logs, diffs, JSON, HTML source) | 1 MiB |

Anything else is refused with `artifact_invalid`. A declared type is never
trusted: an `.png` name whose bytes aren't PNG is stored as whatever the
bytes are, or refused. Each revision of an artifact must keep its kind.
Empty files are refused. Names are 1-128 characters, one path element, no
control characters, no leading `.`; the store keeps the name only for
display and for the file name `get` writes.

**Limits.** 10 MiB a revision as above; 100 revisions an artifact; a store
quota of 2 GiB across all revisions, checked on every write by summing the
sizes in the records (`artifact_full` past it, exit 4). Every revision is
kept: anchors ([0079](0079-artifact-comments-and-anchors.md)) and history
need them, and text revisions are small.

**Safe rendering.** The front ends show `image` artifacts only through
`<img src="data:…">`, never by inlining SVG into the DOM; `markdown` through
`react-markdown` with `skipHtml` and no `rehype-raw`, links opened
externally (`open_external`) and only for `http`, `https` and `mailto`,
images in Markdown not loaded (shown as their alt text and URL), so
Markdown can't fetch anything; `text` as text in a `<pre>`. The CSPs stay
as they are. Workspace Online serves no artifact bytes at their own URL, so
no uploaded file is ever a document the browser renders.

**Agents: `ws machine artifact`.** `add`, `list`, `show`, `get`, `put`,
`rm`, and `comment list|add|resolve`, always JSON, acting for the caller
([0046](0046-ws-machine.md)). `get <id>` copies a revision to a file and
prints its absolute `path`: by default
`<workspace>/.ws-artifacts/<id>/r<n>/<name>`, or `--out <dir>` (any
directory the caller can write). This is the one place `ws` writes inside a
workspace, and only because the agent asked: the agent reads the file with
its own Read tool, and the workspace's removal deletes the copy. The copy
goes in the workspace root, not in a repo, so it never shows in `git
status`. An agent may add, read, revise and comment on any artifact (every
revision is kept, so nothing is lost), but `rm` only artifacts its own tree
created (`artifact_forbidden`, exit 4).

**People and the desktop app: `ws artifact`, hidden.** The same verbs with
`cat` instead of `get` (raw bytes on stdout, for the desktop), plus `add -`
from stdin. It is a people command, like `ws loop`, but **hidden** from
`ws --help` and `ws llm`, like `ws runner`: its main user is the desktop
app, a person at a terminal reads `docs/tools/ws-artifact.md` or
`ws artifact --help`, and an agent reading `ws llm` sees only
`ws machine artifact`, so it never reaches for the people command.

**References.** `@artifact:<id>`, optionally `@artifact:<id>/r<n>`, in any
prompt. `ws claude` and `ws machine spawn` check each reference in the
prompt (an unknown one is `artifact_unknown`, before anything is created)
and append one line telling the agent how to read them:

```
(Artifacts art-1a2b3c4d, art-5e6f7a8b: run "ws machine artifact get <id>" for each, then read the file at the "path" it prints.)
```

The desktop app inserts the same reference when a person uploads into the
New agent dialog's prompt or the reply box, and appends the same line when
it sends a reply containing references. `ws llm`'s rules for agents gain
one bullet saying what `@artifact:` means.

**Front ends.** The desktop app and Workspace Online get the same commands
(`artifact_list`, `artifact_show`, `artifact_read`, `artifact_add`,
`artifact_remove`, `artifact_comments`, `artifact_comment_add`,
`artifact_comment_resolve`), each running `ws artifact … --json`. Uploads
travel as base64 in the command's JSON (`artifact_add`), are decoded in
Rust and piped to `ws artifact add -` on stdin; `artifact_read` returns
`{contentType, dataBase64}`. Workspace Online raises the body limit of
`/api/invoke/{command}` to 16 MiB, enough for a 10 MiB file in base64.

**Remote environments.** The store is the controller's, the machine the
desktop app and Workspace Online run on. An agent on a remote environment
runs its own `ws`, whose store nobody looks at, so on an enrolled agent
machine (`environment/agent.toml` exists) every `ws machine artifact`
command fails with `artifact_unreachable` (exit 1) and a message naming
this ADR. `ws claude --env <remote>` with a reference in its prompt sends
the prompt unchanged, without the appended line, with a warning. Reaching
the controller's store from a remote (an `artifact.*` request on the
agent's `up` topic, chunked under the 256 KiB payload limit) is **out of
scope** and needs its own ADR.

**Runner agents.** The ws runner's guard
([0066](0066-runner-security-boundaries.md)) allows `ws machine artifact`
as it allows `ws machine progress`; whether a runner model can see an image
is the model's business.

### Why this option

1. **Readable files**: `get` gives a plain path, the one thing every agent
   and runner can read; a reference costs one command.
2. **Rendered for people**: one set of commands for Tauri and Workspace
   Online, through the `ws` CLI as every other desktop feature does.
3. **Outlives the workspace**: the store sits beside `reviews/` and
   `loops/`, not under `state/`.
4. **Safe**: type from bytes, a short allow-list, no HTML, no inline SVG, no
   network from Markdown, and the CSPs unchanged.
5. **Bounded**: per-file, per-artifact and store limits.
6. **Atomic**: immutable revision files and a record renamed into place,
   writes under a per-artifact lock.
7. **Remote**: fails loudly. This is the weak point: an agent on a remote
   can't use artifacts until the follow-up ADR.

It is also weak on cleanup: nothing expires, so a busy user fills the
quota and must remove artifacts by hand.

## Alternatives

### Artifacts per workspace, under `state/<id>/`

**Pros**

- Removed with the workspace; no quota needed.

**Cons**

- Fails criterion 3: the pitch dies with the pitching agent. Screenshots a
  person uploads before starting the agent have no workspace yet.

### Copy referenced files into the workspace when the agent starts

`ws claude` would materialise every referenced artifact before the agent
starts.

**Pros**

- The agent needs no command.

**Cons**

- Only works at start; a reference typed into a running agent's reply box
  still needs a command, so agents need `get` anyway. Two paths to test,
  and the start path has to handle remotes. Rejected for the hint line.

### Agents read the store's files directly

**Pros**

- No copy.

**Cons**

- Makes the layout a contract, puts the user's other artifacts one `ls`
  away, and a path outside the session's directories may prompt for
  permission. `get` copies one revision on request.

### Serve artifacts at their own URL in Workspace Online

`GET /api/artifacts/<id>/r<n>` with the stored content type.

**Pros**

- Streams large files; `<img src>` works without base64.

**Cons**

- A different path from Tauri (which has no HTTP server), and an uploaded
  file served from the app's origin is one content-type mistake away from
  script. `data:` URLs through the existing command path keep both front
  ends identical.

### A visible people command `ws artifact`

**Pros**

- Discoverable in `ws --help`.

**Cons**

- Agents read `ws llm`; a people command beside `ws machine artifact`
  invites them to use the wrong one, and adds ten guides that say the same
  thing. Hidden like `ws runner`, it stays documented in `docs/tools/`.

### Allow any file type, or PDF

**Pros**

- Logs in odd encodings, PDFs of specs.

**Cons**

- Every type is a renderer to secure and a preview to build. PDF needs a
  viewer (pdf.js) or an external app; deferred until asked for.

## Consequences

- New error codes ([0016](0016-output-contract.md)): `artifact_invalid`
  (2), `artifact_unknown` (3), `artifact_conflict` (4, see 0079),
  `artifact_full` (4), `artifact_forbidden` (4), `artifact_unreachable`
  (1); `anchor_not_found` (3) from 0079.
- `config.Paths` gains `Artifacts` (`CONFIG_DIR/artifacts`), and
  `docs/state.md` gains its rows; `ws cache prune` and the removal pass
  never touch it.
- `ws llm` gains guides for `machine artifact` and its subcommands, and a
  rule for `@artifact:` references; `ws artifact` has no guide.
- The desktop gains an Artifacts section, an image viewer, a Markdown
  viewer with comment threads, and upload in the New agent dialog and the
  reply box; Workspace Online gets the same through `/api/invoke`.
- The desktop's `base64ct` dependency stops being online-only.
- Unverified: whether Claude Code in its default permission mode reads
  `<workspace>/.ws-artifacts/…` without asking when the session runs in a
  one-repo workspace's repo (the file is outside the session directory).
  The guide tells the agent to rerun `get --out <dir inside your working
  directory>` if the read is refused. Images over 7.5 MB raw exceed the
  API's 10 MB base64 limit and may not reach the model.

## References

[^state]: `docs/state.md`, "Layout" and "Records" (repo at `320b683`): "Nothing `ws` writes goes inside `workspaces/<id>/`." "They're written through a temporary file renamed into place, so a reader sees all of a record or none of it."
[^online]: `desktop/src-tauri/src/online/server.rs` `router` (`/api/invoke/{command}`) and `dispatch.rs` `COMMANDS` (repo at `320b683`).
[^axum]: `desktop/src-tauri/src/online/server.rs`, `invoke(… body: Bytes)`, and no `DefaultBodyLimit` anywhere in `desktop/src-tauri/src` (`grep -rn DefaultBodyLimit`, 2026-11-23). axum docs, "DefaultBodyLimit." <https://docs.rs/axum/0.8/axum/extract/struct.DefaultBodyLimit.html> (accessed 2026-11-23). "For security reasons, Bytes will, by default, not accept bodies larger than 2MB."
[^csp]: `desktop/src-tauri/tauri.conf.json` `security.csp` and `server.rs` `CONTENT_SECURITY_POLICY` (repo at `320b683`): both `default-src 'self'; … img-src 'self' data:`.
[^svgimg]: W3C, "SVG Integration," §3 "Processing modes" and §4 "Referencing modes." <https://www.w3.org/TR/svg-integration/> (accessed 2026-11-23). "SVG documents processed with the animated image document referencing mode must use the secure animated processing mode", the mode for an HTML `img`; secure animated mode is for an SVG "that is not allowed to resolve external references", and its feature table disables script ("When script execution is disabled in an SVG document, no script in the document must be run.").
[^rm]: remarkjs, "react-markdown" README, "Security." <https://github.com/remarkjs/react-markdown> (accessed 2026-11-23). "Use of `react-markdown` is secure by default." "`react-markdown` typically escapes HTML (or ignores it, with `skipHtml`)."
[^vision]: Anthropic, "Vision." <https://platform.claude.com/docs/en/build-with-claude/vision> (accessed 2026-11-23). "The maximum dimensions for each image are 8000x8000 px." "The maximum size per image is: 10 MB (base64-encoded) on the Claude API…"
[^measured-sniff]: Measured 2026-11-23 in the ws-online pod, x86_64, go1.27.3: `http.DetectContentType` on minimal PNG, JPEG, GIF, WebP, PDF, SVG, XML-prolog SVG, Markdown, JSON and HTML samples returned `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `application/pdf`, `text/plain; charset=utf-8`, `text/xml; charset=utf-8`, `text/plain; charset=utf-8`, `text/plain; charset=utf-8`, `text/html; charset=utf-8`. One run.
