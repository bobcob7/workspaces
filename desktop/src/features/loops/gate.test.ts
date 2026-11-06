import { describe, expect, it } from "vitest";
import type { LoopGate } from "../../api";
import { gateText } from "./gate";

const at = new Date().toISOString();

const gate = (overrides: Partial<LoopGate>): LoopGate => ({ at, outcome: "passed", exitCode: 0, message: null, ...overrides });

describe("gateText", () => {
  it("says never when there is no gate", () => {
    expect(gateText(null)).toBe("never");
  });

  it("ends with passed", () => {
    expect(gateText(gate({ outcome: "passed" }))).toMatch(/passed$/);
  });

  it("gives the exit code for a skip without a message", () => {
    expect(gateText(gate({ outcome: "skipped", exitCode: 2, message: null }))).toMatch(/skipped \(exit 2\)$/);
  });

  it("gives the message for a timeout", () => {
    expect(gateText(gate({ outcome: "timed_out", exitCode: null, message: "script timed out after 1m0s" }))).toMatch(
      /timed out: script timed out after 1m0s$/,
    );
  });

  it("gives the message for a failure", () => {
    expect(gateText(gate({ outcome: "failed", exitCode: null, message: "exec: no such file" }))).toMatch(
      /failed: exec: no such file$/,
    );
  });
});
