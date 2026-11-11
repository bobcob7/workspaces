import type { LoopHistoryRun, LoopRunStatus } from "../../api";

/** What a run's status says in words, with the skip count for a skipped run. */
export const statusLabel = (run: LoopHistoryRun): string => {
  switch (run.status) {
    case "succeeded":
      return "Succeeded";
    case "failed":
      return "Failed";
    case "start_failed":
      return "Couldn't start";
    case "running":
      return "Running";
    case "gone":
      return "Workspace removed";
    case "done":
      return "Finished, no verdict";
    case "skipped":
      return `Skipped ×${run.repeats}`;
  }
};

/** The chip colour for a run's status. */
export const statusColor = (status: LoopRunStatus): "success" | "error" | "info" | "warning" | "default" => {
  switch (status) {
    case "succeeded":
      return "success";
    case "failed":
    case "start_failed":
      return "error";
    case "running":
      return "info";
    case "gone":
      return "warning";
    case "done":
    case "skipped":
      return "default";
  }
};

/** "14m 30s" style length of a run, or "–" when it has none. */
export const formatDuration = (seconds: number | null): string => {
  if (seconds === null) {
    return "–";
  }
  if (seconds < 60) {
    return `${seconds}s`;
  }
  if (seconds < 3600) {
    return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  }
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
};

/** The one-line result of a run: its verdict, else its error, else its gate's exit. */
export const runSummary = (run: LoopHistoryRun): string => {
  if (run.verdict !== null) {
    return run.verdict.verdict;
  }
  if (run.error !== null) {
    return `${run.error.code}: ${run.error.message}`;
  }
  if (run.gate !== null && run.gate.exitCode !== null) {
    return `exit ${run.gate.exitCode}${run.gate.message === null ? "" : `: ${run.gate.message}`}`;
  }
  return "";
};
