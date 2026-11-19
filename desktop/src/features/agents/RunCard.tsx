import type { JSX } from "react";
import type { Entry, WsRunner } from "../../api";
import { endpointLabel, tokenCount } from "./runner";
import styles from "./RunCard.module.css";

export interface RunCardProps {
  readonly entry: Entry;
  readonly run: WsRunner;
}

/** A `ws` child's run: its runner, model, endpoint, progress, MCP servers and exit. */
export const RunCard = ({ entry, run }: RunCardProps): JSX.Element => {
  const endpoint = endpointLabel(run);
  const progress =
    run.step === null || run.maxSteps === null
      ? null
      : `step ${run.step} of ${run.maxSteps}${run.restarts ? `, ${run.restarts} restarts` : ""}`;
  const context = run.contextTokens === null ? null : `${tokenCount(run.contextTokens)} context`;
  return (
    <section className={styles.card} aria-label="Run">
      <h2 className={styles.head}>Run</h2>
      <dl className={styles.rows}>
        <dt>Runner</dt>
        <dd>ws runner</dd>
        <dt>Model</dt>
        <dd>
          {[entry.model, run.modelAlias && `alias ${run.modelAlias}`, context].filter(Boolean).join(" · ")}
        </dd>
        {endpoint !== null && (
          <>
            <dt>Endpoint</dt>
            <dd>{endpoint}</dd>
          </>
        )}
        {progress !== null && (
          <>
            <dt>Progress</dt>
            <dd>{progress}</dd>
          </>
        )}
        {run.mcp.length > 0 && (
          <>
            <dt>MCP servers</dt>
            <dd>
              {run.mcp.map((server) => `${server.server} ${server.state} (${server.tools} tools, ${server.loaded} loaded)`).join("; ")}
            </dd>
          </>
        )}
        <dt>Session</dt>
        <dd>{entry.sessionId}</dd>
        {run.exit !== null && (
          <>
            <dt>Exit</dt>
            <dd>{run.exit}</dd>
          </>
        )}
      </dl>
    </section>
  );
};
