# 0079. Artifact comments are threads anchored to a quote of a text revision's source, moved to each new revision by exact quote and context, and kept as detached when the quote is gone

- Status: Accepted (2026-11-23)
- Date: 2026-11-23

## Context

### Problem

An agent pitches a design as a Markdown artifact
([0078](0078-artifacts.md)); a person reads it rendered and comments on
parts of it; the agent reads the comments, edits the document, and the
person checks the result. How is a comment attached to "this part" of a
document so that it survives the document being revised, both the desktop
app (which shows rendered Markdown) and an agent (which sees source text)
can make and read anchors, and nothing gets silently attached to the wrong
text?

### What a solution needs

1. **Agents can read anchors as text**: which words a comment is about, and
   where they are now, without a renderer.
2. **Agents can make anchors** from a command line, with a quote.
3. **People can make anchors** by selecting rendered text, including
   selections that cross formatting.
4. **Survives revisions**: a comment on a sentence that didn't change stays
   on it after an unrelated edit; a comment whose text was rewritten isn't
   moved onto other text.
5. **Deterministic and small**: the same input gives the same anchor, in
   code a small model can write and test.

### Relevant facts

- A revision is immutable and every revision is kept
  ([0078](0078-artifacts.md)).
- `react-markdown` gives every custom component `node`, the hast element it
  renders[^rm-pos]; hast nodes made from Markdown carry the source
  `position` (start and end line, column and offset) through
  `mdast-util-to-hast`'s `patch`. Not yet checked in this repo: the first
  UI task asserts it in a test before relying on it.
- The W3C Web Annotation model's TextQuoteSelector identifies text by an
  exact quote with a prefix and suffix; it calls its TextPositionSelector
  (offsets) "very brittle with regards to changes to the resource"[^wa].
- JavaScript strings index UTF-16 code units and Go strings index bytes,
  so an offset computed in one isn't the other's without conversion.

## Decision

We will anchor comments to **source text**, by **quote and context**, and
compute every offset in `ws`, never in the front end.

**Threads.** `comments.json` (version 1) beside the revisions holds
`threads`, each `{id, anchor, resolved, resolved_at, resolved_by,
comments}`. A thread id is `t1`, `t2`, … in creation order. A comment is
`{id, author, body, created_at}`, ids `c1`, `c2`, … within the thread; the
first is the thread's opening comment. `author` is `{"kind": "person",
"workspace": null, "name": null}` or `{"kind": "agent", "workspace": "<id>",
"name": "<display name at the time>"}`. Bodies are Markdown, 1-10,000
characters, rendered as artifacts are (no HTML). Limits: 200 threads an
artifact, 50 comments a thread (`artifact_full`).

**Anchors.** A thread's `anchor` is `null` (about the whole artifact; the
only kind on an `image`) or:

```json
{"rev": 2, "start": 120, "end": 139, "line_start": 7, "line_end": 7,
 "quote": "the cache is shared", "prefix": "…up to 32 chars", "suffix": "up to 32 chars…",
 "state": "anchored"}
