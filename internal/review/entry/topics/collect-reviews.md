title: Combining stored reviews
aliases: collecting, combined-reviews
related: store, id
---
Every stored submission for one repo and commit combines into one report:
`submissions` and `comments`, neither a review-document field, because the
report is not one.

Two submissions can share an `id`. Each comment's id is qualified to stay
unique: `profile:origin_id` when its submission is current for that
profile, `#ordinal:origin_id` otherwise — superseded. `ordinal` is
positional and shifts on the next submission; treat a `#N:id` form as good
for one call only.

Resubmitting under the same profile supersedes (`topic:store`): the
earlier submission gets `superseded_by`, naming the superseding ordinal.
Nothing is ever dropped — filter out anything carrying `superseded_by` for
current opinions only.

The report renders as JSON, Markdown, or HTML. None validates as a review
document.
