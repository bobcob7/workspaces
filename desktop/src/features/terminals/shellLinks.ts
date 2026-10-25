import { createListenerMiddleware, type ThunkDispatch, type UnknownAction } from "@reduxjs/toolkit";
import type { Entry } from "../../api";
import type { RootState, ThunkExtra } from "../../store";
import { fetchEntries } from "../agents/agentsSlice";
import { selected } from "../ui/uiSlice";
import { shellsLinked, type ShellLink, type TerminalInfo } from "./terminalsSlice";

/** How long before a terminal opened its workspace may say it was created: ws records whole seconds. */
const createdSlackMs = 2000;

const isUnlinkedShell = (t: TerminalInfo): boolean => t.spec.kind === "shell" && t.workspaceId === null;

/** The display name ws gives a shell opened with this --name: the name, or for none, the id. */
const namedAs = (t: TerminalInfo, e: Entry): boolean => {
  const name = t.spec.kind === "shell" ? t.spec.name.trim() : "";
  return name === "" ? e.name === e.workspaceId : e.name === name;
};

const createdMs = (e: Entry): number => (e.createdAt === null ? Number.NaN : Date.parse(e.createdAt));

/**
 * Pairs each shell terminal the app opened with the workspace its ws shell
 * made: a shell workspace no other terminal has, created after the terminal
 * opened, under the name it was given. The oldest terminal is paired first,
 * with the oldest such workspace.
 */
export const shellLinks = (terminals: readonly TerminalInfo[], entries: readonly Entry[]): readonly ShellLink[] => {
  const taken = new Set(terminals.flatMap((t) => (t.workspaceId === null ? [] : [t.workspaceId])));
  const candidates = entries
    .filter((e) => e.workspaceId !== null && (e.kind === "shell" || e.kind === "pending"))
    .toSorted((a, b) => createdMs(a) - createdMs(b));
  const links: ShellLink[] = [];
  for (const t of terminals.filter(isUnlinkedShell).toSorted((a, b) => a.openedAt - b.openedAt)) {
    const match = candidates.find(
      (e) =>
        e.workspaceId !== null &&
        !taken.has(e.workspaceId) &&
        createdMs(e) >= t.openedAt - createdSlackMs &&
        namedAs(t, e),
    );
    if (match?.workspaceId == null) {
      continue;
    }
    taken.add(match.workspaceId);
    links.push({ key: t.key, workspaceId: match.workspaceId });
  }
  return links;
};

type Dispatch = ThunkDispatch<RootState, ThunkExtra, UnknownAction>;

/**
 * Links new shell terminals to their workspaces as the listing shows them,
 * and moves the selection from the terminal to its workspace.
 */
export const shellLinkListener = createListenerMiddleware<RootState, Dispatch>();

shellLinkListener.startListening({
  actionCreator: fetchEntries.fulfilled,
  effect: (action, { dispatch, getState }) => {
    const links = shellLinks(getState().terminals.items, action.payload.entries);
    if (links.length === 0) {
      return;
    }
    dispatch(shellsLinked(links));
    const current = getState().ui.selectedKey;
    const moved = links.find((l) => l.key === current);
    if (moved !== undefined) {
      dispatch(selected(moved.workspaceId));
    }
  },
});
