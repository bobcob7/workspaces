import { describe, expect, it } from "vitest";
import type { LoopRunStatus } from "../../api";
import { loopHistoryRun } from "../../test/fixtures";
import { formatDuration, runSummary, statusColor, statusLabel } from "./history";

describe("statusLabel", () => {
  it.each<[LoopRunStatus, string]>([
    ["succeeded", "Succeeded"],
    ["failed", "Failed"],
    ["start_failed", "Couldn't start"],
    ["running", "Running"],
    ["gone", "Workspace removed"],
    ["done", "Finished, no verdict"],
    ["blocked", "Blocked"],
    ["needs_attention", "Needs attention"],
  ])("labels a %s run", (status, label) => {
    expect(statusLabel(loopHistoryRun({ status }))).toBe(label);
  });

  it("counts the repeats of a skipped run", () => {
    expect(statusLabel(loopHistoryRun({ status: "skipped", repeats: 12 }))).toBe("Skipped ×12");
  });
});

describe("statusColor", () => {
  it.each<[LoopRunStatus, "success" | "error" | "info" | "warning" | "default"]>([
    ["succeeded", "success"],
    ["failed", "error"],
    ["start_failed", "error"],
    ["running", "info"],
    ["gone", "warning"],
    ["blocked", "warning"],
    ["needs_attention", "warning"],
    ["done", "default"],
    ["skipped", "default"],
  ])("colours a %s run", (status, color) => {
    expect(statusColor(status)).toBe(color);
  });
});

describe("formatDuration", () => {
  it.each<[number | null, string]>([
    [null, "–"],
    [59, "59s"],
    [870, "14m 30s"],
    [3720, "1h 2m"],
  ])("formats %p seconds as %s", (seconds, text) => {
    expect(formatDuration(seconds)).toBe(text);
  });
});

describe("runSummary", () => {
  it("gives the verdict first", () => {
    const run = loopHistoryRun({
      error: { code: "x", message: "ignored" },
      verdict: { verdict: "merged", outcome: "pass", recordedAt: "2026-10-24T08:14:00Z", report: null },
    });
    expect(runSummary(run)).toBe("merged");
  });

  it("gives the code and message of an error without a verdict", () => {
    const run = loopHistoryRun({ status: "start_failed", error: { code: "spawn", message: "no such file" } });
    expect(runSummary(run)).toBe("spawn: no such file");
  });

  it("gives the exit code and message of a skipped run's gate", () => {
    const gate = { at: "2026-10-24T08:00:00Z", outcome: "skipped" as const, exitCode: 3, message: "nothing to do" };
    expect(runSummary(loopHistoryRun({ status: "skipped", gate }))).toBe("exit 3: nothing to do");
  });

  it("is empty for a run with nothing to say", () => {
    expect(runSummary(loopHistoryRun())).toBe("");
  });
});