```

`start` and `end` are UTF-8 byte offsets into revision `rev`'s source,
`line_start` and `line_end` 1-based lines; `prefix` and `suffix` are the up
to 32 characters (runes) before and after in that revision. `state` is
`anchored` when `rev` is the latest revision, else `detached`.

**Making an anchor.** The caller gives a `quote` (1-2,000 characters, not
only whitespace) and may give a `prefix`, a `suffix` or a `line`. `ws`
finds every occurrence of the quote in the latest revision's source and
picks:

1. the only one; else
2. with `line`, the one starting on that line; else
3. the one with the highest context score (the length of the common suffix
   of the given prefix and the text before the occurrence, plus the length
   of the common prefix of the given suffix and the text after), when one
   occurrence scores strictly highest.

No occurrence, or a tie, is `anchor_not_found` (exit 3) with
`details: {"occurrences": <n>, "lines": [<line of each>]}`, so an agent can
retry with `--line`. Anchors are made only on the latest revision.

**From a rendered selection.** The desktop's Markdown viewer marks each
block element with its source lines (from `node.position`). For a selection
inside one block, the front end sends the selected text as `quote`, the
block's first line as `line`, and up to 32 characters of the block's source
before and after the first match as context. When the selected text isn't
in the block's source as-is (it crosses `**bold**`, a link, inline code),
it anchors the **whole block**: `quote` is the block's source (its first
2,000 characters), `line` its first line. A selection across blocks anchors
the first block. A person always sees what was anchored, highlighted, before
posting.

**New revisions move anchors.** `put` re-anchors every thread, open or
resolved, from its current anchor to the new revision:

1. every exact occurrence of `quote` in the new source; if there are none,
   occurrences after collapsing each run of whitespace to one space on both
   sides (mapped back to source offsets);
2. none: the thread is `detached`, keeping its last anchor (its `rev`
   is older than the latest);
3. one: moved there;
4. several: the highest context score against the old `prefix` and
   `suffix`; a tie goes to the occurrence whose relative position
   (`start / len`) is closest to the old one, then the earliest.

A moved anchor's `prefix`, `suffix`, lines and `rev` are recomputed from
the new revision. `put` prints how many threads were anchored and detached.
A detached thread stays detached; a person or agent may reply, resolve it,
or open a new anchored thread.

**Revisions.** `put` takes `--base-rev <n>`: if the latest revision isn't
`n`, it's `artifact_conflict` (exit 4) and nothing is written, so an agent
revising while another does doesn't silently drop the other's change.
Without it, `put` always appends. Old revisions stay readable (`get --rev`,
the desktop's revision picker) and show no anchors.

**Resolving.** `resolve <thread>` sets `resolved`, `resolved_at` and
`resolved_by`; `--reopen` clears them. Replies don't change it. Agents and
people may both resolve.

### Why this option

1. **Agents read text**: the quote and lines are the anchor.
2. **Agents make anchors** with `--quote` and, when it repeats, `--line`.
3. **People select rendered text**; a selection the source doesn't contain
   falls back to its block, so it is never refused.
4. **Survives revisions**: exact quote plus context moves a comment past
   unrelated edits and refuses to move it onto rewritten text. It is weak
   where a sentence is lightly reworded: the comment detaches rather than
   following it. Detached threads are shown, not lost.
5. **Deterministic**: three rules, all in Go, tested by table.

## Alternatives

### Offsets only (TextPositionSelector)

**Pros**

- Trivial to store and apply.

**Cons**

- Any edit above the comment moves its text; needs a diff to rebase.

### Fuzzy matching (diff-match-patch's `match_main`)

**Pros**

- Follows lightly reworded text.

**Cons**

- Can attach a comment to text the reviewer never read, with a threshold to
  tune; a new dependency or a port. Exact matching plus visible detachment
  is predictable; fuzzy can be added behind the same `state`.

### Anchor to rendered text (DOM ranges)

**Pros**

- Matches what a person selected exactly.

**Cons**

- Agents don't render Markdown, so they couldn't make or read anchors, and
  a renderer upgrade would move every anchor.

### Anchor to blocks only (paragraph or heading ids)

**Pros**

- Simple; robust to edits within a block.

**Cons**

- Too coarse for "this word is wrong", and blocks have no stable id in
  Markdown. Kept as the fallback for selections the source can't match.

### Comments on images (regions)

**Pros**

- "This button" on a screenshot.

**Cons**

- Out of scope: the screenshot case flows person → agent, where the prompt
  says what to look at. Image comments are whole-artifact for now; a
  region anchor can be a later `anchor` shape.

## Consequences

- New error code `anchor_not_found` (3); `artifact_conflict` (4) is used
  by `put --base-rev`.
- `ws machine artifact comment list|add|resolve` and `ws artifact comment
  list|add|resolve` print threads with their anchors.
- The desktop's Markdown viewer highlights anchored quotes in the rendered
  text by searching the block that holds `line_start`, falling back to
  marking the block; detached threads are listed apart with their quote.
- Agents learn of new comments only when they look (`comment list --open`
  in their guide); notification is out of scope.
- Unverified: how often real edits detach comments; the counts `put` prints
  will tell.

## References

[^rm-pos]: remarkjs, "react-markdown" README, "Appendix B: Components." <https://github.com/remarkjs/react-markdown#appendix-b-components> (accessed 2026-11-23). "Every component will receive a `node`. This is the original `Element` from `hast` element being turned into a React element." The README doesn't state that `node.position` is set; that part is unverified here.
[^wa]: W3C, "Web Annotation Data Model," §4.2.4 "Text Quote Selector" and §4.2.5 "Text Position Selector." <https://www.w3.org/TR/annotation-model/#text-quote-selector> (accessed 2026-11-23). "This Selector describes a range of text by copying it, and including some of the text immediately before (a prefix) and after (a suffix) it to distinguish between multiple copies of the same sequence of characters." On the Text Position Selector: "but is very brittle with regards to changes to the resource."
