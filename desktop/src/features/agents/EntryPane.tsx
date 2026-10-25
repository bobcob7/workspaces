import {
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  ToggleButton,
  ToggleButtonGroup,
} from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import { api, type Entry } from "../../api";
import { repoLabel } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { ConversationView } from "../conversation/ConversationView";
import { ReplyBox } from "../terminals/ReplyBox";
import { TerminalView } from "../terminals/TerminalView";
import { attachKey, closeTerminal, openTerminal, shellKey, terminalShown } from "../terminals/terminalsSlice";
import { ownerRepo } from "../reviews/groups";
import { reviewSelected } from "../reviews/reviewsSlice";
import { agentViewChosen, sectionChosen, type AgentView } from "../ui/uiSlice";
import { removeEntry, stopAgent } from "./agentsSlice";
import { isLive } from "./status";
import { StatusDot } from "./StatusDot";
import styles from "./EntryPane.module.css";

export interface EntryPaneProps {
  readonly entry: Entry;
}

const terminalKeyFor = (entry: Entry): string | null => {
  if (entry.kind === "agent" && entry.sessionId !== null) {
    return attachKey(entry.sessionId);
  }
  if (entry.kind === "shell" && entry.workspaceId !== null) {
    return shellKey(entry.workspaceId);
  }
  return null;
};

const removeWarning = (entry: Entry): string =>
  entry.workspaceId === null
    ? `This deletes the Claude Code session ${entry.sessionId ?? ""}. Its transcript stays available through claude --resume.`
    : `This stops the agent and deletes ${entry.path}, including uncommitted changes, unpushed commits, and stashes.`;

