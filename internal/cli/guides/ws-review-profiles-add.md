# ws review profiles add

```
ws review profiles add [--json] <name> --description <d> [--when <w>] [--match <glob>]... (--guide-file <f> | -)
```

Adds a review profile: writes `$CONFIG_DIR/review/profiles/<name>.md`.
The name is lowercase letters, digits, and hyphens, and isn't
`decisions`, which is reserved. It can't be any profile's already, built
in or yours; to change one, use `ws review profiles edit`.

The guide, what the reviewer is told to look for, comes from
`--guide-file` or, with `-`, from stdin, and can't be empty. The file is
written to a temp file, parsed back, and renamed into place, so a profile
that wouldn't load is never left behind.

Reviews already running aren't affected; the profile joins reviews
started afterwards.

## Flags

| Flag                  | Meaning                                                         |
| --------------------- | --------------------------------------------------------------- |
| `--description <d>`   | Required. One line, at most 120 characters                      |
| `--when <w>`          | `always`, `auto`, or `off`; without it, `auto` if there are patterns, else `always` |
| `--match <glob>`      | A file pattern that pulls an `auto` profile in; repeatable. Without a `/` it matches the base name |
| `--guide-file <f>`    | Read the guide from this file                                   |
| `-`                   | After the name: read the guide from stdin                       |
| `--json`              | Print the profile as `ws review profiles show` does             |

## JSON output

```json
{"name": "docs", "description": "Docs: accuracy and links", "when": "auto", "match": ["*.md"],
 "model": "", "builtin": false, "source": "user", "guide": "Check every link…",
 "path": "/home/me/.config/ws/review/profiles/docs.md", "warnings": []}
```

## Exit codes

| Situation                                        | Error code        | Exit |
| ------------------------------------------------ | ----------------- | ---- |
| No `--description`, no guide or both `--guide-file` and `-`, a bad `--when` | `usage` | 2 |
| An invalid or reserved name, an empty guide, or content that wouldn't parse, such as `--when auto` without patterns | `profile_invalid` | 2 |
| A profile already has the name                   | `profile_exists`  | 4    |

## Examples

```console
$ ws review profiles add --json docs --description "Docs: accuracy and links" --match '*.md' --guide-file docs-guide.md
$ ws review profiles add --json docs --description "Docs: accuracy and links" - < docs-guide.md
```
