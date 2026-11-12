import {
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  Menu,
  MenuItem,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  useMediaQuery,
} from "@mui/material";
import { useEffect, useState, type FormEvent, type JSX } from "react";
import { api, type Entry, type LoopAttention, type OpenWith } from "../../api";
import { repoLabel } from "../../format";
import { isWeb } from "../../platform";
import { useAppDispatch, useAppSelector } from "../../store";
import { ConversationView } from "../conversation/ConversationView";
import { ReplyBox } from "../terminals/ReplyBox";
import { TerminalView } from "../terminals/TerminalView";
import { attachKey, closeTerminal, openTerminal, terminalShown, type TerminalInfo } from "../terminals/terminalsSlice";
import { ownerRepo } from "../reviews/groups";
import { reviewSelected } from "../reviews/reviewsSlice";
import { agentViewChosen, sectionChosen, selected, type AgentView } from "../ui/uiSlice";
import { editorDir, openWorkspace, renameEntry, setEntryHidden, stopAgent } from "./agentsSlice";
import { ChildrenCard } from "./ChildrenCard";
import { RemoveDialog } from "./RemoveDialog";
import { isLive } from "./status";
import { StatusDot } from "./StatusDot";
import styles from "./EntryPane.module.css";

const PHONE_QUERY = "(max-width:720px)";

export interface EntryPaneProps {
  readonly entry: Entry;
}

/** The terminal shown for an entry: its agent's attach terminal, or the shell terminal linked to its workspace. */
const terminalFor = (items: readonly TerminalInfo[], entry: Entry): TerminalInfo | undefined => {
  if (entry.kind === "agent" && entry.sessionId !== null) {
    const key = attachKey(entry.sessionId);
    return items.find((t) => t.key === key);
  }
  if (entry.kind === "shell" && entry.workspaceId !== null) {
    return items.find((t) => t.spec.kind === "shell" && t.workspaceId === entry.workspaceId);
  }
  return undefined;
};

const attentionLabel = (attention: LoopAttention): string => (attention === "blocked" ? "Blocked" : "Needs attention");

const EyeOffIcon = (): JSX.Element => (
  <svg
    width="14"
    height="14"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M10.6 5.1A10.6 10.6 0 0 1 12 5c6.4 0 10 7 10 7a17.6 17.6 0 0 1-2.8 3.7M6.6 6.6C3.7 8.4 2 12 2 12s3.6 7 10 7a9.6 9.6 0 0 0 5.4-1.6" />
    <path d="M9.9 9.9a3 3 0 0 0 4.2 4.2" />
    <path d="m3 3 18 18" />
  </svg>
);

const FolderIcon = (): JSX.Element => (
  <svg
    width="14"
    height="14"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
  </svg>
);

const CodeIcon = (): JSX.Element => (
  <svg
    width="14"
    height="14"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="m8 7-5 5 5 5" />
    <path d="m16 7 5 5-5 5" />
  </svg>
);

interface RenameDialogProps {
  readonly entry: Entry;
  readonly open: boolean;
  readonly onClose: () => void;
}

/** Changes a workspace's display name with ws rename; its id and directory stay (ADR 0036). */
const RenameDialog = ({ entry, open, onClose }: RenameDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [name, setName] = useState(entry.name);
  useEffect(() => {
    if (open) {
      setName(entry.name);
    }
  }, [open]);
  const trimmed = name.trim();
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    if (trimmed === "" || trimmed === entry.name) {
      onClose();
      return;
    }
    void dispatch(renameEntry({ entry, name: trimmed }));
    onClose();
  };
  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="xs">
      <form onSubmit={submit}>
        <DialogTitle>Rename workspace</DialogTitle>
        <DialogContent>
          <TextField
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            helperText={`The directory stays ${entry.workspaceId ?? ""}.`}
            fullWidth
            autoFocus
            margin="dense"
          />
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="contained" disabled={trimmed === ""}>
            Rename
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
};

