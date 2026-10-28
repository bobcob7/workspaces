# ws review profiles edit

```
ws review profiles edit [--json] <name> [--description <d>] [--when <w>] [--match <glob>]... [--no-match] [--guide-file <f> | -]
```

Changes a review profile's fields; those not given are kept. `--match`
replaces the patterns, and `--no-match` removes them.

Editing your profile, or your override of a built-in, rewrites its file.
Editing a built-in writes a full copy to
`$CONFIG_DIR/review/profiles/<name>.md` first, so it becomes an
`override`; `ws review profiles rm <name>` undoes that. An override no
longer follows improvements to the built-in.

The file is written to a temp file, parsed back, and renamed into place,
so an edit that wouldn't load leaves the profile as it was. Reviews
already running keep the guide they started with.

## Flags

| Flag                  | Meaning                                                         |
| --------------------- | --------------------------------------------------------------- |
| `--description <d>`   | One line, at most 120 characters                                |
| `--when <w>`          | `always`, `auto`, or `off`                                      |
| `--match <glob>`      | Replace the file patterns; repeatable                           |
| `--no-match`          | Remove every file pattern; an `auto` profile needs `--when` too |
| `--guide-file <f>`    | Replace the guide with this file's content                      |
| `-`                   | After the name: replace the guide with stdin                    |
| `--json`              | Print the profile as `ws review profiles show` does             |

## JSON output

```json
{"name": "go", "description": "Go, our way", "when": "auto", "match": ["*.go", "go.mod"],
 "model": "", "builtin": false, "source": "override", "guide": "Anchor a concurrency finding…",
 "path": "/home/me/.config/ws/review/profiles/go.md", "warnings": []}
```

## Exit codes

| Situation                                        | Error code        | Exit |
| ------------------------------------------------ | ----------------- | ---- |
| Nothing to change, `--match` with `--no-match`, both `--guide-file` and `-`, a bad `--when` | `usage` | 2 |
| An invalid or reserved name, an empty guide, or a result that wouldn't parse | `profile_invalid` | 2 |
| No profile has the name                          | `profile_unknown` | 2    |
| Your file for the profile can't be parsed; fix or remove it | `config_invalid` | 1 |

## Examples

```console
$ ws review profiles edit --json go --guide-file our-go.md
$ ws review profiles edit --json docs --when always --no-match
```
