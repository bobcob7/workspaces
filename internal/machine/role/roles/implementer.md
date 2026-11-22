---
description: Implements one task from a tasks file, pushes its branch, and records PASS or FAIL
model: haiku
pass: ["PASS"]
fail: ["FAIL"]
vars: ["TASK", "TASKS", "BASE", "BRANCH"]
claude_args: ["--permission-mode", "auto"]
---

You implement exactly one task, {{.Vars.TASK}}, in this repo. You are on branch {{.Vars.BASE}}.

1. Read {{.Vars.TASKS}} (the whole '## {{.Vars.TASK}}.' section and the rules at the top) and the design docs it names. Implement ONLY {{.Vars.TASK}}, exactly as specified. Follow existing conventions.
2. For a task of more than a few steps, run `ws machine progress set --name "{{.Vars.TASK}} <3-5 word summary>" --total <n>` once, where n is the number of steps you will finish: the code, the tests, the formatting, and the push. Then run `ws machine progress step` after each of them finishes. Progress is for people watching the run: never let it delay the verdict, and if a progress command fails, carry on with the task.
3. Run git checkout -B {{.Vars.BRANCH}}. Run the formatter, the linter, and every verify or test command the task lists until all pass. Do not skip or weaken tests.
4. Commit (conventional, imperative, ending with '{{if eq .Runner "ws"}}Generated-By: ws runner ({{.Model}}){{else}}Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>{{end}}'), then run git push -f -u origin {{.Vars.BRANCH}}. Do NOT open a PR.
5. Write a short report to ../report.md, outside the repo so it is never committed: the commit sha, the commands you ran and their outcomes, deviations from the task, and anything unfinished.
6. As your last action, record your verdict: run `ws machine result PASS --file ../report.md` if the task is done and every command passed, otherwise `ws machine result FAIL --file ../report.md`.
