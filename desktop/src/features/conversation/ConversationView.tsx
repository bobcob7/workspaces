import { Alert, Button, CircularProgress } from "@mui/material";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type JSX } from "react";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, type Status } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { conversationClosed, fetchConversation } from "./conversationSlice";
import { toRows, type Row } from "./rows";
import styles from "./ConversationView.module.css";

export interface ConversationViewProps {
  readonly sessionId: string;
  readonly status: Status;
  readonly detail: string | null;
  readonly onShowTerminal: () => void;
}

/** How often the transcript is checked for new lines while it's shown. */
export const followInterval = 1000;

/** How close to the bottom, in pixels, still counts as following along. */
const stickDistance = 48;

// Links open in the browser; navigating the app's own webview would replace it.
const markdownComponents: Components = {
  a: ({ href, children }) => (
    <a
      href={href}
      onClick={(event) => {
        event.preventDefault();
        if (href !== undefined) {
          void api.openExternal(href);
        }
      }}
    >
      {children}
    </a>
  ),
};

const Text = ({ text }: { readonly text: string }): JSX.Element => (
  <div className={styles.markdown}>
    <Markdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
      {text}
    </Markdown>
  </div>
);

const toolState = (row: Extract<Row, { kind: "tool" }>): string => {
  if (row.result === null) {
    return styles.toolRunning ?? "";
  }
  return (row.result.isError ? styles.toolError : styles.toolDone) ?? "";
};

const RowView = ({ row }: { readonly row: Row }): JSX.Element | null => {
  if (row.kind === "tool") {
    return (
      <details className={`${styles.tool} ${toolState(row)}`}>
        <summary>
          <span className={styles.toolName}>{row.use.name}</span>
          <span className={styles.toolSummary}>{row.use.summary}</span>
        </summary>
        <pre className={styles.toolInput}>{row.use.input}</pre>
        {row.result !== null && row.result.text !== "" && <pre className={styles.toolOutput}>{row.result.text}</pre>}
      </details>
    );
  }
  const { item } = row;
  switch (item.kind) {
    case "user":
      return (
        <div className={styles.user} aria-label="You">
          {item.text}
        </div>
      );
    case "assistant":
      return (
        <div className={styles.assistant}>
          <Text text={item.text} />
        </div>
      );
    case "thinking":
      return (
        <details className={styles.thinking}>
          <summary>Thinking</summary>
          <Text text={item.text} />
        </details>
      );
    case "notice":
      return <div className={styles.notice}>{item.text}</div>;
  }
};

/**
 * An agent's conversation, read from its transcript and followed as it
 * grows. It stays at the bottom while the reader is there, and stays put
 * when they've scrolled up.
 */
export const ConversationView = ({ sessionId, status, detail, onShowTerminal }: ConversationViewProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const conversation = useAppSelector((s) => s.conversations.bySession[sessionId]);
  const rows = useMemo(() => toRows(conversation?.items ?? []), [conversation?.items]);
  const scroller = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const [behind, setBehind] = useState(false);
  useEffect(() => {
    void dispatch(fetchConversation(sessionId));
    const timer = window.setInterval(() => void dispatch(fetchConversation(sessionId)), followInterval);
    return () => {
      window.clearInterval(timer);
      dispatch(conversationClosed(sessionId));
    };
  }, [dispatch, sessionId]);
  useLayoutEffect(() => {
    const element = scroller.current;
    if (element === null) {
      return;
    }
    if (following.current) {
      element.scrollTop = element.scrollHeight;
      return;
    }
    setBehind(true);
  }, [rows.length, status]);
  const onScroll = (): void => {
    const element = scroller.current;
    if (element === null) {
      return;
    }
    following.current = element.scrollHeight - element.scrollTop - element.clientHeight < stickDistance;
    if (following.current) {
      setBehind(false);
    }
  };
  const jumpToLatest = (): void => {
    const element = scroller.current;
    if (element === null) {
      return;
    }
    following.current = true;
    element.scrollTo({ top: element.scrollHeight, behavior: "smooth" });
    setBehind(false);
  };
  if (conversation?.loaded !== true) {
    return (
      <div className={styles.center}>
        <CircularProgress size={20} aria-label="Loading conversation" />
      </div>
    );
  }
  return (
    <div className={styles.root}>
      <div ref={scroller} className={styles.scroller} onScroll={onScroll} role="log" aria-label="Conversation">
        <div className={styles.column}>
          {conversation.error !== null && (
            <Alert severity="warning" action={<Button onClick={onShowTerminal}>Terminal</Button>}>
              {conversation.error.message}
            </Alert>
          )}
          {conversation.dropped > 0 && (
            <div className={styles.notice}>{conversation.dropped} earlier items aren't shown</div>
          )}
          {conversation.error === null && rows.length === 0 && (
            <div className={styles.notice}>Nothing in the conversation yet.</div>
          )}
          {rows.map((row) => (
            <RowView key={row.kind === "tool" ? row.use.id : row.item.id} row={row} />
          ))}
          {status === "working" && (
            <div className={styles.working}>
              <CircularProgress size={12} />
              <span>{detail ?? "Working"}</span>
            </div>
          )}
          {status === "needsInput" && (
            <Alert severity="info" className={styles.waiting} action={<Button onClick={onShowTerminal}>Terminal</Button>}>
              Waiting for you. A permission prompt or question shows only in the terminal.
            </Alert>
          )}
        </div>
      </div>
      {behind && (
        <Button className={styles.jump} size="small" variant="contained" onClick={jumpToLatest}>
          Jump to latest
        </Button>
      )}
    </div>
  );
};
