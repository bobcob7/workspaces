import { Button, Menu, MenuItem, TextField } from "@mui/material";
import { useState, type JSX, type MouseEvent } from "react";
import { api, type DecisionKind, type ReportComment } from "../../api";
import { age } from "../../format";
import { MarkdownText } from "../conversation/ConversationView";
import { anchorLabel, anchorUrl, bandOf, clickedDecision, findingTitle, pendingDecision, type Finding } from "./report";
import styles from "./Report.module.css";

const segments: readonly (readonly [DecisionKind, string])[] = [
  ["comment", "Comment"],
  ["fix", "Fix"],
  ["skip", "Skip"],
];

export interface DecisionControlProps {
  readonly comment: ReportComment;
  readonly busy: boolean;
  readonly onDecide: (decision: DecisionKind | "clear") => void;
}

/** Comment · Fix · Skip; clicking the selected one again clears it. */
export const DecisionControl = ({ comment, busy, onDecide }: DecisionControlProps): JSX.Element => {
  const current = pendingDecision(comment);
  return (
    <span className={styles.seg} role="group" aria-label={`Decision for ${comment.id}`}>
      {segments.map(([kind, label]) => (
        <button
          key={kind}
          type="button"
          className={styles.segButton}
          data-on={current === kind ? kind : undefined}
          aria-pressed={current === kind}
          disabled={busy}
          onClick={(event) => {
            event.stopPropagation();
            onDecide(clickedDecision(current, kind));
          }}
        >
          {label}
        </button>
      ))}
    </span>
  );
};

interface DoneProps {
  readonly comment: ReportComment;
  readonly onActAgain: () => void;
  readonly onOpenAgent: (id: string) => void;
}

/** What was done about a finding, with Act again to put its decision back. */
const Done = ({ comment, onActAgain, onOpenAgent }: DoneProps): JSX.Element | null => {
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const done = comment.decision?.done;
  if (done === undefined || done === null) {
    return null;
  }
  const stop = (event: MouseEvent): void => event.stopPropagation();
  return (
    <span className={styles.done} onClick={stop}>
      <button type="button" className={styles.doneButton} onClick={(e) => setAnchor(e.currentTarget)}>
        ✓ {done.commented !== undefined ? "Commented" : "Fix sent to"}
      </button>
      {done.commented !== undefined ? (
        <>
          {" · "}
          <a
            href={done.commented}
            onClick={(e) => {
              e.preventDefault();
              void api.openExternal(done.commented ?? "");
            }}
          >
            view on GitHub
          </a>
        </>
      ) : (
        <>
          {" "}
          <a
            href={`#${done.fix_sent ?? ""}`}
            onClick={(e) => {
              e.preventDefault();
              onOpenAgent(done.fix_sent ?? "");
            }}
          >
            {done.fix_sent}
          </a>
          {` · ${age(done.at)}`}
        </>
      )}
      <Menu anchorEl={anchor} open={anchor !== null} onClose={() => setAnchor(null)}>
        <MenuItem
          onClick={() => {
            setAnchor(null);
            onActAgain();
          }}
        >
          Act again
        </MenuItem>
      </Menu>
    </span>
  );
};

/** The reviewer's code excerpt, numbered from the first anchor's line. */
const CodeExcerpt = ({ comment }: { readonly comment: ReportComment }): JSX.Element | null => {
  if (comment.code === null || comment.code === "") {
    return null;
  }
  const first = comment.anchors[0]?.line ?? 0;
  return (
    <pre className={styles.code} aria-label="Code">
      {comment.code.split("\n").map((line, i) => (
        <span key={i} className={styles.codeLine}>
          {first > 0 && <span className={styles.lineNo}>{first + i}</span>}
          {line}
          {"\n"}
        </span>
      ))}
    </pre>
  );
};

interface SuggestionsProps {
  readonly comment: ReportComment;
  readonly busy: boolean;
  readonly onChoose: (index: number) => void;
}

/** Suggestions as cards; choosing one decides Fix with it. */
const Suggestions = ({ comment, busy, onChoose }: SuggestionsProps): JSX.Element | null => {
  if (comment.suggestions.length === 0) {
    return null;
  }
  const chosen = pendingDecision(comment) === "fix" ? comment.decision?.suggestion ?? null : null;
  return (
    <div className={styles.suggestions} role="radiogroup" aria-label="Suggestions">
      {comment.suggestions.map((s, i) => (
        <label key={i} className={styles.suggestion} data-chosen={chosen === i || undefined}>
          <span className={styles.suggestionHead}>
            <input
              type="radio"
              name={`suggestion-${comment.id}`}
              checked={chosen === i}
              disabled={busy}
              onChange={() => onChoose(i)}
            />
            <span>{s.summary}</span>
          </span>
          <span className={styles.chips}>
            <span className={styles.smallChip}>effort: {s.effort}</span>
            <span className={styles.smallChip}>scope: {s.scope}</span>
          </span>
          <span className={styles.prosCons}>
            <span>
              {s.pros.map((p) => (
                <span key={p} className={styles.pro}>
                  {p}
                </span>
              ))}
            </span>
            <span>
              {s.cons.map((c) => (
                <span key={c} className={styles.con}>
                  {c}
                </span>
              ))}
            </span>
          </span>
          {s.code !== null && <pre className={styles.code}>{s.code}</pre>}
        </label>
      ))}
    </div>
  );
};

