import { useId, type JSX } from "react";
import type { TokenCounts } from "../../api";
import { formatCount, formatPercent } from "./format";
import { tokenKinds, type TokenKind } from "./insights";
import cards from "./Cards.module.css";
import styles from "./StatsPane.module.css";

const segmentClass: Readonly<Record<TokenKind, string | undefined>> = {
  Input: cards.segment,
  Output: `${cards.segment} ${cards.segmentOutput}`,
  "Cache write": `${cards.segment} ${cards.segmentCacheWrite}`,
  "Cache read": `${cards.segment} ${cards.segmentCacheRead}`,
};

/** Input, output and cache tokens of the window as a bar and a list, with thinking shown as part of output. */
export const TokenKindsCard = ({ totals }: { readonly totals: TokenCounts }): JSX.Element => {
  const headingId = useId();
  const kinds = tokenKinds(totals);
  return (
    <section className={styles.card} aria-labelledby={headingId}>
      <h2 id={headingId} className={styles.cardTitle}>
        Tokens by kind
      </h2>
      <div className={cards.bar} aria-hidden="true">
        {kinds.map(({ kind, tokens, share }) => (
          <div
            key={kind}
            className={segmentClass[kind]}
            style={{ flexBasis: `${share * 100}%` }}
            title={`${kind}: ${formatCount(tokens)}`}
          />
        ))}
      </div>
      <ul className={cards.list}>
        {kinds.map(({ kind, tokens, share }) => (
          <li key={kind}>{`${kind} ${formatCount(tokens)} (${formatPercent(share)})`}</li>
        ))}
        <li>{`Thinking ${formatCount(totals.thinkingTokens)} (part of output)`}</li>
      </ul>
    </section>
  );
};
