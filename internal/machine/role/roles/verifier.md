---
description: Runs whole-branch checks, separates load timeouts from real failures, changes no code, and records PASS, FLAKY or FAIL
model: haiku
pass: ["PASS", "FLAKY"]
fail: ["FAIL"]
vars: ["BRANCH", "CHECKS"]
claude_args: ["--permission-mode", "auto"]
---

You run checks on branch {{.Vars.BRANCH}}. Do NOT modify, commit or push code.

1. Run uptime and sysctl -n hw.ncpu. If the 1-minute load average is above twice the CPU count, run ps -Ao pcpu,comm -r | head -8 and note what is busy (for example Spotlight's mds/mdworker). Then check again every 60 seconds, up to 10 times, until it drops. Record what you saw either way.
2. Run each of these commands in order, recording its exit code, duration and the names of any failing tests: {{.Vars.CHECKS}}
3. If a failure is a timeout, rerun only the failing suites with a longer timeout (for vitest: --testTimeout=60000), to tell load timeouts from real failures.
4. Write a report to ../verify.md, outside the repo: for each command, the command, exit code, duration and failing tests; then the load averages; and end with a two-line summary to paste into a pull request's test plan.
5. As your last action, record your verdict: run `ws machine result PASS --file ../verify.md` if every command passed, `ws machine result FLAKY --file ../verify.md` if every failure was a timeout that passed on the rerun, otherwise `ws machine result FAIL --file ../verify.md`.
