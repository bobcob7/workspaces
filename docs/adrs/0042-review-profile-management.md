# 0042. Review profiles are added, edited, and deleted with `ws review profiles`, as files in `CONFIG_DIR/review/profiles/`; a built-in is edited by overriding it and restored by deleting the override

- Status: Accepted (2026-10-28)
- Date: 2026-10-28

## Context

### Problem

Reviewer profiles ([0038](0038-review-fans-out-to-profile-subagents.md))
are eight built into `ws`, plus any Markdown file a user writes in
`CONFIG_DIR/review/profiles/<name>.md`, which adds a profile or replaces
the built-in of that name. `ws review profiles` only lists them, and the
desktop app's settings only choose when each runs and on which model.
There is no way, short of writing files by hand, to add a profile, change
what one tells its reviewer, or remove one. How are profiles managed, from
the CLI and from the app?

### What a solution needs

1. **Add, edit, and delete** a profile's description, file patterns, and
   guide, from `ws` and from the desktop app.
2. **Built-ins stay recoverable**: editing one can be undone, and a `ws`
   upgrade that improves a built-in still reaches users who never edited
   it.
3. **One place for a profile's content**, so the CLI, the app, and agents
   all see the same profile.
4. **Safe while reviews run**: editing a profile doesn't change a review
   that has already started.

### Relevant facts

- `profile.Set` reads the user's directory first, then the built-ins; a
  user file hides the built-in of the same name, and a user file that
  doesn't parse hides it too rather than falling back
  (`internal/review/profile/set.go`).
- A profile file is front matter (`description`, `when`, `match`) and a
  Markdown body, the reviewer's guide (`internal/review/profile/profile.go`).
  `decisions` is reserved and can't name a profile.
- `when` and `model` can also be set in `config.toml` under
  `[review.profiles.<name>]`, which `ws` writes once and never
  rewrites ([0011](0011-config-dir.md), `internal/writeonce`); the app
  keeps its own when/model table in `desktop.json` and passes it as
  `--profile`/`--auto` flags ([0038](0038-review-fans-out-to-profile-subagents.md)).
- `ws review start` puts each profile's guide into the session's
  `--agents` JSON when the review starts ([0038](0038-review-fans-out-to-profile-subagents.md)).
- `ws review profiles --json` reports `builtin: false` for a user file
  that replaces a built-in, the same as for a new profile
  (`internal/cli/review_profiles.go`).

## Decision

We will manage profiles as files in `CONFIG_DIR/review/profiles/`, through
new `ws review profiles` subcommands, and give the app an editor that
calls them.

**Commands** (all take `--json`):

```
ws review profiles                       # list, as today, plus source
ws review profiles show <name>           # one profile, with its guide
ws review profiles add <name> --description D [--when W] [--match GLOB]... (--guide-file F | -)
ws review profiles edit <name> [--description D] [--when W] [--match GLOB]... [--no-match] [--guide-file F | -]
ws review profiles rm <name>
```

- **`source`** in the listing is `builtin`, `user`, or `override` (a user
  file replacing a built-in); `builtin` stays for compatibility.
- **`add`** writes `<name>.md`. The name must be a valid profile name
  (`profile_invalid`, exit 2); an existing profile of that name, built-in
  or not, is `profile_exists` (exit 4). The guide comes from a file or
  stdin and can't be empty.
- **`edit`** changes only the fields given. Editing a `user` or
  `override` profile rewrites its file; editing a `builtin` writes a full
  copy to the user directory first, so it becomes an `override`.
- **`rm`** deletes the user's file. For an `override` that restores the
  built-in; a `builtin` with no override is `profile_builtin` (exit 4),
  with advice to set it Off instead. An unknown name is
  `profile_unknown`.
- Writes are a temp file and a rename, under a lock on the directory;
  every write is parsed back before the rename, so a broken file is never
  left behind.

**The app.** Settings' Reviewer profiles table gets **New profile**, and
**Edit** and **Delete** (or **Reset to built-in** for an override) on each
row, with a source badge. The editor dialog edits the description, file
patterns, and guide (Markdown, with a preview), and runs `add` or
`edit`. **Runs** and **Model** stay table columns, kept in `desktop.json`
as now; a new profile's Runs is also written as its `when`, so the CLI's
default matches. Deleting asks first, saying whether the built-in comes
back.

**Running reviews** aren't affected: `ws review start` already copies each
guide into the session's `--agents` definitions, so an edit applies to
reviews started after it.

### Why this option

1. Files are what `profile.Set` already reads, so the CLI, the app, and
   agents share one source; the commands only add safe ways to write them.
2. Overriding instead of editing in place keeps built-ins pristine:
   upgrades still improve the ones nobody touched, and **Reset to
   built-in** is a file delete.
3. Content (description, patterns, guide) lives in the file; per-review
   choices (when, model) stay where 0038 put them, so nothing is stored
   twice.
4. Guides are copied into a session when it starts, so an edit can't
   change a review mid-run.

It's weak in that `when` is in two places for a profile the app created:
the file's default, used by the CLI, and the app's Runs column, which
overrides it for reviews the app starts. And an override doesn't follow
later improvements to its built-in; the listing's `override` source is
how a user notices.

## Alternatives

### Keep profiles in `desktop.json`

**Pros:** no new `ws` commands; the app owns its settings.
**Cons:** agents and the CLI wouldn't see profiles made in the app, and
`ws review start` would need them passed in full on every call.

### Store everything, including model, in the profile file

**Pros:** one record per profile.
**Cons:** the model is a per-person choice that 0038 put in settings, and
mixing it into shareable profile files would make copying a profile copy
someone's model too.

### Let `rm` hide a built-in

Write a tombstone file that hides a built-in completely.

**Pros:** a list with only the profiles you use.
**Cons:** a second mechanism for what **Off** already does, and a hidden
built-in is easy to forget about.

## Consequences

- Amends [0038](0038-review-fans-out-to-profile-subagents.md) (profiles
  can be managed, not only read) and [0016](0016-output-contract.md)
  (error codes `profile_invalid` under exit 2, `profile_exists` and
  `profile_builtin` under exit 4).
- `docs/tools/ws-review.md` and `ws llm review` describe the commands;
  `report.md` describes the editor.
- Conformance: Go tests for each command, including editing a built-in
  (becomes an override), `rm` of an override (built-in returns) and of a
  built-in (refused), invalid and reserved names, an empty guide, and a
  write that wouldn't parse; cargo and Vitest tests for the editor and the
  table's actions.

## References

None beyond the files named above.