export const EntryPane = ({ entry }: EntryPaneProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const terminal = useAppSelector((s) => terminalFor(s.terminals.items, entry));
  const termKey = terminal?.key ?? (entry.kind === "agent" && entry.sessionId !== null ? attachKey(entry.sessionId) : null);
  const busy = useAppSelector((s) => s.agents.busy.includes(entry.key));
  const view = useAppSelector((s) => s.ui.agentView);
  const reviewing = useAppSelector((s) =>
    s.reviews.view?.prs.find((pr) => pr.reviews.some((r) => r.workspaceId === entry.workspaceId)),
  );
  const parent = useAppSelector((s) =>
    entry.parentId === null ? undefined : s.agents.entries.find((e) => e.workspaceId === entry.parentId),
  );
  const phone = useMediaQuery(PHONE_QUERY);
  const [menuAnchor, setMenuAnchor] = useState<HTMLElement | null>(null);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [opening, setOpening] = useState<OpenWith | null>(null);
  const openIn = (openWith: OpenWith): void => {
    setOpening(openWith);
    void dispatch(openWorkspace({ entry, with: openWith })).finally(() => setOpening(null));
  };
  const renamable = entry.workspaceId !== null && entry.kind !== "unknown";
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
  const removable = entry.kind !== "shell" && entry.kind !== "unknown";
  const loopRun = entry.loopRun;
  const hideable = renamable && !loopRun?.attention;
  const closeMenu = (): void => setMenuAnchor(null);
  const fromMenu = (action: () => void) => (): void => {
    closeMenu();
    action();
  };
  const canStop = isAgent && isLive(entry.status);
  const canDetach = isAgent && attached && termKey !== null;
  const hasMenu = canDetach || canStop || renamable || removable || !isWeb();
  const viewToggle = isAgent && (
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
  );
  const attachButton = isAgent && !attached && (
    <Button size="small" variant="contained" onClick={attach}>
      {isLive(entry.status) ? "Attach" : "Resume"}
    </Button>
  );
  return (
    <section className={styles.root} aria-label={entry.name}>
      <header className={`${styles.header} ${phone ? styles.compact : ""}`}>
        <div className={styles.titleRow}>
          <h1 className={styles.title}>{entry.name}</h1>
          <StatusDot status={entry.status} withLabel={!phone} />
          {!phone && entry.stale === true && (
            <Chip size="small" variant="outlined" label="old environment" title="Started before the background service; ws restart gives it the new environment" />
          )}
          <span className={styles.spacer} />
          {phone ? (
            <>
              {viewToggle}
              {attachButton}
              {hasMenu && (
                <IconButton size="small" aria-label="More actions" onClick={(e) => setMenuAnchor(e.currentTarget)}>
                  <span aria-hidden="true">⋯</span>
                </IconButton>
              )}
              <IconButton
                size="small"
                aria-label="Details"
                aria-expanded={detailsOpen}
                onClick={() => setDetailsOpen((o) => !o)}
              >
                <span aria-hidden="true">{detailsOpen ? "▴" : "▾"}</span>
              </IconButton>
              <Menu anchorEl={menuAnchor} open={menuAnchor !== null} onClose={closeMenu}>
                {!isWeb() && (
                  <MenuItem
                    disabled={entry.kind === "pending" || busy || opening === "fileManager"}
                    onClick={fromMenu(() => openIn("fileManager"))}
                  >
                    Open folder
                  </MenuItem>
                )}
                {!isWeb() && (
                  <MenuItem
                    disabled={entry.kind === "pending" || busy || opening === "editor"}
                    onClick={fromMenu(() => openIn("editor"))}
                  >
                    Open in editor
                  </MenuItem>
                )}
                {canDetach && <MenuItem onClick={fromMenu(() => void dispatch(closeTerminal(termKey)))}>Detach</MenuItem>}
                {canStop && (
                  <MenuItem disabled={busy} onClick={fromMenu(() => void dispatch(stopAgent(entry)))}>
                    Stop
                  </MenuItem>
                )}
                {renamable && (
                  <MenuItem disabled={busy} onClick={fromMenu(() => setRenaming(true))}>
                    Rename
                  </MenuItem>
                )}
                {hideable && (
                  <MenuItem
                    disabled={busy}
                    onClick={fromMenu(() => void dispatch(setEntryHidden({ entry, hidden: !entry.hidden })))}
                  >
                    {entry.hidden ? "Unhide" : "Hide"}
                  </MenuItem>
                )}
                {removable && (
                  <MenuItem disabled={busy} onClick={fromMenu(() => setConfirming(true))}>
                    Remove
                  </MenuItem>
                )}
              </Menu>
            </>
          ) : (
            <>
          {!isWeb() && (
            <>
              <Button
                size="small"
                startIcon={<FolderIcon />}
                title={`Open ${entry.path} in the file manager (Settings › Workspaces)`}
                disabled={entry.kind === "pending" || busy || opening === "fileManager"}
                onClick={() => openIn("fileManager")}
              >
                Open folder
              </Button>
              <Button
                size="small"
                startIcon={<CodeIcon />}
                title={`Open ${editorDir(entry)} in the editor (Settings › Workspaces)`}
                disabled={entry.kind === "pending" || busy || opening === "editor"}
                onClick={() => openIn("editor")}
              >
                Open in editor
              </Button>
            </>
          )}
          {viewToggle}
          {attachButton}
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
          {renamable && (
            <Button size="small" disabled={busy} onClick={() => setRenaming(true)}>
              Rename
            </Button>
          )}
          {hideable && (
            <Button
              size="small"
              disabled={busy}
              onClick={() => void dispatch(setEntryHidden({ entry, hidden: !entry.hidden }))}
            >
              {entry.hidden ? "Unhide" : "Hide"}
            </Button>
          )}
          {entry.kind !== "shell" && entry.kind !== "unknown" && (
            <Button size="small" color="error" disabled={busy} onClick={() => setConfirming(true)}>
              Remove
            </Button>
          )}
            </>
          )}
        </div>
        <div className={phone && !detailsOpen ? styles.collapsed : undefined}>
        {phone && entry.stale === true && (
          <Chip size="small" variant="outlined" label="old environment" title="Started before the background service; ws restart gives it the new environment" />
        )}
        <div className={styles.meta}>
          <button
            type="button"
            className={styles.path}
            title="Open in the file manager"
            onClick={() => void dispatch(openWorkspace({ entry, with: "fileManager" }))}
          >
            {entry.path}
          </button>
          {entry.sessionId !== null && <span className={styles.mono}>session {entry.sessionId}</span>}
          {entry.repos.map((r) => (
            <Chip key={r.path} size="small" label={repoLabel(r.id, r.ref)} title={r.id} />
          ))}
          {parent !== undefined && (
            <Chip
              size="small"
              variant="outlined"
              clickable
              label={`↳ ${parent.name}`}
              title="Started from this workspace, and removed with it"
              onClick={() => dispatch(selected(parent.key))}
            />
          )}
          {loopRun !== null && loopRun.attention !== null && (
            <Chip
              size="small"
              color="warning"
              label={attentionLabel(loopRun.attention)}
              title={`A run of loop ${loopRun.loop}; shown until it's resolved`}
            />
          )}
          {loopRun !== null && <Chip size="small" variant="outlined" label={`Loop ${loopRun.loop}`} />}
          {entry.hidden && (
            <Chip
              size="small"
              icon={<EyeOffIcon />}
              label="Hidden"
              title="Left out of the Agents list's groups and the waiting count; Unhide shows it"
            />
          )}
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
        </div>
      </header>
      <ChildrenCard entry={entry} />
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
      {renamable && <RenameDialog entry={entry} open={renaming} onClose={() => setRenaming(false)} />}
      <RemoveDialog entry={entry} open={confirming} onClose={() => setConfirming(false)} />
    </section>
  );
};
