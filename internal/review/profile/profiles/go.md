---
description: Go: concurrency, error wrapping, context handling, and resource lifetime
when: auto
match: ["*.go", "go.mod"]
---

Anchor a concurrency finding at the goroutine or channel that leaks, not at the
symptom downstream.

Look hardest at:

- Errors. Wrapped with `%w`, matched with `errors.Is`/`errors.As` at
  boundaries. A sentinel compared with `==` across a package boundary is a
  finding; so is one swallowed for a linter.
- `context`. First parameter, propagated rather than re-derived, and honored —
  a loop that never selects on `ctx.Done()` ignores every cancellation above
  it.
- Lifetime and aliasing. `defer` inside a loop, a handle closed only on the
  happy path, a goroutine with no way to stop, a struct copied while holding
  its mutex.
- Cleanup that runs on a context already cancelled: a compensating write
  or delete after the request's ctx is done needs its own, bounded context.

Interfaces belong at the consumer. A wide interface with one implementation is
still a finding: file it at the bottom of the priority scale, not nowhere.

Do not report anything `gofumpt` or the vet suite would fix — that spends
attention neither the reviewer nor the author chose to spend.
