import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { Loop, LoopMemoryEntry } from "../../api";
import { age, count, formatSize } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { Card } from "./Card";
import { loopLabel } from "./formValues";
import { MemoryDialog } from "./MemoryDialog";
import { clearLoopMemory, fetchLoopMemory, removeLoopMemory } from "./loopDataSlice";
import styles from "./MemoryCard.module.css";

/** The entry's first line, cut to 120 characters. */
const preview = (value: string): string => (value.split("\n")[0] ?? "").slice(0, 120);

const EntryRow = ({ entry, onEdit, onDelete }: { readonly entry: LoopMemoryEntry; readonly onEdit: () => void; readonly onDelete: () => void }): JSX.Element => (
  <tr>
    <td className={styles.mono}>{entry.key}</td>
    <td className={styles.mono}>{preview(entry.value)}</td>
    <td className={styles.updated}>
      {age(entry.updatedAt)} by {entry.updatedBy ?? "you"}
    </td>
    <td className={styles.actions}>
      <Button size="small" aria-label={`Edit ${entry.key}`} onClick={onEdit}>
        Edit
      </Button>
      <Button size="small" color="error" aria-label={`Delete ${entry.key}`} onClick={onDelete}>
        Delete
      </Button>
    </td>
  </tr>
);

/** The loop's remembered values: a table to add, edit, and delete them, and to clear them all. */
export const MemoryCard = ({ loop }: { readonly loop: Loop }): JSX.Element => {
  const dispatch = useAppDispatch();
  const data = useAppSelector((s) => s.loopData);
  const busy = useAppSelector((s) => s.loops.busy === loop.name);
  const [editing, setEditing] = useState<LoopMemoryEntry | "new" | null>(null);
  const [confirming, setConfirming] = useState(false);
  const held = data.loop === loop.name;
  const memory = held ? data.memory : null;
  const error = held ? data.memoryError : null;

  useEffect(() => {
    void dispatch(fetchLoopMemory(loop.name));
  }, [dispatch, loop.name]);

  return (
    <Card title="Memory">
      <div className={styles.content}>
        <div className={styles.toolbar}>
          <Button size="small" disabled={busy} onClick={() => setEditing("new")}>
            Add
          </Button>
          <Button size="small" color="error" disabled={busy || memory === null || memory.entries.length === 0} onClick={() => setConfirming(true)}>
            Clear memory
          </Button>
        </div>
        {error !== null && (
          <div className={styles.error}>
            <p role="alert" className={styles.muted}>
              {error.message}
            </p>
            <Button size="small" onClick={() => void dispatch(fetchLoopMemory(loop.name))}>
              Retry
            </Button>
          </div>
        )}
        {memory !== null && memory.entries.length === 0 && (
          <p className={styles.muted}>Nothing remembered yet. Runs write here with ws machine memory set.</p>
        )}
        {memory !== null && memory.entries.length > 0 && (
          <table aria-label="Memory" className={styles.table}>
            <thead>
              <tr>
                <th scope="col">Key</th>
                <th scope="col">Value</th>
                <th scope="col">Updated</th>
                <th scope="col">
                  <span className={styles.srOnly}>Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {memory.entries.map((entry) => (
                <EntryRow
                  key={entry.key}
                  entry={entry}
                  onEdit={() => setEditing(entry)}
                  onDelete={() => void dispatch(removeLoopMemory({ name: loop.name, key: entry.key }))}
                />
              ))}
            </tbody>
          </table>
        )}
        {memory !== null && (
          <p className={styles.muted}>
            {formatSize(memory.bytes)} of 256 KiB · {count(memory.entries.length, "key")} of 100
          </p>
        )}
      </div>
      {editing !== null && (
        <MemoryDialog
          loopName={loop.name}
          entry={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
        />
      )}
      <Dialog open={confirming} onClose={() => setConfirming(false)}>
        <DialogTitle>Clear the memory of {loopLabel(loop)}?</DialogTitle>
        <DialogContent>
          <DialogContentText>Every remembered key is deleted.</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirming(false)}>Cancel</Button>
          <Button
            color="error"
            onClick={() => {
              setConfirming(false);
              void dispatch(clearLoopMemory(loop.name));
            }}
          >
            Clear memory
          </Button>
        </DialogActions>
      </Dialog>
    </Card>
  );
};
