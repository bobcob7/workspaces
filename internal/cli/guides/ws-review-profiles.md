# ws review profiles

```
ws review profiles [--json]
ws review profiles show [--json] <name>
ws review profiles add [--json] <name> --description <d> [--when <w>] [--match <glob>]... (--guide-file <f> | -)
ws review profiles edit [--json] <name> [--description <d>] [--when <w>] [--match <glob>]... [--no-match] [--guide-file <f> | -]
ws review profiles rm [--json] <name>
```

Lists every review profile, built in and your own
(`$CONFIG_DIR/review/profiles/<name>.md`), with `config.toml`'s
`[review.profiles.<name>]` applied: `when` (`always`, `auto`, or `off`),
the file patterns that pull an `auto` profile in, and the model (`""` for
the session's).

`source` says where each comes from: `builtin`, `user` (a profile you
added), or `override` (your file replacing the built-in of that name; it
no longer follows improvements to the built-in). `builtin` is kept for
compatibility, and is true only for `source: builtin`.

`show`, `add`, `edit`, and `rm` manage profiles; run
`ws llm review profiles <op>` for each one's guide.

## JSON output

```json
{"profiles": [{"name": "go", "description": "Go: concurrency, error wrapping, …", "when": "auto",
               "match": ["*.go", "go.mod"], "model": "", "builtin": true, "source": "builtin"}],
 "warnings": []}
```

Without `--json`, a table: `NAME`, `SOURCE`, `WHEN`, `MATCH`, `MODEL`,
and `DESCRIPTION`.

## Exit codes

`usage` for an unknown operation; `config_invalid` for a malformed
`config.toml`. A profile file that can't be parsed is skipped with a
warning.

## Examples

```console
$ ws review profiles --json
```
