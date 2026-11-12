import { useId, type JSX } from "react";
import type { ModelUsage } from "../../api";
import { formatCount, formatPercent, formatMinutes, formatUsd } from "./format";
import cards from "./Cards.module.css";
import styles from "./StatsPane.module.css";

/** Requests, sessions, time, tokens, cache hit and estimated cost for each model in the window. */
export const ModelsCard = ({ models }: { readonly models: readonly ModelUsage[] }): JSX.Element => {
  const headingId = useId();
  const unpriced = models.some((model) => !model.priced);
  return (
    <section className={styles.card} aria-labelledby={headingId}>
      <h2 id={headingId} className={styles.cardTitle}>
        Usage by model
      </h2>
      {models.length === 0 ? (
        <p className={styles.muted}>No requests in this window.</p>
      ) : (
        <div className={cards.scroll}>
          <table className={cards.table}>
            <thead>
              <tr>
                <th scope="col">Model</th>
                <th scope="col" className={cards.numeric}>Requests</th>
                <th scope="col" className={cards.numeric}>Sessions</th>
                <th scope="col" className={cards.numeric}>Agent time</th>
                <th scope="col" className={cards.numeric}>Tokens</th>
                <th scope="col" className={cards.numeric}>Cache hit</th>
                <th scope="col" className={cards.numeric}>Est. cost</th>
              </tr>
            </thead>
            <tbody>
              {models.map((model) => (
                <tr key={model.model}>
                  <th scope="row">{model.label}</th>
                  <td className={cards.numeric}>{formatCount(model.requests)}</td>
                  <td className={cards.numeric}>{formatCount(model.sessions)}</td>
                  <td className={cards.numeric}>{formatMinutes(model.activeMinutes)}</td>
                  <td className={cards.numeric}>{formatCount(model.totalTokens)}</td>
                  <td className={cards.numeric}>{formatPercent(model.cacheHitRatio)}</td>
                  <td className={cards.numeric}>
                    {formatUsd(model.costUsd)}
                    {model.priced ? "" : "*"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {unpriced && <p className={cards.footnote}>* No price known for this model; its cost is not counted.</p>}
    </section>
  );
};
