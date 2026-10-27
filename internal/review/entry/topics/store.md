title: The review store
aliases: storing
related: exit-codes, collect-reviews
---
Every `ws review submit` that passes is stored, outside the workspace, so
the review outlives it: one file per profile for the reviewed repo and
commit. A run that fails stores nothing.

Storing needs no flag: it is simply what `submit` does. The result names
the stored file in `path`.

Resubmitting under the same profile supersedes rather than replaces: the
earlier submission is kept and marked superseded, and its ids are
requalified from `profile:id` to `#N:id`, so an id from an earlier report
can stop resolving.

A store that cannot be written — read-only home, full disk — fails with
`internal` (exit 1): the machine is at fault, not the review. Stop and
report it.
