import { Button } from "@mui/material";
import { useId, useMemo, type JSX } from "react";
import type { Entry } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { selected } from "../ui/uiSlice";
import { filtersCleared, hiddenShown, nodeToggled, queryChanged, statusFilterChanged } from "./agentsSlice";
import { EntryRow } from "./EntryRow";
import { FilterBar } from "./FilterBar";
import { attentionEntries, type StatusFilter } from "./status";
import {
  autoExpandedKeys,
  buildForest,
  isFiltering,
  isOpen,
  matchesFilter,
  parentOf,
  type TreeFilter,
  type TreeNode,
} from "./tree";
import styles from "./Sidebar.module.css";

/** The listed name of the workspace entry was started from, or null. */
const parentName = (entries: readonly Entry[], entry: Entry): string | null => parentOf(entries, entry)?.name ?? null;

interface TreeListProps {
  readonly nodes: readonly TreeNode[];
  readonly depth: number;
  readonly id?: string;
  readonly expanded: Readonly<Record<string, boolean>>;
  readonly auto: ReadonlySet<string>;
  readonly selectedKey: string | null;
}

const TreeList = (props: TreeListProps): JSX.Element => {
  const { nodes, depth, id, expanded, auto, selectedKey } = props;
  return (
    <ul id={id} className={depth === 0 ? styles.list : styles.children} data-capped={depth > 5 ? "true" : undefined}>
      {nodes.map((node) => (
        <TreeItem
          key={node.entry.key}
          node={node}
          depth={depth}
          expanded={expanded}
          auto={auto}
          selectedKey={selectedKey}
        />
      ))}
    </ul>
  );
};

interface TreeItemProps extends Omit<TreeListProps, "nodes" | "id"> {
  readonly node: TreeNode;
}

const TreeItem = (props: TreeItemProps): JSX.Element => {
  const { node, depth, expanded, auto, selectedKey } = props;
  const dispatch = useAppDispatch();
  const childListId = useId();
  const open = isOpen(node.entry.key, expanded, auto);
  const hasChildren = node.children.length > 0;
  return (
    <li data-context={node.match ? undefined : "true"} data-hidden={node.entry.hidden ? "true" : undefined}>
      <div className={styles.node}>
        {hasChildren ? (
          <button
            type="button"
            className={styles.disclosure}
            aria-expanded={open}
            aria-controls={open ? childListId : undefined}
            aria-label={`${open ? "Collapse" : "Expand"} ${node.entry.name}`}
            onClick={() => dispatch(nodeToggled({ key: node.entry.key, open: !open }))}
          >
            <span className={styles.caret} data-open={open} aria-hidden="true">
              ▸
            </span>
          </button>
        ) : (
          <span className={styles.spacer} aria-hidden="true" />
        )}
        <EntryRow
          entry={node.entry}
          current={node.entry.key === selectedKey}
          onClick={() => dispatch(selected(node.entry.key))}
        />
      </div>
      {open && hasChildren && (
        <TreeList
          nodes={node.children}
          depth={depth + 1}
          id={childListId}
          expanded={expanded}
          auto={auto}
          selectedKey={selectedKey}
        />
      )}
    </li>
  );
};

interface EmptyTreeProps {
  readonly filtering: boolean;
  readonly onClear: () => void;
}

const EmptyTree = ({ filtering, onClear }: EmptyTreeProps): JSX.Element => {
  if (!filtering) {
    return (
      <div className={styles.empty}>
        <p>Every workspace is hidden.</p>
      </div>
    );
  }
  return (
    <div className={styles.empty}>
      <p>No workspaces match the filters.</p>
      <Button size="small" onClick={onClear}>
        Clear filters
      </Button>
    </div>
  );
};

/** The Agents list: Needs input, the filter bar, and the tree of root workspaces (ADR 0057). */
export const Sidebar = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const entries = useAppSelector((s) => s.agents.entries);
  const loaded = useAppSelector((s) => s.agents.loaded);
  const selectedKey = useAppSelector((s) => s.ui.selectedKey);
  const query = useAppSelector((s) => s.agents.query);
  const statusFilter = useAppSelector((s) => s.agents.statusFilter);
  const showHidden = useAppSelector((s) => s.agents.showHidden);
  const expanded = useAppSelector((s) => s.agents.expanded);
  const filter = useMemo<TreeFilter>(
    () => ({ query, status: statusFilter, showHidden }),
    [query, statusFilter, showHidden],
  );
  const attention = useMemo(() => attentionEntries(entries), [entries]);
  const forest = useMemo(() => buildForest(entries, filter, selectedKey), [entries, filter, selectedKey]);
  const auto = useMemo(() => autoExpandedKeys(entries, filter, selectedKey), [entries, filter, selectedKey]);
  const matchCount = useMemo(() => entries.filter((e) => matchesFilter(e, filter)).length, [entries, filter]);
  const hiddenCount = useMemo(() => entries.filter((e) => e.hidden).length, [entries]);
  return (
    <nav className={styles.root} aria-label="Workspaces">
      {loaded && entries.length === 0 && <p className={styles.empty}>No workspaces yet.</p>}
      {attention.length > 0 && (
        <section className={styles.group} aria-label="Needs input">
          <h2 className={styles.heading}>
            Needs input
            <span className={styles.count}>{attention.length}</span>
          </h2>
          <ul className={styles.list}>
            {attention.map((entry) => (
              <li key={entry.key}>
                <EntryRow
                  entry={entry}
                  current={entry.key === selectedKey}
                  parentName={parentName(entries, entry)}
                  onClick={() => dispatch(selected(entry.key))}
                />
              </li>
            ))}
          </ul>
        </section>
      )}
      {entries.length > 0 && (
        <>
          <FilterBar
            query={query}
            status={statusFilter}
            showHidden={showHidden}
            hiddenCount={hiddenCount}
            onQueryChange={(text) => dispatch(queryChanged(text))}
            onStatusChange={(value: StatusFilter) => dispatch(statusFilterChanged(value))}
            onShowHiddenChange={(value) => dispatch(hiddenShown(value))}
          />
          <section className={styles.group} aria-label="All workspaces">
            <h2 className={styles.heading}>
              All workspaces
              <span className={styles.count}>{matchCount}</span>
            </h2>
            {forest.length > 0 ? (
              <TreeList nodes={forest} depth={0} expanded={expanded} auto={auto} selectedKey={selectedKey} />
            ) : (
              <EmptyTree filtering={isFiltering(filter)} onClear={() => dispatch(filtersCleared())} />
            )}
          </section>
        </>
      )}
    </nav>
  );
};
