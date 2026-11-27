import { Button, useMediaQuery } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { ArtifactSummary } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { artifactLinkOpened } from "../artifacts/artifactsSlice";
import { fetchLinkedArtifacts, linkedArtifactsInterval } from "./conversationSlice";
import styles from "./ConversationArtifacts.module.css";

/** How many chips show before "+n more", on a wide screen and on a phone. */
export const stripChips = 8;
export const phoneStripChips = 2;

const kindIcon: Readonly<Record<ArtifactSummary["kind"], string>> = { image: "▣", markdown: "▤", text: "▢" };

/** The tooltip of a chip: what the conversation did with the artifact. */
export const chipTitle = (item: ArtifactSummary): string => {
  const row = item.conversation;
  if (row === null || row === undefined) {
    return item.name;
  }
  const revs = row.revs.length === 0 ? "" : ` (${row.revs.map((rev) => `r${rev}`).join(", ")})`;
  return `${row.actions.join(", ")}${revs}`;
};

/** The latest revision a conversation touched, which a chip opens; null for the latest. */
const linkedRev = (item: ArtifactSummary): number | null => {
  const revs = item.conversation?.revs ?? [];
  return revs.length === 0 ? null : Math.max(...revs);
};

/**
 * "Artifacts in this conversation": the artifacts a workspace created, read,
 * discussed or was handed, read with `ws artifact list --conversation` when
 * the view opens and every 15 s (ADR 0084). Hidden when there are none; a
 * failed read keeps the last list.
 */
export const ConversationArtifacts = ({ workspaceId }: { readonly workspaceId: string }): JSX.Element | null => {
  const dispatch = useAppDispatch();
  const linked = useAppSelector((s) => s.conversations.artifactsByWorkspace[workspaceId]);
  const phone = useMediaQuery("(max-width:720px)");
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    void dispatch(fetchLinkedArtifacts(workspaceId));
    const timer = window.setInterval(() => void dispatch(fetchLinkedArtifacts(workspaceId)), linkedArtifactsInterval);
    return () => window.clearInterval(timer);
  }, [dispatch, workspaceId]);
  const items = linked?.items ?? [];
  if (items.length === 0) {
    return null;
  }
  const limit = phone ? phoneStripChips : stripChips;
  const shown = expanded ? items : items.slice(0, limit);
  const more = items.length - shown.length;
  return (
    <nav className={styles.strip} aria-label="Artifacts in this conversation">
      <span className={styles.label}>Artifacts in this conversation</span>
      {shown.map((item) => (
        <button
          key={item.id}
          type="button"
          className={styles.chip}
          title={chipTitle(item)}
          onClick={() => dispatch(artifactLinkOpened({ id: item.id, rev: linkedRev(item) }))}
        >
          <span className={styles.kind} aria-hidden>
            {kindIcon[item.kind]}
          </span>
          {item.title ?? item.name}
        </button>
      ))}
      {more > 0 && (
        <Button size="small" className={styles.more} onClick={() => setExpanded(true)}>
          +{more} more
        </Button>
      )}
    </nav>
  );
};
