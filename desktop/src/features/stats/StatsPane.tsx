import { Alert } from "@mui/material";
import type { JSX } from "react";
import { useAppSelector } from "../../store";
import { LanguagesCard } from "./LanguagesCard";
import { ModelsCard } from "./ModelsCard";
import { ProductivityCard } from "./ProductivityCard";
import { windowLabels } from "./statsSlice";
import { SystemCard } from "./SystemCard";
import styles from "./StatsPane.module.css";
import { TokenKindsCard } from "./TokenKindsCard";
import { UsageCard } from "./UsageCard";
import { useStatsPolling } from "./useStatsPolling";

const clock = (ms: number): string =>
  new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

/** The chosen window's stats: the System and Usage cards. */
export const StatsPane = (): JSX.Element => {
  const statsWindow = useAppSelector((s) => s.stats.window);
  const result = useAppSelector((s) => s.stats.results[statsWindow]);
  useStatsPolling(statsWindow);
  const data = result.data;
  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <h1 className={styles.title}>{windowLabels[statsWindow]}</h1>
        {data !== null && (
          <p className={styles.muted}>{`Updated ${clock(data.generatedAtMs)} · ${data.scan.files} transcripts`}</p>
        )}
      </header>
      {data === null && result.error !== null && <Alert severity="error">{result.error.message}</Alert>}
      {data === null && result.error === null && <p className={styles.muted}>Loading…</p>}
      {data !== null && (
        <div className={styles.cards}>
          <SystemCard system={data.system} />
          <UsageCard usage={data.usage} />
          <ModelsCard models={data.usage.byModel} />
          <TokenKindsCard totals={data.usage.totals} />
          <LanguagesCard languages={data.usage.byLanguage} />
          <ProductivityCard rows={data.productivity} />
        </div>
      )}
    </div>
  );
};
