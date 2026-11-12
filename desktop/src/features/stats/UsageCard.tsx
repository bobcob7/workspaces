import { useId, type JSX } from "react";
import type { Usage } from "../../api";
import { formatCount, formatMinutes, formatPercent, formatUsd } from "./format";
import styles from "./StatsPane.module.css";

/** Requests, sessions, time, tokens, cache hit and estimated cost over the window. */
export const UsageCard = ({ usage }: { readonly usage: Usage }): JSX.Element => {
  const headingId = useId();
  const { totals } = usage;
  return (
    <section className={styles.card} aria-labelledby={headingId}>
      <h2 id={headingId} className={styles.cardTitle}>
        Usage
      </h2>
      <dl className={styles.facts}>
        <dt>Requests</dt>
        <dd>{formatCount(totals.requests)}</dd>
        <dt>Sessions</dt>
        <dd>{formatCount(usage.sessions)}</dd>
        <dt>Agent time</dt>
        <dd>{formatMinutes(usage.activeMinutes)}</dd>
        <dt>Tokens</dt>
        <dd>{formatCount(totals.totalTokens)}</dd>
        <dt>Cache hit</dt>
        <dd>{formatPercent(totals.cacheHitRatio)}</dd>
        <dt>Estimated cost</dt>
        <dd>{formatUsd(totals.costUsd)}</dd>
      </dl>
    </section>
  );
};
