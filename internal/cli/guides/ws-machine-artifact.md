# ws machine artifact

```
ws machine artifact add <PATH|-> [--name <name>] [--title <text>] [--note <text>]
ws machine artifact list [--kind image|markdown|text] [--mine] [--limit <n>]
ws machine artifact show <ID>
ws machine artifact get <ID> [--rev <n>] [--out <dir>]
ws machine artifact put <ID> <PATH|-> [--base-rev <n>] [--note <text>]
ws machine artifact rm <ID>...
ws machine artifact comment list <ID> [--open]
ws machine artifact comment add <ID> (--body <text> | --body-file <path|->) [--reply <thread> | --quote <text> [--line <n>]]
ws machine artifact comment resolve <ID> <THREAD> [--reopen]
```

Shares files with people. People upload screenshots and logs, and read what
you upload, rendered, in the desktop app. You show them a document you wrote,
such as a design pitch or a report, and they comment on it.

When a prompt says `@artifact:art-1a2b3c4d`, run
`ws machine artifact get art-1a2b3c4d` and read the file at the printed
`path` with your Read tool; if the read is refused, rerun with
`--out <a directory inside your working directory>`. To show a person a
document, `ws machine artifact add pitch.md --title "…"` and give them the
`reference`. When they say they've commented, read the threads, change the
file, `ws machine artifact put <id> pitch.md --base-rev <latest>`, then reply
to each thread you addressed and resolve it.

Output is always JSON; `--json` changes nothing. Errors are one JSON line on
stderr. Run inside your workspace: outside one, every command fails with
`outside_workspace`.

## Subcommands

- `add`: store a file as a new artifact. `-` reads stdin and needs `--name`.
- `list`: artifacts, latest first. `--mine` keeps those your workspace tree created.
- `show`: one artifact with its revisions.
- `get`: copy a revision into your workspace and print its path.
- `put`: add a revision to an artifact, moving its comment anchors.
- `rm`: remove an artifact your workspace tree created.
- `comment`: read the threads people left, reply to them and resolve them
  (`comment list`, `comment add`, `comment resolve`).

Each takes an `<ID>` or a reference `@artifact:<id>` with an optional `/r<n>`.

## Rules

- A file is text (UTF-8, Markdown or plain), PNG, JPEG, GIF, WebP or SVG.
  Empty files, other binary files and PDFs are refused.
- Limits: 1 MiB for text, Markdown and SVG, 10 MiB for other images. A name is
  1 to 128 characters with no slash; a title or note is one line of up to 200.
- An artifact keeps at most 100 revisions, and the store holds 2 GiB.
- A revision keeps its artifact's kind: a Markdown artifact can't get a PNG.

## Exit codes

| Situation                                                        | Error code             | Exit |
| ---------------------------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                                          | `outside_workspace`    | 2    |
| A type, size, name, title, note or revision isn't allowed        | `artifact_invalid`     | 2    |
| No artifact, or no revision of it, with that id                  | `artifact_unknown`     | 3    |
| `put --base-rev` isn't the latest revision                      | `artifact_conflict`    | 4    |
| An artifact or the store is at a limit                           | `artifact_full`        | 4    |
| `rm` of an artifact another workspace tree created               | `artifact_forbidden`   | 4    |
| Run on an enrolled environment's agent machine, if it can't reach the controller's artifact API | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact add pitch.md --title "Pitch: cache sharing"
$ ws machine artifact get @artifact:art-1a2b3c4d
$ ws machine artifact comment list art-1a2b3c4d --open
$ ws machine artifact put art-1a2b3c4d pitch.md --base-rev 1 --note "Done the threads"
```
