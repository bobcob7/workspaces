title: Anchors as checkable claims
aliases: anchor-claims, checking
related: anchors, file, line, ref, anchor-file-missing
---
An anchor says: this path, at this commit, has this line. That is a factual
claim, and the one an agent gets wrong most confidently: a hallucinated line
number is well-formed, plausibly numbered, and wrong.

So the document carries a ref: a full 40-character commit SHA, never a branch.
A branch names a moving target — an anchor recorded against main means whatever
main points at when someone looks, stale without ever changing.

ws review submit checks those claims against the review workspace's repository,
by default and with no flag, by object lookup — a commit that is not checked
out still resolves. When git cannot answer, the anchors are not passed: the
run fails as review_unverifiable. An anchorless document passes either way.

ws confirms the anchor points somewhere; whether it points at
the right line is the reviewer's job. What the SHA buys is that anyone can
check:

  git show 4f2c1a9:internal/fetch/client.go | sed -n '88,94p'
