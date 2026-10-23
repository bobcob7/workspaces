import { Alert, Button, CircularProgress, Snackbar } from "@mui/material";
import { useEffect, type JSX } from "react";
import { EntryPane } from "./features/agents/EntryPane";
import { fetchEntries, restoreAgents } from "./features/agents/agentsSlice";
import { Sidebar } from "./features/agents/Sidebar";
import { needsAttention } from "./features/agents/status";
import { NewAgentDialog } from "./features/create/NewAgentDialog";
import { NewShellDialog } from "./features/create/NewShellDialog";
import { TerminalView } from "./features/terminals/TerminalView";
import { closeTerminal, shellKey } from "./features/terminals/terminalsSlice";
import { dialogOpened, noticeDismissed, selected } from "./features/ui/uiSlice";
import { useAppDispatch, useAppSelector } from "./store";
import styles from "./App.module.css";

/** How often the list is refreshed. Each refresh runs ws list and claude agents. */
export const pollInterval = 4000;

const OrphanShell = ({ id }: { readonly id: string }): JSX.Element => {
  const dispatch = useAppDispatch();
  const terminal = useAppSelector((s) => s.terminals.items.find((t) => t.key === shellKey(id)));
  if (terminal === undefined) {
    return <div className={styles.empty}>Select a workspace.</div>;
  }
  return (
    <section className={styles.orphan} aria-label={id}>
      <header className={styles.orphanHeader}>
        <h1>{id}</h1>
        <span className={styles.muted}>{terminal.state === "exited" ? "shell exited; workspace removed" : "shell"}</span>
        <span className={styles.spacer} />
        {terminal.state === "exited" && (
          <Button
            size="small"
            onClick={() => {
              void dispatch(closeTerminal(terminal.key));
              dispatch(selected(null));
            }}
          >
            Close
          </Button>
        )}
      </header>
      <TerminalView terminalKey={terminal.key} generation={terminal.generation} />
    </section>
  );
};

const Main = (): JSX.Element => {
  const selectedKey = useAppSelector((s) => s.ui.selectedKey);
  const entry = useAppSelector((s) => s.agents.entries.find((e) => e.key === selectedKey));
  const error = useAppSelector((s) => s.agents.error);
  if (entry !== undefined) {
    return <EntryPane key={entry.key} entry={entry} />;
  }
  if (selectedKey !== null) {
    return <OrphanShell id={selectedKey} />;
  }
  if (error !== null) {
    return (
      <div className={styles.empty}>
        <Alert severity="error">
          {error.message}
          {error.code === "ws_missing" && <div>Install ws (make build in this repo) and put it on your PATH.</div>}
        </Alert>
      </div>
    );
  }
  return <div className={styles.empty}>Select a workspace, or start a new agent.</div>;
};

export const App = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const loading = useAppSelector((s) => s.agents.loading);
  const warnings = useAppSelector((s) => s.agents.warnings);
  const attention = useAppSelector((s) => s.agents.entries.filter(needsAttention).length);
  const notice = useAppSelector((s) => s.ui.notice);
  useEffect(() => {
    void dispatch(fetchEntries());
    const timer = window.setInterval(() => void dispatch(fetchEntries()), pollInterval);
    return () => window.clearInterval(timer);
  }, [dispatch]);
  useEffect(() => {
    document.title = attention > 0 ? `Workspaces (${attention} need${attention === 1 ? "s" : ""} input)` : "Workspaces";
  }, [attention]);
  return (
    <div className={styles.root}>
      <header className={styles.topBar}>
        <span className={styles.brand}>Workspaces</span>
        {attention > 0 && <span className={styles.badge}>{attention} waiting for you</span>}
        <span className={styles.spacer} />
        {loading && <CircularProgress size={14} aria-label="Refreshing" />}
        <Button size="small" onClick={() => void dispatch(restoreAgents())}>
          Restore stopped
        </Button>
        <Button size="small" variant="outlined" onClick={() => dispatch(dialogOpened("shell"))}>
          New shell
        </Button>
        <Button size="small" variant="contained" onClick={() => dispatch(dialogOpened("agent"))}>
          New agent
        </Button>
      </header>
      {warnings.length > 0 && (
        <Alert severity="warning" className={styles.warnings}>
          {warnings.join(" · ")}
        </Alert>
      )}
      <div className={styles.body}>
        <aside className={styles.sidebar}>
          <Sidebar />
        </aside>
        <main className={styles.main}>
          <Main />
        </main>
      </div>
      <NewAgentDialog />
      <NewShellDialog />
      <Snackbar
        open={notice !== null}
        autoHideDuration={notice?.severity === "error" ? 8000 : 4000}
        onClose={() => dispatch(noticeDismissed())}
        anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
      >
        <Alert severity={notice?.severity ?? "info"} onClose={() => dispatch(noticeDismissed())} variant="filled">
          {notice?.message}
        </Alert>
      </Snackbar>
    </div>
  );
};
