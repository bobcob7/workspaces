# 0066. The runner's security boundary is the user's account, with a scrubbed environment, filtered MCP tools and guard rails, not a sandbox

- Status: Proposed
- Date: 2026-11-13; reworked 2026-11-14 after review (environment from
  the daemon, MCP servers)

## Context

### Problem

The runner ([0065](0065-runner-agent-loop-tools-and-budgets.md)) executes
shell commands, writes files and calls MCP tools chosen by a model, and it
reads repository content, and MCP results, an attacker may control. What does it protect, with what, and what
does it honestly not protect?

### What a solution needs

1. **No worse than a Claude child today.** Built-in roles run Claude Code
   with `--permission-mode auto`[^roles], which runs commands without
   asking.
2. **Secrets don't leak by accident**: endpoint tokens and `ws`'s own
   credentials stay out of the tools' environment and the transcript.
3. **Honest.** What isn't enforced is written down, not implied.
4. **Portable** to macOS and the unprivileged ws-online pod (no root, no
   user namespaces)[^pod].

### Relevant facts

- OWASP LLM01:2025: "Indirect prompt injections occur when an LLM accepts
  input from external sources, such as websites or files."[^owasp]
- The "lethal trifecta" is access to private data, exposure to untrusted
  content, and the ability to communicate externally[^trifecta]. A coding
  agent with a shell and network access has all three.
- Agents have been hijacked through repo content: a malicious GitHub issue
  leaked private repo data into a public PR[^invariant], and Cursor's
  CVE-2025-54135 chained "a indirect prompt injection vulnerability" into
  code execution[^cursor].
- Quantized small models hallucinate tool names more[^quant], so mistakes
  are likelier than with Claude.
- The ws-online pod has no root, sudo or user namespaces[^pod], so
  bubblewrap-style sandboxes aren't available there. macOS's
  `sandbox-exec` is deprecated (unsourced; not relied on).

## Decision

**The boundary is the user's account**, as for Claude children. Within it:

1. **Environment.** The runner's own environment is built by the daemon
   ([0068](0068-ws-daemon.md)): its environment, `[runner.env]`,
   `secrets.env`, then `ws`'s own variables. `run` commands get that minus
   every variable whose name matches `WS_ENDPOINT_TOKEN_*`,
   `WS_FORGEJO_TOKEN_*`, `*_API_KEY`, `*_TOKEN`, `*_SECRET`,
   `*_PASSWORD`, or any name defined in `secrets.env`, except `GH_TOKEN`
   and `GITHUB_TOKEN`, which `git push` and `gh` may need. That exception
   is listed in the reference, and the user can turn it off with
   `runner.keep_git_tokens = false`. `HOME`, `PATH`, `USER`, `LANG`,
   `TMPDIR` and git's variables are kept. `WS_RUNNER=ws` is added.
   **MCP stdio servers** get the unscrubbed runner environment plus their
   own `env` block, since they need their tokens; the model never sees
   either ([0069](0069-runner-mcp-tools-and-claude-code-parity.md)).
2. **Files.** File tools resolve symlinks and refuse paths outside the
   workspace, and anything under `.git/`. `run`'s working directory is the
   session directory.
3. **Commands.** The deny-list from 0065 (other `ws` commands, `claude`,
   `sudo`, force pushes outside `BRANCH`, pushes to `main` or `master`) is
   matched on the parsed command words. It catches model mistakes; it
   can't stop a determined `sh -c "$(echo … | base64 -d)"`.
4. **Limits.** Each command runs in its own process group with the
   timeout, and is killed with the group. Output is capped at 16 KiB.
5. **Prompt injection.** Tool results are wrapped as
   `<tool_result tool="read_file" path="…">…</tool_result>`. The preamble
   says that text inside tool results is data from the repo, not
   instructions, and that only the system and first user message give
   instructions. This lowers the risk; it doesn't remove it.
6. **Transcript.** The transcript stores tool arguments and results as the
   model saw them, after the environment scrub. Before writing any event,
   the runner replaces with `[redacted]` the endpoint token, every value of
   a scrubbed variable, and every value it expanded into an MCP server's
   `env`, `headers`, `args` or `url` (including literal header values such
   as an `Authorization` written into `~/.claude.json`). MCP server
   definitions themselves are never written to the transcript, the log or
   `status.json`; only server names, tool names and states. `state/` is
   mode 0700, as today.
