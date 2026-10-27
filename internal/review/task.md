## Your task

Review {{.Repo}} at commit {{.Ref}}, which is checked out in your working directory, as the {{.Profile}} profile.
{{- with .PR}} It is pull request #{{.Number}}{{with .Title}}, "{{.}}"{{end}}: {{.HeadBranch}} into {{.Base}}. Its changes are `git diff origin/{{.Base}}...HEAD`, and its commits `git log origin/{{.Base}}..HEAD`.
{{- else}} Its changes are `git show HEAD`.{{end}}
Read the surrounding code as you need to, then write your review and submit it with `ws review submit --json -`, the document on stdin, setting "profile" to "{{.Profile}}". Don't edit, commit, push, or post anything, and don't change the checkout.
{{with .Focus}}
From the requester: {{.}}
{{end}}
