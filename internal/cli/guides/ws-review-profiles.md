# ws review profiles

```
ws review profiles [--json]
```

Lists every review profile, built in and your own
(`$CONFIG_DIR/review/profiles/<name>.md`), with `config.toml`'s
`[review.profiles.<name>]` applied: `when` (`always`, `auto`, or `off`),
the file patterns that pull an `auto` profile in, and the model (`""` for
the session's).

## JSON output

```json
{"profiles": [{"name": "go", "description": "Go: concurrency, error wrapping, …", "when": "auto",
               "match": ["*.go", "go.mod"], "model": "", "builtin": true}],
 "warnings": []}
```

Without `--json`, a table: `NAME`, `WHEN`, `MATCH`, `MODEL`, and
`DESCRIPTION`.

## Exit codes

`usage` for any argument; `config_invalid` for a malformed `config.toml`.
A profile file that can't be parsed is skipped with a warning.

## Examples

```console
$ ws review profiles --json
```
