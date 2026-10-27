title: Check tiers and exit codes
aliases: exit-codes, severity, strict
related: schema, ref-unknown, priority-flat
---
Checks come in three tiers answering different questions.

Structural checks ask "is this a document?" — schema conformance, unique ids,
safe paths, well-formed refs. Always run, always hard.

Verification checks ask "is this about anything real?" — does this path exist at
this ref, does the file have this many lines. Always run, and cannot be
disabled or demoted: an anchor claim that goes unconfirmed fails the run.

Advisory checks ask "is this a good review?" — a thin body, a suggestion with no
stated cost, every comment at priority 9. Always run, never fatal; no
flag silences one.

Exit 0 is stored. Exit 1 with review_invalid means revise the review and
resubmit; with review_unverifiable, the anchors could not be checked — the
working tree diverged or the commit is missing — and resubmitting will not
help: stop and report it. Exit 2 means fix the invocation, exit 3 means
this is not a review workspace. Document-unparseable and a diverged working
tree both gate the rest; every other run reports everything findable,
skipped checks included.
