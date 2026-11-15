import type { Entry, WsRunner, WsRunnerUsage } from "../../api";

const apiLabels: Readonly<Record<string, string>> = { openai: "OpenAI-compatible" };

/** A token count as the panes show it: 210k, 9.1k, 512. */
export const tokenCount = (count: number): string => {
  if (count >= 10_000) {
    return `${Math.round(count / 1000)}k`;
  }
  if (count >= 1000) {
    return `${(count / 1000).toFixed(1)}k`;
  }
  return String(count);
};

export const tokensLine = (usage: WsRunnerUsage): string =>
  `tokens ${tokenCount(usage.promptTokens)} in · ${tokenCount(usage.completionTokens)} out`;

export const apiLabel = (api: string): string => apiLabels[api] ?? api;

/** A `ws` runner's step, as the summary line shows it, once it has one. */
export const stepLabel = (run: WsRunner | null): string | null =>
  run?.step != null && run.maxSteps != null ? `step ${run.step}/${run.maxSteps}` : null;

/** The endpoint's name, URL and API, as the tooltip and the pane give them. */
export const endpointLabel = (run: WsRunner): string | null => {
  if (run.endpoint === null) {
    return null;
  }
  const details = [run.url, run.api === null ? null : apiLabel(run.api)].filter(
    (part): part is string => part !== null && part !== "",
  );
  return details.length > 0 ? `${run.endpoint} (${details.join(", ")})` : run.endpoint;
};

/**
 * The row's tooltip for an agent's model: a `ws` child's model, alias and
 * endpoint, or a Claude agent's model; undefined when there is no model.
 */
export const modelTooltip = (entry: Entry): string | undefined => {
  if (entry.model === null) {
    return undefined;
  }
  if (entry.runner !== "ws") {
    return `${entry.model} · Claude Code`;
  }
  const parts = [entry.model];
  if (entry.wsRunner?.modelAlias) {
    parts.push(`alias ${entry.wsRunner.modelAlias}`);
  }
  const endpoint = entry.wsRunner === null ? null : endpointLabel(entry.wsRunner);
  if (endpoint !== null) {
    parts.push(`endpoint ${endpoint}`);
  }
  return parts.join(" · ");
};

/** Whether a `ws` runner is still going, so Stop applies. */
export const isRunnerRunning = (entry: Entry): boolean =>
  entry.runner === "ws" && (entry.status === "working" || entry.status === "starting");
