import type { JSX } from "react";
import type { ArtifactConversation } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { agentFocused, agentViewChosen } from "../ui/uiSlice";
import styles from "./ConversationsSection.module.css";

/** More rows than this start collapsed. */
export const collapseAfter = 5;

/** The action chips of a row, with the revisions after the action that made them. */
export const actionLabels = (row: ArtifactConversation): string[] =>
  row.actions.map((action) => {
    if (action === "revised") {
      const revs = row.revs.filter((rev) => rev > 1);
      return revs.length === 0 ? action : `revised ${revs.map((rev) => `r${rev}`).join(" ")}`;
    }
    return action;
  });

const Row = ({ row }: { readonly row: ArtifactConversation }): JSX.Element => {
  const dispatch = useAppDispatch();
  const entryKey = useAppSelector((s) => s.agents.entries.find((e) => e.workspaceId === row.workspace)?.key ?? null);
  const removed = row.removed === true;
  const name = row.name ?? row.workspace;
  const open = (): void => {
    if (entryKey === null) {
      return;
    }
    dispatch(agentFocused(entryKey));
    dispatch(agentViewChosen("conversation"));
  };
  return (
    <li className={removed ? `${styles.row} ${styles.removed}` : styles.row}>
      {removed || entryKey === null ? (
        <span className={styles.name} title={row.workspace}>
          {name}
        </span>
      ) : (
        <button type="button" className={styles.link} title={`Open ${row.workspace}'s conversation`} onClick={open}>
          {name}
        </button>
      )}
      {row.environment !== null && <span className={styles.env}>{row.environment}</span>}
      {actionLabels(row).map((label) => (
        <span key={label} className={styles.chip}>
          {label}
        </span>
      ))}
      {removed && <span className={styles.gone}>workspace removed</span>}
      <span className={styles.age}>{age(row.lastAt)}</span>
    </li>
  );
};

/**
 * "Conversations (n)": the agents' conversations an artifact was part of,
 * latest first (ADR 0084). A row opens that agent's conversation; a
 * removed workspace's is greyed and has no link. Names come from agents, so
 * they are only ever shown as text.
 */
export const ConversationsSection = ({ rows }: { readonly rows: readonly ArtifactConversation[] }): JSX.Element | null => {
  if (rows.length === 0) {
    return null;
  }
  return (
    <details className={styles.section} open={rows.length <= collapseAfter}>
      <summary className={styles.summary}>Conversations ({rows.length})</summary>
      <ul className={styles.rows}>
        {rows.map((row) => (
          <Row key={row.workspace} row={row} />
        ))}
      </ul>
    </details>
  );
};
