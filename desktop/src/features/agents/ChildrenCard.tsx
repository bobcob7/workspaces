import { Button } from "@mui/material";
import { useState, type JSX } from "react";
import type { Entry } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { selected } from "../ui/uiSlice";
import { RemoveDialog } from "./RemoveDialog";
import { childrenOf, statusLabels } from "./status";
import { StatusDot } from "./StatusDot";
import styles from "./ChildrenCard.module.css";

const ChildRow = ({ child }: { readonly child: Entry }): JSX.Element => {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((s) => s.agents.busy.includes(child.key));
  const [confirming, setConfirming] = useState(false);
  return (
    <li className={styles.row}>
      <StatusDot status={child.status} />
      <span className={styles.name}>{child.name}</span>
      <span className={styles.muted}>{statusLabels[child.status]}</span>
      {child.hidden && <span className={styles.chip}>hidden</span>}
      <span className={styles.spacer} />
      <Button size="small" onClick={() => dispatch(selected(child.key))}>
        Open
      </Button>
      <Button size="small" color="error" disabled={busy} onClick={() => setConfirming(true)}>
        Remove
      </Button>
      <RemoveDialog entry={child} open={confirming} onClose={() => setConfirming(false)} />
    </li>
  );
};

/** The workspaces an entry started, which are removed with it (ADR 0044). */
export const ChildrenCard = ({ entry }: { readonly entry: Entry }): JSX.Element | null => {
  const children = useAppSelector((s) => childrenOf(s.agents.entries, entry));
  if (children.length === 0) {
    return null;
  }
  return (
    <section className={styles.card} aria-label="Started from this workspace">
      <h2 className={styles.heading}>
        Started from this workspace <span className={styles.muted}>{children.length} · removed with it</span>
      </h2>
      <ul className={styles.list}>
        {children.map((child) => (
          <ChildRow key={child.key} child={child} />
        ))}
      </ul>
    </section>
  );
};