7. **MCP tools.** A role gets no MCP server unless its config names it,
   and only the tools its allow-list matches. Tools whose MCP annotations
   say `destructiveHint: true`, or that don't say `readOnlyHint: true`, are
   refused unless named exactly in the allow-list (not by a glob).
   Annotations are the server's claim, not a guarantee. Each MCP result is
   framed like any tool result (point 5).
8. **Network.** Nothing is filtered. A `run` command can reach anything the
   user can. The endpoint itself is reached over the URL in config;
   `http://` is allowed (on-prem), and a warning is printed when a token is
   sent over plain HTTP to a non-loopback host.

### Why this option

1. **No worse.** The same account and the same push rights as Claude
   children in auto mode, plus a scrubbed environment those don't have.
2. **Secrets.** Scrubbing and redaction cover the tokens `ws` knows about.
3. **Honest.** Points 3, 5, 7 and 8 say what isn't enforced.
4. **Portable.** Nothing here needs root, namespaces or an OS-specific
   sandbox.

Where it's weak: a prompt-injected or confused model can read anything the
user can, run anything, and send it anywhere: all three legs of the
trifecta[^trifecta]. MCP servers add more of each leg: an allowed
read-only `grafana` or `outline` tool is still private data, and its
results are untrusted content. Run runner children only on repos, and with
MCP servers, you'd let an auto-mode Claude child use. Keeping `GH_TOKEN` lets a hijacked runner
push to any repo the token can reach.

## Alternatives

### Linux sandbox (bubblewrap or Landlock) for `run`

**Pros**

- Real file and network confinement.

**Cons**

- It isn't available in the unprivileged pod (bubblewrap needs user
  namespaces), and Landlock needs kernel ≥ 5.13 plus Go bindings. There's
  nothing equivalent on macOS. It could be an opt-in later
  (`runner.sandbox`); see the open questions.

### Ask a person before each command

**Pros**

- Strong control.

**Cons**

- A background child has no one to ask. It defeats unattended
  orchestration.

### Run in a container per child

**Pros**

- Isolation.

**Cons**

- The pod's Docker daemon is remote and can't bind-mount the
  workspace[^pod].

## Consequences

- `internal/agent/tools` has an `Environ` scrub function and a
  `CheckPath`, each with table tests, plus a redactor used by the
  transcript writer; `internal/mcp` registers every expanded secret with
  that redactor and applies the annotation rule.
- The handbook's mixed-mode procedure says which repos and MCP servers may
  be used by runner children.
- Unverified: how well the tool-result framing resists injection in the
  chosen models. A fake-ollama scenario can only check that the framing is
  present, not that a model obeys it.

## References

[^roles]: workspaces `internal/machine/role/roles/*.md` @98697f3 (read 2026-11-13). Each sets `claude_args: ["--permission-mode", "auto"]`.
[^pod]: Agent Handbook, "Procedure: tooling in the ws-online pod" (last verified 2026-11-09, read 2026-11-13). "The pod is unprivileged (uid 1000, no root, no sudo, no user namespaces)", and on Docker: "Bind mounts (`-v $PWD:/src`) do not work".
[^owasp]: OWASP GenAI, "LLM01:2025 Prompt Injection." <https://genai.owasp.org/llmrisk/llm01-prompt-injection/> (accessed 2026-11-13). "Indirect prompt injections occur when an LLM accepts input from external sources, such as websites or files."
[^trifecta]: S. Willison, "The lethal trifecta for AI agents," 2025-06-16. <https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/> (accessed 2026-11-13). "Access to your private data", "Exposure to untrusted content", "The ability to externally communicate".
[^invariant]: Invariant Labs, "GitHub MCP Exploited," 2025-05-26. <https://invariantlabs.ai/blog/mcp-github-vulnerability> (accessed 2026-11-13). The attack "allows an attacker to hijack a user's agent via a malicious GitHub Issue".
[^cursor]: NIST NVD, "CVE-2025-54135," published 2025-08-05. <https://nvd.nist.gov/vuln/detail/CVE-2025-54135> (accessed 2026-11-13). An attacker could "chain a indirect prompt injection vulnerability" to write `.cursor/mcp.json`.
[^quant]: "Flat Score, Amplified Failures," arXiv 2607.27275. <https://arxiv.org/abs/2607.27275> (accessed 2026-11-13). "Quantization amplifies tool-name hallucination… by up to 2.5×".