export const EntryPane = ({ entry }: EntryPaneProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const termKey = terminalKeyFor(entry);
  const terminal = useAppSelector((s) => s.terminals.items.find((t) => t.key === termKey));
  const busy = useAppSelector((s) => s.agents.busy.includes(entry.key));
  const view = useAppSelector((s) => s.ui.agentView);
  const reviewing = useAppSelector((s) =>
    s.reviews.view?.prs.find((pr) => pr.reviews.some((r) => r.workspaceId === entry.workspaceId)),
  );
  const [confirming, setConfirming] = useState(false);
  const attach = (): void => {
    if (entry.sessionId === null) {
      return;
    }
    void dispatch(
      openTerminal({
        key: attachKey(entry.sessionId),
        title: entry.name,
        spec: { kind: "attach", sessionId: entry.sessionId, cwd: entry.path },
      }),
    );
  };
  // Attach when an agent that's running is selected, as agent view does.
  // A stopped one waits for Resume, since attaching restarts it.
  useEffect(() => {
    if (entry.kind === "agent" && isLive(entry.status) && entry.status !== "starting") {
      attach();
    }
  }, [entry.key]);
  const attached = terminal !== undefined && terminal.state !== "exited";
  const isAgent = entry.kind === "agent" && entry.sessionId !== null;
  const showConversation = isAgent && view === "conversation";
  // The conversation types into the hidden terminal, so it counts as shown
  // and isn't the one detached to make room for another.
  useEffect(() => {
    if (showConversation && termKey !== null && terminal !== undefined) {
      dispatch(terminalShown(termKey));
    }
  }, [showConversation, termKey, terminal?.generation]);
  return (
    <section className={styles.root} aria-label={entry.name}>
      <header className={styles.header}>
        <div className={styles.titleRow}>
          <h1 className={styles.title}>{entry.name}</h1>
          <StatusDot status={entry.status} withLabel />
          {entry.stale === true && (
            <Chip size="small" variant="outlined" label="old environment" title="Started before the background service; ws restart gives it the new environment" />
          )}
          <span className={styles.spacer} />
          {isAgent && (
            <ToggleButtonGroup
              size="small"
              exclusive
              value={view}
              onChange={(_event, value: AgentView | null) => {
                if (value !== null) {
                  dispatch(agentViewChosen(value));
                }
              }}
              aria-label="View"
            >
              <ToggleButton value="conversation">Conversation</ToggleButton>
              <ToggleButton value="terminal">Terminal</ToggleButton>
            </ToggleButtonGroup>
          )}
          {isAgent && !attached && (
            <Button size="small" variant="contained" onClick={attach}>
              {isLive(entry.status) ? "Attach" : "Resume"}
            </Button>
          )}
          {isAgent && attached && termKey !== null && (
            <Button size="small" onClick={() => void dispatch(closeTerminal(termKey))}>
              Detach
            </Button>
          )}
          {isAgent && isLive(entry.status) && (
            <Button size="small" disabled={busy} onClick={() => void dispatch(stopAgent(entry))}>
              Stop
            </Button>
          )}
          {entry.kind !== "shell" && entry.kind !== "unknown" && (
            <Button size="small" color="error" disabled={busy} onClick={() => setConfirming(true)}>
              Remove
            </Button>
          )}
        </div>
        <div className={styles.meta}>
          <button type="button" className={styles.path} title="Open in Finder" onClick={() => void api.openExternal(entry.path)}>
            {entry.path}
          </button>
          {entry.sessionId !== null && <span className={styles.mono}>session {entry.sessionId}</span>}
          {entry.repos.map((r) => (
            <Chip key={r.path} size="small" label={repoLabel(r.id, r.ref)} title={r.id} />
          ))}
          {reviewing !== undefined && (
            <Chip
              size="small"
              color="primary"
              variant="outlined"
              clickable
              label={`Reviewing ${ownerRepo(reviewing.repo)}#${reviewing.number}`}
              onClick={() => {
                dispatch(reviewSelected(reviewing.key));
                dispatch(sectionChosen("reviews"));
              }}
            />
          )}
        </div>
        {entry.detail !== null && <p className={styles.detail}>{entry.detail}</p>}
        {entry.intent !== null && (
          <details className={styles.intent}>
            <summary>Task</summary>
            <p>{entry.intent}</p>
          </details>
        )}
        {entry.result !== null && <p className={styles.result}>{entry.result}</p>}
        {entry.links.length > 0 && (
          <div className={styles.links}>
            {entry.links.map((link) => (
              <Chip
                key={link.href}
                size="small"
                color="primary"
                variant="outlined"
                clickable
                label={link.href.replace(/^https:\/\/github\.com\//, "")}
                onClick={() => void api.openExternal(link.href)}
              />
            ))}
          </div>
        )}
      </header>
      {showConversation && entry.sessionId !== null ? (
        <>
          <ConversationView
            sessionId={entry.sessionId}
            status={entry.status}
            detail={entry.detail}
            onShowTerminal={() => dispatch(agentViewChosen("terminal"))}
          />
          {terminal !== undefined && termKey !== null && <ReplyBox terminalKey={termKey} disabled={!attached} />}
        </>
      ) : terminal !== undefined && termKey !== null ? (
        <>
          <TerminalView terminalKey={termKey} generation={terminal.generation} />
          {isAgent && <ReplyBox terminalKey={termKey} disabled={!attached} />}
        </>
      ) : (
        <div className={styles.placeholder}>
          {entry.kind === "shell" && "This shell runs in another terminal."}
          {entry.kind === "pending" && "ws is still handing this workspace to its session."}
          {entry.kind === "unknown" && "This directory has no ws state; ws never deletes it."}
          {isAgent && !isLive(entry.status) && "The agent is stopped. Resume attaches to it and restarts it."}
          {isAgent && entry.status === "starting" && "The agent is starting."}
        </div>
      )}
      <Dialog open={confirming} onClose={() => setConfirming(false)}>
        <DialogTitle>Remove {entry.name}?</DialogTitle>
        <DialogContent>
          <DialogContentText>{removeWarning(entry)}</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirming(false)}>Cancel</Button>
          <Button
            color="error"
            variant="contained"
            onClick={() => {
              setConfirming(false);
              void dispatch(removeEntry(entry));
            }}
          >
            Remove
          </Button>
        </DialogActions>
      </Dialog>
    </section>
  );
};
