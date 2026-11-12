import { useId, type JSX } from "react";
import type { ProductivityRow } from "../../api";
import { formatCount, formatMinutes, formatPercent, formatRate } from "./format";
import { formatCostPerLine, languageInsights, rowsByLanguage } from "./insights";
import cards from "./Cards.module.css";
import styles from "./StatsPane.module.css";

const columnCount = 8;

/** Per model and language: share of lines, speed, time and cost per line, with a sentence comparing the models. */
export const ProductivityCard = ({ rows }: { readonly rows: readonly ProductivityRow[] }): JSX.Element => {
  const headingId = useId();
  const insights = languageInsights(rows);
  return (
    <section className={styles.card} aria-labelledby={headingId}>
      <h2 id={headingId} className={styles.cardTitle}>
        Productivity by model and language
      </h2>
      {rows.length === 0 ? (
        <p className={styles.muted}>No edits in this window.</p>
      ) : (
        <>
          <ul className={cards.list}>
            {insights.map((insight) => (
              <li key={insight}>{insight}</li>
            ))}
          </ul>
          <div className={cards.scroll}>
            <table className={cards.table}>
              <thead>
                <tr>
                  <th scope="col">Model</th>
                  <th scope="col" className={cards.numeric}>Share</th>
                  <th scope="col" className={cards.numeric}>Lines</th>
                  <th scope="col" className={cards.numeric}>Agent time</th>
                  <th scope="col" className={cards.numeric}>Lines/min</th>
                  <th scope="col" className={cards.numeric}>Min/100 lines</th>
                  <th scope="col" className={cards.numeric}>Tokens/line</th>
                  <th scope="col" className={cards.numeric}>Cost/line</th>
                </tr>
              </thead>
              {[...rowsByLanguage(rows)].map(([language, group]) => (
                <tbody key={language}>
                  <tr className={cards.groupRow}>
                    <th scope="rowgroup" colSpan={columnCount}>
                      {language}
                    </th>
                  </tr>
                  {group.map((row) => (
                    <tr key={`${language}-${row.model}`}>
                      <th scope="row">{row.label}</th>
                      <td className={cards.numeric}>{formatPercent(row.shareOfLanguage)}</td>
                      <td className={cards.numeric}>{formatCount(row.linesAdded)}</td>
                      <td className={cards.numeric}>{formatMinutes(row.activeMinutes)}</td>
                      <td className={cards.numeric}>{formatRate(row.locPerMinute, 1)}</td>
                      <td className={cards.numeric}>{formatRate(row.minutesPer100Loc, 1)}</td>
                      <td className={cards.numeric}>{formatRate(row.tokensPerLoc, 0)}</td>
                      <td className={cards.numeric}>{formatCostPerLine(row.costPerLoc)}</td>
                    </tr>
                  ))}
                </tbody>
              ))}
            </table>
          </div>
          <p className={cards.footnote}>
            Lines are lines added by agents' edits; agent time is active session time split by lines changed. Costs
            are estimates at API rates.
          </p>
        </>
      )}
    </section>
  );
};
