# ws cache resolve

```
ws cache resolve [--json] <spec>...
```

Resolves specs to canonical repo ids (`host/owner/name`) without cloning
or fetching. A short name that isn't cached is probed upstream under each
configured host in order: a bare name under each default owner in turn, and
an `owner/name` on the first host that has it. URLs are accepted, so it also maps an
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
  in the cache), `default` (an uncached `owner/name` when one host is
  configured, taken to be on it), or `probe` (found upstream: a bare name
  under a default owner, or an `owner/name` when several hosts are
  configured, on the first host that has it).
- `upstream`: for a cached repo, the URL it was cloned from; a URL spec
  that differs adds a warning.

`warnings` notes a repo that is also on a later configured host (name the
host to use the other), and a host that couldn't be reached while probing.

Without `--json`, `<id>` or `<id>@<ref>` per spec.

## Exit codes

| Situation                                        | Error code       | Exit |
| ------------------------------------------------ | ---------------- | ---- |
| No spec                                          | `usage`          | 2    |
| Unparsable spec                                  | `spec_invalid`   | 2    |
| No such repo                                     | `repo_not_found` | 3    |
| Several cached repos match; `details.candidates` lists them | `repo_ambiguous` | 3 |
| No configured host has it, and at least one couldn't be reached | `fetch_failed` | 1 |

## Examples

```console
$ ws cache resolve --json buff-bear@feat/new-thing git@github.com:bobcob7/api.git
```
