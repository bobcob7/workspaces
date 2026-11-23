import { Button } from "@mui/material";
import { useId, useState, type JSX } from "react";
import type { Entry } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { selected } from "../ui/uiSlice";
import { RemoveDialog } from "./RemoveDialog";
import { isFailed } from "./progress";
import { childrenOf, statusLabels } from "./status";
import { StatusDot } from "./StatusDot";
import { StepProgress } from "./StepProgress";
import styles from "./ChildrenCard.module.css";

/** Up to this many children, the list starts expanded; more start collapsed. */
const expandedUpTo = 3;

const ChildRow = ({ child }: { readonly child: Entry }): JSX.Element => {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((s) => s.agents.busy.includes(child.key));
  const [confirming, setConfirming] = useState(false);
  const rollup = child.rollup;
  return (
    <li className={styles.row}>
      <StatusDot status={child.status} />
      <span className={styles.name}>{child.name}</span>
      <span className={styles.muted}>{statusLabels[child.status]}</span>
      {child.hidden && <span className={styles.chip}>hidden</span>}
      {rollup !== null && (
        <span className={styles.progress}>
          <StepProgress
            label={`${child.progress?.name ?? child.name} progress`}
            done={rollup.done}
            total={rollup.total}
            failed={isFailed(child)}
          />
        </span>
      )}
      <span className={styles.spacer} />
      <Button size="small" className={styles.action} onClick={() => dispatch(selected(child.key))}>
        Open
      </Button>
      <Button size="small" color="error" className={styles.action} disabled={busy} onClick={() => setConfirming(true)}>
        Remove
      </Button>
      <RemoveDialog entry={child} open={confirming} onClose={() => setConfirming(false)} />
    </li>
  );
};

/** The workspaces an entry started, which are removed with it (ADR 0044). */
export const ChildrenCard = ({ entry }: { readonly entry: Entry }): JSX.Element | null => {
  const children = useAppSelector((s) => childrenOf(s.agents.entries, entry));
  const listId = useId();
  const [open, setOpen] = useState(() => children.length <= expandedUpTo);
  if (children.length === 0) {
    return null;
  }
  return (
    <section className={styles.card} aria-label="Started from this workspace">
      <h2 className={styles.heading}>
        <button type="button" className={styles.toggle} aria-expanded={open} aria-controls={listId} onClick={() => setOpen(!open)}>
          <span aria-hidden="true" className={styles.chevron}>
            {open ? "▾" : "▸"}
          </span>
          Started from this workspace <span className={styles.muted}>{children.length} · removed with it</span>
        </button>
      </h2>
      <ul id={listId} className={styles.list} hidden={!open}>
        {children.map((child) => (
          <ChildRow key={child.key} child={child} />
        ))}
      </ul>
    </section>
  );
};
