import type { LoopGate } from "../../api";
import { age } from "../../format";

const suffix = (message: string | null): string => (message === null ? "" : `: ${message}`);

const outcomeText = (gate: LoopGate): string => {
  switch (gate.outcome) {
    case "passed":
      return "passed";
    case "skipped":
      return `skipped${gate.exitCode === null ? "" : ` (exit ${gate.exitCode})`}${suffix(gate.message)}`;
    case "timed_out":
      return `timed out${suffix(gate.message)}`;
    case "failed":
      return `failed${suffix(gate.message)}`;
  }
};

/** A loop's last gate, for the details: "never", or "<age> · <what>". */
export const gateText = (gate: LoopGate | null): string => {
  if (gate === null) {
    return "never";
  }
  return `${age(gate.at)} · ${outcomeText(gate)}`;
};
