import { Alert, Button, Snackbar } from "@mui/material";
import { useEffect, type JSX } from "react";
import { AgentsActions } from "./features/agents/AgentsActions";
import { EntryPane } from "./features/agents/EntryPane";
import { fetchEntries } from "./features/agents/agentsSlice";
import { Sidebar } from "./features/agents/Sidebar";
import { needsAttention } from "./features/agents/status";
import { NewAgentDialog } from "./features/create/NewAgentDialog";
import { NewShellDialog } from "./features/create/NewShellDialog";
import { Rail } from "./features/rail/Rail";
import { AddRepoDialog } from "./features/repos/AddRepoDialog";
import { ReposActions } from "./features/repos/ReposActions";
import { ReposList } from "./features/repos/ReposList";
import { ReposPane } from "./features/repos/ReposPane";
import { TerminalView } from "./features/terminals/TerminalView";
import { closeTerminal, shellKey } from "./features/terminals/terminalsSlice";
import { noticeDismissed, selected, type Section } from "./features/ui/uiSlice";
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

interface SectionView {
  readonly actions: () => JSX.Element;
  readonly list: () => JSX.Element;
  readonly pane: () => JSX.Element;
}

const agentsView: SectionView = { actions: AgentsActions, list: Sidebar, pane: Main };

/** Each section's top-bar actions, list, and detail pane (ADR 0031). */
const sectionViews: Readonly<Partial<Record<Section, SectionView>>> = {
  agents: agentsView,
  repos: { actions: ReposActions, list: ReposList, pane: ReposPane },
};

const Warnings = ({ section }: { readonly section: Section }): JSX.Element | null => {
  const warnings = useAppSelector((s) => (section === "repos" ? s.repos.warnings : s.agents.warnings));
  if (warnings.length === 0) {
    return null;
  }
  return (
    <Alert severity="warning" className={styles.warnings}>
      {warnings.join(" · ")}
    </Alert>
  );
};

export const App = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const section = useAppSelector((s) => s.ui.section);
  const attention = useAppSelector((s) => s.agents.entries.filter(needsAttention).length);
  const notice = useAppSelector((s) => s.ui.notice);
  const view = sectionViews[section] ?? agentsView;
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
        <view.actions />
      </header>
      <Warnings section={section} />
      <div className={styles.body}>
        <Rail />
        <aside className={styles.sidebar}>
          <view.list />
        </aside>
        <main className={styles.main}>
          <view.pane />
        </main>
      </div>
      <NewAgentDialog />
      <NewShellDialog />
      <AddRepoDialog />
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
