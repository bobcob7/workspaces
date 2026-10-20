# ws cache resolve

```
ws cache resolve [--json] <spec>...
```

Resolves specs to canonical repo ids (`host/owner/name`) without cloning
or fetching. A short name that isn't cached is probed upstream under each
configured default owner in turn. URLs are accepted, so it also maps an
upstream URL to its id. The ref is echoed, not checked.

## When to use it

To check what a spec means before `ws claude`, or to turn a URL into an
id.

## JSON output

```json
{"repos": [{"id": "github.com/bobcob7/buff-bear", "ref": null,
            "upstream": "https://github.com/bobcob7/buff-bear.git",
            "cached": true, "source": "cache"}],
 "warnings": []}
```

- `source`: `spec` (the spec names the host), `cache` (found by short name
  in the cache), `default` (an uncached `owner/name`, taken to be on the
  default host), or `probe` (found under a default owner upstream).
- `upstream`: for a cached repo, the URL it was cloned from; a URL spec
  that differs adds a warning.

Without `--json`, `<id>` or `<id>@<ref>` per spec.

## Exit codes

| Situation                                        | Error code       | Exit |
| ------------------------------------------------ | ---------------- | ---- |
| No spec                                          | `usage`          | 2    |
| Unparsable spec                                  | `spec_invalid`   | 2    |
| No such repo                                     | `repo_not_found` | 3    |
| Several cached repos match; `details.candidates` lists them | `repo_ambiguous` | 3 |
| Upstream unreachable while probing               | `fetch_failed`   | 1    |

## Examples

```console
$ ws cache resolve --json buff-bear@feat/new-thing git@github.com:bobcob7/api.git
```
