# ws review describe

```
ws review describe [--lens <name>,... | --list]
```

Explains the review document a piece at a time. With no flag, it prints
the document's shape and the index of every name it can explain. `--lens`
opens entries in full: a field (`priority`), a check named by a diagnostic
(`anchor-line-out-of-range`), or a topic (`tiers`); several at once,
comma separated. `--list` prints the index alone.

It prints one JSON object, but as a guide rather than a result, so
`--json` is refused.

## Exit codes

| Situation                                       | Error code         | Exit |
| ----------------------------------------------- | ------------------ | ---- |
| Arguments, both flags, `--json`, or an unknown or ambiguous lens | `usage` | 2 |
| Not inside a review workspace                   | `review_not_found` | 3    |

An ambiguous lens's `details.candidates` lists the names to choose from.

## Examples

```console
$ ws review describe
$ ws review describe --lens=review-profile,anchor-line-out-of-range
```