interface NoteProps {
  readonly comment: ReportComment;
  readonly draft: string | undefined;
  readonly onSave: (text: string) => void;
}

/** A note for the fixer or the comment, collapsed until clicked. */
const Note = ({ comment, draft, onSave }: NoteProps): JSX.Element => {
  const saved = comment.decision?.note ?? "";
  const [text, setText] = useState(draft ?? saved);
  const [open, setOpen] = useState(text !== "");
  if (!open) {
    return (
      <Button size="small" className={styles.noteButton} onClick={() => setOpen(true)}>
        Add a note for the fixer or the comment
      </Button>
    );
  }
  return (
    <TextField
      size="small"
      multiline
      minRows={2}
      label="Note"
      placeholder="Add a note for the fixer or the comment"
      helperText={comment.decision === null ? "Saved with the decision you choose." : undefined}
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => {
        if (text.trim() !== (draft ?? saved).trim()) {
          onSave(text);
        }
      }}
    />
  );
};

export interface FindingRowProps {
  readonly finding: Finding;
  readonly repo: string;
  readonly gitRef: string;
  readonly expanded: boolean;
  readonly focused: boolean;
  readonly selected: boolean;
  readonly busy: boolean;
  /** The commit an older review decided this id at, while it's undecided here. */
  readonly decidedAt: string | undefined;
  readonly draft: string | undefined;
  readonly onToggle: () => void;
  readonly onFocus: () => void;
  readonly onSelect: () => void;
  readonly onDecide: (decision: DecisionKind | "clear", suggestion?: number) => void;
  readonly onNote: (text: string) => void;
  readonly onOpenAgent: (id: string) => void;
}

/** One finding: a row that expands to its body, anchors, code, suggestions, and note. */
export const FindingRow = ({
  finding,
  repo,
  gitRef,
  expanded,
  focused,
  selected,
  busy,
  decidedAt,
  draft,
  onToggle,
  onFocus,
  onSelect,
  onDecide,
  onNote,
  onOpenAgent,
}: FindingRowProps): JSX.Element => {
  const { comment, readOnly } = finding;
  const [first, ...more] = comment.anchors;
  const open = (url: string) => (event: MouseEvent): void => {
    event.preventDefault();
    event.stopPropagation();
    void api.openExternal(url);
  };
  const done = comment.decision?.done != null;
  return (
    <div
      className={styles.finding}
      data-read-only={readOnly || undefined}
      data-selected={selected || undefined}
      data-focused={focused || undefined}
    >
      <div
        id={`finding-${comment.id}`}
        className={styles.findingHead}
        onClick={(event) => {
          if (event.metaKey || event.ctrlKey || event.shiftKey) {
            onSelect();
            return;
          }
          onToggle();
        }}
      >
        <button
          id={`toggle-${comment.id}`}
          type="button"
          className={styles.toggle}
          tabIndex={focused ? 0 : -1}
          aria-expanded={expanded}
          onFocus={onFocus}
          onClick={(event) => {
            event.stopPropagation();
            if (event.metaKey || event.ctrlKey || event.shiftKey) {
              onSelect();
              return;
            }
            onToggle();
          }}
        >
          <span className={styles.priority} data-band={bandOf(comment.priority)}>
            {comment.priority}
          </span>{" "}
          <span className={styles.title} data-open={expanded || undefined}>
            {findingTitle(comment.body)}
          </span>
        </button>
        <span className={styles.findingId}>{comment.id}</span>
        {first !== undefined && (
          <a className={styles.anchor} href={anchorUrl(repo, gitRef, first)} onClick={open(anchorUrl(repo, gitRef, first))}>
            {anchorLabel(first)}
            {more.length > 0 && <span className={styles.more}> +{more.length}</span>}
          </a>
        )}
        {decidedAt !== undefined && <span className={styles.muted}>decided at {decidedAt}</span>}
        {readOnly ? (
          <span className={styles.muted}>read-only</span>
        ) : done ? (
          <Done comment={comment} onActAgain={() => comment.decision !== null && onDecide(comment.decision.decision, comment.decision.suggestion ?? undefined)} onOpenAgent={onOpenAgent} />
        ) : (
          <DecisionControl comment={comment} busy={busy} onDecide={(d) => onDecide(d)} />
        )}
      </div>
      {expanded && (
        <div className={styles.findingBody}>
          <MarkdownText text={comment.body} />
          {comment.anchors.length > 1 && (
            <ul className={styles.anchors} aria-label="Anchors">
              {comment.anchors.map((a) => (
                <li key={anchorLabel(a)}>
                  <a className={styles.anchor} href={anchorUrl(repo, gitRef, a)} onClick={open(anchorUrl(repo, gitRef, a))}>
                    {anchorLabel(a)}
                  </a>
                </li>
              ))}
            </ul>
          )}
          <CodeExcerpt comment={comment} />
          {!readOnly && <Suggestions comment={comment} busy={busy || done} onChoose={(i) => onDecide("fix", i)} />}
          {!readOnly && !done && <Note comment={comment} draft={draft} onSave={onNote} />}
        </div>
      )}
    </div>
  );
};
