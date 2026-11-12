import { useId, type JSX } from "react";
import type { LanguageLines } from "../../api";
import { formatCount, formatPercent, usedRatio } from "./format";
import cards from "./Cards.module.css";
import styles from "./StatsPane.module.css";

/** Lines added and removed, and edits, per language in the window, with each language's share of lines added. */
export const LanguagesCard = ({ languages }: { readonly languages: readonly LanguageLines[] }): JSX.Element => {
  const headingId = useId();
  const totalAdded = languages.reduce((total, language) => total + language.linesAdded, 0);
  return (
    <section className={styles.card} aria-labelledby={headingId}>
      <h2 id={headingId} className={styles.cardTitle}>
        Lines by language
      </h2>
      {languages.length === 0 ? (
        <p className={styles.muted}>No edits in this window.</p>
      ) : (
        <div className={cards.scroll}>
          <table className={cards.table}>
            <thead>
              <tr>
                <th scope="col">Language</th>
                <th scope="col" className={cards.numeric}>Lines added</th>
                <th scope="col" className={cards.numeric}>Lines removed</th>
                <th scope="col" className={cards.numeric}>Edits</th>
                <th scope="col">Share of added</th>
              </tr>
            </thead>
            <tbody>
              {languages.map((language) => {
                const share = usedRatio(language.linesAdded, totalAdded);
                return (
                  <tr key={language.language}>
                    <th scope="row">{language.language}</th>
                    <td className={cards.numeric}>{formatCount(language.linesAdded)}</td>
                    <td className={cards.numeric}>{formatCount(language.linesRemoved)}</td>
                    <td className={cards.numeric}>{formatCount(language.edits)}</td>
                    <td>
                      <div className={cards.shareTrack} title={formatPercent(share)}>
                        <div className={cards.shareFill} style={{ width: `${(share ?? 0) * 100}%` }} />
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
};
