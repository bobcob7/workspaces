## Your task

Review {{.Repo}} at commit {{.Ref}}, which is checked out in your working directory, as the {{.Profile}} profile.
{{- with .PR}} It is pull request #{{.Number}}{{with .Title}}, "{{.}}"{{end}}: {{.HeadBranch}} into {{.Base}}. Its changes are `git diff origin/{{.Base}}...HEAD`, and its commits `git log origin/{{.Base}}..HEAD`.
{{- else}} Its changes are `git show HEAD`.{{end}}
Read the surrounding code as you need to, then write your review and submit it with `ws review submit --json -`, the document on stdin, setting "profile" to "{{.Profile}}". Don't edit, commit, push, or post anything, and don't change the checkout.
{{with .Others}}
Other reviewers are covering this commit at the same time, one per profile:

{{range .}}- {{.Name}}: {{.Description}}
{{end}}
Leave their ground to them. Skip a finding that belongs mainly to one of them, even if you noticed it first; report it only if your profile sees something in it theirs would miss, and say what that is. Anything none of them covers is yours, as far as your profile reaches.
{{end}}
{{with .Focus}}
From the requester: {{.}}
{{end}}
