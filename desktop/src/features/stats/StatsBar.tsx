import { Button } from "@mui/material";
import type { JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { sectionChosen } from "../ui/uiSlice";
import { formatCount, formatPercent, formatPercentValue, formatUsd, usedRatio, workspaceCounts } from "./format";
import styles from "./StatsBar.module.css";
import { useStatsPolling } from "./useStatsPolling";

/** The app's bottom bar: today's workspaces, tokens, cache, cost and host load (ADR 0059). */
export const StatsBar = (): JSX.Element => {
  useStatsPolling("today");
  const dispatch = useAppDispatch();
  const entries = useAppSelector((s) => s.agents.entries);
  const data = useAppSelector((s) => s.stats.results.today.data);
  const counts = workspaceCounts(entries);
  const totals = data?.usage.totals;
  const system = data?.system;
  const memory = usedRatio(system?.memoryUsedBytes ?? null, system?.memoryTotalBytes ?? null);
  const disk = usedRatio(system?.disk?.usedBytes ?? null, system?.disk?.totalBytes ?? null);
  return (
    <footer className={styles.bar} aria-label="Stats">
      <span className={styles.item}>{`${counts.active} / ${counts.total} workspaces`}</span>
      <span className={styles.item}>{`${totals ? formatCount(totals.totalTokens) : "—"} tokens`}</span>
      <span className={styles.item}>{`cache ${formatPercent(totals?.cacheHitRatio ?? null)}`}</span>
      <span className={styles.item} data-wide title="Estimated at API rates">
        {totals ? formatUsd(totals.costUsd) : "—"}
      </span>
      <span className={styles.item} data-wide>{`CPU ${formatPercentValue(system?.cpuPercent ?? null)}`}</span>
      <span className={styles.item} data-wide>{`RAM ${formatPercent(memory)}`}</span>
      <span className={styles.item} data-wide>{`disk ${formatPercent(disk)}`}</span>
      <Button size="small" data-wide onClick={() => dispatch(sectionChosen("stats"))}>
        Open Stats
      </Button>
    </footer>
  );
};
